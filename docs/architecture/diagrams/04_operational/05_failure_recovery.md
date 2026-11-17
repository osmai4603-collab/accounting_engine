# مخطط التعافي من الأعطال والأخطاء (Failure & Recovery Workflows)

- **النطاق (Scope):** مسارات التعافي الآلي والتشغيلي عند حدوث أعطال حرجة: تعليق المعاملات (Deadlocks)، انهيار العمليات بين الحجز والإكمال، انقطاع قاعدة البيانات، وفشل تطبيق التهجيرات.
- **مصدر الحقيقة (Source of Truth):** `docs/runbooks/*`, `docs/operations/backup_and_recovery_policy.md`, `internal/adapters/postgres/transactions.go`.
- **آخر تحقق (Last Verified):** 2026-11-17 (Commit: `dd1483a`).

---

## 1. المخطط المعماري: مسارات التعامل مع الأخطاء والتعافي

```mermaid
flowchart TD
    classDef failure fill:#fee2e2,stroke:#dc2626,stroke-width:2px,color:#7f1d1d;
    classDef retry fill:#fef3c7,stroke:#d97706,stroke-width:2px,color:#0f172a;
    classDef recover fill:#ecfdf5,stroke:#059669,stroke-width:2px,color:#0f172a;
    classDef manual fill:#f1f5f9,stroke:#64748b,stroke-width:2px,color:#0f172a;

    Fault["حدوث خطأ أو عطل تشغيلي (System Failure)"]:::failure

    Fault --> DeadlockErr{"خطأ تعليق تزامن<br/>PostgreSQL 40P01 / 40001<br/>(Deadlock / Serialization)"}:::failure
    Fault --> CrashStarted{"انهيار العملية<br/>أثناء حالة حدث 'started'<br/>قبل تسجيل 'completed'"}:::failure
    Fault --> MigrationFail{"فشل تشغيل Migration<br/>خطأ DDL أو انقطاع اتصال"}:::failure
    Fault --> OutageErr{"انقطاع الاتصال بقاعدة البيانات<br/>Database Unavailable (503)"}:::failure

    %% مسار التعافي من Deadlock
    DeadlockErr --> BackoffRetry["إعادة المحاولة التلقائية في النواة<br/><code>ExecuteWithRetry</code><br/>Exponential Backoff مع عشوائية (Jitter)"]:::retry
    BackoffRetry -- نجاح المحاولة التابعة --> Restored1["تم الترحيل بسلام واستقرار التسلسل"]:::recover
    BackoffRetry -- تجاوز الحد الأقصى للمحاولات --> AlertOps1["إطلاق إنذار تشغيلي: Alert Deadlock Spike"]:::manual

    %% مسار استعادة الأحداث العالقة
    CrashStarted --> IdempotentReplay["وصول إعادة تسليم الحدث (Event Re-delivery)"]:::retry
    IdempotentReplay --> CheckMoveExists{"هل يوجد قيد مسجل<br/>لهذا المفتاح بالدفتر العام؟"}:::retry
    CheckMoveExists -- نعم --> MarkComplete["تحديث حالة الحدث إلى 'completed'<br/>وإرجاع نتيجة القيد السابق فورًا"]:::recover
    CheckMoveExists -- لا --> ResumePost["إعادة تشغيل ترحيل القيد مجددًا<br/>وتثبيت المعاملة دون تكرار"]:::recover

    %% مسار فشل التهجيرات
    MigrationFail --> RunbookMigration["تطبيق دليل التشغيل: <code>runbook_migration_failure.md</code><br/>1. فحص جدول schema_migrations<br/>2. تنفيذ ملف التراجع المعكوس (.down.sql)<br/>3. معالجة القيد المتعارض وإعادة التشغيل"]:::manual

    %% مسار انقطاع قاعدة البيانات
    OutageErr --> HealthCheck["فحص الجاهزية (Readiness Probe 503)<br/>عزل الخدمة عن موزع الأحمال (Load Balancer)<br/>إعادة الاتصال التدريجي بحوض pgxpool"]:::manual
```

---

## 2. جدول تصنيف الأخطاء وإجراءات التعافي

| نوع العطل / الفشل | رمز الخطأ | آلية التعافي (Recovery Mechanism) | الأثر على البيانات |
| --- | --- | --- | --- |
| **تعليق متزامن (Deadlock)** | `40P01` / `40001` | إعادة محاولة تلقائية في Go عبر `ExecuteWithRetry` مع تأخير أسي. | **صفر أثر؛** تتراجع المعاملة ثم تنجح لاحقًا. |
| **انهيار الخادم بعد الحجز** | حالة `started` عالقة | آلية الحجز الذكي `ClaimPublicEventIngestion` تفحص القيد وتصلحه عند أول إعادة تسليم. | **لا تكرار للقيود؛** الحفاظ على سلامة الدفتر. |
| **تعثر تهجير DDL** | `migration error` | كل ملف `.sql` مقترن بملف `.down.sql` صريح قابل للتراجع التام. | استعادة المخطط السابق فورًا. |
| **تلف وسائط أو كارثة خادم** | `Disaster / Loss` | استعادة لقطة قاعدة البيانات اليومية وتطبيق ملفات WAL (Point-in-Time Recovery). | استرجاع الحالة حتى آخر ثانية قبل الكارثة. |
