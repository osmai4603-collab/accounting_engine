# آلة حالة مفاتيح التكرار للأحداث (State Machine: Event Idempotency Lifecycle)

- **النطاق (Scope):** دورة حياة حجز ومطابقة ومعالجة الأحداث الاقتصادية (`started` → `processing` → `completed` مقابل مسارات الفشل والتعافي من الانهيار).
- **مصدر الحقيقة (Source of Truth):** `internal/adapters/postgres/public_events.go`, `pkg/spi/events`, `internal/adapters/postgres/migrations/202611120001_public_event_ingestion_idempotency.sql`.
- **آخر تحقق (Last Verified):** 2026-11-17 (Commit: `dd1483a`).

---

## 1. المخطط المعماري: دورة حياة حالة الحدث والتعافي من الانهيار

```mermaid
stateDiagram-v2
    [*] --> started : ClaimPublicEventIngestion<br/>[حجز المفتاح لأول مرة بنجاح]

    state started {
        [*] --> InFlight
        InFlight : تم حجز المفتاح مع البصمة القانونية
        InFlight : جاري تحضير وترحيل القيد في الدفتر العام
    }

    started --> processing : استلام الحدث للتنفيذ الموزع (Processing Lock)

    %% مسار النجاح
    processing --> completed : نجاح ترحيل القيد (CompletePublicEventIngestion)<br/>[تخزين معرف القيد entry_id وبصمة entry_snapshot]
    started --> completed : ترحيل مباشر وسريع للقيد

    state completed {
        [*] --> FinalSuccess
        FinalSuccess : حالة مكتملة نهائية (Processed)
        FinalSuccess : إعادة التسليم بنفس المفتاح والبصمة ترجع نفس النتيجة
        FinalSuccess : إعادة التسليم ببصمة مختلفة ترفض بـ Conflict
    }

    %% مسارات الفشل
    processing --> retryable_failure : تعثر مؤقت بالشبكة أو تعليق أقفال (Deadlock)
    started --> retryable_failure : فشل مؤقت قابل لإعادة المحاولة (Transient Failure)

    state retryable_failure {
        [*] --> AwaitingRetry
        AwaitingRetry : خطأ عابر؛ مسموح بإعادة الحجز تلقائيًا
    }

    retryable_failure --> started : إعادة استلام الحدث (Reclaim with same hash)

    processing --> terminal_failure : خطأ تحقق محاسبي أو خرق قواعد (Validation / Closed Period)
    started --> terminal_failure : خطأ دائم غير قابل للمحاولة (Business Invariant Breach)

    state terminal_failure {
        [*] --> PermanentDeadLetter
        PermanentDeadLetter : فشل نهائي مسجل مع failure_reason
        PermanentDeadLetter : يمنع إعادة المحاولة التلقائية لحماية سلامة النظام
    }

    %% سيناريو الانهيار بين الحجز والإكمال
    state CrashScenario {
        direction TB
        CrashDesc : <b>سيناريو الانهيار (Crash between Claim and Complete):</b><br/>إذا انهارت الخدمة أثناء وجود السجل بحالة 'started':<br/>1. تفحص إعادة التسليم هل ترحل القيد فعليًا في الدفتر العام.<br/>2. إن وُجد القيد، يُحدّث السجل مباشرة إلى 'completed'.<br/>3. إن لم يوجد، يُستأنف الترحيل دون توليد قيد مكرر.
    }
```

---

## 2. جدول تفسير الحالات ورموز الخطأ المقترنة

| الحالة بالجدول (`status`) | نوع الفشل (`failure_class`) | المعنى الهندسي | قابلية إعادة المحاولة |
| --- | --- | --- | --- |
| `started` | `""` | تم حجز `(company_id, source_system, idempotency_key)` وجارٍ استدعاء محرك الترحيل. | تحت المعالجة النشطة. |
| `processing` | `""` | الحدث قيد المعالجة الموزعة في Worker نشط. | قيد التشغيل. |
| `completed` / `processed` | `""` | القيد المحاسبي مرحل بنجاح ومحفوظ في `entry_snapshot`. | **مكتمل نهائيًا**؛ يُرجع نفس الرد فورًا. |
| `retryable_failure` | `transient` | خطأ تعليق تزامن مؤقت (Deadlock 40P01) أو انقطاع شبكة مؤقت. | **نعم**؛ يُعاد فتحه عند تكرار وصول الحدث. |
| `failed` / `terminal_failure` | `terminal` | قيد غير متوازن، فترة مقفلة، أو حساب محظور (فشل غير قابل للتصحيح التلقائي). | **لا**؛ يتطلب تدخلًا بشريًا وتعديل الحدث بمفتاح جديد. |
