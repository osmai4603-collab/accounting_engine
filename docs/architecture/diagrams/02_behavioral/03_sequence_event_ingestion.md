# مخطط تتابع استقبال الأحداث الاقتصادية (Sequence: Event Ingestion)

- **النطاق (Scope):** المسار الزمني لاستقبال ومعالجة الأحداث الاقتصادية، وحجز مفتاح التكرار (`Claim`)، واستدعاء محرك الترحيل، وتثبيت الإنجاز (`Complete`)، وسيناريوهات إعادة التسليم والتعارض.
- **مصدر الحقيقة (Source of Truth):** `internal/adapters/postgres/public_events.go`, `pkg/spi/events`, `docs/architecture/unit_of_work_and_transactions.md`.
- **آخر تحقق (Last Verified):** 2026-11-17 (Commit: `dd1483a`).

---

## 1. المخطط المعماري: دورة حياة حجز ومعالجة الحدث

```mermaid
sequenceDiagram
    autonumber
    actor Broker as موزع الأحداث / Event Source
    participant Handler as معالج الأحداث / Ingestion Handler
    participant EventStore as مخزن الأحداث / Events DB Store
    participant GL as محرك الترحيل / GL Posting Engine

    Broker->>Handler: IngestEvent(companyID, source, idempotencyKey, payload)
    activate Handler

    %% 1. حساب البصمة القانونية للحدث
    Handler->>Handler: حساب بصمة SHA-256 للمحتوى (Canonical Payload Hash)

    %% 2. محاولة حجز مفتاح التكرار (Claim Step)
    Handler->>EventStore: ClaimPublicEventIngestion(record)
    activate EventStore
    Note over EventStore: SELECT ... FOR UPDATE<br/>أو INSERT ON CONFLICT DO NOTHING
    
    alt الحالة: حدث جديد تمامًا (New Claim)
        EventStore->>EventStore: إدراج سجل بحالة 'started' وتاريخ البدء
        EventStore-->>Handler: نجاح الحجز (claimed = true)
    else الحالة: إعادة تسليم بنفس المفتاح وبنفس البصمة المكتملة مسبقًا (Duplicate Re-delivery)
        EventStore-->>Handler: إرجاع السجل الموجود مسبقًا (claimed = false, status = 'completed')
        Handler-->>Broker: إرجاع النتيجة السابقة فورًا دون إعادة ترحيل القيد
    else الحالة: إعادة تسليم بنفس المفتاح ولكن ببصمة مختلفة (Payload Mismatch)
        EventStore-->>Handler: تعارض البصمة (claimed = false, payload_hash != new_hash)
        Handler-->>Broker: رفض فوري بخطأ تعارض: ErrIdempotencyConflict
    else الحالة: حدث سابق تعثر وقابل لإعادة المحاولة (Retryable Failure)
        EventStore->>EventStore: تحديث الحالة من 'retryable_failure' إلى 'started'
        EventStore-->>Handler: إعادة تفعيل الحجز (claimed = true)
    end
    deactivate EventStore

    %% 3. ترحيل القيد المحاسبي
    alt تم الحجز بنجاح (claimed == true)
        Handler->>GL: PostEntryContext(entry)
        activate GL
        alt نجاح ترحيل القيد
            GL-->>Handler: تم الترحيل بنجاح (JournalEntry)
            Handler->>EventStore: CompletePublicEventIngestion(entrySnapshot, status='completed')
            EventStore-->>Handler: تم التثبيت
            Handler-->>Broker: نجاح معالجة الحدث (Ack)
        else فشل الترحيل (خطأ تحقق أو إغلاق فترة أو قفل)
            GL-->>Handler: فشل الترحيل (Posting Error)
            Handler->>EventStore: FailPublicEventIngestion(failureClass, reason)
            Note over EventStore: تصنيف الخطأ:<br/>'retryable_failure' (تزامن مؤقت)<br/>أو 'failed' / 'terminal_failure' (خطأ بيانات)
            Handler-->>Broker: فشل الاستقبال (Nack / Error)
        end
        deactivate GL
    end
    deactivate Handler
```

---

## 2. جدول حالات مفتاح التكرار وإعادة التسليم

| السيناريو | مفتاح التكرار | بصمة المحتوى (Hash) | الحالة المسجلة بالجدول | الإجراء المتخذ | النتيجة للمرسل |
| --- | --- | --- | --- | --- | --- |
| **وصول أول مرة** | جديد | جديدة | لا يوجد سجل | يتم الحجز بحالة `started` والمضي للترحيل | ترحيل جديد |
| **إعادة إرسال مطابقة** | مستخدم | مطابقة تمامًا | `completed` | عدم الترحيل؛ قراءة القيد المحفوظ في `entry_snapshot` | إرجاع القيد نفسه (Idempotent 200 OK) |
| **تعارض حمولة (Spoof/Tamper)** | مستخدم | مختلفة عن المحفوظة | أي حالة | إيقاف الطلب فورًا بـ `ErrIdempotencyConflict` | رفض (409 Conflict) لمنع التزوير |
| **تعافي بعد انهيار (Crash Recovery)** | مستخدم | مطابقة | `started` (انهارت العملية قبل إكمال القيد) | الفحص في الدفتر العام: إن وجد القيد يُثبت `completed`، وإن لم يوجد يُعاد استئناف الترحيل | معالجة سالمة بلا تكرار |
| **خطأ مؤقت قابل للمحاولة** | مستخدم | مطابقة | `retryable_failure` | يُعاد فتح السجل إلى `started` وتجربة الترحيل مجددًا | إعادة محاولة مسموحة |
