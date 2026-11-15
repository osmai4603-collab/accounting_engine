# Runbook: تعطل أو انقطاع الاتصال بقاعدة البيانات (Database Unavailable)

- **رمز الحادث:** `INC-RUNBOOK-01`
- **الخطورة:** P1 (حرجة)
- **المهمة:** `P6-06`
- **المكونات المتأثرة:** PostgreSQL, `cmd/accounting-service`, `internal/adapters/postgres/`

---

## 1. الأعراض والمؤشرات (Symptoms & Alerts)

- تنبيه مراقبة: `AccountingServiceDatabaseUnavailable` أو `ReadinessProbeFailing`.
- مسار `/readyz` يرجع كود `503 Service Unavailable`.
- مقياس `accounting_db_errors_total` يتصاعد بمعدل مرتفع.
- السجلات (Logs) تحتوي على: `connection refused`, `dial tcp: timeout`, أو `failed to acquire connection from pool`.

---

## 2. الإجراءات التشخيصية السريعة (Immediate Diagnostics)

1. **فحص حالة المخدم ومسار الجاهزية:**
   ```bash
   curl -i http://localhost:8080/readyz
   curl -i http://localhost:8080/healthz
   ```
   *(إذا كان `/healthz` يرجع 200 و`/readyz` يرجع 503، فالمشكلة محصورة في التخزين أو اتصاله).*

2. **فحص اتصال شبكة PostgreSQL:**
   ```bash
   pg_isready -h $PGHOST -p $PGPORT -U accounting_app_user
   ```

3. **فحص موارد مجمع الاتصالات (Connection Pool Exhaustion):**
   ```sql
   SELECT count(*), state FROM pg_stat_activity WHERE datname = 'accounting_db' GROUP BY state;
   ```

---

## 3. خطوات الحل والمعالجة (Remediation Steps)

### أ. في حال امتلاء مجمع الاتصالات (Connection Pool Exhaustion)
1. فحص الاتصالات العالقة أو بطيئة التنفيذ:
   ```sql
   SELECT pid, now() - xact_start AS duration, query 
   FROM pg_stat_activity 
   WHERE state = 'active' AND now() - xact_start > interval '10 seconds';
   ```
2. إنهاء الاستعلامات العالقة غير الحرجة:
   ```sql
   SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE now() - xact_start > interval '30 seconds';
   ```

### ب. في حال فشل خادم PostgreSQL (Crash or Restart Required)
1. إعادة تشغيل خدمة PostgreSQL أو تحويل الحمل التلقائي للمخدم الثانوي (Failover to Replica):
   ```bash
   sudo systemctl restart postgresql
   ```
2. فور عودة قاعدة البيانات، سيتصل المجمع تلقائياً ويعود `/readyz` لحالة `200 OK` (READY) بدون الحاجة لإعادة تشغيل خدمة المحرك.

---

## 4. التحقق بعد الحل (Post-Incident Verification)

1. التأكد من نجاح فحص الجاهزية:
   ```bash
   curl -s -o /dev/null -w "%{http_code}\n" http://localhost:8080/readyz
   # يجب أن تكون النتيجة 200
   ```
2. تتبع مقياس استقرار المعاملات في `/metrics`.

