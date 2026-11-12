//go:build gapred

// Red tests for the Phase 0 traceability matrix (missing ERP subledgers).
// Tracked by the Phase 5 tasks and intentionally skipped until those packages
// exist; there is no code path to exercise today.
package accounting_engine_test

import "testing"

func TestGAP_ERP_01_InventorySubledgerExists(t *testing.T) {
	t.Skip("GAP-ERP-01: no independent inventory/valuation/COGS subledger; tracked by P5-T2")
}

func TestGAP_ERP_02_PayrollModuleExists(t *testing.T) {
	t.Skip("GAP-ERP-02: no payroll/accrual/deduction module; tracked by P5-T5")
}
