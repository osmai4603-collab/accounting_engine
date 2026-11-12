package reporting_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"accounting_engine/internal/consolidation"
	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/reporting"
	"accounting_engine/internal/valuation"
	"accounting_engine/storage/memory"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type allowConsolidation struct {
	err error
}

func (a allowConsolidation) AuthorizeConsolidation(context.Context, string, []uuid.UUID) error {
	return a.err
}

func TestGenerateConsolidatedBuildsScopedTrialBalances(t *testing.T) {
	ctx := context.Background()
	repository := memory.New()
	parentID, subsidiaryID := uuid.New(), uuid.New()
	parentCashID, parentInvestmentID, subsidiaryEquityID := uuid.New(), uuid.New(), uuid.New()
	start := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, time.March, 31, 0, 0, 0, 0, time.UTC)
	accounts := []masterdata.Account{
		reportAccount(parentCashID, "1000", "Cash", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents),
		reportAccount(parentInvestmentID, "1500", "Investment", masterdata.AccountTypeAsset, masterdata.SubClassFinancialInvestments),
		reportAccount(subsidiaryEquityID, "3000", "Share capital", masterdata.AccountTypeEquity, masterdata.SubClassShareCapital),
	}
	accounts[0].CompanyID, accounts[1].CompanyID = parentID, parentID
	accounts[2].CompanyID = subsidiaryID
	for _, account := range accounts {
		if err := repository.CreateAccount(ctx, account); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.AddRate(valuation.ExchangeRate{
		FromCurrency: "EUR", ToCurrency: "USD", Rate: decimal.NewFromFloat(1.1),
		EffectiveDate: start, Type: valuation.SpotRate,
	}); err != nil {
		t.Fatal(err)
	}

	group := consolidation.Group{
		ID: "group-1", ParentEntityID: parentID.String(), ReportingCurrency: "USD",
		Period: consolidation.ReportingPeriod{StartDate: start, EndDate: end},
		Entities: []consolidation.Entity{
			{ID: parentID.String(), FunctionalCurrency: "USD"},
			{ID: subsidiaryID.String(), FunctionalCurrency: "EUR"},
		},
		Accounts: []consolidation.GroupAccount{
			{ID: "cash", Name: "Cash", Type: consolidation.AccountAsset},
			{ID: "investment", Name: "Investment", Type: consolidation.AccountAsset},
			{ID: "sub-equity", Name: "Share capital", Type: consolidation.AccountEquity},
			{ID: "goodwill", Name: "Goodwill", Type: consolidation.AccountAsset},
			{ID: "nci", Name: "NCI", Type: consolidation.AccountEquity},
			{ID: "parent-retained", Name: "Parent retained earnings", Type: consolidation.AccountEquity},
		},
		Mappings: []consolidation.AccountMapping{
			{EntityID: parentID.String(), SourceAccountID: parentCashID.String(), GroupAccountID: "cash", RateClass: consolidation.RateClosing},
			{EntityID: parentID.String(), SourceAccountID: parentInvestmentID.String(), GroupAccountID: "investment", RateClass: consolidation.RateClosing},
			{EntityID: subsidiaryID.String(), SourceAccountID: subsidiaryEquityID.String(), GroupAccountID: "sub-equity", RateClass: consolidation.RateHistorical, HistoricalDate: start},
		},
		Ownerships: []consolidation.Ownership{{
			ParentEntityID: parentID.String(), SubsidiaryEntityID: subsidiaryID.String(),
			Percentage: decimal.NewFromFloat(0.8), AcquisitionDate: start,
			InvestmentAccountID: parentInvestmentID.String(), GoodwillAccountID: "goodwill",
			NCIAccountID: "nci", ParentRetainedEarningsAccountID: "parent-retained",
		}},
	}
	engine := reporting.NewBundleEngine(repository)
	engine.SetConsolidationAccessChecker(allowConsolidation{})
	bundle, err := engine.GenerateConsolidated(ctx, group)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.TrialBalances) != 2 || bundle.Statements.GroupID != group.ID ||
		bundle.Statements.Currency != "USD" || !bundle.Statements.Totals.Balanced {
		t.Fatalf("consolidated bundle = %+v", bundle)
	}
}

func TestGenerateConsolidatedFailsClosedWithoutAuthorization(t *testing.T) {
	group := consolidation.Group{
		ID: "group", Entities: []consolidation.Entity{{ID: uuid.NewString(), FunctionalCurrency: "USD"}},
	}
	_, err := reporting.NewBundleEngine(memory.New()).GenerateConsolidated(context.Background(), group)
	if !errors.Is(err, reporting.ErrConsolidationAccessCheckerRequired) {
		t.Fatalf("missing authorization error = %v", err)
	}
	denied := errors.New("not authorized")
	engine := reporting.NewBundleEngine(memory.New())
	engine.SetConsolidationAccessChecker(allowConsolidation{err: denied})
	_, err = engine.GenerateConsolidated(context.Background(), group)
	if !errors.Is(err, denied) {
		t.Fatalf("denied authorization error = %v", err)
	}
}


func reportAccount(id uuid.UUID, code, name string, accountType masterdata.AccountType, classification string) masterdata.Account {
	account := masterdata.Account{
		ID: id, Code: code, Name: name, AccountType: accountType,
		NormalBalance: masterdata.DefaultNormalBalance(accountType), SubClassification: classification,
	}
	switch accountType {
	case masterdata.AccountTypeIncome, masterdata.AccountTypeExpense:
		account.IFRSCategory = masterdata.IFRSCategoryOperating
		account.CashFlowActivity = masterdata.CashFlowOperating
	case masterdata.AccountTypeEquity:
		account.IFRSCategory = masterdata.IFRSCategoryBalanceSheetPerpetual
		account.CashFlowActivity = masterdata.CashFlowFinancing
	case masterdata.AccountTypeAsset:
		account.IFRSCategory = masterdata.IFRSCategoryBalanceSheetPerpetual
	}
	return account
}
