# المرحلة `P11` — إكمال نطاق وحدات المعايير المحاسبية (IFRS/IAS)

- **الهدف:** توسيع نطاق كل وحدة معيارية بالمتطلبات الناقصة المحددة في التحليل، وربط جميع الوحدات بمسار الترحيل عبر Application Layer
- **عدد المهام:** 5
- **الاعتماديات:** `P8-01` (ترحيل SPI)، `P9-01` (إقفال)، `P9-05` (إيضاحات)

> [!NOTE]
> كل وحدة يجب أن تُعلن نطاقها المحدّث بوضوح في Compliance Matrix. **لا ادعاء بامتثال كامل** إلا بعد تغطية جميع المتطلبات والمراجعة المحاسبية. الشفافية في النطاق والاستثناءات أفضل من الادعاءات العامة.

---

## تفاصيل المهام

### `P11-01` — توسيع نطاق IFRS 15 — Revenue from Contracts

- **المشكلة التي تحلها:** تقرير التحليل يُحدد 4 فجوات رئيسية في IFRS 15:
  - **Variable consideration** (المقابل المتغير): غير منفذة — مصرّح عنها في `UnimplementedRequirements`
  - **Financing component** (المكون التمويلي): غير منفذة
  - **Contract modifications** (تعديلات العقود): غير منفذة
  - **Durable workflow state** (حالة دائمة): الحالة في الذاكرة فقط

- **الهدف الذي تحققه:** توسيع نطاق IFRS 15 ليشمل المتطلبات المحددة مع workflow دائم وربط بالترحيل.

- **نطاق المهمة:**
  - `internal/ifrs15/` — توسيع التنفيذ
  - `modules/ifrs15/module.go` — تحديث القدرات والنطاق
  - `pkg/spi/standards/` — استخدام `DisclosureProvider` من P9-05
  - اختبارات و Golden Scenarios
  - **لا تشمل:** Principal/Agent assessment كامل، أو Licenses of IP

- **خطوات التنفيذ:**
  1. **Variable Consideration:** تنفيذ تقدير المقابل المتغير (Expected Value أو Most Likely Amount)، مع Constraining the variable consideration (حد الاعتراف).
  2. **Financing Component:** تعريف عتبة الأهمية (إعفاء العقود ≤ سنة)، حساب Time Value of Money على مبالغ العقد.
  3. **Contract Modifications:** تعريف أنواع التعديلات: (أ) عقد منفصل، (ب) إنهاء القديم وإنشاء جديد، (ج) تعديل تراكمي. تنفيذ إعادة حساب التخصيص عند التعديل.
  4. **Durable State:** نقل حالة العقد (Contract State Machine) من الذاكرة إلى التخزين الدائم عبر Storage Port. تعريف حالات: Draft, Active, Modified, Satisfied, Cancelled.
  5. تحديث Compliance Matrix بالمتطلبات الجديدة المدعومة.
  6. ربط مخرجات القياس بإيضاحات IFRS 15 عبر `DisclosureProvider`.
  7. بناء Golden Scenarios: عقد مع مقابل متغير، عقد مع مكون تمويلي، تعديل عقد.
  8. مراجعة محاسبية مستقلة.

- **الاعتماديات:** `P8-01`، `P9-01`، `P9-05`.

- **معايير القبول:**
  - [ ] Variable Consideration يُقدَّر ويُقيَّد وفق IFRS 15.56-58
  - [ ] Financing Component يُحسب لعقود > سنة مع تعديل الإيراد والفائدة
  - [ ] Contract Modification ينفّذ الأنواع الثلاثة بنتائج محاسبية صحيحة
  - [ ] حالة العقد دائمة وقابلة للاسترجاع بعد Restart
  - [ ] Compliance Matrix محدّثة بدقة
  - [ ] Golden Scenarios مراجعة محاسبياً
  - [ ] الاستثناءات غير المنفذة مصرّح عنها صراحةً

- **ملاحظة التوازي:** يمكن تنفيذها بالتوازي مع `P11-02` و`P11-03` و`P11-04`. لا تعدّل Standards SPI أو Registry أو Posting Core. تعدّل `internal/ifrs15/` و `modules/ifrs15/` فقط.

---

### `P11-02` — توسيع نطاق IFRS 9 — Financial Instruments

- **المشكلة التي تحلها:** التحليل يُحدد 3 فجوات رئيسية:
  - **Classification & measurement** (التصنيف والقياس): غير منفذة — لا Business Model أو SPPI test
  - **Hedge accounting** (محاسبة التحوط): غير منفذة
  - **Posting workflow**: لا يتكامل مع Application Layer بعد

- **الهدف الذي تحققه:** إضافة إطار التصنيف الأولي، وتوسيع ECL ليشمل workflow ترحيل كامل.

- **نطاق المهمة:**
  - `internal/ifrs9/` — توسيع
  - `modules/ifrs9/module.go` — تحديث
  - **لا تشمل:** Hedge accounting الكامل (يبقى خارج النطاق مع تصريح)

- **خطوات التنفيذ:**
  1. **Classification Framework:** تعريف نموذج بيانات لـ Business Model Assessment و SPPI Test كمدخلات من النظام المضيف (المحرك لا يقيّم بنفسه — يستقبل القرار).
  2. تعريف فئات القياس: Amortised Cost, FVOCI, FVTPL مع قواعد القياس اللاحق لكل فئة.
  3. **ECL Posting Workflow:** ربط نتائج ECL (المخصصات المحسوبة) بـ Application Layer لتوليد قيود تسوية المخصص ذرياً.
  4. تعريف `ECLPostingCapability` مع Journal Mapping حقيقي لحسابات المخصص والخسائر الائتمانية.
  5. ضمان منع التكرار (Idempotency) لقيود ECL: نفس التقييم لنفس الفترة لا يُنتج قيداً مكرراً.
  6. تحديث Compliance Matrix.
  7. Golden Scenarios: تصنيف أداة مالية، حساب ECL، ترحيل مخصص، انتقال بين المراحل.
  8. مراجعة مستقلة للمنهجية.

- **الاعتماديات:** `P8-01`، `P9-01`، `P9-05`.

- **معايير القبول:**
  - [ ] إطار التصنيف يقبل Business Model/SPPI كمدخلات ويحدد فئة القياس
  - [ ] ECL يُنتج قيود مخصص عبر Application Layer
  - [ ] Idempotency: نفس التقييم لا يُنتج قيداً مكرراً
  - [ ] Journal Mapping حقيقي — لا UUID اصطناعي
  - [ ] انتقال المراحل (Stage 1→2→3) يُغيّر المخصص ويولّد قيد تعديل
  - [ ] Compliance Matrix محدّثة — Hedge accounting مصرّح عنه صراحةً كخارج النطاق
  - [ ] مراجعة مستقلة معتمدة

---

### `P11-03` — توسيع نطاق IFRS 16 — Leases

- **المشكلة التي تحلها:** التحليل يُحدد فجوتين رئيسيتين:
  - **محاسبة المؤجر** (Lessor accounting): غير منفذة
  - **إعفاءات الإيجار قصير الأجل/منخفض القيمة** (Short-term/low-value exemptions): غير منفذة

- **الهدف الذي تحققه:** إضافة محاسبة المؤجر الأساسية (Operating/Finance lease classification) والإعفاءات المنصوص عليها في المعيار.

- **نطاق المهمة:**
  - `internal/ifrs16/` — توسيع
  - `modules/ifrs16/module.go` — تحديث
  - **لا تشمل:** Sale and leaseback، Sub-lease كامل

- **خطوات التنفيذ:**
  1. **Lessor - Operating Lease:** تعريف نموذج المؤجر التشغيلي — اعتراف إيراد الإيجار بشكل مستقيم + حفظ الأصل المؤجر.
  2. **Lessor - Finance Lease:** تعريف نموذج المؤجر التمويلي — إلغاء اعتراف الأصل + اعتراف صافي الاستثمار في الإيجار + جدول فائدة.
  3. **Short-term Exemption:** إعفاء العقود ≤ 12 شهراً — مصروف مستقيم بدون أصل/التزام.
  4. **Low-value Exemption:** إعفاء الأصول منخفضة القيمة — حد قيمة قابل للإعداد.
  5. ربط كل العمليات بـ Journal Mapping وApplication Layer.
  6. تحديث Compliance Matrix.
  7. Golden Scenarios: مؤجر تشغيلي، مؤجر تمويلي، إعفاء قصير الأجل.
  8. مراجعة محاسبية مستقلة.

- **الاعتماديات:** `P8-01`، `P9-01`، `P9-05`.

- **معايير القبول:**
  - [ ] Lessor Operating: إيراد مستقيم + الأصل يبقى في الميزانية
  - [ ] Lessor Finance: صافي الاستثمار في الإيجار + جدول فائدة صحيح
  - [ ] Short-term: مصروف مباشر بدون أصل حق استخدام
  - [ ] Low-value: حد قابل للإعداد + مصروف مباشر
  - [ ] كل العمليات تُرحّل عبر Application Layer
  - [ ] Compliance Matrix محدّثة
  - [ ] مراجعة محاسبية معتمدة

---

### `P11-04` — توسيع نطاق IAS 12 — Income Taxes

- **المشكلة التي تحلها:** التحليل يُحدد فجوتين:
  - **Uncertain tax positions** (المراكز الضريبية غير المؤكدة — IFRIC 23): غير منفذة
  - **Presentation & disclosure** (العرض والإفصاح): غير منفذة

- **الهدف الذي تحققه:** إضافة إطار المراكز الضريبية غير المؤكدة وربط IAS 12 بإطار الإيضاحات.

- **نطاق المهمة:**
  - `internal/ias12/` — توسيع
  - `modules/ias12/module.go` — تحديث
  - ربط بـ `DisclosureProvider` من P9-05
  - **لا تشمل:** نظام إقرار ضريبي محلي

- **خطوات التنفيذ:**
  1. **Uncertain Tax Positions:** تعريف نموذج `UncertainTaxPosition` مع: وصف المركز، المبلغ، احتمال القبول، طريقة التقدير (Most Likely Amount أو Expected Value).
  2. تنفيذ اختبار "more likely than not" — إذا كان الاحتمال > 50% يُعترف بالمبلغ الكامل/المعدّل.
  3. حفظ الافتراضات والمعلومات المتاحة لإعادة التقييم لاحقاً.
  4. **Disclosure Integration:** ربط IAS 12 بـ `DisclosureProvider` لإنتاج إيضاحات: مكونات مصروف الضريبة، معدل الضريبة الفعلي vs القانوني، الفروقات المؤقتة.
  5. تعريف تسوية بين الربح المحاسبي ومصروف الضريبة (Tax Rate Reconciliation).
  6. تحديث Compliance Matrix.
  7. Golden Scenario: شركة مع فروقات مؤقتة + مركز ضريبي غير مؤكد + إيضاحات.
  8. مراجعة ضريبية/محاسبية مستقلة.

- **الاعتماديات:** `P8-01`، `P9-01`، `P9-05`.

- **معايير القبول:**
  - [ ] نموذج Uncertain Tax Position مع طريقتي التقدير
  - [ ] اختبار "more likely than not" يعمل ويؤثر على القياس
  - [ ] الافتراضات محفوظة لإعادة الإنتاج
  - [ ] إيضاحات IAS 12 تُنتَج عبر `DisclosureProvider`
  - [ ] Tax Rate Reconciliation متاحة
  - [ ] Compliance Matrix محدّثة — حدود القانون المحلي موثقة
  - [ ] مراجعة مهنية معتمدة

---

### `P11-05` — ربط Posting Workflow لجميع الوحدات المعيارية

- **المشكلة التي تحلها:** التحليل يُشير إلى أن الوحدات المعيارية الأربع تحسب القياس لكن **لا تُرحّل تلقائياً** عبر Application Layer. القياس ينتج أرقاماً، لكن لا يُولّد قيوداً. هذا يعني أن المستهلك يجب أن يأخذ نتائج القياس وينشئ القيود يدوياً — وهو مسار خطأ ومكرر.

- **الهدف الذي تحققه:** كل وحدة معيارية مفعّلة تُنتج مسودات قيود ثم تُرحّلها عبر Application Layer + Posting Pipeline الموحدة، مع Idempotency ومرجع المعيار.

- **نطاق المهمة:**
  - `modules/ifrs15/`, `modules/ifrs9/`, `modules/ifrs16/`, `modules/ias12/`
  - `internal/ifrs15/`, `internal/ifrs9/`, `internal/ifrs16/`, `internal/ias12/`
  - `pkg/spi/standards/` — تعريف `PostingCapability` إذا لم يكن موجوداً
  - **لا تشمل:** إعادة تصميم Posting Engine

- **خطوات التنفيذ:**
  1. تعريف `PostingCapability` (أو استخدام الموجود): واجهة تسمح للوحدة بإنتاج مسودة قيد وتقديمها للترحيل.
  2. لكل وحدة، تعريف Journal Mapping: أي Journal يُستخدم، أي حسابات، أي فترة.
  3. لكل وحدة، تعريف Trigger: متى يُنتج القيد — عند اكتمال القياس، عند نهاية الفترة، عند حدث معين.
  4. ضمان أن مسودة القيد تحمل: مرجع المعيار (`IFRS15`, `IFRS9`, `IFRS16`, `IAS12`)، مرجع العملية (Contract ID, Lease ID, إلخ)، Idempotency Key.
  5. ضمان أن فشل Posting لا يترك حالة المعيار مؤكدة — حالة العملية تُحدّث فقط بعد نجاح Commit.
  6. اختبار تكاملي لكل وحدة: قياس → إنتاج مسودة → ترحيل → تحقق من GL → تحقق من Idempotency.
  7. اختبار فشل: قياس → إنتاج مسودة → فشل Posting → التأكد من عدم تأكيد الحالة.

- **الاعتماديات:** `P11-01`، `P11-02`، `P11-03`، `P11-04`.

- **معايير القبول:**
  - [ ] كل وحدة مفعّلة تُنتج قيوداً عبر Application Layer — لا SQL مباشر
  - [ ] كل قيد يحمل مرجع المعيار ومرجع العملية
  - [ ] كل قيد يحمل Idempotency Key — لا تكرار
  - [ ] Journal Mapping حقيقي — لا UUID اصطناعي أو Journal وهمي
  - [ ] فشل Posting لا يُأكّد حالة المعيار
  - [ ] اختبار تكاملي لكل وحدة على PostgreSQL
  - [ ] Contract Parity: نفس القيود على PostgreSQL وMemory

---

## معايير اعتماد المرحلة `P11`

| المعيار | الدليل المطلوب |
| --------- | --------------- |
| IFRS 15 توسّع ضمن نطاق معلن | Compliance Matrix + Golden Scenarios + مراجعة |
| IFRS 9 توسّع ضمن نطاق معلن | Compliance Matrix + Golden Scenarios + مراجعة |
| IFRS 16 توسّع ضمن نطاق معلن | Compliance Matrix + Golden Scenarios + مراجعة |
| IAS 12 توسّع ضمن نطاق معلن | Compliance Matrix + Golden Scenarios + مراجعة |
| Posting Workflow لكل وحدة | اختبار تكاملي: قياس → ترحيل → GL → Idempotency |
| لا ادعاء بلا دليل | كل Compliance Matrix تذكر الاستثناءات صراحةً |
| Disclosures تعمل | إيضاحات منتجة من كل وحدة مفعّلة |

---

## ملكية الملفات والتنسيق

| المهمة | الملفات التي تملك حق تعديلها | المحظور تعديله دون تنسيق |
| -------- | ------------------------------ | -------------------------- |
| `P11-01` | `internal/ifrs15/`, `modules/ifrs15/` | `pkg/spi/standards/`, `internal/posting/` |
| `P11-02` | `internal/ifrs9/`, `modules/ifrs9/` | `pkg/spi/standards/`, `internal/posting/` |
| `P11-03` | `internal/ifrs16/`, `modules/ifrs16/` | `pkg/spi/standards/`, `internal/posting/` |
| `P11-04` | `internal/ias12/`, `modules/ias12/` | `pkg/spi/standards/`, `internal/posting/` |
| `P11-05` | `modules/*/`, `pkg/spi/standards/` | `internal/posting/engine.go`, `pkg/sdk/builder.go` |
