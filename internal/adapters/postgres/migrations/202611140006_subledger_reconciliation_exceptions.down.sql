DROP TABLE IF EXISTS subledger_reconciliation_exception_events;
DROP TABLE IF EXISTS subledger_reconciliation_exceptions;

DROP POLICY IF EXISTS tenant_isolation_policy ON subledger_reconciliation_logs;
ALTER TABLE subledger_reconciliation_logs NO FORCE ROW LEVEL SECURITY;
ALTER TABLE subledger_reconciliation_logs DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation_policy ON subledger_reconciliation_certificates;
ALTER TABLE subledger_reconciliation_certificates NO FORCE ROW LEVEL SECURITY;
ALTER TABLE subledger_reconciliation_certificates DISABLE ROW LEVEL SECURITY;
