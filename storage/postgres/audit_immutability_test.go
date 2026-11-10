package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"accounting_engine/internal/governance"
	"accounting_engine/internal/masterdata"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPostgresAuditLogsRejectUpdateDeleteAndTruncate(t *testing.T) {
	ctx := context.Background()
	driver, err := newIsolatedPostgresDriver(t, ctx)
	if err != nil {
		t.Skipf("isolated PostgreSQL schema unavailable: %v", err)
	}
	companyID := uuid.New()
	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: fmt.Sprintf("AUDIT-%s", companyID.String()[:8]),
		LegalName: "Audit Immutability Test", FunctionalCurrency: "USD",
		PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	log, err := driver.AppendAuditLogAtomic(ctx, governance.FinancialAuditLog{
		CompanyID: companyID, EntityType: "journal_entry", EntityID: uuid.New(),
		Action: "POST", ActorID: uuid.New(), Timestamp: time.Now().UTC(),
		PayloadAfter: []byte(`{"status":"posted"}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	statements := []struct {
		name string
		sql  string
		args []any
	}{
		{name: "update", sql: "UPDATE financial_audit_logs SET action='TAMPERED' WHERE company_id=$1 AND id=$2", args: []any{companyID, log.ID}},
		{name: "delete", sql: "DELETE FROM financial_audit_logs WHERE company_id=$1 AND id=$2", args: []any{companyID, log.ID}},
		{name: "truncate", sql: "TRUNCATE financial_audit_logs"},
	}
	for _, statement := range statements {
		t.Run(statement.name, func(t *testing.T) {
			err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, "SAVEPOINT verify_audit_immutability"); err != nil {
					return err
				}
				_, mutationErr := tx.Exec(ctx, statement.sql, statement.args...)
				var pgErr *pgconn.PgError
				if !errors.As(mutationErr, &pgErr) || pgErr.Code != "23001" {
					return fmt.Errorf("mutation error = %v, want SQLSTATE 23001", mutationErr)
				}
				_, rollbackErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT verify_audit_immutability")
				return rollbackErr
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
