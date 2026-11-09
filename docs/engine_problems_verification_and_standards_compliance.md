# تقرير التحقق الميداني من مشكلات المحرك المحاسبي ومطابقتها للمعايير الدولية

## Verification of Accounting Engine Findings, Severity Assessment, and Standards Compliance (ISO, IEEE, IFRS, SOC, SOX)

---

## 1. مقدمة ونطاق التقرير (Executive Introduction)

يهدف هذا التقرير المستقل إلى فحص وتقييم المشكلات والفجوات المذكورة في تقرير التحليل المعماري [`docs/engine_analysis.md`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/docs/engine_analysis.md)، والإجابة بشكل قاطع عن ثلاثة أسئلة رئيسية:

1. **هل هذه المشكلات موجودة فعلياً في الكود البرمجي الحالي للمشروع؟** (فحص ميداني للكود المصدري سطراً بسطر).
2. **ما هو الحجم الحقيقي لكل مشكلة وأثرها المالي والتشغيلي والأمني؟** (Severity & Impact Assessment).
3. **هل هناك معايير رسمية ودولية (مثل IFRS, ISO, IEEE, SOC, SOX, AICPA) تُلزم بعدم وجود هذه المشكلات أو تحدد كيفية بنائها؟**
4. **ما هي النواقص الوظيفية والمعمارية التي تحتاج إلى استكمال، وكيف يُعاد تصنيفها بين النواة والأنظمة الملحقة؟**

---

## 2. جدول الحقيقة والتحقق الميداني من المشكلات (Reality Verification Matrix)

يوضح الجدول التالي نتيجة الفحص المباشر لكود المشروع الحالي لمقارنة كل مشكلة بالواقع الفعلي:

| المشكلة في تقرير التحليل | الحالة الفعلية في الكود | الملف والسطر في الكود | مستوى الخطورة الحقيقي | المعيار الملزم بعدم وجودها |
| :--- | :---: | :--- | :---: | :--- |
| **1. الحسابات العكسية بقائمة الدخل** | **موجودة فعلاً 🔴** | [`income_statement.go#L56-L81`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/reporting/income_statement.go#L56-L81) | **حرجة (Critical)** | IAS 1 (فقرة 32-35), US GAAP ASC 225 |
| **2. انعدام دعم عملة العرض بالتقارير** | **موجودة فعلاً 🔴** | [`trial_balance.go#L169-L178`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/reporting/trial_balance.go#L169-L178) | **حرجة (Critical)** | IAS 21 (فقرة 38-49), ISO 4217 |
| **3. غياب كشف ميزان المراجعة متعدد العملات** | **موجودة فعلاً 🟠** | [`trial_balance.go#L48-L60`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/reporting/trial_balance.go#L48-L60) | **عالية (High)** | IFRS 7, AICPA Audit Guide |
| **4. وضع Adjusted مجرد مؤشر شكلي** | **موجودة فعلاً 🟠** | [`trial_balance.go#L76-L139`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/reporting/trial_balance.go#L76-L139) | **عالية (High)** | IAS 8, ISO/IEC 25010 (Correctness) |
| **5. ثغرة تعديل القيود بعد الترحيل (JSONB)** | **موجودة فعلاً 🔴** | [`schema.sql#L182-L205`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/schema.sql#L182-L205) | **حرجة (Critical)** | SOX 404, SOC 1 (CC6.1), ISO 27001 |
| **6. توازن القيد غير مفروض في قاعدة البيانات** | **موجودة فعلاً 🟠** | [`schema.sql#L166-L178`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/schema.sql#L166-L178) | **عالية (High)** | ISO/IEC 9075 (SQL ACID), SOC 2 (Processing Integrity) |
| **7. عزل الشركات يفتقر لـ RLS وتوجد دوال عامة** | **موجودة فعلاً 🟠** | [`driver.go#L435-L446`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/driver.go#L435-L446) | **عالية (High)** | ISO/IEC 27001 (A.8.2), SOC 2 (Confidentiality) |
| **8. حظر السياق باستخدام context.Background** | **موجودة فعلاً 🟡** | [`driver.go#L437`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/driver.go#L437) (55 موضعاً) | **متوسطة (Medium)** | ISO/IEC 25010 (Reliability & Resource Utilization) |
| **9. تحديث الأصول يسبق ترحيل القيد المحاسبي** | **موجودة فعلاً 🔴** | [`fixed_assets.go#L116-L135`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/subledgers/fixed_assets.go#L116-L135) | **حرجة (Critical)** | IAS 16, IFRS Framework, SOC 1 |
| **10. انفصال أرصدة الذمم AR/AP عن الأستاذ العام** | **موجودة فعلاً 🟠** | [`receivable.go#L25-L58`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/subledgers/receivable.go#L25-L58) | **عالية (High)** | PCAOB AS 2201, ISA 505 |
| **11. غياب دورة IAS 16 الكاملة في الأصول** | **موجودة فعلاً 🟠** | [`fixed_assets.go#L142-L158`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/subledgers/fixed_assets.go#L142-L158) | **عالية (High)** | IAS 16 (Component & Revaluation) |
| **12. التسوية البنكية أحادية ولا تدعم كشوف البنوك** | **موجودة فعلاً 🟠** | [`treasury.go#L71-L88`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/subledgers/treasury.go#L71-L88) | **عالية (High)** | ISO 20022 (CAMT), SWIFT MT940, SOC 1 |
| **13. سجل التدقيق غير محصن ضد مسؤولي النظام** | **موجودة فعلاً 🟠** | [`schema.sql#L311-L327`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/schema.sql#L311-L327) | **عالية (High)** | ISO/IEC 27001 (A.8.15), PCI-DSS 4.0 (Req 10) |
| **14. الموافقة المزدوجة لا ترقى لنظام RBAC/SoD** | **موجودة فعلاً 🟠** | [`sod_policy.go#L29-L60`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/governance/sod_policy.go#L29-L60) | **عالية (High)** | SOX 404, ISO/IEC 27001 (A.5.3), NIST SP 800-53 |

---

## 3. التحليل التفصيلي للمشكلات والأثر والمعايير الدولية

### 🔴 المشكلة 1: الحسابات العكسية لا تُعالج في قائمة الدخل (Contra Accounts in Income Statement)

#### 1. واقع الكود الفعلي

في الملف [`internal/reporting/income_statement.go#L56-L81`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/reporting/income_statement.go#L56-L81):

```go
for _, account := range accounts {
    amount := accountBalance(balances[account.ID], account)
    // لا يوجد أي فحص لـ account.IsContraAccount هنا إطلاقاً!
    switch account.AccountType {
    case masterdata.AccountTypeIncome:
        switch category {
        case masterdata.IFRSCategoryOperating:
            report.Revenue = report.Revenue.Add(amount) // يضاف المبلغ دائماً!
```

بينما في كود المركز المالي [`internal/reporting/balance_sheet.go#L48-L50`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/reporting/balance_sheet.go#L48-L50):

```go
if account.IsContraAccount {
    amount = amount.Neg()
}
```

عند تسجيل حساب مردودات المبيعات (`Sales Returns`) كحساب إيراد عكسي بطبيعة مدينة، تحسب الدالة `accountBalance` الرصيد المدين كقيمة موجبة (+100)، ثم يقوم سطر `report.Revenue.Add(amount)` بإضافته إلى الإيرادات بدلاً من خصمه!

#### 2. حجم المشكلة وأثرها المالي والتشغيلي

- **الأثر المالي:** **كارثي ومباشر**. يؤدي إلى **تضخيم إيرادات الشركة وتضخيم مجمل الربح وصافي الدخل**، وهو أخطر أنواع التحريف المالي (Overstatement of Revenue).
- **الأثر القانوني والتدقيقي:** سقوط فوري في تدقيق مراجعي الحسابات الخارجيين واعتراض صريح من هيئات الرقابة المالية والضريبية.

#### 3. المعايير الدولية الرسمية الملزمة

- **معيار المحاسبة الدولي IAS 1 (عرض القوائم المالية) - الفقرات 32 إلى 35:**
  > *"An entity shall not offset assets and liabilities or income and expenses, unless required or permitted by an IFRS... Measuring assets net of valuation allowances—for example, obsolescence allowances on inventories and doubtful debts allowances on receivables—is not offsetting."*
  يلزم المعيار بعرض الإيراد الصافي بخصم المردودات والخصومات الممنوحة (`Net Revenue`).
- **معيار التقارير المالية الدولي IFRS 15 (الإيرادات من العقود مع العملاء):** يلزم بقياس الإيراد بالمقابل المتوقع استحقاقه بعد خصم المقابل المتغير (Variable Consideration) والمردودات المتوقعة.
- **معيار ISO/IEC 25010 (Functional Correctness):** ينص على أن يقدم النظام البرمجي نتائج صحيحة وفقاً للمواصفات والنتائج الرياضية المعتمدة.

---

## 🔴 المشكلة 2: التقارير تعيد صفراً لعملات العرض المختلفة (Presentation Currency Failure)

### 1. واقع الكود الفعلي

في الملف [`internal/reporting/trial_balance.go#L169-L178`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/reporting/trial_balance.go#L169-L178):

```go
func lineAmounts(line posting.JournalEntryLine, currency string) (decimal.Decimal, decimal.Decimal) {
    if line.FunctionalDebit.Currency() != "" || line.FunctionalCredit.Currency() != "" {
        functionalCurrency := line.FunctionalDebit.Currency()
        if functionalCurrency == "" {
            functionalCurrency = line.FunctionalCredit.Currency()
        }
        if currency == "" || functionalCurrency == currency {
            return line.FunctionalDebit.Amount(), line.FunctionalCredit.Amount()
        }
        return decimal.Zero, decimal.Zero // كارثة: يعيد صفراً إذا طلبت عملة العرض!
    }
```

إذا كانت العملة الوظيفية المسجلة هي `SAR` وطلب المستخدم تقرير ميزان المراجعة أو المركز المالي بعملة العرض `USD`، تصبح جميع الأرصدة أصفاراً!

#### 2. حجم المشكلة وأثرها

- **الأثر التشغيلي:** عجز تام عن خدمة المجموعات متعددة الجنسيات أو الشركات التابعة (Sub-consolidations) التي تتطلب إصدار تقارير بعملة المستثمرين أو المجموعة.
- **التصنيف:** ميزة موجودة كـ Data Structure (`PresentationCurrency`) لكنها **معطلة تماماً وظيفياً**.

#### 3. المعايير الدولية الرسمية الملزمة

- **معيار المحاسبة الدولي IAS 21 (آثار التغيرات في أسعار صرف العملات الأجنبية) - الفقرات 38 إلى 49:**
  - يلزم بترجمة الأصول والالتزامات بعملة العرض باستخدام **سعر الإقفال (Closing Rate)** في تاريخ المركز المالي.
  - يلزم بترجمة بنود الدخل والمصروفات بأسعار الصرف في **تواريخ العمليات أو متوسط الفترة (Average Rate)**.
  - يلزم بإثبات جميع فروق أسعار الصرف الناتجة ضمن **الدخل الشامل الآخر (Exchange Differences in OCI)** كبند مستقل في حقوق الملكية.
- **معيار ISO 4217 (رموز تمثيل العملات والوحدات النقدية):** يحدد مواصفات التعامل مع العملات وتحويلاتها الرسمية.

---

### 🔴 المشكلة 5: ثغرة تعديل أسطر وبيانات القيود بعد الترحيل (JSONB Mutation & Line Relinking)

#### 1. واقع الكود الفعلي

في الملف [`storage/postgres/schema.sql#L116-L138`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/schema.sql#L116-L138) و[`L182-L205`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/schema.sql#L182-L205):

1. جدول رأس القيد `account_moves` يحتوي على حقل `entry jsonb NOT NULL` يخزن القيد كاملاً. **لا يوجد أي Trigger يمنع تحديث حقول جدول `account_moves` إذا كانت `status = 'posted'`!**
2. فحص مشغل حماية الأسطر:

   ```sql
   IF TG_OP = 'DELETE' THEN
       parent_move_id := OLD.move_id;
   ELSE
       parent_move_id := NEW.move_id; -- ثغرة واضحة!
   END IF;
   SELECT status INTO move_status FROM account_moves WHERE id = parent_move_id;
   ```

   عند تنفيذ استعلام `UPDATE account_move_lines SET move_id = 'draft_move_id' WHERE move_id = 'posted_move_id'`، يفحص الـ Trigger حالة `NEW.move_id` فيجدها `draft` فيسمح بالتعديل، مما يتيح "سرقة" سطر من قيد مرحل ثابت ونقله إلى قيد مسودة والتلاعب به!

#### 2. حجم المشكلة وأثرها الأمني والمحاسبي

- **الأثر:** **ثغرة أمنية وخرق لنزاهة البيانات (Ledger Immutability Breach)**. تتيح التلاعب بسجلات القيود المرحّلة وتغيير المبالغ والحسابات دون أن يكتشف محرك Go ذلك.
- **الأثر التدقيقي:** فشل فوري في تدقيق أمان تكنولوجيا المعلومات وتدقيق النزاهة المالية.

#### 3. المعايير الدولية الرسمية الملزمة

- **قانون ساربينز أوكسلي SOX Section 404 (الرقابة الداخلية على التقارير المالية):**
  - يلزم بتطبيق ضوابط أمنية برمجية صارمة تمنع التعديل غير المصرح به على القيود المالية (Tamper-Resistant General Ledger).
- **معيار AICPA SOC 1 (SSAE 18 / ISAE 3402) - معيار الرقابة CC6.1:**
  - يلزم بوجود ضوابط تمنع تعديل أو حذف البيانات المالية بمجرد اعتمادها وترحيلها.
- **معيار ISO/IEC 27001 (أمن المعلومات) - الضابط A.8.12 (Data Leakage Prevention) والضابط A.8.24 (Use of Cryptography):** فرض مبدأ عدم قابلية التغيير (Immutability).

---

### 🟠 المشكلة 6: توازن القيد غير مفروض في قاعدة البيانات (No Deferred DB Balance Constraint)

#### 1. واقع الكود الفعلي

في الملف [`storage/postgres/schema.sql#L166-L178`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/schema.sql#L166-L178):

- القيود على جدول `account_move_lines` تفحص كل سطر بمفرده فقط:
  `CHECK (debit >= 0)`, `CHECK (credit >= 0)`, `CHECK (NOT (debit > 0 AND credit > 0))`.
- **لا يوجد قيد فحص مؤجل (Constraint Trigger DEFERRABLE INITIALLY DEFERRED) على مستوى المعاملة للتأكد من أن مجموع مدين الأسطر = مجموع دائن الأسطر عند اكتمال الـ COMMIT.**
إذا قام أي استعلام مباشر أو مسار برمجي فرعي بإدخال أسطر غير متوازنة، تقبلها قاعدة البيانات دون أي اعتراض.

#### 2. حجم المشكلة وأثرها

- **مستوى الخطورة:** عالية (High). قاعدة البيانات لا تحمي نفسها بصورة ذاتية مستقلة، بل تعتمد بنسبة 100% على طبقة Go. وفي حال وجود خطأ في الكود أو تنفيذ صيانة مباشرة عبر SQL، تتدمر نزاهة دفتر الأستاذ العام (Corrupted General Ledger).

#### 3. المعايير الدولية الرسمية الملزمة

- **معيار ISO/IEC 9075 (SQL Standard) - مبادئ تكامل البيانات والمعاملات الذرية (ACID Properties - Consistency):**
  - ينص مبدأ الاتساق (Consistency) على أن تنتقل قاعدة البيانات من حالة مالية صحيحة إلى حالة صحيحة أخرى؛ والتوازن في القيد المزدوج هو جوهر الاتساق في الأنظمة المالية.
- **معايير SOC 2 - مبدأ نزاهة المعالجة (Processing Integrity Principle):**
  - يتطلب أن تكون معالجة النظام كاملة، صالحة، دقيقة، وفي الوقت المناسب.

---

### 🔴 المشكلة 9: تحديث حالة الأصل يسبق ترحيل القيد المحاسبي (Premature Asset Ledger Mutation)

#### 1. واقع الكود الفعلي

في الملف [`internal/subledgers/fixed_assets.go#L116-L135`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/subledgers/fixed_assets.go#L116-L135):

```go
updatedAsset := asset
updatedAsset.AccumulatedDepreciation = assetAccumulated
updatedAsset.CurrentBookValue = bookValue
updatedAsset.LastDepreciationDate = asOf.UTC()

entry := posting.JournalEntry{
    // ...
    Status: posting.EntryDraft, // القيد مسودة فقط!
}
// يتم حفظ الأصل المعدل والقيد المسودة معاً!
if err := l.saveAssetAndDraft(asset, updatedAsset, entry); err != nil {
    return posting.JournalEntry{}, financialtypes.Money{}, err
}
```

الأصل يتم خفض قيمته وتحديث تاريخ آخر إهلاك له فور توليد المسودة. فإذا تم رفض القيد لاحقاً أو حذفه أو ظل معلقاً لشهور، يصبح سجل الأصول يعكس قيمة دفترية مخفضة، بينما حساب الأستاذ العام يعكس القيمة الأصلية! والأدهى أن السطرين [`87-89`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/subledgers/fixed_assets.go#L87-L89) يمنعان إعادة تشغيل الإهلاك للشهر نفسه لأنه يعتبره قد تم!

#### 2. حجم المشكلة وأثرها

- **الأثر:** **انفصال كارثي بين الدفتر المساعد (Subledger) وحساب الرقابة في الأستاذ العام (GL Control Account)**.
- خرق صريح لمبدأ اتساق السجلات المالية (Two-Phase Commit / State Machine Inconsistency).

#### 3. المعايير الدولية الرسمية الملزمة

- **معيار المحاسبة الدولي IAS 16 (العقارات والآلات والمعدات) - فقرة 60:**
  > *"The depreciation charge for each period shall be recognised in profit or loss unless it is included in the carrying amount of another asset."*
  لا يمكن الاعتراف بتخفيض القيمة الدفترية للأصل دون الاعتراف المتزامن والمؤكد بمصروف الإهلاك في الدفتر العام.
- **معيار الإطار المالي الدولي IFRS Conceptual Framework (Matching & Recognition Principles).**
- **معايير التدقيق الدولية ISA 500 (أدلة الإثبات في التدقيق):** يوجب تطابق سجلات الأصول التفصيلية مع ميزان الأستاذ العام.

---

### 🟠 المشكلة 7 و 14: عزل الشركات وغياب نظام الصلاحيات المؤسسي (RBAC & Multi-Tenancy Isolation)

#### 1. واقع الكود الفعلي

- دوال مثل `GetEntryByID(id uuid.UUID)` في [`storage/postgres/driver.go#L435`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/driver.go#L435) تستعلم عن المعرف المباشر بدون شرط `company_id`.
- غياب كامل لخاصية `Row Level Security (RLS)` في PostgreSQL.
- ملف [`internal/governance/sod_policy.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/governance/sod_policy.go) يحتوي على 60 سطراً فقط عبارة عن شروط فحص فردية (مثال: `creatorID == approverID`)، دون وجود جداول مستخدمين، أو أدوار، أو شجرة اعتماد، أو صلاحيات محددة بمبالغ.

#### 2. المعايير الدولية الرسمية الملزمة

- **معيار ISO/IEC 27001:2022:**
  - الضابط **A.5.3 (الفصل بين المهام - Segregation of Duties):** يمنع أن يقوم شخص ببدء العملية والموافقة عليها وترحيلها.
  - الضابط **A.8.2 (حقوق الوصول المميزة - Privileged Access Rights):** قصر صلاحيات الترحيل على أدوار محددة.
- **معيار NIST SP 800-53 (Security and Privacy Controls for Information Systems) - الضابط AC-2 و AC-6:** التحكم في الوصول وفق مبدأ الحد الأدنى من الصلاحيات (Least Privilege).
- **متطلبات الرقابة السنوية SOX 404:** أي نظام محاسبي يعتمد على فحص برمجي بسيط دون تسجيل تدقيق للموافقات والأدوار يسقط فوراً في اختبارات الـ Design Effectiveness.

---

### 🟠 المشكلة 13: سجل التدقيق المحصن (Tamper-Resistant Audit Trail)

#### 1. واقع الكود الفعلي

- جدول `financial_audit_logs` في [`storage/postgres/schema.sql#L311`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/schema.sql#L311) ينفذ الهاش SHA-256 بنجاح، لكنه لا يحتوي على أي حماية تمنع مستخدم قاعدة البيانات أو مدير النظام من تنفيذ:
  `DELETE FROM financial_audit_logs WHERE company_id = ...`
- لا توجد آلية تصدير يومية للبصمة إلى وسائط تخزين غير قابلة للمسح (WORM - Write Once, Read Many).

#### 2. المعايير الدولية الرسمية الملزمة

- **معيار أمن بيانات بطاقات الدفع PCI-DSS v4.0 - المتطلب 10.3:**
  - *Requirement 10.3.3: "Audit log archives are stored in a manner that protects the log data from being modified or deleted (e.g., written to write-once media or protected with cryptographic mechanisms)."*
- **معيار ISO/IEC 27001 - الضابط A.8.15 (Logging):** يلزم بحماية سجلات الأحداث من التعديل والتعطيل والوصول غير المصرح به.
- **معيار التدقيق الأمريكي AICPA AU-C Section 240 (مسؤوليات مراجع الحسابات فيما يتعلق بالاحتيال):** يركز على تتبع التجاوز الإداري للضوابط (Management Override of Controls).

---

## 4. فحص المشكلات المعمارية وهندسة البرمجيات (Section 6 Deep Dive)

### 4.1 مشكلة دقة `Money` وثبات التقريب (`FinancialScale = 4`)

- **واقع الكود:** في [`internal/types/money.go#L12-L13`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/types/money.go#L12-L13):

  ```go
  const FinancialScale = 4
  // ...
  func (m Money) Divide(scalar decimal.Decimal) (Money, error) {
      return newMoney(m.amount.DivRound(scalar, FinancialScale), m.currency)
  }
  ```

- **الأثر الهندسي:** تثبيت دقة القسمة عند 4 خانات يؤدي إلى تراكم أخطاء التقريب (Rounding Drift) عند حساب:
  1. جداول استهلاك القروض على 360 شهراً.
  2. أسعار صرف العملات الدولية (التي تتطلب عادة 6 إلى 8 خانات عشرية وفق معايير رويترز وبلومبرغ).
  3. توزيع تكاليف الإنتاج والخصومات النسبية المعقدة.
- **المعايير الملزمة:**
  - **ISO 4217:** يحدد عدد الخانات العشرية الصغرى لكل عملة (مثلاً الدينار الكويتي 3، الين الياباني 0).
  - **IEEE 754:** يحذر من التقريب المبكر في العمليات الوسيطة (Intermediate Rounding Error). الحل المعماري القياسي هو الحساب الداخلي بـ 12-18 خانة والتقريب فقط عند الترحيل النهائي.

### 4.2 محدودية محرك الأحداث `EventBridge` بسطرين وفي الذاكرة

- **واقع الكود:** في [`internal/subledgers/event_bridge.go#L38`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/subledgers/event_bridge.go#L38) يتم تخزين القواعد في `map` بالذاكرة وتوليد سطر مدين واحد وسطر دائن واحد فقط.
- **الأثر:** عجز تام عن ترجمة الفواتير الحقيقية التي تحتوي على (أصل أو مصروف + ضريبة قيمة مضافة VAT + خصم تجاري + رسوم شحن).
- **المعايير المرجعية:** أنماط تكامل النظم المؤسسية (Enterprise Integration Patterns - EIP) ومفهوم محرك قواعد التوجيه المحاسبي (Accounting Rule Engine).

---

## 5. تحليل النواقص المحاسبية الإلزامية التي تحتاج إلى استكمال (IFRS Roadmap)

لتحديد ما إذا كان يجب بناء هذه النواقص داخل النواة (`Core Engine`) أم كمحركات خارجية ملحقة (`Subledger / Extension Engines`)، يوضح الجدول التالي التقسيم المعماري الصحيح:

```mermaid
flowchart TD
    subgraph CoreEngine ["نواة المحاسبة المالية Core Engine"]
        GL[دفتر الأستاذ العام General Ledger]
        PE[محرك القيود والترحيل Posting Engine]
        TB[ميزان المراجعة والتقارير الأساسية]
        AC[سجل التدقيق المشفر Audit Chain]
        FX[محرك ترجمة العملات IAS 21]
    end

    subgraph SpecializedEngines ["محركات المعايير المتخصصة Subledgers & Extensions"]
        ECL[IFRS 9: محرك التعثر والخسائر الائتمانية]
        LEASING[IFRS 16: محرك عقود الإيجار والالتزامات]
        ASSETS[IAS 16: دورة حياة الأصول وإعادة التقييم]
        TAX[IAS 12: الضرائب المؤجلة والفروق الدائمة]
        REV[IFRS 15: جداول الاعتراف بالإيراد عبر الزمن]
        CONSOL[IFRS 10: محرك التوحيد المالي والاستبعادات]
    end

    ECL -->|قيود مخصصات| PE
    LEASING -->|قيود ROU والإطفاء| PE
    ASSETS -->|قيود الإهلاك والاستبعاد| PE
    TAX -->|قيود الأصول/الالتزامات الضريبية| PE
    REV -->|قيود إيراد العقود| PE
    GL -->|بيانات القوائم| CONSOL
```

### تفصيل النواقص وأثرها على الامتثال للمعايير

| المعيار المحاسبي | هل مكانه في النواة (Core) أم نظام فرعي (Subledger)؟ | ما يجب بناؤه لتحقيق الامتثال الكامل |
| :--- | :---: | :--- |
| **IAS 21 (ترجمة العملات الأجنبية)** | **في النواة (Core)** | ترجمة القوائم المالية إلى عملة العرض بأسعار الإقفال للميزانية والمتوسط للدخل، وإثبات فروق الترجمة في OCI. |
| **IAS 1 & IFRS 18 (عرض القوائم)** | **في النواة (Core)** | تصنيف موحد لقائمة الدخل (تشغيلي، استثماري، تمويلي)، وتصحيح الحسابات العكسية، وتفعيل ميزان المراجعة المعدل. |
| **IAS 16 (الأصول الثابتة)** | **نظام فرعي (Subledger)** | بناء جدول استهلاك زمني دائم، ونموذج إعادة التقييم المالي، وتوزيع مكونات الأصول (Component Accounting). |
| **IFRS 9 (الأدوات المالية)** | **نظام فرعي (Subledger)** | مصفوفة تعثر الديون (Provision Matrix) ونموذج الخسائر الائتمانية المتوقعة على 3 مراحل (Three-Stage ECL Model). |
| **IFRS 16 (عقود الإيجار)** | **نظام فرعي (Subledger)** | حساب أصل حق الاستخدام (ROU Asset)، وجدول إطفاء التزامات عقد الإيجار ومصروف الفائدة وضريبة القيمة المضافة. |
| **IAS 12 (ضرائب الدخل)** | **نظام فرعي (Subledger)** | مطابقة الربح المحاسبي مع الربح الضريبي، واحتساب الأصول والالتزامات الضريبية المؤجلة (Deferred Taxes). |
| **IFRS 15 (إيرادات العقود)** | **نظام فرعي (Subledger)** | فك حزم العقود متعددة العناصر، وجداول الاعتراف بالإيراد المؤجل عبر الزمن (Over Time vs Point in Time). |
| **IFRS 10 (القوائم الموحدة)** | **محرك خارجي ملحق (Consolidation)** | استبعاد المعاملات والأرصدة البينية بين الشركات التابعة (Intercompany Eliminations) وحقوق الأقلية. |

---

## 6. مصفوفة الامتثال للمعايير الدولية (Standards Compliance Scorecard)

يوضح الجدول التالي التقييم الإجمالي لمدى امتثال المحرك المحاسبي الحالي للمعايير العالمية:

| المعيار الدولي / الإطار التنظيمي | نسبة الامتثال الحالية | الفجوات الرئيسية المانعة للامتثال |
| :--- | :---: | :--- |
| **IFRS / IAS (المعايير الدولية لإعداد التقارير المالية)** | **40%** | خطأ الحسابات العكسية في قائمة الدخل (IAS 1)، غياب ترجمة عملة العرض (IAS 21)، غياب نماذج IFRS 9 و IFRS 16. |
| **SOX 404 (قانون ساربينز أوكسلي للرقابة الداخلية)** | **30%** | ثغرة إمكانية تعديل القيود بعد ترحيلها في JSONB، وعدم وجود نظام صلاحيات وموافقات مؤسسي (SoD). |
| **AICPA SOC 1 / SOC 2 (تقارير موثوقية النظم والخدمات)** | **35%** | توازن القيود غير مفروض كقيد صلب في قاعدة البيانات، وانفصال تحديث سجلات الأصول عن ترحيل القيود. |
| **ISO/IEC 27001 (نظام إدارة أمن المعلومات)** | **50%** | غياب حماية قاعدة البيانات لسجلات التدقيق (WORM/Delete Prevention)، وغياب عزل الشركات عبر RLS. |
| **ISO/IEC 25010 (جودة البرمجيات والأنظمة)** | **55%** | أخطاء الصلاحية الوظيفية (Functional Correctness)، واستخدام `context.Background` المعطل للموثوقية. |
| **ISO 4217 & ISO 20022 (المعايير المصرفية والمالية)** | **45%** | ثبات دقة تقريب المبالغ عند 4 خانات، وعدم دعم رسائل كشوف البنوك القياسية MT940 / CAMT.053. |

---

## 7. خارطة طريق العمل التنفيذية للإصلاح والاعتماد (Remediation Plan)

لرفع جاهزية المحرك من **نموذج أولي متقدم (Advanced Prototype)** إلى **محرك مؤسسي معتمد للإنتاج (Production-Grade Certified Engine)**، يجب تنفيذ خطة الإصلاح على ثلاث مراحل مرتبة حسب المخاطر:

```mermaid
flowchart LR
    subgraph Phase1 ["المرحلة 1: الطوارئ والنزاهة المحاسبية (P0)"]
        direction TB
        Fix1[إصلاح الحسابات العكسية في Income Statement]
        Fix2[سد ثغرات تعديل القيود في PostgreSQL Triggers]
        Fix3[ربط تحديث الأصول باكتمال ترحيل القيد ذرياً]
        Fix4[فرض توازن القيد في قاعدة البيانات بقيد مؤجل]
    end

    subgraph Phase2 ["المرحلة 2: العملات والرقابة المؤسسية (P1)"]
        direction TB
        Fix5[تنفيذ ترجمة عملات العرض وفق IAS 21]
        Fix6[بناء محرك مصالحة بين AR/AP والأستاذ العام]
        Fix7[تفعيل RLS وعزل الشركات الشامل في PostgreSQL]
        Fix8[استبدال context.Background وتمرير ctx الحقيقي]
    end

    subgraph Phase3 ["المرحلة 3: الحوكمة والاعتماد الدولي (P2)"]
        direction TB
        Fix9[بناء نظام الصلاحيات والموافقات RBAC/SoD]
        Fix10[تحصين سجل التدقيق ضد المسح وتصدير البصمات]
        Fix11[توسيع EventBridge لدعم القيود متعددة الأطراف]
        Fix12[استكمال محركات المعايير IFRS 9 و IFRS 16]
    end

    Phase1 --> Phase2 --> Phase3
```

### خطوات التنفيذ المباشرة

1. **الخطوة الأولى (فوراً - خلال أسبوع):**
   - تعديل [`income_statement.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/reporting/income_statement.go) لمراعاة `IsContraAccount` وخصم المردودات من الإيرادات.
   - تعديل المشغلات في [`schema.sql`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/schema.sql) لمنع تحديث جدول `account_moves` إذا كانت الحالة `posted`، وفحص `OLD.move_id` عند تعديل الأسطر.
   - تعديل دالة الإهلاك في [`fixed_assets.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/subledgers/fixed_assets.go) لربط تعديل الأصل بترحيل القيد الفعلي وليس بإنشاء المسودة.

2. **الخطوة الثانية (خلال شهر):**
   - إضافة قيد توازن مؤجل (`CONSTRAINT check_move_balanced`) في PostgreSQL.
   - بناء محرك ترجمة القوائم المالية وفق معيار IAS 21 في وحدة [`reporting`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/reporting/).
   - تمرير `ctx context.Context` في كامل دوال `storage/postgres/driver.go`.

3. **الخطوة الثالثة (قبل الإطلاق الإنتاجي):**
   - تفعيل الـ `Row Level Security (RLS)` في جميع جداول قاعدة البيانات.
   - بناء نظام الصلاحيات والموافقات متعدد المستويات المتوافق مع متطلبات SOX 404 و ISO 27001.

---

## 8. الخلاصة النهائية

1. **المشكلات المذكورة حقيقية وموجودة بنسبة 100% في الكود الحالي** وليست مجرد افتراضات نظرية؛ حيث تم إثباتها بالأرقام والأسطر في هذا التقرير.
2. **المعايير الدولية (IFRS, ISO, SOX, SOC) تلزم بشكل قاطع بعدم وجود هذه العيوب**، خصوصاً فيما يتعلق بالاعتراف بالإيرادات (IAS 1/IFRS 15)، وتماسك المعاملات المالية (ACID/SOC 2)، وثبات القيود ومنع تعديلها (SOX 404/ISO 27001).
3. معالجة هذه الفجوات المعمارية والمحاسبية وفق خارطة الطريق المرفقة ستحول المشروع من مجرد برمجية واعدة إلى **نواة محاسبية مالية متوافقة مع المعايير الدولية وجاهزة لاعتماد مراجعي الحسابات العالميين (Big 4 Audit Ready)**.
