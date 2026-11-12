package memory_test

import (
	"context"
	"testing"
	"time"

	accountingengine "accounting_engine"
	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/subledgers"
	financialtypes "accounting_engine/internal/types"
	"accounting_engine/storage/memory"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// TestPhase6GoldenIAS16IAS36FixedAssetLifecycle is the Phase 6 golden fixed
// asset benchmark. It exercises the full IAS 16 / IAS 36 lifecycle through the
// real posting engine and the in-memory ledger:
//
//   - monthly straight-line depreciation (IAS 16.43-62);
//   - an upward revaluation recognised in other comprehensive income (IAS 16.31, 39);
//   - an impairment loss after the revaluation (IAS 36.59);
//   - a disposal with gain on derecognition (IAS 16.67-72).
//
// Every amount is exact and frozen as the audit reference.
func TestPhase6GoldenIAS16IAS36FixedAssetLifecycle(t *testing.T) {
	ctx := context.Background()
	driver := memory.New()

	companyID := uuid.New()
	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: "IAS16-GOLDEN", LegalName: "IAS 16 Golden Company",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}

	type accountSpec struct {
		code, name string
		kind       masterdata.AccountType
		normal     masterdata.NormalBalance
	}
	specs := map[string]accountSpec{
		"asset":       {"1500", "Plant and Equipment", masterdata.AccountTypeAsset, masterdata.NormalBalanceDebit},
		"accumulated": {"1510", "Accumulated Depreciation", masterdata.AccountTypeAsset, masterdata.NormalBalanceCredit},
		"dep_expense": {"6100", "Depreciation Expense", masterdata.AccountTypeExpense, masterdata.NormalBalanceDebit},
		"imp_expense": {"6200", "Impairment Loss", masterdata.AccountTypeExpense, masterdata.NormalBalanceDebit},
		"imp_accum":   {"1520", "Accumulated Impairment", masterdata.AccountTypeAsset, masterdata.NormalBalanceCredit},
		"surplus":     {"3100", "Revaluation Surplus", masterdata.AccountTypeEquity, masterdata.NormalBalanceCredit},
		"reval_loss":  {"6300", "Revaluation Loss", masterdata.AccountTypeExpense, masterdata.NormalBalanceDebit},
		"cash":        {"1010", "Cash", masterdata.AccountTypeAsset, masterdata.NormalBalanceDebit},
		"gain":        {"7100", "Gain on Disposal", masterdata.AccountTypeIncome, masterdata.NormalBalanceCredit},
		"loss":        {"7200", "Loss on Disposal", masterdata.AccountTypeExpense, masterdata.NormalBalanceDebit},
	}
	accounts := make(map[string]uuid.UUID, len(specs))
	for key, spec := range specs {
		id := uuid.New()
		accounts[key] = id
		if err := driver.CreateAccount(ctx, masterdata.Account{
			CompanyID: companyID, ID: id, Code: spec.code, Name: spec.name,
			AccountType: spec.kind, NormalBalance: spec.normal, IsActive: true,
		}); err != nil {
			t.Fatal(err)
		}
	}

	year := masterdata.GenerateFiscalYear(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = companyID
	if err := driver.CreateFiscalYear(ctx, year); err != nil {
		t.Fatal(err)
	}

	engine, err := accountingengine.New(driver, accountingengine.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	ledger := engine.ForCompany(companyID).FixedAssets

	money := func(value string) financialtypes.Money {
		t.Helper()
		amount, err := financialtypes.NewMoneyFromString(value, "USD")
		if err != nil {
			t.Fatal(err)
		}
		return amount
	}
	date := func(year int, month time.Month, day int) time.Time {
		return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	}

	assetID, journalID := uuid.New(), uuid.New()
	asset := subledgers.FixedAsset{
		CompanyID: companyID, ID: assetID,
		AssetAccountID: accounts["asset"], AccumulatedAccountID: accounts["accumulated"],
		ExpenseAccountID:    accounts["dep_expense"],
		ImpairmentAccountID: accounts["imp_expense"], AccumulatedImpairmentAccountID: accounts["imp_accum"],
		RevaluationSurplusAccountID: accounts["surplus"], RevaluationLossAccountID: accounts["reval_loss"],
		DisposalCashAccountID: accounts["cash"], DisposalGainAccountID: accounts["gain"],
		DisposalLossAccountID: accounts["loss"],
		HistoricalCost:        money("36500"), SalvageValue: money("0"),
		UsefulLifeMonths: 12, Method: subledgers.StraightLine,
		InServiceDate: date(2026, 1, 1),
	}
	if err := ledger.RegisterAssetContext(ctx, asset); err != nil {
		t.Fatalf("RegisterAssetContext: %v", err)
	}

	// 1) IAS 16 monthly depreciation for January and February.
	for _, month := range []time.Time{date(2026, 1, 31), date(2026, 2, 28)} {
		entry, err := ledger.DepreciateAssetAtomic(ctx, engine.Posting, assetID, journalID, month, "poster")
		if err != nil {
			t.Fatalf("DepreciateAssetAtomic %s: %v", month.Format("2006-01"), err)
		}
		if entry.Status != posting.EntryPosted {
			t.Fatalf("depreciation entry status = %s, want posted", entry.Status)
		}
		if len(entry.Lines) != 2 ||
			!entry.Lines[0].Debit.Amount().Equal(map[string]decimal.Decimal{
				"2026-01": decimal.NewFromInt(3100),
				"2026-02": decimal.NewFromInt(2800),
			}[month.Format("2006-01")]) ||
			!entry.Lines[1].Credit.Amount().Equal(entry.Lines[0].Debit.Amount()) {
			t.Fatalf("depreciation entry %s has unexpected amount: %+v", month.Format("2006-01"), entry.Lines)
		}
	}
	afterDepreciation, err := driver.GetFixedAsset(ctx, companyID, assetID)
	if err != nil {
		t.Fatal(err)
	}
	if !afterDepreciation.AccumulatedDepreciation.Amount().Equal(decimal.NewFromInt(5900)) ||
		!afterDepreciation.CurrentBookValue.Amount().Equal(decimal.NewFromInt(30600)) {
		t.Fatalf("after depreciation: accumulated=%s book=%s, want 5900 and 30600",
			afterDepreciation.AccumulatedDepreciation.Amount(), afterDepreciation.CurrentBookValue.Amount())
	}

	// 2) IAS 16 upward revaluation from 30600 to 33600, recognised in OCI.
	revaluationDate := date(2026, 3, 1)
	revaluation, err := ledger.RevalueAssetContext(ctx, assetID, journalID, money("33600"), revaluationDate)
	if err != nil {
		t.Fatalf("RevalueAssetContext: %v", err)
	}
	if len(revaluation.Lines) != 2 ||
		revaluation.Lines[0].AccountID != accounts["asset"] ||
		!revaluation.Lines[0].Debit.Amount().Equal(decimal.NewFromInt(3000)) ||
		revaluation.Lines[1].AccountID != accounts["surplus"] ||
		!revaluation.Lines[1].Credit.Amount().Equal(decimal.NewFromInt(3000)) {
		t.Fatalf("revaluation increase was not recognised in OCI: %+v", revaluation.Lines)
	}
	if err := engine.Posting.PostEntryContext(ctx, &revaluation, "poster"); err != nil {
		t.Fatalf("post revaluation: %v", err)
	}
	if err := ledger.ConfirmRevaluationContext(ctx, assetID, revaluation.ID, money("33600"), revaluationDate); err != nil {
		t.Fatalf("ConfirmRevaluationContext: %v", err)
	}
	afterRevaluation, err := driver.GetFixedAsset(ctx, companyID, assetID)
	if err != nil {
		t.Fatal(err)
	}
	if !afterRevaluation.CurrentBookValue.Amount().Equal(decimal.NewFromInt(33600)) ||
		!afterRevaluation.RevaluationAdjustment.Amount().Equal(decimal.NewFromInt(3000)) ||
		!afterRevaluation.RevaluationSurplus.Amount().Equal(decimal.NewFromInt(3000)) {
		t.Fatalf("after revaluation: book=%s adjustment=%s surplus=%s, want 33600, 3000, 3000",
			afterRevaluation.CurrentBookValue.Amount(), afterRevaluation.RevaluationAdjustment.Amount(),
			afterRevaluation.RevaluationSurplus.Amount())
	}

	// 3) IAS 36 impairment from 33600 to a 29600 recoverable amount.
	impairmentDate := date(2026, 3, 31)
	impairment, err := ledger.PostImpairmentAtomic(
		ctx, engine.Posting, assetID, money("29600"), journalID, impairmentDate, "poster")
	if err != nil {
		t.Fatalf("PostImpairmentAtomic: %v", err)
	}
	if impairment.Status != posting.EntryPosted {
		t.Fatalf("impairment entry status = %s, want posted", impairment.Status)
	}
	var impairmentLoss financialtypes.Money
	for _, line := range impairment.Lines {
		if line.AccountID == accounts["imp_expense"] {
			impairmentLoss = line.Debit
		}
	}
	if !impairmentLoss.Amount().Equal(decimal.NewFromInt(4000)) {
		t.Fatalf("impairment loss = %s, want 4000", impairmentLoss.Amount())
	}
	afterImpairment, err := driver.GetFixedAsset(ctx, companyID, assetID)
	if err != nil {
		t.Fatal(err)
	}
	if !afterImpairment.CurrentBookValue.Amount().Equal(decimal.NewFromInt(29600)) ||
		!afterImpairment.AccumulatedImpairment.Amount().Equal(decimal.NewFromInt(4000)) {
		t.Fatalf("after impairment: book=%s accumulated impairment=%s, want 29600 and 4000",
			afterImpairment.CurrentBookValue.Amount(), afterImpairment.AccumulatedImpairment.Amount())
	}

	// 4) IAS 16 disposal at 30100 proceeds, a 500 gain on the 29600 carrying amount.
	disposalDate := date(2026, 4, 15)
	disposal, err := ledger.DisposeAssetAtomic(
		ctx, engine.Posting, assetID, disposalDate, money("30100"), journalID, "poster")
	if err != nil {
		t.Fatalf("DisposeAssetAtomic: %v", err)
	}
	if disposal.Status != posting.EntryPosted {
		t.Fatalf("disposal entry status = %s, want posted", disposal.Status)
	}
	var gain, cash financialtypes.Money
	for _, line := range disposal.Lines {
		switch line.AccountID {
		case accounts["gain"]:
			gain = line.Credit
		case accounts["cash"]:
			cash = line.Debit
		}
	}
	if !gain.Amount().Equal(decimal.NewFromInt(500)) || !cash.Amount().Equal(decimal.NewFromInt(30100)) {
		t.Fatalf("disposal gain=%s cash=%s, want 500 and 30100", gain.Amount(), cash.Amount())
	}
	disposed, err := driver.GetFixedAsset(ctx, companyID, assetID)
	if err != nil {
		t.Fatal(err)
	}
	if disposed.DisposedAt == nil || !disposed.DisposedAt.Equal(disposalDate) ||
		disposed.PendingOperation != "" || disposed.PendingOperationEntryID != uuid.Nil {
		t.Fatalf("disposal did not atomically finalize the asset: %+v", disposed)
	}

	// The whole lifecycle must remain balanced in the general ledger.
	trialBalance, err := engine.Reporting.TrialBalance.GenerateForCompanyContext(
		ctx, companyID, date(2026, 1, 1), date(2026, 12, 31), "USD")
	if err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	if !trialBalance.Balanced || !trialBalance.DebitTotal.Equal(trialBalance.CreditTotal) {
		t.Fatalf("trial balance after lifecycle: balanced=%v debits=%s credits=%s",
			trialBalance.Balanced, trialBalance.DebitTotal, trialBalance.CreditTotal)
	}
}
