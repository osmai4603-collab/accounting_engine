# مراجعة معمارية النسخة engine2 — 12 نوفمبر 2026

## 1. الحكم التنفيذي

النسخة `engine2.zip` أفضل من `engine.zip` في تأسيس طبقة المنتج: أصبحت عقود `pkg/api/v1` وواجهات `pkg/spi` وواجهات `pkg/sdk` أوضح، وأضيفت موائمات عامة الشكل إلى `storage/memory` و`storage/postgres`، كما أصبحت خدمة HTTP تستدعي واجهات SDK بدل الاستجابات الثابتة السابقة.

لكن النسخة **ليست بعد Universal Accounting Engine قابلًا للاستخدام الإنتاجي من أنظمة خارجية**. السبب الحاسم أن SDK ما زال يمرر عمليات المحاسبة إلى مخازن البيانات مباشرة، ولا يربطها بمحرك الأعمال المحاسبي الأصلي `internal/posting.Engine` أو بخدمات المحرك الموجودة في `engine.go`. لذلك لدينا الآن واجهة عامة وموائمات، لكن قواعد التحقق والترحيل والعكس والتقارير لم تُجمع بعد في مسار واحد موثوق.

**التقييم العام: تحسن بنيوي واضح، مع بقاء فجوات حرجة في Accounting Correctness وApplication Architecture وProduction Readiness. لا أوصي بإعلان v1 أو استخدام المسار الجديد لترحيل قيود حقيقية قبل إغلاق P0 أدناه.**

## 2. ما تحسن مقارنة بالنسخة السابقة

1. أضيفت حزم `pkg/api/v1` للعقود العامة، ولم يظهر فيها استيراد مباشر من `internal` عند فحص imports.
2. أصبح `pkg/sdk` يعرّف واجهة `Engine` وخدمات Ledger وReporting وGovernance وEvents.
3. أضيفت واجهات تخزين مستقلة في `pkg/spi/storage` وواجهة لوحدات المعايير في `pkg/spi/standards`.
4. أضيفت ملفات `storage/memory/spi_adapter.go` و`storage/postgres/spi_adapter.go`، وأصبحت الموائمات تتعامل مع DTOs العامة بدل إجبار المستهلك على تمرير أنواع `internal` مباشرة.
5. اختبارات `test/acid` أصبحت تستخدم `storage/memory.Driver` الفعلي بدل بعض Fake Stores المحلية السابقة.
6. تحولت بعض Handlers من استجابات Stub ثابتة إلى استدعاء SDK فعليًا.

هذه خطوات صحيحة ويجب البناء عليها، لا التراجع عنها. لكنها تثبت وجود واجهات وموائمات، ولا تثبت بعد أن المحرك المحاسبي الفعلي يعمل من خلالها.

## 3. الفجوة المركزية: مساران محاسبيان منفصلان

### المسار القديم

الجذر `engine.go` يركّب الخدمات الداخلية، ومنها:

- `internal/posting.Engine`
- `internal/masterdata.Service`
- `internal/reporting.Engine`
- `internal/governance.Engine`
- `internal/subledgers.Service`
- `internal/valuation.Engine`
- وحدات السياسات والمعايير والمراقبة.

ويحتوي `internal/posting.Engine` على مسار إنشاء المسودة والتحقق من صحتها والتحضير للترحيل وتطبيق سياسات الشركة والموافقة وMaker-Checker ثم الترحيل الذري.

### المسار الجديد

`pkg/sdk/builder.go` ينشئ `engineInstance` يحتفظ بمخازن SPI فقط. ثم:

- `Ledger().PostEntry` يستدعي `store.PostEntry` مباشرة.
- `Ledger().ReverseEntry` ينشئ قيدًا عكسيًا بنفسه ثم يستدعي المخزن مباشرة.
- `Reporting()` يستدعي `ReportingStore` مباشرة.
- `Events()` ينشئ خدمة أحداث مستقلة تستدعي المخزن مباشرة.

لا يوجد في هذا المسار ربط فعلي بـ `internal/posting.Engine` أو `internal/reporting.Engine` أو خدمات المحرك الأصلي.

**النتيجة:** واجهة SDK لا تمثل حاليًا Facade على المحرك الأصلي؛ إنها طبقة تشغيل ثانية مبنية فوق واجهات تخزين جديدة. يجب توحيد هذين المسارين قبل توسيع API أو إعلان المنتج.

## 4. النتائج الحرجة P0

### P0-1 — PostEntry يتجاوز Posting Engine

**المواضع:**

- `pkg/sdk/builder.go`، الدالة `defaultLedgerService.PostEntry`.
- `storage/memory/spi_adapter.go`، الدالة `Driver.PostEntry`.
- `storage/postgres/spi_adapter.go`، الدالة `Driver.PostEntry`.
- `internal/posting/engine.go`، الدوال `CreateDraftContext` و`PostEntryContext` و`prepareForPosting`.

SDK يرسل القيد مباشرة إلى `CoreLedgerStore.PostEntry`. وموائمات التخزين تنشئ Draft ثم تستدعي `SaveDraftContext` و`PostAtomicContext` مباشرة. هذا لا يعادل المرور عبر `posting.Engine.PostEntryContext`، الذي ينفذ قواعد التحقق والسياسات والتحضير للترحيل، بما فيها صلاحية الحالة، والتحقق من بيانات الشركة والفترة، والموافقات وسياسات العملة والأبعاد وغيرها بحسب الإعداد.

التحقق من تساوي مجموع Debit وCredit داخل موائم التخزين لا يكفي كتحقق محاسبي. توازن القيد شرط ضروري، لكنه لا يثبت صحة الحسابات والفترة والعملة والصلاحيات وسياسات الترحيل.

**الإجراء:** اجعل SDK يستدعي Application Service واحدة داخلية. هذه الخدمة تنشئ المسودات وتتحقق منها وتستدعي `posting.Engine`، بينما تظل موائمات التخزين مسؤولة عن persistence وtransaction boundaries فقط. يجب ألا يصبح `PostEntry` في Storage Port بديلًا عن حالة الاستخدام المحاسبية.

### P0-2 — مسار العكس لا يحقق دورة عكس محاسبية كاملة

**الموضع:** `pkg/sdk/builder.go`، `defaultLedgerService.ReverseEntry`.

التحسين عن النسخة السابقة أن المبالغ في السطور تُبدّل بين المدين والدائن. لكن التنفيذ ما زال غير مكتمل:

- وسيط `reason` لا يُستخدم ولا يُحفظ.
- لا تُحدّث حالة القيد الأصلي إلى Reversed.
- لا يمنع عكس القيد أكثر من مرة.
- لا يظهر تنفيذ ذري يربط إنشاء قيد العكس بتغيير حالة الأصل.
- لا يظهر التحقق من أن القيد الأصلي Posted أو أن الفترة وتاريخ العكس صالحان.
- التاريخ يضبط إلى `time.Now()` مباشرة بدل تاريخ عكس صريح/سياسة موثقة.
- لا يمر العكس عبر `internal/posting.Engine`، رغم وجود مسار Reverse داخله.

كما أن `EntryLine.ID` من نوع `AccountID` في Public API، لذلك يستخدم التنفيذ النوع الدلالي الخاطئ عند توليد معرّف سطر العكس.

**الإجراء:** اجعل العكس Use Case في Application Layer، ويستدعي مسار العكس الأصلي (`posting.Engine`) ضمن transaction boundary واحدة، مع سبب العكس، وربط الأصل، ومنع التكرار.

### P0-3 — ProcessEvent لا ينتج قيدًا محاسبيًا موثوقًا

**المواضع:**

- `pkg/sdk/builder.go`، `defaultEventService.ProcessEvent`.
- `pkg/api/v1/events/rules.go`، `EvaluateAndConstructEntry`.

المشكلات:

1. لا يجري حل `DebitAccountCode` و`CreditAccountCode` إلى حسابات حقيقية. بل ينشئ `AccountID` عشوائيًا لكل سطر.
2. لا يحدد `FiscalPeriodID`، ولا يستدعي Master Data للتحقق من الحسابات والفترة.
3. يضع `ExchangeRate = 1` دائمًا، ولا يميز بين العملة المعاملة والوظيفية على نحو صحيح.
4. لا يتحقق من أن المبلغ موجب وغير صفري أو أن نوع الحدث صالح.
5. لا يستخدم `Payload` أو `Dimensions` في mapping محاسبي محدد، ولا يطبق سياسة قطاع أو سياسة معيار فعلية.
6. عند عدم العثور على قاعدة يستخدم قاعدة افتراضية 1000/4000. في محرك محاسبي عام، غياب قاعدة الحدث يجب أن يؤدي إلى رفض واضح أو وضع الحدث في حالة غير معالجة، لا إنشاء قيد افتراضي قد يكون خاطئًا.
7. وصف القيد يأخذ `DescriptionTemplate` كنص حرفي، وليس قالبًا يملأ بيانات الحدث.

والأخطر أن موائم الذاكرة يقوم بإنشاء حسابات افتراضية تلقائيًا بمعرّفات الحسابات العشوائية، فيُخفي عدم وجود mapping صحيح للحسابات.

**الإجراء:** اجعل الأنظمة القطاعية تنتج `AccountingCommand` أو `CanonicalAccountingEvent` ذا معنى محاسبي واضح. ويجب أن يحول Application Layer هذا الأمر إلى Journal Draft عبر حسابات/سياسات معرّفة للشركة. لا تجعل المحرك يخمّن الحساب المقابل أو ينشئ حسابات عشوائية.

### P0-4 — Idempotency غير موثوقة

**الموضع:** `pkg/sdk/builder.go`، `engineInstance.Events` و`defaultEventService.ProcessEvent`.

`Engine.Events()` ينشئ كائن `defaultEventService` جديدًا في كل استدعاء. وهذا يعني أن `keys` و`mu` يُعاد إنشاؤهما كل مرة. وHandler الأحداث يستدعي `s.engine.Events().ProcessEvent` لكل طلب؛ لذلك لن يرى الطلب التالي مفتاح Idempotency المحفوظ في الطلب السابق.

حتى عند الاحتفاظ بنفس Event Service، توجد نافذة سباق: يتم فحص المفتاح ثم تحرير القفل قبل PostEntry، ما يسمح لطلبين متزامنين بالمفتاح نفسه بترحيل قيدين. كما أن الخريطة غير دائمة وتضيع عند إعادة تشغيل العملية.

**الإجراء:** يجب أن تكون Idempotency دائمة وذرية في نفس transaction boundary التي ترحّل القيد. استخدم Unique Constraint على المفتاح ضمن نطاق الشركة/المصدر، مع تخزين نتيجة المعالجة وإعادة النتيجة السابقة عند التكرار.

### P0-5 — موائم PostgreSQL والذاكرة لا يقدمان بيانات Master Data / Reporting موثوقة

**المواضع:**

- `storage/postgres/spi_adapter.go`.
- `storage/memory/spi_adapter.go`.

في Postgres Adapter:

- `GetFiscalPeriod` يرجع فترة مصطنعة من سنة قبل اليوم إلى سنة بعده، وحالتها Open دائمًا.
- `GetTrialBalance` يرجع Trial Balance فارغًا ومتوازنًا دائمًا.
- `GetGeneralLedger` و`GetBalanceSheet` و`GetIncomeStatement` ترجع تقارير صفرية/فارغة دون قراءة البيانات.
- `GetApprovalFingerprint` يرجع نصًا مشتقًا من ID، لا fingerprint مشتقًا من موافقة حقيقية.

في Memory Adapter:

- `PostEntry` ينشئ Company وPeriod وAccounts افتراضية عند عدم وجودها بدل رفض مرجع غير صالح.
- `GetFiscalPeriod` يفرض `PeriodOpen` و`FiscalPeriodTypeNormal` بدل إظهار الحالة والنوع الفعليين.
- `GetAccount` يفرض `IsActive: true` بدل نقل الحالة الفعلية.
- `GetGeneralLedger` و`GetBalanceSheet` و`GetIncomeStatement` ترجع نتائج صفرية/فارغة. Trial Balance محدود ولا يثبت احتسابًا كاملًا حسب الفترة والأرصدة الافتتاحية والختامية.
- Audit/Fingerprint في الذاكرة قيم تجريبية وليست بديلًا لآلية الحوكمة الأصلية.

**الإجراء:** لا تعرض Feature باعتبارها مفعلة إذا كان Adapter يرجع بيانات فارغة أو افتراضية. نفذ كل Query Adapter من البيانات الفعلية أو أعد خطأ `ErrCapabilityNotImplemented` صريحًا. يجب ألا يعيد تقرير مالي `IsBalanced=true` لمجرد أن التنفيذ Stub.

### P0-6 — وحدات المعايير لا تسجل قواعدها في Router الذي يستخدمه SDK

**المواضع:**

- `pkg/sdk/builder.go`، إنشاء `events.NewRouter()` واستدعاء `RegisterRules`.
- `modules/ifrs15/module.go` ونظائرها في `ias12` و`ifrs16` و`ifrs9`.
- `pkg/api/v1/events/rules.go`.
- `internal/events/*`.

SDK يستعمل `accounting_engine/pkg/api/v1/events.Router`، بينما وحدات المعايير تجري Type Assertion إلى `*accounting_engine/internal/events.Router`. هذان نوعان مختلفان. لذلك Type Assertion يفشل، ولا تسجل القاعدة. ثم يرجع Module بـ `nil`، فيبدو التسجيل ناجحًا رغم عدم حدوثه.

وفوق ذلك، Builder يتجاهل الخطأ الذي قد ترجعه `RegisterRules` عبر `_ = mod.RegisterRules(router)`، ولا يستدعي `RunScheduledJobs` أصلًا. واختبار Industry Independence يمر حتى مع فشل التسجيل لأنه يقبل القاعدة الافتراضية ولا يتحقق من حسابات أو سطور أو أثر القاعدة.

**الإجراء:** عرّف عقدًا عامًا typed مثل `events.RuleRegistrar` داخل `pkg/api/v1` أو SPI إصدارية. لا تستخدم `interface{}` ولا type assertions على أنواع داخلية، ولا تتجاهل أخطاء التسجيل. أضف اختبارًا يثبت أن IFRS15 مثلًا سجل القاعدة الصحيحة وأن مخرجاتها تستخدم حسابات فعليّة معتمدة.

## 5. نتائج عالية الأولوية P1

### P1-1 — Public API تحسن، لكن نموذج الأنواع يحتاج تصحيحًا قبل تثبيت v1

- `CompanyID`, `AccountID`, `FiscalPeriodID`, `JournalEntryID` كلها Aliases لـ `uuid.UUID`؛ لا توفر تمييزًا نوعيًا.
- لا يوجد `FiscalYearID` مستقل.
- `FiscalYear.ID` معرف كـ `CompanyID`، و`FiscalPeriod.FiscalYearID` أيضًا `CompanyID`.
- `EntryLine.ID` معرف كـ `AccountID` بدل `JournalEntryLineID`.
- معرفات Partner/CostCenter/Project/Segment/BusinessUnit ممثلة بـ `*CompanyID`.
- بعض حقول القيد والسطور لا تُنقل من Public DTO إلى Posting Model أو بالعكس في mapping داخل SPI adapters؛ منها حقول وصف/مصدر/مرجع/موافقة وأبعاد وتحليل وتسوية. هذا يجعل round-trip غير محافظ على البيانات.

صحح النموذج والـ Mapping الآن قبل أن يعتمد عليه مستهلك خارجي؛ تغيير دلالات حقول v1 بعد النشر مكلف.

### P1-2 — عقود SPI غير كافية لتمثيل حدود المعاملة وقواعد المحرك

`CoreLedgerStore` يقدم `PostEntry` كعملية Storage مباشرة، ولا يعرّف Unit of Work/Transaction Scope ولا حدود حفظ المسودة ثم الموافقة ثم الترحيل ولا Idempotency Store. هذا يدفع SDK إلى تفويض عمل محاسبي إلى المخزن.

صمم Ports على أساس متطلبات Application/Domain Layer: Transaction Manager، Draft/Journal Repository، Account/Fiscal Period Readers، Approval/Policy Ports، Idempotency Repository، Reporting Data Readers. لا تجعل Persistence Port مسؤولة عن قرار الترحيل.

### P1-3 — Builder لا يفرض تهيئة الميزات الاختيارية على نحو صحيح

- `NewBuilder` يتجاهل أخطاء Option عبر `_ = opt(b)`.
- يمكن إضافة Module nil ثم يحدث panic عند `mod.Name()`.
- `RegisterRules` errors متجاهلة.
- `Reporting()` و`Governance()` يعيدان Services حتى عندما لم تُوفّر مخازنهما؛ يتأخر الخطأ إلى أول استدعاء بدل توضيح حالة Capability عند Build.
- لا توجد تعريفات Requires/Provides أو Module Version أو Dependency Graph.
- لا توجد حماية من تكرار Module أو تعارض قواعدها.

استخدم Module Registry typed مع capabilities واعتماديات وإصدارات واضحة. عند طلب تفعيل Reporting دون Reporting Sources يجب أن يفشل Build برسالة دقيقة، أو لا تُعرض Capability ضمن الواجهة أصلًا.

### P1-4 — اختبارات ACID الثلاثة لا تثبت الاختبارات المعمارية المطلوبة

**External Module Consumption:** `test/acid/external_consumption_test.go` موجود داخل نفس Go Module ويستورد `accounting_engine/...`. هذا ليس مستهلكًا خارجيًا مستقلًا ولا يختبر حزمًا من مشروع ذي `go.mod` منفصل. كذلك `module accounting_engine` ليس مسار نشر رسميًا قابلًا للاعتماد؛ يجب حسم المسار الحقيقي للمستودع قبل اختبار `go get`.

**Industry Independence:** الاختبار يرسل أسماء EventType مختلفة إلى نفس منشئ القيد العام. لا توجد Adapters قطاعية مستقلة تنتج Canonical Accounting Commands، ولا تتحقق الاختبارات من صحة القيود. كما أن قواعد IFRS لا تُسجل بسبب اختلاف Router.

**Infrastructure Independence:** الاختبار يستخدم Memory فقط. لا توجد مقارنة سلوكية لنفس سيناريو الحسابات بين Memory وPostgreSQL، والاختبار يتجاهل أخطاء الترحيل داخل goroutines، ثم يتحقق من CompanyID في تقرير لا يثبت صحة الأرصدة.

حوّل الاختبارات إلى Fixtures مستقلة:

1. مشروع خارجي فعلي يملك `go.mod` خاصًا ويستورد فقط Public API وSDK وDriver منشورًا.
2. ثلاثة Adapters قطاعية منفصلة تولد نفس العقد المحاسبي الموحد، ثم assertions على القيود والحسابات والمدين/الدائن.
3. نفس سيناريو Posting/Reverse/Trial Balance يُنفذ على Memory حقيقي وPostgres حقيقي مع فحص الأرصدة والذرية والتزامن وإعادة المحاولة.

### P1-5 — Root Package ما زال يكشف تفاصيل داخلية

`engine.go` في جذر الوحدة يعرّف `AccountingEngine` و`Config` وواجهات تخزين عامة، لكنه يستورد ويكشف أنواعًا من `internal/*` في الحقول والتكوين والواجهات. Script `scripts/check_public_api_purity.sh` يفحص `./pkg/...` فقط، ولذلك لا يكتشف تسرب الواجهة الجذرية أو اعتماديات وحدات `modules` على `internal/events`.

إذا كان `pkg/sdk` هو Public Product API المقصود، فإما أن تحول Root Facade إلى واجهة توافقية تعتمد عليه ولا تكشف أي نوع داخلي، أو تعلنها Legacy/غير مخصصة للمستهلك الخارجي. وسّع فحص النقاء ليشمل كل الحزم العامة، ولا تكتفِ بفحص `pkg/`.

## 6. خدمة Microservice: تحسن الربط، لكنها ليست جاهزة

### ما تحسن

HTTP Handlers أصبحت تستدعي `engine.Ledger().PostEntry` و`engine.Reporting().GetTrialBalance` و`engine.Events().ProcessEvent` بدل بعض الاستجابات الثابتة السابقة.

### ما بقي

1. `cmd/accounting-service/main.go` ينشئ `memory.New()` دائمًا، ولا يستخدم `cfg.Database` لاختيار PostgreSQL. رغم أن `docker-compose.yml` يمرر `DATABASE_DSN` ويبدأ PostgreSQL، الخدمة لا تتصل به؛ البيانات تبقى في الذاكرة وتضيع عند إعادة التشغيل.
2. `StartGRPCServer` في `grpc_server.go` Stub يرجع `nil` فقط؛ لا يوجد gRPC Server فعلي رغم وجود Port وProto.
3. Dockerfile يستخدم `golang:1.22-alpine` بينما `go.mod` يطلب `go 1.26.11`؛ صورة البناء غير متوافقة مع إصدار الوحدة.
4. `GetTrialBalance` في PostgreSQL يرجع تقريرًا فارغًا ومتوازنًا دائمًا، لذا HTTP endpoint ليس تقريرًا ماليًا فعليًا.
5. `/readyz` يعيد READY دون التحقق من اتصال التخزين أو تهيئة الميزات.
6. `/metrics` يعيد Gauge ثابتًا وليس مقاييس Posting/Failure/Latency فعلية.
7. `uuid.MustParse` في Handler قد يسبب panic لمدخل query غير صالح بدل إرجاع 400.
8. لا تظهر إعدادات تشغيلية كافية مثل graceful shutdown وHTTP timeouts وحدود body وauthentication/authorization.

**التوصية:** أجّل اعتماد Microservice كمنتج جاهز. اجعل المكتبة هي المنتج الأساسي، ثم ابنِ REST/gRPC Adapters فوق نفس Application Services بعد ثباتها. أصلح Docker وDSN والجاهزية قبل تقديمها كتوزيعة تشغيلية.

## 7. القرارات المعمارية التي أوصي بحسمها

| القرار | التوصية |
| --- | --- |
| طريقة الاستخدام | Go Library هي المنتج الأساسي؛ Microservice توزيع اختياري لاحقًا |
| استقبال العمليات | Use Case Commands للقيود، وCanonical Accounting Events عند وجود تكامل بالأحداث؛ لا تقبل أحداثًا تشغيلية غير محولة وتخمن القيد |
| تركيب الوحدات | Core Ledger إلزامي؛ Reporting/Governance/Subledgers/Valuation/Standards وحدات اختيارية حسب Capabilities واعتماديات معلنة |
| المحاسبة والمعايير | Core يحتوي القواعد العامة؛ وحدات IAS/IFRS تضيف سياسات/حسابات معيارية typed دون فرضها على كل مشروع |
| تكامل القطاعات | Adapters خارج المحرك التجاري/الزراعي/الصناعي تحول عمليات القطاع إلى Accounting Commands |
| التخزين | Ports للتخزين والمعاملات والقراءة، مستقلة عن Integration API؛ لا تكشف SQL ولا تجعل Store هو Posting Use Case |
| الإصدارات | API وSPI وModule Contracts بإصدارات وسياسة توافق واضحة، مع مسار Module رسمي قابل للنشر |

## 8. ترتيب التنفيذ المقترح

### المرحلة A — توحيد Application Path (P0)

1. تحديد API المنتج الوحيدة، ونقل تركيب `AccountingEngine` إلى طبقة داخلية قابلة للاستدعاء من SDK.
2. إعادة تصميم SDK بحيث يستخدم Application Services التي تستدعي Posting Engine الأصلي.
3. إعادة تصميم Ports حول متطلبات تلك الخدمات وحدود المعاملات، لا حول عمليات `PostEntry` المباشرة.
4. توحيد Money/IDs/Journal DTO mapping والتحقق من عدم فقد أي حقل.

**معيار القبول:** استدعاء `sdk.Ledger().PostEntry` ينفذ نفس قواعد `internal/posting.Engine` ويعيد نفس نتائج/أخطاء المسار الداخلي في سيناريوهات متكافئة.

### المرحلة B — عكس وأحداث وIdempotency (P0)

1. توصيل Reverse Use Case إلى `posting.Engine`.
2. وضع Idempotency في تخزين دائم مع Unique Constraint ومعاملة واحدة.
3. حذف القاعدة الافتراضية 1000/4000.
4. تعريف Canonical Accounting Event وAccount Mapping قابل للتهيئة لكل شركة/دفتر، أو تمرير Journal Draft جاهز من Adapter.

**معيار القبول:** اختبار إعادة حدث بنفس المفتاح بالتتابع وبالتوازي وبعد إعادة تشغيل الخدمة لا ينتج أكثر من قيد واحد، والعكس ينشئ قيدًا متوازنًا ويمنع العكس المكرر ويغير حالة الأصل وفق السياسة.

### المرحلة C — Storage and Reporting (P0)

1. تنفيذ `CoreLedgerStore` و`ReportingStore` و`GovernanceStore` في Postgres وMemory دون دوال placeholder.
2. إرجاع أخطاء واضحة للقدرات غير المنفذة بدل تقارير صحيحة شكليًا.
3. ربط Master Data والفترات الفعلية وعدم إنشاء حسابات/فترات افتراضية عند Posting.
4. اختبارات معزولة لحدود PostgreSQL Transaction وRLS وConcurrency وBalance Integrity.

**معيار القبول:** نفس fixtures تنتج نفس Posting وTrial Balance/GL/report outputs على Memory وPostgreSQL.

### المرحلة D — Modules & Contracts (P1)

1. استبدال `interface{}` بعقود typed عامة.
2. استدعاء Register/Initialize/Close والتحقق من أخطائها.
3. إضافة Module Version وRequires/Provides وDependency Graph.
4. اختبار كل وحدة معيارية للتسجيل والحسابات الفعلية وقواعدها؛ لا يكفي أن تقبل Build.
5. تثبيت أنواع IDs والحقول العامة قبل نشر `v1`.

### المرحلة E — إثبات قابلية إعادة الاستخدام (P1)

1. External Consumer Fixture مستقل ذو `go.mod` منفصل.
2. Sector Adapters مستقلة للتجارة والزراعة والخدمات/الصناعة.
3. Infrastructure Adapter Matrix يشمل Memory وPostgres.
4. CI ينفذ compile/test/race/integration ويمنع imports الداخلية من الحزم العامة.

### المرحلة F — Service Distribution (لاحقًا)

1. استخدام نفس SDK/Application Services في REST وgRPC.
2. تنفيذ gRPC المولّد فعليًا، لا Stub.
3. استخدام PostgreSQL من DSN، migration lifecycle وhealth/readiness حقيقية.
4. إضافة graceful shutdown وtimeouts وauthn/authz ومقاييس فعلية.
5. تحديث Docker Go version إلى إصدار متوافق مع `go.mod` وإضافة CA certificates عند الحاجة لاتصالات TLS.

## 9. شروط إعلان المنتج v1

لا أوصي بإعلان `v1` قبل تحقق جميع النقاط التالية:

- مستهلك خارجي يبني من Module منفصل دون import لأي `internal/*`.
- كل عمليات Ledger من SDK تمر عبر Application Layer ومحرك Posting الحقيقي.
- الترحيل والعكس وIdempotency ذرية ومختبرة مع PostgreSQL.
- لا توجد تقارير Stub أو Master Data افتراضية في أي Adapter يقدم Capability مفعلة.
- وحدات المعايير تستخدم Contracts typed وتفشل بوضوح عند نقص الاعتماديات.
- اختبار قطاعين أو ثلاثة يثبت صحة الأثر المحاسبي وليس مجرد قبول الأحداث.
- اختبار تبديل Memory/PostgreSQL دون تغيير كود المستهلك.
- Docker/service build وreadiness وgRPC/HTTP تم اختبارها على بيئة مطابقة للإصدار المطلوب.

## 10. حدود هذه المراجعة

هذه مراجعة Static Code Review للملفات الموجودة في `engine2.zip`، مع مقارنة الملفات المتغيرة بالنسخة السابقة `engine.zip`. لم يتم الادعاء بنجاح الاختبارات أو Build. تعذر تشغيل Go toolchain المطلوب: `go.mod` يطلب Go 1.26.11، بينما البيئة المتاحة تحاول تنزيله ولا يتوفر اتصال بمصدر التنزيل. لذلك يجب إعادة تنفيذ `go test ./...` و`go test -race ./...` واختبارات PostgreSQL Integration في CI ببيئة Go مطابقة قبل اعتماد أي نتيجة تشغيلية.

---

## الخلاصة

**هذه النسخة أصلحت جزءًا مهمًا من مشكلة واجهة المنتج وربطت SDK شكليًا بموائمات التخزين، لكنها لم تحل المشكلة المعمارية الأساسية: جعل المحرك المحاسبي الأصلي هو المنفذ الوحيد لقواعد المحاسبة.**

لا تضف مزيدًا من API أو وحدات معيارية الآن. الأولوية هي إنشاء Application Layer واحدة تربط Public SDK بـ `internal/posting.Engine` وMaster Data وGovernance وReporting، ثم تجعل موائمات PostgreSQL/Memory تنفذ Ports تلك الطبقة. عندها فقط تصبح واجهة SDK مدخلًا حقيقيًا للمحرك، لا مسارًا موازيًا له.
