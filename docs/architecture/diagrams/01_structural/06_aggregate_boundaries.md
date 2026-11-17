# حدود التجميع والذرية (Aggregate & Consistency Boundaries)

- **النطاق (Scope):** تحديد الوحدة الذرية (Unit of Work) في قاعدة البيانات وحرمة حدود التجميع (Aggregate Root).
- **مصدر الحقيقة (Source of Truth):** `internal/posting`, `internal/adapters/postgres/posting.go`, `docs/architecture/unit_of_work_and_transactions.md`.
- **آخر تحقق (Last Verified):** 2026-11-17 (Commit: `dd1483a`).

---

## 1. المخطط المعماري: حدود المعاملة الذرية الواحدة

```mermaid
flowchart TB
    classDef root fill:#eff6ff,stroke:#1e40af,stroke-width:3px,color:#0f172a;
    classDef part fill:#f8fafc,stroke:#334155,stroke-width:1.5px,color:#0f172a;
    classDef guard fill:#fee2e2,stroke:#b91c1c,stroke-width:2px,color:#7f1d1d;
    classDef bound fill:#fefce8,stroke:#ca8a04,stroke-width:2px,stroke-dasharray: 5 5,color:#0f172a;

    subgraph AtomicBoundary ["حدود المعاملة الذرية (Single PostgreSQL Transaction / Repeatable Read)"]
        direction TB

        subgraph AggregateRoot ["جذر التجميع (Aggregate Root)"]
            Move["<b>رأس القيد (JournalEntry / account_moves)</b><br/>ID, CompanyID, JournalID, PeriodID, SequenceNumber, Status='posted'"]:::root
        end

        subgraph AggregateMembers ["عناصر التجميع الإلزامية (Invariant-Bound Elements)"]
            Lines["<b>سطور القيد (JournalEntryLine / account_move_lines)</b><br/>N lines (Min 2) | Σ Functional Debit == Σ Functional Credit"]:::part
            Sequence["<b>تتابع اليومية (Journal Sequence Counter)</b><br/>NextSequence per (Company, Journal) — Gap-free on commit"]:::part
            Balances["<b>الأرصدة الدائمة (Perpetual Balances)</b><br/>account_balances updated via deterministic locking order"]:::part
            Audit["<b>سلسلة التدقيق (Audit Record)</b><br/>financial_audit_logs with rolling SHA-256 hash chaining"]:::part
            Idempotency["<b>سجل منع التكرار (Idempotency Claim)</b><br/>posting_idempotency: (Company, Source, Key) with payload hash"]:::part
        end

        subgraph InvariantGuards ["حراس الاتساق والنزاهة (Dual Enforcement Guards)"]
            BalanceCheck["فحص التوازن المالي<br/>Trigger: chk_posted_move_balance"]:::guard
            ImmutabilityGuard["منع التعديل والحذف<br/>Trigger: trg_prevent_posted_move_mutation"]:::guard
            RLSGuard["عزل المستأجر الصارم<br/>RLS: company_id = app_current_company_id()"]:::guard
        end

        Move -->|يحتوي ويدير| Lines
        Move -->|يخصص رقم متسلسل| Sequence
        Move -->|يحدث الأستاذ آليًا| Balances
        Move -->|يسجل بصمة غير قابلة للحذف| Audit
        Move -->|يثبت حالة الإنجاز| Idempotency

        Lines -.-> BalanceCheck
        Move -.-> ImmutabilityGuard
        Move -.-> RLSGuard
    end
```

---

## 2. قواعد الاتساق الصارمة (Consistency Invariants)

1. **لا انفصال للسطور عن رأس القيد:** لا يمكن كتابة أو تعديل سطر قيد (`account_move_lines`) خارج دورة حياة رأس القيد (`account_moves`).
2. **الكل أو لا شيء (All-or-Nothing Atomicity):** في حال حدوث أي خطأ (مثل: تعارض تسلسل، خطأ توازن، قفل فترة مغلقة، أو فشل سلسلة تدقيق)، تتراجع المعاملة بالكامل (`ROLLBACK`) ولا يتبقى أي أثر جزئي.
3. **الترتيب الحتمي لمنع التعليق (Deadlock Prevention Order):** عند تحديث أرصدة الحسابات المتعددة (`account_balances`)، يتم ترتيب معرفات الحسابات تصاعديًا (`Order by AccountID ASC`) قبل طلب الأقفال لتفادي التعليق المتشابك.
4. **تزامن التدقيق مع العملية المالية:** لا ينجح الترحيل إلا إذا سُجلت البصمة المشفرة في `financial_audit_logs` ضمن نفس المعاملة.
