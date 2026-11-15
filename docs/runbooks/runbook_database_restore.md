# Runbook: استعادة قاعدة البيانات والتحقق من سلامة الأستاذ (Database Restore & Verification)

- **رمز الحادث:** `INC-RUNBOOK-05`
- **الخطورة:** P1 (حرجة)
- **المهمة:** `P6-06`
- **المكونات المتأثرة:** `pg_restore`, `ValidateSchemaHealth`, `AssertBalancesMatchMoves`, `VerifyAuditIntegrity`

---

## 1. شروط البدء (Prerequisites & Triggers)

- الحاجة لاستعادة قاعدة البيانات بعد فشل عتادي كارثي، أو اختبار دوري لبيئة الاستعادة (Recovery Drill)، أو ترحيل بيئة الإنتاج إلى مزود سحابي جديد.
- توفر النسخة الاحتياطية المعتمدة (Base Backup + WAL Archives).

---

## 2. إجراءات الاستعادة خطوة بخطوة (Step-by-Step Recovery)

### الخطوة 1: تجهيز بيئة الاستعادة المعزولة (Target Environment Provisioning)
1. إنشاء قاعدة بيانات جديدة أو تهيئة خادم PostgreSQL فارغ مع تثبيت الامتدادات المطلوبة (`pgcrypto`, `uuid-ossp`).
2. التأكد من تطابق إصدار PostgreSQL (16+).

### الخطوة 2: فك واستعادة النسخة الاحتياطية (Restore Files)
```bash
# استعادة ملفات النسخة الاحتياطية الأساسية
tar -zxvf /backups/base_backup_latest.tar.gz -C /var/lib/postgresql/data/

# تكوين مسار استعادة WAL
cat << EOF >> /var/lib/postgresql/data/postgresql.conf
restore_command = 'cp /wal_archive/%f %p'
recovery_target = 'immediate'
EOF

touch /var/lib/postgresql/data/recovery.signal
sudo systemctl start postgresql
```

### الخطوة 3: التحقق من توافق المخطط (Schema Validation)
- فحص أن جميع الترحيلات مطبقة ومتطابقة مع بصمات الكود:
```bash
go test -v ./internal/adapters/postgres/ -run TestValidateSchemaHealth_HealthyAndIncompatible
```

### الخطوة 4: التحقق من توازن ومطابقة الأستاذ العام (Balance Reconciliation Integrity)
- تشغيل فحص التطابق التام بين القيود اليومية والأرصدة المحققة لجميع الشركات:
```bash
go test -v ./internal/adapters/postgres/ -run TestBackupRestore_IntegrityVerification
```
- التأكد من عدم وجود أي انحراف (`Drift = 0`).

### الخطوة 5: التحقق من سلسلة التدقيق غير القابلة للعبث (Audit Chain Check)
- فحص ترابط الـ Merkle Hash Chain لسجلات التدقيق المحاسبي:
```bash
go test -v ./internal/adapters/postgres/ -run TestVerifyAuditIntegrityDetectsChainTampering
```

### الخطوة 6: تبديل مسار حركة المرور (Traffic Promotion)
- فور اجتياز الفحوصات الثلاثة بنجاح:
  1. توجيه متغيرات البيئة `DATABASE_URL` نحو قاعدة البيانات المستعادة.
  2. تشغيل خدمة المحرك `accounting-service`.
  3. التحقق من كود فحص الجاهزية:
     ```bash
     curl -i http://localhost:8080/readyz
     # المتوقع: HTTP/1.1 200 OK (READY)
     ```

---

## 3. معايير النجاح (Success Criteria)

- [x] مسار `/readyz` يرجع `200 OK`.
- [x] رصيد ميزان المراجعة متوازن لجميع الشركات المستعادة.
- [x] ترابط كامل لسلسلة التدقيق دون ثغرات أو بصمات مكسورة.
- [x] عدم وجود أي قيود مفقودة أو غير متسلسلة.

