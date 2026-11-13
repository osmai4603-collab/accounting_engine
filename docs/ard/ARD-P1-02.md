# `ARD-P1-02` — تصميم عقود Public API وPublic SPI

- **المهمة:** `P1-02`
- **المرحلة:** `P1`
- **تاريخ الإتمام:** 2026-11-14
- **الحالة عند الإغلاق:** منفذة

## 1. كيف تم التنفيذ

- كتابة `docs/public_contracts_design.md` بمخطط تبعيات، وجدول لكل Port مع
  ضماناته، وقائمة Capabilities الاختيارية، وسياسة التوافق.
- فصل `pkg/spi/application` (Principal/Authorizer/Commands/Queries/Errors) عن
  `pkg/spi/storage` (Ports فقط).
- نقل `CoreLedgerStore` و`LedgerApplication*` إلى `pkg/spi/legacy` المهمَل، ما
  يحقق شرط ألا تحتوي Storage SPI على Post/Reverse أو إنشاء Application Service.
- تعريف `storage.TransactionRunner` و`IsolationLevel` دون كشف `pgx.Tx` أو `sql.Tx`.
- تعريف `MasterDataReader` قراءةً فقط، وترك Provisioning في `pkg/spi/masterdata.Store`.
- تعريف `storage.Ports.Validate()` الذي يجمع كل Port مفقود.
- كتابة مثال Adapter خارجي كامل في `test/external_consumer/ports_adapter_test.go`
  لا يستورد أي `internal` ولا يعرف SQL.

## 2. إثبات معايير القبول

| معيار القبول | الدليل | النتيجة |
| --- | --- | --- |
| وجود `docs/public_contracts_design.md` ومخطط تبعيات | الملف يحتوي مخططًا نصيًا وجداول عقود | محقق |
| لا تتضمن العقود أنواع `internal`/`pgx`/SQL | `check_public_api_purity.sh` و`internalapi` يمران | محقق |
| لا تحتوي Storage SPI على Post/Reverse/إنشاء Service | `go doc ./pkg/spi/storage` لا يعرض `LedgerApplication*`؛ انتقلت إلى `pkg/spi/legacy` | محقق |
| كل Capability اختيارية لها عقد مستقل | Reporting/Governance/Balance/Audit/Idempotency/ExchangeRate منفصلة | محقق |
| مراجعة من منظور مطور خارجي | اختبار خارجي ينفذ `storage.Ports` ويرحّل قيدًا فعليًا | محقق |

## 3. النواقص

- `application.Service` موثّق لكنه غير موصول بـ`pkg/sdk` بعد؛ الربط في `P2-01`/`P2-02`.
- لا يوجد Port لقراءة الدفتر بالرمز؛ مذكور كفجوة في التصميم ومهمة `P2-06`.
- Idempotency الأحداث غير موصول بـ`storage.IdempotencyStore` في وضع Ports.

## 4. ملاحظات

- التصميم يرفض صراحة أي تحويل صامت للعملة أو التاريخ أو الهوية، وأي توليد
  اصطناعي لمعرّفات القيود.
- `PostingCommit` يفصل `ActorID`/`Reason`/`CorrelationID` عن نموذج القيد المخزَّن.
