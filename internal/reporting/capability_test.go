package reporting_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/reporting"
	"github.com/osmai4603-collab/accounting_engine/internal/valuation"
	"github.com/shopspring/decimal"
)

type capabilitySources struct {
	accounts []masterdata.Account
}

func (s capabilitySources) ListAccounts(context.Context) ([]masterdata.Account, error) {
	return s.accounts, nil
}

func (capabilitySources) ListPostedEntries(context.Context, time.Time, time.Time) ([]posting.JournalEntry, error) {
	return nil, nil
}

type translatedCapabilitySources struct{ capabilitySources }

func (s translatedCapabilitySources) GetRate(context.Context, string, string, time.Time) (decimal.Decimal, error) {
	return decimal.NewFromInt(1), nil
}

func (s translatedCapabilitySources) GetRateOfType(context.Context, valuation.RateType, string, string, time.Time) (decimal.Decimal, error) {
	return decimal.NewFromInt(1), nil
}

type detailedReconciliationRepository struct {
	reporting.Repository
	report reporting.ReconciliationReport
}

func (r detailedReconciliationRepository) BuildBalanceReconciliationReport(
	context.Context, uuid.UUID, uuid.UUID, string,
) (reporting.ReconciliationReport, error) {
	return r.report, nil
}

type assertOnlyReconciliationRepository struct{ reporting.Repository }

func (assertOnlyReconciliationRepository) AssertBalancesMatchMoves(context.Context, uuid.UUID, uuid.UUID, string) error {
	return nil
}

func TestReportingCapabilityRequirements(t *testing.T) {
	if _, err := reporting.NewEngine(nil).TrialBalance.Generate(time.Time{}, time.Time{}, "USD"); !errors.Is(err, reporting.ErrReportingDependencyMissing) {
		t.Fatalf("core-only report error = %v", err)
	}
	requirements, err := reporting.RequirementsForReport(reporting.ReportBalanceSheet, "EUR", "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !requirements.LedgerRead || !requirements.AccountTaxonomy || !requirements.CurrencyTranslation {
		t.Fatalf("cross-currency balance sheet requirements = %+v", requirements)
	}
	generalLedger, err := reporting.RequirementsForReport(reporting.ReportGeneralLedger, "USD", "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !generalLedger.EntryRead || generalLedger.CurrencyTranslation {
		t.Fatalf("general-ledger requirements = %+v", generalLedger)
	}
	if err := reporting.ValidateReportRequirements(nil, requirements); !errors.Is(err, reporting.ErrReportingDependencyMissing) {
		t.Fatalf("core-only reporting error = %v", err)
	}

	sources := capabilitySources{}
	if err := reporting.ValidateReportRequirements(sources, requirements); !errors.Is(err, reporting.ErrReportingTaxonomyMissing) {
		t.Fatalf("empty taxonomy error = %v", err)
	}

	sources.accounts = []masterdata.Account{{
		ID: uuid.New(), Code: "1000", AccountType: masterdata.AccountTypeAsset,
	}}
	if err := reporting.ValidateReportRequirements(sources, requirements); !errors.Is(err, reporting.ErrReportingDependencyMissing) {
		t.Fatalf("missing translation provider error = %v", err)
	}
	if err := reporting.ValidateReportRequirements(translatedCapabilitySources{sources}, requirements); err != nil {
		t.Fatalf("valid reporting capability error = %v", err)
	}

	if _, err := reporting.RequirementsForReport("not-a-report", "", ""); err == nil {
		t.Fatal("unknown report kind unexpectedly validated")
	}
}

func TestBalanceReconciliationRequiresComparableDetail(t *testing.T) {
	companyID, periodID := uuid.New(), uuid.New()
	repository := detailedReconciliationRepository{
		Repository: &memoryRepository{},
		report: reporting.ReconciliationReport{
			CompanyID: companyID, FiscalPeriodID: periodID, CurrencyID: "USD", ZeroDrift: true,
		},
	}
	engine := reporting.NewEngine(repository).Reconciliation
	_, err := engine.Reconcile(context.Background(), companyID, periodID, "USD")
	if !errors.Is(err, reporting.ErrBalanceReconciliationNoData) {
		t.Fatalf("empty detail error = %v", err)
	}

	repository.report.AccountsChecked = 1
	report, err := reporting.NewEngine(repository).Reconciliation.Reconcile(
		context.Background(), companyID, periodID, "USD",
	)
	if err != nil || !report.ZeroDrift || report.AccountsChecked != 1 {
		t.Fatalf("valid detailed reconciliation = %+v, %v", report, err)
	}

	assertOnly := assertOnlyReconciliationRepository{Repository: &memoryRepository{}}
	_, err = reporting.NewEngine(assertOnly).Reconciliation.Reconcile(
		context.Background(), companyID, periodID, "USD",
	)
	if !errors.Is(err, reporting.ErrBalanceReconciliationDetailRequired) {
		t.Fatalf("assert-only reconciliation error = %v", err)
	}
}

var _ valuation.RateProviderSPI = translatedCapabilitySources{}
