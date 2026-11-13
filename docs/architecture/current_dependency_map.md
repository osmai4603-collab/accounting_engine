# خريطة الاعتماديات الحالية — Accounting Engine

- **المصدر:** تحليل ساكن لملفات Go في نسخة `engine5.zip` بتاريخ 2026-11-13.
- **المنهج:** استخراج package paths وimports من ملفات `.go`؛ لا يتطلب تشغيل Go toolchain.
- **الحدود:** الخريطة تثبت imports المباشرة فقط؛ لا تثبت مسارات الاستدعاء وقت التشغيل ولا الاعتماديات الديناميكية.

## 1. مجموعات الحزم

### `pkg/api/v1` (4 packages)

- `accounting_engine/pkg/api/v1/events` (3 Go files)
- `accounting_engine/pkg/api/v1/ledger` (3 Go files)
- `accounting_engine/pkg/api/v1/reporting` (1 Go files)
- `accounting_engine/pkg/api/v1/types` (5 Go files)

### `pkg/sdk` (2 packages)

- `accounting_engine/pkg/sdk` (7 Go files)
- `accounting_engine/pkg/sdk/templates` (3 Go files)

### `pkg/spi` (4 packages)

- `accounting_engine/pkg/spi/events` (1 Go files)
- `accounting_engine/pkg/spi/masterdata` (1 Go files)
- `accounting_engine/pkg/spi/standards` (2 Go files)
- `accounting_engine/pkg/spi/storage` (5 Go files)

### `internal` (21 packages)

- `accounting_engine/internal/apperrors` (2 Go files)
- `accounting_engine/internal/consolidation` (8 Go files)
- `accounting_engine/internal/events` (3 Go files)
- `accounting_engine/internal/governance` (18 Go files)
- `accounting_engine/internal/ias12` (7 Go files)
- `accounting_engine/internal/ifrs15` (4 Go files)
- `accounting_engine/internal/ifrs16` (6 Go files)
- `accounting_engine/internal/ifrs9` (5 Go files)
- `accounting_engine/internal/ifrsautomation` (8 Go files)
- `accounting_engine/internal/masterdata` (16 Go files)
- `accounting_engine/internal/observability` (7 Go files)
- `accounting_engine/internal/policy` (2 Go files)
- `accounting_engine/internal/posting` (21 Go files)
- `accounting_engine/internal/reconciliation` (2 Go files)
- `accounting_engine/internal/reporting` (30 Go files)
- `accounting_engine/internal/reporting/taxonomy` (6 Go files)
- `accounting_engine/internal/sdkapplication` (3 Go files)
- `accounting_engine/internal/subledgers` (23 Go files)
- `accounting_engine/internal/subledgers/bank_parsers` (6 Go files)
- `accounting_engine/internal/types` (6 Go files)
- `accounting_engine/internal/valuation` (8 Go files)

### `storage` (3 packages)

- `accounting_engine/storage/masterdatacontract` (1 Go files)
- `accounting_engine/storage/memory` (25 Go files)
- `accounting_engine/storage/postgres` (56 Go files)

### `modules` (4 packages)

- `accounting_engine/modules/ias12` (2 Go files)
- `accounting_engine/modules/ifrs15` (2 Go files)
- `accounting_engine/modules/ifrs16` (2 Go files)
- `accounting_engine/modules/ifrs9` (2 Go files)

### `cmd` (2 packages)

- `accounting_engine/cmd/accounting-service` (6 Go files)
- `accounting_engine/cmd/agy` (5 Go files)

### `proto` (1 packages)

- `accounting_engine/proto/accounting/v1` (6 Go files)

### `test` (3 packages)

- `accounting_engine/test/acid` (3 Go files)
- `accounting_engine/test/external_consumer` (1 Go files)
- `accounting_engine/test/service_integration` (1 Go files)

## 2. الاعتماديات بين حدود المشروع

| المصدر | يستورد مباشرة |
| --- | --- |
| `pkg/api/v1` | `pkg/api/v1/ledger`, `pkg/api/v1/types` |
| `pkg/sdk` | `internal/masterdata`, `internal/posting`, `pkg/api/v1/events`, `pkg/api/v1/ledger`, `pkg/api/v1/reporting`, `pkg/api/v1/types`, `pkg/sdk`, `pkg/sdk/templates`, `pkg/spi/events`, `pkg/spi/masterdata`, `pkg/spi/standards`, `pkg/spi/storage`, `storage/memory`, `storage/postgres` |
| `pkg/spi` | `pkg/api/v1/events`, `pkg/api/v1/ledger`, `pkg/api/v1/reporting`, `pkg/api/v1/types`, `pkg/spi/storage` |
| `internal` | `internal/apperrors`, `internal/consolidation`, `internal/governance`, `internal/ias12`, `internal/ifrs15`, `internal/ifrs16`, `internal/ifrs9`, `internal/ifrsautomation`, `internal/masterdata`, `internal/observability`, `internal/policy`, `internal/posting`, `internal/reporting`, `internal/reporting/taxonomy`, `internal/subledgers`, `internal/types`, `internal/valuation`, `pkg/api/v1/events`, `pkg/api/v1/ledger`, `pkg/api/v1/types`, `pkg/spi/storage`, `storage/memory`, `storage/postgres` |
| `storage` | `internal/governance`, `internal/ifrs15`, `internal/masterdata`, `internal/observability`, `internal/policy`, `internal/posting`, `internal/reporting`, `internal/reporting/taxonomy`, `internal/sdkapplication`, `internal/subledgers`, `internal/types`, `internal/valuation`, `pkg/api/v1/ledger`, `pkg/api/v1/reporting`, `pkg/api/v1/types`, `pkg/spi/events`, `pkg/spi/masterdata`, `pkg/spi/storage`, `storage/masterdatacontract`, `storage/memory`, `storage/postgres` |
| `modules` | `pkg/api/v1/events`, `pkg/api/v1/ledger`, `pkg/api/v1/types`, `pkg/sdk`, `pkg/spi/masterdata`, `pkg/spi/standards`, `pkg/spi/storage`, `storage/memory` |
| `cmd` | `internal/ifrs15`, `internal/ifrs16`, `internal/ifrsautomation`, `internal/masterdata`, `internal/observability`, `internal/posting`, `internal/subledgers`, `internal/types`, `pkg/api/v1/events`, `pkg/api/v1/ledger`, `pkg/api/v1/reporting`, `pkg/api/v1/types`, `pkg/sdk`, `pkg/spi/storage`, `proto/accounting/v1`, `storage/memory`, `storage/postgres` |

## 3. Imports داخل الحزم العامة

### `pkg/api/v1`

- `accounting_engine/pkg/api/v1/events` → `pkg/api/v1/ledger`, `pkg/api/v1/types`
- `accounting_engine/pkg/api/v1/ledger` → `pkg/api/v1/types`
- `accounting_engine/pkg/api/v1/reporting` → `pkg/api/v1/types`
- `accounting_engine/pkg/api/v1/types` → لا توجد imports داخلية من الوحدة

### `pkg/sdk`

- `accounting_engine/pkg/sdk` → `internal/masterdata`, `internal/posting`, `pkg/api/v1/events`, `pkg/api/v1/ledger`, `pkg/api/v1/reporting`, `pkg/api/v1/types`, `pkg/sdk`, `pkg/spi/events`, `pkg/spi/masterdata`, `pkg/spi/standards`, `pkg/spi/storage`, `storage/memory`, `storage/postgres`
- `accounting_engine/pkg/sdk/templates` → `pkg/api/v1/events`, `pkg/api/v1/types`, `pkg/sdk`, `pkg/sdk/templates`, `pkg/spi/masterdata`, `pkg/spi/standards`, `pkg/spi/storage`, `storage/memory`

### `pkg/spi`

- `accounting_engine/pkg/spi/events` → `pkg/api/v1/ledger`
- `accounting_engine/pkg/spi/masterdata` → `pkg/api/v1/types`
- `accounting_engine/pkg/spi/standards` → `pkg/api/v1/events`, `pkg/spi/storage`
- `accounting_engine/pkg/spi/storage` → `pkg/api/v1/ledger`, `pkg/api/v1/reporting`, `pkg/api/v1/types`

## 4. ملاحظات حدودية مثبتة من الكود

- `pkg/sdk` يعتمد على عقود `pkg/api/v1` و`pkg/spi`، لكنه يطلب من `CoreLedgerStore` تنفيذ `LedgerApplicationProvider` عبر Type Assertion، كما يطلب `MasterDataStore()` عبر Type Assertion إضافي في `pkg/sdk/builder.go`.
- `pkg/spi/storage.CoreLedgerStore` يضم `PostEntry` إلى جانب عمليات القراءة والحفظ؛ وهذا يخلط أمرًا تطبيقيًا بواجهة التخزين.
- `pkg/spi/storage.LedgerApplicationProvider` يجعل الـ Store موفرًا لـ Application Service؛ أي أن اتجاه الإنشاء معكوس عن هدف أن ينشئ SDK التطبيق من Ports.
- `pkg/spi/standards.StandardModule` يعتمد على `pkg/spi/storage.CoreLedgerStore`، ما يربط واجهة الوحدات المعيارية بعقد التخزين المركب الحالي.
- Root `engine.go` و`engine_observability.go` يقدمان API منفصلًا عن `pkg/sdk`; يلزم فحص أنواعها وتبعياتها قبل تقرير إزالتها أو تحويلها إلى واجهة توافقية.
- `internal/sdkapplication` و`internal/posting` و`internal/masterdata` تبقى تفاصيل تنفيذ داخلية، ولا ينبغي نقل أنواعها كما هي إلى Public SPI.
- يوجد فصل مبدئي بين `storage/postgres` و`storage/memory`، لكن ذلك لا يثبت إمكان بناء Adapter خارجي مستقل.

## 5. مسارات يجب تتبعها في P1/P2

1. `pkg/sdk/builder.go` → `storage.CoreLedgerStore` → `LedgerApplicationProvider` → `internal/sdkapplication`.
2. `pkg/sdk/builder.go` → Type Assertion `MasterDataStore()` → `pkg/spi/masterdata.Store`.
3. `pkg/sdk` → `pkg/spi/standards` → `pkg/spi/storage`.
4. Root `engine.go` → internal services and storage ports.
5. SDK event ingestion → event mapping → journal/account resolution → `internal/sdkapplication` → Posting repository.

## 6. القيود

- لم يتمكن `go list ./...` من العمل لأن `go.mod` يطلب Go toolchain `1.26.3` ولم يكن متاحًا محليًا، كما تعذر تنزيله بسبب عدم توفر DNS/اتصال بـ `proxy.golang.org`.
- لذلك هذه خريطة imports ساكنة وليست ناتج `go list` أو `go mod graph`.
- لا تُستخدم هذه الخريطة وحدها لاتخاذ قرار حذف حزمة أو تغيير عقد عام.
