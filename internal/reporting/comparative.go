package reporting

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"accounting_engine/internal/masterdata"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var ErrInvalidComparativePeriods = errors.New("current and comparative reporting periods are required")

type ComparativeStatementLine struct {
	LineCode             string
	Description          string
	CurrentAmount        decimal.Decimal
	PriorAmount          decimal.Decimal
	Variance             decimal.Decimal
	VariancePct          decimal.Decimal
	VariancePctAvailable bool
}

type ComparativeBalanceSheet struct {
	CurrentPeriodDate time.Time
	PriorPeriodDate   time.Time
	Lines             []ComparativeStatementLine
	Currency          string
}

type ComparativeIncomeStatement struct {
	CurrentPeriodStart time.Time
	CurrentPeriodEnd   time.Time
	PriorPeriodStart   time.Time
	PriorPeriodEnd     time.Time
	Lines              []ComparativeStatementLine
	Currency           string
}

type ComparativeCashFlow struct {
	Current CashFlow
	Prior   CashFlow
	Lines   []ComparativeStatementLine
}

type ComparativeEquityMovement struct {
	AccountID                uuid.UUID
	AccountCode              string
	AccountName              string
	Opening                  ComparativeStatementLine
	NetIncome                ComparativeStatementLine
	OtherComprehensiveIncome ComparativeStatementLine
	Distributions            ComparativeStatementLine
	TransfersToReserve       ComparativeStatementLine
	Closing                  ComparativeStatementLine
}

type ComparativeEquityStatement struct {
	Current   EquityStatement
	Prior     EquityStatement
	Movements []ComparativeEquityMovement
}

type NoteDisclosure struct {
	ID            uuid.UUID
	CompanyID     uuid.UUID
	ReportCode    string
	StatementDate time.Time
	Code          string
	Title         string
	Description   string
	Amount        decimal.Decimal
	Currency      string
}

type NoteDisclosureRepository interface {
	SaveFinancialStatementNote(context.Context, uuid.UUID, NoteDisclosure) error
	ListFinancialStatementNotesForCompanyContext(context.Context, uuid.UUID, string, time.Time) ([]NoteDisclosure, error)
}

type NoteDisclosureUnit struct {
	repository NoteDisclosureRepository
}

func NewNoteDisclosureUnit(repository NoteDisclosureRepository) *NoteDisclosureUnit {
	return &NoteDisclosureUnit{repository: repository}
}

func (u *NoteDisclosureUnit) Save(ctx context.Context, companyID uuid.UUID, note NoteDisclosure) error {
	if ctx == nil {
		return errors.New("note disclosure context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if u == nil || u.repository == nil {
		return errors.New("note disclosure repository is unavailable")
	}
	if companyID == uuid.Nil || strings.TrimSpace(note.ReportCode) == "" ||
		note.StatementDate.IsZero() || strings.TrimSpace(note.Code) == "" ||
		strings.TrimSpace(note.Title) == "" || strings.TrimSpace(note.Currency) == "" {
		return errors.New("note disclosure requires company, report, date, code, title, and currency")
	}
	note.CompanyID = companyID
	return u.repository.SaveFinancialStatementNote(ctx, companyID, note)
}

func (u *NoteDisclosureUnit) List(
	ctx context.Context,
	companyID uuid.UUID,
	reportCode string,
	statementDate time.Time,
) ([]NoteDisclosure, error) {
	if ctx == nil {
		return nil, errors.New("note disclosure context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if u == nil || u.repository == nil || companyID == uuid.Nil ||
		strings.TrimSpace(reportCode) == "" || statementDate.IsZero() {
		return nil, errors.New("note disclosure scope is incomplete")
	}
	return u.repository.ListFinancialStatementNotesForCompanyContext(ctx, companyID, reportCode, statementDate)
}

type SegmentDisclosure struct {
	SegmentID uuid.UUID
	Currency  string
	Revenue   decimal.Decimal
	Expenses  decimal.Decimal
	Profit    decimal.Decimal
}

type ComparativeSegmentDisclosure struct {
	SegmentID uuid.UUID
	Revenue   ComparativeStatementLine
	Expenses  ComparativeStatementLine
	Profit    ComparativeStatementLine
}

type ComparativeStatementBundle struct {
	BalanceSheet    ComparativeBalanceSheet
	IncomeStatement ComparativeIncomeStatement
	CashFlow        ComparativeCashFlow
	EquityStatement ComparativeEquityStatement
	Notes           []NoteDisclosure
	Segments        []ComparativeSegmentDisclosure
}

func comparativeLine(code, description string, current, prior decimal.Decimal) ComparativeStatementLine {
	variance := current.Sub(prior)
	line := ComparativeStatementLine{
		LineCode: code, Description: description, CurrentAmount: current,
		PriorAmount: prior, Variance: variance,
	}
	if !prior.IsZero() {
		line.VariancePct = variance.Div(prior.Abs()).Mul(decimal.NewFromInt(100))
		line.VariancePctAvailable = true
	}
	return line
}

func comparativeBalanceSheetLines(current, prior BalanceSheet) []ComparativeStatementLine {
	values := []struct {
		code, description string
		current, prior    decimal.Decimal
	}{
		{"CURRENT_ASSETS", "Current assets", current.CurrentAssets, prior.CurrentAssets},
		{"NONCURRENT_ASSETS", "Non-current assets", current.NonCurrentAssets, prior.NonCurrentAssets},
		{"TOTAL_ASSETS", "Total assets", current.TotalAssets, prior.TotalAssets},
		{"CURRENT_LIABILITIES", "Current liabilities", current.CurrentLiabilities, prior.CurrentLiabilities},
		{"NONCURRENT_LIABILITIES", "Non-current liabilities", current.NonCurrentLiabilities, prior.NonCurrentLiabilities},
		{"TOTAL_LIABILITIES", "Total liabilities", current.TotalLiabilities, prior.TotalLiabilities},
		{"EQUITY", "Equity", current.Equity, prior.Equity},
		{"TRANSLATION_RESERVE", "Translation reserve", current.TranslationReserve, prior.TranslationReserve},
		{"TOTAL_LIABILITIES_AND_EQUITY", "Total liabilities and equity", current.TotalLiabilitiesAndEquity, prior.TotalLiabilitiesAndEquity},
	}
	lines := make([]ComparativeStatementLine, 0, len(values))
	for _, value := range values {
		lines = append(lines, comparativeLine(value.code, value.description, value.current, value.prior))
	}
	return lines
}

func comparativeIncomeStatementLines(current, prior IncomeStatement) []ComparativeStatementLine {
	values := []struct {
		code, description string
		current, prior    decimal.Decimal
	}{
		{"REVENUE", "Revenue", current.Revenue, prior.Revenue},
		{"INVESTING_INCOME", "Investing income", current.InvestingIncome, prior.InvestingIncome},
		{"FINANCING_INCOME", "Financing income", current.FinancingIncome, prior.FinancingIncome},
		{"COST_OF_SALES", "Cost of sales", current.CostOfSales, prior.CostOfSales},
		{"GROSS_PROFIT", "Gross profit", current.GrossProfit, prior.GrossProfit},
		{"SELLING_EXPENSES", "Selling expenses", current.SellingExpenses, prior.SellingExpenses},
		{"ADMINISTRATIVE_EXPENSES", "Administrative expenses", current.AdministrativeExpenses, prior.AdministrativeExpenses},
		{"DEPRECIATION_EXPENSES", "Depreciation and provision expenses", current.DepreciationAndProvisionExpenses, prior.DepreciationAndProvisionExpenses},
		{"OTHER_OPERATING_EXPENSES", "Other operating expenses", current.OtherOperatingExpenses, prior.OtherOperatingExpenses},
		{"OPERATING_PROFIT", "Operating profit", current.OperatingProfit, prior.OperatingProfit},
		{"PROFIT_BEFORE_TAX", "Profit before tax", current.ProfitBeforeTax, prior.ProfitBeforeTax},
		{"TAX_EXPENSE", "Tax expense", current.TaxExpense, prior.TaxExpense},
		{"NET_PROFIT", "Net profit", current.NetProfit, prior.NetProfit},
		{"OCI", "Other comprehensive income", current.OCI, prior.OCI},
		{"TOTAL_COMPREHENSIVE_INCOME", "Total comprehensive income", current.TotalComprehensiveIncome, prior.TotalComprehensiveIncome},
	}
	lines := make([]ComparativeStatementLine, 0, len(values))
	for _, value := range values {
		lines = append(lines, comparativeLine(value.code, value.description, value.current, value.prior))
	}
	return lines
}

func comparativeCashFlowLines(current, prior CashFlow) []ComparativeStatementLine {
	values := []struct {
		code, description string
		current, prior    decimal.Decimal
	}{
		{"OPERATING_CASH_FLOW", "Net cash from operating activities", current.OperatingCashFlow, prior.OperatingCashFlow},
		{"INVESTING_CASH_FLOW", "Net cash from investing activities", current.InvestingCashFlow, prior.InvestingCashFlow},
		{"FINANCING_CASH_FLOW", "Net cash from financing activities", current.FinancingCashFlow, prior.FinancingCashFlow},
		{"NET_CHANGE_IN_CASH", "Net change in cash", current.NetChangeInCash, prior.NetChangeInCash},
		{"EFFECT_OF_EXCHANGE_RATES", "Effect of exchange rates", current.EffectOfExchangeRatesOnCash, prior.EffectOfExchangeRatesOnCash},
		{"OPENING_CASH", "Cash at beginning of period", current.OpeningCash, prior.OpeningCash},
		{"CLOSING_CASH", "Cash at end of period", current.ClosingCash, prior.ClosingCash},
	}
	lines := make([]ComparativeStatementLine, 0, len(values))
	for _, value := range values {
		lines = append(lines, comparativeLine(value.code, value.description, value.current, value.prior))
	}
	return lines
}

func validateComparativePeriods(currentStart, currentEnd, priorStart, priorEnd time.Time) error {
	if currentStart.IsZero() || currentEnd.IsZero() || priorStart.IsZero() || priorEnd.IsZero() ||
		currentEnd.Before(currentStart) || priorEnd.Before(priorStart) {
		return ErrInvalidComparativePeriods
	}
	return nil
}

func (e *BundleEngine) GenerateComparative(
	currentStart, currentEnd, priorStart, priorEnd time.Time,
	currency string,
	notes []NoteDisclosure,
) (ComparativeStatementBundle, error) {
	return generateComparativeBundle(e.repository, e.repository, currentStart, currentEnd, priorStart, priorEnd, currency, notes)
}

func generateComparativeBundle(
	currentRepository, priorRepository Repository,
	currentStart, currentEnd, priorStart, priorEnd time.Time,
	currency string,
	notes []NoteDisclosure,
) (ComparativeStatementBundle, error) {
	if err := validateComparativePeriods(currentStart, currentEnd, priorStart, priorEnd); err != nil {
		return ComparativeStatementBundle{}, err
	}
	currentSheet, err := NewBalanceSheetEngine(currentRepository).Generate(currentEnd, currency)
	if err != nil {
		return ComparativeStatementBundle{}, fmt.Errorf("generate current balance sheet: %w", err)
	}
	priorSheet, err := NewBalanceSheetEngine(priorRepository).Generate(priorEnd, currency)
	if err != nil {
		return ComparativeStatementBundle{}, fmt.Errorf("generate comparative balance sheet: %w", err)
	}
	currentIncome, err := NewIncomeStatementEngine(currentRepository).Generate(currentStart, currentEnd, currency)
	if err != nil {
		return ComparativeStatementBundle{}, fmt.Errorf("generate current income statement: %w", err)
	}
	priorIncome, err := NewIncomeStatementEngine(priorRepository).Generate(priorStart, priorEnd, currency)
	if err != nil {
		return ComparativeStatementBundle{}, fmt.Errorf("generate comparative income statement: %w", err)
	}
	currentCash, err := NewCashFlowEngine(currentRepository).Generate(currentStart, currentEnd, currency)
	if err != nil {
		return ComparativeStatementBundle{}, fmt.Errorf("generate current cash flow: %w", err)
	}
	priorCash, err := NewCashFlowEngine(priorRepository).Generate(priorStart, priorEnd, currency)
	if err != nil {
		return ComparativeStatementBundle{}, fmt.Errorf("generate comparative cash flow: %w", err)
	}
	currentEquity, err := NewEquityStatementEngine(currentRepository).Generate(currentStart, currentEnd, currency)
	if err != nil {
		return ComparativeStatementBundle{}, fmt.Errorf("generate current equity statement: %w", err)
	}
	priorEquity, err := NewEquityStatementEngine(priorRepository).Generate(priorStart, priorEnd, currency)
	if err != nil {
		return ComparativeStatementBundle{}, fmt.Errorf("generate comparative equity statement: %w", err)
	}
	currentSegments, err := NewBundleEngine(currentRepository).segmentDisclosures(currentStart, currentEnd, currency)
	if err != nil {
		return ComparativeStatementBundle{}, fmt.Errorf("generate segment disclosures: %w", err)
	}
	priorSegments, err := NewBundleEngine(priorRepository).segmentDisclosures(priorStart, priorEnd, currency)
	if err != nil {
		return ComparativeStatementBundle{}, fmt.Errorf("generate comparative segment disclosures: %w", err)
	}
	validatedNotes := make([]NoteDisclosure, 0, len(notes))
	for _, note := range notes {
		if strings.TrimSpace(note.Code) == "" || strings.TrimSpace(note.Title) == "" ||
			note.Currency != "" && note.Currency != currency {
			return ComparativeStatementBundle{}, errors.New("invalid or mismatched financial statement note disclosure")
		}
		note.Currency = currency
		validatedNotes = append(validatedNotes, note)
	}
	comparativeEquity := compareEquityStatements(currentEquity, priorEquity)
	return ComparativeStatementBundle{
		BalanceSheet: ComparativeBalanceSheet{
			CurrentPeriodDate: currentEnd, PriorPeriodDate: priorEnd,
			Lines: comparativeBalanceSheetLines(currentSheet, priorSheet), Currency: currency,
		},
		IncomeStatement: ComparativeIncomeStatement{
			CurrentPeriodStart: currentStart, CurrentPeriodEnd: currentEnd,
			PriorPeriodStart: priorStart, PriorPeriodEnd: priorEnd,
			Lines: comparativeIncomeStatementLines(currentIncome, priorIncome), Currency: currency,
		},
		CashFlow: ComparativeCashFlow{
			Current: currentCash, Prior: priorCash,
			Lines: comparativeCashFlowLines(currentCash, priorCash),
		},
		EquityStatement: comparativeEquity,
		Notes:           validatedNotes, Segments: compareSegmentDisclosures(currentSegments, priorSegments),
	}, nil
}

func compareEquityStatements(current, prior EquityStatement) ComparativeEquityStatement {
	currentByAccount := make(map[uuid.UUID]EquityMovement, len(current.Movements))
	priorByAccount := make(map[uuid.UUID]EquityMovement, len(prior.Movements))
	for _, movement := range current.Movements {
		currentByAccount[movement.AccountID] = movement
	}
	for _, movement := range prior.Movements {
		priorByAccount[movement.AccountID] = movement
	}
	accountIDs := make([]uuid.UUID, 0, len(currentByAccount)+len(priorByAccount))
	seen := make(map[uuid.UUID]struct{}, len(currentByAccount)+len(priorByAccount))
	for id := range currentByAccount {
		seen[id] = struct{}{}
		accountIDs = append(accountIDs, id)
	}
	for id := range priorByAccount {
		if _, exists := seen[id]; !exists {
			accountIDs = append(accountIDs, id)
		}
	}
	sort.Slice(accountIDs, func(i, j int) bool { return accountIDs[i].String() < accountIDs[j].String() })
	result := ComparativeEquityStatement{
		Current: current, Prior: prior,
		Movements: make([]ComparativeEquityMovement, 0, len(accountIDs)),
	}
	for _, id := range accountIDs {
		currentMovement, currentExists := currentByAccount[id]
		priorMovement, priorExists := priorByAccount[id]
		if !currentExists {
			currentMovement = EquityMovement{AccountID: id, AccountCode: priorMovement.AccountCode, AccountName: priorMovement.AccountName}
		}
		if !priorExists {
			priorMovement = EquityMovement{AccountID: id, AccountCode: currentMovement.AccountCode, AccountName: currentMovement.AccountName}
		}
		prefix := currentMovement.AccountCode
		if prefix == "" {
			prefix = priorMovement.AccountCode
		}
		result.Movements = append(result.Movements, ComparativeEquityMovement{
			AccountID: id, AccountCode: prefix, AccountName: currentMovement.AccountName,
			Opening:   comparativeLine(prefix+"_OPENING", "Opening equity", currentMovement.Opening, priorMovement.Opening),
			NetIncome: comparativeLine(prefix+"_NET_INCOME", "Net income", currentMovement.NetIncome, priorMovement.NetIncome),
			OtherComprehensiveIncome: comparativeLine(prefix+"_OCI", "Other comprehensive income",
				currentMovement.OtherComprehensiveIncome, priorMovement.OtherComprehensiveIncome),
			Distributions: comparativeLine(prefix+"_DISTRIBUTIONS", "Distributions",
				currentMovement.Distributions, priorMovement.Distributions),
			TransfersToReserve: comparativeLine(prefix+"_TRANSFERS_TO_RESERVE", "Transfers to reserve",
				currentMovement.TransfersToReserve, priorMovement.TransfersToReserve),
			Closing: comparativeLine(prefix+"_CLOSING", "Closing equity", currentMovement.Closing, priorMovement.Closing),
		})
	}
	return result
}

func compareSegmentDisclosures(current, prior []SegmentDisclosure) []ComparativeSegmentDisclosure {
	currentByID := make(map[uuid.UUID]SegmentDisclosure, len(current))
	priorByID := make(map[uuid.UUID]SegmentDisclosure, len(prior))
	ids := make([]uuid.UUID, 0, len(current)+len(prior))
	seen := make(map[uuid.UUID]struct{}, len(current)+len(prior))
	for _, segment := range current {
		currentByID[segment.SegmentID] = segment
		ids = append(ids, segment.SegmentID)
		seen[segment.SegmentID] = struct{}{}
	}
	for _, segment := range prior {
		priorByID[segment.SegmentID] = segment
		if _, exists := seen[segment.SegmentID]; !exists {
			ids = append(ids, segment.SegmentID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	result := make([]ComparativeSegmentDisclosure, 0, len(ids))
	for _, id := range ids {
		currentSegment := currentByID[id]
		priorSegment := priorByID[id]
		result = append(result, ComparativeSegmentDisclosure{
			SegmentID: id,
			Revenue: comparativeLine("SEGMENT_REVENUE", "Segment revenue",
				currentSegment.Revenue, priorSegment.Revenue),
			Expenses: comparativeLine("SEGMENT_EXPENSES", "Segment expenses",
				currentSegment.Expenses, priorSegment.Expenses),
			Profit: comparativeLine("SEGMENT_PROFIT", "Segment profit",
				currentSegment.Profit, priorSegment.Profit),
		})
	}
	return result
}

func (e *BundleEngine) GenerateComparativeForCompanyContext(
	ctx context.Context,
	companyID uuid.UUID,
	currentStart, currentEnd, priorStart, priorEnd time.Time,
	currency string,
	notes []NoteDisclosure,
) (ComparativeStatementBundle, error) {
	if ctx == nil {
		return ComparativeStatementBundle{}, errors.New("comparative reporting context is required")
	}
	if err := ctx.Err(); err != nil {
		return ComparativeStatementBundle{}, err
	}
	if notes == nil {
		if noteRepository, ok := e.repository.(NoteDisclosureRepository); ok {
			loadedNotes, err := noteRepository.ListFinancialStatementNotesForCompanyContext(
				ctx, companyID, "COMPARATIVE_FINANCIAL_STATEMENTS", currentEnd,
			)
			if err != nil {
				return ComparativeStatementBundle{}, fmt.Errorf("load comparative statement notes: %w", err)
			}
			notes = loadedNotes
		}
	}
	currentRepository, err := newCompanyReportingRepository(ctx, e.repository, companyID, currentEnd)
	if err != nil {
		return ComparativeStatementBundle{}, err
	}
	priorRepository, err := newCompanyReportingRepository(ctx, e.repository, companyID, priorEnd)
	if err != nil {
		return ComparativeStatementBundle{}, err
	}
	return generateComparativeBundle(
		currentRepository, priorRepository, currentStart, currentEnd, priorStart, priorEnd, currency, notes,
	)
}

func (e *BundleEngine) segmentDisclosures(start, end time.Time, currency string) ([]SegmentDisclosure, error) {
	accounts, err := listAccounts(e.repository)
	if err != nil {
		return nil, err
	}
	accountByID := make(map[uuid.UUID]masterdata.Account, len(accounts))
	for _, account := range accounts {
		accountByID[account.ID] = account
	}
	entries, err := listPostedEntries(e.repository, start, end)
	if err != nil {
		return nil, err
	}
	segments := make(map[uuid.UUID]*SegmentDisclosure)
	for _, entry := range entries {
		if entry.Status != "posted" {
			continue
		}
		for _, line := range entry.Lines {
			if line.SegmentID == nil {
				continue
			}
			account, ok := accountByID[line.AccountID]
			if !ok {
				return nil, ErrReportingAccountNotFound
			}
			debit, credit, err := translateLineAmounts(
				line, account.AccountType, currency, rateProvider(e.repository), start, end, entryEffectiveDate(entry),
			)
			if err != nil {
				return nil, err
			}
			amount := debit.Sub(credit)
			if account.NormalBalance == masterdata.NormalBalanceCredit {
				amount = amount.Neg()
			}
			segment := segments[*line.SegmentID]
			if segment == nil {
				segment = &SegmentDisclosure{SegmentID: *line.SegmentID, Currency: currency}
				segments[*line.SegmentID] = segment
			}
			switch account.AccountType {
			case masterdata.AccountTypeIncome:
				segment.Revenue = segment.Revenue.Add(amount)
				segment.Profit = segment.Profit.Add(amount)
			case masterdata.AccountTypeExpense:
				segment.Expenses = segment.Expenses.Add(amount)
				segment.Profit = segment.Profit.Sub(amount)
			}
		}
	}
	result := make([]SegmentDisclosure, 0, len(segments))
	for _, segment := range segments {
		result = append(result, *segment)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].SegmentID.String() < result[j].SegmentID.String() })
	return result, nil
}
