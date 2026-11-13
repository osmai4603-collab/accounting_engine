package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
)

func TestPostgresRLSBlocksCrossCompanyEntryReads(t *testing.T) {
	ctx := context.Background()
	admin, err := newIsolatedPostgresDriver(t, ctx)
	if err != nil {
		t.Skipf("isolated PostgreSQL schema unavailable: %v", err)
	}

	companyA, companyB, journalID, entryID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, companyID := range []uuid.UUID{companyA, companyB} {
		if err := admin.CreateCompany(ctx, masterdata.Company{
			ID: companyID, Code: fmt.Sprintf("RLS-%s", companyID.String()[:8]),
			LegalName: "RLS Isolation Test", FunctionalCurrency: "USD",
			PresentationCurrency: "USD", Active: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := admin.WithCompanyTx(ctx, companyA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO journals(id,company_id,code) VALUES($1,$2,$3)`,
			journalID, companyA, "RLS-"+journalID.String()[:8])
		return err
	}); err != nil {
		t.Fatal(err)
	}
	entryDate := time.Now().UTC()
	if err := admin.WithCompanyTx(ctx, companyA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO account_moves(
			id,company_id,journal_id,entry_date,status,entry
		) VALUES($1,$2,$3,$4,$5,$6)`,
			entryID, companyA, journalID, entryDate, posting.EntryDraft,
			`{"ID":"`+entryID.String()+`","CompanyID":"`+companyA.String()+`","Status":"draft"}`)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	var currentUser string
	if err := admin.Pool().QueryRow(ctx, "SELECT current_user").Scan(&currentUser); err != nil {
		t.Fatal(err)
	}
	var schemaName string
	if err := admin.Pool().QueryRow(ctx, "SELECT current_schema()").Scan(&schemaName); err != nil {
		t.Fatal(err)
	}
	roleName := "test_app_" + uuid.NewString()
	roleIdentifier := pgx.Identifier{roleName}.Sanitize()
	if _, err := admin.Pool().Exec(ctx, "CREATE ROLE "+roleIdentifier+" NOLOGIN NOBYPASSRLS"); err != nil {
		t.Skipf("PostgreSQL role creation unavailable for isolated RLS test: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.WithoutCancel(ctx)
		if _, err := admin.Pool().Exec(cleanupCtx, "DROP OWNED BY "+roleIdentifier); err != nil {
			t.Errorf("drop temporary RLS role privileges: %v", err)
		}
		if _, err := admin.Pool().Exec(cleanupCtx,
			"REVOKE "+roleIdentifier+" FROM "+pgx.Identifier{currentUser}.Sanitize()); err != nil {
			t.Errorf("revoke temporary RLS role: %v", err)
		}
		if _, err := admin.Pool().Exec(cleanupCtx, "DROP ROLE "+roleIdentifier); err != nil {
			t.Errorf("drop temporary RLS role: %v", err)
		}
	})
	if _, err := admin.Pool().Exec(ctx, "GRANT "+roleIdentifier+" TO "+pgx.Identifier{currentUser}.Sanitize()); err != nil {
		t.Fatalf("grant temporary RLS role to test connection: %v", err)
	}
	if _, err := admin.Pool().Exec(ctx, "GRANT USAGE ON SCHEMA "+pgx.Identifier{schemaName}.Sanitize()+" TO "+roleIdentifier); err != nil {
		t.Fatalf("grant schema access to temporary RLS role: %v", err)
	}
	if _, err := admin.Pool().Exec(ctx, "GRANT SELECT ON "+
		pgx.Identifier{schemaName, "account_moves"}.Sanitize()+" TO "+roleIdentifier); err != nil {
		t.Fatalf("grant ledger read access to temporary RLS role: %v", err)
	}
	appTx, err := admin.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer appTx.Rollback(context.WithoutCancel(ctx))
	if _, err := appTx.Exec(ctx, "SET LOCAL ROLE "+roleIdentifier); err != nil {
		t.Fatalf("assume temporary RLS role: %v", err)
	}
	var bypassesRLS, ownsLedger bool
	if err := appTx.QueryRow(ctx, `SELECT role.rolbypassrls,
		pg_get_userbyid(ledger_table.relowner)=current_user
		FROM pg_roles role
		CROSS JOIN pg_class ledger_table
		WHERE role.rolname=current_user AND ledger_table.oid='account_moves'::regclass`).
		Scan(&bypassesRLS, &ownsLedger); err != nil {
		t.Fatal(err)
	}
	if bypassesRLS || ownsLedger {
		t.Fatalf("temporary RLS role must be a non-owner without BYPASSRLS (bypass=%v, owner=%v)", bypassesRLS, ownsLedger)
	}
	if _, err := appTx.Exec(ctx, `SELECT set_config('app.current_company_id',$1,true)`, companyB.String()); err != nil {
		t.Fatal(err)
	}
	var data []byte
	err = appTx.QueryRow(ctx, `SELECT entry FROM account_moves WHERE id=$1`, entryID).Scan(&data)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-company entry lookup error = %v, want no rows", err)
	}
}

// TestPostgresRejectsCrossCompanyAccountInPosting proves tenant isolation at
// the storage boundary: a journal entry that belongs to company A but carries
// an account owned by company B is rejected by the composite foreign keys
// (and the tenant RLS policies behind them) the moment it is written, so no
// draft, no lines, and no audit trace of the poisoned entry can ever exist.
func TestPostgresRejectsCrossCompanyAccountInPosting(t *testing.T) {
	ctx := context.Background()
	admin, err := newIsolatedPostgresDriver(t, ctx)
	if err != nil {
		t.Skipf("isolated PostgreSQL schema unavailable: %v", err)
	}

	companyA, companyB := uuid.New(), uuid.New()
	for _, company := range []struct {
		id   uuid.UUID
		code string
	}{
		{id: companyA, code: "ISOL-A"},
		{id: companyB, code: "ISOL-B"},
	} {
		if err := admin.CreateCompany(ctx, masterdata.Company{
			ID: company.id, Code: fmt.Sprintf("%s-%s", company.code, company.id.String()[:8]),
			LegalName: "Cross-Company Isolation Test", FunctionalCurrency: "USD",
			PresentationCurrency: "USD", Active: true,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Company B owns the "foreign" account.
	foreignAccountID := uuid.New()
	if err := admin.CreateAccount(ctx, masterdata.Account{
		CompanyID: companyB, ID: foreignAccountID, Code: "9999", Name: "Company B Account",
		AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit,
		IsActive: true,
	}); err != nil {
		t.Fatal(err)
	}

	// Company A has its own journal and cash account.
	journalID, cashAccountID := uuid.New(), uuid.New()
	if err := admin.WithCompanyTx(ctx, companyA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO journals(id,company_id,code) VALUES($1,$2,$3)`,
			journalID, companyA, "XA-"+journalID.String()[:8])
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := admin.CreateAccount(ctx, masterdata.Account{
		CompanyID: companyA, ID: cashAccountID, Code: "1000", Name: "Company A Cash",
		AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit,
		IsActive: true,
	}); err != nil {
		t.Fatal(err)
	}
	year := masterdata.GenerateFiscalYear(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = companyA
	if err := admin.CreateFiscalYear(ctx, year); err != nil {
		t.Fatal(err)
	}

	entryID := uuid.New()
	entryDate := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)
	amount, err := financialtypes.NewMoneyFromString("500.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	zero, _ := financialtypes.Zero("USD")

	// 1) Company A cannot even read company B's account through its scoped
	// repository: RLS hides the row from the tenant context.
	if _, err := admin.GetAccountByIDForCompany(ctx, companyA, foreignAccountID); !errors.Is(err, masterdata.ErrAccountNotFound) {
		t.Fatalf("cross-company account read error = %v, want account not found", err)
	}

	// 2) Saving a company A draft that references company B's account must be
	// rejected by the composite foreign key (company_id, account_id).
	entry, err := posting.NewJournalEntry(journalID, entryDate, "maker", []posting.JournalEntryLine{
		{CompanyID: companyA, ID: uuid.New(), AccountID: foreignAccountID, Debit: amount, Credit: zero},
		{CompanyID: companyA, ID: uuid.New(), AccountID: cashAccountID, Debit: zero, Credit: amount},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry.CompanyID = companyA
	entry.ID = entryID
	entry.PeriodID = year.Periods[3].ID
	saveErr := admin.SaveDraft(ctx, entry)
	if saveErr == nil {
		t.Fatal("expected the cross-company draft to be rejected")
	}
	var pgErr *pgconn.PgError
	if !errors.As(saveErr, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("cross-company draft error = %v, want foreign key violation (SQLSTATE 23503)", saveErr)
	}

	// 3) A raw SQL attempt to smuggle the same cross-company line into the
	// ledger is rejected at the same boundary even before posting: first a
	// legitimate draft move shell for company A is inserted, then attaching
	// company B's account to it must trip the composite foreign key.
	rawErr := admin.WithCompanyTx(ctx, companyA, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO account_moves(id,company_id,journal_id,period_id,entry_date,status,entry)
			VALUES($1,$2,$3,$4,$5,'draft','{}')`,
			entryID, companyA, journalID, year.Periods[3].ID, entryDate); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO account_move_lines(
			move_id,company_id,line_id,account_id,debit,credit,amount_currency,currency_id,
			transaction_amount_minor,transaction_currency,exchange_rate,
			functional_debit_minor,functional_credit_minor,line_data)
			VALUES($1,$2,$3,$4,$5,0,$5,'USD',50000,'USD',1,50000,0,'{}')`,
			entryID, companyA, uuid.New(), foreignAccountID, amount.Amount())
		return err
	})
	if !errors.As(rawErr, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("raw cross-company line insert error = %v, want foreign key violation (SQLSTATE 23503)", rawErr)
	}

	// 4) Zero residue: the rejected entry left no trace whatsoever.
	var moves, lines, audit int
	if err := admin.Pool().QueryRow(ctx,
		`SELECT (SELECT COUNT(*) FROM account_moves WHERE id=$1),
		        (SELECT COUNT(*) FROM account_move_lines WHERE move_id=$1),
		        (SELECT COUNT(*) FROM financial_audit_logs WHERE entity_id=$1)`,
		entryID).Scan(&moves, &lines, &audit); err != nil {
		t.Fatal(err)
	}
	if moves != 0 || lines != 0 || audit != 0 {
		t.Fatalf("cross-company residue: moves=%d lines=%d audit=%d, want all zero", moves, lines, audit)
	}
	var sequence int64
	if err := admin.Pool().QueryRow(ctx,
		`SELECT current_sequence FROM journals WHERE company_id=$1 AND id=$2`,
		companyA, journalID).Scan(&sequence); err != nil {
		t.Fatal(err)
	}
	if sequence != 0 {
		t.Fatalf("journal sequence after rejected posting = %d, want 0", sequence)
	}

	// 5) The two tenants stay fully isolated: a legitimate company A entry
	// posts normally and is invisible from company B's scope.
	entry, err = posting.NewJournalEntry(journalID, entryDate, "maker", []posting.JournalEntryLine{
		{CompanyID: companyA, ID: uuid.New(), AccountID: cashAccountID, Debit: amount, Credit: zero},
		{CompanyID: companyA, ID: uuid.New(), AccountID: cashAccountID, Debit: zero, Credit: amount},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry.CompanyID = companyA
	entry.PeriodID = year.Periods[3].ID
	if err := admin.SaveDraft(ctx, entry); err != nil {
		t.Fatal(err)
	}
	entry.Status = posting.EntryPosted
	if _, err := admin.PostAndSequence(ctx, entry); err != nil {
		t.Fatalf("legitimate same-company posting failed: %v", err)
	}

	// The two tenants stay fully isolated on the read path as well. The
	// driver connection is a superuser (RLS never applies to superusers even
	// with FORCE ROW LEVEL SECURITY), so isolation is proven through a
	// temporary non-owner role bound to company B's tenant context.
	var currentUser, schemaName string
	if err := admin.Pool().QueryRow(ctx, "SELECT current_user").Scan(&currentUser); err != nil {
		t.Fatal(err)
	}
	if err := admin.Pool().QueryRow(ctx, "SELECT current_schema()").Scan(&schemaName); err != nil {
		t.Fatal(err)
	}
	roleName := "test_isol_" + uuid.NewString()
	roleIdentifier := pgx.Identifier{roleName}.Sanitize()
	if _, err := admin.Pool().Exec(ctx, "CREATE ROLE "+roleIdentifier+" NOLOGIN NOBYPASSRLS"); err != nil {
		t.Skipf("PostgreSQL role creation unavailable for isolation test: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.WithoutCancel(ctx)
		if _, err := admin.Pool().Exec(cleanupCtx, "DROP OWNED BY "+roleIdentifier); err != nil {
			t.Errorf("drop temporary isolation role privileges: %v", err)
		}
		if _, err := admin.Pool().Exec(cleanupCtx,
			"REVOKE "+roleIdentifier+" FROM "+pgx.Identifier{currentUser}.Sanitize()); err != nil {
			t.Errorf("revoke temporary isolation role: %v", err)
		}
		if _, err := admin.Pool().Exec(cleanupCtx, "DROP ROLE "+roleIdentifier); err != nil {
			t.Errorf("drop temporary isolation role: %v", err)
		}
	})
	if _, err := admin.Pool().Exec(ctx, "GRANT "+roleIdentifier+" TO "+pgx.Identifier{currentUser}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Pool().Exec(ctx,
		"GRANT USAGE ON SCHEMA "+pgx.Identifier{schemaName}.Sanitize()+" TO "+roleIdentifier); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Pool().Exec(ctx, "GRANT SELECT ON "+
		pgx.Identifier{schemaName, "account_moves"}.Sanitize()+" TO "+roleIdentifier); err != nil {
		t.Fatal(err)
	}
	isolationTx, err := admin.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer isolationTx.Rollback(context.WithoutCancel(ctx))
	if _, err := isolationTx.Exec(ctx, "SET LOCAL ROLE "+roleIdentifier); err != nil {
		t.Fatal(err)
	}
	if _, err := isolationTx.Exec(ctx, `SELECT set_config('app.current_company_id',$1,true)`, companyB.String()); err != nil {
		t.Fatal(err)
	}
	var visibleFromB int
	if err := isolationTx.QueryRow(ctx,
		`SELECT COUNT(*) FROM account_moves WHERE company_id=$1`, companyA).Scan(&visibleFromB); err != nil {
		t.Fatal(err)
	}
	if visibleFromB != 0 {
		t.Fatalf("company B sees %d company A moves, want 0", visibleFromB)
	}
}
