# سياسة الهجرات والتوافق الخلفي

- **الحالة:** معتمد
- **التاريخ:** 2026-11-12
- **المراجعة:** 2026-11-12
- **المالك:** `DBA` + `ARCH`
- **المهمة:** `P0-T8`
- **المرجع البرمجي:** `storage/postgres/migrations.go`, `storage/postgres/migrations_guard_test.go`

## 1. المبادئ

1. **التوافق الخلفي أولاً:** يجب أن يعمل الإصدار القديم من التطبيق مع المخطط الجديد أثناء النشر المتدرج (rolling deploy).
2. **كل هجرة قابلة للتراجع:** لكل هجرة أمامية ملف `.down.sql` غير فارغ.
3. **لا فقدان بيانات مالية:** لا حذف/اقتطاع دون أرشفة وخطة Contract معتمدة.
4. **الفحص الآلي بوابة:** `go test ./storage/postgres -run TestDangerousMigrationsRequireApproval` يرفض الأنماط الخطرة.
5. **لا تعديل هجرة مطبقة:** checksum في `schema_migrations` يمنع ذلك؛ التصحيح بهجرة جديدة.

## 2. نمط Expand/Contract

كل تغيير غير متوافق يمر بأربع مراحل في **إصدارات منفصلة**:

| المرحلة | الإجراء | مثال |
| --- | --- | --- |
| **Expand** | أضف العمود/الجدول/الفهرس بصيغة متوافقة مع القديم | `ADD COLUMN status text NULL` |
| **Backfill** | املأ البيانات على دفعات قابلة للاستئناف | `UPDATE ... WHERE id BETWEEN ...` |
| **Switch** | انقل القراءة/الكتابة إلى الشكل الجديد | كود يقرأ `status` الجديد |
| **Contract** | احذف الشكل القديم في إصدار لاحق | `DROP COLUMN legacy_status` |

> لا يجوز دمج Expand وContract في إصدار واحد. المراجعة ترفض ذلك حتى لو كان الاختبار أخضر.

## 3. الهجرات الممنوعة

يمنع حارس الهجرات (`migrations_guard_test.go`) الأنماط التالية في الملفات الأمامية (عدا الأساس التاريخي):

| النمط | السبب |
| --- | --- |
| `DROP COLUMN` | يكسر الإصدار القديم ويفقد بيانات |
| `DROP TABLE` | فقدان بيانات كامل |
| `ALTER COLUMN ... TYPE` | قد يعيد كتابة الجدول ويكسر القراءة القديمة |
| `DELETE FROM` | حذف بيانات مالية دون أرشفة |
| `TRUNCATE <table>` | فقدان بيانات فوري |

### الاستثناءات المعتمدة (Grandfathered)

| الهجرة | السبب | الحالة |
| --- | --- | --- |
| `202611010001_baseline_schema` | مخطط التأسيس الأول، لا توجد بيانات سابقة | مسموح تاريخياً |
| `202611110005_audit_payload_bytes` | تحويل `payload_before/after` من `jsonb` إلى `bytea` مرة واحدة، معتمد قبل تفعيل السياسة | مسموح؛ لا يُكرر |

أي استثناء جديد يتطلب:
1. ADR أو ملحق قرار معتمد.
2. خطة ترحيل بيانات وتراجع.
3. إضافة السبب إلى `grandfatheredDangerousMigrations` في الاختبار.
4. مراجعة `DBA` + `ARCH`.

## 4. قواعد إضافية

- إضافة `NOT NULL` على جدول قائم تتطلب `DEFAULT` أو Backfill مكتمل قبل القيد.
- القيود الجديدة على بيانات قائمة تُضاف `NOT VALID` ثم تُتحقق في خطوة منفصلة.
- الفهارس على جداول كبيرة تُنشأ `CONCURRENTLY` خارج المعاملة (مع توثيق ذلك في الهجرة).
- أسماء الملفات: `<12 رقم إصدار>_<snake_case>.sql` و`.down.sql`، والإصدارات متزايدة بلا تكرار.
- كل هجرة مالية تحمل `company_id` في المفاتيح والفهارس، وتخضع لـRLS.

## 5. النسخ الاحتياطي والتراجع

قبل أي هجرة إنتاجية:

1. لقطة/نسخة احتياطية كاملة مع التحقق من الاستعادة (PITR إن توفر).
2. تسجيل `schema_migrations` قبل النشر.
3. خطة تراجع:
   - إذا كانت الهجرة Expand فقط: التراجع = إيقاف الكود الجديد، لا حاجة لـdown.
   - إذا كانت تحتوي Backfill: خطة إيقاف/استئناف + عكس الدفعة.
   - إذا كانت Contract: لا تُنفّذ إلا بعد التأكد من عدم وجود كود قديم.
4. نافذة تنفيذ وتواصل مع الفريق.

## 6. اختبارات إلزامية

| الاختبار | ماذا يثبت |
| --- | --- |
| `TestMigrationsHaveReversiblePairs` | كل هجرة أمامية لها down غير فارغ، والأسماء صحيحة |
| `TestDangerousMigrationsRequireApproval` | لا أنماط خطرة غير معتمدة |
| `TestCleanMigrationLifecycle` | السلسلة كاملة تصعد وتهبط وتصعد بلا تعارض checksum |
| `TestMigrationUpgradeFromPreviousVersionPreservesData` | الترقية من الإصدار السابق تحافظ على بيانات قائمة |
| `TestEveryFinancialTableHasForcedRLS` | كل جدول مالي عليه RLS + FORCE + سياسة |

تشغيل محلي:

```bash
POSTGRES_URL='postgres://user@localhost:5432/accounting_test?sslmode=disable' \
  go test -count=1 ./storage/postgres -run 'TestMigrations|TestCleanMigrationLifecycle|TestBaselineMigrationCleanRun|TestMigrationUpgradeFromPreviousVersionPreservesData|TestEveryFinancialTableHasForcedRLS'
```

## 7. إجراء الطوارئ عند فشل هجرة

1. **أوقف النشر** ولا تعِد تشغيل الإصدار الجديد.
2. حدد هل فشل في معاملة (rollback تلقائي) أم بعد commit جزئي.
3. إن فشل داخل معاملة: صحّح الهجرة في إصدار جديد، لا تعدّل الملف المطبق.
4. إن نجح commit جزئياً: استخدم `.down.sql` للهجرة الأخيرة إن كان آمناً، وإلا استعد النسخة الاحتياطية/PITR.
5. سجّل الحادث بـ`trace_id`، والهجرة، والإصدار، والوقت، والأثر.
6. أضف اختبار انحدار يمنع تكرار السبب.

## 8. المراجع

- `docs/adr/ADR-0005-storage-parity-and-migrations.md`
- `storage/postgres/migrations/`
- `storage/postgres/migrations_integration_test.go`
- `storage/postgres/migrations_upgrade_integration_test.go`
