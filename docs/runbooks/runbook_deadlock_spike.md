# Runbook: تصاعد أقفال التعارض الميت (Deadlock Spike Incident)

- **رمز الحادث:** `INC-RUNBOOK-03`
- **الخطورة:** P2 (عالية)
- **المهمة:** `P6-06`
- **المكونات المتأثرة:** `accounting_deadlocks_detected_total`, `accounting_transaction_retries_total`, `account_balances`

---

## 1. الأعراض والمؤشرات (Symptoms & Alerts)

- تنبيه مراقبة: `AccountingDeadlockSpikeAlert` (تصاعد مؤشر `accounting_deadlocks_detected_total`).
- زيادة زمن الاستجابة في `accounting_transaction_duration_seconds`.
- تسجيل أخطاء من نوع PostgreSQL: `ERROR: deadlock detected (SQLSTATE 40P01)`.
- تصاعد معدل إعادة المحاولة للمعاملات (`accounting_transaction_retries_total`).

---

## 2. الإجراءات التشخيصية (Diagnostics)

1. **معرفة المعاملات والجداول المتنازعة من سجلات PostgreSQL:**
   ```sql
   SELECT pid, query_start, wait_event_type, wait_event, query 
   FROM pg_stat_activity 
   WHERE wait_event_type = 'Lock' AND state = 'active';
   ```

2. **تحليل ترتيب حجز الأقفال في الكود:**
   - عادة ما تنشأ الأقفال الميتة في الأنظمة المحاسبية عند محاولة عمليتين متزامنتين حجز صفوف حسابات في `account_balances` أو أرقام قيود متسلسلة بترتيب مختلف (مثلاً: المعاملة A تحجز الحساب 1 ثم 2، بينما المعاملة B تحجز الحساب 2 ثم 1).

---

## 3. خطوات الحل والمعالجة (Remediation Steps)

1. **التحقق من سياسة الترتيب الحتمي للأقفال (Deterministic Locking Order):**
   - يفرض المحرك في `internal/adapters/postgres/posting.go` حجز صفوف `account_balances` مرتبة دائماً تصاعدياً بحسب معرف الحساب (`ORDER BY account_id ASC`):
   ```sql
   SELECT account_id FROM account_balances 
   WHERE company_id = $1 AND fiscal_period_id = $2 AND account_id = ANY($3)
   ORDER BY account_id ASC FOR UPDATE;
   ```
   - إذا ظهر قفل ميت، تحقق مما إذا كان هناك مسار برمجي حديث يتجاوز ترتيب الفرز.

2. **ضبط استراتيجية إعادة المحاولة (Exponential Backoff with Jitter):**
   - يتضمن محرك المحاسبة إعادة محاولة تلقائية في حال حدوث خطأ `40P01` أو serialization failure `40001` حتى 3 محاولات مع تأخير عشوائي لمنع التنافس المتكرر (Thundering Herd).

3. **تخفيف التزامن اللحظي على نفس الشركة:**
   - في حال وجود دفق هائل من القيود لنفس الشركة في أجزاء من الثانية، يتم تفعيل مجمع الدفعات الذري (`Bulk Posting / Batching`) بدلاً من القيود المفردة المتزامنة.

---

## 4. التحقق بعد الحل (Post-Incident Verification)

1. انخفاض مقياس `accounting_deadlocks_detected_total` إلى الصفر أو المعدل الطبيعي.
2. استقرار `accounting_posting_duration_seconds` ضمن المستويات المعتادة ($\le 50\text{ms}$).

