# `P1-02` — تصميم عقود Public API وPublic SPI

- **التاريخ:** 2026-11-14
- **يعتمد على:** `docs/public_api_inventory.md`, `ADR-0001`, `ADR-0003`, `ADR-0007`
- **الحالة:** العقود الأساسية منفذة ومجمّعة؛ ربط `pkg/spi/application.Service` الكامل في `pkg/sdk` يتبع في `P2-01`/`P2-02`.

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

**حالة التنفيذ:** العقود موجودة ومختبرة تجميعيًا. `pkg/sdk` ما زال يعرض
`LedgerService` بالشكل القديم؛ ربط `application.Service` وتمرير `Principal` و
`Authorizer` هو نطاق `P2-01`/`P2-02`.

## 5. عقود التكامل (`pkg/spi/events`)

- `Ingestion`: سجل Idempotency دائم (مفتاح، بصمة، حالة، قيد النتيجة).
- `MappingStore`: خريطة نوع حدث ← دفتر/حساب مدين/دائن/قالب وصف.
- `IdempotencyStore`: `Claim`/`Complete`/`Fail` مع حالات صريحة.

هذه قدرات اختيارية؛ غيابها يمنع التكامل الآلي ولا يفعّل تخمين الحسابات.

## 6. مثال موائم خارجي

الاختبار `test/external_consumer/ports_adapter_test.go` ينفذ `storage.Ports`
بالكامل **خارج** الوحدة، دون استيراد `internal` أو SQL، ثم:

```go
engine, err := sdk.NewBuilder(sdk.WithStoragePorts(storage.Ports{
    Drafts: adapter, Entries: adapter, Posting: adapter,
    Sequences: adapter, MasterData: adapter,
})).Build(ctx)
```

ويُرحّل قيدًا فعليًا ويقرأه. هذا يثبت أن Application Layer تُبنى من Ports العامة
ولا تطلب من التخزين إنشاء Service.

## 7. الثبات والتوافق

- أي إضافة إلى واجهة عامة = MINOR.
- أي حذف/تغيير توقيع/تغيير دلالة = MAJOR.
- `docs/api/v1.snapshot` أساس فحص التوافق في CI.
- لا تُضاف أنواع `internal` أو `pgx` أو أسماء جداول إلى أي عقد عام.

## 8. فجوات معروفة تُغلق لاحقًا

| الفجوة | الأثر | المهمة |
| --- | --- | --- |
| لا يوجد Port لقراءة الدفتر بالرمز | بناء قيد حدث بقاعدة فيها `JournalCode` يحتاج MasterDataReader موسّعًا | `P2-06` |
| Idempotency الحدث غير موصول بـ `storage.IdempotencyStore` في وضع Ports | وضع Ports يستخدم Idempotency داخل العملية فقط | `P2-06` |
| `ReverseWithReasonContext` يحفظ مسودة ثم يرحّل العكس في معاملتين | مسودة يتيمة عند فشل الترحيل | `P2-05` |
| `application.Service` غير موصول بـ SDK | الأوامر/التفويض غير مطبقة على المسار العام | `P2-01`, `P2-02` |
| Standards تتطلب `WithCoreStorage` | لا تعمل في وضع Ports | `P3-02` |
