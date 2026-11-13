package reporting

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/policy"
)

type Engine struct {
	TrialBalance   *TrialBalanceEngine
	BalanceSheet   *BalanceSheetEngine
	Income         *IncomeStatementEngine
	CashFlow       *CashFlowEngine
	Equity         *EquityStatementEngine
	Closing        *ClosingEngine
	GeneralLedger  *GeneralLedgerEngine
	Ratios         *RatiosEngine
	Bundle         *BundleEngine
	Reconciliation *BalanceReconciliationEngine
	policyUnit     *policy.Unit
}

func NewEngine(repository Repository) *Engine {
	return &Engine{
		TrialBalance:   NewTrialBalanceEngine(repository),
		BalanceSheet:   NewBalanceSheetEngine(repository),
		Income:         NewIncomeStatementEngine(repository),
		CashFlow:       NewCashFlowEngine(repository),
		Equity:         NewEquityStatementEngine(repository),
		Closing:        NewClosingEngine(repository),
		GeneralLedger:  NewGeneralLedgerEngine(repository),
		Ratios:         NewRatiosEngine(repository),
		Bundle:         NewBundleEngine(repository),
		Reconciliation: NewBalanceReconciliationEngine(repository),
	}
}

// SetPolicyUnit enables presentation-currency defaults for company reports.
func (e *Engine) SetPolicyUnit(unit *policy.Unit) { e.policyUnit = unit }

// ResolvePresentationCurrency uses the effective currency policy when a
// caller omits a currency; an explicit translation currency must be allowed.
func (e *Engine) ResolvePresentationCurrency(
	ctx context.Context,
	companyID uuid.UUID,
	asOf time.Time,
	requested string,
) (string, error) {
	if e.policyUnit == nil {
		return strings.ToUpper(strings.TrimSpace(requested)), nil
	}
	currencyPolicy, err := e.policyUnit.GetCurrencyPolicyAsOf(ctx, companyID, asOf)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(requested) == "" {
		return currencyPolicy.PresentationCurrency, nil
	}
	requested = strings.ToUpper(strings.TrimSpace(requested))
	if !currencyPolicy.AllowsCurrency(requested) {
		return "", policy.ErrCurrencyNotAllowed
	}
	return requested, nil
}

// GenerateBundleForCompanyContext defaults to the effective presentation
// currency, while a non-empty currency is an explicit translation request.
func (e *Engine) GenerateBundleForCompanyContext(
	ctx context.Context,
	companyID uuid.UUID,
	start, end time.Time,
	currency string,
) (StatementBundle, error) {
	resolved, err := e.ResolvePresentationCurrency(ctx, companyID, end, currency)
	if err != nil {
		return StatementBundle{}, err
	}
	return e.Bundle.GenerateForCompanyContext(ctx, companyID, start, end, resolved)
}
