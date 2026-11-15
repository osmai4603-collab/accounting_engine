# سياسة النسخ الاحتياطي والتعافي والاستعادة الزمنية (Backup, Recovery & PITR Policy)

- **المهمة:** `P6-06`
- **الحالة:** معتمد
- **النطاق:** PostgreSQL، مجمع الاتصالات، ترحيلات المخطط، فحوصات السلامة، واستعادة البيانات المحاسبية.

---

## 1. الأهداف والمبادئ (Objectives & Principles)

1. **RPO (Recovery Point Objective):** $\le 5$ دقائق للبيانات المحاسبية المعاملاتية باستخدام أرشفة سجلات الكتابة المسبقة المتواصلة (Continuous WAL Archiving).
2. **RTO (Recovery Time Objective):** $\le 30$ دقيقة لاستعادة بيئة كاملة وجعلها في حالة جاهزية (`/readyz` = 200).
3. **عدم قبول أي فقدان للبيانات المعتمدة (Zero Loss on Posted Moves):** القيود المرحلة (`posted`) والأستاذ العام غير قابلة للتعديل وتتمتع بحماية على مستوى قاعدة البيانات (`Immutability Triggers`).
4. **فصل النسخ الاحتياطي جغرافياً:** تخزين النسخ المشفرة في مساحة تخزين كائنية مستقلة (e.g. S3 / GCS / Geo-redundant bucket) مع سياسة احتفاظ (Retention Policy) لا تقل عن 7 سنوات للأغراض الضريبية والقانونية.

---

## 2. استراتيجية النسخ الاحتياطي (Backup Strategy)

### 2.1 النسخ الكامل والتزايدي (Full & Incremental Backups)

- **النسخ الكامل اليومي (Daily Physical Base Backup):**
  يتم أخذ نسخة أساسية باستخدام `pg_basebackup` يومياً في فترات انخفاض الحمل التشغيلي (02:00 UTC):

  ```bash
  pg_basebackup -h $PGHOST -U replicator -D /backups/base_$(date +%Y%m%d_%H%M%S) -Ft -z -P -X stream
  ```

- **أرشفة سجلات WAL المستمرة (Continuous WAL Archiving):**
  تفعيل `archive_mode = on` مع نقل السجلات كل 60 ثانية أو عند امتلاء 16MB عبر `archive_command` مشفر:

  ```bash
  archive_command = 'test ! -f /wal_archive/%f && gzip < %p > /wal_archive/%f'
  ```

### 2.2 النسخ المنطقي للشركات الفردية (Tenant-Level Logical Dump)

- يمكن أخذ نسخة منطقية لشركة محددة عند الحاجة لتسليم بيانات مستأجر:

  ```bash
  pg_dump -h $PGHOST -U accounting_admin -d accounting_db --schema=public \
    --table=account_moves --table=account_move_lines --table=accounts \
    --file=/backups/tenant_${COMPANY_ID}_$(date +%Y%m%d).sql
  ```

---

## 3. الاستعادة إلى نقطة زمنية محددة (Point-in-Time Recovery - PITR)

عند وقوع تلف كارثي أو خطأ إجرائي يستوجب الرجوع لنقطة سابقة:

1. إيقاف الخدمة أو تحويلها لوضع الصيانة: إيقاف استقبال الطلبات الخارجية عبر قطع مسار `/readyz`.
2. استعادة أحدث نسخة أساسية كاملة تسبق نقطة الهدف:

   ```bash
   tar -xvf /backups/base_YYYYMMDD_HHMMSS.tar.gz -C /var/lib/postgresql/data/
   ```

3. إعداد ملف إشارات الاستعادة `recovery.signal` وضبط `postgresql.conf`:

   ```ini
   restore_command = 'gunzip < /wal_archive/%f > %p'
   recovery_target_time = '2026-11-15 23:45:00 UTC'
   recovery_target_action = 'promote'
   ```

4. تشغيل PostgreSQL ومراقبة سجلات إعادة التشغيل وصولاً إلى Target LSN.

---

## 4. التحقق من سلامة البيانات بعد الاستعادة (Post-Restore Integrity Checks)

قبل إتاحة قاعدة البيانات المستعادة للخدمة وتشغيلها، **يجب إلزامياً** تنفيذ ثلاث خطوات تحقق آلية:

1. **فحص توافق المخطط (Schema Compatibility Check):**
   - التحقق من تطبيق جميع الترحيلات وعدم وجود ترحيلات معلقة أو تعديل غير مطابق في البصمات:

   ```bash
   # عبر محرك المحاسبة
   go test -v ./internal/adapters/postgres/ -run TestValidateSchemaHealth_HealthyAndIncompatible
   ```

2. **مطابقة توازن الأستاذ العام والأرصدة المحققة (Ledger Balance Reconciliation):**
   - استدعاء `AssertBalancesMatchMoves` أو `Reconcile` لجميع الشركات للتأكد من عدم وجود أي انحراف (`Zero Drift`):

   ```go
   driver.AssertBalancesMatchMoves(ctx, companyID, periodID, currency)
   ```

3. **فحص ترابط سلسلة التدقيق (Audit Chain Integrity):**
   - التحقق من بصمات التجزئة المشفرة في `governance.VerifyAuditIntegrity` للتأكد من عدم وجود أي سجل محذوف أو معدل سراً أثناء الاستعادة.

---

## 5. سياسة اختبار خطة الاستعادة (Drill Schedule)

- **اختبار دوري ربع سنوي (Quarterly Recovery Drill):**
  استعادة تلقائية لأحدث نسخة احتياطية في بيئة معزولة (Staging/Drill Environment)، وتشغيل حزمة الاختبارات الكاملة:

  ```bash
  go test -v ./internal/adapters/postgres/ -run TestBackupRestore_IntegrityVerification
  ```

  وتسجيل وقت الاستعادة الفعلي ومطابقته لـ RTO.
