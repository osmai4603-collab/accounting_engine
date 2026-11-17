# المرحلة `P8` — معالجة الفجوات المعمارية الحرجة وترحيل StandardModule

- **الهدف:** إزالة الاعتماد على `legacy.CoreLedgerStore` في واجهة StandardModule، ضمان ذرية عمليات الترحيل عبر المسار الحديث (`storage.Ports`)، فرض سلامة تفعيل القدرات في Bootstrap، وحل التشوهات المعمارية الحرجة.
- **عدد المهام:** 5 مهام (`P8-01` إلى `P8-05`)
- **الاعتماديات:** اكتمال `P7` واعتمادها
- **الحالة الحالية للميزات:**
  - StandardModule SPI → legacy: **مكتملة جزئياً** (التصميم موجود لكن الربط بـ Ports معطّل)
  - AccountTypeRevenue: **مكتملة جزئياً** (alias مكرر بدون توثيق deprecation رسمي)
  - Posting concurrency: **مكتملة جزئياً** (sync.Mutex يعمل لكنه bottleneck محتمل)
  - Atomic Posting via Ports: **غير مكتملة** (تخصيص التسلسل والترحيل قد ينفصلان)
  - Bootstrap Integrity: **غير مكتملة** (إمكانية تفعيل قدرات دون التحقق من توفر اعتمادياتها)

---

## تفاصيل المهام

### `P8-01` — ترحيل واجهة StandardModule من `legacy.CoreLedgerStore` إلى `storage.Ports`

- **المشكلة التي تحلها:** واجهة `StandardModule` في `pkg/spi/standards/module.go` (سطر 23-24) تعتمد على `legacy.CoreLedgerStore` لـ `ValidateDependencies` و `Initialize`. هذا يمنع استخدام `WithStoragePorts()` مع `WithStandard()` معاً، حيث يرفض Builder ذلك صراحةً في `builder.go` (سطر 356-358): *"standard modules require WithCoreStorage in this release"*. النتيجة: المسار الحديث (`Ports`) لا يدعم وحدات المعايير، وهو تناقض معماري يمنع تحقيق مبدأ P5 (الوحدات المعيارية اختيارية) عبر المسار الموصى به.

- **الهدف الذي تحققه:** تمكين استخدام `WithStoragePorts(ports)` و `WithStandard(module)` معاً، وتعتمد StandardModule على واجهات عامة من `pkg/spi/` فقط دون استيراد `legacy` أو `internal`. يحتفظ المسار القديم (`WithCoreStorage`) بالعمل حتى إزالته المخططة.

- **نطاق المهمة:**
  - `pkg/spi/standards/module.go` — واجهة `StandardModule`
  - `pkg/spi/standards/registry.go` — تسجيل وتنسيق القدرات
  - `pkg/sdk/builder.go` — إزالة حظر Ports+Standard وتكييف `initializeStandards`
  - `modules/ifrs15/module.go`, `modules/ifrs9/module.go`, `modules/ifrs16/module.go`, `modules/ias12/module.go` — تحديث التنفيذ
  - اختبارات تكامل وعقود في `test/`
  - **لا تشمل:** تغيير السلوك الداخلي لوحدات المعايير أو إضافة ميزات IFRS جديدة

- **خطوات التنفيذ:**
  1. تصميم واجهة `ModulePorts` في `pkg/spi/standards/` تكشف فقط القدرات المطلوبة: قراءة الأرصدة والقيود، الوصول لـ MasterData، وتقديم مسودات القيود عبر Application Layer.
  2. تحديث `StandardModule.ValidateDependencies` لتقبل `ModulePorts` بدلاً من `legacy.CoreLedgerStore`.
  3. تحديث `StandardModule.Initialize` لتلقي واجهة المنافذ الجديدة.
  4. تقديم Adapters داخلية لتحويل `storage.Ports` إلى `ModulePorts` للمسار الحديث، مع دعم المسار القديم للتوافق العكسي.
  5. فك الحظر في `builder.go` (سطر 356-358).
  6. تكييف الوحدات الأربع (`ifrs15`, `ifrs9`, `ifrs16`, `ias12`).
  7. كتابة اختبار تكامل: `WithStoragePorts` + `WithStandard` يُثبت نجاح التهيئة.
  8. اختبار استمرارية مسار `WithCoreStorage`.
  9. تحديث `docs/compatibility_policy.md`.

- **الاعتماديات:** `P7` (اكتمال جميع مهامها واعتماد المرحلة).

- **معايير القبول:**
  - [x] `WithStoragePorts(ports)` + `WithStandard(module)` يبني المحرك بنجاح.
  - [x] `StandardModule` لا تستورد `legacy` ولا `internal` في واجهتها العامة.
  - [x] استمرار عمل `WithCoreStorage(legacyStore)` + `WithStandard(module)` للتوافق العكسي.
  - [x] اجتياز الوحدات الأربع لاختبارات العقود على كلا المسارين.
  - [x] غياب أي import غير مصرح به لـ `legacy` في `pkg/spi/standards/`.
  - [x] اختبار Contract Parity يثبت تكافؤ السلوك.

---

### `P8-02` — توثيق وحسم التباس `AccountTypeRevenue` واعتماده كـ Alias

- **المشكلة التي تحلها:** في `pkg/api/v1/ledger/account.go`: القيمة `AccountTypeRevenue = "income"` معرّفة كـ Alias مكرر لـ `AccountTypeIncome`. يسبب هذا التباساً لدى المطورين حول النوع المعتمد وما إذا كانت هناك قيمة مستقلة برمجياً.

- **القرار المعتمد:**
  1. الإبقاء على `AccountTypeRevenue` كـ Alias للقيمة الحالية `"income"`.
  2. إضافة وسم `// Deprecated: Use AccountTypeIncome instead.` صريح في GoDoc مع توجيه المطورين للاعتماد على `AccountTypeIncome`.
  3. الحفاظ التام على القيمة الحالية دون تغيير وعدم إضافة قيمة مستقلة `"revenue"`.
  4. عدم إجراء أي Database Migration في هذه المهمة لحماية استقرار قواعد البيانات العاملة.
  5. إبقاء الرمز متاحاً للتوافق التام مع المستهلكين الحاليين دون كسر الـ Public API.
  6. توثيق أن التمييز بين الإيرادات التشغيلية (Revenue) وغيرها من الإيرادات (Other Income) يُدار عبر التصنيف المحاسبي الشجري أو تصنيف التقارير المستقل وليس عبر النوع الأساسي للحساب.

- **نطاق المهمة:**
  - `pkg/api/v1/ledger/account.go` — توثيق الـ Deprecation والتوجيه المحاسبي
  - `docs/adr/` — توثيق القرار في ADR-0008
  - `docs/api_guidelines.md` — توثيق معايير تصنيف الإيرادات في التقارير
  - **لا تشمل:** تعديل جداول قاعدة البيانات أو ترحيل البيانات

- **خطوات التنفيذ:**
  1. تدقيق كافة المواضع التي تستخدم `AccountTypeRevenue` و `AccountTypeIncome` في الكود ومكتبات الأمثلة.
  2. إضافة توثيق `// Deprecated: Use AccountTypeIncome instead.` رسمي مع شرح دلالي كامل في `pkg/api/v1/ledger/account.go`.
  3. كتابة ADR-0008 يوضح المبرر المعماري والمحاسبي للقرار.
  4. تحديث API snapshot لاحتواء وسم الإهمال.
  5. التأكد من أن اختبارات ميزان المراجعة وقائمة الدخل تتبع التصنيف الموحد.

- **الاعتماديات:** لا شيء (مستقلة داخل `P8`).

- **معايير القبول:**
  - [x] وجود تعليق `// Deprecated: Use AccountTypeIncome instead.` فوق `AccountTypeRevenue`.
  - [x] الحفاظ على القيمة الحرفية `"income"` دون أي تعديل.
  - [x] عدم وجود أي كود ترحيل لقواعد البيانات (No DB Migration).
  - [x] توثيق ADR-0008 وتوضيح كيفية التمييز بين Revenue و Other Income في التقارير.
  - [x] عدم كسر أي من الاختبارات القائمة أو المستهلكين الخارجيين.

---

### `P8-03` — استبدال `sync.Mutex` بتزامن على مستوى DB وإدارة الأقفال

- **المشكلة التي تحلها:** استخدام `sync.Mutex` العام في `internal/posting/engine.go` يُشكل عنق زجاجة (bottleneck) في الأنظمة عالية الأحمال، ويمنع التوسع الأفقي (Multi-instance) حيث لا يحمي من التعارض عبر عدة حاويات، ويخلط بين مسؤوليات التطبيق وقاعدة البيانات.

- **الهدف الذي تحققه:** نقل إدارة التزامن إلى مستوى قاعدة البيانات عبر Advisory Locks أو تقنيات القفل على مستوى الدفتر/الشركة، مع توفير بديل دقيق للذاكرة.

- **نطاق المهمة:**
  - `internal/posting/engine.go` — إعادة صياغة قفل الترحيل
  - `storage/postgres/` — تنفيذ آليات `pg_advisory_xact_lock`
  - `storage/memory/` — تنفيذ محاكاة مجزأة على مستوى الكيان (Fine-grained in-process lock)
  - اختبارات التزامن في `test/concurrency/`

- **خطوات التنفيذ:**
  1. حصر العمليات الحرجة: تخصيص التسلسل، الترحيل، العكس، والإقفال.
  2. تصميم استراتيجية قفل دقيقة (على مستوى الدفتر Journal أو الكيان Company) تمنع تعارض التسلسل وتسمح بالترحيل المتزامن في الدفاتر المنفصلة.
  3. تنفيذ `PostgresLockStrategy` باستخدام `pg_advisory_xact_lock` لضمان تحرير القفل آلياً فور انتهاء المعاملة (Commit أو Rollback).
  4. استبدال الـ Mutex العام باستدعاء استراتيجية القفل.
  5. كتابة اختبارات التزامن للأحمال المتوازية على نفس الدفتر مقابل دفاتر متعددة.

- **الاعتماديات:** لا شيء (مستقلة داخل `P8`).

- **معايير القبول:**
  - [x] إزالة القفل العام الأحادي من مسار الترحيل الرئيسي.
  - [x] الترحيل المتزامن في دفاتر مختلفة يعمل بالتوازي الحقيقي على PostgreSQL.
  - [x] الترحيل على نفس الدفتر يتسلسل بانتظام دون حدوث Deadlocks أو فجوات ترقيم.
  - [x] تحرير القفل تلقائياً عند حدوث Rollback.
  - [x] توفير محاكاة دقيقة ومتكافئة في محول الذاكرة (Memory Adapter).

---

### `P8-04` — ضمان ذرية الترحيل عبر Public Storage Ports (Atomic Posting Boundary)

- **المشكلة التي تحلها:** في بنية المسار الحديث (`storage.Ports`)، قد يتم تنفيذ تخصيص التسلسل (Sequence Allocation) وتخزين القيد وتحديث الأرصدة وسجل التدقيق في معاملات منفصلة أو غير مدمجة تحت وحدة عمل واحدة (Single Unit of Work)، مما يعرض النظام لخطر تجزئة البيانات وفجوات التسلسل عند حدوث فشل وسيط. هذا يمثل خطرًا حرجًا على سلامة الدفتر الأستاذ.

- **الهدف الذي تحققه:** ضمان تنفيذ كافة مراحل عملية الترحيل (تخصيص التسلسل، رأس القيد، السطور، الأرصدة، التدقيق، مفتاح عدم التكرار، وتحديث الحالة) داخل حدود معاملة ذرية موحدة غير قابلة للتجزئة (Single Atomic Transaction) عبر `storage.Ports`.

- **نطاق المهمة:**
  - `pkg/spi/storage/ports.go` — تدقيق واجهات المعاملات (UnitOfWork / TransactionalPort)
  - `internal/posting/` — توحيد خط أنابيب الترحيل داخل المعاملة
  - `storage/postgres/` و `storage/memory/` — التحقق من حدود المعاملة
  - اختبارات حقن الأخطاء (Fault Injection Tests)
  - **ملاحظة حرجة:** هذه المهمة شرط إلزامي مسبق (Prerequisite) لكافة مهام المرحلة `P9` (خاصة الإقفال السنوي).

- **خطوات التنفيذ:**
  1. فحص واجهات `storage.Ports` والتأكد من دعمها لواجهة `UnitOfWork` أو سياق المعاملة الذري الموحد.
  2. إعادة تنظيم خطوات الترحيل في `PostingPipeline`:
     - فتح المعاملة (Begin Tx)
     - التحقق من عدم التكرار (Idempotency Check)
     - تخصيص التسلسل وحجز الرقم (Sequence Allocation under Tx)
     - كتابة رأس القيد وسطور التفاصيل (Insert Header & Lines)
     - تحديث أرصدة الحسابات المتأثرة (Update Account Balances)
     - تسجيل قيد التدقيق (Append Audit Trail)
     - تثبيت مفتاح عدم التكرار وتحديث الحالة (Mark Posted & Set Idempotency)
     - اعتماد المعاملة (Commit Tx)
  3. بناء اختبارات حقن الفشل (Failure Injection Tests):
     - محاكاة انقطاع الاتصال أو خطأ بعد تخصيص الرقم وقبل حفظ السطور -> التحقق من Rollback كامل وعدم حرق الرقم إن أمكن أو تسجيل الإلغاء بدقة.
     - محاكاة فشل في كتابة التدقيق أو تحديث الأرصدة -> تراجع كامل دون ترك قيد معلق.
  4. التحقق من تكافؤ السلوك بين PostgreSQL ومحول الذاكرة.

- **الاعتماديات:** `P8-01`.

- **معايير القبول:**
  - [x] جميع خطوات الترحيل تُنفذ داخل معاملة ذرية واحدة (Single UoW).
  - [x] فشل أي خطوة وسيطة يؤدي إلى Rollback كامل للحالة في قاعدة البيانات دون بقاء آثار جزئية.
  - [x] نجاح اختبارات حقن الفشل بعد تخصيص التسلسل وقبل التثبيت.
  - [x] توثيق معايير الذرية كمرجع إلزامي لمهمة الإقفال السنوي `P9-01`.

---

### `P8-05` — فرض سلامة Bootstrap وتحقق الاعتماديات (Capability Integrity)

- **المشكلة التي تحلها:** يستطيع مهيئ المحرك (`Bootstrap` / `Builder`) حالياً إعلان تفعيل ميزات وقدرات (مثل التقارير أو المعايير أو استقبال الأحداث) حتى لو كانت الاعتماديات ومصادر التخزين اللازمة لها مفقودة أو غير متوافقة (مثل تفعيل `ReportingEnabled` مع عدم تمرير `ReportingStore`). هذا يؤدي إلى انهيار التطبيق أثناء التشغيل بدلاً من اكتشافه مبكراً.

- **الهدف الذي تحققه:** تطبيق قاعدة معمارية صارمة: `Enabled(C) ⟹ Available(Service_C)`، بحيث يتحقق الـ Bootstrap من توفر كافة الاعتماديات اللازمة لكل قدرة مفعّلة ويفشل فوراً وبشكل صريح (Fail-Fast) عند نقص أي اعتمادية إجبارية.

- **نطاق المهمة:**
  - `pkg/sdk/builder.go` — تعزيز فحص الاعتماديات لكل Capability
  - `pkg/sdk/engine.go` — تدقيق مصفوفة التحقق أثناء التهيئة
  - `test/bootstrap/` — اختبارات حالات الفشل الموجهة
  - توثيق الاعتماديات في دليل التهيئة

- **خطوات التنفيذ:**
  1. رسم مصفوفة الاعتماديات لكل Capability:
     - Reporting ⟹ يتطلب `ReportingStore` أو منافذ قراءة مطابقة.
     - Standards Modules ⟹ يتطلب `ModulePorts` كاملة وجدول حسابات معتمد.
     - Event Ingestion ⟹ يتطلب `EventStore` و `MasterDataService` ومحول ترحيل.
     - Subledgers ⟹ يتطلب مخازن الدفاتر الفرعية وحسابات الرقابة.
     - Posting Policy ⟹ يتطلب مزود السياسات وقواعد الترحيل.
  2. تعديل دالة `Build()` في `builder.go` لإجراء الفحص المتزامن قبل إنشاء كائن المحرك.
  3. صياغة رسائل خطأ دقيقة توضح اسم الميزة المفعلة، الاعتمادية الناقصة، والحل البرمجي الموصى به.
  4. كتابة اختبارات وحدة للـ Builder تثبت فشل البناء عند غياب أي اعتمادية مطلوبة.

- **الاعتماديات:** `P8-01`.

- **معايير القبول:**
  - [x] فشل التهيئة الصريح مع رسالة واضحة عند طلب تفعيل Reporting دون توفير المخزن الخاص به.
  - [x] فشل التهيئة عند تفعيل أي وحدة معايير دون توفير منافذ التخزين اللازمة.
  - [x] عدم بدء تشغيل المحرك في حالة "يعمل جزئياً" إذا كانت إحدى القدرات المفعّلة فاقدة لاعتمادياتها.
  - [x] توثيق مصفوفة الاعتماديات في وثائق الـ Bootstrap.

---

## معايير اعتماد المرحلة `P8`

| المعيار | الدليل المطلوب |
| --------- | --------------- |
| StandardModule يعمل مع Ports | اختبار تكامل: Builder + Ports + Standard Module |
| توافق عكسي مع Legacy path | اختبار يثبت بقاء وعمل المسار القديم |
| AccountType واضح وموثق | GoDoc Deprecation + ADR-0008 دون DB Migration |
| Posting concurrency محسّن | اختبارات تزامن على مستوى DB دون Deadlocks |
| ذرية الترحيل عبر Ports مؤكدة | اختبارات Failure Injection و Rollback كامل (`P8-04`) |
| سلامة Bootstrap محققة | اختبارات Fail-fast لكل Capability ناقصة الاعتمادية (`P8-05`) |
| Contract Parity لم يتأثر | نجاح جناح Contract Parity Suite بالكامل |

---

## ملكية الملفات والتنسيق

| المهمة | الملفات التي تملك حق تعديلها | الملفات المشتركة المحظور تعديلها دون تنسيق |
| -------- | ------------------------------ | ------------------------------------------ |
| `P8-01` | `pkg/spi/standards/`, `pkg/sdk/builder.go`, `modules/*` | `pkg/api/v1/`, `pkg/spi/storage/`, `internal/posting/` |
| `P8-02` | `pkg/api/v1/ledger/account.go`, `docs/adr/ADR-0008.md` | `pkg/sdk/`, `storage/`, `internal/posting/` |
| `P8-03` | `internal/posting/engine.go`, `storage/postgres/`, `storage/memory/` | `pkg/spi/storage/ports.go`, `pkg/sdk/` |
| `P8-04` | `internal/posting/`, `pkg/spi/storage/ports.go` | `pkg/api/v1/`, `modules/*` |
| `P8-05` | `pkg/sdk/builder.go`, `pkg/sdk/engine.go` | `internal/posting/`, `storage/*` |
