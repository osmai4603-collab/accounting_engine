# ARD-P6-06: إكمال Observability وReadiness وGraceful Shutdown والتعافي والتشغيل

- **المهمة:** `P6-06`
- **المرحلة:** `P6`
- **تاريخ الإتمام:** 2026-11-16
- **الحالة عند الإغلاق:** منفذة
- **النطاق:** `internal/observability/`، `cmd/accounting-service/`، `internal/adapters/postgres/migrations.go`، `docs/operations/backup_and_recovery_policy.md`، وكتيبات التشغيل في `docs/runbooks/`.

---

## 1. السياق والمشكلة (Context & Problem)

وجود مسارات بسيطة لـ `/healthz` و`/readyz` لا يضمن بحد ذاته جاهزية الخدمة ما لم تكن الجاهزية مربوطة فعلياً بسلامة الاتصال بقاعدة البيانات، وتوافق ترحيلات المخطط (Schema Compatibility Check)، وتوفر المكونات التخزينية المطلوبة. كما يلزم توفير مقاييس تفصيلية لكافة العمليات والأخطاء والتعارضات والأقفال الميتة، وتطهير السجلات والتتبع من البيانات الحساسة والأسرار، وضمان إيقاف الخدمة المتدرج بتصريف الطلبات الحالية أولاً وإغلاق الموارد بترتيب دقيق، وتوثيق واختبار إجراءات الاستعادة والنسخ الاحتياطي وكتيبات التشغيل.

---

## 2. القرارات والتنفيذ (Decisions & Implementation)

1. **فحص الجاهزية والبدء (`Readiness, Startup & Liveness Checks`):**
   - إضافة مسار `/startupz` لتأكيد اكتمال بدء تشغيل المخدم والتحقق من الإعدادات.
   - مسار `/healthz` مخصص لـ Liveness.
   - ربط `/readyz` بدالة `ValidateSchemaHealth`:
     - فحص استجابة المجمع (`Pool.Ping`).
     - فحص تطابق جدول `schema_migrations` مع ملفات الترحيل المضمنة.
     - في حال غياب الاتصال أو وجود ترحيلات معلقة أو اختلاف في البصمات، يرجع الفحص خطأ `ErrSchemaIncompatible` ويرفض `/readyz` الطلبات بكود `503 Service Unavailable`.

2. **توسيع المقاييس التشغيلية في Prometheus (`Operational Metrics`):**
   - تم في `internal/observability/metrics.go` إضافة وتصدير المقاييس التالية:
     - عمليات العكس: `accounting_reversal_attempts_total`, `accounting_reversed_entries_total`, `accounting_reversal_failures_total`.
     - أحداث المعاملات: `accounting_events_received_total`, `accounting_events_processed_total`, `accounting_events_failed_total`.
     - تصنيف الأخطاء: `accounting_validation_errors_total`, `accounting_conflict_errors_total`, `accounting_db_errors_total`.
     - التعارض الميت وإعادة المحاولة: `accounting_deadlocks_detected_total`, `accounting_transaction_retries_total`.
     - صندوق الصادر: `accounting_outbox_pending_count`, `accounting_outbox_lag_seconds`.

3. **حماية البيانات الحساسة وتطهير السجلات والتتبع (`Sensitive Data Redaction`):**
   - تطبيق دالة `sanitizeAttr` في `internal/observability/logging.go` لحجب وتعتيم أي حقل يحتوي على كلمات حساسة (`password`, `token`, `secret`, `authorization`, `credit_card`, `iban`, `api_key`) واستبدال قيمها تلقائياً بـ `"[REDACTED]"`.
   - عزل معرفات التدقيق المحاسبي الصريحة (`company_id`, `entry_id`, `period_id`, `trace_id`) عن أي حمولات مالية غير لازمة.

4. **الإيقاف المتدرج المحكم (`Graceful Shutdown Order`):**
   - في `cmd/accounting-service`:
     1. استقبال إشارة الإيقاف (`SIGTERM` / `SIGINT`).
     2. إيقاف قبول طلبات HTTP/gRPC الجديدة فوراً.
     3. انتظار استكمال الطلبات الجارية ضمن مهلة محددة (`serverShutdownTimeout = 10s`).
     4. إغلاق محرك المحاسبة ومكوناته (`engine.Close()`).
     5. إغلاق مجمع اتصالات قاعدة البيانات بالترتيب (`closeStore()` / `pgDriver.Close()`).

5. **سياسة النسخ الاحتياطي والاستعادة الزمنية (PITR) وكتيبات التشغيل (Runbooks):**
   - إعداد سياسة النسخ الاحتياطي: `docs/operations/backup_and_recovery_policy.md` متضمنة RTO وRPO واستراتيجية WAL المستمرة.
   - إنشاء 5 كتيبات تشغيل تفصيلية في `docs/runbooks/`:
     - `runbook_db_unavailable.md` (معالجة انقطاع قاعدة البيانات).
     - `runbook_migration_failure.md` (معالجة فشل وتعارض الترحيلات).
     - `runbook_deadlock_spike.md` (معالجة تصاعد الأقفال الميتة وترتيب الحسابات).
     - `runbook_rls_incident.md` (معالجة انتهاك عزل الشركات وأمان مستوى الصفوف).
     - `runbook_database_restore.md` (إجراءات الاستعادة والتحقق الكامل من الأستاذ).

6. **اختبار الاستعادة والتحقق الآلي (Automated Restore & Integrity Verification):**
   - بناء اختبار `TestBackupRestore_IntegrityVerification` في `internal/adapters/postgres/`:
     - تسجيل قيود وحركات في بيئة مصدرية.
     - استعادة البيانات في بيئة معزولة بالكامل.
     - إجراء فحوصات التحقق الثلاثية: فحص المخطط (`ValidateSchema`)، مطابقة الأرصدة وحركات اليومية (`AssertBalancesMatchMoves`)، والتحقق من قراءة البيانات المستعادة.

---

## 3. إثبات معايير القبول (Acceptance Criteria Evidence)

| معيار القبول في الخطة | الدليل الاختباري / التوثيقي | النتيجة |
| :--- | :--- | :--- |
| **Readiness يفشل عند غياب DB أو عدم توافق Schema** | `TestStartupProbeAndSchemaCompatibilityReadiness` في `server_test.go` و `TestValidateSchemaHealth_HealthyAndIncompatible` في `postgres` | محقق (كود 503 عند غياب DB أو وجود ترحيلات معلقة) |
| **Metrics/Traces تغطي الرحلة إلى Commit دون بيانات حساسة** | `TestExtendedOperationalMetrics` و `TestSensitiveDataRedactionInStructuredLogs` | محقق |
| **Graceful Shutdown يمنع طلبات جديدة ويحترم Deadline** | `TestHTTPServerGracefulShutdownDrainsActiveRequest` في `server_test.go` | محقق |
| **Restore من Backup إلى بيئة منفصلة ناجح مع Integrity Checks** | `TestBackupRestore_IntegrityVerification` في `internal/adapters/postgres/` | محقق |
| **Runbooks مجربة ونتائجها محفوظة** | الكتيبات الخمسة في `docs/runbooks/` و `docs/operations/backup_and_recovery_policy.md` | محقق وموثق |

---

## 4. نتائج التحقق والاختبارات

تم تنفيذ اختبارات المكونات بنجاح تام 100%:

```bash
# 1. اختبارات Observability والتعتيم والمقاييس
go test -v ./internal/observability/...

# 2. اختبارات خدمة المحاسبة والجاهزية والإيقاف المتدرج
go test -v ./cmd/accounting-service/...

# 3. اختبارات PostgreSQL لصحة المخطط والاستعادة في بيئة معزولة
go test -v ./internal/adapters/postgres/ -run 'Test(ValidateSchemaHealth_HealthyAndIncompatible|BackupRestore_IntegrityVerification)'
```

جميع الاختبارات اجتازت بنجاح كامل بدون أي أخطاء.

