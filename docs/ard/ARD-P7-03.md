# ARD-P7-03: التحقق من الترحيلات والنشر والنسخ الاحتياطي والاستعادة والجاهزية

- **المهمة:** `P7-03`
- **المرحلة:** `P7`
- **تاريخ الإتمام:** 2026-11-16
- **الحالة عند الإغلاق:** منفذة
- **النطاق:** `internal/adapters/postgres/`، `docs/migration_policy.md`، `docs/operations/backup_and_recovery_policy.md`، `docs/runbooks/runbook_migration_failure.md`، `docs/runbooks/runbook_database_restore.md`.

---

## 1. السياق والمشكلة (Context & Problem)

مجرد التحقق من صحة البصمات (Checksums) لا يكفي لإثبات أن ترقية قاعدة البيانات آمنة، أو أن التراجع (Rollback) أو الاستعادة (Restore/PITR) ممكنة دون فقد بيانات، أو أن نسخة التطبيق الحالية متوافقة تشغيلياً مع مخطط قاعدة البيانات أثناء النشر المتدرج (Rolling Deployment).
كانت المهمة `P7-03` مكتملة جزئياً حيث وُضعت مسودات السياسات وكتيبات التشغيل ولكن كان ينقص استكمال إثباتات الترقية الآلية من أقدم مخطط مدعوم، واختبار فشل الترحيل وتراجعه الذري وإعادة المحاولة، وفحص السلوك عند وجود إصدارات ترحيل غير معروفة أو بصمات معدلة، والتحقق الآلي من سلامة الأرصدة والقيود بعد الاستعادة.

---

## 2. القرارات والتنفيذ (Decisions & Implementation)

1. **اعتماد سياسة النشر والتوافق المتدرج (Expand/Contract Policy):**
   - توثيق نمط التغيير غير المتوافق عبر 4 مراحل منفصلة في `docs/migration_policy.md`:
     1. **Expand:** إضافة الحقول/الجداول بصيغة اختيارية ومتوافقة مع النسخ القديمة.
     2. **Backfill:** ملء البيانات على دفعات قابلة للاستئناف.
     3. **Switch:** توجيه التطبيق الجديد لاستخدام المخطط الموسع.
     4. **Contract:** حذف الحقول القديمة بعد إيقاف كافة النسخ القديمة.
   - تحديد متى يكون Rollback آمناً ومتى يكون Roll-forward عبر هجرة تصحيحية إلزامياً.

2. **الترقية الآمنة من أقدم مخطط مدعوم (Upgrade from Baseline):**
   - تنفيذ اختبار التكامل `TestMigrationUpgradeFromOldestSupportedSchemaPreservesData` في `internal/adapters/postgres/migrations_upgrade_integration_test.go`:
     - تطبيق المخطط التأسيسي الأولي (`202611090001_baseline_tables.up.sql`).
     - بذر بيانات شركات وحسابات تمثل بيانات عميل فعلية.
     - الترقية عبر كامل سلسلة الترحيلات إلى الإصدار الحالي.
     - إثبات بقاء بيانات الشركة دون تعديل أو فقدان واجتياز فحص `ValidateSchemaHealth`.

3. **ذرية فشل الترحيل وإعادة المحاولة (Atomic Migration Failure & Retry):**
   - تنفيذ اختبار `TestFailedMigrationRollsBackAndCanBeRetried` في `internal/adapters/postgres/migrations_integration_test.go`:
     - حقن فشل اصطناعي أثناء تسجيل الترحيل في جدول `schema_migrations`.
     - إثبات تراجع المعاملة بالكامل (`ROLLBACK`) وعدم تسجيل الترحيل كمنفذ وتراجع تعديلات DDL المتزامنة.
     - إثبات إرجاع `ValidateSchemaHealth` لخطأ `ErrSchemaIncompatible`.
     - إزالة الفشل وإعادة تشغيل الترحيل بنجاح واجتياز الفحص التشغيلي.

4. **رفض المخططات المجهولة والبصمات المعدلة (Unknown Migration & Checksum Guard):**
   - تحديث دالة `pendingMigrations` في `internal/adapters/postgres/migrations.go` لترفض قواعد البيانات التي تحتوي إصدار ترحيل غير معروف للنسخة الحالية من التطبيق (`database has unknown applied migration version %d`).
   - اختبار رفض الترحيلات المستقبلية أو غير المعروفة في `TestValidateSchemaHealthRejectsUnknownAppliedMigration`.
   - اختبار رفض أي تعديل على بصمات الترحيلات المطبقة في `TestValidateSchemaHealthRejectsChangedMigrationChecksum`.

5. **النسخ الاحتياطي والاستعادة والتحقق المحاسبي (Backup, PITR & Integrity Verification):**
   - اعتماد سياسة النسخ والاستعادة في `docs/operations/backup_and_recovery_policy.md` متضمنة RPO/RTO وأوامر `pg_dump` و`pg_restore` و`pg_basebackup`.
   - توفير كتيب التشغيل التفصيلي `docs/runbooks/runbook_database_restore.md` متضمناً خطوات الاستعادة المعزولة وإجراءات التحقق الثلاثية:
     1. فحص الاتصال وتطابق المخطط عبر `ValidateSchemaHealth`.
     2. فحص الجاهزية التشغيلية عبر `/readyz`.
     3. فحص الأرصدة وحركات اليومية ومطابقتها (`AssertBalancesMatchMoves`).
   - إثبات ذلك برمجياً في اختبار `TestBackupRestore_IntegrityVerification` في `internal/adapters/postgres/p6_06_observability_and_recovery_test.go`.

---

## 3. إثبات معايير القبول (Acceptance Criteria Evidence)

| معيار القبول في الخطة | الدليل الاختباري / التوثيقي | النتيجة |
| :--- | :--- | :--- |
| **ترقية من Schema مدعوم تنتهي دون فقد بيانات** | `TestMigrationUpgradeFromOldestSupportedSchemaPreservesData` | محقق (تم اختبار الترقية من baseline وحفظ بيانات الشركة بنجاح) |
| **Migration الفاشلة لا تترك Schema مجهولًا يسمح للتطبيق بالعمل** | `TestFailedMigrationRollsBackAndCanBeRetried` و `TestValidateSchemaHealthRejectsUnknownAppliedMigration` | محقق (تراجع ذري للـ DDL وفحص Readiness يرجع `503 ErrSchemaIncompatible`) |
| **لكل Migration غير متوافق استراتيجية موثقة** | `docs/migration_policy.md` و `docs/runbooks/runbook_migration_failure.md` | محقق (توثيق كامل لنمط Expand/Contract وسياسة Rollback vs Roll-forward) |
| **Restore مجرب وينتج DB تجتاز Integrity Checks** | `TestBackupRestore_IntegrityVerification` و `docs/runbooks/runbook_database_restore.md` | محقق (مطابقة تامة للأرصدة وسجلات اليومية واجتياز الفحص) |

---

## 4. نتائج التحقق والاختبارات

```bash
# 1. اختبارات الترقية والذرية وصحة المخطط
go test -v ./internal/adapters/postgres -run 'TestFailedMigrationRollsBackAndCanBeRetried|TestMigrationUpgradeFromOldestSupportedSchemaPreservesData|TestValidateSchemaHealth'

# 2. اختبار الاستعادة وفحص الأرصدة والنزاهة
go test -v ./internal/adapters/postgres -run 'TestBackupRestore_IntegrityVerification'
```

تم اجتياز جميع الاختبارات بنجاح.

