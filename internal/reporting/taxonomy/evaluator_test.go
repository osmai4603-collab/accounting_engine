package taxonomy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type balanceReaderFunc func(context.Context, uuid.UUID, uuid.UUID, string) (PeriodBalances, error)

func (f balanceReaderFunc) GetPeriodBalances(ctx context.Context, companyID, periodID uuid.UUID, currency string) (PeriodBalances, error) {
	return f(ctx, companyID, periodID, currency)
}

func TestBuiltInIncomeStatementsAgreeAcrossPresentations(t *testing.T) {
	accounts := []AccountBalance{
		testAccount("4000", 100, "revenue"),
		testAccount("5000", 20, "cost_of_sales", "materials_consumed"),
		testAccount("6100", 5, "other_operating_expense"),
		testAccount("6200", 10, "administrative_expense", "employee_expense"),
		testAccount("7100", 2, "finance_cost"),
		testAccount("8000", 5, "income_tax"),
		testAccount("9000", 3, "oci"),
	}
	reader := balanceReaderFunc(func(context.Context, uuid.UUID, uuid.UUID, string) (PeriodBalances, error) {
		return PeriodBalances{Accounts: accounts}, nil
	})
	evaluator := NewTaxonomyEvaluator(reader)
	function, _ := BuiltInTemplate(TemplateIFRSFunctionIncome)
	nature, _ := BuiltInTemplate(TemplateIFRSNatureIncome)

	functionReport, err := evaluator.EvaluateStatement(context.Background(), uuid.New(), function, uuid.New(), "SAR")
	if err != nil {
		t.Fatal(err)
	}
	natureReport, err := evaluator.EvaluateStatement(context.Background(), uuid.New(), nature, uuid.New(), "SAR")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := lineAmount(t, functionReport, "GROSS_PROFIT"), decimal.NewFromInt(80); !got.Equal(want) {
		t.Fatalf("function gross profit = %s, want %s", got, want)
	}
	if got, want := lineAmount(t, functionReport, "OPERATING_PROFIT"), decimal.NewFromInt(65); !got.Equal(want) {
		t.Fatalf("function operating profit = %s, want %s", got, want)
	}
	if got, want := lineAmount(t, functionReport, "NET_PROFIT"), decimal.NewFromInt(58); !got.Equal(want) {
		t.Fatalf("function net profit = %s, want %s", got, want)
	}
	if got, want := lineAmount(t, natureReport, "NET_PROFIT"), lineAmount(t, functionReport, "NET_PROFIT"); !got.Equal(want) {
		t.Fatalf("nature net profit = %s, function net profit = %s", got, want)
	}
}

func TestFormulaNodeSupportsPrecedenceParenthesesAndDecimals(t *testing.T) {
	definition := testDefinition([]*ReportNode{
		formulaNode("TEST", "BASE", "الأساس", "Base", 1, "100 - 20"),
		formulaNode("TEST", "EBITDA", "الأرباح قبل الفوائد والضرائب والإهلاك", "EBITDA", 2, "(BASE - 5) * 2 / 3"),
	})
	report, err := NewTaxonomyEvaluator(emptyBalanceReader()).EvaluateStatement(
		context.Background(), uuid.New(), definition, uuid.New(), "USD",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := lineAmount(t, report, "EBITDA"), decimal.NewFromInt(50); !got.Equal(want) {
		t.Fatalf("EBITDA = %s, want %s", got, want)
	}
}

func TestSubtotalNodeSumsDirectChildrenAndCombinesDrilldowns(t *testing.T) {
	first := testAccount("4100", 10, "sales")
	second := testAccount("4200", 20, "sales")
	subtotal := &ReportNode{
		Code: "SALES_TOTAL", TitleAr: "إجمالي المبيعات", TitleEn: "Sales total",
		NodeType: NodeSubtotal, DisplayOrder: 1, Children: []*ReportNode{
			tagNode("TEST", "SALES_A", "مبيعات أ", "Sales A", 1, "sales"),
			tagNode("TEST", "SALES_B", "مبيعات ب", "Sales B", 2, "sales"),
		},
	}
	// Each account is intentionally tagged only to one child node.
	subtotal.Children[0].AccountTags = []string{"sales_a"}
	subtotal.Children[1].AccountTags = []string{"sales_b"}
	first.Tags = []string{"sales_a"}
	second.Tags = []string{"sales_b"}
	definition := testDefinition([]*ReportNode{subtotal})
	report, err := NewTaxonomyEvaluator(balanceReaderFunc(func(context.Context, uuid.UUID, uuid.UUID, string) (PeriodBalances, error) {
		return PeriodBalances{Accounts: []AccountBalance{first, second}}, nil
	})).EvaluateStatement(context.Background(), uuid.New(), definition, uuid.New(), "SAR")
	if err != nil {
		t.Fatal(err)
	}
	line := findLine(t, report, "SALES_TOTAL")
	if !line.Amount.Equal(decimal.NewFromInt(30)) || len(line.DrilldownIDs) != 2 {
		t.Fatalf("subtotal = %+v, want amount 30 and both contributing account ids", line)
	}
}

func TestTaxonomyCanAddAccountTagNodeWithoutCodeChanges(t *testing.T) {
	definition := testDefinition([]*ReportNode{
		tagNode("TEST", "CUSTOM_REVENUE", "إيرادات مخصصة", "Custom revenue", 10, "new_reporting_tag"),
	})
	account := testAccount("4100", 42, "new_reporting_tag")
	report, err := NewTaxonomyEvaluator(balanceReaderFunc(func(context.Context, uuid.UUID, uuid.UUID, string) (PeriodBalances, error) {
		return PeriodBalances{Accounts: []AccountBalance{account}}, nil
	})).EvaluateStatement(context.Background(), uuid.New(), definition, uuid.New(), "SAR")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := lineAmount(t, report, "CUSTOM_REVENUE"), decimal.NewFromInt(42); !got.Equal(want) {
		t.Fatalf("custom node amount = %s, want %s", got, want)
	}
	if got := report.Lines[0].DrilldownIDs; len(got) != 1 || got[0] != account.AccountID {
		t.Fatalf("custom node drilldown ids = %v, want [%s]", got, account.AccountID)
	}
}

func TestIFRSBalanceSheetBalances(t *testing.T) {
	accounts := []AccountBalance{
		testAccount("1000", 150, "current_asset"),
		testAccount("2000", 40, "current_liability"),
		testAccount("3000", 110, "equity"),
	}
	for i := range accounts {
		accounts[i].EndingAmount = accounts[i].Amount
		accounts[i].PeriodAmount = decimal.Zero
	}
	reader := balanceReaderFunc(func(context.Context, uuid.UUID, uuid.UUID, string) (PeriodBalances, error) {
		return PeriodBalances{Accounts: accounts}, nil
	})
	definition, _ := BuiltInTemplate(TemplateIFRSBalanceSheet)
	report, err := NewTaxonomyEvaluator(reader).EvaluateStatement(
		context.Background(), uuid.New(), definition, uuid.New(), "SAR",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := lineAmount(t, report, "TOTAL_ASSETS"), lineAmount(t, report, "TOTAL_LIABILITIES_AND_EQUITY"); !got.Equal(want) {
		t.Fatalf("assets = %s, liabilities and equity = %s", got, want)
	}
}

func TestEvaluatedLinesIncludeArabicAndEnglishTitles(t *testing.T) {
	definition, _ := BuiltInTemplate(TemplateIFRSFunctionIncome)
	report, err := NewTaxonomyEvaluator(emptyBalanceReader()).EvaluateStatement(
		context.Background(), uuid.New(), definition, uuid.New(), "SAR",
	)
	if err != nil {
		t.Fatal(err)
	}
	line := findLine(t, report, "REVENUE")
	if line.TitleAr == "" || line.TitleEn == "" {
		t.Fatalf("localized titles are missing: %+v", line)
	}
}

func TestEvaluatorRejectsUnknownFormulaReferencesAndDivisionByZero(t *testing.T) {
	for _, expression := range []string{"MISSING + 1", "10 / (2 - 2)"} {
		definition := testDefinition([]*ReportNode{
			formulaNode("TEST", "RESULT", "نتيجة", "Result", 1, expression),
		})
		if _, err := NewTaxonomyEvaluator(emptyBalanceReader()).EvaluateStatement(
			context.Background(), uuid.New(), definition, uuid.New(), "USD",
		); err == nil {
			t.Errorf("EvaluateStatement(%q) succeeded, want an error", expression)
		}
	}
}

func TestFormulaParserLimitsInputAndNesting(t *testing.T) {
	if _, err := evaluateFormula(strings.Repeat("1", maxFormulaLength+1), nil); err == nil {
		t.Fatal("oversized formula was accepted")
	}
	if _, err := evaluateFormula(strings.Repeat("-", maxFormulaDepth+1)+"1", nil); err == nil {
		t.Fatal("overly nested formula was accepted")
	}
}

func TestEvaluatorPropagatesBalanceReaderAndContextErrors(t *testing.T) {
	readerErr := errors.New("database unavailable")
	definition := testDefinition([]*ReportNode{
		tagNode("TEST", "VALUE", "قيمة", "Value", 1, "value"),
	})
	reader := balanceReaderFunc(func(context.Context, uuid.UUID, uuid.UUID, string) (PeriodBalances, error) {
		return PeriodBalances{}, readerErr
	})
	if _, err := NewTaxonomyEvaluator(reader).EvaluateStatement(context.Background(), uuid.New(), definition, uuid.New(), "SAR"); !errors.Is(err, readerErr) {
		t.Fatalf("reader error = %v, want wrapped %v", err, readerErr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewTaxonomyEvaluator(emptyBalanceReader()).EvaluateStatement(ctx, uuid.New(), definition, uuid.New(), "SAR"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context error = %v, want context.Canceled", err)
	}
}

func TestBuiltInTemplatesAreValidAndStable(t *testing.T) {
	templates := BuiltInTemplates()
	if len(templates) < 3 {
		t.Fatalf("got %d built-in templates, want at least 3", len(templates))
	}
	for _, definition := range templates {
		if err := ValidateDefinition(definition); err != nil {
			t.Errorf("template %s is invalid: %v", definition.Code, err)
		}
	}
	first := BuiltInTemplates()
	if first[0].ID != templates[0].ID || first[0].RootNodes[0].ID != templates[0].RootNodes[0].ID {
		t.Fatal("built-in template identifiers are not stable")
	}
}

func TestAccountRangeNodeMatchesInclusiveLexicalBounds(t *testing.T) {
	definition := testDefinition([]*ReportNode{{
		Code: "RANGE", TitleAr: "النطاق", TitleEn: "Range", NodeType: NodeAccountRange,
		AccountFrom: "1100", AccountTo: "1199", SignMultiplier: 1,
	}})
	accounts := []AccountBalance{
		testAccount("1100", 5),
		testAccount("1150", 7),
		testAccount("1200", 11),
	}
	reader := balanceReaderFunc(func(context.Context, uuid.UUID, uuid.UUID, string) (PeriodBalances, error) {
		return PeriodBalances{Accounts: accounts}, nil
	})
	report, err := NewTaxonomyEvaluator(reader).EvaluateStatement(
		context.Background(), uuid.New(), definition, uuid.New(), "USD",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := lineAmount(t, report, "RANGE"), decimal.NewFromInt(12); !got.Equal(want) {
		t.Fatalf("range amount = %s, want %s", got, want)
	}
}

func TestStatementSelectsPeriodOrEndingAmounts(t *testing.T) {
	account := testAccount("4000", 20, "revenue")
	account.PeriodAmount = decimal.NewFromInt(20)
	account.EndingAmount = decimal.NewFromInt(100)
	reader := balanceReaderFunc(func(context.Context, uuid.UUID, uuid.UUID, string) (PeriodBalances, error) {
		return PeriodBalances{Accounts: []AccountBalance{account}}, nil
	})
	income, _ := BuiltInTemplate(TemplateIFRSFunctionIncome)
	income.RootNodes = []*ReportNode{tagNode(income.Code, "VALUE", "قيمة", "Value", 1, "revenue")}
	balance := income
	balance.Type = StatementBalanceSheet

	incomeReport, err := NewTaxonomyEvaluator(reader).EvaluateStatement(context.Background(), uuid.New(), income, uuid.New(), "SAR")
	if err != nil {
		t.Fatal(err)
	}
	balanceReport, err := NewTaxonomyEvaluator(reader).EvaluateStatement(context.Background(), uuid.New(), balance, uuid.New(), "SAR")
	if err != nil {
		t.Fatal(err)
	}
	if !lineAmount(t, incomeReport, "VALUE").Equal(decimal.NewFromInt(20)) ||
		!lineAmount(t, balanceReport, "VALUE").Equal(decimal.NewFromInt(100)) {
		t.Fatalf("income/balance values = %s/%s, want 20/100",
			lineAmount(t, incomeReport, "VALUE"), lineAmount(t, balanceReport, "VALUE"))
	}
}

func testDefinition(nodes []*ReportNode) FinancialReportDefinition {
	return FinancialReportDefinition{
		ID: uuid.New(), Code: "TEST", Name: "Test", Framework: FrameworkIFRS,
		Type: StatementIncome, RootNodes: nodes, Version: 1, CreatedAt: time.Now().UTC(),
	}
}

func testAccount(code string, amount int64, tags ...string) AccountBalance {
	return AccountBalance{
		AccountID: uuid.New(),
		Code:      code,
		Tags:      tags,
		Amount:    decimal.NewFromInt(amount),
	}
}

func emptyBalanceReader() BalanceReader {
	return balanceReaderFunc(func(context.Context, uuid.UUID, uuid.UUID, string) (PeriodBalances, error) {
		return PeriodBalances{}, nil
	})
}

func lineAmount(t *testing.T, report *EvaluatedStatement, code string) decimal.Decimal {
	t.Helper()
	return findLine(t, report, code).Amount
}

func findLine(t *testing.T, report *EvaluatedStatement, code string) EvaluatedLine {
	t.Helper()
	for _, line := range report.Lines {
		if line.Code == code {
			return line
		}
	}
	t.Fatalf("report line %q not found", code)
	return EvaluatedLine{}
}
