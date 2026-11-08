package valuation

type Engine struct {
	Conversion  *ConversionEngine
	Revaluation *RevaluationEngine
}

func NewEngine(provider RateProviderSPI) *Engine {
	return &Engine{
		Conversion:  NewConversionEngine(provider),
		Revaluation: NewRevaluationEngine(provider),
	}
}
