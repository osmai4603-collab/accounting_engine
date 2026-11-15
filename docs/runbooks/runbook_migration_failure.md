# Runbook: فشل أو تعارض ترحيلات المخطط (Schema Migration Failure)

- **رمز الحادث:** `INC-RUNBOOK-02`
- **الخطورة:** P1 (حرجة)
- **المهمة:** `P6-06`
- **المكونات المتأثرة:** `schema_migrations`, `internal/adapters/postgres/migrations.go`

---

## 1. الأعراض والمؤشرات (Symptoms & Alerts)

- فشل إقلاع الخدمة أو فشل مسار `/readyz` مع خطأ:
  `storage schema validation failed: database schema is incompatible or has pending migrations`
- ظهور خطأ في السجلات: `applied migration checksum differs from source` أو `pending migration(s) unapplied`.
- تعطل النشر المستمر (Deployment Pipeline) أثناء مرحلة التحقق من المخطط.

---

## 2. الإجراءات التشخيصية (Diagnostics)

1. **الاستعلام عن حالة جدول الترحيلات في قاعدة البيانات:**
   ```sql
   SELECT version, name, checksum, applied_at 
   FROM schema_migrations 
   ORDER BY version DESC LIMIT 10;
   ```

2. **التحقق من الترحيل المتعارض أو المفقود:**
   - مقارنة رقم وبصمة آخر ترحيل مطبق مع ملفات المجلد `internal/adapters/postgres/migrations/`.
   - فحص هل تم تعديل محتوى ملف ترحيل مطبق بالفعل (تغير الـ Checksum).

---

## 3. خطوات الحل والمعالجة (Remediation Steps)

### الحالة الأولى: وجود ترحيلات معلقة (Pending Migrations)
1. تشغيل أمر تطبيق الترحيلات الأمامية يدوياً أو عبر أداة المحرك:
   ```bash
   go run ./cmd/accounting-service -migrate-up
   # أو تطبيقها مباشرة عبر دالة postgres.MigrateUp
   ```
2. التأكد من وصول كل الترحيلات المعلقة للجدول.

### الحالة الثانية: تعارض بصمة ترحيل سابق (Checksum Mismatch)
> [!CAUTION]
> **قاعدة صارمة:** لا تعدل ملف ترحيل قديم تم نشره وتطبيقه على بيئات التشغيل الإنتاجية.
1. استعادة النسخة الأصلية للملف التي تتطابق بصمتها مع ما في قاعدة البيانات (`git checkout HEAD~1 -- path/to/migration.sql`).
2. كتابة أي تعديل مطلوب كملف ترحيل تكميلي جديد بتاريخ ورقم تسلسلي حديث (`YYYYMMDDXXXX_fix_...sql`).
3. التأكد من وجود ملف `.down.sql` مطابق.

### الحالة الثالثة: فشل جزئي أثناء الترحيل (Aborted Migration Transaction)
1. عمليات الترحيل في المحرك محاطة بمعاملة (`BEGIN ... COMMIT`)، مما يعني عدم ترك ترحيلات معلقة نصف مكتملة.
2. مراجعة سبب الفشل في ملف SQL (نقص صلاحيات، وجود أقفال متضاربة على الجداول، أو انتهاك شروط القيد).
3. معالجة القيد المتعارض ثم إعادة تشغيل الترحيل.

---

## 4. التحقق بعد الحل (Post-Incident Verification)

```bash
go test -v ./internal/adapters/postgres/ -run TestValidateSchemaHealth_HealthyAndIncompatible
```
يجب أن يجتاز الاختبار بنجاح مع عودة `/readyz` لحالة 200.

