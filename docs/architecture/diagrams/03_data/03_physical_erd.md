# مخطط الكيانات الفيزيائي المفصل (Physical ERD by Sub-Domains)

- **النطاق (Scope):** المخطط الفيزيائي الدقيق المقسم حسب النطاقات الوظيفية، متضمناً المفاتيح الأساسية والخارجية (FK)، وقيود الفحص (CHECK)، وفهارس التفرد، وأعمدة RLS.
- **مصدر الحقيقة (Source of Truth):** ملفات التهجير الـ 48 في `internal/adapters/postgres/migrations/*.sql`.
- **آخر تحقق (Last Verified):** 2026-11-17 (Commit: `dd1483a`).

---

## 1. نطاق الترحيل والأرصدة العامة (Core Posting & Balances Domain)

```mermaid
erDiagram
    account_moves ||--|{ account_move_lines : "move_id"
    account_moves ||--o{ account_moves : "reversed_move_id"
    account_balances }|--|| accounts : "account_id"
    account_balances }|--|| fiscal_periods : "fiscal_period_id"

    account_moves {
        uuid id PK
        uuid company_id FK
        uuid journal_id FK
        uuid period_id FK
        timestamptz entry_date
        bigint entry_number "CHECK >= 0"
        varchar status "CHECK IN ('draft','posted','reversed')"
        varchar classification "CHECK IN ('regular','adjusting','closing','opening','reversing')"
        uuid reversed_move_id FK
        varchar created_by
        varchar posted_by
        timestamptz posted_at
        jsonb entry
    }

    account_move_lines {
        uuid move_id PK, FK
        uuid line_id PK
        uuid company_id FK
        uuid account_id FK
        uuid partner_id
        uuid cost_center_id
        varchar matching_number
        bigint transaction_amount_minor
        varchar transaction_currency FK
        numeric exchange_rate "CHECK > 0"
        bigint functional_debit_minor "CHECK >= 0"
        bigint functional_credit_minor "CHECK >= 0"
        jsonb line_data
    }

    account_balances {
        uuid company_id PK, FK
        uuid account_id PK, FK
        uuid fiscal_period_id PK, FK
        varchar currency_id PK, FK
        numeric beginning_debit "CHECK >= 0"
        numeric beginning_credit "CHECK >= 0"
        numeric period_debit "CHECK >= 0"
        numeric period_credit "CHECK >= 0"
        numeric ending_debit "GENERATED ALWAYS"
        numeric ending_credit "GENERATED ALWAYS"
        bigint version "CHECK > 0"
        timestamptz updated_at
    }

    posting_idempotency {
        uuid company_id PK, FK
        varchar idempotency_key PK
        uuid move_id FK
        timestamptz created_at
    }
```

---

## 2. نطاق الدفاتر المساعدة والبنود المفتوحة (Subledgers & Open Items Domain)

```mermaid
erDiagram
    subledger_open_lines }|--|| account_moves : "move_id"
    subledger_open_lines }|--|| accounts : "control_account_id"
    fixed_assets ||--o{ fixed_asset_depreciations : "asset_id"
    bank_reconciliations ||--o{ bank_items : "reconciliation_id"

    subledger_open_lines {
        uuid id PK
        uuid company_id FK
        uuid move_id FK
        uuid partner_id
        uuid control_account_id FK
        varchar kind "CHECK IN ('receivable','payable')"
        numeric amount "CHECK > 0"
        varchar currency FK
        numeric reconciled_amount "CHECK >= 0"
        varchar status
        timestamptz due_date
    }

    fixed_assets {
        uuid id PK
        uuid company_id FK
        varchar code UK
        varchar name
        numeric purchase_cost
        numeric salvage_value
        int useful_life_months
        varchar depreciation_method
        uuid asset_account_id FK
        uuid accumulated_depr_account_id FK
        uuid depreciation_expense_account_id FK
        varchar status "CHECK IN ('draft','active','fully_depreciated','disposed')"
    }

    fixed_asset_depreciations {
        uuid id PK
        uuid company_id FK
        uuid asset_id FK
        uuid move_id FK
        date period_date
        numeric amount
    }

    subledger_reconciliation_logs {
        uuid id PK
        uuid company_id FK
        varchar domain "AR/AP/Bank"
        uuid control_account_id FK
        date as_of_date
        numeric subledger_balance
        numeric gl_balance
        numeric variance
        boolean certified
        timestamptz created_at
    }
```

---

## 3. نطاق الحوكمة وسلسلة التدقيق (Governance & Audit Domain)

```mermaid
erDiagram
    financial_audit_logs {
        bigint id PK "IDENTITY"
        uuid company_id FK
        varchar entity_type
        uuid entity_id
        varchar action
        uuid actor_id
        jsonb payload_before
        jsonb payload_after
        inet ip_address
        timestamptz timestamp
        varchar previous_log_hash UK "SHA-256"
        varchar current_log_hash UK "SHA-256"
    }

    accounting_policy_versions {
        uuid id PK
        uuid company_id FK
        int version UK
        jsonb policy_data
        uuid created_by
        timestamptz created_at
    }

    approval_requests {
        uuid id PK
        uuid company_id FK
        varchar request_type
        uuid target_entity_id
        varchar status "pending/approved/rejected"
        uuid requester_id
        timestamptz created_at
    }

    approval_steps {
        uuid id PK
        uuid request_id FK
        int step_order
        uuid approver_role_id
        uuid approved_by
        timestamptz decided_at
    }
```

---

## 4. نطاق التقارير والتصنيفات (Reporting & Taxonomies Domain)

```mermaid
erDiagram
    reporting_taxonomies ||--|{ reporting_taxonomy_nodes : "taxonomy_id"
    reporting_taxonomy_nodes ||--o{ reporting_taxonomy_nodes : "parent_id"
    reporting_account_tags }|--|| accounts : "account_id"

    reporting_taxonomies {
        uuid id PK
        uuid company_id FK "NULL for global"
        varchar code UK
        varchar name
        varchar framework "IFRS / GAAP / STATUTORY"
        boolean is_active
    }

    reporting_taxonomy_nodes {
        uuid id PK
        uuid taxonomy_id FK
        uuid parent_id FK
        varchar code
        varchar name
        varchar statement_type "BS / PL / CF / EQ"
        int display_order
        varchar calculation_expression
    }

    reporting_account_tags {
        uuid id PK
        uuid company_id FK
        uuid account_id FK
        uuid taxonomy_node_id FK
    }

    financial_statement_notes {
        uuid id PK
        uuid company_id FK
        uuid period_id FK
        varchar note_number
        varchar title
        text content_html
    }
```

---

## 5. نطاق استيعاب الأحداث الاقتصادية (Public Events Domain)

```mermaid
erDiagram
    public_event_ingestions {
        uuid company_id PK, FK
        varchar source_system PK
        varchar idempotency_key PK
        varchar event_type
        varchar payload_hash "SHA-256"
        int payload_hash_version
        uuid entry_id FK
        jsonb entry_snapshot
        varchar status "started/processing/completed/failed"
        varchar failure_class "transient/terminal"
        text failure_reason
        timestamptz created_at
        timestamptz updated_at
    }

    event_accounting_rules {
        uuid id PK
        uuid company_id FK
        varchar event_type
        int version
        jsonb rules_spec
        boolean is_active
    }
```

