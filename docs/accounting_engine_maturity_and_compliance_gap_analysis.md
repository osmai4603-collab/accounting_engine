# تقرير التقييم الشامل ومطابقة المعايير العالمية لمحرك المحاسبة المالية

## Comprehensive Maturity, Gap Analysis & Global Standards Compliance Audit of `accounting_engine`

**تاريخ التقرير:** نوفمبر 2026  
**الإصدار الخاضع للمراجعة:** `accounting_engine v1.0-rc`  
**المرجعيات والمعايير المعتمدة:**  

- **مجلس معايير المحاسبة الدولية (IASB):** معايير التقرير المالي الدولية (IFRS / IAS).
- **مجلس معايير المحاسبة المالية الأمريكي (FASB):** مبادئ المحاسبة المقبولة عموماً (US GAAP / ASC).
- **المنظمة الدولية للمعايير (ISO):** معيار العملات ISO 4217 ومعيار الرسائل المالية ISO 20022.
- **لجنة دعم المنظمات التابعة للجنة تريدواي (COSO):** إطار الرقابة الداخلية المتكامل (Internal Control - Integrated Framework).
- **المعهد الأمريكي للمحاسبين القانونيين (AICPA):** معايير مسار التدقيق وموثوقية الأنظمة (SOC-1 / SSAE 18 / ISAE 3402).
- **المعايير التقنية والمحاسبية لدفاتر الحسابات الرقمية عالية الاعتمادية:** أطر عمل TigerBeetle و Martin Fowler Financial Accounting Patterns و Formance Core.

---

## 1. الملخص التنفيذي (Executive Summary)

أجرينا فحصاً تدقيقياً شاملاً (Deep-Dive Code & Architectural Audit) لكافة مكونات النواة المحاسبية `accounting_engine` المكتوبة بلغة Go، وشمل الفحص طبقات البيانات المرجعية (Master Data)، ومحرك الترحيل (Posting Engine)، ومحرك التقارير المالية (Reporting Engine)، ومحرك الدفاتر المساعدة (Subledgers)، ونظام الحوكمة ومسار التدقيق (Governance & Audit Chain)، ومحرك تقييم العملات (Valuation Engine)، وسائقي التخزين في الذاكرة وقاعدة بيانات PostgreSQL.

### الحكم الفني والمهني (Professional Verdict)

**المحرك غير مكتمل للإنتاج (Not Production-Ready)**؛ إذ يمثل حالياً **"نموذجاً أولياً متقدماً" (Advanced Prototype / POC)** يمتلك هيكلاً معمارياً واعداً ونوايا تصميمية ممتازة للفصل المعماري والترحيل المزدوج، لكنه يحتوي على **فجوات محاسبية بنيوية حاسمة**، و**أخطاء منطقية برمجية**، و**ثغرات في قواعد تكامل البيانات** تمنعه من الامتثال للمعايير المحاسبية العالمية والتشريعات الضريبية والرقابية الصارمة.

### بطاقة تقييم النضج (Maturity Scorecard)

| المحور / المكون | مستوى الاكتمال | مطابقة المعايير | مستوى الخطورة | الملخص السريع |
| :--- | :---: | :---: | :---: | :--- |
| **محرك القيود والترحيل (Posting)** | 70% | جزئي | **حرجة (Critical)** | فجوات في تسلسل الترقيم، قفل محلي لا يحمي العناقيد، وتخطي Maker-Checker في العكس. |
| **العملات والقياس النقدي (Currency & Money)** | 60% | ضعيف | **حرجة (Critical)** | تقييد يدوي لـ 8 عملات فقط، ومنع القيود متعددة العملات، وإسقاط العملات الأخرى في التقارير. |
| **شجرة الحسابات (COA & Master Data)** | 65% | غير مطابق | **حرجة (Critical)** | حظر برمجي صريح للحسابات العكسية (Contra Accounts)، وتداخل زمني في الفترات 13 و 14. |
| **التقارير المالية (Reporting IAS 1/7)** | 55% | ضعيف | **عالية (High)** | اعتماد مطابقة سلاسل نصية هشة، عدم احتساب الدخل الشامل OCI، وافتراض تدفقات استثمارية سالبة دائماً. |
| **الدفاتر المساعدة (Subledgers)** | 50% | جزئي | **عالية (High)** | فقدان قيد إهلاك الأصول عند الانخفاض (IAS 36)، غياب الاستبعاد والتخريد، وغياب الربط الآلي الشامل لـ AR/AP بالـ GL. |
| **الحوكمة والتدقيق (Governance / COSO)** | 65% | متوسط | **عالية (High)** | إسقاط `PayloadBefore` من بصمة التشفير، وغياب القفل التزامني على سلسلة الكتل الرقابية. |
| **تعدد المنشآت (Multi-Tenancy / Entity)** | 0% | غير موجود | **حرجة (Critical)** | غياب تام لمفهوم الشركة/المنشأة/المؤسسة (IFRS 10 / Consolidated Reporting). |

---

## 2. المعايير والمصادر العالمية المعتمدة في التقييم

تم الاستناد في هذا التحليل إلى المراجع الرسمية التالية:

1. **معيار المحاسبة الدولي IAS 1 (عرض القوائم المالية):** قواعد تصنيف الأصول والالتزامات إلى متداولة وغير متداولة، وشمولية قائمة الدخل الشامل وقائمة التغيرات في حقوق الملكية.
2. **معيار المحاسبة الدولي IAS 7 (قائمة التدفقات النقدية):** متطلبات تصنيف الأنشطة التشغيلية والاستثمارية والتمويلية، ومعالجة التدفقات غير النقدية، وعرض الضرائب والفوائد.
3. **معيار المحاسبة الدولي IAS 21 (آثار التغيرات في أسعار صرف العملات الأجنبية):** التمييز بين عملة المعاملة والعملة الوظيفية وعملة العرض، وقواعد إعادة تقييم البنود النقدية مقابل غير النقدية وفروق العملة المحققة وغير المحققة.
4. **معيار المحاسبة الدولي IAS 16 و IAS 36:** قياس واستهلاك الأصول الثابتة، واختبار التدني في القيمة ومعالجة التخريد والاستبعاد.
5. **معيار التقرير المالي الدولي IFRS 9 (الأدوات المالية):** نموذج خسائر الائتمان المتوقعة (ECL)، وتسوية الذمم المدينة والمقاصة.
6. **معيار الأيزو ISO 4217:** الرموز والوحدات الكسرية (Minor Units) لجميع العملات المعترف بها دولياً.
7. **إطار عمل الرقابة الداخلية COSO:** مبدأ الفصل بين المهام (Segregation of Duties / SoD) وصلاحيات الصانع والمراجع (Maker-Checker).
8. **معايير القوانين الضريبية والرقابية للقيد المتسلسل غير المنقطع (Gapless Sequential Numbering):** مثل متطلبات هيئة الزكاة والضريبة والجمارك (ZATCA)، والمعيار الفرنسي (FEC - Article A.47 A-1)، والمعيار الألماني (GoBD).

---

## 3. الفحص والتحليل التفصيلي للعيوب والفجوات (Detailed Gap Analysis)

### 3.1. محرك البيانات المرجعية وشجرة الحسابات (Master Data & COA)

#### الفجوة 1: الحظر المعماري للحسابات العكسية (Contra Accounts Prohibited)

- **الموقع:** [`internal/masterdata/account.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/masterdata/account.go#L141-L143)

- **الكود الحالي:**

  ```go
  if a.NormalBalance != DefaultNormalBalance(a.AccountType) {
      return ErrInvalidAccountType
  }
  ```

- **المخالفة المحاسبية:**
  وفقاً لـ (IFRS / US GAAP)، تعتبر **الحسابات العكسية (Contra Accounts)** ركيزة محاسبية لا غنى عنها في أي شجرة حسابات:
  - **مجمع الاستهلاك (Accumulated Depreciation):** نوع الحساب أصل (`Asset`) لكن طبيعته دائنة (`Credit`).
  - **مخصص خسائر الائتمان المتوقعة / الديون المشكوك فيها (ECL Allowance / Provision):** نوع الحساب أصل (`Asset`) لكن طبيعته دائنة (`Credit`).
  - **أسهم الخزينة (Treasury Shares):** نوع الحساب حقوق ملكية (`Equity`) لكن طبيعته مدينة (`Debit`).
  - **مردودات ومسموحات المبيعات (Sales Returns & Allowances):** نوع الحساب إيراد (`Income`) لكن طبيعته مدينة (`Debit`).
  - **الخصم المسموح به (Discounts Allowed):** نوع الحساب إيراد (`Income`) لكن طبيعته مدينة (`Debit`).

- **الأثر:** الكود البرمجي الحالي يرفض تسجيل هذه الحسابات النظامية ويلقي خطأ `ErrInvalidAccountType`. إجبار المطور على جعل طبيعة مجمع الاستهلاك "مديناً" يخرب معادلة الميزانية العمومية وعرض الأصول بالقيمة الدفترية الصافية!

#### الفجوة 2: تداخل الفترات المالية المحاسبية وعدم الحتمية في استرجاعها

- **الموقع:** [`internal/masterdata/fiscal_calendar.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/masterdata/fiscal_calendar.go#L65-L69) و [`storage/postgres/driver.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/driver.go#L166-L175)

- **الكود الحالي:**

  ```go
  // Period 13 & 14 تبدأ قبل نهاية السنة بساعة وتنتهي بنهاية السنة!
  FiscalPeriod{Number: 13, StartDate: year.EndDate.Add(-time.Hour), EndDate: year.EndDate, ...}
  ```

  وفي استعلام Postgres:

  ```sql
  SELECT ... FROM fiscal_periods WHERE start_date <= $1 AND end_date >= $1 LIMIT 1
  ```

- **المخالفة:** فترات التسوية المحاسبية الختامية (Period 13 / Period 14 / Adjustment Periods) وفق معايير ERP القياسية (مثل SAP / Oracle) هي **فترات منطقية غير زمنية (Logical / Special Periods)** مخصصة لقيود التسوية والتدقيق الختامي، ولا يجوز أن تتداخل زمنياً مع الفترة 12 (ديسمبر) بنطاق فيزيائي بالساعات؛ لأن أي استعلام لتاريخ يقع في تلك الساعة يختار عشوائياً (بدون `ORDER BY`) إما الفترة 12 أو 13 أو 14!

---

### 3.2. محرك العملات والقياس النقدي (Currencies & Monetary Precision)

#### الفجوة 3: حصر العملات يدوياً بـ 8 عملات فقط (ISO 4217 Non-Compliance)

- **الموقع:** [`internal/types/currency.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/types/currency.go#L25-L34)

- **المخالفة:** النظام يحتوي فقط على: `SAR, USD, EUR, AED, GBP, JPY, CNY, KWD`. أي معاملة بأي عملة إقليمية أو دولية أخرى (مثل: `BHD, OMR, QAR, EGP, CAD, AUD, CHF, INR, SGD`) يتم رفضها فوراً بخطأ `ErrInvalidCurrency`.
- **المعيار الدولي:** يجب دعم جدول ISO 4217 الكامل ديناميكياً مع احترام عدد الخانات العشرية (`MinorUnit`): فالين الياباني `JPY` خاناته 0، والدينار الكويتي والبحريني والعماني 3 خانات، والعملات العالمية الأخرى خانتان. فرض التقريب `RoundBank(4)` على كافة العملات دون تمييز يتعارض مع متطلبات تسوية الحسابات البنكية والنقدية.

#### الفجوة 4: حظر القيود متعددة العملات (Multi-Currency Transactions Blocked)

- **الموقع:** [`internal/posting/entry.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/posting/entry.go#L143-L145)

- **الكود الحالي:**

  ```go
  if len(currencies) != 1 {
      return financialtypes.ErrCurrencyMismatch
  }
  ```

- **المخالفة المحاسبية (IAS 21):**
  المعاملات المالية اليومية تتضمن بطبيعتها أطرافاً بعملات مختلفة:
  - سداد فاتورة مورد خارجي باليورو (`EUR`) من حساب بنكي بالدولار (`USD`) مع الاعتراف بأرباح أو خسائر فروق عملة محققة.
  - إثبات قيد تحويل بين حسابين بنكيين للشركة بعملتين مختلفتين.

- **الأثر:** النظام الحالي يشترط أن تكون كل أسطر القيد بنفس العملة حصراً! ولا يسمح بوجود سطرين بعملات مختلفة حتى لو كان القيد متزناً بالقيمة المحولة بالعملة الوظيفية (`Functional Currency`).

#### الفجوة 5: إسقاط بنود العملات الأخرى في ميزان المراجعة والأستاذ العام

- **الموقع:** [`internal/reporting/trial_balance.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/reporting/trial_balance.go#L152-L165)

- **الكود الحالي:**

  ```go
  func lineAmounts(line posting.JournalEntryLine, currency string) (decimal.Decimal, decimal.Decimal) {
      if line.Debit.Currency() == "" || (currency != "" && line.Debit.Currency() != currency) {
          ... // يجعل المدين صفر!
      }
  ```

- **المخالفة:** عندما يطلب المستخدم ميزان المراجعة بعملة معينة، يقوم المحرك بـ **تصفير/إسقاط أي سطر قيد بعملة أخرى** بدلاً من تحويله بسعر الصرف التاريخي أو سعر الإقفال وفق IAS 21! والنتيجة: **ميزان المراجعة يصبح غير متزن حسابياً (`Debit != Credit`) بمجرد وجود عمليات بعملات مختلفة!**

---

### 3.3. محرك القيود والترحيل وعدم القابلية للتعديل (Posting Engine & Immutability)

#### الفجوة 6: ضياع تسلسل الترقيم المحاسبي (Sequence Gaps Vulnerability)

- **الموقع:** [`internal/posting/engine.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/posting/engine.go#L95-L105)

- **الخلل:**
  يقوم المحرك باستدعاء `GetNextSequence` لزيادة عداد تسلسل دفتر اليومية في قاعدة البيانات **قبل** تنفيذ الترحيل الذري `PostAtomic`.

  ```go
  sequence, err := e.repo.GetNextSequence(entry.JournalID) // يرفع الرقم في DB
  ...
  if err := e.repo.PostAtomic(*entry); err != nil { // إذا فشل الترحيل هنا!
      ...
  }
  ```

- **المخالفة التشريعية:** إذا فشل حفظ القيد الذري في قاعدة البيانات (مثل تعطل الاتصال أو تعارض القيود)، يكون الرقم المتسلسل قد حُرق وضاع!
  معايير الفحص الضريبي الدولية والتشريعات المحلية (مثل متطلبات الفاتورة الإلكترونية والقيود المحاسبية لـ ZATCA، والمعايير الأوروبية FEC و GoBD) تفرض **منعاً باتاً لوجود فجوات في الترقيم المتسلسل (Strict Gapless Numbering)**. أي فجوة في أرقام القيود تعتبر لدى المدقق الخارجي دليلاً على حذف قيود محاسبية أو تلاعب دفتري.

#### الفجوة 7: قفل الذاكرة المحلي الوهمي في البيئات الموزعة

- **الموقع:** [`internal/posting/engine.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/posting/engine.go#L93)

- **الكود الحالي:** `e.mu.Lock()`
- **الخلل البرمجي المعماري:**
  هذا القفل هو `sync.Mutex` محلي على مستوى نسخة التطبيق في الذاكرة (In-Process Memory).
  في بيئات الإنتاج الحقيقية (Production Kubernetes / Cloud Run / Multi-Replica Instances)، عندما تعمل نسختان أو أكثر من المحرك خلف موازن أحمال (Load Balancer)، لن يمنع هذا القفل الترحيل المتزامن؛ مما يؤدي إلى تصادم الترقيم المتسلسل (Sequence Race Conditions) وحالات التنافس (Concurrency Hazards).
  **الحل المعياري:** استخدام أقفال قاعدة البيانات الحقيقية (`SELECT ... FOR UPDATE` على دفتر اليومية) أو قفل موزع أو التسلسل التسلسلي عبر مشغل مالي مثل قواعد TigerBeetle.

#### الفجوة 8: خطأ فادح في مخطط SQL يمنع توليد التسلسل للدفاتر الجديدة

- **الموقع:** [`storage/postgres/schema.sql`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/schema.sql#L56-L62) مقارنة بـ [`storage/postgres/driver.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/driver.go#L182-L188)

- **الكود في Schema:**

  ```sql
  CREATE TABLE IF NOT EXISTS journals (
      id uuid PRIMARY KEY,
      code varchar(32) NOT NULL, -- غير قابل لأن يكون NULL
      next_sequence bigint NOT NULL DEFAULT 0
  );
  ```

- **الكود في Driver:**

  ```go
  INSERT INTO journals(id, next_sequence) VALUES($1, 1) 
  ON CONFLICT(id) DO UPDATE SET next_sequence = journals.next_sequence + 1 RETURNING next_sequence
  ```

- **الخلل:** أمر الـ `INSERT` لا يمرر حقل `code`! في قاعدة بيانات PostgreSQL الحقيقية، سيفشل هذا الاستعلام فوراً بخطأ: `null value in column "code" violates not-null constraint`.

#### الفجوة 9: كسر مبدأ Maker-Checker عند إجراء القيود العكسية (Reversal)

- **الموقع:** [`internal/posting/reversal.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/posting/reversal.go#L30-L53)

- **الكود:**

  ```go
  reversal.CreatedBy = posterID
  ...
  if err := e.PostEntry(&reversal, posterID); err != nil {
  ```

- **المخالفة:** المحرك يعين منشئ القيد العكسي `CreatedBy` هو نفسه `posterID`، ثم يستدعي فوراً `PostEntry` بنفس الـ `posterID`.
  إذا تجاوز مبلغ القيد العكسي حد الرقابة `MakerCheckerThreshold`، يفشل العكس فوراً بخطأ `ErrMakerCheckerViolation`! المحرك هنا يناقض سياسته الرقابية: إما أنه يستحيل عكس المبالغ الكبيرة، أو أنه يخرق مبدأ الفصل بين المهام إذا تم تجاوزه.

#### الفجوة 10: حذف أسطر القيد في قاعدة البيانات يكسر قيود التكامل المرجعي

- **الموقع:** [`storage/postgres/driver.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/storage/postgres/driver.go#L254-L256)

- **الكود:**

  ```sql
  DELETE FROM account_move_lines WHERE move_id = $1
  ```

- **المخالفة والمعضلة:**
  عند تحديث حالة القيد الأصلي إلى `EntryReversed`، يستدعي المحرك دالة الحفظ التي تقوم بحذف جميع أسطر القيد وإعادة إدراجها.
  ولكن جدول `account_reconciliation_items` مرتبط مع `account_move_lines` بعلاقة تكامل مرجعي:
  `FOREIGN KEY (move_id, move_line_id) REFERENCES account_move_lines(move_id, line_id) ON DELETE RESTRICT`
  إذا كان هذا القيد قد جرت عليه أي تسوية بنكية أو مقاصة ذمم، سيفشل أمر الـ `DELETE` فوراً وتفشل عملية العكس بالكامل بخطأ Foreign Key Constraint Violation.

---

### 3.4. محرك القوائم والتقارير المالية (Reporting Engine IAS 1 / IAS 7 / IAS 8)

#### الفجوة 11: قائمة التدفقات النقدية (IAS 7) قائمة على مطابقة نصوص إنجليزية هشة

- **الموقع:** [`internal/reporting/cash_flow.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/reporting/cash_flow.go#L69-L87)

- **الكود:**

  ```go
  if strings.Contains(classification, "depreciation") || strings.Contains(classification, "provision") { ... }
  else if strings.Contains(classification, "fixed") || strings.Contains(classification, "ppe") || ... {
      report.InvestingCashFlow = report.InvestingCashFlow.Sub(change.Abs())
  }
  ```

- **المخالفات الجسيمة:**
  1. **الهشاشة اللغوية والتصنيفية:** إذا تم إدخال التصنيف بالعربية (مثل: "استهلاك"، "مخصص")، أو تم استخدام شجرة حسابات رقمية كلاسيكية (مثل 10101)، يتجاهل المحرك التدفق النقدي تماماً وتصدر القائمة بأرقام خاطئة دون إشعار!
  2. **افتراض التدفق الاستثماري سالباً دائماً (`Sub(change.Abs())`):** ماذا لو قامت المنشأة **ببيع أصل ثابت قديم** واستلمت ثمناً نقدياً؟ بموجب معيار IAS 7، التدفق النقدي من بيع الأصول هو **تدفق داخل موجب (Inflow)**. كود المحرك يطرح القيمة المطلقة دائماً معتبراً الاستثمار تدفقاً خارجاً سالباً حصراً!
  3. **غياب الطريقة المباشرة (Direct Method):** يشجع معيار IAS 7.19 بشدة على استخدام الطريقة المباشرة لما تقدمه من وضوح للمقبوضات والمدفوعات النقدية التشغيلية، بينما يوفر المحرك فقط طريقة غير مباشرة غير متكاملة تفتقر إلى إفصاحات الضرائب المدفوعة والفوائد والبنود غير النقدية.

#### الفجوة 12: عيوب تصنيف الأصول المتداولة في الميزانية العمومية (IAS 1)

- **الموقع:** [`internal/reporting/balance_sheet.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/reporting/balance_sheet.go#L84-L88)

- **الكود:**

  ```go
  func isCurrent(classification string) bool {
      return classification == "" || classification == "current" ||
          classification == "current_asset" || ...
  }
  ```

- **المخالفة:**
  إذا كان الحساب مصنفاً كـ `trade_receivables` (ذمم مدينة تجارية) أو `inventories` (مخزون)، فإن دالة `isCurrent` تُرجع `false`!
  النتيجة: **يتم إدراج الذمم المدينة والمخزون ضمن الأصول غير المتداولة (Non-Current Assets)**، وهو تشويه فادح ومخالفة صريحة لمعيار IAS 1 الذي يعتبر رأس المال العامل وأصول دورة التشغيل متداولة بطبيعتها!

#### الفجوة 13: تجاهل الدخل الشامل الآخر (OCI) واستخدام القيم المطلقة للإيرادات (IAS 1)

- **الموقع:** [`internal/reporting/income_statement.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/reporting/income_statement.go#L46-L78)

- **المخالفة:**
  1. كود `IncomeStatement` يحتوي على حقل `OCI` و `TotalComprehensiveIncome`، ولكنه في حلقة معالجة الحسابات **لا يحسب أي قيمة للـ OCI نهائياً**، ويظل الحقل دائماً صفراً حتى لو كانت هناك حسابات مصنفة كـ `SubClassOCIRecyclable` أو `SubClassOCINonRecyclable`.
  2. استخدام `amount.Abs()` في إضافة الإيرادات والمصروفات:
     `report.Revenue = report.Revenue.Add(amount.Abs())`
     إذا كان هناك قيد تسوية أو مردودات مسجلة في الجانب المدين لحساب الإيراد، فإن أخذ القيمة المطلقة سيحول التخفيض إلى زيادة، مما يؤدي إلى تضخيم الإيرادات والمصروفات بدلاً من أخذ صافي الحركة الحقيقية.

#### الفجوة 14: تضارب قيد الافتتاح السنوي مع دفتر الأستاذ المستمر (Double-Counting Bug)

- **الموقع:** [`internal/reporting/closing_engine.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/reporting/closing_engine.go#L41-L91) مقابل [`internal/reporting/general_ledger.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/reporting/general_ledger.go#L58-L85)

- **المعضلة المعمارية:**
  - في الأنظمة المحاسبية الحديثة (Perpetual Ledger)، حسابات الميزانية العمومية (الأصول والالتزامات وحقوق الملكية) لا يتم تصفيرها بنهاية السنة، بل ينتقل رصيدها تراكمياً إلى الأبد. ما يتم إقفاله فقط هو حسابات الإيرادات والمصروفات بتحويل صافيها إلى الأرباح المبقاة (Retained Earnings).
  - لكن `GenerateOpeningMove` في المحرك يقوم بتوليد قيد افتتاحي بجميع أرصدة الأصول والالتزامات للسنة الجديدة!
  - وفي نفس الوقت، محرك الأستاذ العام `GeneralLedgerEngine` يحسب الرصيد الافتتاحي بجمع كل القيود التاريخية من بداية الزمان `time.Time{}` حتى تاريخ البداية.
  - **الأثر الكارثي:** بمجرد ترحيل القيد الافتتاحي الذي ولّده المحرك، **تتضاعف أرصدة الأصول والالتزامات إلى الضعف (200%)** في تقرير الأستاذ العام والقوائم المالية اللاحقة!

---

### 3.5. الدفاتر المساعدة والتسويات البنكية والأصول الثابتة (Subledgers)

#### الفجوة 15: هبوط الأصول الثابتة (IAS 36) لا يولد قيوداً محاسبية

- **الموقع:** [`internal/subledgers/fixed_assets.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/subledgers/fixed_assets.go#L115-L126)

- **المخالفة:**
  دالة `Impair` تقوم بتخفيض القيمة الدفترية للأصل `CurrentBookValue` في سجل الأصل فقط، **دون توليد أو اقتراح أي قيد محاسبي في دفتر الأستاذ العام** (لا يوجد مدين: خسائر هبوط الأصول في قائمة الدخل، ولا دائن: مجمع هبوط قيمة الأصول في الميزانية)! هذا يخلق انفصالاً تاماً بين دفتر الأستاذ الفرعي والأستاذ العام (Ledger Out of Sync).

#### الفجوة 16: غياب سجل الاستهلاك الزمني وخطر التكرار المضاعف

- **الموقع:** [`internal/subledgers/fixed_assets.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/subledgers/fixed_assets.go#L40-L91)

- **المخالفة:** دالة `MonthlyDepreciation` يمكن استدعاؤها في أي وقت دون التحقق مما إذا كان قد تم استهلاك الأصل لنفس الشهر المالي من قبل! لا يوجد سجل استهلاك تاريخي مرتبط بالفترات المالية، وإذا تم استدعاء الدالة مرتين بالخطأ، سيتم استهلاك الأصل مرتين وتخفيض قيمته بصورة مكررة.

#### الفجوة 17: غياب تام لعمليات تخريد واستبعاد الأصول (Asset Disposal / Derecognition IAS 16)

- **المخالفة:** لا توجد أي واجهة أو منطق محاسبي لمعالجة بيع الأصل، أو تخريده، أو التخلص منه، والاعتراف بأرباح أو خسائر استبعاد الأصول الثابتة (Gain/Loss on Disposal) وإقفال تكلفته التاريخية ومجمع استهلاكه.

#### الفجوة 18: تسوية بنكية هشة تفرض تطابق التاريخ الحرفي

- **الموقع:** [`internal/subledgers/treasury.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/subledgers/treasury.go#L29-L68)

- **المخالفة:**
  تشترط دالة `Reconcile` أن يكون تاريخ حركة كشف الحساب البنكي هو ذاته يوم حركة الدفاتر الداخلية (`sameDay`).
  في الواقع المصرفي والمالي العالمي (وفق رسائل ISO 20022 CAMT.053 أو ملفات MT940):
  - المقاصات البنكية والشيكات وعمليات نقاط البيع تستغرق من يوم إلى 4 أيام عمل (Clearing Float / Settlement Lag / Value Date).
  - شرط اليوم المتطابق يؤدي إلى فشل مطابقة 90% من العمليات البنكية الحقيقية.
  - فضلاً عن أن الدالة لا تحفظ حالة المطابقة في قاعدة البيانات بل تعيدها في مصفوفة بالذاكرة فقط!

---

### 3.6. نظام الرقابة الداخلية ومسار التدقيق (Governance & Internal Control - COSO / SOC-1)

#### الفجوة 19: إسقاط الحالة السابقة `PayloadBefore` من بصمة التشفير الرقابية

- **الموقع:** [`internal/governance/audit_chain.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/governance/audit_chain.go#L38-L49)

- **الكود الحالي:**

  ```go
  func CalculateHash(log FinancialAuditLog) string {
      timestamp := log.Timestamp.UTC().Format(time.RFC3339Nano)
      input := log.PreviousLogHash +
          log.EntityType +
          log.EntityID.String() +
          log.Action +
          log.ActorID.String() +
          string(log.PayloadAfter) + // أين PayloadBefore؟!
          timestamp
      sum := sha256.Sum256([]byte(input))
      return hex.EncodeToString(sum[:])
  }
  ```

- **المخالفة الأمنية والرقابية (SOC-1 / ISAE 3402):**
  تعتمد سلسلة التدقيق المشفرة (Tamper-Evident Audit Chain) على ضمان عدم تعديل أي سجل تاريخي. في الكود أعلاه، **تم إسقاط `PayloadBefore` تماماً من حساب البصمة المشفرة `CalculateHash`!**
  هذا يعني أنه إذا قام أي شخص مخول بالدخول إلى قاعدة البيانات وتعديل القيم السابقة `payload_before` في سجل التدقيق المالي، فلن تتغير البصمة ولن تكتشف دالة التحقق `Verify` أي تلاعب، مما يبطل الاعتمادية الجنائية لمسار التدقيق!

#### الفجوة 20: غياب حماية التزامن عند إضافة كتل التدقيق (Audit Chain Forking)

- **الموقع:** [`internal/governance/audit_chain.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/governance/audit_chain.go#L55-L68)

- **المخالفة:** تقوم الدالة بقراءة آخر سجل `GetLastAuditLog`، ثم تحسب الهاش، ثم تدرج السجل الجديد. في حال وجود عمليتين ماليتين متزامنتين، ستقرأ كلاهما نفس الهاش السابق، وسيتم إدراج سجلين يحملان نفس `PreviousLogHash`، مما يؤدي إلى انشطار السلسلة (Forking) وفشلها عند التحقق اللاحق.

---

### 3.7. المعمارية الكلية وتعدد المنشآت (Multi-Tenancy & Group Accounting)

#### الفجوة 21: الغياب الكامل لتعدد المنشآت والشركات (Single-Entity Architecture)

- **الموقع:** كافة نماذج النظام وجداول قاعدة البيانات.

- **المخالفة (IFRS 10):** لا يوجد في أي جدول أو هيكل بيانات حقل يمثل معرف الشركة أو المنشأة (`tenant_id`, `company_id`, `organization_id`).
- **الأثر:**
  1. لا يمكن استخدام هذا المحرك كنواة لمنصة سحابية متعددة المستأجرين (SaaS) دون إعادة هيكلة جذرية.
  2. يستحيل إجراء قوائم مالية مجمعة (Consolidated Financial Statements) للشركات القابضة والتابعة، أو إجراء قيود الاستبعاد بين الشركات الشقيقة (Intercompany Eliminations).

---

## 4. مصفوفة العيوب والفجوات التفصيلية وتصنيف خطورتها

| المعرف | المكون | وصف الفجوة / العيب | التأثير المحاسبي والتقني | المعيار المرجعي | مستوى الخطورة |
| :---: | :--- | :--- | :--- | :--- | :---: |
| **GAP-01** | Master Data | حظر الحسابات العكسية (Contra Accounts) | استحالة تسجيل مجمع الاستهلاك ومخصص الديون وأسهم الخزينة بطبيعتها الصحيحة | IFRS Conceptual Framework | **حرجة جداً (Blocker)** |
| **GAP-02** | Master Data | غياب حقل المنشأة (`CompanyID`) | عدم القدرة على خدمة أكثر من شركة أو عمل قوائم مالية موحدة | IFRS 10 | **حرجة جداً (Blocker)** |
| **GAP-03** | Currency | حظر القيود متعددة العملات | منع سداد الموردين الأجانب أو التحويل بين حسابات العملات | IAS 21 | **حرجة (Critical)** |
| **GAP-04** | Currency | تقييد العملات بـ 8 وتثبيت 4 خانات عشرية | تعطل المحرك مع عملات الخليج (3 خانات) وبقية دول العالم | ISO 4217 | **حرجة (Critical)** |
| **GAP-05** | Reporting | إسقاط العملات الأخرى في ميزان المراجعة | اختلال توازن الميزان العام للأستاذ والتقارير عند تعدد العملات | IAS 21 / Double-Entry | **حرجة (Critical)** |
| **GAP-06** | Posting | حرق الأرقام المتسلسلة عند فشل الترحيل | وجود فجوات في تسلسل القيود يرفضها مدققو الحسابات والهيئات الضريبية | ZATCA / FEC / GoBD | **حرجة (Critical)** |
| **GAP-07** | Storage | خطأ استعلام إضافة الدفاتر في Postgres | فشل إدراج الدفاتر بسبب غياب حقل `code` الإلزامي | Database Integrity | **حرجة (Critical)** |
| **GAP-08** | Reporting | تكرار الأرصدة (Double-Counting) مع قيد الافتتاح | تضخيم أرصدة الأصول والالتزامات للضعف في الأستاذ العام | IAS 1 / IAS 8 | **عالية (High)** |
| **GAP-09** | Reporting | تصنيف الذمم والمخزون كأصول غير متداولة | تشويه نسب السيولة ورأس المال العامل وقائمة المركز المالي | IAS 1 | **عالية (High)** |
| **GAP-10** | Reporting | اعتماد نصوص إنجليزية هشة وافتراض سلبية تدفق الاستثمار | عدم موثوقية قائمة التدفقات النقدية وفشلها التام مع اللغات الأخرى | IAS 7 | **عالية (High)** |
| **GAP-11** | Subledgers | هبوط الأصول الثابتة لا ينشئ قيداً دفترياً | عدم تطابق القيمة في سجل الأصول مع ميزان المراجعة العام | IAS 36 | **عالية (High)** |
| **GAP-12** | Governance | إسقاط `PayloadBefore` من بصمة التدقيق المشفرة | فقدان الحماية من التلاعب في السجلات القديمة | AICPA SOC-1 / COSO | **عالية (High)** |
| **GAP-13** | Posting | تعطل إلغاء وعكس القيود المسواة بنكياً | فشل عملية العكس بسبب قيود الحذف المباشر لأسطر القيد | Database Referencing | **متوسطة (Medium)** |
| **GAP-14** | Treasury | فرض التطابق اليومي في التسويات البنكية | فشل 90% من المطابقات البنكية العملية | ISO 20022 Clearing | **متوسطة (Medium)** |
| **GAP-15** | Reporting | إهمال حساب الدخل الشامل الآخر (OCI) في الأرباح | عدم اكتمال قائمة الدخل الشامل وافتقارها للبنود غير القابلة للتدوير | IAS 1 | **متوسطة (Medium)** |

---

## 5. التقييم المقارن مع المنظومات المحاسبية العالمية المماثلة

لتوضيح موقع `accounting_engine` مقارنة بالمعايير الهندسية للمحركات المحاسبية العالمية، نلخص المقارنة التالية:

| الخاصية / القدرة المعمارية | المحرك الحالي `accounting_engine` | محرك TigerBeetle المالي | معمارية SAP S/4HANA Finance | نمط Martin Fowler Accounting |
| :--- | :--- | :--- | :--- | :--- |
| **قاعدة القيد المزدوج الحتمية** | مطبقة بالكامل بالعملة الواحدة | مطبقة بلغة Zig فائقة السرعة | مطبقة وفق Universal Journal (ACDOCA) | مطبقة مع كائنات القيد والأسطر |
| **دعم العملات المتعددة (Multi-Currency)** | محظور في القيد الواحد | مدعوم مع تتبع أكواد العملات | مدعوم حتى 10 عملات موازية لكل قيد | مدعوم مع حسابات الفروق |
| **الترقيم المتسلسل بدون فجوات** | غير مضمون (معرض للفجوات) | غير تسلسلي تقليدي (Two-Phase Commit) | مضمون بسلاسل ترقيم لكل نوع وثيقة | مدعوم على مستوى دفتر اليومية |
| **سلامة مسار التدقيق** | جزئي (إسقاط `PayloadBefore`) | ثابت بحسابات دفتر الأستاذ غير القابلة للتغيير | مسار تغييرات غير قابل للحذف | سجلات أحداث مالية غير قابلة للحذف |
| **تعدد الكيانات القانونية** | غير متوفر | يعتمد على تصميم الحسابات المستأجرة | مدعوم (Company Code / Client) | مدعوم بكائن المنشأة (Entity) |
| **التقارير المالية المعيارية** | موجودة هيكلياً ولكنها هشة | تترك لطبقة التقارير العليا | مبنية آلياً عبر محرك التحليلات | تقارير مبنية على شجرة الحسابات |

---

## 6. خارطة طريق المعالجة والوصول للاكتمال الإنتاجي (Remediation Roadmap)

لتحويل `accounting_engine` من "نموذج أولي متقدم" إلى **"محرك محاسبي مالي نقي جاهز للإنتاج التجاري والمصرفي" (Enterprise-Grade Pure Accounting Engine)**، يجب تنفيذ خطة العمل المرحلية التالية:

### المرحلة الأولى: تصحيح الأسس البنيوية المحاسبية (Core Invariants)

1. **تصحيح فئات شجرة الحسابات (COA Fix):**
   - تعديل دالة التحقق في [`account.go`](file:///home/osm/StudioProjects/cashflow_system/accounting_engine/internal/masterdata/account.go) لإتاحة الحسابات العكسية بحيث يمكن تعريف حساب أصل بطبيعة دائنة (مجمع استهلاك، مخصص خسائر)، أو حساب حقوق ملكية بطبيعة مدينة (أسهم الخزينة).
2. **إصلاح نموذج القيود متعددة العملات (IAS 21 Multi-Currency Support):**
   - السماح لأسطر القيد بأن تكون بعملات معاملات مختلفة (`Transaction Currency`) مع تسجيل المعادل بالعملة الوظيفية (`Functional/Base Currency`) عند سعر الصرف الساري، وفرض توازن القيد على مستوى العملة الوظيفية $\sum \text{Debit}_{\text{Base}} = \sum \text{Credit}_{\text{Base}}$.
3. **تحديث جدول العملات لـ ISO 4217 الكامل:**
   - استبدال القائمة الثابتة للعملات بمستودع يدعم كافة عملات العالم مع كسورها العشرية الحقيقية (0، 2، 3 خانات).

### المرحلة الثانية: تأمين سلامة المعاملات والترقيم والرقابة (ACID & Audit Trail)

1. **توليد الترقيم المتسلسل داخل معاملة الترحيل الذرية (Atomic Sequence Generation):**
   - نقل زيادة العداد `GetNextSequence` لتكون حصراً **داخل المعاملة الذاتية `PostAtomic` في PostgreSQL**، بحيث إذا فشلت المعاملة يتراجع العداد تلقائياً (Rollback) لمنع أي فجوات نهائياً.
2. **تصحيح بصمة التدقيق المشفرة (Audit Hash Chain Fix):**
   - تضمين `PayloadBefore` وحقول المعرفات الكاملة داخل معادلة حساب `sha256` لضمان عدم إمكانية تعديل أي سجل تاريخي خلسة، مع إضافة قفل تزامني أو معاملة متسلسلة عند إضافة الكتل.
3. **إصلاح عملية العكس المحاسبي (Reversal Refactor):**
   - فصل هوية مدخل القيد العكسي عن مرحّله لعدم انتهاك `MakerCheckerThreshold`.
   - تعديل عملية حفظ القيد المعكوس لتجنب `DELETE FROM account_move_lines` والاعتماد على تحديث الحالة في رأس القيد فقط (`UPDATE account_moves SET status = 'reversed'`).

### المرحلة الثالثة: ترقية محرك التقارير المالية (IAS 1 & IAS 7 Overhaul)

1. **إعادة كتابة قائمة التدفقات النقدية (IAS 7 Engine):**
   - ربط بنود التدفقات النقدية بحقول صريحة في شجرة الحسابات (`CashFlowActivity`: Operating, Investing, Financing) بدلاً من البحث في السلاسل النصية لأسماء الحسابات.
   - دعم بيع وتخريد الأصول كتدفقات استثمارية واردة موجبة.
   - إبراز الضرائب والفوائد بشكل مستقل وفق متطلبات المعيار.
2. **تصحيح تصنيفات المركز المالي (Balance Sheet Current/Non-Current):**
   - اعتماد تصنيف واضح للأصول المتداولة والالتزامات المتداولة يشمل الذمم المدينة والتجارية والمخزون والمصروفات المدفوعة مقدماً.
3. **حسم منطق الإقفال السنوي (Perpetual Closing Model):**
   - إلغاء قيد الافتتاح المضاعف لحسابات الميزانية أو ضبط محرك الأستاذ العام ليقرأ أرصدة بداية الفترة من قيود الإقفال فقط.

### المرحلة الرابعة: سد النواقص الوظيفية في الدفاتر المساعدة (Subledgers Completion)

1. **استكمال محرك الأصول الثابتة (IAS 16 / IAS 36):**
   - توليد قيد محاسبي لتدني القيمة عند استدعاء `Impair`.
   - إضافة جدول زمني لمنع تكرار الاستهلاك الشهري في نفس الفترة.
   - إضافة دالة ودورة حياة تخريد وبيع الأصول الثابتة.
2. **مرونة التسويات البنكية (Treasury Lag Tolerance):**
   - إضافة نافذة زمنية للمطابقة البنكية (Tolerance Window: $\pm 3$ أيام) ودعم المطابقة بنسبة المبالغ والمراجع الجزئية، وتخزين حالة المطابقة في قاعدة البيانات.
3. **إضافة حقل المنشأة لتجهيز المحرك لتعدد الشركات (Multi-Tenancy Readiness):**
   - إضافة `CompanyID uuid.UUID` إلى كافة الكيانات والبيانات لتمكين العمل المؤسسي والقوائم الموحدة.

---

## 7. الخلاصة والتوصية النهائية

مشروع `accounting_engine` يتمتع بأساس برمجي نظيف ومبني بلغة حديثة (Go) ويوظف مكتبة العمليات الحسابية الدقيقة `shopspring/decimal`. لكنه **من الناحية المحاسبية والرقابية ما زال في مرحلة التطوير الأولي (Alpha / Incomplete Core)**؛ ولا يجوز اعتماده في بيئة إنتاجية أو مالية حقيقية قبل معالجة **النقاط الـ 15 المذكورة في مصفوفة العيوب**، وبخاصة تصحيح الحسابات العكسية، والقيود متعددة العملات، وسلامة الترقيم غير المنقطع، وتصحيح معادلات التدفقات النقدية ومسار التدقيق المشفر.

تنفيذ خطة المعالجة الموضحة في القسم (6) سيرفع المحرك إلى مصاف المحركات المالية القياسية عالية الاعتمادية المتوافقة تماماً مع معايير IFRS العالمية ومتطلبات التدقيق والمراجعة القانونية.
