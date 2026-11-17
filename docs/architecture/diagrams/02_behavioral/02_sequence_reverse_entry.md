# مخطط تتابع عكس القيد المحاسبي (Sequence: ReverseEntry)

- **النطاق (Scope):** المسار الزمني لعملية عكس قيد مرحل (`ReverseEntry`)، وتوليد القيد العكسي المعكوس الأطراف، وتحديث حالة القيد الأصلي وتوثيق سبب الإلغاء في سجل التدقيق.
- **مصدر الحقيقة (Source of Truth):** `internal/posting/reversal.go`, `internal/adapters/postgres/posting.go`, `docs/accounting/reversal_specification.md`.
- **آخر تحقق (Last Verified):** 2026-11-17 (Commit: `dd1483a`).

---

## 1. المخطط المعماري

```mermaid
sequenceDiagram
    autonumber
    actor Caller as المستدعي / Caller
    participant Engine as Posting Engine<br/>(internal/posting)
    participant ReversalValidator as Reversal Validator<br/>(reversal_validation.go)
    participant DB as محول التخزين<br/>(Postgres Driver)
    participant DB_Guards as حراس قاعدة البيانات<br/>(Triggers & Balances)

    Caller->>Engine: ReverseWithRequestContext(ctx, companyID, entryID, request, posterID)
    activate Engine

    %% 1. التحقق من الهوية والسبب ومفتاح التكرار
    Engine->>Engine: فحص ActorID غير فارغ ووجود سبب العكس (Reason)
    alt غياب الفاعل أو السبب
        Engine-->>Caller: رفض: ErrReversalActorRequired / ErrInvalidReversal
    end

    %% 2. فحص حالة القيد الأصلي
    Engine->>DB: GetEntryByIDForCompany(companyID, entryID)
    DB-->>Engine: القيد الأصلي (OriginalEntry)
    
    Engine->>Engine: فحص: هل القيد مرحل أصلاً (Status == EntryPosted)؟
    alt القيد مسودة أو غير مرحل
        Engine-->>Caller: رفض: ErrEntryNotPosted
    end

    Engine->>Engine: فحص: هل القيد معكوس مسبقًا (ReversedEntryID != nil)؟
    alt القيد تم عكسه مسبقًا
        Engine-->>Caller: رفض: ErrEntryAlreadyReversed
    end

    %% 3. إنشاء كائن القيد العكسي (Reversal Entry Construction)
    Engine->>ReversalValidator: بناء القيد العكسي وعكس أطراف السطور
    activate ReversalValidator
    Note over ReversalValidator: Reversal.Lines[i].Debit = Original.Lines[i].Credit<br/>Reversal.Lines[i].Credit = Original.Lines[i].Debit<br/>Reversal.Classification = 'reversing'<br/>Reversal.OriginalEntryID = Original.ID
    ReversalValidator-->>Engine: كائن القيد العكسي الجاهز للترحيل
    deactivate ReversalValidator

    %% 4. المعاملة الذرية لعكس القيد في قاعدة البيانات
    Engine->>DB: CreateAndPostReversalContext (معاملة ذرية واحدة)
    activate DB
    DB->>DB: SET LOCAL app.current_company_id = companyID

    %% تحديث حالة القيد الأصلي
    DB->>DB: تحديث القيد الأصلي: SET reversed_move_id = reversal.ID
    Note over DB: القيد الأصلي لا يُحذف ولا تُعدل سطوره (Immutability)

    %% إدراج القيد العكسي
    DB->>DB: تخصيص رقم متسلسل جديد للقيد العكسي
    DB->>DB: إدراج القيد العكسي في account_moves بحالة 'posted'
    DB->>DB: إدراج السطور المعكوسة في account_move_lines

    %% إطلاق زناد تحديث الأرصدة وسلسلة التدقيق
    DB->>DB_Guards: تحديث الأرصدة: trg_update_balances_on_posting
    DB_Guards->>DB: احتساب الأثر العكسي التلقائي على account_balances
    
    DB->>DB: كتابة سجل تدقيق في financial_audit_logs يربط القيدين مع السبب

    DB->>DB: تثبيت المعاملة: COMMIT
    deactivate DB

    Engine-->>Caller: نجاح العكس وإرجاع كائن القيد العكسي الجديد
    deactivate Engine
```

---

## 2. المبادئ المحاسبية الحاكمة لعملية العكس

1. **حرمة القيد الأصلي (No Mutation of Original Move):** لا يُحذف القيد الأصلي ولا يُعدل تاريخه أو أرقام سطوره؛ بل يظل محفوظًا للأبد في الدفاتر ويُربط حقل `reversed_move_id` فيه فقط.
2. **العكس المحاسبي الحقيقي (True Accounting Reversal):** يتم عكس المدين بالدائن والدائن بالمدين بالعملة الوظيفية نفسها وسعر الصرف ذاته؛ مما يؤدي إلى تصفير الأثر الصافي على الحسابات في الدفتر العام.
3. **منع العكس المزدوج (Idempotent Reversal Protection):** محاولة عكس قيد معكوس مسبقًا تُرفض رفضًا قاطعًا بـ `ErrEntryAlreadyReversed`، ما لم تكن إعادة تسليم لنفس مفتاح الـ Idempotency وبنفس البصمة المشفرة.

