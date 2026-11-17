# مخطط تتابع ترحيل القيد المحاسبي (Sequence: PostEntry)

- **النطاق (Scope):** المسار الزمني التفصيلي لترحيل القيد (`PostEntry`) شاملاً الفحوصات، تخصيص التسلسل، الترحيل الذري، ومسار الفشل والتراجع (`Rollback`).
- **مصدر الحقيقة (Source of Truth):** `internal/posting/engine.go`, `internal/adapters/postgres/posting.go`, `internal/adapters/postgres/transactions.go`.
- **آخر تحقق (Last Verified):** 2026-11-17 (Commit: `dd1483a`).

---

## 1. المخطط المعماري: مسار النجاح والـ Rollback عند الفشل

```mermaid
sequenceDiagram
    autonumber
    actor Caller as المستدعي / Caller
    participant Engine as Posting Engine<br/>(internal/posting)
    participant MasterData as Master Data Service<br/>(internal/masterdata)
    participant Locker as Concurrency Locker<br/>(WithinPostingLock)
    participant DB as محول PostgreSQL<br/>(storage/postgres)
    participant Trigger as حراس قاعدة البيانات<br/>(Postgres Triggers / RLS)

    Caller->>Engine: PostEntryContext(ctx, entry, posterID)
    activate Engine

    %% 1. التحقق المبدئي وسياق الرصد
    Engine->>Engine: فحص الحالة: هل القيد مسودة (entry.Status == EntryDraft)؟
    alt الحالة ليست مسودة
        Engine-->>Caller: خطأ ErrMoveAlreadyPosted أو ErrEntryNotDraft
    end

    %% 2. تحضير الترحيل وفحص القواعد
    Engine->>Engine: prepareForPosting(ctx, entry, posterID)
    Engine->>Engine: تحقق البنية: سطرين على الأقل، توازن العملة الوظيفية (Validate)
    
    %% 3. فحص الشركة والفترة والحسابات
    Engine->>MasterData: GetCompanyByID(CompanyID)
    MasterData-->>Engine: بيانات الشركة (Active == true, FunctionalCurrency)
    
    Engine->>MasterData: GetPeriodForCompanyDate(CompanyID, EntryDate)
    MasterData-->>Engine: الفترة المالية (PeriodID, State == 'open')
    alt الفترة مقفلة (SoftClosed / HardClosed)
        Engine-->>Caller: رفض: ErrFiscalPeriodClosed
    end

    loop لكل سطر في القيد
        Engine->>MasterData: ValidateCanPostToAccount(AccountID)
        alt الحساب معطل أو غير قابل للترحيل المباشر
            Engine-->>Caller: رفض: ErrAccountInactive / ErrControlAccountDirectPost
        end
    end

    %% 4. فحص الصلاحيات والفصل بين المنشئ والمرحل (Maker-Checker)
    Engine->>Engine: checkMakerChecker(entry, posterID)
    alt تجاوز الحد المالي وكان المرحل هو نفس المنشئ
        Engine-->>Caller: رفض: ErrMakerCheckerViolation
    end

    %% 5. مرحلة القفل وبدء المعاملة الذرية
    Engine->>Locker: WithinPostingLock(CompanyID, JournalID)
    activate Locker

    Locker->>DB: WithCompanyTx (بدء المعاملة: BEGIN)
    activate DB
    DB->>DB: ضبط عزل المستأجر: SET LOCAL app.current_company_id = CompanyID

    %% 6. تخصيص التسلسل وكتابة القيد
    DB->>DB: تخصيص رقم اليومية التالي: NextSequence (SELECT FOR UPDATE)
    DB->>DB: إدراج رأس القيد في account_moves (حالة 'posted')
    DB->>DB: إدراج سطور القيد في account_move_lines

    %% 7. تفعيل الحراس التلقائيين في قاعدة البيانات
    DB->>Trigger: إطلاق زناد التحقق: trg_check_posted_move_balance
    alt مجموع المدين لا يساوي مجموع الدائن في قاعدة البيانات
        Trigger-->>DB: خطأ استثناء: RAISE EXCEPTION 'Move unbalanced'
        DB->>DB: تراجع كامل: ROLLBACK
        DB-->>Locker: إرجاع خطأ التراجع
        Locker-->>Engine: خطأ الفشل
        Engine->>Engine: إعادة تعيين كائن القيد إلى مسودة (Status = Draft)
        Engine-->>Caller: فشل الترحيل وتراجع كافة الآثار
    end

    %% 8. تحديث الأرصدة وسلسلة التدقيق
    Trigger->>DB: إطلاق trg_update_balances_on_posting لتحديث account_balances
    DB->>DB: إدراج سجل التدقيق في financial_audit_logs مع SHA-256 rolling hash
    DB->>DB: تسجيل Idempotency Claim في posting_idempotency

    %% 9. تثبيت المعاملة
    DB->>DB: تثبيت نهائي: COMMIT
    deactivate DB
    Locker-->>Engine: نجاح الترحيل وإطلاق قفل اليومية
    deactivate Locker

    Engine->>Engine: تسجيل المقاييس والزمن: recordPosting (Metrics & Logs)
    Engine-->>Caller: نجاح الترحيل (JournalEntry Posted مع SequenceNumber)
    deactivate Engine
```

---

## 2. مفتاح المخطط ومسار الفشل (Legend & Failure Semantics)

| الخطوة | المكون المسؤول | نتيجة الفشل والضمانة |
| --- | --- | --- |
| **فحص الفترة** | `internal/masterdata` | يتم الرفض قبل طلب أي أقفال أو بدء أي معاملة قاعدة بيانات. |
| **قفل التزامن** | `WithinPostingLock` | يضمن عدم تصادم تسلسل أرقام اليومية لنفس الشركة واليومية في العمليات المتزامنة. |
| **عزل المستأجر** | `SET LOCAL app.current_company_id` | تنتهي صلاحية المتغير بانتهاء المعاملة، مما يمنع تسرب البيانات بين اتصالات الحوض. |
| **زناد التوازن** | `trg_check_posted_move_balance` | خط الدفاع الأخير في DB؛ يمنع كتابة أي قيد غير متوازن حتى لو تم تجاوز كود التطبيق. |
| **الـ Rollback** | `PostgreSQL Transaction` | في حال حدوث أي خطأ، تتراجع كل من: (الأرصدة، السطور، الرأس، التدقيق، والتسلسل) دون ترك أي بقايا. |
