//go:build gapred

// Red test for GAP-OPS-02: the automation scheduler keeps its run state in
// memory, so a restart loses work and there is no retry/outbox/dead-letter
// path. Tracked by P4-T2.
package ifrsautomation_test

import "testing"

func TestGAP_OPS_02_SchedulerHasDurableState(t *testing.T) {
	t.Skip("GAP-OPS-02: Scheduler is in-memory only; durable state/retry/outbox/DLQ tracked by P4-T2")
}
