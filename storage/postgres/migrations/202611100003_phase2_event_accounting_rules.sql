CREATE TABLE IF NOT EXISTS event_accounting_rules (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    event_type varchar(128) NOT NULL,
    priority int NOT NULL DEFAULT 0,
    rule_data jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    UNIQUE (company_id, event_type, priority)
);

CREATE INDEX IF NOT EXISTS event_accounting_rules_lookup_idx
    ON event_accounting_rules(company_id, event_type, priority DESC);
