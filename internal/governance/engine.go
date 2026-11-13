package governance

import (
	"context"

	"github.com/osmai4603-collab/accounting_engine/internal/observability"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
)

type Engine struct {
	Chain     *AuditChain
	Approvals *ApprovalEngine
}

func NewEngine(repository Repository) *Engine {
	engine := &Engine{Chain: NewAuditChain(repository)}
	approvals, ok := repository.(ApprovalRepository)
	if !ok {
		return engine
	}
	var authorizer *Authorizer
	if authorization, ok := repository.(AuthorizationRepository); ok {
		authorizer = NewAuthorizer(authorization)
	}
	engine.Approvals = NewApprovalEngine(approvals, authorizer, nil)
	return engine
}

// SetMetrics attaches observability metrics to the governance subsystem.
func (e *Engine) SetMetrics(metrics *observability.Metrics) {
	if e == nil || e.Approvals == nil {
		return
	}
	e.Approvals.SetMetrics(metrics)
}

// ApprovalGate returns the posting-boundary gate backed by this engine's
// approval subsystem and company policy.
func (e *Engine) ApprovalGate() posting.ApprovalGate {
	if e == nil {
		return nil
	}
	gate := &ApprovalGate{approvals: e.Approvals}
	if e.Approvals != nil {
		if entries, ok := e.Approvals.repository.(ApprovalEntryRepository); ok {
			gate.entries = entries
		}
	}
	if e.Chain != nil {
		if policy, ok := e.Chain.repository.(posting.MakerCheckerPolicy); ok {
			gate.policy = policy
		}
	}
	if gate.approvals == nil && gate.policy == nil {
		return nil
	}
	return gate
}

func (e *Engine) Append(ctx context.Context, log FinancialAuditLog) error {
	return e.Chain.Append(ctx, log)
}

func (e *Engine) VerifyIntegrity(ctx context.Context) (bool, error) {
	return e.Chain.VerifyIntegrity(ctx)
}
