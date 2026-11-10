package governance

import (
	"context"

	"accounting_engine/internal/posting"
	"github.com/google/uuid"
)

// ApprovalGate enforces the company approval policy at the posting boundary.
// When a company has configured a maker-checker threshold, entries whose
// functional total is at or below it are allowed without an approval request;
// larger entries require a completed approval whose stored fingerprint still
// matches the draft. Companies without a configured policy keep the historical
// behaviour of posting without a mandatory approval request.
type ApprovalGate struct {
	approvals *ApprovalEngine
	entries   ApprovalEntryRepository
	policy    posting.MakerCheckerPolicy
}

func (g *ApprovalGate) IsApproved(ctx context.Context, companyID, entryID uuid.UUID) (bool, error) {
	if g == nil {
		return true, nil
	}
	if g.policy == nil {
		return true, nil
	}
	threshold, configured, err := g.policy.MakerCheckerThreshold(ctx, companyID)
	if err != nil {
		return false, err
	}
	if !configured {
		return true, nil
	}
	if g.approvals == nil {
		return false, ErrApprovalRepositoryRequired
	}
	if g.entries == nil {
		return false, ErrApprovalEntrySourceRequired
	}
	entry, err := g.entries.GetEntryByIDForCompanyContext(ctx, companyID, entryID)
	if err != nil {
		return false, err
	}
	total, err := posting.EntryFunctionalTotal(entry)
	if err != nil {
		return false, err
	}
	if !total.Amount().GreaterThan(threshold) {
		return true, nil
	}
	return g.approvals.IsApproved(ctx, companyID, entryID)
}
