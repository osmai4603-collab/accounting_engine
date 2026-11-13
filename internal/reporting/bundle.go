package reporting

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/consolidation"
	"github.com/osmai4603-collab/accounting_engine/internal/subledgers"
	"github.com/shopspring/decimal"
)

var (
	ErrPPENotReconciledWithBalanceSheet = errors.New("reporting: PPE roll-forward ending balance does not match balance sheet non-current PPE assets")
)

type PPERollForwardReport struct {
	Currency           string
	BeginningBalance   decimal.Decimal
	Additions          decimal.Decimal
	Disposals          decimal.Decimal
	DepreciationCharge decimal.Decimal
	EndingBalance      decimal.Decimal
	Reconciled         bool
	Assets             []subledgers.FixedAsset
}

type AgedReceivableLine struct {
	LineID    uuid.UUID
	Amount    decimal.Decimal
	PartnerID string
	Reference string
	Bucket    subledgers.AgingBucket
}

type AgedReceivablesReport struct {
	Currency   string
	Current    decimal.Decimal
	Days1To30  decimal.Decimal
	Days31To60 decimal.Decimal
	Days61To90 decimal.Decimal
	Over90     decimal.Decimal
	Total      decimal.Decimal
	Lines      []AgedReceivableLine
}

type StatementBundle struct {
	BalanceSheet    BalanceSheet
	IncomeStatement IncomeStatement
	CashFlow        CashFlow
	EquityStatement EquityStatement
	TrialBalance    TrialBalance
	Ratios          FinancialRatios
	PPE             PPERollForwardReport
	AgedReceivables AgedReceivablesReport
}

type ConsolidatedStatementBundle struct {
	Statements    consolidation.Result
	TrialBalances []consolidation.TrialBalance
}

type ConsolidationAccessChecker interface {
	AuthorizeConsolidation(context.Context, string, []uuid.UUID) error
}

type BundleEngine struct {
	repository                 Repository
	consolidationAccessChecker ConsolidationAccessChecker
}

func NewBundleEngine(repository Repository) *BundleEngine {
	return &BundleEngine{repository: repository}
}

func (e *BundleEngine) SetConsolidationAccessChecker(checker ConsolidationAccessChecker) {
	e.consolidationAccessChecker = checker
}

func (e *BundleEngine) Generate(start, end time.Time, currency string) (StatementBundle, error) {
	balanceSheet, err := NewBalanceSheetEngine(e.repository).Generate(end, currency)
	if err != nil {
		return StatementBundle{}, fmt.Errorf("generate balance sheet: %w", err)
	}
	income, err := NewIncomeStatementEngine(e.repository).Generate(start, end, currency)
	if err != nil {
		return StatementBundle{}, fmt.Errorf("generate income statement: %w", err)
	}
	cashFlow, err := NewCashFlowEngine(e.repository).Generate(start, end, currency)
	if err != nil {
		return StatementBundle{}, fmt.Errorf("generate cash flow: %w", err)
	}
	equity, err := NewEquityStatementEngine(e.repository).Generate(start, end, currency)
	if err != nil {
		return StatementBundle{}, fmt.Errorf("generate equity statement: %w", err)
	}
	trial, err := NewTrialBalanceEngine(e.repository).Generate(start, end, currency)
	if err != nil {
		return StatementBundle{}, fmt.Errorf("generate trial balance: %w", err)
	}
	ratios, err := NewRatiosEngine(e.repository).Generate(start, end, currency)
	if err != nil {
		return StatementBundle{}, fmt.Errorf("generate ratios: %w", err)
	}

	ppe, err := e.ppeReport(start, end, currency, balanceSheet)
	if err != nil {
		return StatementBundle{}, fmt.Errorf("generate PPE roll-forward: %w", err)
	}

	receivables, err := e.receivablesReport(end, currency)
	if err != nil {
		return StatementBundle{}, fmt.Errorf("generate aged receivables: %w", err)
	}

	return StatementBundle{
		BalanceSheet: balanceSheet, IncomeStatement: income, CashFlow: cashFlow,
		EquityStatement: equity, TrialBalance: trial, Ratios: ratios,
		PPE: ppe, AgedReceivables: receivables,
	}, nil
}

func (e *BundleEngine) GenerateForCompany(companyID uuid.UUID, start, end time.Time, currency string) (StatementBundle, error) {
	return e.GenerateForCompanyContext(context.Background(), companyID, start, end, currency)
}

func (e *BundleEngine) GenerateForCompanyContext(ctx context.Context, companyID uuid.UUID, start, end time.Time, currency string) (StatementBundle, error) {
	repository, err := newCompanyReportingRepository(ctx, e.repository, companyID, end)
	if err != nil {
		return StatementBundle{}, err
	}
	return NewBundleEngine(repository).Generate(start, end, currency)
}

func (e *BundleEngine) ppeReport(start, end time.Time, currency string, sheet BalanceSheet) (PPERollForwardReport, error) {
	report := PPERollForwardReport{
		Currency:           currency,
		BeginningBalance:   decimal.Zero,
		Additions:          decimal.Zero,
		Disposals:          decimal.Zero,
		DepreciationCharge: decimal.Zero,
		EndingBalance:      decimal.Zero,
		Assets:             make([]subledgers.FixedAsset, 0),
	}
	assets, err := e.repository.ListFixedAssets(context.Background())
	if err != nil {
		return report, fmt.Errorf("list fixed assets: %w", err)
	}

	for _, asset := range assets {
		if asset.HistoricalCost.Currency() != currency {
			continue
		}
		report.Assets = append(report.Assets, asset)

		cost := asset.HistoricalCost.Amount()
		accum := asset.AccumulatedDepreciation.Amount()
		bookVal := asset.CurrentBookValue.Amount()

		if !asset.InServiceDate.After(start) {
			report.BeginningBalance = report.BeginningBalance.Add(cost)
		} else if !asset.InServiceDate.After(end) {
			report.Additions = report.Additions.Add(cost)
		}

		report.DepreciationCharge = report.DepreciationCharge.Add(accum)
		report.EndingBalance = report.EndingBalance.Add(bookVal)
	}

	if len(report.Assets) > 0 {
		expectedEnding := report.BeginningBalance.Add(report.Additions).Sub(report.Disposals).Sub(report.DepreciationCharge)
		if report.EndingBalance.Equal(expectedEnding) {
			report.Reconciled = true
		}
	} else {
		report.Reconciled = true
	}

	return report, nil
}

func (e *BundleEngine) receivablesReport(asOf time.Time, currency string) (AgedReceivablesReport, error) {
	report := AgedReceivablesReport{Currency: currency, Lines: make([]AgedReceivableLine, 0)}
	lines, err := e.repository.ListOpenLines(context.Background(), "")
	if err != nil {
		return report, fmt.Errorf("list open lines: %w", err)
	}
	for _, line := range lines {
		if line.Kind != subledgers.LineReceivable || line.Closed || line.ResidualAmount.Currency() != currency {
			continue
		}
		age := asOf.Sub(line.DueDate)
		bucket := subledgers.AgingBucket("current")
		if age > 90*24*time.Hour {
			bucket = subledgers.AgingOver90
		} else if age > 60*24*time.Hour {
			bucket = subledgers.Aging61To90
		} else if age > 30*24*time.Hour {
			bucket = subledgers.Aging31To60
		} else if age > 0 {
			bucket = subledgers.Aging1To30
		}
		amount := line.ResidualAmount.Amount()
		report.Total = report.Total.Add(amount)
		switch bucket {
		case subledgers.AgingCurrent:
			report.Current = report.Current.Add(amount)
		case subledgers.Aging1To30:
			report.Days1To30 = report.Days1To30.Add(amount)
		case subledgers.Aging31To60:
			report.Days31To60 = report.Days31To60.Add(amount)
		case subledgers.Aging61To90:
			report.Days61To90 = report.Days61To90.Add(amount)
		case subledgers.AgingOver90:
			report.Over90 = report.Over90.Add(amount)
		}
		report.Lines = append(report.Lines, AgedReceivableLine{
			LineID: line.ID, Amount: amount, PartnerID: linePartner(line),
			Reference: line.Reference, Bucket: bucket,
		})
	}
	return report, nil
}

func linePartner(line subledgers.OpenLine) string {
	if line.PartnerID == nil {
		return ""
	}
	return line.PartnerID.String()
}
