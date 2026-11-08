package reporting

type Engine struct {
	TrialBalance *TrialBalanceEngine
	BalanceSheet *BalanceSheetEngine
	Income       *IncomeStatementEngine
	CashFlow     *CashFlowEngine
	Equity       *EquityStatementEngine
	Closing      *ClosingEngine
}

func NewEngine(repository Repository) *Engine {
	return &Engine{
		TrialBalance: NewTrialBalanceEngine(repository),
		BalanceSheet: NewBalanceSheetEngine(repository),
		Income:       NewIncomeStatementEngine(repository),
		CashFlow:     NewCashFlowEngine(repository),
		Equity:       NewEquityStatementEngine(repository),
		Closing:      NewClosingEngine(repository),
	}
}
