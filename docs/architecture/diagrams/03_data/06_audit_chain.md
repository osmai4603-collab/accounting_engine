# مخطط سلسلة التدقيق المشفرة (Audit Chain & WORM Architecture)

- **النطاق (Scope):** البنية المشفرة لسلسلة التدقيق المحاسبي المترابطة (Cryptographic Hash Chaining)، وآلية حماية WORM الممنوعة من الحذف والتعديل عبر زنادات PostgreSQL.
- **مصدر الحقيقة (Source of Truth):** `internal/governance`, `internal/adapters/postgres/governance.go`, `internal/adapters/postgres/migrations/202611010001_baseline_schema.sql`.
- **آخر تحقق (Last Verified):** 2026-11-17 (Commit: `dd1483a`).

---

## 1. المخطط المعماري: ترابط كتل سلسلة التدقيق

```mermaid
flowchart LR
    classDef block fill:#eff6ff,stroke:#1d4ed8,stroke-width:2px,color:#0f172a;
    classDef guard fill:#fee2e2,stroke:#b91c1c,stroke-width:2px,color:#7f1d1d;
    classDef genesis fill:#f0fdf4,stroke:#16a34a,stroke-width:2px,color:#0f172a;

    Genesis["<b>كتلة التأسيس (Genesis Block)</b><br/>ID: 1 | CompanyID: C1<br/>PreviousHash: 64 zeros (0000...00)<br/>CurrentHash: H1 = SHA256(Payload1 + 0000...)"]:::genesis

    Block2["<b>سجل العملية الثاني (Audit Block 2)</b><br/>ID: 2 | Action: POST_MOVE<br/>PreviousHash: H1<br/>CurrentHash: H2 = SHA256(Payload2 + H1)"]:::block

    Block3["<b>سجل العملية الثالث (Audit Block 3)</b><br/>ID: 3 | Action: REVERSE_MOVE<br/>PreviousHash: H2<br/>CurrentHash: H3 = SHA256(Payload3 + H2)"]:::block

    subgraph WORMProtection ["حراس عدم التعديل (PostgreSQL WORM Triggers)"]
        TrgNoUpdate["زناد منع التعديل<br/><code>trg_financial_audit_logs_no_update</code><br/>RAISE EXCEPTION 'Audit logs are immutable'"]:::guard
        TrgNoDelete["زناد منع الحذف<br/><code>trg_financial_audit_logs_no_delete</code><br/>RAISE EXCEPTION 'Audit logs cannot be deleted'"]:::guard
        TrgNoTruncate["زناد منع التفريغ<br/><code>trg_financial_audit_logs_no_truncate</code><br/>RAISE EXCEPTION 'Audit table truncate forbidden'"]:::guard
    end

    Genesis -->|يرتبط عبر H1| Block2
    Block2 -->|يرتبط عبر H2| Block3

    Block2 -.->|محاولة UPDATE مرفوضة| TrgNoUpdate
    Block3 -.->|محاولة DELETE مرفوضة| TrgNoDelete
    Block3 -.->|محاولة TRUNCATE مرفوضة| TrgNoTruncate
```

---

## 2. معادلة حساب البصمة المشفرة (Chaining Invariant)

لكل سجل تدقيق جديد $N$ تابع لنفس الشركة:

$$\text{CurrentHash}_N = \text{SHA-256}\Big(\text{PreviousHash}_{N-1} \,\|\, \text{Timestamp} \,\|\, \text{ActorID} \,\|\, \text{EntityID} \,\|\, \text{Action} \,\|\, \text{PayloadAfter}\Big)$$

### الفهارس الفريدة الحاكمة في قاعدة البيانات

1. `CREATE UNIQUE INDEX uq_audit_chain_previous_hash ON financial_audit_logs(company_id, previous_log_hash);`
   - يضمن عدم وجود أي تشعب أو فرعين لنفس السلسلة (No Forking).
2. `CREATE UNIQUE INDEX uq_audit_chain_current_hash ON financial_audit_logs(company_id, current_log_hash);`
   - يضمن عدم وجود تصادم في البصمات داخل نفس الشركة.

---

## 3. خوارزمية التحقق من السلامة (Audit Integrity Verification)

يقوم المحرك بفحص دوري مستقل (`VerifyAuditChainIntegrity`) لقراءة السلسلة من كتلة التأسيس حتى آخر سجل، ومقارنة البصمات:

- أي محاولة تلاعب بالبيانات التاريخية في قاعدة البيانات (حتى لو تمت بصلاحيات `postgres` عبر تجاوز زناد) ستؤدي فورًا إلى **انكسار السلسلة (Hash Mismatch)** عند أول سجل تم العبث به، وإطلاق إنذار أمني فوري.
