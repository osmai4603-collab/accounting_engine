package masterdatacontract

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"

	"github.com/google/uuid"
)

func Run(t *testing.T, store masterdata.Store) {
	t.Helper()
	ctx := context.Background()
	companyID := types.CompanyID(uuid.New())
	company := masterdata.Company{
		ID: companyID, Code: "CONTRACT-" + strings.ToUpper(uuid.NewString()[:8]), LegalName: "Contract Test Company",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}
	if err := store.CreateCompany(ctx, company); err != nil {
		t.Fatalf("create company: %v", err)
	}
	if got, err := store.GetCompany(ctx, company.ID); err != nil || !reflect.DeepEqual(got, company) {
		t.Fatalf("get company = %#v, %v; want %#v", got, err, company)
	}

	journal := masterdata.Journal{
		ID: masterdata.NewJournalID(), CompanyID: company.ID, Code: "GENERAL", Name: "General Journal",
	}
	if err := store.CreateJournal(ctx, journal); err != nil {
		t.Fatalf("create journal: %v", err)
	}
	if got, err := store.GetJournalByCode(ctx, company.ID, journal.Code); err != nil || !reflect.DeepEqual(got, journal) {
		t.Fatalf("get journal = %#v, %v; want %#v", got, err, journal)
	}
	if _, err := store.GetJournalByCode(ctx, types.NewCompanyID(), journal.Code); err != masterdata.ErrJournalNotFound {
		t.Errorf("read journal under another company error = %v, want ErrJournalNotFound", err)
	}

	for _, invalidJournal := range []masterdata.Journal{
		{ID: masterdata.NewJournalID(), CompanyID: company.ID, Code: "  ", Name: "Invalid"},
		{ID: masterdata.NewJournalID(), CompanyID: company.ID, Code: "NO-NAME"},
		{CompanyID: company.ID, Code: "NO-ID", Name: "Invalid"},
		{ID: masterdata.NewJournalID(), CompanyID: company.ID, Code: strings.Repeat("X", 33), Name: "Invalid"},
		{ID: masterdata.NewJournalID(), CompanyID: company.ID, Code: "NAME-TOO-LONG", Name: strings.Repeat("X", 101)},
	} {
		if err := store.CreateJournal(ctx, invalidJournal); err != masterdata.ErrInvalidJournal {
			t.Errorf("create invalid journal %+v error = %v, want ErrInvalidJournal", invalidJournal, err)
		}
	}
	duplicate := journal
	duplicate.ID = masterdata.NewJournalID()
	if err := store.CreateJournal(ctx, duplicate); err != masterdata.ErrDuplicateJournalCode {
		t.Errorf("create duplicate journal code error = %v, want ErrDuplicateJournalCode", err)
	}
	unknownCompany := journal
	unknownCompany.ID = masterdata.NewJournalID()
	unknownCompany.CompanyID = types.NewCompanyID()
	unknownCompany.Code = "UNKNOWN-COMPANY"
	if err := store.CreateJournal(ctx, unknownCompany); err != masterdata.ErrCompanyNotFound {
		t.Errorf("create journal for unknown company error = %v, want ErrCompanyNotFound", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := store.CreateJournal(cancelled, masterdata.Journal{
		ID: masterdata.NewJournalID(), CompanyID: company.ID, Code: "CANCELLED", Name: "Cancelled",
	}); err == nil {
		t.Error("create journal with cancelled context succeeded")
	}
	if _, err := store.GetJournalByCode(cancelled, company.ID, journal.Code); err == nil {
		t.Error("read journal with cancelled context succeeded")
	}

	account := masterdata.Account{
		ID: types.AccountID(uuid.New()), CompanyID: company.ID, Code: "1000", Name: "Cash",
		Type: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit,
		Currency: "USD", IsReconcilable: true, IsActive: true, IsCashEquivalent: true,
		CashFlowActivity: "operating", SubClassification: "unclassified", IFRSCategory: "unclassified",
	}
	if err := store.CreateAccount(ctx, account); err != nil {
		t.Fatalf("create account: %v", err)
	}
	if got, err := store.GetAccount(ctx, company.ID, account.ID); err != nil || !reflect.DeepEqual(got, account) {
		t.Fatalf("get account = %#v, %v; want %#v", got, err, account)
	}
	if got, err := store.GetAccountByCode(ctx, company.ID, account.Code); err != nil || !reflect.DeepEqual(got, account) {
		t.Fatalf("get account by code = %#v, %v; want %#v", got, err, account)
	}

	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, time.December, 31, 0, 0, 0, 0, time.UTC)
	year := masterdata.FiscalYear{
		ID: types.FiscalYearID(uuid.New()), CompanyID: company.ID, Name: "FY2026",
		StartDate: start, EndDate: end, State: masterdata.FiscalYearOpen,
	}
	if err := store.CreateFiscalYear(ctx, year); err != nil {
		t.Fatalf("create fiscal year: %v", err)
	}
	if got, err := store.GetFiscalYear(ctx, company.ID, year.ID); err != nil || !reflect.DeepEqual(got, year) {
		t.Fatalf("get fiscal year = %#v, %v; want %#v", got, err, year)
	}

	period := masterdata.FiscalPeriod{
		ID: types.FiscalPeriodID(uuid.New()), CompanyID: company.ID, FiscalYearID: year.ID,
		Number: 1, Name: "January 2026", StartDate: start,
		EndDate: time.Date(2026, time.January, 31, 0, 0, 0, 0, time.UTC),
		State:   masterdata.PeriodOpen, Type: masterdata.FiscalPeriodTypeNormal,
	}
	if err := store.CreateFiscalPeriod(ctx, period); err != nil {
		t.Fatalf("create fiscal period: %v", err)
	}
	gotPeriod, err := store.GetFiscalPeriod(ctx, company.ID, period.ID)
	if err != nil {
		t.Fatalf("get fiscal period: %v", err)
	}
	if gotPeriod.ID != period.ID || gotPeriod.CompanyID != period.CompanyID ||
		gotPeriod.FiscalYearID != period.FiscalYearID || gotPeriod.Number != period.Number ||
		gotPeriod.Name != period.Name || !gotPeriod.StartDate.Equal(period.StartDate) ||
		!gotPeriod.EndDate.Equal(period.EndDate) || gotPeriod.State != period.State || gotPeriod.Type != period.Type {
		t.Fatalf("get fiscal period = %#v; want %#v", gotPeriod, period)
	}
}

func AssertUnsupported(t *testing.T, store masterdata.Store) {
	t.Helper()
	ctx := context.Background()
	companyID := types.NewCompanyID()
	fiscalYearID := types.NewFiscalYearID()
	accountID := types.NewAccountID()
	periodID := types.NewFiscalPeriodID()
	checks := []struct {
		name string
		call func() error
	}{
		{"create company", func() error { return store.CreateCompany(ctx, masterdata.Company{}) }},
		{"read company", func() error { _, err := store.GetCompany(ctx, companyID); return err }},
		{"create journal", func() error { return store.CreateJournal(ctx, masterdata.Journal{}) }},
		{"read journal", func() error { _, err := store.GetJournalByCode(ctx, companyID, "GENERAL"); return err }},
		{"create account", func() error { return store.CreateAccount(ctx, masterdata.Account{}) }},
		{"read account", func() error { _, err := store.GetAccount(ctx, companyID, accountID); return err }},
		{"read account by code", func() error { _, err := store.GetAccountByCode(ctx, companyID, "1000"); return err }},
		{"create fiscal year", func() error { return store.CreateFiscalYear(ctx, masterdata.FiscalYear{}) }},
		{"read fiscal year", func() error { _, err := store.GetFiscalYear(ctx, companyID, fiscalYearID); return err }},
		{"create fiscal period", func() error { return store.CreateFiscalPeriod(ctx, masterdata.FiscalPeriod{}) }},
		{"read fiscal period", func() error { _, err := store.GetFiscalPeriod(ctx, companyID, periodID); return err }},
	}
	for _, check := range checks {
		if err := check.call(); err != masterdata.ErrCapabilityNotImplemented {
			t.Errorf("%s error = %v, want ErrCapabilityNotImplemented", check.name, err)
		}
	}
}
