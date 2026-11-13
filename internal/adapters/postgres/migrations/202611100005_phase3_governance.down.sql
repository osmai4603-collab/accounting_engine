DO $$
DECLARE
    relation_name text;
BEGIN
    FOREACH relation_name IN ARRAY ARRAY[
        'companies', 'accounts', 'fiscal_years', 'fiscal_periods', 'journals',
        'account_moves', 'account_move_lines', 'subledger_mappings', 'event_accounting_rules',
        'subledger_events', 'subledger_open_lines', 'fixed_assets', 'fixed_asset_depreciations',
        'bank_items', 'exchange_rates', 'financial_audit_logs', 'accounting_configurations',
        'account_reconciliations', 'account_reconciliation_items', 'bank_reconciliations',
        'account_versions', 'cost_centers', 'governance_roles', 'user_role_assignments',
        'approval_requests', 'approval_steps'
    ]
    LOOP
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation_policy ON %I', relation_name);
        EXECUTE format('ALTER TABLE %I NO FORCE ROW LEVEL SECURITY', relation_name);
        EXECUTE format('ALTER TABLE %I DISABLE ROW LEVEL SECURITY', relation_name);
    END LOOP;
END;
$$;

DROP FUNCTION IF EXISTS app_current_company_id();
DROP TRIGGER IF EXISTS trg_financial_audit_logs_no_update ON financial_audit_logs;
DROP TRIGGER IF EXISTS trg_financial_audit_logs_no_delete ON financial_audit_logs;
DROP TRIGGER IF EXISTS trg_financial_audit_logs_no_truncate ON financial_audit_logs;
DROP FUNCTION IF EXISTS prevent_financial_audit_log_mutation();
DROP TABLE IF EXISTS approval_steps;
DROP TABLE IF EXISTS approval_requests;
DROP TABLE IF EXISTS user_role_assignments;
DROP TABLE IF EXISTS governance_roles;
DROP TABLE IF EXISTS cost_centers;
DROP TABLE IF EXISTS account_versions;
ALTER TABLE accounts DROP COLUMN IF EXISTS requires_cost_center;
