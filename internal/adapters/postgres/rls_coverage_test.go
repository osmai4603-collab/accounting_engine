package postgres_test

import (
	"context"
	"testing"
)

// rlsProtectedTables lists company-scoped financial tables across the
// migration chain. Each table must have RLS, FORCE RLS, and a tenant policy.
var rlsProtectedTables = []string{
	"companies",
	"accounts",
	"fiscal_years",
	"fiscal_periods",
	"journals",
	"account_moves",
	"account_move_lines",
	"subledger_mappings",
	"event_accounting_rules",
	"subledger_events",
	"subledger_open_lines",
	"fixed_assets",
	"fixed_asset_depreciations",
	"bank_items",
	"exchange_rates",
	"financial_audit_logs",
	"accounting_configurations",
	"account_reconciliations",
	"account_reconciliation_items",
	"bank_reconciliations",
	"account_versions",
	"cost_centers",
	"governance_roles",
	"user_role_assignments",
	"approval_requests",
	"approval_steps",
	"financial_dimension_values",
	"posting_idempotency",
	"subledger_reconciliation_exceptions",
	"subledger_reconciliation_exception_events",
	"subledger_reconciliation_logs",
	"subledger_reconciliation_certificates",
	"governance_policies",
	"ifrs15_revenue_contracts",
	"public_event_ingestions",
	"public_event_account_mappings",
	"accounting_policy_versions",
	"accounting_policy_audit_logs",
	"financial_statement_notes",
	"reporting_taxonomies",
	"reporting_taxonomy_nodes",
	"reporting_account_tags",
}

// TestEveryFinancialTableHasForcedRLS is the continuous isolation guard
// required by GAP-OPS-07: a new financial table cannot ship without RLS, FORCE
// RLS, and a policy. It runs against an isolated schema in CI.
func TestEveryFinancialTableHasForcedRLS(t *testing.T) {
	ctx := context.Background()
	driver, err := newIsolatedPostgresDriver(t, ctx)
	if err != nil {
		t.Skipf("isolated PostgreSQL schema unavailable: %v", err)
	}
	defer driver.Close()

	for _, table := range rlsProtectedTables {
		var enabled, forced, hasPolicy bool
		if err := driver.Pool().QueryRow(ctx, `
			SELECT relation.relrowsecurity,
			       relation.relforcerowsecurity,
			       EXISTS (
			           SELECT 1 FROM pg_policies policy
			           WHERE policy.schemaname = current_schema()
			             AND policy.tablename = relation.relname
			       )
			FROM pg_class relation
			JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
			WHERE namespace.nspname = current_schema() AND relation.relname = $1`,
			table,
		).Scan(&enabled, &forced, &hasPolicy); err != nil {
			t.Fatalf("inspect RLS for %s: %v", table, err)
		}
		if !enabled {
			t.Errorf("%s: row level security is disabled", table)
		}
		if !forced {
			t.Errorf("%s: FORCE ROW LEVEL SECURITY is disabled", table)
		}
		if !hasPolicy {
			t.Errorf("%s: no tenant policy exists", table)
		}
	}
}
