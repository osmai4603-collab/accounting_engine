# تحليل مقارن: مراجعة `engine3` مقابل تقرير تحقق إعلان `v1`

**تاريخ التحليل:** 2026-11-13  
**المرجع الأول:** مراجعة معمارية وتقنية للنسخة الثالثة `engine3.zip` (فحص ثابت).  
**المرجع الثاني:** [`docs/v1_declaration_verification.md`](./v1_declaration_verification.md) (تحقق عند `HEAD a58ebe6`).  
**أساس التحقق العملي لهذا الملف:** `HEAD a58ebe6`، Go `1.26.3`، PostgreSQL `18.6`.  
**ملاحظة نطاق:** التحليل يعتمد على `HEAD a58ebe6`، وليس على `engine3.zip`. أي اختلاف زمني بين النسختين قد يفسر جزءاً من فروق النتائج. أثناء إعداد الملف كانت هناك تعديلات غير مُودعة في الشجرة (معرفات، خدمة، mapping) لم تُحتسب في الأحكام.

---

## 1. الحكم التنفيذي

الوثيقتان **لا تتعارضان في الجوهر**، بل تصفان المشكلة نفسها بدرجتين مختلفتين من التدقيق:

1. **الاتفاق على القرار:** كلتاهما ترفض إعلان `v1`، وتعتبر تحسن الربط بـ `posting.Engine` حقيقياً لكنه غير كافٍ لقابلية إعادة الاستخدام الخارجية.
2. **الاختلاف الأساسي في المنهج:** مراجعة `engine3` فحص ثابت ولم تشغّل الاختبارات، بينما تقرير التحقق أجرى بناءً واختبارات وبوابات جودة على شجرة أحدث، ثم صاغ **ثمانية شروط قبول** رسمية.
3. **الاختلاف في التغطية:** تقرير التحقق أقوى في الأدلة التشغيلية وبوابات `gapred/gofmt/purity/CI`، بينما مراجعة `engine3` أكثر دقة في بعض مسارات الكود التي لم يغطها تقرير التحقق، وأهمها:
   - اشتراط وجود سجل `Journal` مسبقاً في PostgreSQL ليعمل `PostAndSequenceContext`.
   - عدم نقل `JournalCode` والأبعاد و`Payload` إلى القيد الناتج.
   - تجاهل خطأ `GetIncomeStatement` داخل `GetBalanceSheet` في Memory.
   - اعتماد موائمات التخزين على `pkg/sdk` (تبعية معكوسة).
   - إهمال سبب فشل الحدث (`FailPublicEventIngestion`) وسياسة إعادة المحاولة.

4. **نتيجة الدمج:** تقرير التحقق هو **المرجع الرسمي للحالة**، ومراجعة `engine3` هي **قائمة فجوات إضافية يجب ضمها إليه**، خصوصاً في مسار PostgreSQL وبناء الحدث وMemory Reporting.
5. **تعديل واحد مقترح على تقرير التحقق:** خفض حكم الشرط الرابع `#4` من «مستوفى مع تحفظات» إلى **«جزئي»**، بسبب خطأ Memory Balance Sheet ودالة `EvaluateAndConstructEntry` العامة، وعدم تحويل العملات فعلياً في تقارير Memory.

---

## 2. لماذا تختلف الوثيقتان؟

| سبب الاختلاف | مراجعة `engine3` | تقرير تحقق `v1` | الأثر |
| --- | --- | --- | --- |
| النسخة المفحوصة | `engine3.zip` | `HEAD a58ebe6` | بعض الفجوات قد تكون عولجت أو تغيرت بين النسختين. |
| المنهج | فحص ثابت فقط | فحص ثابت + `build` + `vet` + `test` + PostgreSQL + `gapred` + مستهلك خارجي | تقرير التحقق يملك أدلة تنفيذ، لكنه قد يغفل تفاصيل مسارات لم يختبرها. |
| البيئة | Go `1.23.2`، تعذر تشغيل الاختبارات | Go `1.26.3`، PostgreSQL `18.6` | نتائج المراجعة لا تتضمن حكماً على نجاح الاختبارات. |
| طبيعة المخرجات | مراجعة معمارية + توصيات + مراحل A–D | تحقق من شروط `v1` + مصفوفة فجوات + أدلة أوامر | الوثيقتان متكاملتان لا متبادلتان. |
| مستوى التفصيل | أوسع في بعض مسارات الكود | أدق في العقود، الاختبارات، وبوابات الجودة | الدمج يعطي صورة أكمل. |

---

## 3. جدول المقارنة التفصيلي

### 3.1 المسار المحاسبي وبيانات Master Data

| البند | مراجعة `engine3` | تقرير تحقق `v1` | التحقق عند `HEAD a58ebe6` | الحكم |
| --- | --- | --- | --- | --- |
| SDK → Posting Engine | يمر عبر `posting.Engine` عند توفر `BulkRepository` و`ContextStorageRepository` | يمر فعلياً للسائقين الرسميين، لكن مع `fallback` مباشر | `builder.go` ينشئ المحرك فقط عند نجاح Type Assertions، وإلا يعود إلى `store.PostEntry` | متفقان: المسار مشروط وليس إلزامياً |
| فشل Build عند غياب Posting | لا يفشل `Build`؛ يبقى `postingEngine == nil` | يذكر `fallback` لكنه لا يعتبره فشل بناء | `Build` ينجح بـ `CoreLedgerStore` فقط | متفقان |
| ربط المحرك بالحوكمة والسياسات | لا يربط Approval Gate / Maker-Checker / Policy Unit / Dimensions / Observability | يضيف أن `PosterID = CreatedBy`، وأن بوابة الاعتماد fail-open | لا استدعاء لأي `Set...`؛ `posterID` يأتي من `CreatedBy` | متفقان؛ تقرير التحقق أدق في التفاصيل |
| Public Master Data API | غير موجودة؛ التهيئة عبر `internal/masterdata` | غير موجودة؛ لا `pkg/spi/masterdata` | لا توجد حزمة Master Data عامة؛ `CreateCompany/CreateAccount/CreateFiscalYear` تستقبل أنواع `internal/masterdata` | متفقان |
| مستهلك خارجي مستقل | الاختبار داخل نفس الوحدة ويستورد `internal` | يثبت فشل بناء مستهلك عام يحاول التهيئة | لا يوجد `go.mod` ثانٍ، ومحاولة `CreateCompany` من خارج الوحدة تفشل | متفقان؛ تقرير التحقق يملك دليل بناء |
| سجل `Journal` في PostgreSQL | **فجوة صريحة:** لا `CreateJournal`، و`PostAndSequenceContext` يقرأ `journals ... FOR UPDATE` | لا يذكرها كبند مستقل، رغم تغطيته لفجوة Master Data | `JournalID` يُشتق من `company + SourceModule`، ولا يوجد مسار عام لإنشاء `Journal`؛ اختبارات PostgreSQL تُدخل السجل بـ SQL مباشر | **مراجعة `engine3` انفردت بالفجوة الأهم هنا** |
| `JournalCode` والأبعاد و`Payload` | `JournalCode` لا ينتقل إلى `PostingRule`؛ الأبعاد و`Payload` لا يستخدمان | لا يعالجها كفجوة مستقلة | Mapping يبني `PostingRule` دون `JournalCode`، و`constructEventEntry` لا يقرأ `Dimensions/Payload` | **مراجعة `engine3` أدق** |
| العملات الأجنبية | `ExchangeRate = 1` دائماً، ولا حقن `ExchangeRateSPI` | يذكرها ضمن P0-3 الجزئي | السطران يستخدمان `1`، والمبالغ الوظيفية تساوي مبالغ المعاملة | متفقان |
| العكس المحاسبي | يستخدم `ReverseDualContext`، لكن السبب لا يُحفظ والتاريخ `time.Now()` | يضيف فشل `GAP-PST-04/05` | السبب يُمرر كـ `creator` فقط؛ الوصف يعدل على DTO المرجعي، والتاريخ `time.Now().UTC()` | متفقان؛ تقرير التحقق يضيف دليل `gapred` |
| `EvaluateAndConstructEntry` العامة | تنشئ حسابات عشوائية وتضع `posted` | تذكرها كتحفظ على الشرط الرابع | الدالة ما زالت عامة، وتستخدم `NewAccountID()` و`ExchangeRate=1` و`Status=posted` | متفقان |

### 3.2 الأحداث وIdempotency

| البند | مراجعة `engine3` | تقرير تحقق `v1` | التحقق عند `HEAD a58ebe6` | الحكم |
| --- | --- | --- | --- | --- |
| ذرية Idempotency | Claim ثم Posting ثم Complete في معاملات منفصلة | يؤكدها ويضيف غياب اختبار PostgreSQL للمسار العام | `ProcessEvent` يستدعي الثلاثة منفصلة | متفقان |
| فشل `Complete` | يُخفى ويعاد القيد كنجاح | يؤكد الإخفاء وبقاء `processing` | الخطأ يُهمل صراحةً بعد نجاح الترحيل | متفقان |
| إعادة محاولة `failed` | Claim لا يعيد حجز المفتاح الفاشل ويعيد خطأ حالة غير صالحة | لا يفصّل سياسة الإعادة | الفرع يعيد `idempotency key is in an invalid state` | **مراجعة `engine3` أدق تشغيلياً** |
| سبب الفشل | `FailPublicEventIngestion` يتجاهل النص | لا يذكره | `_ = reason` في Memory وPostgreSQL | **مراجعة `engine3` انفردت به** |
| بصمة الحدث | تستثني `EventID` و`EffectiveDate` و`Dimensions` و`Payload` والوصف/المرجع | تستثني `EventID` و`Payload` و`Dimensions` | البصمة تغطي النوع والشركة والدفتر والمبلغ والعملة وأكواد الحسابين، ولا تغطي الحقول المذكورة | متفقان؛ مراجعة `engine3` أوسع قليلاً |
| اختبارات Idempotency | لا توجد اختبارات PostgreSQL للمسار العام | يؤكد غيابها | لا استدعاء لـ `Claim/Complete/ProcessEvent` في اختبارات PostgreSQL | متفقان |

### 3.3 العقود العامة والاختبارات المعمارية

| البند | مراجعة `engine3` | تقرير تحقق `v1` | التحقق عند `HEAD a58ebe6` | الحكم |
| --- | --- | --- | --- | --- |
| أنواع المعرفات | كلها Aliases لـ `uuid.UUID`؛ حقول خاطئة دلالياً | يضيف تفاصيل `PartnerID/CostCenterID/ProjectID/SegmentID/BusinessUnitID` و`FiscalYear.ID` | مؤكد في `identifiers.go` و`entry.go` و`period.go` | متفقان |
| Round-trip Mapping | لا يذكره | يذكره: `SourceModule: ""`، وغياب حقول على مستوى القيد | مؤكد في `mapping.go` و`internal/posting/entry.go` | **تقرير التحقق انفرد به** |
| عقود SPI وحدود المعاملة | يطالب بنقل عقود Posting/Master Data إلى Public SPI | يطالب بـ Unit of Work/Transaction Manager/Idempotency Repository | `CoreLedgerStore` ما زال يعرّف `PostEntry` كعملية تخزين | متفقان |
| تبعية Storage Adapter على SDK | **فجوة صريحة:** `storage/*/public_events.go` يستورد `pkg/sdk` | لا يذكرها | مؤكد في Memory وPostgreSQL | **مراجعة `engine3` انفردت بها** |
| تسرب `internal/*` من الجذر | مدخلان عامان: الجذر و`pkg/sdk` | يذكر تسرب الجذر وفشل سكربت النقاء | `engine.go` يستورد حزم `internal/*`، و`pkg/sdk` يستورد `internal/*` أيضاً | متفقان |
| اختبار المستهلك الخارجي | داخل الوحدة ويستورد `internal` | يؤكد ويضيف دليل فشل التهيئة | مؤكد | متفقان |
| اختبار القطاعات | لا Adapters، فقط Templates؛ الاختبار يفحص القبول/الرفض وعدد السطور | يؤكد ويضيف أنه يتوقع رفض Retail/Agriculture/SaaS | مؤكد | متفقان |
| تبديل Memory/PostgreSQL | لا اختبار تبديل عبر SDK | يؤكد؛ Phase C يقارن الموائمات مباشرة | مؤكد | متفقان |
| اختبارات `gapred` | لا يذكرها | **7 حزم حمراء**، وCI لا يشغّل الوسم | إعادة التشغيل تؤكد 7 حزم | **تقرير التحقق انفرد به** |
| `gofmt` ونقاء API | لا يذكرهما | 9 ملفات غير منسقة، وسكربت النقاء يفشل | مؤكد | **تقرير التحقق انفرد به** |

### 3.4 الخدمة والتشغيل

| البند | مراجعة `engine3` | تقرير تحقق `v1` | التحقق عند `HEAD a58ebe6` | الحكم |
| --- | --- | --- | --- | --- |
| REST | يستدعي SDK فعلياً، لكنه يحتاج تحصيناً | يؤكد ويضيف غياب الاختبارات | `server.go` يستدعي `Ledger/Reporting/Events` | متفقان |
| gRPC | Listener فقط دون تسجيل خدمات | يؤكد ويضيف غياب `.pb.go` | `grpc.NewServer()` ثم `Serve` بلا `Register...` | متفقان |
| `/readyz` و`/metrics` | قيم ثابتة | قيم ثابتة | مؤكد | متفقان |
| `uuid.MustParse` | يسبب Panic | يطلب 400 | مؤكد | متفقان |
| الأخطاء | غالباً 500 | يؤكد | مؤكد | متفقان |
| Timeouts/Auth/TLS/Shutdown | غير موجودة | غير موجودة، مع ملاحظة أن `cmd/agy` يملك جزءاً منها | `accounting-service` لا يملكها | متفقان |
| Docker/CI | صورة Go صحيحة | يضيف تعارض Compose `postgres:15` مع CI `postgres:18.6` | مؤكد | **تقرير التحقق أوسع** |
| اختبارات الخدمة | لا يذكرها | لا توجد | `cmd/accounting-service [no test files]` | متفقان |

---

## 4. ما انفردت به كل وثيقة

### 4.1 ما انفردت به مراجعة `engine3` (يجب ضمه إلى تقرير التحقق)

1. **فجوة إنشاء `Journal` في PostgreSQL — الأخطر:**
   - `toInternalEntry` يشتق `JournalID` من `(company, SourceModule)`.
   - `PostAndSequenceContext` ينفذ `SELECT current_sequence FROM journals ... FOR UPDATE`.
   - لا يوجد `CreateJournal` في SPI ولا في الموائمات.
   - النتيجة: حتى لو أضيفت `MasterDataService` عامة كما يوصي تقرير التحقق، فإن الترحيل على PostgreSQL سيظل يفشل ما لم تُنشأ سجلات `journals` عبر Use Case عام.
   - اختبارات External Consumption تستخدم Memory، فلا تكشف هذا الشرط.

2. **عدم استخدام `JournalCode` والأبعاد و`Payload`:**
   - `PublicEventAccountMapping.JournalCode` موجود في العقد وفي SQL، لكنه لا ينتقل إلى `PostingRule`.
   - `Dimensions` و`Payload` لا يؤثران في القيد.
   - اختيار Mapping مختلف حسب `LedgerID` لا يضمن دفتراً مختلفاً فعلياً.

3. **خطأ Memory Balance Sheet:**
   - `GetBalanceSheet` يستدعي `GetIncomeStatement` ثم يتجاهل الخطأ:
     ```go
     is, err := d.GetIncomeStatement(...)
     if err == nil && !is.NetIncome.Amount().IsZero() { ... }
     ```
   - النتيجة: تقرير مالي ناقص يُعاد بنجاح. تقرير التحقق يصف تقارير Memory بأنها «فعلية» دون هذا التحفظ.

4. **التبعية المعكوسة بين Storage Adapter وSDK:**
   - `storage/postgres/public_events.go` و`storage/memory/public_events.go` يستوردان `pkg/sdk`.
   - هذا يخالف اتجاه الاعتماد: Adapter يجب أن يعتمد على Port/Contract لا على طبقة التطبيق.

5. **تفاصيل سياسة فشل الأحداث:**
   - `FailPublicEventIngestion` يتجاهل `reason`.
   - المفتاح `failed` لا يُعاد حجزه، والطلب اللاحق يحصل على «حالة غير صالحة» بدل Retry واضح.

6. **المدخلان العامان:**
   - الجذر `accounting_engine` ما زال يعرّف `StorageDriver/StoragePorts/AccountingEngine` ويكشف أنواعاً داخلية، بالتوازي مع `pkg/sdk`.

### 4.2 ما انفرد به تقرير تحقق `v1` (يجب الإبقاء عليه)

1. **أدلة تنفيذ قابلة للتكرار:** `go build`، `go vet`، `go test ./...`، وPhase C 5/5.
2. **مصفوفة `gapred`:** 7 حزم حمراء، وتوضيح أن CI لا يشغّل الوسم.
3. **بوابات الجودة:** `gofmt` (9 ملفات) وفشل `check_public_api_purity.sh`، وأن CI لا يشغّل سكربت النقاء.
4. **إطار شروط `v1` الثمانية** وحكم صريح: 2 مستوفيان مع تحفظات، 1 جزئي، 5 غير مستوفاة.
5. **دليل المستهلك الخارجي:** لا `go.mod` منفصل، والاختبار يستورد `internal/masterdata`، ومحاولة التهيئة العامة تفشل.
6. **تفصيل أنواع المعرفات والحقول الخاطئة** على مستوى الملف والسطر.
7. **فجوة Round-trip Mapping:** `SourceModule` يُفقد، ولا توجد حقول عملة/سعر/بصمة اعتماد على مستوى القيد الداخلي.
8. **Builder لا يفشل مبكراً عند غياب Reporting/Governance:** يعيد خدمة معطلة ثم يفشل عند أول استدعاء.
9. **تفاصيل الخدمة:** لا `.pb.go`، لا اختبارات خدمة، تعارض إصدار PostgreSQL بين Compose وCI، و`cmd/agy` جزئي.
10. **جودة اختبارات ACID:** المستهلك الخارجي غير مستقل، القطاعات ليست قطاعية، والبنية التحتية Memory فقط.

---

## 5. نقاط الاتفاق المؤكدة بالكود

هذه النقاط تؤكدها الوثيقتان معاً، وأعدنا التحقق منها:

1. `PostEntry` يمر عبر `posting.Engine` عندما يكون السائق الرسمي منفذاً للعقود الداخلية، لكن هناك `fallback` إلى `store.PostEntry`.
2. `ReverseEntry` يستخدم `ReverseDualContext`، والعكس الذري موجود للسائقين الرسميين.
3. تقارير PostgreSQL تستخدم محرك `internal/reporting` الفعلي.
4. تقارير Memory تحسب من القيود الفعلية، لكنها لا تحوّل العملات فعلياً وتتجاهل خطأ قائمة الدخل في الميزانية.
5. جدول `public_event_ingestions` موجود بمفتاح فريد `(company_id, idempotency_key)`.
6. وحدات المعايير تسجل قواعد عبر `events.RuleRegistrar` typed، وBuilder يفحص التكرار والاعتماديات.
7. `pkg/sdk` يستورد `internal/masterdata` و`internal/posting` و`internal/types`، أي أنه يخالف سياسة النقاء المعلنة.
8. `gRPC` لا يسجل خدمات، و`/readyz` و`/metrics` ثابتان، و`uuid.MustParse` قد يسبب Panic.
9. لا يوجد `CreateJournal` عام، و`JournalID` يُشتق من `company + SourceModule`.
10. `EvaluateAndConstructEntry` العامة ما زالت تنشئ حسابات عشوائية وتضع حالة `posted`.

---

## 6. تعارضات ظاهرية وتصحيحات مقترحة

### 6.1 تعارض حكم الشرط الرابع

تقرير التحقق يعتبر الشرط `#4` «مستوفى مع تحفظات»، لأن الموائمات لم تعد ترجع تقارير صفرية أو فترات مصطنعة. لكن:

- Memory Balance Sheet يتجاهل خطأ قائمة الدخل.
- Memory Reporting لا يحوّل العملات.
- `EvaluateAndConstructEntry` العامة ما زالت موجودة وتخمّن الحسابات.

**التصحيح المقترح:** خفض الحكم إلى **«جزئي»**، مع الإبقاء على أن إزالة Stub الأساسية تمت.

### 6.2 تعارض «نجاح `go test ./...`»

تقرير التحقق يقول إن الاختبارات تنجح. إعادة التشغيل عند `HEAD a58ebe6` تؤكد ذلك في تشغيل نظيف. لكن اختبار `TestPostgresHeavyStress50000Moves` حساس للحمل: في تشغيل متوازٍ تحت ضغط إضافي فشل بحد أدنى `437 entries/sec` مقابل `500`، ثم نجح في تشغيل HEAD النظيف.

**التصحيح المقترح:** لا تُستخدم نتيجة الاختبار الضخم كبوابة مطلقة في CI دون تثبيت البيئة أو فصل الاختبار خلف وسم أداء.

### 6.3 مخرجات سكربت النقاء

`scripts/check_public_api_purity.sh` يطبع كل imports الحزمة لأن شرط `*"internal"*` يطابق السطر كاملاً، لا اسم الحزمة فقط. الفشل حقيقي، لكن المخرجات توحي بأن كل الاستيرادات مخالفة.

**التصحيح المقترح:** فحص كل import منفرداً، أو استخدام `go list -f '{{join .Imports "\n"}}'` والتحقق من `*/internal/*`.

### 6.4 «كل الاختبارات خضراء» مقابل `gapred`

`go test ./...` ينجح لأنه لا يشغّل وسم `gapred`. تشغيل `-tags gapred` يفشل في 7 حزم. هذا ليس تعارضاً إذا فُهم أن «الاختبارات الافتراضية» فقط هي الخضراء.

**التصحيح المقترح:** توضيح ذلك في أي تقرير جاهزية، وتشغيل `gapred` في CI أو تحويل الفجوات المعروفة إلى `Skip` موثق.

### 6.5 اختلاف النسخة المفحوصة

مراجعة `engine3` لا تتضمن نتائج تشغيل، وتقرير التحقق يخص `HEAD a58ebe6`. لذلك لا يصح استخدام نتائج التحقق كدليل على نجاح `engine3.zip` نفسها، ولا يصح اعتبار كل فرق بين الوثيقتين تراجعاً أو تحسناً في الكود.

---

## 7. مصفوفة شروط `v1` بعد الدمج

| # | الشرط | مراجعة `engine3` | تقرير `v1` | الحكم المدمج المقترح |
| --- | --- | --- | --- | --- |
| 1 | مستهلك خارجي من Module منفصل دون `internal/*` | غير مستوفى | غير مستوفى | ❌ غير مستوفى |
| 2 | كل عمليات Ledger عبر Application Layer ومحرك Posting | جزئي | جزئي | ⚠️ جزئي |
| 3 | ذرية الترحيل والعكس وIdempotency مع PostgreSQL | غير مستوفى | غير مستوفى | ❌ غير مستوفى |
| 4 | لا Stub ولا Master Data افتراضية | لم يصغه كشرط | مستوفى مع تحفظات | ⚠️ جزئي (بسبب Memory BS والعملات والدالة العامة) |
| 5 | Contracts typed وفشل واضح عند نقص الاعتماديات | جزئي | مستوفى مع تحفظات | ✅ مع تحفظات |
| 6 | اختبار قطاعين أو ثلاثة يثبت الأثر المحاسبي | غير مستوفى | غير مستوفى | ❌ غير مستوفى |
| 7 | تبديل Memory/PostgreSQL دون تغيير كود المستهلك | غير مستوفى | غير مستوفى | ❌ غير مستوفى |
| 8 | Docker/service/readiness/gRPC مختبرة | غير مستوفى | غير مستوفى | ❌ غير مستوفى |

**النتيجة المدمجة:** شرط واحد مستوفى مع تحفظات (#5)، شرطان جزئيان (#2 و#4)، وخمسة غير مستوفاة (#1، #3، #6، #7، #8). هذا أقرب إلى 1/2/5 من 2/1/5.

---

## 8. الأولويات المدمجة المقترحة

### P0 — تمنع `v1`

1. **Master Data عام + Journal provisioning:**
   - `pkg/spi/masterdata` أو `pkg/api/v1/masterdata`.
   - `CreateJournal` و`CreateFiscalYear/Period` و`CreateCompany/Account` بعقود عامة.
   - مستهلك خارجي فعلي بـ `go.mod` مستقل، ومنع `internal/*`.

2. **فرض مسار Posting الوحيد:**
   - إزالة `fallback` إلى `store.PostEntry` أو تحويله إلى `ErrCapabilityNotImplemented`.
   - ربط `SetPolicyUnit` و`SetApprovalGate` و`SetFinancialDimensionsEngine` و`SetObservability` و`SetMakerCheckerPolicy`.
   - فصل Maker/Checker وعدم استخدام `CreatedBy` كـ `posterID`.

3. **Idempotency ذرية وقابلة للاسترداد:**
   - معاملة واحدة أو State Machine/Outbox مع lease وattempt count وlast error وreconciliation.
   - إرجاع خطأ `Complete` بوضوح، وحفظ `reason`، وسياسة Retry للفشل.
   - بصمة تشمل `EventID` و`EffectiveDate` و`Dimensions` و`Payload`.
   - اختبارات PostgreSQL متزامنة وتعارض حمولة وإعادة تشغيل.

4. **بناء الحدث:**
   - `ExchangeRateSPI` وتحويل فعلي إلى العملة الوظيفية.
   - تمرير `JournalCode` و`Dimensions` و`Payload` أو رفضها صراحةً.
   - حذف/تعطيل `events.EvaluateAndConstructEntry` العامة.

5. **إغلاق `gapred`:** `GAP-PST-01/02/03/04/05/06`، `GAP-GOV-01/03`، و`GAP-STD/AST` ذات الصلة.

6. **Adapters قطاعية:** Retail/Agriculture/SaaS، مع اختبارات حسابات ومبالغ وتوازن وأثر محاسبي.

### P1 — قبل تثبيت العقد

1. أنواع معرفات مستقلة (`CompanyID/AccountID/FiscalYearID/FiscalPeriodID/JournalEntryID/JournalEntryLineID` وحقول الأبعاد).
2. Round-trip كامل لكل الحقول العامة.
3. SPI على أساس Unit of Work/Transaction Manager وIdempotency Repository وApproval/Policy Ports.
4. Builder يفشل عند غياب Reporting/Governance بدل خدمة معطلة.
5. إزالة تسرب `internal/*` من الجذر أو إعلانه Legacy.
6. إصلاح `gofmt` وتشغيل النقاء و`gapred` في CI.
7. إصلاح Memory Balance Sheet ليرجع خطأ قائمة الدخل.
8. نقل عقود الأحداث من `pkg/sdk` إلى `pkg/spi/events` لكسر التبعية المعكوسة.

### P2 — الخدمة والتوزيع

1. توليد gRPC من `proto/accounting/v1/*.proto` وتسجيل الخدمات.
2. `/readyz` يفحص التخزين والمigrations والقدرات.
3. `/metrics` بمقاييس Posting/Events/Storage الفعلية.
4. `uuid.Parse` مع 400، وحدود جسم، وtimeouts، وgraceful shutdown، ومصادقة/TLS.
5. اختبارات خدمة وDocker/Compose في CI، وتوحيد PostgreSQL `18.6`.

---

## 9. الخلاصة

- **لا تعارض جوهري بين الوثيقتين:** كلتاهما ترفض `v1` وتشترط إغلاق Master Data وIdempotency ومسار Posting قبل الإعلان.
- **تقرير التحقق هو الأساس الرسمي:** لأنه يملك أدلة تنفيذ وبوابات جودة ومصفوفة شروط.
- **مراجعة `engine3` تضيف فجوات حرجة يجب دمجها:** أهمها `Journal` في PostgreSQL، و`JournalCode/Dimensions/Payload`، وخطأ Memory Balance Sheet، والتبعية المعكوسة لـ SDK.
- **التعديل الوحيد المقترح على تقرير التحقق:** الشرط `#4` يجب أن يكون «جزئياً» لا «مستوفى».
- **الأولوية القصوى:** عقد Master Data عام يشمل `CreateJournal`، ثم فرض Posting الوحيد، ثم Idempotency الذرية، ثم بناء الحدث والعملات، ثم `gapred` والقطاعات.

---

## 10. ملحق: أدلة إعادة التحقق عند `HEAD a58ebe6`

| الأمر | النتيجة |
| --- | --- |
| `go build ./...` | ✅ نجاح |
| `go vet ./...` | ✅ نجاح |
| `go test ./... -count=1` | ✅ نجاح في تشغيل نظيف (اختبار الضغط حساس للحمل) |
| `go test ./storage/postgres -run TestPhaseC -v` | ✅ 5/5 نجاح |
| `go test ./test/acid -count=1` | ✅ 3/3 نجاح |
| `go test -tags gapred ./... -count=1` | ❌ 7 حزم: `internal/governance`، `internal/ias12`، `internal/posting`، `internal/reporting` (build failed)، `internal/subledgers`، `storage/memory`، `storage/postgres` |
| `gofmt -l .` | ❌ 9 ملفات غير منسقة |
| `scripts/check_public_api_purity.sh` | ❌ `pkg/sdk` يستورد `internal/masterdata` و`internal/posting` و`internal/types` |
| `go test ./modules/...` | ⚠️ `[no test files]` لكل الوحدات |
| بناء مستهلك خارجي بسيط (SDK + Memory) | ✅ نجح |
| محاولة تهيئة Master Data من خارج الوحدة | ❌ `cannot use ... as masterdata.Company value` |
| `find . -name '*.pb.go'` | لا نتائج |
| `find . -name go.mod` | ملف واحد فقط في الجذر |

**ملاحظة:** نتائج هذا الملف تخص `HEAD a58ebe6`. أي تعديل لاحق على العقود أو مسار SDK أو الخدمة يستوجب إعادة تشغيل الأوامر أعلاه وتحديث الحكم.
