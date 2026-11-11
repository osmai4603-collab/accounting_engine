package reporting

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
