# `P1-02` — تصميم عقود Public API وPublic SPI

- **التاريخ:** 2026-11-14
- **يعتمد على:** `docs/public_api_inventory.md`, `ADR-0001`, `ADR-0003`, `ADR-0007`
- **الحالة:** وثيقة تصميم تاريخية؛ راجع [دليل SDK الحالي](./sdk_developer_guide.md) للعقود المنفذة.

> **ملاحظة تحديث:** هذه وثيقة تصميم تاريخية من `P1-02` وليست وصفًا موثوقًا
> للسلوك الحالي. منذ كتابتها أصبح `pkg/sdk` يوفّر Application Service
> واستخدامات `CreateDraft`, `Validate`, `Post`, `Reverse`, و`GetEntry`.
> استخدم [دليل SDK الحالي](./sdk_developer_guide.md) وواجهات الحزم العامة
> مرجعًا لعقود المستهلكين.

## 1. الهدف

فصل ثلاث طبقات بشكل صريح:

1. **API عام** (`pkg/api/v1`): نماذج نقل بيانات ثابتة لا تعرف التخزين.
2. **Application Contract** (`pkg/spi/application`): أوامر/استعلامات/هوية/تفويض/أخطاء.
3. **Persistence Ports** (`pkg/spi/storage`): عمليات تخزين ذرية لا تنشئ Services ولا
   تعرف PostgreSQL.

## 2. مخطط التبعيات

```text
التطبيق / النقل (HTTP, gRPC, CLI, Host)
        │
        ▼
pkg/sdk  ───────────────► pkg/spi/application   (عقود Use Case)
   │                          ▲
   │                          │ (P2)
   ▼                          │
internal/sdkapplication ──────┘
   │
   ├──► internal/posting (Domain/Validation)
   ├──► internal/masterdata (Domain)
   └──► internal/portbridge (تحويل صريح)
              │
              ▼
        pkg/spi/storage.Ports
              ▲
              │ implements
   storage/memory.Adapter | storage/postgres.Adapter
              │
              ▼
        internal/adapters/{memory,postgres}

اتجاه مسموح: root facade → pkg/sdk → pkg/api|pkg/spi
اتجاه ممنوع: pkg/sdk أو internal/** → root
اتجاه ممنوع: pkg/api/** أو pkg/spi/** → internal/**
```

## 3. عقود Persistence (`pkg/spi/storage`)

### 3.1 `Ports`

| Port | إلزامي | المسؤولية | الضمانات |
| --- | --- | --- | --- |
| `DraftStore.SaveDraft` | نعم | حفظ مسودة قيد | يرفض تعارض الشركة/الفترة/الحساب قبل الكتابة؛ لا يولّد IDs |
| `EntryReader.GetEntry` | نعم | قراءة قيد داخل شركة | لا يعيد بيانات شركة أخرى؛ لا يحوّل عملة/تاريخًا |
| `PostingWriter.CommitPosting` | نعم | ترحيل قيد أو عكس | ذرية كاملة: الرأس + السطور + الحالة + التسلسل + الأرصدة + التدقيق + Idempotency |
| `SequenceAllocator.NextSequence` | نعم | حجز رقم القيد | فريد لكل (شركة, دفتر)؛ يُحجز داخل معاملة الترحيل |
| `MasterDataReader` | نعم | شركة/حساب/دفتر/سنة/فترة | قراءة فقط؛ لا Provisioning |
| `BalanceReader` | لا | رصيد حساب حتى تاريخ | يطبق العملة الوظيفية والتاريخ بشكل حتمي |
| `AuditWriter.AppendAudit` | لا | سجل تدقيق ثابت | يرفض UPDATE/DELETE على مستوى التخزين |
| `IdempotencyStore` | لا | حجز/إكمال مفاتيح التكرار | `Claim` ذري؛ إكمال مع نفس معاملة العملية |
| `Events` (`publicevents.Stores`) | لا | Idempotency وMappings أحداث Public | هوية `(شركة, مصدر, مفتاح)` دائمة؛ اختلاف البصمة = تعارض؛ لا Fallback داخل العملية |
| `TransactionRunner` | لا | تنفيذ مجموعة عمليات | لا يكشف `pgx.Tx` أو `sql.Tx` |
| `PrivateExtensions` | لا | قدرات خاصة بالموائم | المحرك لا يعتمد على أي مفتاح فيه |

`Ports.Validate()` يجمع **كل** Port مفقود في خطأ واحد بدل الفشل عند أول غياب.

### 3.2 `PostingCommit`

- `Operation`: `post` أو `reverse`.
- `OriginalEntryID`: إلزامي للعكس.
- `ActorID`, `Reason`, `CorrelationID`: تُحفظ للتدقيق، وليست حقولًا في DTO القيد.
- الموائم لا يعيد تنفيذ سياسة المحاسبة أو التحقق من التوازن أو بناء العكس.

### 3.3 `MasterDataReader`

```go
GetCompany(ctx, companyID) (Company, error)
GetAccount(ctx, companyID, accountID) (Account, error)
GetAccountByCode(ctx, companyID, code) (Account, error)
GetJournalByCode(ctx, companyID, code) (Journal, error)
HasAccountChildren(ctx, companyID, accountID) (bool, error)
GetFiscalYear(ctx, companyID, fiscalYearID) (FiscalYear, error)
GetPeriodForDate(ctx, companyID, date) (FiscalPeriod, error)
```

لا يتضمن إنشاء/تعديل. إعداد البيانات الرئيسية عبر `pkg/spi/masterdata.Store`
(أداة Provisioning منفصلة).

### 3.4 Capabilities منفصلة

`ReportingStore`, `GovernanceStore`, `ExchangeRateSPI`, `standards.StandardModule`
عقود مستقلة اختيارية. غيابها يعني تعطيل القدرة، وليس تنفيذًا صامتًا أو `nil`
يعمل كأنه مفعّل.

## 4. عقود Application (`pkg/spi/application`)

- `Principal`: هوية موثوقة من المضيف (`ActorID`, `TenantID`, `CompanyIDs`,
  `Permissions`). المحرك لا يثق بـ `CompanyID` قادم من الحمولة وحدها.
- `Authorizer`: قدرة مضيف اختيارية؛ في غيابها يفرض المحرك وجود Principal وعضوية
  الشركة.
- أوامر: `CreateDraftCommand`, `ValidateEntryCommand`, `PostEntryCommand`,
  `ReverseEntryCommand`.
- استعلامات: `GetEntryQuery`.
- `Service`: سطح Use Case واحد لكل النقل والتكامل.
- أخطاء ثابتة قابلة للفحص عبر `errors.Is`: `ErrValidation`, `ErrNotFound`,
  `ErrConflict`, `ErrUnauthorized`, `ErrForbidden`, `ErrPeriodClosed`,
  `ErrDuplicateRequest`, `ErrDependencyUnavailable`, `ErrInternal`.
- `Error` يحمل رسالة عامة آمنة و`Cause` للسجلات فقط.

**ملاحظة تاريخية:** فقرة الحالة أعلاه تعكس نقطة التصميم بتاريخ `P1-02` فقط.
يوفّر SDK الحالي `LedgerService` كما يوفّر Application Service العام. يمرر
المضيف `application.Context` موثوقًا، ويمكنه تزويد `application.Authorizer`.

## 5. عقود التكامل (`pkg/spi/events`)

- `Ingestion`: سجل Idempotency دائم. هويته المركبة
  `(CompanyID, SourceSystem, IdempotencyKey)`؛ المصدر الافتراضي `event` حتى
  تبقى المفاتيح السابقة صالحة. يحتوي أيضًا على `PayloadHash` و
  `PayloadHashVersion` وحالة دورة الحياة و`FailureClass` ولقطة القيد الناتج.
- حالات دورة الحياة الأساسية: `started`, `completed`, `retryable_failure`,
  `terminal_failure`. القيم القديمة `processing`, `processed`, `failed` تبقى
  مقروءة ومكافئة عبر `NormalizeStatus` و`IsCompleted`/`IsStarted`/
  `IsRetryableFailure`/`IsTerminalFailure`. الفشل `transient` قابل لإعادة
  المحاولة، و`terminal` يمنعها نهائيًا.
- `MappingStore`: خريطة نوع حدث ← دفتر/حساب مدين/دائن/قالب وصف.
- `IdempotencyStore`: التوقيعات المستقرة `Claim`/`Complete`/`Fail` دون تغيير.
- `ScopedIdempotencyStore` (اختياري): قراءة حسب المصدر، إكمال يتحقق من بصمة
  الحمولة، وفشل مصنّف. `ClassifiedIdempotencyStore` (اختياري) يضيف تصنيف
  الفشل صراحة. المحرك يفضّل القدرات الأغنى إن وجدت، وإلا يستخدم التوقيعات
  المستقرة للمصدر الافتراضي.
- `Stores`: يجمع `Idempotency` و`Mappings` ويُعرض عبر `storage.Ports.Events`.
  غياب المتجر الدائم خطأ صريح (`ErrDependencyUnavailable` +
  `sdk.ErrIdempotencyStateUnavailable`) ولا يوجد Fallback داخل العملية.
- البصمة تُحسب في `pkg/sdk` بإصدار تطبيع معلن
  (`PublicEventPayloadHashVersion = "v1"`). نفس المفتاح + نفس البصمة يعيد
  النتيجة الأصلية، واختلاف البصمة يعيد تعارضًا دون استبدال السجل.
- العملية الجارية تنتظر ضمن سياسة محدودة (افتراضيًا 2s بفاصل 25ms، وقابلة
  للضبط عبر `WithEventIdempotencyWaitPolicy`) ثم تعيد
  `ErrIdempotencyInProgress`. إعادة المحاولة مسموحة للأخطاء المؤقتة فقط،
  وبعد فحص الحالة المرحّلة فعليًا.
- مسار `WithCoreStorage` القديم يبقى محافظًا على مفردات الحالات السابقة
  (`processing`/`processed`/`failed`) ولا يُرقّي السجلات تلقائيًا.

هذه قدرات اختيارية؛ غيابها يمنع التكامل الآلي ولا يفعّل تخمين الحسابات.

## 6. مثال موائم خارجي

`test/external_adapter_consumer` وحدة Go مستقلة (module منفصل) تنفذ Public SPI
بالكامل وتشغّل سيناريو محاسبيًا كاملًا دون استيراد `internal/` أو
`storage/memory` أو `storage/postgres`:

- **Storage Ports:** `DraftStore`, `EntryReader`, `PostingWriter`,
  `SequenceAllocator`, `MasterDataReader` من `pkg/spi/storage`.
- **Reporting Capability:** `ReportingStore`؛ ينفذ `GetTrialBalance` و
  `GetGeneralLedger` من القيود المرحّلة (بما فيها العكوس)، ويرجع
  `ErrReportNotSupported` صريحًا للتقارير غير المنفذة.
- **Currency Source:** `ExchangeRateSPI` بسيط للعملة الوظيفية USD.

الموائم لا يولّد معرفات أساسية ولا ينفذ قواعد المحاسبة: يستنسخ رأس القيد
وسطوره عند القراءة والكتابة، ويفرض نطاق الشركة على كل Port، ويخصص تسلسلًا
حتميًا لكل دفتر، ويجري انتقال حالة الأصل/العكس تحت قفل كتابة واحد.

نقطة التركيب الخارجية تمرر الموائم فقط:

```go
ports := store.Ports() // Drafts, Entries, Posting, Sequences, MasterData
useCases, err := sdk.NewApplicationService(ports)
engine, err := sdk.NewBuilder(
    sdk.WithStoragePorts(ports),
    sdk.WithReporting(store), // قدرة منفصلة، وليست جزءًا إلزاميًا من Ports
).Build(ctx)
```

ثم يُنفذ `CreateDraft`/`Validate`/`Post`/`GetEntry` عبر
`pkg/spi/application.Service`، و`GetEntry`/`ReverseEntry`/`GetTrialBalance`
عبر `pkg/sdk.Engine`.

**العكس الذري عبر العقد العام:** لا يستطيع موائم خارجي تنفيذ الواجهة الداخلية
`portbridge.atomicReversalPostingPort` لأن توقيعها يستخدم أنواع
`internal/posting`. لذلك يعتمد `portbridge.Repository` الآن على
`PostingWriter.CommitPosting` كمسار عام بديل:

```go
commit := storage.PostingCommit{
    Entry:           reversalEntry,
    Operation:       storage.OperationReverse,
    OriginalEntryID: &originalID,
    ActorID:         actorID,
    Reason:          reason,
}
```

على الموائم عندها التحقق داخل قفل واحد من أن الأصل موجود ومرحّل وغير معكوس،
ثم حفظ العكس وتحديث حالة الأصل و`ReversedByID` ذريًا. مسار العكس المرتبط
بمفتاح Idempotency غير الفارغ يبقى غير مدعوم للموائمات العامة، ويسجّل ذلك في
`docs/ard/ARD-P3-05.md`.

**فحص الاستهلاك:** `scripts/check_external_adapter_consumer.sh` يبني ويختبر
الوحدة ويفشل عند أي import محظور. الاختبار `TestNoForbiddenImports` يحلل كل
ملفات `.go` ويمنع `internal/` و`storage/memory` و`storage/postgres` ويشترط أن
تكون كل استيرادات وحدة المحرك تحت `pkg/`. الوضع `--published` ينسخ الوحدة،
يحذف `replace`، ويبنيها من Module Proxy بلا استيراد داخلي.

## 7. الثبات والتوافق

- أي إضافة إلى واجهة عامة = MINOR.
- أي حذف/تغيير توقيع/تغيير دلالة = MAJOR.
- `docs/api/v1.snapshot` أساس فحص التوافق في CI.
- لا تُضاف أنواع `internal` أو `pgx` أو أسماء جداول إلى أي عقد عام.

## 8. سجل فجوات التصميم وقت كتابة الوثيقة (تاريخي)

الجدول التالي لقطة تخطيطية بتاريخ `P1-02`؛ لا يثبت أن الفجوة ما زالت قائمة.
تحقق من الكود واختبارات المستهلك الخارجي قبل الاستناد إلى أي صف.

| الفجوة | الأثر | المهمة |
| --- | --- | --- |
| ~~لا يوجد Port لقراءة الدفتر بالرمز~~ | أُغلق بإضافة `MasterDataReader.GetJournalByCode` | `P2-06` |
| ~~Idempotency الحدث غير موصول في وضع Ports~~ | أُغلق في `P3-04`: `storage.Ports.Events` يجعل السجل دائمًا ومحصورًا بالمصدر؛ لا Fallback داخل العملية | `P3-04` |
| `ReverseWithReasonContext` يحفظ مسودة ثم يرحّل العكس في معاملتين | مسودة يتيمة عند فشل الترحيل | `P2-05` |
| ~~`application.Service` غير موصول بـ SDK~~ | أصبح متاحًا عبر `NewApplicationService` و`NewApplicationServiceWithAuthorizer` ويستخدمه `EngineBuilder` | `P2-01`, `P2-02` |
| Standards تتطلب `WithCoreStorage` | لا تعمل في وضع Ports | `P3-02` |
