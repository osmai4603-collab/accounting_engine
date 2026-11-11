CREATE TABLE IF NOT EXISTS accounting_policy_versions (
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    policy_id uuid NOT NULL,
    policy_kind varchar(32) NOT NULL CHECK (policy_kind IN ('currency', 'posting')),
    version integer NOT NULL CHECK (version > 0),
    effective_from timestamptz NOT NULL,
    effective_to timestamptz,
    created_by text NOT NULL,
    approved_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    payload jsonb NOT NULL,
    PRIMARY KEY (company_id, policy_kind, version),
    UNIQUE (company_id, policy_id, policy_kind, version),
    CHECK (effective_to IS NULL OR effective_to > effective_from)
);

CREATE INDEX IF NOT EXISTS ix_accounting_policy_as_of
    ON accounting_policy_versions(company_id, policy_kind, effective_from DESC);

CREATE TABLE IF NOT EXISTS accounting_policy_audit_logs (
    id bigserial PRIMARY KEY,
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    policy_id uuid NOT NULL,
    policy_kind varchar(32) NOT NULL,
    version integer NOT NULL,
    action text NOT NULL CHECK (action = 'created'),
    actor text NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, policy_kind, version)
);

CREATE OR REPLACE FUNCTION reject_accounting_policy_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'accounting policy history is immutable';
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION audit_accounting_policy_version_insert() RETURNS trigger AS $$
BEGIN
    INSERT INTO accounting_policy_audit_logs(
        company_id, policy_id, policy_kind, version, action, actor, payload, created_at
    ) VALUES (
        NEW.company_id, NEW.policy_id, NEW.policy_kind, NEW.version,
        'created', NEW.created_by, NEW.payload, NEW.created_at
    );
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS accounting_policy_versions_audit_insert ON accounting_policy_versions;
CREATE TRIGGER accounting_policy_versions_audit_insert
    AFTER INSERT ON accounting_policy_versions
    FOR EACH ROW EXECUTE FUNCTION audit_accounting_policy_version_insert();

DROP TRIGGER IF EXISTS accounting_policy_versions_no_update ON accounting_policy_versions;
CREATE TRIGGER accounting_policy_versions_no_update
    BEFORE UPDATE OR DELETE ON accounting_policy_versions
    FOR EACH ROW EXECUTE FUNCTION reject_accounting_policy_mutation();
DROP TRIGGER IF EXISTS accounting_policy_versions_no_truncate ON accounting_policy_versions;
CREATE TRIGGER accounting_policy_versions_no_truncate
    BEFORE TRUNCATE ON accounting_policy_versions
    FOR EACH STATEMENT EXECUTE FUNCTION reject_accounting_policy_mutation();
DROP TRIGGER IF EXISTS accounting_policy_audit_no_update ON accounting_policy_audit_logs;
CREATE TRIGGER accounting_policy_audit_no_update
    BEFORE UPDATE OR DELETE ON accounting_policy_audit_logs
    FOR EACH ROW EXECUTE FUNCTION reject_accounting_policy_mutation();
DROP TRIGGER IF EXISTS accounting_policy_audit_no_truncate ON accounting_policy_audit_logs;
CREATE TRIGGER accounting_policy_audit_no_truncate
    BEFORE TRUNCATE ON accounting_policy_audit_logs
    FOR EACH STATEMENT EXECUTE FUNCTION reject_accounting_policy_mutation();

ALTER TABLE accounting_policy_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE accounting_policy_versions FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation_policy ON accounting_policy_versions;
CREATE POLICY tenant_isolation_policy ON accounting_policy_versions
    USING (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());

ALTER TABLE accounting_policy_audit_logs ENABLE ROW LEVEL SECURITY;
ALTER TABLE accounting_policy_audit_logs FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation_policy ON accounting_policy_audit_logs;
CREATE POLICY tenant_isolation_policy ON accounting_policy_audit_logs
    USING (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());
