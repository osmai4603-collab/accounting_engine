package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
