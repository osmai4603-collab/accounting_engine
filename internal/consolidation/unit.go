package consolidation

// Unit is the optional consolidation service exposed to the central engine.
type Unit struct{ engine *Engine }

func NewUnit(rates RateProvider) *Unit {
	return &Unit{engine: NewEngine(rates)}
}

func (u *Unit) Consolidate(group Group, trialBalances []TrialBalance) (Result, error) {
	if u == nil || u.engine == nil {
		return Result{}, ErrInvalidInput
	}
	return u.engine.Consolidate(group, trialBalances)
}
