# مخطط العلاقات المنطقي (Logical ERD)

- **النطاق (Scope):** العلاقات المنطقية المجردة بين كيانات النظام المحاسبي على مستوى النطاق دون الخوض في تفاصيل الأنواع الفيزيائية والفهارس.
- **مصدر الحقيقة (Source of Truth):** ملفات التهجير `internal/adapters/postgres/migrations/202611010001_baseline_schema.sql` وما تلاها (48 migration).
- **آخر تحقق (Last Verified):** 2026-11-17 (Commit: `dd1483a`).

---

## 1. المخطط المنطقي للعلاقات (Logical ERD)

```mermaid
erDiagram
    COMPANY ||--o{ FISCAL_YEAR : defines
    COMPANY ||--o{ ACCOUNT : owns
    COMPANY ||--o{ JOURNAL : maintains
    COMPANY ||--o{ COST_CENTER : defines
    COMPANY ||--o{ CURRENCY : uses_as_functional

    FISCAL_YEAR ||--|{ FISCAL_PERIOD : contains
ا
    JOURNAL ||--o{ ACCOUNT_MOVE : records
    FISCAL_PERIOD ||--o{ ACCOUNT_MOVE : bounds

    ACCOUNT_MOVE ||--|{ ACCOUNT_MOVE_LINE : composed_of
    ACCOUNT ||--o{ ACCOUNT_MOVE_LINE : categorized_by
    COST_CENTER ||--o{ ACCOUNT_MOVE_LINE : allocates

    ACCOUNT_MOVE ||--o| ACCOUNT_MOVE : reverses

    ACCOUNT ||--o{ ACCOUNT_BALANCE : accumulates
    FISCAL_PERIOD ||--o{ ACCOUNT_BALANCE : periodizes

    COMPANY ||--o{ FINANCIAL_AUDIT_LOG : tracks
    ACCOUNT_MOVE ||--o| FINANCIAL_AUDIT_LOG : audits

    COMPANY ||--o{ PUBLIC_EVENT : ingests
    PUBLIC_EVENT ||--o| ACCOUNT_MOVE : realizes

    ACCOUNT_MOVE ||--o{ SUBLEDGER_OPEN_LINE : attaches
    ACCOUNT ||--o{ SUBLEDGER_OPEN_LINE : controls

    COMPANY {
        UUID id PK
        string code UK
        string legal_name
        string functional_currency FK
    }

    FISCAL_YEAR {
        UUID id PK
        UUID company_id FK
        string code
        int year
        string state
    }

    FISCAL_PERIOD {
        UUID id PK
        UUID company_id FK
        UUID year_id FK
        int period_number
        string state
    }

    ACCOUNT {
        UUID id PK
        UUID company_id FK
        string code UK
        string name
        string account_type
        string normal_balance
        boolean is_control_account
    }

    JOURNAL {
        UUID id PK
        UUID company_id FK
        string code UK
        string name
        bigint current_sequence
    }

    ACCOUNT_MOVE {
        UUID id PK
        UUID company_id FK
        UUID journal_id FK
        UUID period_id FK
        bigint entry_number
        string status
        string classification
        UUID reversed_move_id FK
    }

    ACCOUNT_MOVE_LINE {
        UUID move_id PK, FK
        UUID line_id PK
        UUID account_id FK
        UUID cost_center_id FK
        string matching_number
        numeric debit
        numeric credit
    }

    ACCOUNT_BALANCE {
        UUID company_id PK, FK
        UUID account_id PK, FK
        UUID fiscal_period_id PK, FK
        string currency_id PK
        numeric period_debit
        numeric period_credit
        bigint version
    }

    SUBLEDGER_OPEN_LINE {
        UUID id PK
        UUID company_id FK
        UUID move_id FK
        UUID control_account_id FK
        string kind
        numeric original_amount
        numeric reconciled_amount
    }

    PUBLIC_EVENT {
        UUID company_id PK, FK
        string source_system PK
        string idempotency_key PK
        string payload_hash
        string status
        UUID entry_id FK
    }

    FINANCIAL_AUDIT_LOG {
        bigint id PK
        UUID company_id FK
        string entity_type
        UUID entity_id
        string action
        string previous_log_hash UK
        string current_log_hash UK
    }
```

---

## 2. القيود المنطقية الرئيسة

1. **مفتاح الشركة المشترك (Multi-Tenant Composite Keys):** كل جدول فرعي يحمل `company_id` لضمان عزل البيانات منطقيًا وفيزيائيًا.
2. **علاقة الرأس بالسطور:** لا وجود لسطر مستقل في `ACCOUNT_MOVE_LINE` دون ارتباطه القاطع برأس القيد في `ACCOUNT_MOVE`.
3. **تراكم الأرصدة المستمر:** جدول `ACCOUNT_BALANCE` يمثل لقطة تجميعية للأستاذ العام برقم إصدار (`version`) لدعم القفل التفاؤلي.
