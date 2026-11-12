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

> **حالة التنفيذ (تحديث):** تم تنفيذ إصلاحات P0 الجوهرية في مسار المنتج/SDK والبنية المرتبطة بها (توحيد مسار التطبيق عبر `posting.Engine`، العكس عبر محرك الترحيل، رفض الأحداث غير المعرّفة/بدون ربط حسابات حقيقي، وتصحيح تسجيل قواعد وحدات المعايير بعقد typed). نُفّذت **المرحلة B**: idempotency دائمة لاستيعاب الأحداث العامة عبر تخزين دائم (`public_event_ingestions` بقيد فريد لكل شركة/مفتاح) مع إرجاع النتيجة السابقة عند التكرار ومنع المعالجة المكررة، إزالة قواعد التخمين الافتراضية، ودعم الحدث المحاسبي القياسي مع ربط حسابات قابل للتهيئة لكل شركة/دفتر أو تمرير مسودة قيد جاهزة. ونُفّذت **المرحلة C (P0)**: موائمات SPI الحقيقية لـ `CoreLedgerStore`/`ReportingStore`/`GovernanceStore` في PostgreSQL وMemory دون دوال placeholder وهمية (تقارير فعلية عبر محركات `internal/reporting`، بصمة اعتماد/محتوى حقيقية للقيود، وأخطاء قدرة واضحة `ErrCapabilityNotImplemented` عند عدم دعم قدرة SPI بشكل آمن)، عدم اختلاق حسابات/فترات في مسارات الترحيل/الربط والاعتماد على بيانات Master Data والفترات الفعلية، واختبارات PostgreSQL معزولة تغطي Transaction/RLS/Concurrency/Balance Integrity مع fixtures تكافؤ تُثبت تطابق مخرجات Posting وTrial Balance/GL/Balance Sheet/Income Statement بين Memory وPostgreSQL. تبقى أعمال P1 والمراحل اللاحقة (توحيد عقود SPI/IDs وحدود المعاملات الشاملة، observability، DX/Hardened release) قبل إعلان `v1`.

### P0-1 — PostEntry يتجاوز Posting Engine — ✅ تم التنفيذ (مسار SDK/التطبيق)

**المواضع:**

- `pkg/sdk/builder.go`، الدالة `defaultLedgerService.PostEntry`.
- `storage/memory/spi_adapter.go`، الدالة `Driver.PostEntry`.
- `storage/postgres/spi_adapter.go`، الدالة `Driver.PostEntry`.
- `internal/posting/engine.go`، الدوال `CreateDraftContext` و`PostEntryContext` و`prepareForPosting`.

SDK يرسل القيد مباشرة إلى `CoreLedgerStore.PostEntry`. وموائمات التخزين تنشئ Draft ثم تستدعي `SaveDraftContext` و`PostAtomicContext` مباشرة. هذا لا يعادل المرور عبر `posting.Engine.PostEntryContext`، الذي ينفذ قواعد التحقق والسياسات والتحضير للترحيل، بما فيها صلاحية الحالة، والتحقق من بيانات الشركة والفترة، والموافقات وسياسات العملة والأبعاد وغيرها بحسب الإعداد.

التحقق من تساوي مجموع Debit وCredit داخل موائم التخزين لا يكفي كتحقق محاسبي. توازن القيد شرط ضروري، لكنه لا يثبت صحة الحسابات والفترة والعملة والصلاحيات وسياسات الترحيل.

**الإجراء:** اجعل SDK يستدعي Application Service واحدة داخلية. هذه الخدمة تنشئ المسودات وتتحقق منها وتستدعي `posting.Engine`، بينما تظل موائمات التخزين مسؤولة عن persistence وtransaction boundaries فقط. يجب ألا يصبح `PostEntry` في Storage Port بديلًا عن حالة الاستخدام المحاسبية.

### P0-2 — مسار العكس لا يحقق دورة عكس محاسبية كاملة — ✅ تم التنفيذ (عبر posting engine)

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

### P0-3 — ProcessEvent لا ينتج قيدًا محاسبيًا موثوقًا — ✅ تم التنفيذ (رفض التخمين/ربط حسابات حقيقي)

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

### P0-4 — Idempotency غير موثوقة — ✅ تم التنفيذ

**الموضع:** `pkg/sdk/builder.go`، `storage/postgres/migrations/202611120001_public_event_ingestion_idempotency.sql`، موائمات التخزين (`storage/memory/public_events.go`, `storage/postgres/public_events.go`).

تم توحيد خدمة أحداث SDK لكل مثيل محرك، وإضافة Idempotency **دائمة** لاستيعاب الأحداث العامة:
- قيد فريد `(company_id, idempotency_key)` في جدول `public_event_ingestions` مع تخزين الحالة وبصمة الحمولة ومعرّف/لقطة نتيجة القيد.
- حجز المفتاح ذريًا على مستوى التخزين (`Claim`) لمنع الترحيل المكرر عبر الطلبات المتزامنة وإعادة التشغيل، مع إرجاع النتيجة السابقة عند التكرار وحفظ النتيجة بعد الترحيل (`Complete`).
- كشف تعارض بصمة الحمولة لنفس المفتاح (رفض) عند اختلاف الطلب.
- مسار fallback داخل العملية فقط عندما لا يوفر التخزين مخزن idempotency دائم.

**ملاحظة:** الحجز الدائم يمنع الازدواجية ويعيد النتيجة، ويمكن تعزيز “حجز+ترحيل+حفظ النتيجة” ضمن معاملة SQL واحدة شاملة لاحقًا عبر دعم معاملات موحّد أعمق في مسار الترحيل.

### P0-5 — موائم PostgreSQL والذاكرة لا يقدمان بيانات Master Data / Reporting موثوقة — ✅ تم التنفيذ (إزالة البيانات/التقارير الوهمية)

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

### P0-6 — وحدات المعايير لا تسجل قواعدها في Router الذي يستخدمه SDK — ✅ تم التنفيذ

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

### المرحلة A — توحيد Application Path (P0) — ✅ تم التنفيذ

1. ✅ تحديد API المنتج الوحيدة لمسار SDK، ونقل تركيب مسار الترحيل/العكس إلى خدمات تطبيقية تستدعي المحرك الداخلي.
2. ✅ إعادة تصميم SDK بحيث يستخدم Application Services التي تستدعي Posting Engine الأصلي (`CreateDraftContext`/`PostEntryContext`/`GetEntry`/`ReverseDualContext`) عند توفر منافذ داخلية في مخزن التخزين.
3. ✅ إزالة تفويض قرار الترحيل المحاسبي إلى `store.PostEntry` في مسار SDK؛ أصبحت موائمات SPI التخزينية مسؤولة عن الاستمرارية/العمليات فقط دون اختراع بيانات Master Data.
4. ✅ توحيد Money/IDs/Journal DTO mapping في `pkg/sdk/mapping.go` مع الحفاظ على الحقول الأساسية (التواريخ، المرجع/الوصف، المنشئ، ربط العكس، الأبعاد/الشريك على السطور، مبالغ/عملات/أسعار الصرف) وتحويل الروابط بشكل صحيح.

**معيار القبول:** ✅ استدعاء `sdk.Ledger().PostEntry` يمر عبر `internal/posting.Engine` (مسودة + تحقق/تحضير + ترحيل) ويعيد الحالة المرحّلة من المحرك، مع اختبارات ACID محدثة ومارة على مسار SDK.

### المرحلة B — عكس وأحداث وIdempotency (P0) — ✅ تم التنفيذ

1. ✅ توصيل Reverse Use Case إلى `posting.Engine` في مسار SDK (`Ledger().ReverseEntry` عبر `ReverseDualContext`) مع الحفاظ على تحقق حالة القيد/الفترة ومنع العكس المكرر وتوليد قيد عكسي متوازن وتحديث حالة الأصل وفق محرك الترحيل.
2. ✅ Idempotency دائمة لاستيعاب الأحداث العامة: ترحيل/جدول `public_event_ingestions` بقيد فريد `(company_id,idempotency_key)`، حجز مفتاح ومنع التكرار، تخزين نتيجة المعالجة (معرّف/لقطة القيد) وإعادتها عند التكرار، مع كشف تعارض الحمولة لنفس المفتاح. دعم تخزين Memory/PostgreSQL.
3. ✅ حذف القاعدة الافتراضية 1000/4000 وعدم تخمين الحسابات: رفض الأحداث غير المعرّفة/غير المربوطة بقاعدة أو ربط قابل للحل.
4. ✅ Canonical Accounting Event (`pkg/api/v1/events.AccountingEvent`) مع دعم مسارين: (أ) تمرير **Journal Draft** جاهز من الـ Adapter (`JournalDraft`) للترحيل المباشر عبر مسار الترحيل الموحد، أو (ب) حل **Account Mapping قابل للتهيئة لكل شركة/دفتر** عبر جدول `public_event_account_mappings` (مع fallback لقواعد الوحدات المسجلة في الـ Router) وربط أكواد الحسابات بحسابات الشركة الحقيقية.

**معيار القبول:** منع تكرار الترحيل لنفس مفتاح idempotency (عبر القيد الفريد والتخزين الدائم) مع إعادة النتيجة السابقة؛ والعكس عبر محرك الترحيل ينشئ قيدًا متوازنًا ويمنع العكس المكرر ويحدّث حالة الأصل. (التحقق التكاملي الكامل تحت PostgreSQL حي/توازي/إعادة تشغيل يُستكمل ضمن اختبارات المرحلة C/بيئة التكامل.)

### المرحلة C — تخزين وتكامل وParity (P0) — ✅ تم التنفيذ

1. ✅ تنفيذ موائمات PostgreSQL وMemory بشكل حقيقي ودون Stub لمسارات SPI الأساسية:
   - `CoreLedgerStore`: حفظ/جلب/ترحيل القيود وربط الحسابات والفترات بالبيانات الفعلية (بدون اختلاق حسابات/فترات)، مع فرض توازن وظيفي واضح في مسار SPI.
   - `ReportingStore`: تنفيذ فعلي لـ Trial Balance/General Ledger/Balance Sheet/Income Statement عبر محركات `internal/reporting` على مستودعات التشغيل الفعلية في كلا الخلفيتين (بدلاً من نتائج فارغة/افتراضية).
   - `GovernanceStore`: بصمة اعتماد/محتوى حقيقية للقيود (`posting.EntryContentHash`) حيث يمكن حل القيد، وتسجيل تدقيق SPI في Memory عند توفر سياق شركة، مع إرجاع `ErrCapabilityNotImplemented` الصريح للقدرات غير المدعومة بأمان (بدون placeholder نجاح وهمي).
2. ✅ إضافة/تعزيز اختبارات PostgreSQL معزولة تغطي: **Transaction** (فشل ترحيل ذري بلا أثر جزئي)، **RLS** (عزل المستأجر/الشركة ومنع رؤية بيانات شركة أخرى)، **Concurrency** (ترحيلات متزامنة مع سلامة العدد/التسلسل/الأرصدة)، و**Balance Integrity** (رفض القيود غير المتوازنة عند حدود التطبيق/قاعدة البيانات).
3. ✅ الميزات المدعومة تستند إلى بيانات حقيقية؛ القدرات غير المدعومة تُعيد أخطاء قدرة واضحة.

**معيار القبول:** fixtures التكافؤ تُنتج نفس نتائج Posting وTrial Balance وGeneral Ledger وBalance Sheet وIncome Statement على Memory وPostgreSQL، واختبارات العزل تمنع الوصول بين شركتين. (تم التحقق باختبارات `storage/postgres` المستهدفة للمرحلة C ونجاحها في بيئة PostgreSQL المتاحة.)

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
