# Runbook: حوادث أمان مستوى الصفوف وعزل الشركات (RLS & Tenant Isolation Incident)

- **رمز الحادث:** `INC-RUNBOOK-04`
- **الخطورة:** P0 (أمنية حرجة للغاية)
- **المهمة:** `P6-06`
- **المكونات المتأثرة:** `storage/postgres/`, `transactions.go`, `app.current_company_id`, RLS Policies

---

## 1. الأعراض والمؤشرات (Symptoms & Alerts)

- تنبيه: `AccountingRLSViolationDetected` أو محاولة قراءة/كتابة عبر الشركات (Cross-Company Access).
- تسجيل خطأ في الاستعلامات: `new row violates row-level security policy for table ...`
- استدعاء استعلام دون تمرير `CompanyID` في سياق التنفيذ (`beginCompanyTx` يرفض `uuid.Nil`).
- أي بلاغ عن ظهور قيود أو حسابات تابعة لشركة في كشف حساب شركة أخرى.

---

## 2. الإجراءات التشخيصية الفورية (Immediate Diagnostics)

1. **التحقق من تفعيل RLS وسياسات الإلزام (Force RLS) على كافة الجداول المالية (42 جدولاً):**
   ```sql
   SELECT relname, relrowsecurity, relforcerowsecurity 
   FROM pg_class 
   WHERE relnamespace = 'public'::regnamespace 
     AND relkind = 'r' 
     AND relname IN ('account_moves', 'account_move_lines', 'accounts', 'journals')
     AND (relrowsecurity = false OR relforcerowsecurity = false);
   ```
   *(يجب ألا يرجع الاستعلام أي صف؛ جميع الجداول يجب أن تكون TRUE لكلا العمودين).*

2. **فحص سياق المعاملة المحلية (`Transaction-Local Setting`):**
   - التحقق من أن ضبط `app.current_company_id` يتم دائماً بالمعلمة الثالثة `is_local = true`:
   ```sql
   SELECT set_config('app.current_company_id', $1, true);
   ```
   - إذا تم ضبطه بـ `false` فقد يتسرب المعرف عبر مجمع الاتصالات (Connection Pool) للطلب اللاحق.

---

## 3. خطوات الحل والمعالجة (Remediation Steps)

1. **إعادة تفعيل RLS و FORCE RLS فوراً في حال تعطيلها:**
   ```sql
   ALTER TABLE account_moves ENABLE ROW LEVEL SECURITY;
   ALTER TABLE account_moves FORCE ROW LEVEL SECURITY;
   ALTER TABLE account_move_lines ENABLE ROW LEVEL SECURITY;
   ALTER TABLE account_move_lines FORCE ROW LEVEL SECURITY;
   ```

2. **التأكد من دور قاعدة البيانات المستخدم من التطبيق (`accounting_app_user`):**
   - يمنع منعاً باتاً تشغيل الخدمة باستخدام مستخدم `superuser` أو مالك الجداول (`table owner`) لأن PostgreSQL يتجاوز RLS للمالكين ما لم يفعل `FORCE ROW LEVEL SECURITY`.
   - يجب تشغيل التطبيق بحساب `accounting_app_user` المقيد.

3. **تشغيل اختبارات الاختراق الشاملة للعزل:**
   ```bash
   go test -v ./internal/adapters/postgres/ -run 'Test(EveryFinancialTableHasForcedRLS|Phase6RLSPenetration.*)'
   ```

---

## 4. التحقق بعد الحل (Post-Incident Verification)

- نجاح اختبارات الاختراق `TestEveryFinancialTableHasForcedRLS` و `TestPhase6RLSPenetrationCrossCompanyReads`.
- التأكد من سلامة سجل التدقيق وعدم حدوث أي تسريب بيانات بين المستأجرين.

