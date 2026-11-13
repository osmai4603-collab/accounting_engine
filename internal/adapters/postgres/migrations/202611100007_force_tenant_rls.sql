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
        'approval_requests', 'approval_steps', 'financial_dimension_values'
    ]
    LOOP
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', relation_name);
    END LOOP;
END;
$$;
