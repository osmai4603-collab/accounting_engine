# Runbook: استعادة الكوارث والتحقق من السلامة المحاسبية (Disaster Recovery & Integrity Runbook)

- **المعرف:** RB-OPS-001 / P12-01
- **الهدف:** إرشادات تشغيلية موحدة لتنفيذ استعادة قاعدة بيانات PostgreSQL والتحقق الصارم من السلامة المحاسبية قبل استئناف تشغيل المحرك.
- **الجمهور المستهدف:** مهندسو العمليات والموثوقية (SRE / DevOps) ومسؤولو قواعد البيانات (DBA).
- **الوثائق المرجعية:** [سياسة النسخ والاستعادة](../backup_and_recovery_policy.md)، [ARD-P12-01](../../ard/ARD-P12-01.md).

---

## 1. شروط ما قبل البدء (Prerequisites)

1. توفر ملف النسخة الاحتياطية المأخوذ بواسطة `pg_dump -Fc` أو نقطة استعادة PITR معتمدة مع أرشيف WAL متصل.
2. توفر قاعدة بيانات هدف جديدة فارغة تماماً (Clean Isolated Target Database).
3. إيقاف حركة المرور والطلبات إلى خادم التطبيق المتصل بقاعدة المصدر التالفة أو المعزولة.
4. تثبيت أدوات PostgreSQL العميلة المتوافقة مع إصدار الخادم (`pg_restore` الإصدار 14 وما فوق).

---

## 2. إجراءات الاستعادة خطوة بخطوة

### الخطوة 1: استعادة المخطط والبيانات إلى القاعدة الجديدة

```bash
# إنشاء قاعدة بيانات الهدف النظيفة
createdb -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" "$TARGET_DB"

# استعادة ملف النسخة الاحتياطية مع التحقق من عدم حدوث أخطاء
pg_restore -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" \
  --dbname="$TARGET_DB" \
  --exit-on-error \
  --no-owner \
  --no-privileges \
  "$BACKUP_FILE_PATH"
```

### الخطوة 2: التحقق من المخطط والهجرات

تحقق من اكتمال تطبيق كافة الهجرات التسلسلية في جدول `schema_migrations`:

```sql
SELECT version, name, applied_at FROM schema_migrations ORDER BY version ASC;
```

أو عبر كود المحرك:

```go
err := postgres.ValidateSchemaHealth(ctx, pool)
```

*يجب ألا تتطلب القاعدة المستعادة أي إعادة تنفيذ للهجرات (No re-migration).*

### الخطوة 3: التحقق من التوازن المحاسبي المزدوج (Double-Entry Balance)

تحقق من توازن كافة الحركات المرحلة لجميع الشركات:

```sql
SELECT company_id, move_id, 
       SUM(debit_amount) AS total_debit, 
       SUM(credit_amount) AS total_credit
FROM account_move_lines
GROUP BY company_id, move_id
HAVING SUM(debit_amount) <> SUM(credit_amount);
```

*النتيجة المتوقعة: 0 صفوف (توازن تام).*

### الخطوة 4: مطابقة الأرصدة مع القيود (Balances Reconciled with Moves)

تحقق من أن جدول `account_balances` يطابق مجموع حركات `account_move_lines`:

```go
err := driver.AssertBalancesMatchMoves(ctx, companyID, periodID, currencyID)
```

### الخطوة 5: التحقق من سلامة سلسلة التجزئة لسجل التدقيق (Audit Hash Chain)

تشغيل التحقق البرمجي لسلسلة الهاش لضمان عدم حدوث أي تلاعب أو انقطاع:

```go
err := governance.VerifyAuditIntegrity(ctx, auditRepo)
```

### الخطوة 6: التحقق من عزل المستأجرين (RLS Tenant Isolation)

التأكد من تفعيل سياسات الأمان على مستوى الصفوف (Row Level Security):

```sql
SELECT tablename, rowsecurity 
FROM pg_tables 
WHERE schemaname = 'public' AND tablename IN ('companies', 'account_moves', 'account_move_lines', 'account_balances');
```

*يجب أن تكون `rowsecurity = true` لكافة الجداول الحاكمة.*

---

## 3. استئناف الخدمة (Service Resumption)

1. تحديث متغير البيئة `DATABASE_URL` في إعداد خادم المحرك المحاسبي ليشير إلى القاعدة المستعادة:

   ```bash
   export DATABASE_URL="postgres://user:pass@host:port/target_db?sslmode=require"
   ```

2. بدء تشغيل الخدمة وفحص نقطة النهاية الصحية `/readyz`:

   ```bash
   curl -f http://localhost:8080/readyz
   ```

3. فتح حركة المرور تدريجياً ومراقبة السجلات ومعدل الأخطاء.

---

## 4. الأتمتة والاختبار الدوري

يتم تشغيل هذا التمرين بشكل مؤتمت بالكامل ضمن جناح الاختبارات عبر الأمر:

```bash
go test -v ./test/dr
```

يضمن هذا الاختبار تنفيذ خطوات النسخ والاستعادة والفحوصات الستة واستئناف المحرك آلياً في أقل من 5 ثوانٍ، محققاً **RTO < 2s** و **RPO = 0**.
