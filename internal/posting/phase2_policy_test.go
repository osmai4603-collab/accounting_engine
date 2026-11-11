package posting_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/policy"
	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type testManagerAuthorizer bool

func (a testManagerAuthorizer) IsAccountingManager(context.Context, uuid.UUID, string) (bool, error) {
	return bool(a), nil
}

func installPolicies(
	t *testing.T,
	engine *posting.Engine,
	driver policy.Repository,
	companyID uuid.UUID,
	maxBackdating int,
	roundingAccountID ...uuid.UUID,
) *policy.Unit {
	t.Helper()
	unit := policy.NewUnit(driver)
	now := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	currency := policy.CurrencyPolicy{
		FunctionalCurrency: "USD", PresentationCurrency: "USD",
		RoundingMethod: policy.RoundingBankers, ToleranceScale: 4,
		AllowedCurrencies: []string{"USD"}, EffectiveDate: now,
	}
	if len(roundingAccountID) > 0 {
		currency.RoundingDifferenceAccountID = roundingAccountID[0]
		currency.AutoSettleRoundingDifferences = true
	}
	postingPolicy := policy.PostingPolicy{
		MaxBackdatingDays: maxBackdating, EffectiveDate: now,
	}
	if err := unit.SaveCurrencyPolicy(context.Background(), policy.EffectivePolicyHeader{
		PolicyID: uuid.New(), CompanyID: companyID, Version: 1, EffectiveFrom: now,
		CreatedBy: "maker", ApprovedBy: "approver",
	}, currency); err != nil {
		t.Fatal(err)
	}
	if err := unit.SavePostingPolicy(context.Background(), policy.EffectivePolicyHeader{
		PolicyID: uuid.New(), CompanyID: companyID, Version: 1, EffectiveFrom: now,
		CreatedBy: "maker", ApprovedBy: "approver",
	}, postingPolicy); err != nil {
		t.Fatal(err)
	}
	engine.SetPolicyUnit(unit)
	return unit
}

func TestCurrencyPolicyAutomaticallySettlesRoundingDifference(t *testing.T) {
	engine, driver, companyID, journalID, debitAccount, creditAccount := setupTestEngine(t)
	installPolicies(t, engine, driver, companyID, 365, debitAccount)
	date := time.Now().UTC()
	transactionDebit, _ := financialtypes.NewMoneyFromString("10.00", "USD")
	functionalDebit, _ := financialtypes.NewMoneyFromString("10.005", "USD")
	transactionCredit, _ := financialtypes.NewMoneyFromString("20.01", "USD")
	functionalCredit, _ := financialtypes.NewMoneyFromString("20.010", "USD")
	zero, _ := financialtypes.Zero("USD")
	entry, err := posting.NewJournalEntry(journalID, date, "maker", []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: debitAccount, TransactionAmount: transactionDebit,
			TransactionCurrency: "USD", ExchangeRate: decimal.RequireFromString("1.0005"),
			FunctionalDebit: functionalDebit, FunctionalCredit: zero},
		{CompanyID: companyID, ID: uuid.New(), AccountID: debitAccount, TransactionAmount: transactionDebit,
			TransactionCurrency: "USD", ExchangeRate: decimal.RequireFromString("1.0005"),
			FunctionalDebit: functionalDebit, FunctionalCredit: zero},
		{CompanyID: companyID, ID: uuid.New(), AccountID: creditAccount, TransactionAmount: transactionCredit,
			TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1),
			FunctionalDebit: zero, FunctionalCredit: functionalCredit},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry.CompanyID = companyID
	if err := engine.CreateDraftContext(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	if err := engine.PostEntryContext(context.Background(), &entry, "poster"); err != nil {
		t.Fatal(err)
	}
	if len(entry.Lines) != 4 {
		t.Fatalf("posted lines = %d, want the rounding settlement line", len(entry.Lines))
	}
	settlement := entry.Lines[3]
	if settlement.AccountID != debitAccount || settlement.FunctionalDebit.Amount().String() != "0.01" {
		t.Fatalf("settlement = account %s debit %s", settlement.AccountID, settlement.FunctionalDebit.Amount())
	}
}

func policyEntry(t *testing.T, companyID, journalID, debitAccount, creditAccount uuid.UUID, date time.Time) posting.JournalEntry {
	t.Helper()
	amount, err := financialtypes.NewMoneyFromString("10.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	zero, _ := financialtypes.Zero("USD")
	entry, err := posting.NewJournalEntry(journalID, date, "maker", []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: debitAccount, Debit: amount, Credit: zero},
		{CompanyID: companyID, ID: uuid.New(), AccountID: creditAccount, Debit: zero, Credit: amount},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry.CompanyID = companyID
	return entry
}

func TestPostingPolicyRejectsExcessiveBackdating(t *testing.T) {
	engine, driver, companyID, journalID, debitAccount, creditAccount := setupTestEngine(t)
	installPolicies(t, engine, driver, companyID, 7)
	entry := policyEntry(t, companyID, journalID, debitAccount, creditAccount, time.Now().UTC().AddDate(0, 0, -8))
	if err := engine.CreateDraftContext(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	err := engine.PostEntryContext(context.Background(), &entry, "poster")
	if !errors.Is(err, policy.ErrBackdatingLimit) {
		t.Fatalf("post error = %v, want backdating limit", err)
	}
}

func TestPostingPolicyRejectsHardClosedPeriod(t *testing.T) {
	engine, driver, companyID, journalID, debitAccount, creditAccount := setupTestEngine(t)
	installPolicies(t, engine, driver, companyID, 365)
	date := time.Now().UTC()
	period, err := driver.GetPeriodByDateForCompany(context.Background(), companyID, date)
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.UpdatePeriodState(context.Background(), companyID, period.ID, masterdata.PeriodSoftClosed); err != nil {
		t.Fatal(err)
	}
	if err := driver.UpdatePeriodState(context.Background(), companyID, period.ID, masterdata.PeriodHardClosed); err != nil {
		t.Fatal(err)
	}
	entry := policyEntry(t, companyID, journalID, debitAccount, creditAccount, date)
	if err := engine.CreateDraftContext(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	err = engine.PostEntryContext(context.Background(), &entry, "manager")
	if !errors.Is(err, policy.ErrHardClosedPeriod) {
		t.Fatalf("post error = %v, want hard-closed denial", err)
	}
}

func TestPostingPolicyRequiresManagerForSoftClosedPeriod(t *testing.T) {
	engine, driver, companyID, journalID, debitAccount, creditAccount := setupTestEngine(t)
	unit := policy.NewUnit(driver)
	effective := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := unit.SaveCurrencyPolicy(context.Background(), policy.EffectivePolicyHeader{
		PolicyID: uuid.New(), CompanyID: companyID, Version: 1, EffectiveFrom: effective,
		CreatedBy: "maker", ApprovedBy: "approver",
	}, policy.CurrencyPolicy{
		FunctionalCurrency: "USD", PresentationCurrency: "USD", RoundingMethod: policy.RoundingBankers,
		ToleranceScale: 4, AllowedCurrencies: []string{"USD"}, EffectiveDate: effective,
	}); err != nil {
		t.Fatal(err)
	}
	if err := unit.SavePostingPolicy(context.Background(), policy.EffectivePolicyHeader{
		PolicyID: uuid.New(), CompanyID: companyID, Version: 1, EffectiveFrom: effective,
		CreatedBy: "maker", ApprovedBy: "approver",
	}, policy.PostingPolicy{
		MaxBackdatingDays: 365, AllowSoftClosedPosting: true,
		RequireManagerForSoftClosed: true, EffectiveDate: effective,
	}); err != nil {
		t.Fatal(err)
	}
	unit.SetAccountingManagerAuthorizer(testManagerAuthorizer(false))
	engine.SetPolicyUnit(unit)
	date := time.Now().UTC()
	period, err := driver.GetPeriodByDateForCompany(context.Background(), companyID, date)
	if err != nil {
		t.Fatal(err)
	}
	entry := policyEntry(t, companyID, journalID, debitAccount, creditAccount, date)
	if err := engine.CreateDraftContext(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	if err := driver.UpdatePeriodState(context.Background(), companyID, period.ID, masterdata.PeriodSoftClosed); err != nil {
		t.Fatal(err)
	}
	err = engine.PostEntryContext(context.Background(), &entry, "poster")
	if !errors.Is(err, policy.ErrSoftClosedPeriod) {
		t.Fatalf("unauthorized soft-close posting error = %v", err)
	}
	unit.SetAccountingManagerAuthorizer(testManagerAuthorizer(true))
	if err := engine.PostEntryContext(context.Background(), &entry, "poster"); err != nil {
		t.Fatalf("accounting manager soft-close posting: %v", err)
	}
}
