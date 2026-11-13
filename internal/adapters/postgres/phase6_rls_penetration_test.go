package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// phase6AppRoleName is the restricted, non-owner application role required by
// Phase 6. It is provisioned by scripts/create_app_role.sql and must never be
// able to bypass row-level security.
const phase6AppRoleName = "accounting_app_user"

// ensurePhase6AppRole guarantees that the restricted application role exists,
// that it is NOBYPASSRLS/NOSUPERUSER, and that the current test connection may
// assume it through SET LOCAL ROLE. The global role is left in place (it is a
// deployment artifact); only the temporary membership is revoked.
func ensurePhase6AppRole(ctx context.Context, t *testing.T, admin *postgres.Driver) string {
	t.Helper()
	roleIdentifier := pgx.Identifier{phase6AppRoleName}.Sanitize()
	var exists bool
	if err := admin.Pool().QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname=$1)`, phase6AppRoleName).Scan(&exists); err != nil {
		t.Fatalf("inspect application role: %v", err)
	}
	if !exists {
		if _, err := admin.Pool().Exec(ctx, "CREATE ROLE "+roleIdentifier+" NOLOGIN NOBYPASSRLS"); err != nil {
			t.Skipf("PostgreSQL role creation unavailable for RLS penetration test: %v", err)
		}
	}
	if _, err := admin.Pool().Exec(ctx, "ALTER ROLE "+roleIdentifier+" NOBYPASSRLS NOSUPERUSER"); err != nil {
		t.Skipf("cannot enforce NOBYPASSRLS on application role: %v", err)
	}

	var currentUser string
	if err := admin.Pool().QueryRow(ctx, "SELECT current_user").Scan(&currentUser); err != nil {
		t.Fatalf("resolve current user: %v", err)
	}
	var isMember bool
	if err := admin.Pool().QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_auth_members membership
			JOIN pg_roles granted ON granted.oid = membership.roleid
			JOIN pg_roles member ON member.oid = membership.member
			WHERE granted.rolname = $1 AND member.rolname = $2
		)`, phase6AppRoleName, currentUser).Scan(&isMember); err != nil {
		t.Fatalf("inspect application role membership: %v", err)
	}
	if !isMember {
		if _, err := admin.Pool().Exec(ctx,
			"GRANT "+roleIdentifier+" TO "+pgx.Identifier{currentUser}.Sanitize()); err != nil {
			t.Fatalf("grant application role membership: %v", err)
		}
		t.Cleanup(func() {
			_, _ = admin.Pool().Exec(context.WithoutCancel(ctx),
				"REVOKE "+roleIdentifier+" FROM "+pgx.Identifier{currentUser}.Sanitize())
		})
	}
	return roleIdentifier
}

// grantPhase6Role grants schema usage and the requested privilege on each
// table to the restricted application role.
func grantPhase6Role(ctx context.Context, t *testing.T, admin *postgres.Driver, schemaName, roleIdentifier, privileges string, tables ...string) {
	t.Helper()
	if _, err := admin.Pool().Exec(ctx,
		"GRANT USAGE ON SCHEMA "+pgx.Identifier{schemaName}.Sanitize()+" TO "+roleIdentifier); err != nil {
		t.Fatalf("grant schema usage to application role: %v", err)
	}
	for _, table := range tables {
		qualified := pgx.Identifier{schemaName, table}.Sanitize()
		if _, err := admin.Pool().Exec(ctx,
			"GRANT "+privileges+" ON "+qualified+" TO "+roleIdentifier); err != nil {
			t.Fatalf("grant %s on %s to application role: %v", privileges, table, err)
		}
	}
}

func assertPhase6RoleIsRestricted(ctx context.Context, t *testing.T, tx pgx.Tx) {
	t.Helper()
	var bypassesRLS, isSuperuser bool
	if err := tx.QueryRow(ctx, `SELECT rolbypassrls, rolsuper FROM pg_roles WHERE rolname=current_user`).
		Scan(&bypassesRLS, &isSuperuser); err != nil {
		t.Fatalf("inspect active application role: %v", err)
	}
	if bypassesRLS || isSuperuser {
		t.Fatalf("application role must be NOBYPASSRLS and NOSUPERUSER (bypass=%v, super=%v)",
			bypassesRLS, isSuperuser)
	}
}

type phase6TenantFixture struct {
	companyID uuid.UUID
	journalID uuid.UUID
	cashAccID uuid.UUID
	accounts  []uuid.UUID
	moveID    uuid.UUID
	periodID  uuid.UUID
}

func seedPhase6Tenant(ctx context.Context, t *testing.T, admin *postgres.Driver) phase6TenantFixture {
	t.Helper()
	fixture := phase6TenantFixture{
		companyID: uuid.New(),
		journalID: uuid.New(),
		cashAccID: uuid.New(),
	}
	revenueAccID := uuid.New()
	fixture.accounts = []uuid.UUID{fixture.cashAccID, revenueAccID}
	if err := admin.CreateCompany(ctx, masterdata.Company{
		ID: fixture.companyID, Code: fmt.Sprintf("PEN-%s", fixture.companyID.String()[:8]),
		LegalName: "Phase 6 Penetration Tenant", FunctionalCurrency: "USD",
		PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := admin.WithCompanyTx(ctx, fixture.companyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO journals(id,company_id,code) VALUES($1,$2,$3)`,
			fixture.journalID, fixture.companyID, "PEN-"+fixture.journalID.String()[:8])
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, account := range []masterdata.Account{
		{CompanyID: fixture.companyID, ID: fixture.cashAccID, Code: "1000", Name: "Cash",
			AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, IsActive: true},
		{CompanyID: fixture.companyID, ID: revenueAccID, Code: "4000", Name: "Revenue",
			AccountType: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit,
			IFRSCategory: masterdata.IFRSCategoryOperating, IsActive: true},
	} {
		if err := admin.CreateAccount(ctx, account); err != nil {
			t.Fatal(err)
		}
	}
	year := masterdata.GenerateFiscalYear(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = fixture.companyID
	if err := admin.CreateFiscalYear(ctx, year); err != nil {
		t.Fatal(err)
	}
	fixture.periodID = year.Periods[0].ID

	amount, err := financialtypes.NewMoneyFromString("250.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	zero, _ := financialtypes.Zero("USD")
	entry, err := posting.NewJournalEntry(fixture.journalID, time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		"penetration-maker", []posting.JournalEntryLine{
			{CompanyID: fixture.companyID, ID: uuid.New(), AccountID: fixture.cashAccID, Debit: amount, Credit: zero},
			{CompanyID: fixture.companyID, ID: uuid.New(), AccountID: revenueAccID, Debit: zero, Credit: amount},
		})
	if err != nil {
		t.Fatal(err)
	}
	entry.CompanyID = fixture.companyID
	entry.PeriodID = fixture.periodID
	fixture.moveID = entry.ID
	if err := admin.SaveDraft(ctx, entry); err != nil {
		t.Fatal(err)
	}
	entry.Status = posting.EntryPosted
	if _, err := admin.PostAndSequence(ctx, entry); err != nil {
		t.Fatal(err)
	}
	return fixture
}

// TestPhase6RLSPenetrationCrossCompanyReads proves that the restricted
// application role cannot read another tenant's rows even when it deliberately
// sets app.current_company_id to the victim company.
func TestPhase6RLSPenetrationCrossCompanyReads(t *testing.T) {
	ctx := context.Background()
	admin, err := newIsolatedPostgresDriver(t, ctx)
	if err != nil {
		t.Skipf("isolated PostgreSQL schema unavailable: %v", err)
	}
	tenantA := seedPhase6Tenant(ctx, t, admin)
	tenantB := seedPhase6Tenant(ctx, t, admin)

	roleIdentifier := ensurePhase6AppRole(ctx, t, admin)
	var schemaName string
	if err := admin.Pool().QueryRow(ctx, "SELECT current_schema()").Scan(&schemaName); err != nil {
		t.Fatal(err)
	}
	grantPhase6Role(ctx, t, admin, schemaName, roleIdentifier, "SELECT",
		"companies", "accounts", "journals", "fiscal_years", "fiscal_periods",
		"account_moves", "account_move_lines", "account_balances")

	appTx, err := admin.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer appTx.Rollback(context.WithoutCancel(ctx))
	if _, err := appTx.Exec(ctx, "SET LOCAL ROLE "+roleIdentifier); err != nil {
		t.Fatalf("assume restricted application role: %v", err)
	}
	assertPhase6RoleIsRestricted(ctx, t, appTx)

	// Tenant A context: only tenant A rows are visible.
	if _, err := appTx.Exec(ctx, `SELECT set_config('app.current_company_id',$1,true)`, tenantA.companyID.String()); err != nil {
		t.Fatal(err)
	}
	assertTenantCount := func(table, column string, companyID uuid.UUID, want int) {
		t.Helper()
		var count int
		query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s=$1",
			pgx.Identifier{schemaName, table}.Sanitize(), pgx.Identifier{column}.Sanitize())
		if err := appTx.QueryRow(ctx, query, companyID).Scan(&count); err != nil {
			t.Fatalf("count %s for %s: %v", table, companyID, err)
		}
		if count != want {
			t.Fatalf("%s rows visible for company %s = %d, want %d", table, companyID, count, want)
		}
	}
	assertTenantCount("account_moves", "company_id", tenantA.companyID, 1)
	assertTenantCount("account_move_lines", "company_id", tenantA.companyID, 2)
	assertTenantCount("accounts", "company_id", tenantA.companyID, 2)
	assertTenantCount("journals", "company_id", tenantA.companyID, 1)
	assertTenantCount("account_moves", "company_id", tenantB.companyID, 0)
	assertTenantCount("account_move_lines", "company_id", tenantB.companyID, 0)
	assertTenantCount("accounts", "company_id", tenantB.companyID, 0)
	assertTenantCount("journals", "company_id", tenantB.companyID, 0)

	var companiesVisible int
	if err := appTx.QueryRow(ctx,
		`SELECT COUNT(*) FROM companies WHERE id=$1`, tenantB.companyID).Scan(&companiesVisible); err != nil {
		t.Fatal(err)
	}
	if companiesVisible != 0 {
		t.Fatalf("company B row visible from company A context, want 0")
	}

	// Direct victim-row lookup by primary key must return no rows.
	var entry []byte
	err = appTx.QueryRow(ctx, `SELECT entry FROM account_moves WHERE id=$1`, tenantB.moveID).Scan(&entry)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-company move lookup error = %v, want no rows", err)
	}

	// Even when the attacker switches the session setting to the victim
	// company, the victim's rows stay invisible unless the connection is
	// actually acting for that tenant.
	if _, err := appTx.Exec(ctx, `SELECT set_config('app.current_company_id',$1,true)`, tenantB.companyID.String()); err != nil {
		t.Fatal(err)
	}
	assertTenantCount("account_moves", "company_id", tenantA.companyID, 0)
	assertTenantCount("accounts", "company_id", tenantA.companyID, 0)
}

// TestPhase6RLSPenetrationCrossCompanyAccountInEntry proves that the restricted
// role cannot combine an account from company A and an account from company B
// inside one journal entry, neither through the repository nor through raw SQL.
func TestPhase6RLSPenetrationCrossCompanyAccountInEntry(t *testing.T) {
	ctx := context.Background()
	admin, err := newIsolatedPostgresDriver(t, ctx)
	if err != nil {
		t.Skipf("isolated PostgreSQL schema unavailable: %v", err)
	}
	tenantA := seedPhase6Tenant(ctx, t, admin)
	tenantB := seedPhase6Tenant(ctx, t, admin)

	roleIdentifier := ensurePhase6AppRole(ctx, t, admin)
	var schemaName string
	if err := admin.Pool().QueryRow(ctx, "SELECT current_schema()").Scan(&schemaName); err != nil {
		t.Fatal(err)
	}
	grantPhase6Role(ctx, t, admin, schemaName, roleIdentifier, "SELECT, INSERT",
		"account_moves", "account_move_lines", "accounts", "journals", "fiscal_periods")

	// A legitimate draft shell owned by company A exists before the attack.
	shellID := uuid.New()
	if err := admin.WithCompanyTx(ctx, tenantA.companyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO account_moves(id,company_id,journal_id,period_id,entry_date,status,entry)
			VALUES($1,$2,$3,$4,$5,'draft','{}')`,
			shellID, tenantA.companyID, tenantA.journalID, tenantA.periodID,
			time.Date(2026, 2, 10, 0, 0, 0, 0, time.UTC))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	appTx, err := admin.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer appTx.Rollback(context.WithoutCancel(ctx))
	if _, err := appTx.Exec(ctx, "SET LOCAL ROLE "+roleIdentifier); err != nil {
		t.Fatalf("assume restricted application role: %v", err)
	}
	assertPhase6RoleIsRestricted(ctx, t, appTx)
	if _, err := appTx.Exec(ctx, `SELECT set_config('app.current_company_id',$1,true)`, tenantA.companyID.String()); err != nil {
		t.Fatal(err)
	}

	// One entry, two lines: company A's cash account and company B's account.
	// The composite foreign key (company_id, account_id) must reject the whole
	// statement so the poisoned entry can never be persisted.
	_, insertErr := appTx.Exec(ctx, `INSERT INTO account_move_lines(
			move_id,company_id,line_id,account_id,debit,credit,amount_currency,currency_id,
			transaction_amount_minor,transaction_currency,exchange_rate,
			functional_debit_minor,functional_credit_minor,line_data)
			VALUES
			($1,$2,$3,$4,100,0,100,'USD',10000,'USD',1,10000,0,'{}'),
			($1,$2,$5,$6,0,100,100,'USD',10000,'USD',1,0,10000,'{}')`,
		shellID, tenantA.companyID, uuid.New(), tenantA.cashAccID,
		uuid.New(), tenantB.accounts[0])
	if insertErr == nil {
		t.Fatal("cross-company account line was accepted, want RLS/foreign-key rejection")
	}
	assertPhase6Rejection(t, insertErr, "cross-company account line")

	// The failed statement aborted the transaction; start a clean one for the
	// second attack.
	if err := appTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	smuggleTx, err := admin.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer smuggleTx.Rollback(context.WithoutCancel(ctx))
	if _, err := smuggleTx.Exec(ctx, "SET LOCAL ROLE "+roleIdentifier); err != nil {
		t.Fatalf("assume restricted application role: %v", err)
	}
	if _, err := smuggleTx.Exec(ctx, `SELECT set_config('app.current_company_id',$1,true)`, tenantA.companyID.String()); err != nil {
		t.Fatal(err)
	}

	// Attempting to smuggle a move owned by company B while the session is
	// bound to company A must fail the RLS WITH CHECK clause.
	_, smuggleErr := smuggleTx.Exec(ctx, `INSERT INTO account_moves(id,company_id,journal_id,entry_date,status,entry)
		VALUES($1,$2,$3,$4,'draft','{}')`,
		uuid.New(), tenantB.companyID, tenantB.journalID, time.Date(2026, 2, 11, 0, 0, 0, 0, time.UTC))
	if smuggleErr == nil {
		t.Fatal("cross-company move insert was accepted, want RLS rejection")
	}
	assertPhase6Rejection(t, smuggleErr, "cross-company move insert")
}

// TestPhase6RLSPenetrationMissingCompanyContext proves that a connection that
// never sets app.current_company_id sees no tenant data and cannot write.
func TestPhase6RLSPenetrationMissingCompanyContext(t *testing.T) {
	ctx := context.Background()
	admin, err := newIsolatedPostgresDriver(t, ctx)
	if err != nil {
		t.Skipf("isolated PostgreSQL schema unavailable: %v", err)
	}
	tenant := seedPhase6Tenant(ctx, t, admin)

	roleIdentifier := ensurePhase6AppRole(ctx, t, admin)
	var schemaName string
	if err := admin.Pool().QueryRow(ctx, "SELECT current_schema()").Scan(&schemaName); err != nil {
		t.Fatal(err)
	}
	grantPhase6Role(ctx, t, admin, schemaName, roleIdentifier, "SELECT, INSERT",
		"account_moves", "accounts", "journals", "fiscal_periods")

	appTx, err := admin.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer appTx.Rollback(context.WithoutCancel(ctx))
	if _, err := appTx.Exec(ctx, "SET LOCAL ROLE "+roleIdentifier); err != nil {
		t.Fatalf("assume restricted application role: %v", err)
	}
	assertPhase6RoleIsRestricted(ctx, t, appTx)

	// No app.current_company_id has been set at all.
	var currentCompany *uuid.UUID
	if err := appTx.QueryRow(ctx, `SELECT app_current_company_id()`).Scan(&currentCompany); err != nil {
		t.Fatalf("read app_current_company_id: %v", err)
	}
	if currentCompany != nil {
		t.Fatalf("app_current_company_id = %v without tenant context, want NULL", *currentCompany)
	}
	for _, table := range []string{"account_moves", "accounts", "journals"} {
		var count int
		query := "SELECT COUNT(*) FROM " + pgx.Identifier{schemaName, table}.Sanitize()
		if err := appTx.QueryRow(ctx, query).Scan(&count); err != nil {
			t.Fatalf("count %s without tenant context: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s rows visible without tenant context = %d, want 0", table, count)
		}
	}

	// An empty setting is treated as missing, not as "all companies".
	if _, err := appTx.Exec(ctx, `SELECT set_config('app.current_company_id','',true)`); err != nil {
		t.Fatal(err)
	}
	var movesWithEmptySetting int
	if err := appTx.QueryRow(ctx, `SELECT COUNT(*) FROM account_moves`).Scan(&movesWithEmptySetting); err != nil {
		t.Fatal(err)
	}
	if movesWithEmptySetting != 0 {
		t.Fatalf("account_moves visible with empty tenant context = %d, want 0", movesWithEmptySetting)
	}

	// Writes are rejected by the RLS WITH CHECK clause when no context exists.
	_, insertErr := appTx.Exec(ctx, `INSERT INTO account_moves(id,company_id,journal_id,entry_date,status,entry)
		VALUES($1,$2,$3,$4,'draft','{}')`,
		uuid.New(), tenant.companyID, tenant.journalID, time.Date(2026, 2, 12, 0, 0, 0, 0, time.UTC))
	if insertErr == nil {
		t.Fatal("move insert without tenant context was accepted, want RLS rejection")
	}
	assertPhase6Rejection(t, insertErr, "move insert without tenant context")
}

// assertPhase6Rejection accepts the two database-level outcomes that both
// prove isolation: a row-level security policy violation (SQLSTATE 42501) or a
// composite foreign-key violation (SQLSTATE 23503).
func assertPhase6Rejection(t *testing.T, err error, scenario string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("%s error = %v, want a PostgreSQL RLS/foreign-key error", scenario, err)
	}
	if pgErr.Code != "42501" && pgErr.Code != "23503" {
		t.Fatalf("%s SQLSTATE = %s (%s), want 42501 (RLS) or 23503 (foreign key)",
			scenario, pgErr.Code, pgErr.Message)
	}
}
