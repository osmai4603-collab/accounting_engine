package reporting

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/shopspring/decimal"
)

var (
	ErrStatementReconciliationFailed = errors.New("financial statement reconciliation failed")
	ErrUnclassifiedAccountsPresent   = errors.New("unclassified accounts detected in reporting scope")
	ErrAbnormalBalancesPresent       = errors.New("abnormal balances detected in reporting scope")
	ErrNetIncomeStatementMismatch    = errors.New("net income differs between trial balance and income statement")
	ErrEquityStatementMismatch       = errors.New("equity differs between balance sheet and equity statement")
)

// UnclassifiedAccount records any account that is missing proper taxonomy classification,
// ensuring no accounts or balances are silently hidden from financial statements.
type UnclassifiedAccount struct {
	AccountID    uuid.UUID
	AccountCode  string
	AccountName  string
	AccountType  masterdata.AccountType
	SubClass     string
	EndingDebit  decimal.Decimal
	EndingCredit decimal.Decimal
	NetBalance   decimal.Decimal
	Reason       string
}

// AbnormalBalance records accounts whose ending balances contradict their normal balance side.
type AbnormalBalance struct {
	AccountID     uuid.UUID
	AccountCode   string
	AccountName   string
	AccountType   masterdata.AccountType
	NormalBalance masterdata.NormalBalance
	EndingDebit   decimal.Decimal
	EndingCredit  decimal.Decimal
	NetBalance    decimal.Decimal
	Warning       string
}

// StatementReconciliationReport contains the exhaustive mathematical and classification
// verification results connecting Trial Balance, General Ledger, Balance Sheet, Income
// Statement, and Statement of Changes in Equity.
type StatementReconciliationReport struct {
	CompanyID             uuid.UUID
	StartDate             time.Time
	EndDate               time.Time
	Currency              string
	TrialBalanceBalanced  bool
	TBDebitTotal          decimal.Decimal
	TBCreditTotal         decimal.Decimal
	TBNetIncome           decimal.Decimal
	ISNetIncome           decimal.Decimal
	NetIncomeVariance     decimal.Decimal
	NetIncomeReconciled   bool
	BSAssets              decimal.Decimal
	BSLiabilities         decimal.Decimal
	BSEquity              decimal.Decimal
	BSTotalLiabAndEquity  decimal.Decimal
	BSBalanced            bool
	EquityClosingTotal    decimal.Decimal
	EquityMovementNetInc  decimal.Decimal
	EquityStatementTied   bool
	UnclassifiedAccounts  []UnclassifiedAccount
	AbnormalBalances      []AbnormalBalance
	RoundingDiscrepancies decimal.Decimal
	Reconciled            bool
	AuditSummary          string
	GeneratedAt           time.Time
}

// StatementReconciler coordinates cross-statement balance verification.
type StatementReconciler struct {
	repository Repository
}

func NewStatementReconciler(repository Repository) *StatementReconciler {
	return &StatementReconciler{repository: repository}
}

// Reconcile performs end-to-end reconciliation between TB, BS, IS, and Equity Statement.
func (r *StatementReconciler) Reconcile(
	ctx context.Context,
	companyID uuid.UUID,
	startDate, endDate time.Time,
	currency string,
) (StatementReconciliationReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	repo := r.repository
	if companyID != uuid.Nil {
		compRepo, err := newCompanyReportingRepository(ctx, r.repository, companyID, endDate)
		if err != nil {
			return StatementReconciliationReport{}, err
		}
		repo = compRepo
	}

	report := StatementReconciliationReport{
		CompanyID:            companyID,
		StartDate:            startDate,
		EndDate:              endDate,
		Currency:             strings.ToUpper(strings.TrimSpace(currency)),
		UnclassifiedAccounts: make([]UnclassifiedAccount, 0),
		AbnormalBalances:     make([]AbnormalBalance, 0),
		GeneratedAt:          time.Now().UTC(),
	}

	// 1. Generate Trial Balance
	tbEngine := NewTrialBalanceEngine(repo)
	tb, err := tbEngine.GenerateAdjusted(startDate, endDate, report.Currency, true)
	if err != nil {
		return report, fmt.Errorf("reconciliation failed to generate trial balance: %w", err)
	}
	report.TrialBalanceBalanced = tb.Balanced
	report.TBDebitTotal = tb.DebitTotal
	report.TBCreditTotal = tb.CreditTotal

	// 2. Generate Income Statement
	isEngine := NewIncomeStatementEngine(repo)
	is, err := isEngine.Generate(startDate, endDate, report.Currency)
	if err != nil {
		return report, fmt.Errorf("reconciliation failed to generate income statement: %w", err)
	}
	report.ISNetIncome = is.NetProfit

	// 3. Generate Balance Sheet
	bsEngine := NewBalanceSheetEngine(repo)
	bs, err := bsEngine.Generate(endDate, report.Currency)
	if err != nil {
		return report, fmt.Errorf("reconciliation failed to generate balance sheet: %w", err)
	}
	report.BSAssets = bs.TotalAssets
	report.BSLiabilities = bs.TotalLiabilities
	report.BSEquity = bs.Equity
	report.BSTotalLiabAndEquity = bs.TotalLiabilitiesAndEquity
	report.BSBalanced = bs.Balanced

	// 4. Generate Equity Statement
	eqEngine := NewEquityStatementEngine(repo)
	eq, err := eqEngine.Generate(startDate, endDate, report.Currency)
	if err != nil {
		return report, fmt.Errorf("reconciliation failed to generate equity statement: %w", err)
	}
	report.EquityClosingTotal = eq.ClosingTotal

	// 5. Inspect TB lines for:
	//    a) TB Net Income = (Revenues - Expenses)
	//    b) Unclassified accounts
	//    c) Abnormal balances
	accounts, err := repo.ListAccounts(ctx)
	if err != nil {
		return report, fmt.Errorf("reconciliation failed to list accounts: %w", err)
	}
	accountMap := make(map[uuid.UUID]masterdata.Account, len(accounts))
	for _, acc := range accounts {
		accountMap[acc.ID] = acc
	}

	var tbRevenues decimal.Decimal
	var tbExpenses decimal.Decimal

	for _, line := range tb.Lines {
		acc, exists := accountMap[line.AccountID]
		if !exists {
			report.UnclassifiedAccounts = append(report.UnclassifiedAccounts, UnclassifiedAccount{
				AccountID:    line.AccountID,
				AccountCode:  line.AccountCode,
				AccountName:  line.AccountName,
				EndingDebit:  line.EndingDebit,
				EndingCredit: line.EndingCredit,
				NetBalance:   line.EndingDebit.Sub(line.EndingCredit),
				Reason:       "account definition missing from master data repository",
			})
			continue
		}

		// Check unclassified account types or subclasses
		if !isValidAccountType(acc.AccountType) {
			report.UnclassifiedAccounts = append(report.UnclassifiedAccounts, UnclassifiedAccount{
				AccountID:    acc.ID,
				AccountCode:  acc.Code,
				AccountName:  acc.Name,
				AccountType:  acc.AccountType,
				SubClass:     acc.SubClassification,
				EndingDebit:  line.EndingDebit,
				EndingCredit: line.EndingCredit,
				NetBalance:   line.EndingDebit.Sub(line.EndingCredit),
				Reason:       fmt.Sprintf("unknown or invalid account type %q", acc.AccountType),
			})
		} else if strings.TrimSpace(acc.SubClassification) == "" && acc.AccountType != masterdata.AccountTypeEquity {
			// Equity can have empty subclassification in basic charts, but assets/liabilities/income/expense require mapping
			report.UnclassifiedAccounts = append(report.UnclassifiedAccounts, UnclassifiedAccount{
				AccountID:    acc.ID,
				AccountCode:  acc.Code,
				AccountName:  acc.Name,
				AccountType:  acc.AccountType,
				SubClass:     acc.SubClassification,
				EndingDebit:  line.EndingDebit,
				EndingCredit: line.EndingCredit,
				NetBalance:   line.EndingDebit.Sub(line.EndingCredit),
				Reason:       "missing subclassification for financial reporting taxonomy",
			})
		}

		// Accumulate Income & Expense for TB Net Income
		switch acc.AccountType {
		case masterdata.AccountTypeIncome:
			// Net revenue activity: Credit increases income, Debit decreases income
			netIncomeLine := line.EndingCredit.Sub(line.EndingDebit)
			tbRevenues = tbRevenues.Add(netIncomeLine)
		case masterdata.AccountTypeExpense:
			// Net expense activity: Debit increases expense, Credit decreases expense
			netExpenseLine := line.EndingDebit.Sub(line.EndingCredit)
			tbExpenses = tbExpenses.Add(netExpenseLine)
		}

		// Check Abnormal Balances
		checkAbnormalBalance(&report, acc, line)
	}

	report.TBNetIncome = tbRevenues.Sub(tbExpenses)
	report.NetIncomeVariance = report.TBNetIncome.Sub(report.ISNetIncome).Abs()
	report.NetIncomeReconciled = report.NetIncomeVariance.LessThanOrEqual(decimal.NewFromFloat(0.0001))

	// Equity movement net income sum
	var eqNetIncomeTotal decimal.Decimal
	for _, m := range eq.Movements {
		eqNetIncomeTotal = eqNetIncomeTotal.Add(m.NetIncome)
	}
	report.EquityMovementNetInc = eqNetIncomeTotal

	// Check equity consistency between Balance Sheet and Equity Statement
	// When BS includes equity + translation reserve, compare closing totals
	equityDiff := bs.Equity.Sub(eq.ClosingTotal).Abs()
	report.EquityStatementTied = equityDiff.LessThanOrEqual(decimal.NewFromFloat(0.0001))
	if !report.EquityStatementTied {
		// Also allow if Equity Statement Closing matches Balance Sheet Equity minus Net Income if unclosed
		if bs.Equity.Sub(is.NetProfit).Sub(eq.ClosingTotal).Abs().LessThanOrEqual(decimal.NewFromFloat(0.0001)) {
			report.EquityStatementTied = true
		}
	}

	// Overall reconciliation status
	report.Reconciled = report.TrialBalanceBalanced &&
		report.BSBalanced &&
		report.NetIncomeReconciled &&
		report.EquityStatementTied &&
		len(report.UnclassifiedAccounts) == 0

	report.AuditSummary = fmt.Sprintf(
		"TB Balanced: %t | BS Balanced: %t | Net Income Reconciled: %t (TB: %s, IS: %s) | Equity Tied: %t | Unclassified: %d | Abnormal: %d",
		report.TrialBalanceBalanced, report.BSBalanced, report.NetIncomeReconciled,
		report.TBNetIncome.StringFixed(2), report.ISNetIncome.StringFixed(2),
		report.EquityStatementTied, len(report.UnclassifiedAccounts), len(report.AbnormalBalances),
	)

	return report, nil
}

func isValidAccountType(t masterdata.AccountType) bool {
	switch t {
	case masterdata.AccountTypeAsset,
		masterdata.AccountTypeLiability,
		masterdata.AccountTypeEquity,
		masterdata.AccountTypeIncome,
		masterdata.AccountTypeExpense:
		return true
	default:
		return false
	}
}

func checkAbnormalBalance(report *StatementReconciliationReport, acc masterdata.Account, line TrialBalanceLine) {
	switch acc.AccountType {
	case masterdata.AccountTypeAsset:
		// Normal balance is debit. If ending credit > ending debit with no contra subclass, flag as abnormal
		if line.EndingCredit.GreaterThan(line.EndingDebit) && !isContraAsset(acc.SubClassification) {
			report.AbnormalBalances = append(report.AbnormalBalances, AbnormalBalance{
				AccountID:     acc.ID,
				AccountCode:   acc.Code,
				AccountName:   acc.Name,
				AccountType:   acc.AccountType,
				NormalBalance: acc.NormalBalance,
				EndingDebit:   line.EndingDebit,
				EndingCredit:  line.EndingCredit,
				NetBalance:    line.EndingDebit.Sub(line.EndingCredit),
				Warning:       "asset account has credit ending balance (overdraft, negative position, or uncredited return)",
			})
		}
	case masterdata.AccountTypeLiability:
		// Normal balance is credit. If ending debit > ending credit, flag as abnormal
		if line.EndingDebit.GreaterThan(line.EndingCredit) {
			report.AbnormalBalances = append(report.AbnormalBalances, AbnormalBalance{
				AccountID:     acc.ID,
				AccountCode:   acc.Code,
				AccountName:   acc.Name,
				AccountType:   acc.AccountType,
				NormalBalance: acc.NormalBalance,
				EndingDebit:   line.EndingDebit,
				EndingCredit:  line.EndingCredit,
				NetBalance:    line.EndingCredit.Sub(line.EndingDebit),
				Warning:       "liability account has debit ending balance (debit balance position or overpayment)",
			})
		}
	case masterdata.AccountTypeIncome:
		// Normal balance is credit. If ending debit > ending credit, flag as abnormal
		if line.EndingDebit.GreaterThan(line.EndingCredit) && !isContraRevenue(acc.SubClassification) {
			report.AbnormalBalances = append(report.AbnormalBalances, AbnormalBalance{
				AccountID:     acc.ID,
				AccountCode:   acc.Code,
				AccountName:   acc.Name,
				AccountType:   acc.AccountType,
				NormalBalance: acc.NormalBalance,
				EndingDebit:   line.EndingDebit,
				EndingCredit:  line.EndingCredit,
				NetBalance:    line.EndingCredit.Sub(line.EndingDebit),
				Warning:       "income account has net debit balance (unusual reversal or excessive sales returns)",
			})
		}
	case masterdata.AccountTypeExpense:
		// Normal balance is debit. If ending credit > ending debit, flag as abnormal
		if line.EndingCredit.GreaterThan(line.EndingDebit) && !isContraExpense(acc.SubClassification) {
			report.AbnormalBalances = append(report.AbnormalBalances, AbnormalBalance{
				AccountID:     acc.ID,
				AccountCode:   acc.Code,
				AccountName:   acc.Name,
				AccountType:   acc.AccountType,
				NormalBalance: acc.NormalBalance,
				EndingDebit:   line.EndingDebit,
				EndingCredit:  line.EndingCredit,
				NetBalance:    line.EndingDebit.Sub(line.EndingCredit),
				Warning:       "expense account has net credit balance (over-credited expense recovery or misclassification)",
			})
		}
	}
}

func isContraAsset(sub string) bool {
	s := strings.ToLower(strings.TrimSpace(sub))
	return strings.Contains(s, "depreciation") ||
		strings.Contains(s, "amortization") ||
		strings.Contains(s, "allowance") ||
		strings.Contains(s, "impairment")
}

func isContraRevenue(sub string) bool {
	s := strings.ToLower(strings.TrimSpace(sub))
	return strings.Contains(s, "discount") ||
		strings.Contains(s, "return") ||
		strings.Contains(s, "allowance")
}

func isContraExpense(sub string) bool {
	s := strings.ToLower(strings.TrimSpace(sub))
	return strings.Contains(s, "recovery") ||
		strings.Contains(s, "rebate")
}

// ComparativeReconciliationReport verifies prior and current comparative periods.
type ComparativeReconciliationReport struct {
	CurrentPeriodEnd    time.Time
	PriorPeriodEnd      time.Time
	Currency            string
	OpeningBalancesTied bool
	DiscrepanciesCount  int
	AuditSummary        string
}

// ReconcileComparativePeriods verifies that current period opening balances equal prior period ending balances.
func (r *StatementReconciler) ReconcileComparativePeriods(
	ctx context.Context,
	companyID uuid.UUID,
	priorStart, priorEnd, currentStart, currentEnd time.Time,
	currency string,
) (ComparativeReconciliationReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	repo := r.repository
	if companyID != uuid.Nil {
		compRepo, err := newCompanyReportingRepository(ctx, r.repository, companyID, currentEnd)
		if err != nil {
			return ComparativeReconciliationReport{}, err
		}
		repo = compRepo
	}

	tbEngine := NewTrialBalanceEngine(repo)
	priorTB, err := tbEngine.GenerateAdjusted(priorStart, priorEnd, currency, true)
	if err != nil {
		return ComparativeReconciliationReport{}, fmt.Errorf("generate prior trial balance: %w", err)
	}

	currentTB, err := tbEngine.GenerateAdjusted(currentStart, currentEnd, currency, true)
	if err != nil {
		return ComparativeReconciliationReport{}, fmt.Errorf("generate current trial balance: %w", err)
	}

	priorEnding := make(map[uuid.UUID][2]decimal.Decimal, len(priorTB.Lines))
	for _, l := range priorTB.Lines {
		priorEnding[l.AccountID] = [2]decimal.Decimal{l.EndingDebit, l.EndingCredit}
	}

	discrepancies := 0
	for _, l := range currentTB.Lines {
		if pe, exists := priorEnding[l.AccountID]; exists {
			// Compare prior ending vs current opening
			if !pe[0].Equal(l.OpeningDebit) || !pe[1].Equal(l.OpeningCredit) {
				discrepancies++
			}
		}
	}

	tied := discrepancies == 0
	return ComparativeReconciliationReport{
		CurrentPeriodEnd:    currentEnd,
		PriorPeriodEnd:      priorEnd,
		Currency:            currency,
		OpeningBalancesTied: tied,
		DiscrepanciesCount:  discrepancies,
		AuditSummary: fmt.Sprintf("Prior End: %s | Current Start: %s | Tied: %t | Discrepancies: %d",
			priorEnd.Format("2006-01-02"), currentStart.Format("2006-01-02"), tied, discrepancies),
	}, nil
}
