package postgres_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/policy"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
)

func TestPostgresVersionedPolicyHistoryIsImmutable(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)
	unit := policy.NewUnit(env.Driver)
	policyID := uuid.New()
	for version, year := range []int{2024, 2025} {
		effective := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
		presentation := []string{"USD", "EUR"}[version]
		if err := unit.SaveCurrencyPolicy(ctx, policy.EffectivePolicyHeader{
			PolicyID: policyID, CompanyID: env.CompanyID, Version: version + 1,
			EffectiveFrom: effective, CreatedBy: "maker", ApprovedBy: "approver",
		}, policy.CurrencyPolicy{
			FunctionalCurrency: "USD", PresentationCurrency: presentation,
			RoundingMethod: policy.RoundingBankers, ToleranceScale: 4,
			AllowedCurrencies: []string{"USD", "EUR"}, EffectiveDate: effective,
		}); err != nil {
			t.Fatal(err)
		}
	}
	historical, err := unit.GetCurrencyPolicyAsOf(ctx, env.CompanyID, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || historical.PresentationCurrency != "USD" {
		t.Fatalf("historical policy = %+v, error %v", historical, err)
	}
	current, err := unit.GetCurrencyPolicyAsOf(ctx, env.CompanyID, time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || current.PresentationCurrency != "EUR" {
		t.Fatalf("current policy = %+v, error %v", current, err)
	}
	if err := env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		var auditCount int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM accounting_policy_audit_logs
			WHERE company_id=$1 AND policy_id=$2`, env.CompanyID, policyID).Scan(&auditCount); err != nil {
			return err
		}
		if auditCount != 2 {
			t.Fatalf("policy audit rows = %d, want 2", auditCount)
		}
		if _, err := tx.Exec(ctx, "SAVEPOINT policy_mutation_test"); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE accounting_policy_versions SET created_by='tampered'
			WHERE company_id=$1 AND policy_id=$2`, env.CompanyID, policyID)
		if err == nil {
			t.Fatal("expected immutable policy history to reject UPDATE")
		}
		_, err = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT policy_mutation_test")
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "SAVEPOINT policy_truncate_test"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "TRUNCATE accounting_policy_audit_logs"); err == nil {
			t.Fatal("expected immutable policy audit log to reject TRUNCATE")
		}
		_, err = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT policy_truncate_test")
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresPostingPolicyRejectsHardClosedPeriod(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)
	masterData := masterdata.NewService(masterdata.NewRepositoryAdapter(env.Driver))
	engine := posting.NewEngine(env.Driver, masterData)
	unit := policy.NewUnit(env.Driver)
	effective := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := unit.SaveCurrencyPolicy(ctx, policy.EffectivePolicyHeader{
		PolicyID: uuid.New(), CompanyID: env.CompanyID, Version: 1,
		EffectiveFrom: effective, CreatedBy: "maker", ApprovedBy: "approver",
	}, policy.CurrencyPolicy{
		FunctionalCurrency: "USD", PresentationCurrency: "USD",
		RoundingMethod: policy.RoundingBankers, ToleranceScale: 4,
		AllowedCurrencies: []string{"USD"}, EffectiveDate: effective,
	}); err != nil {
		t.Fatal(err)
	}
	if err := unit.SavePostingPolicy(ctx, policy.EffectivePolicyHeader{
		PolicyID: uuid.New(), CompanyID: env.CompanyID, Version: 1,
		EffectiveFrom: effective, CreatedBy: "maker", ApprovedBy: "approver",
	}, policy.PostingPolicy{
		MaxBackdatingDays: 10000, EffectiveDate: effective,
	}); err != nil {
		t.Fatal(err)
	}
	engine.SetPolicyUnit(unit)
	amount, _ := financialtypes.NewMoneyFromString("1.00", "USD")
	zero, _ := financialtypes.Zero("USD")
	entryDate := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	entry, err := posting.NewJournalEntry(env.JournalID, entryDate, "maker", []posting.JournalEntryLine{
		{CompanyID: env.CompanyID, ID: uuid.New(), AccountID: env.DebitAccID, Debit: amount, Credit: zero},
		{CompanyID: env.CompanyID, ID: uuid.New(), AccountID: env.CreditAccID, Debit: zero, Credit: amount},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry.CompanyID = env.CompanyID
	if err := engine.CreateDraftContext(ctx, entry); err != nil {
		t.Fatal(err)
	}
	for _, state := range []masterdata.PeriodState{masterdata.PeriodSoftClosed, masterdata.PeriodHardClosed} {
		if err := env.Driver.UpdatePeriodStateForCompanyContext(ctx, env.CompanyID, env.PeriodID, state); err != nil {
			t.Fatal(err)
		}
	}
	if err := engine.PostEntryContext(ctx, &entry, "manager"); !errors.Is(err, policy.ErrHardClosedPeriod) {
		t.Fatalf("posting hard-closed period error = %v, want hard-closed denial", err)
	}
}
