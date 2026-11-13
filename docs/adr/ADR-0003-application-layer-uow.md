# ADR-0003 — Application Layer واحدة وPorts/Adapters وUnit of Work

- **الحالة:** معتمد
- **التاريخ:** 2026-11-14
- **يخدم:** `P0-04`, `P1-02`, `P1-03`, `P1-04`, `P2-02`, `P3-01`

## السياق

حاليًا `storage/*` ينشئ Application Service عبر `LedgerApplicationProvider` وType Assertion، و`pkg/sdk` يحتاج Storage يوفّر `NewLedgerApplication()`. النتيجة أن Adapter يملك منطق التطبيق، وأن المستهلك لا يستطيع تركيب مكوّناته بحرية. كما أن حدود المعاملة غير معرّفة في العقد العام.

## البدائل

1. إبقاء Application داخل Storage: يخلط المسؤوليات ويمنع Adapter خارجيًا حقيقيًا.
2. توليد Application من كل Transport: يكرر القواعد ويجعل السلوك يختلف حسب المدخل.
3. Application Layer داخل المحرك + Public Ports + Composition Root في SDK. (المختار)

## القرار

1. حزمة تطبيق واحدة داخلية `internal/application` (نقل/إعادة استخدام `internal/sdkapplication` بقرار P2-02) تنفذ Create/Read/Validate/Post/Reverse/Events.
2. Application تعتمد على Public Ports فقط عبر محولات داخلية؛ لا تعرف `pgx` أو SQL أو جداول.
3. Ports مقسمة:
   - `pkg/spi/storage`: Ledger, Entries, Accounts, Journals, Periods, Balances, Sequences, Audit, Idempotency, Transaction Runner.
   - `pkg/spi/application`: Commands/Queries/Results/Errors والتطبيق الاختياري للأحداث.
   - `pkg/spi/integration`: الأحداث الواردة وOutbox/Webhooks، منفصلة عن Persistence.
4. `UnitOfWork`/`TransactionRunner` عقد محايد:
   - `WithinTransaction(ctx, fn func(ctx) error) error`
   - لا تُمرَّر اتصالات أو `pgx.Tx` للمستهلك.
   - Adapter يعلن مستوى العزل وضمانات الذرية والقفل.
5. Post/Reverse/Idempotency/Sequence/Balances/Audit تُنفَّذ داخل حدود معاملة واحدة في Adapter.
6. إنشاء `posting.Engine` يحدث في Composition Root واحد داخل SDK فقط.

## النتائج

- `pkg/sdk.EngineBuilder` يستقبل Ports صريحة (`WithCorePorts`, `WithReportingPorts`, ...) لا Store مركبًا.
- `CoreLedgerStore` المركب و`LedgerApplicationProvider` يُستبدلان بـ Ports صغيرة.
- Memory وPostgreSQL ينفذان نفس Ports؛ Contract Tests المشتركة تثبت التكافؤ.
- Adapter خارجي لا يستورد `internal` ولا ينفذ منطق محاسبة.

## معايير التحقق

- Architecture Test يمنع `internal/posting` في `cmd` و`pkg/sdk` مباشرة.
- Contract Tests تثبت Commit/Rollback وفشل callback على كل Adapter.
- مراجعة تصميم من منظور مطور خارجي موثقة في `docs/public_contracts_design.md`.
