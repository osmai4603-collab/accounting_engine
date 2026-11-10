-- Rollback of the baseline schema migration: removes every object created by
-- 202611010001_baseline_schema.sql. Later migrations own their own rollbacks.

DROP TRIGGER IF EXISTS trg_check_posted_move_balance_statement ON account_moves;
DROP TRIGGER IF EXISTS trg_protect_posted_move_lines ON account_move_lines;
DROP TRIGGER IF EXISTS trg_prevent_direct_posted_move_insert ON account_moves;
DROP TRIGGER IF EXISTS trg_prevent_posted_move_mutation ON account_moves;
DROP FUNCTION IF EXISTS check_posted_move_balance_statement();
DROP FUNCTION IF EXISTS protect_posted_move_lines();
DROP FUNCTION IF EXISTS prevent_direct_posted_move_insert();
DROP FUNCTION IF EXISTS prevent_posted_move_mutation();
DROP VIEW IF EXISTS v_account_move_lines_dual_currency;

DROP INDEX IF EXISTS idx_bank_reconciliations_company;
DROP TABLE IF EXISTS bank_reconciliations;
DROP TABLE IF EXISTS account_reconciliation_items;
DROP TABLE IF EXISTS account_reconciliations;
DROP INDEX IF EXISTS uq_accounting_configurations_singleton;
DROP TABLE IF EXISTS accounting_configurations;
DROP INDEX IF EXISTS uq_audit_chain_current_hash;
DROP INDEX IF EXISTS uq_audit_chain_previous_hash;
DROP TABLE IF EXISTS financial_audit_logs;
DROP TABLE IF EXISTS exchange_rates;
DROP INDEX IF EXISTS bank_items_account_idx;
DROP TABLE IF EXISTS bank_items;
DROP TABLE IF EXISTS fixed_asset_depreciations;
DROP INDEX IF EXISTS idx_fixed_assets_company;
DROP TABLE IF EXISTS fixed_assets;
DROP INDEX IF EXISTS subledger_open_lines_partner_idx;
DROP TABLE IF EXISTS subledger_open_lines;
DROP TABLE IF EXISTS subledger_events;
DROP INDEX IF EXISTS event_accounting_rules_lookup_idx;
DROP TABLE IF EXISTS event_accounting_rules;
DROP TABLE IF EXISTS subledger_mappings;
DROP TABLE IF EXISTS account_move_lines;
DROP INDEX IF EXISTS account_moves_journal_idx;
DROP INDEX IF EXISTS account_moves_entry_date_idx;
DROP INDEX IF EXISTS uq_move_journal_number;
DROP TABLE IF EXISTS account_moves;
DROP TABLE IF EXISTS journals;
DROP TABLE IF EXISTS fiscal_periods;
DROP INDEX IF EXISTS uq_fiscal_years_company_year;
DROP INDEX IF EXISTS uq_fiscal_years_company_code;
DROP TABLE IF EXISTS fiscal_years;
DROP INDEX IF EXISTS idx_accounts_company;
DROP INDEX IF EXISTS uq_accounts_company_code;
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS companies;
DROP TABLE IF EXISTS currencies;
