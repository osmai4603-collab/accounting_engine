# تقرير تحقق مستقل: تنفيذ `review_engine.md` وشروط إعلان `v1`

**تاريخ التحقق:** 2026-11-13  
**نطاق التحقق:** الحالة الفعلية للشجرة عند `HEAD a58ebe6` (10.70)، مع فحص الشيفرة والاختبارات والخدمة.  
**البيئة:** Go `1.26.3`، PostgreSQL `18.6`، Linux.  
**الطريقة:** فحص ثابت للشيفرة + `go build` + `go vet` + `go test` + اختبارات PostgreSQL المعزولة + اختبارات `gapred` + بناء مستهلك خارجي تجريبي.

---

## 1. الحكم التنفيذي

**لا يجوز إعلان `v1` على هذه الحالة، ولا يصح وصف `review_engine.md` بأنه منفذ بالكامل.**

الأسباب الحاسمة:

1. **شرط المستهلك الخارجي غير مستوفى فعلياً:** الاختبار المذكور كدليل (`test/acid/external_consumption_test.go`) يستورد `accounting_engine/internal/masterdata` مباشرة، ولا يوجد `go.mod` منفصل للمستهلك. كما أن واجهات SDK العامة لا توفر طريقة عامة لإنشاء الشركة/الحساب/الفترة، لذلك لا يمكن لمستهلك عام تهيئة بيانات أساسية دون استيراد `internal/masterdata`.
2. **Idempotency العامة ليست ذرية:** الحجز (`Claim`) والترحيل (`PostEntry`) والحفظ (`Complete`) تتم في معاملات منفصلة. لا يوجد اختبار PostgreSQL لمسار `ProcessEvent` العام، بينما توجد اختبارات Idempotency لكنها لمسار `subledger` القديم وليست للمسار العام.
3. **اختبارات `gapred` الحمراء ما زالت تفشل:** تشغيل `go test -tags gapred ./...` يفشل في 7 حزم. بعض هذه الاختبارات يفشل عند assertion مباشر يثبت الفجوة (مثل فحص الفترة داخل المعاملة، وبصمة المحتوى، وfail-open)، وبعضها يفشل في إعداد الاختبار نفسه، لكن المحصلة أن مصفوفة الفجوات ليست خضراء ولا صالحة كإثبات جاهزية.
4. **لا توجد موائمات قطاعية ولا اختبار أثر محاسبي قطاعي:** توجد قوالب قواعد فقط في `pkg/sdk/templates`، بينما اختبار القطاعات الحالي يتوقع رفض أحداث التجزئة/الزراعة/SaaS ولا يستخدم القوالب ولا يتحقق من الحسابات أو الأثر المحاسبي.
5. **الخدمة وgRPC غير مكتملة:** خادم gRPC يُنشأ فارغاً دون تسجيل أي خدمة، ولا توجد ملفات `.pb.go` مولدة، و`/readyz` و`/metrics` استجابتان ثابتتان، ولا توجد اختبارات خدمة أو Docker.

### 1.1 ملخص شروط إعلان `v1` الثمانية

| # | الشرط | الحكم | الخلاصة |
| --- | --- | --- | --- |
| 1 | مستهلك خارجي من Module منفصل دون أي `import internal/*` | ❌ غير مستوفى | الاختبار نفسه يستورد `internal/masterdata`، ولا يوجد Module منفصل، ولا واجهة عامة لتهيئة Master Data. |
| 2 | كل عمليات Ledger من SDK تمر عبر Application Layer ومحرك Posting الحقيقي | ⚠️ جزئي | يمر Post/Reverse عبر `posting.Engine` للسائقين الرسميين، لكن هناك fallback مباشر إلى `store.PostEntry`، والمحرك غير موصول بسياسات/اعتماد/أبعاد/حوكمة/سلسلة تدقيق. |
| 3 | الترحيل والعكس وIdempotency ذرية ومختبرة مع PostgreSQL | ❌ غير مستوفى | ذرية الترحيل جزئياً مختبرة عبر Phase C، لكن Idempotency العامة غير ذرية وغير مختبرة، واختبارات `gapred` تُظهر فجوات في العكس والترحيل داخل حدود التخزين. |
| 4 | لا تقارير Stub أو Master Data افتراضية في Adapter يقدم Capability مفعلة | ✅ مستوفى مع تحفظات | موائمات Memory/PostgreSQL تقرأ بيانات فعلية الآن، والقدرات غير المدعومة تُرجع خطأ صريحاً. تبقى دالة عامة قديمة تخمّن الحسابات. |
| 5 | وحدات المعايير تستخدم Contracts typed وتفشل بوضوح عند نقص الاعتماديات | ✅ مستوفى مع تحفظات | `events.RuleRegistrar` typed، وBuilder يفحص الاعتماديات والتكرار والأخطاء. `RunScheduledJobs` ما زال `interface{}`، ولا اختبارات للوحدات. |
| 6 | اختبار قطاعين أو ثلاثة يثبت صحة الأثر المحاسبي | ❌ غير مستوفى | لا توجد Adapters قطاعية مستقلة. القوالب موجودة فقط، والاختبار الحالي لا يتحقق من الحسابات أو القيود الناتجة. |
| 7 | اختبار تبديل Memory/PostgreSQL دون تغيير كود المستهلك | ❌ غير مستوفى | لا يوجد اختبار تبديل. `infrastructure_independence_test.go` يستخدم Memory فقط. حفظ واجهة `storage.CoreLedgerStore` لا يكفي كإثبات. |
| 8 | Docker/service build وreadiness وgRPC/HTTP مختبرة على بيئة مطابقة | ❌ غير مستوفى | لا اختبارات لـ `cmd/accounting-service`، gRPC بلا خدمات مسجلة، readiness/metrics ثابتة، لا graceful shutdown/timeouts/auth، وبيئة Compose (PostgreSQL 15) تخالف CI (18.6). |

**النتيجة:** شرطان مستوفيان مع تحفظات (#4 و#5)، شرط واحد جزئي (#2)، وخمسة شروط غير مستوفاة (#1، #3، #6، #7، #8).

---

## 2. ما تم إثباته فعلياً (نقاط إيجابية)

هذه النقاط مثبتة بالتشغيل أو بالفحص، ويجب البناء عليها:

1. `go build ./...` ينجح.
2. `go vet ./...` ينجح.
3. `go test ./... -count=1` ينجح بالكامل في البيئة الحالية (PostgreSQL متاح)، بما في ذلك اختبارات `storage/postgres` الافتراضية.
4. اختبارات Phase C الخمسة تنجح فعلياً على PostgreSQL 18.6:
   - `TestPhaseCTransactionAtomicity`
   - `TestPhaseCRLSTenantIsolation`
   - `TestPhaseCConcurrencyPostingIntegrity`
   - `TestPhaseCBalanceIntegrity`
   - `TestPhaseCParityMemoryAndPostgres`
5. موائمات SPI في Memory وPostgreSQL لم تعد ترجع تقارير صفرية أو فترات مصطنعة:
   - `storage/memory/spi_adapter.go` يحسب Trial Balance/GL/Balance Sheet/Income Statement من القيود الفعلية.
   - `storage/postgres/spi_adapter.go` يستخدم محركات `internal/reporting` الفعلية.
   - `GetFiscalPeriod`/`GetAccount` يقرآن الحالة الفعلية.
6. Idempotency الدائمة موجودة على مستوى الجدول: `public_event_ingestions` بمفتاح أساسي `(company_id, idempotency_key)`.
7. وحدات المعايير تسجل القواعد عبر `events.RuleRegistrar` typed، وBuilder يجمع أخطاء `Initialize`/`RegisterRules`/الاعتماديات ولا يتجاهلها.
8. `cmd/accounting-service` يختار PostgreSQL عند تمرير `DATABASE_DSN`، وDockerfile يستخدم `golang:1.26.3-alpine` المتوافق مع `go.mod`.
9. توجد محركات واختبارات محاسبية قوية في النواة (Posting, Reporting, Governance, Period Closing, RLS, Migrations) لا علاقة لها بفجوات مسار المنتج المذكورة هنا.

---

## 3. تفصيل التحقق من شروط إعلان `v1`

### 3.1 الشرط 1 — مستهلك خارجي حقيقي دون `internal/*` ❌

**الادعاء:** «تم التحقق عبر `test/acid/external_consumption_test.go`».

**الواقع:**

- الملف يبدأ بـ `package acid` داخل نفس الوحدة، ويستورد في السطر 8:
  ```go
  "accounting_engine/internal/masterdata"
  ```
- يقوم بتهيئة الشركة والفترة والحسابات باستخدام أنواع `masterdata.Company` و`masterdata.Account` و`masterdata.FiscalYear` مباشرة (الأسطر 37–60).
- لا يوجد في المستودع سوى `go.mod` واحد في الجذر؛ لا يوجد مشروع مستهلك مستقل بـ `go.mod` خاص.
- لا توجد واجهة عامة في `pkg/` لإنشاء Master Data. محاولة بناء مستهلك خارجي يستدعي `store.CreateCompany(...)` دون استيراد `internal/masterdata` تفشل لأن توقيع الدالة يستخدم النوع الداخلي:
  ```
  cannot use struct{}{} (value of type struct{}) as masterdata.Company value in argument to store.CreateCompany
  ```

**الحكم:** الشرط غير مستوفى. ما تم إثباته هو أن SDK يُبنى ويُستدعى من نفس الوحدة، وليس من Module خارجي مستقل.

**المطلوب للإغلاق:**

1. إنشاء مشروع خارجي فعلي (مثلاً `test/external_consumer/`) بملف `go.mod` مستقل و`replace` مؤقت أو إصدار منشور.
2. توفير عقد عام لتهيئة Master Data (مثل `pkg/spi/masterdata` أو `pkg/api/v1/masterdata`) بدون كشف `internal/*`.
3. منع أي استيراد `internal/*` في المشروع الخارجي، مع بوابة CI تتحقق من ذلك.

---

### 3.2 الشرط 2 — كل عمليات Ledger عبر Application Layer ومحرك Posting ⚠️

**ما هو موجود:**

- `pkg/sdk/builder.go:203-227`: `PostEntry` يحوّل DTO إلى `posting.JournalEntry` ثم يستدعي:
  - `postingEngine.CreateDraftContext`
  - `postingEngine.PostEntryContext`
  - ثم `postingEngine.GetEntry` لإرجاع الحالة المرحّلة.
- `pkg/sdk/builder.go:249-269`: `ReverseEntry` يستدعي `postingEngine.ReverseDualContext`.
- هذا يتحقق عندما يكون السائق الرسمي (Memory/PostgreSQL) منفذاً لـ `posting.BulkRepository` و`masterdata.ContextStorageRepository`، وهو الحال فعلاً.

**الفجوات:**

1. **Fallback مباشر إلى التخزين:** إذا لم ينفذ السائق الواجهات الداخلية، يعود SDK إلى `s.store.PostEntry(ctx, entry)` مباشرة (`builder.go:228-232`)، ويعود `ReverseEntry` إلى نسخ يدوي للقيد (`builder.go:271-295`). أي أن ضمان المرور عبر المحرك مشروط بنوع السائق وليس بعقد SDK.
2. **المحرك غير موصول بسياسات/اعتماد/أبعاد/حوكمة:** Builder ينشئ فقط:
   ```go
   inst.postingEngine = posting.NewEngine(postingRepo, masterSvc)
   ```
   (`builder.go:137-143`)
   ولا يستدعي `SetPolicyUnit` أو `SetApprovalGate` أو `SetFinancialDimensionsEngine` أو `SetObservability` أو `SetMakerCheckerPolicy`، بينما محرك الجذر يفعل ذلك (`engine.go:381-418`). لذلك مسار SDK لا يطبق سياسات الشركة ولا Maker-Checker ولا الأبعاد ولا يسجل سلسلة التدقيق.
3. **PosterID = CreatedBy:** `builder.go:214-218` يستخدم منشئ القيد نفسه كمنفذ ترحيل، ما يلغي فصل Maker/Checker عند تفعيل بوابة الاعتماد لاحقاً.
4. **بصمة المحتوى ناقصة:** اختبار `TestGAP_PST_03_EntryContentHashCoversSourceEventID` يفشل، أي أن `EntryContentHash` لا يغطي `SourceEventID`، ما يضعف ربط الاعتماد/التدقيق.
5. **بوابة الاعتماد fail-open:** `TestGAP_GOV_03_UnconfiguredGateFailsClosed` يفشل، أي أن بوابة اعتماد غير مهيأة لا ترفض الترحيل.

**الحكم:** الشرط جزئي: المسار الرسمي يمر عبر `posting.Engine`، لكن التطبيق الكامل (Application Service بسياسات وحوكمة واعتماد) غير موجود، والـ fallback يبقي الباب مفتوحاً لتجاوز المحرك.

---

### 3.3 الشرط 3 — ذرية الترحيل والعكس وIdempotency مع PostgreSQL ❌

**ما هو مثبت:**

- Phase C تثبت ذرية الترحيل على مستوى المحرك والتخزين:
  - `TestPhaseCTransactionAtomicity`
  - `TestPhaseCConcurrencyPostingIntegrity`
  - `TestPhaseCBalanceIntegrity`
- `PostReversalAtomicContext` موجود في Memory وPostgreSQL.

**الفجوات الحاسمة:**

1. **Idempotency العامة ليست ذرية:**
   - `ClaimPublicEventIngestion` يحجز المفتاح في معاملة مستقلة (`pkg/sdk/builder.go:450-458`، `storage/postgres/public_events.go:57-113`).
   - ثم يتم الترحيل في معاملة أخرى عبر `postEventEntry` (`builder.go:481`).
   - ثم يتم `CompletePublicEventIngestion` في معاملة ثالثة (`builder.go:486`).
   - إذا نجح الترحيل وفشل `Complete` أو توقف العملية، يبقى المفتاح `processing`، والطلبات اللاحقة ترجع `ErrIdempotencyInProgress` ولا تستطيع استرجاع النتيجة. لا توجد معاملة SQL واحدة تجمع الحجز والترحيل والحفظ.
2. **لا توجد اختبارات PostgreSQL لـ Idempotency العامة:** البحث في الاختبارات لم يجد استدعاءً لـ `ClaimPublicEventIngestion`/`CompletePublicEventIngestion`/`ProcessEvent` على PostgreSQL. الاختبارات الموجودة في `storage/postgres/phase2_atomic_posting_test.go:406-531` تخص `subledger_events` وليست `public_event_ingestions`.
3. **بصمة الحمولة لا تغطي كل الحمولة:** `publicEventFingerprint` في `pkg/sdk/event_ingestion.go:61-97` تتجاهل `event.Payload` و`event.Dimensions` و`event.EventID` في المسار غير المعتمد على `JournalDraft`. لذلك قد يُقبل نفس المفتاح بطلب مختلف دون كشف تعارض.
4. **العكس:** سبب العكس لا يُحفظ في القيد العكسي. `ReverseDualContext` ينسخ `Reference` من الأصل (`internal/posting/reversal.go:47`)، وSDK يعدل `out.Description` على النسخة المرجعية فقط (`builder.go:262-268`). كما أن تاريخ العكس يُضبط إلى `time.Now()` (`builder.go:257`) بدون سياسة تاريخ عكس صريحة.
5. **اختبارات `gapred` ذات الصلة تفشل:**
   - `TestGAP_PST_01_PostingRechecksPeriodInsideTransaction` يفشل عند assertion: الترحيل لا يعيد فحص الفترة داخل معاملة قاعدة البيانات (TOCTOU بين التحقق والالتزام).
   - `TestGAP_PST_02_DraftContentMatchesPeriodAndAuthorization` يفشل عند assertion: بصمة المسودة تتجاهل `PeriodID` و`TargetPeriodNumber` و`AdjustmentAuthorized` و`OriginalEntryID`.
   - `TestGAP_PST_03_EntryContentHashCoversSourceEventID` يفشل عند assertion: بصمة المحتوى لا تغطي `SourceEventID`.
   - `TestGAP_GOV_01_HardClosedPeriodCannotBeReopened` يفشل في PostgreSQL عند assertion.
   - `TestGAP_PST_04` و`TestGAP_PST_05` و`TestGAP_PST_06` تفشل أيضاً، لكن رسالة الفشل الحالية في بعضها هي «account not found» أو «invalid journal entry» أي أن الاختبار لا يصل إلى assertion المقصود. لذلك لا تُستخدم وحدها كإثبات، لكنها تمنع اعتبار المصفوفة خضراء.

**الحكم:** الشرط غير مستوفى. ذرية الترحيل الأساسية موجودة، لكن Idempotency العامة والعكس الكامل وحدود الفترة الذرية غير مكتملة أو غير مختبرة.

---

### 3.4 الشرط 4 — لا Stub ولا Master Data افتراضية ✅ (مع تحفظات)

**ما تم إصلاحه فعلاً:**

- `storage/memory/spi_adapter.go`:
  - `PostEntry` يتحقق من وجود الشركة والفترة والحسابات ولا ينشئها تلقائياً (الأسطر 63–105).
  - `GetFiscalPeriod` و`GetAccount` ينقلان الحالة الفعلية.
  - `GetTrialBalance`/`GetGeneralLedger`/`GetBalanceSheet`/`GetIncomeStatement` تحسب من القيود الفعلية.
- `storage/postgres/spi_adapter.go`:
  - `GetTrialBalance` و`GetGeneralLedger` و`GetBalanceSheet` و`GetIncomeStatement` تستخدم محرك `internal/reporting` وقراءات فعلية.
  - `GetApprovalFingerprint` يستخدم `posting.EntryContentHash`.
  - `RecordAuditLog` يرجع `types.ErrCapabilityNotImplemented` صراحةً بدلاً من كتابة سجل وهمي.

**التحفظات:**

1. ما زالت دالة عامة قديمة `events.EvaluateAndConstructEntry` في `pkg/api/v1/events/rules.go:55-100` تنشئ `AccountID` عشوائياً لكل سطر وتضع `ExchangeRate = 1`. وهي مستخدمة من `internal/events/service.go:67`، وإن كان المسار القديم غير مستخدم من SDK الحالي. وجودها كواجهة عامة يخالف روح P0-3 ويجب حذفها أو تحويلها إلى خطأ صريح.
2. تقارير Memory لا تحول العملات فعلياً؛ فهي تضع العملة المطلوبة على المبالغ المحسوبة دون تحويل. هذا ليس Stub لكنه قصور محاسبي في حالة تعدد العملات.
3. `storage/postgres/spi_adapter.go` لا يدعم `RecordAuditLog` عبر SPI، ويعيد خطأ قدرة صريحاً. هذا مقبول وفق الشرط (لأنه لا يدّعي النجاح)، لكنه يعني أن حوكمة SPI غير مكتملة.

**الحكم:** الشرط مستوفى من حيث إزالة Stub/البيانات الافتراضية من الموائمات، مع تحفظات يجب إغلاقها قبل `v1`.

---

### 3.5 الشرط 5 — Contracts typed وفشل واضح عند نقص الاعتماديات ✅ (مع تحفظات)

**ما تم إصلاحه:**

- `pkg/spi/standards/module.go:10-18` يتضمن `Version` و`Dependencies` و`ValidateDependencies` و`Initialize` و`RegisterRules` و`Close`.
- `RegisterRules` يستخدم `events.RuleRegistrar` typed بدلاً من `interface{}` أو Type Assertion على `internal/events.Router`.
- `Builder` في `pkg/sdk/builder.go:82-125`:
  - يجمع أخطاء الخيارات.
  - يرفض Module فارغاً.
  - يرفض التكرار بالاسم.
  - يفحص `Dependencies` ويفشل عند غياب الاعتمادية.
  - يمرر أخطاء `ValidateDependencies` و`Initialize` و`RegisterRules`.
- الوحدات الأربع (`modules/ifrs15`, `ifrs16`, `ifrs9`, `ias12`) تسجل قواعدها فعلاً، ويمكن التحقق من ذلك في `test/acid/industry_independence_test.go:88-118`.

**التحفظات:**

1. `RunScheduledJobs(ctx, runner interface{})` ما زال يستخدم `interface{}` (`pkg/spi/standards/module.go:17`)، وهو ما طلبه P1-2/المرحلة D صراحةً.
2. جميع الوحدات ترجع `Dependencies() == nil`، فلا يوجد Dependency Graph حقيقي بين الوحدات، والاختبار الوحيد للاعتماديات يستخدم Module وهمياً في `pkg/sdk/builder_test.go:109-136`.
3. لا توجد اختبارات للوحدات نفسها (`go test ./modules/...` يقول `[no test files]`)، ولا اختبار يثبت أن IFRS 15 سجل الحسابين 1250/4200 فعلاً واستخدمهما في القيد. اختبار القطاعات يتحقق فقط من عدد السطور و`CompanyID`.

**الحكم:** الشرط الأساسي مستوفى، لكنه يحتاج اختبارات وحدات وتوحيد `runner` قبل تثبيت العقد.

---

### 3.6 الشرط 6 — اختبار قطاعين أو ثلاثة يثبت الأثر المحاسبي ❌

**الادعاء:** «توفير قوالب وموائمات قطاعية (تجارة التجزئة، الزراعة، اشتراكات SaaS) مع اختبارات استقلالية الأنشطة».

**الواقع:**

- لا توجد حزم Adapters قطاعية مستقلة. الوسوم/الحزم التي تحتوي `retail`/`agriculture`/`saas` غير موجودة. الموجود فقط:
  - `pkg/sdk/templates/templates.go`: ثلاث دوال ترجع `PostingRule` جاهزة (Retail POS, Agriculture Harvest, SaaS Subscription).
  - لا يوجد `WithTemplate` أو واجهة عامة لتسجيل هذه القوالب في `Router` من SDK.
- `test/acid/industry_independence_test.go`:
  - يستورد `internal/masterdata` (السطر 8).
  - يتوقع رفض `POS_SALE` و`HARVEST_CROP` و`SUBSCRIPTION_REVENUE` لأنها غير مسجلة (الأسطر 70–86).
  - يختبر فقط أحداث `IFRS15_REVENUE_RECOGNITION` و`IFRS16_LEASE_PAYMENT` و`IFRS9_ECL_PROVISION`، ويتحقق فقط من `CompanyID` وعدد السطور (الأسطر 96–118).
  - لا يتحقق من الحسابات المدينة/الدائنة ولا من توازن القيد ولا من صحة الأثر المحاسبي.

**الحكم:** الشرط غير مستوفى. القوالب ليست Adapters، والاختبار الحالي لا يثبت الأثر المحاسبي القطاعي بل يثبت قبول/رفض أنواع أحداث.

**المطلوب:**

1. ثلاث حزم Adapter قطاعية مستقلة تحوّل أوامر القطاع إلى `AccountingCommand`/`AccountingEvent` أو `JournalDraft`.
2. تسجيلها في Builder بعقود typed.
3. اختبارات تتحقق من:
   - الحسابات المدينة/الدائنة الفعلية.
   - المبالغ والعملات.
   - توازن القيد.
   - اختلاف الأثر بين القطاعات الثلاثة.
   - استخدام الحسابات المعتمدة من Master Data وليس حسابات عشوائية.

---

### 3.7 الشرط 7 — تبديل Memory/PostgreSQL دون تغيير كود المستهلك ❌

**ما هو موجود:**

- SDK يعتمد على واجهات `storage.CoreLedgerStore` و`storage.ReportingStore` و`storage.GovernanceStore`.
- `cmd/accounting-service/main.go` يستطيع اختيار Memory أو PostgreSQL حسب `DATABASE_DSN`.

**الفجوات:**

1. لا يوجد اختبار واحد يبني نفس كود المستهلك على Memory ثم PostgreSQL ويقارن السلوك. `test/acid/infrastructure_independence_test.go` يستخدم Memory فقط (السطر 21)، ولا يستورد PostgreSQL.
2. اختبار Parity الوحيد (`TestPhaseCParityMemoryAndPostgres`) يستدعي `SaveDraftContext`/`PostAtomicContext` مباشرة على المخزنين، وليس عبر `sdk.Engine` أو عبر كود مستهلك موحد.
3. التبديل الفعلي عبر SDK لا يعمل لمستهلك عام بسبب مشكلة Master Data في الشرط 1: لا يمكنه إنشاء الشركة/الحساب/الفترة دون `internal/masterdata`.
4. الـ fallback في SDK يعني أن سلوك السائق الثالث (الذي ينفذ SPI فقط) قد يختلف عن Memory/PostgreSQL، فلا يوجد ضمان سلوكي موحد.

**الحكم:** الشرط غير مستوفى كاختبار. التصميم يسمح بالتبديل نظرياً، لكن الإثبات المطلوب غير موجود.

---

### 3.8 الشرط 8 — Docker/service build وreadiness وgRPC/HTTP مختبرة ❌

| البند | الحالة | الدليل |
| --- | --- | --- |
| اختيار PostgreSQL من DSN | ✅ | `cmd/accounting-service/main.go:22-42` يستخدم `cfg.Database` و`postgres.New`. |
| gRPC فعلي | ❌ | `cmd/accounting-service/grpc_server.go:21-23` ينشئ `grpc.NewServer()` ثم `Serve` دون أي `Register...Service`. لا توجد ملفات `.pb.go` مولدة في المستودع. |
| Docker Go version | ✅ | `deployments/Dockerfile:1` يستخدم `golang:1.26.3-alpine`، متوافق مع `go.mod`. |
| PostgreSQL Trial Balance حقيقي | ✅ | `storage/postgres/spi_adapter.go:144-178` يستخدم `internal/reporting`. |
| `/readyz` حقيقي | ❌ | `cmd/accounting-service/server.go:28-31` يرجع `READY` دائماً دون فحص التخزين أو الجاهزية. |
| `/metrics` حقيقي | ❌ | `server.go:32-35` يرجع gauge ثابتاً `accounting_engine_up 1`. |
| معالجة `company_id` غير صالح | ❌ | `server.go:75` يستخدم `uuid.MustParse`، ما يسبب panic بدلاً من 400. |
| graceful shutdown/timeouts/body limits/auth | ❌ | `server.go:42` يستخدم `http.ListenAndServe` مباشرة دون timeouts أو `Shutdown` أو حدود جسم أو مصادقة. |
| اختبارات خدمة | ❌ | `go test ./...` يسجل `cmd/accounting-service [no test files]`. |
| Docker/service build مختبر | ❌ | لا يوجد اختبار Docker build أو docker-compose في CI. |
| بيئة مطابقة | ❌ | `deployments/docker-compose.yml:5` يستخدم `postgres:15-alpine`، بينما CI يستخدم `postgres:18.6` (`.github/workflows/ci.yml:45`). |

**ملاحظة:** يوجد ثنائي خدمة آخر `cmd/agy` (`agy serve`) يعرض `/metrics` حقيقية من `observability.Unit` و`/healthz`، ويستخدم `ReadHeaderTimeout` و`Shutdown`، لكنه لا يعرض `/readyz` ولا مصادقة/TLS، واختبار `TestGAP_OPS_01_ServiceExposesReadinessProbe` معلّق بـ `t.Skip`. لذلك وجوده لا يغيّر حكم الشرط الثامن.

**الحكم:** الشرط غير مستوفى. الخدمة بدأت تصبح قابلة للتشغيل، لكنها ليست موزعة أو مختبرة كمنتج.

---

## 4. تحقق بنود `review_engine.md` نفسها

الوثيقة نفسها تقول في السطر 53 إن المراحل اللاحقة (P1 وD/E/F) ما زالت مطلوبة، وتوصي في السطر 297 بعدم إعلان `v1` قبل تحقق الشروط. لذلك وصفها بأنها «منفذة بالكامل» يناقض محتواها.

### 4.1 نتائج P0

| البند | حالة الوثيقة | الحكم الفعلي | الملاحظات |
| --- | --- | --- | --- |
| P0-1: PostEntry عبر Posting Engine | ✅ | ⚠️ جزئي | يمر عبر المحرك للسائقين الرسميين، لكن fallback مباشر، والمحرك بلا سياسات/اعتماد/أبعاد/حوكمة. |
| P0-2: دورة عكس كاملة | ✅ | ⚠️ جزئي | العكس الذري موجود للسائقين الرسميين، لكن `reason` لا يُحفظ، والتاريخ `time.Now()`، والمسار الاحتياطي غير ذري، و`GAP-PST-04/05` حمراء. |
| P0-3: ProcessEvent موثوق | ✅ | ⚠️ جزئي | لا حسابات عشوائية في SDK ولا قاعدة افتراضية 1000/4000، لكن `ExchangeRate=1` دائماً، والوصف قالب حرفي، و`Payload`/`Dimensions` غير مستخدمين، ودالة `EvaluateAndConstructEntry` العامة ما زالت تخمّن. |
| P0-4: Idempotency موثوقة | ✅ | ⚠️ جزئي | جدول ومفتاح فريد موجودان، لكن الحجز/الترحيل/الحفظ في معاملات منفصلة، ولا اختبار PostgreSQL للمسار العام، والبصمة لا تغطي كل الحمولة. |
| P0-5: Master Data/Reporting موثوقة | ✅ | ✅ | الموائمات تقرأ بيانات فعلية، والقدرات غير المدعومة ترجع أخطاء صريحة. تبقى ملاحظات العملات/الحوكمة. |
| P0-6: تسجيل قواعد المعايير | ✅ | ⚠️ جزئي | العقد typed والأخطاء لا تُتجاهل. لكن لا اختبارات للوحدات، ولا تحقق من الحسابات الفعلية، و`RunScheduledJobs` غير typed. |

### 4.2 نتائج P1

| البند | الحالة | الدليل |
| --- | --- | --- |
| P1-1: تصحيح نموذج الأنواع | ❌ لم ينفذ | `pkg/api/v1/types/identifiers.go:5-9` كلها Aliases لـ `uuid.UUID`. `FiscalYear.ID` و`FiscalPeriod.FiscalYearID` من نوع `CompanyID` (`pkg/api/v1/ledger/period.go:34,46`). `EntryLine.ID` من نوع `AccountID` (`pkg/api/v1/ledger/entry.go:29`). حقول Partner/CostCenter/Project/Segment/BusinessUnit من نوع `*CompanyID` (`entry.go:31-35`). |
| P1-1: Round-trip كامل للحقول | ❌ | `pkg/sdk/mapping.go:253` يضع `SourceModule: ""` عند الإرجاع. النموذج الداخلي `posting.JournalEntry` لا يحتوي أصلاً `TransactionCurrency`/`ExchangeRate`/`ApprovalFingerprint` على مستوى القيد (`internal/posting/entry.go:147-171`). |
| P1-2: عقود SPI حدود المعاملة | ❌ | `pkg/spi/storage/core.go:10-16` ما زال يعرّف `PostEntry` كعملية تخزين. لا `TransactionManager` ولا `IdempotencyRepository` ولا `Approval/Policy Ports` في SPI العام. |
| P1-3: Builder يفرض التهيئة | ⚠️ جزئي | أخطاء الخيارات وnil modules والتكرار والاعتماديات عولجت (`builder.go:68-125`). لكن `Reporting()` و`Governance()` يعيدان خدمات حتى مع `store == nil`، والخطأ يظهر عند أول استدعاء (`builder.go:173-179, 302-346`). |
| P1-4: اختبارات ACID المعمارية | ❌ | المستهلك الخارجي غير مستقل ويستورد `internal`، اختبار القطاعات ليس قطاعياً ولا يثبت الأثر، واختبار البنية التحتية يستخدم Memory فقط. |
| P1-5: منع تسرب الجذر | ❌ | `engine.go` ما زال يصدّر أنواعاً من `internal/*` (`Config.Standards`، `StorageDriver`، `AccountingEngine`). سكربت النقاء يفحص `./pkg/...` فقط، ثم يفشل فعلياً على `pkg/sdk` نفسه. |

### 4.3 نتائج قسم الخدمة (القسم 6)

| البند | الحالة |
| --- | --- |
| 6.1 استخدام PostgreSQL من `cfg.Database` | ✅ تم |
| 6.2 gRPC Server فعلي | ❌ خادم فارغ بلا خدمات مسجلة أو Proto مولّد |
| 6.3 Docker Go version متوافق | ✅ تم |
| 6.4 Trial Balance في PostgreSQL | ✅ تم |
| 6.5 `/readyz` يتحقق من التخزين | ❌ ثابت |
| 6.6 `/metrics` مقاييس فعلية | ❌ ثابت |
| 6.7 `uuid.MustParse` يعيد 400 | ❌ panic محتمل |
| 6.8 graceful shutdown/timeouts/body/auth | ❌ غير موجود |

### 4.4 المراحل D/E/F

| المرحلة | الحكم | السبب |
| --- | --- | --- |
| D — Modules & Contracts (P1) | ❌ | `RunScheduledJobs(interface{})`، لا Module Tests، لا Dependency Graph حقيقي، IDs لم تثبت. |
| E — إثبات قابلية إعادة الاستخدام (P1) | ❌ | لا External Consumer Fixture مستقل، لا Sector Adapters، لا Infrastructure Matrix، لا CI يمنع الاستيراد الداخلي. |
| F — Service Distribution | ❌ | gRPC فارغ، readiness/metrics وهمية، لا auth/timeouts/shutdown، Docker/Compose غير مختبرين. |

---

## 5. اختبارات `gapred` الحمراء: مؤشر على عدم اكتمال المصفوفة

هذه الاختبارات موجودة في المستودع وتهدف إلى كشف الفجوات المعلنة. بعضها يفشل عند assertion مباشر يثبت الفجوة، وبعضها يفشل في إعداد الاختبار. في الحالتين، لا يمكن اعتبار بوابة الجاهزية خضراء. التشغيل:

```bash
POSTGRES_URL='postgres://osm@localhost:5432/accounting_test?sslmode=disable&host=/var/run/postgresql' \
  go test -tags gapred ./... -count=1
```

**النتيجة: `FAIL` (exit 1).**

| الحزمة | الاختبار الفاشل | نوع الفشل | الدلالة |
| --- | --- | --- | --- |
| `internal/governance` | `TestGAP_GOV_03_UnconfiguredGateFailsClosed` | assertion مباشر | بوابة اعتماد غير مهيأة تسمح بالمرور (fail-open). |
| `internal/posting` | `TestGAP_PST_02_DraftContentMatchesPeriodAndAuthorization` | assertion مباشر | بصمة المسودة تتجاهل `PeriodID` و`TargetPeriodNumber` و`AdjustmentAuthorized` و`OriginalEntryID`. |
| `internal/posting` | `TestGAP_PST_03_EntryContentHashCoversSourceEventID` | assertion مباشر | بصمة محتوى القيد لا تغطي `SourceEventID`. |
| `internal/posting` | `TestGAP_PST_04_ReversalFallbackIsNotAtomic` | فشل إعداد (`account not found`) | لا يصل إلى assertion؛ لا يُستخدم وحده كإثبات. |
| `internal/posting` | `TestGAP_PST_06_BulkFallbackIsNotAtomic` | فشل إعداد (`account not found`) | لا يصل إلى assertion؛ لا يُستخدم وحده كإثبات. |
| `storage/postgres` | `TestGAP_GOV_01_HardClosedPeriodCannotBeReopened` | assertion مباشر | إعادة فتح فترة مغلقة نهائياً ممكنة. |
| `storage/postgres` | `TestGAP_PST_01_PostingRechecksPeriodInsideTransaction` | assertion مباشر | الترحيل لا يعيد فحص الفترة داخل معاملة قاعدة البيانات. |
| `storage/postgres` | `TestGAP_PST_05_ReversalMustInvertOriginalLines` | فشل إعداد (`invalid journal entry`) | لا يصل إلى assertion؛ لا يُستخدم وحده كإثبات. |
| `storage/memory` | `TestGAP_AST_02_ConcurrentDepreciationOfSameAssetPostsOnce` | assertion مباشر (متقطع) | الإهلاك المتزامن للأصل نفسه قد يُرحّل مرتين؛ الفشل ظهر في 2 من 3 تشغيلات، لذا هو فجوة حقيقية لكنها غير حتمية. |
| `internal/subledgers` | `TestGAP_AST_01_DepreciationAttachmentValidatesEntryContent` | assertion مباشر | التحقق من محتوى إهلاك الأصل غير مكتمل. |
| `internal/ias12` | `TestGAP_STD_01/02` | assertion مباشر | فجوات في الاعتراف بالأصل الضريبي المؤجل وسعر الضريبة. |
| `internal/reporting` | فشل بناء اختبارات `gapred` | فشل ترجمة | رموز مفقودة: `NewClosingEngine` و`findFiscalPeriod` في `internal/reporting/gapred_test.go`. |

**ملاحظة مهمة:** CI الحالي لا يشغّل `-tags gapred`؛ لذلك تمر بوابة `go test ./...` بينما تبقى هذه الفجوات مفتوحة. هذا يفسر التناقض بين «الاختبارات خضراء» و«الفجوات موجودة».

---

## 6. الأدلة التشغيلية

### 6.1 الأوامر والنتائج

| الأمر | النتيجة |
| --- | --- |
| `go build ./...` | ✅ نجاح |
| `go vet ./...` | ✅ نجاح |
| `go test ./... -count=1` | ✅ نجاح (بدون وسم `gapred`) |
| `POSTGRES_URL=... go test ./storage/postgres -run TestPhaseC -v` | ✅ 5/5 نجاح |
| `go test ./test/acid -v` | ✅ 3/3 نجاح، لكن جودة الاختبارات غير كافية كما سبق |
| `go test -tags gapred ./... -count=1` | ❌ فشل في 7 حزم (إحداها متقطعة: `storage/memory`) |
| `./scripts/check_gofmt.sh` | ❌ فشل: 9 ملفات غير منسقة |
| `./scripts/check_public_api_purity.sh` | ❌ فشل: `pkg/sdk` يستورد `internal/masterdata` و`internal/posting` و`internal/types` |
| بناء مستهلك خارجي بسيط (SDK + Memory) | ✅ نجح |
| بناء مستهلك خارجي يحاول تهيئة Master Data دون `internal` | ❌ فشل: `cannot use ... as masterdata.Company value` |

### 6.2 ملفات gofmt غير المنسقة

```
internal/events/service.go
internal/reporting/perpetual_ledger_benchmark_test.go
pkg/api/v1/events/event.go
pkg/api/v1/types/identifiers.go
pkg/sdk/builder_test.go
pkg/sdk/event_ingestion.go
pkg/sdk/mapping.go
storage/memory/public_events.go
storage/memory/spi_adapter.go
```

هذا يعني أن بوابة `gofmt` في CI ستفشل حتى قبل الوصول إلى الاختبارات.

### 6.3 نقطة إضافية: سكربت النقاء

`scripts/check_public_api_purity.sh` يفحص `go list ./pkg/...` ويفشل إذا وجد أي import يحتوي `internal`. النتيجة الحالية:

```
VIOLATION: Public package imports internal: accounting_engine/pkg/sdk:
accounting_engine/internal/masterdata, accounting_engine/internal/posting,
accounting_engine/internal/types, ...
```

أي أن مشروع `pkg/sdk` نفسه يخالف سياسة النقاء التي يعلنها المستودع. كما أن CI لا يشغّل هذا السكربت أصلاً (`.github/workflows/ci.yml:32-37`).

---

## 7. الفجوات الحاسمة المتبقية (مرتبة)

### P0 — تمنع إعلان `v1`

1. **عقد Master Data عام للمستهلك الخارجي**
   - إضافة `pkg/spi/masterdata` أو `pkg/api/v1/masterdata` مع عمليات إنشاء/قراءة الشركة والحساب والفترة.
   - اختبار مستهلك خارجي فعلي بـ `go.mod` منفصل.
   - منع أي `internal/*` في المشروع الخارجي.

2. **توحيد مسار SDK مع Application Service كامل**
   - ربط `posting.Engine` بـ `SetPolicyUnit` و`SetApprovalGate` و`SetFinancialDimensionsEngine` و`SetObservability` و`SetMakerCheckerPolicy`.
   - إزالة fallback المباشر إلى `store.PostEntry` أو تحويله إلى خطأ `ErrCapabilityNotImplemented`.
   - فصل Maker/Checker وعدم استخدام `CreatedBy` كـ `posterID`.

3. **Idempotency ذرية**
   - حجز المفتاح + الترحيل + حفظ النتيجة في معاملة واحدة أو نمط Outbox/State Machine قابل للاستئناف.
   - معالجة الحالة `processing` العالقة بعد الانهيار.
   - اختبار PostgreSQL متزامن (نفس المفتاح، طلبات متوازية، إعادة تشغيل، تعارض حمولة).
   - توسيع بصمة الحمولة لتشمل `EventID` و`Payload` و`Dimensions`.

4. **إصلاح بناء حدث SDK**
   - تحويل العملة المعاملة إلى العملة الوظيفية باستخدام سعر صرف حقيقي، لا `1`.
   - ملء `DescriptionTemplate` ببيانات الحدث.
   - استخدام `Payload`/`Dimensions` في mapping أو رفضها صراحةً.
   - حذف/تعطيل `events.EvaluateAndConstructEntry` العامة التي تنشئ حسابات عشوائية.

5. **إغلاق فجوات `gapred` المرتبطة بالترحيل**
   - `GAP-PST-01` (فحص الفترة داخل المعاملة).
   - `GAP-PST-02` (بصمة المسودة).
   - `GAP-PST-03` (تغطية `SourceEventID`).
   - `GAP-PST-04` (العكس الاحتياطي الذري).
   - `GAP-PST-05` (التحقق من عكس السطور).
   - `GAP-GOV-01` (منع إعادة فتح الفترة المغلقة نهائياً).
   - `GAP-GOV-03` (fail-closed للاعتماد).

6. **Adapters قطاعية واختبارات أثر محاسبي**
   - ثلاث حزم تجارة/زراعة/SaaS.
   - تحويلها إلى `AccountingCommand`/`JournalDraft`.
   - اختبار الحسابات والمبالغ والتوازن، لا مجرد عدد السطور.

### P1 — مطلوبة قبل تثبيت العقد

1. تصحيح أنواع المعرّفات (`CompanyID`/`AccountID`/`FiscalPeriodID`/`JournalEntryID`/`JournalEntryLineID`/`FiscalYearID`).
2. إكمال Mapping ثنائي الاتجاه لكل الحقول العامة.
3. تصميم SPI على أساس Unit of Work/Transaction Manager وIdempotency Repository وApproval/Policy Ports.
4. إصلاح Builder ليفشل عند غياب Reporting/Governance بدلاً من إرجاع خدمة معطلة.
5. إزالة تسرب `internal/*` من الجذر أو إعلانه Legacy.
6. توسيع بوابة النقاء لتشمل كل الحزم العامة، وتشغيلها في CI.
7. إصلاح `gofmt` وتشغيل `-tags gapred` في CI أو تحويل الفجوات المعروفة إلى `Skip` موثق مع تاريخ إغلاق.

### P2 — الخدمة والتوزيع

1. توليد كود gRPC من `proto/accounting/v1/*.proto` وتسجيل الخدمات فعلياً.
2. `/readyz` يفحص اتصال PostgreSQL وجاهزية الميزات.
3. `/metrics` بمقاييس Posting/Failure/Latency فعلية.
4. `uuid.Parse` مع 400، وحدود جسم، وtimeouts، وgraceful shutdown، ومصادقة/تفويض.
5. اختبارات خدمة HTTP/gRPC وDocker build في CI.
6. توحيد إصدار PostgreSQL بين Compose وCI.

---

## 8. خلاصة القرار

- **`review_engine.md` لم يُنفذ بالكامل:** الوثيقة نفسها تقر بأن P1 والمراحل D/E/F متبقية، والفحص الفعلي يثبت أن عدة بنود P0 «مكتملة» في الوثيقة هي في الحقيقة جزئية.
- **شروط `v1` الثمانية:** 2 مستوفيان مع تحفظات، 1 جزئي، 5 غير مستوفاة.
- **التوصية:** عدم إعلان `v1`، وعدم اعتماد المسار الجديد لترحيل قيود مالية حقيقية قبل إغلاق P0 أعلاه. الأولوية القصوى هي: عقد Master Data عام + مستهلك خارجي حقيقي، ثم توحيد Application Service وIdempotency الذرية، ثم إغلاق فجوات `gapred`، ثم الخدمة وgRPC.

---

## 9. ملحق: أوامر إعادة الإنتاج

```bash
# 1) البناء والفحص
go build ./...
go vet ./...

# 2) الاختبارات الافتراضية
go test ./... -count=1

# 3) اختبارات PostgreSQL Phase C
POSTGRES_URL='postgres://osm@localhost:5432/accounting_test?sslmode=disable&host=/var/run/postgresql' \
  go test ./storage/postgres -run TestPhaseC -count=1 -v

# 4) اختبارات الفجوات الحمراء
POSTGRES_URL='postgres://osm@localhost:5432/accounting_test?sslmode=disable&host=/var/run/postgresql' \
  go test -tags gapred ./... -count=1

# 5) بوابات الجودة
./scripts/check_gofmt.sh
./scripts/check_public_api_purity.sh

# 6) اختبار المستهلك الخارجي (بعد إنشائه)
cd test/external_consumer && go build ./...
```

**ملاحظة:** نتائج هذا التقرير تخص الشجرة الحالية (`a58ebe6`). أي تعديل لاحق على مسار SDK أو الخدمة أو الاختبارات يستوجب إعادة تشغيل الأوامر أعلاه وتحديث هذا الملف.
