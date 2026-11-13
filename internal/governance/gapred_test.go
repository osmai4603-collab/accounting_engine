//go:build gapred

// Red tests for the Phase 0 traceability matrix (governance gaps). Run with:
//
//	go test -tags gapred ./internal/governance -run TestGAP
//
// Every test is expected to FAIL (or be explicitly skipped with a tracked
// reason) until the corresponding phase task lands.
package governance_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/governance"
)

// GAP-GOV-03: an ApprovalGate with no policy configured defaults to fail-open,
// so a production deployment that forgets to configure a policy silently posts
// everything without maker-checker. The zero-value gate must fail closed.
func TestGAP_GOV_03_UnconfiguredGateFailsClosed(t *testing.T) {
	gate := &governance.ApprovalGate{}
	approved, err := gate.IsApproved(context.Background(), uuid.New(), uuid.New())
	if err == nil {
		t.Fatal("GAP-GOV-03: unconfigured ApprovalGate returned no error (fail-open)")
	}
	if approved {
		t.Fatal("GAP-GOV-03: unconfigured ApprovalGate approved the entry (fail-open)")
	}
}

// GAP-GOV-02: period reopening has no mandatory authorization, reason, or
// audit record. There is no reopen API to exercise yet, so the contract is
// tracked without a runtime assertion.
func TestGAP_GOV_02_ReopenRequiresAuthorizationAndAudit(t *testing.T) {
	t.Skip("GAP-GOV-02: no period-reopen API exists yet; tracked by P2-T4")
}
