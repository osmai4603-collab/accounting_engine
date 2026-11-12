package accounting_engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/reporting"
	"accounting_engine/storage/memory"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type reconciliationTestDriver struct {
	*memory.Driver
	companyID uuid.UUID
	periodID  uuid.UUID
	currency  string
	drift     bool
}

func (d *reconciliationTestDriver) BuildBalanceReconciliationReport(
	ctx context.Context,
	companyID, periodID uuid.UUID,
	currency string,
) (reporting.ReconciliationReport, error) {
	if err := ctx.Err(); err != nil {
		return reporting.ReconciliationReport{}, err
	}
	d.companyID, d.periodID, d.currency = companyID, periodID, currency
	report := reporting.ReconciliationReport{
		CompanyID:       companyID,
		FiscalPeriodID:  periodID,
		CurrencyID:      currency,
		AccountsChecked: 2,
		GeneratedAt:     time.Now().UTC(),
		ZeroDrift:       true,
	}
	if d.drift {
		report.ZeroDrift = false
		report.AccountsChecked = 2
		report.Drifts = append(report.Drifts, reporting.ReconciliationAccountDrift{
			AccountID:       uuid.New(),
			DebitDriftMinor: decimal.NewFromInt(1),
		})
	}
	return report, nil
}

func TestReportFacadeRequiresCompanyContext(t *testing.T) {
	engine, err := New(memory.New(), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	companyID, accountID := uuid.New(), uuid.New()
	start, end := time.Now().Add(-24*time.Hour), time.Now()

	if _, err := engine.GeneralLedger(ctx, companyID, accountID, start, end, "USD"); !errors.Is(err, context.Canceled) {
		t.Errorf("GeneralLedger error = %v, want context.Canceled", err)
	}
	if _, err := engine.FinancialRatios(ctx, companyID, start, end, "USD"); !errors.Is(err, context.Canceled) {
		t.Errorf("FinancialRatios error = %v, want context.Canceled", err)
	}
	if _, err := engine.StatementBundle(ctx, companyID, start, end, "USD"); !errors.Is(err, context.Canceled) {
		t.Errorf("StatementBundle error = %v, want context.Canceled", err)
	}
}

func TestReportFacadeWithCompanyContext(t *testing.T) {
	ctx := context.Background()
	driver := memory.New()
	companyA, companyB := uuid.New(), uuid.New()
	for _, companyID := range []uuid.UUID{companyA, companyB} {
		if err := driver.CreateCompany(ctx, masterdata.Company{
			ID: companyID, Code: "REPORT-" + companyID.String()[:8], LegalName: "Report Scope Test",
			FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	account := masterdata.Account{
		CompanyID: companyA, ID: uuid.New(), Code: "1000", Name: "Company A Cash",
		AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit,
		CashFlowActivity: masterdata.CashFlowOperating, IsActive: true,
	}
	if err := driver.CreateAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	engine, err := New(driver, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}

	report, err := engine.GeneralLedger(ctx, companyA, account.ID, time.Time{}, time.Time{}, "USD")
	if err != nil {
		t.Fatalf("generate scoped general ledger: %v", err)
	}
	if report.Name != account.Name {
		t.Fatalf("general ledger name = %q, want %q", report.Name, account.Name)
	}
	if _, err := engine.GeneralLedger(ctx, companyB, account.ID, time.Time{}, time.Time{}, "USD"); err == nil {
		t.Fatal("expected account from another company to be unavailable")
	}

	bundleA, err := engine.StatementBundle(ctx, companyA, time.Time{}, time.Time{}, "USD")
	if err != nil {
		t.Fatalf("generate company A statement bundle: %v", err)
	}
	bundleB, err := engine.StatementBundle(ctx, companyB, time.Time{}, time.Time{}, "USD")
	if err != nil {
		t.Fatalf("generate company B statement bundle: %v", err)
	}
	if len(bundleA.TrialBalance.Lines) != 1 || len(bundleB.TrialBalance.Lines) != 0 {
		t.Fatalf("trial balance lines crossed company scope: A=%d B=%d",
			len(bundleA.TrialBalance.Lines), len(bundleB.TrialBalance.Lines))
	}
}

func TestBalanceReconciliationFacadePassesCompanyScope(t *testing.T) {
	driver := &reconciliationTestDriver{Driver: memory.New()}
	engine, err := New(driver, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}

	companyID, periodID := uuid.New(), uuid.New()
	report, err := engine.ReconcileBalances(context.Background(), companyID, periodID, "USD")
	if err != nil {
		t.Fatalf("reconcile balances: %v", err)
	}
	if driver.companyID != companyID || driver.periodID != periodID || driver.currency != "USD" {
		t.Fatalf("reconciliation scope = (%s, %s, %q)", driver.companyID, driver.periodID, driver.currency)
	}
	if report == nil || !report.ZeroDrift || report.CompanyID != companyID || report.FiscalPeriodID != periodID {
		t.Fatalf("reconciliation report = %+v, want zero-drift report for (%s, %s)", report, companyID, periodID)
	}
}

func TestBalanceReconciliationFacadeSurfacesDrift(t *testing.T) {
	driver := &reconciliationTestDriver{Driver: memory.New(), drift: true}
	engine, err := New(driver, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}

	report, err := engine.ReconcileBalances(context.Background(), uuid.New(), uuid.New(), "USD")
	if !errors.Is(err, reporting.ErrBalanceDriftDetected) {
		t.Fatalf("reconcile balances error = %v, want ErrBalanceDriftDetected", err)
	}
	if report != nil {
		t.Fatalf("reconcile balances report = %+v, want nil on drift", report)
	}
	if driver.companyID == uuid.Nil {
		t.Fatal("reconciliation scope was not forwarded to the repository")
	}
}
