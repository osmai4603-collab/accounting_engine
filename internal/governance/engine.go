package governance

import "context"

type Engine struct {
	Chain *AuditChain
}

func NewEngine(repository Repository) *Engine {
	return &Engine{Chain: NewAuditChain(repository)}
}

func (e *Engine) Append(ctx context.Context, log FinancialAuditLog) error {
	return e.Chain.Append(ctx, log)
}

func (e *Engine) VerifyIntegrity(ctx context.Context) (bool, error) {
	return e.Chain.VerifyIntegrity(ctx)
}
