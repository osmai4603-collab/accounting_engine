package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/subledgers"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
)

func TestP1002FixedAssetDepreciationRevaluationAndDisposalLifecycle(t *testing.T) {
	driver, _, engine, companyID, journalID, assetAccountID, accumulatedAccountID :=
		setupMemorySubledgerEnv(t)
	ctx := context.Background()

	createAccount := func(code, name string, kind masterdata.AccountType, balance masterdata.NormalBalance) uuid.UUID {
		t.Helper()
		id := uuid.New()
		if err := driver.CreateAccount(ctx, masterdata.Account{
			CompanyID: companyID, ID: id, Code: code, Name: name,
			AccountType: kind, NormalBalance: balance, IsActive: true,
		}); err != nil {
			t.Fatalf("create %s account: %v", name, err)
		}
		return id
	}
	expenseAccountID := createAccount("6100", "Depreciation Expense", masterdata.AccountTypeExpense, masterdata.NormalBalanceDebit)
	surplusAccountID := createAccount("3200", "Revaluation Surplus", masterdata.AccountTypeEquity, masterdata.NormalBalanceCredit)
	revaluationLossAccountID := createAccount("6200", "Revaluation Loss", masterdata.AccountTypeExpense, masterdata.NormalBalanceDebit)
	cashAccountID := createAccount("1000", "Cash", masterdata.AccountTypeAsset, masterdata.NormalBalanceDebit)
	disposalLossAccountID := createAccount("6300", "Disposal Loss", masterdata.AccountTypeExpense, masterdata.NormalBalanceDebit)

	money := func(value string) financialtypes.Money {
		t.Helper()
		result, err := financialtypes.NewMoneyFromString(value, "USD")
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	date := func(year int, month time.Month, day int) time.Time {
		return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	}
	assetID := uuid.New()
	ledger := subledgers.NewFixedAssetLedgerForCompany(driver, nil, companyID)
	asset := subledgers.FixedAsset{
		CompanyID: companyID, ID: assetID, AssetAccountID: assetAccountID,
		AccumulatedAccountID: accumulatedAccountID, ExpenseAccountID: expenseAccountID,
		RevaluationSurplusAccountID: surplusAccountID, RevaluationLossAccountID: revaluationLossAccountID,
		DisposalCashAccountID: cashAccountID, DisposalLossAccountID: disposalLossAccountID,
		HistoricalCost: money("12000"), SalvageValue: money("0"),
		UsefulLifeMonths: 12, Method: subledgers.StraightLine, InServiceDate: date(2026, 1, 1),
	}
	if err := ledger.RegisterAssetContext(ctx, asset); err != nil {
		t.Fatalf("register asset: %v", err)
	}

	depreciationDate := date(2026, 1, 31)
	if _, err := ledger.DepreciateAssetAdjustingAtomic(
		ctx, engine, assetID, journalID, depreciationDate, "poster",
		subledgers.FixedAssetAdjustmentApproval{},
	); !errors.Is(err, posting.ErrAdjustingEntryMetadataRequired) {
		t.Fatalf("missing depreciation approval error = %v, want explicit-approval error", err)
	}
	depreciation, err := ledger.DepreciateAssetAdjustingAtomic(
		ctx, engine, assetID, journalID, depreciationDate, "poster",
		subledgers.FixedAssetAdjustmentApproval{
			Authorized: true, Reason: "Monthly depreciation estimate",
			ApprovalReference: "FA-APP-2026-01", ApprovedBy: "controller",
			ApprovedAt: depreciationDate,
		},
	)
	if err != nil {
		t.Fatalf("post depreciation: %v", err)
	}
	if depreciation.Status != posting.EntryPosted ||
		depreciation.Classification != posting.EntryClassAdjusting ||
		depreciation.AdjustingEntryType != posting.AdjustingEntryProvisionEstimate ||
		len(depreciation.Lines) != 2 ||
		!depreciation.Lines[0].Debit.Equal(money("1019.18")) ||
		!depreciation.Lines[1].Credit.Equal(money("1019.18")) {
		t.Fatalf("unexpected depreciation entry: %+v", depreciation)
	}
	afterDepreciation, err := driver.GetFixedAsset(ctx, companyID, assetID)
	if err != nil {
		t.Fatal(err)
	}
	if !afterDepreciation.CurrentBookValue.Equal(money("10980.82")) ||
		!afterDepreciation.AccumulatedDepreciation.Equal(money("1019.18")) {
		t.Fatalf("after depreciation: accumulated=%s book=%s", afterDepreciation.AccumulatedDepreciation, afterDepreciation.CurrentBookValue)
	}

	revaluation, err := ledger.RevalueAssetAtomic(
		ctx, engine, assetID, journalID, money("13000"), date(2026, 2, 1), "poster",
	)
	if err != nil {
		t.Fatalf("post revaluation: %v", err)
	}
	if revaluation.Status != posting.EntryPosted || len(revaluation.Lines) != 2 ||
		revaluation.Lines[0].AccountID != assetAccountID ||
		!revaluation.Lines[0].Debit.Equal(money("2019.18")) ||
		revaluation.Lines[1].AccountID != surplusAccountID ||
		!revaluation.Lines[1].Credit.Equal(money("2019.18")) {
		t.Fatalf("unexpected revaluation entry: %+v", revaluation)
	}
	afterRevaluation, err := driver.GetFixedAsset(ctx, companyID, assetID)
	if err != nil {
		t.Fatal(err)
	}
	if !afterRevaluation.CurrentBookValue.Equal(money("13000")) ||
		!afterRevaluation.RevaluationSurplus.Equal(money("2019.18")) ||
		len(afterRevaluation.AppliedEntryIDs) != 1 ||
		afterRevaluation.AppliedEntryIDs[0] != revaluation.ID {
		t.Fatalf("revaluation was not atomically applied: %+v", afterRevaluation)
	}

	downwardRevaluation, err := ledger.RevalueAssetAtomic(
		ctx, engine, assetID, journalID, money("12500"), date(2026, 2, 28), "poster",
	)
	if err != nil {
		t.Fatalf("post downward revaluation: %v", err)
	}
	if downwardRevaluation.Status != posting.EntryPosted || len(downwardRevaluation.Lines) != 2 ||
		downwardRevaluation.Lines[0].AccountID != surplusAccountID ||
		!downwardRevaluation.Lines[0].Debit.Equal(money("500")) ||
		downwardRevaluation.Lines[1].AccountID != assetAccountID ||
		!downwardRevaluation.Lines[1].Credit.Equal(money("500")) {
		t.Fatalf("unexpected downward revaluation entry: %+v", downwardRevaluation)
	}
	afterDownwardRevaluation, err := driver.GetFixedAsset(ctx, companyID, assetID)
	if err != nil {
		t.Fatal(err)
	}
	if !afterDownwardRevaluation.CurrentBookValue.Equal(money("12500")) ||
		!afterDownwardRevaluation.RevaluationSurplus.Equal(money("1519.18")) ||
		len(afterDownwardRevaluation.AppliedEntryIDs) != 2 ||
		afterDownwardRevaluation.AppliedEntryIDs[1] != downwardRevaluation.ID {
		t.Fatalf("downward revaluation did not consume surplus atomically: %+v", afterDownwardRevaluation)
	}

	disposal, err := ledger.DisposeAssetAtomic(
		ctx, engine, assetID, date(2026, 3, 1), money("12000"), journalID, "poster",
	)
	if err != nil {
		t.Fatalf("post disposal: %v", err)
	}
	var cashProceeds, disposalLoss financialtypes.Money
	for _, line := range disposal.Lines {
		if line.AccountID == cashAccountID {
			cashProceeds = line.Debit
		}
		if line.AccountID == disposalLossAccountID {
			disposalLoss = line.Debit
		}
	}
	if disposal.Status != posting.EntryPosted ||
		!cashProceeds.Equal(money("12000")) ||
		!disposalLoss.Equal(money("500")) {
		t.Fatalf("unexpected disposal entry: %+v", disposal)
	}
	disposed, err := driver.GetFixedAsset(ctx, companyID, assetID)
	if err != nil {
		t.Fatal(err)
	}
	if disposed.DisposedAt == nil || !disposed.DisposedAt.Equal(date(2026, 3, 1)) ||
		disposed.PendingOperation != "" || disposed.PendingOperationEntryID != uuid.Nil ||
		len(disposed.AppliedEntryIDs) != 3 || disposed.AppliedEntryIDs[2] != disposal.ID {
		t.Fatalf("disposal was not atomically applied: %+v", disposed)
	}
}
