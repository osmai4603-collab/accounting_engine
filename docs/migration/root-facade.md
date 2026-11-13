# الهجرة من Root API إلى `pkg/sdk`

- **المرحلة:** `P1-05`
- **الحالة:** الجذر أصبح Facade مهمَلًا؛ الإزالة في أول MAJOR بعد استقرار SDK.
- **المرجع:** `docs/adr/ADR-0001-public-entry-point.md`

## ما تغيّر

| قبل | بعد |
| --- | --- |
| `accounting_engine.New(driver, cfg)` | `sdk.NewBuilder(sdk.WithStoragePorts(driver.Ports())).Build(ctx)` |
| `engine.Posting`, `engine.MasterData`, `engine.Reporting` (أنواع داخلية) | `engine.Ledger()`, `engine.Reporting()`, `engine.Governance()`, `engine.Events()` |
| `engine.Config.Standards` بأنواع `ifrs9.Unit` وغيرها | وحدات معايير عبر `sdk.WithStandard` في مسار `WithCoreStorage` فقط حاليًا |
| `accounting_engine.StorageDriver` | `storage.Ports` من `pkg/spi/storage` |
| `accounting_engine.ObservabilityConfig` | إعداد المراقبة في المضيف/الموائم، لا في عقد المحرك |

## مثال الهجرة

```go
// قبل
import accountingengine "github.com/osmai4603-collab/accounting_engine"

driver := memory.New()
engine, err := accountingengine.New(driver, accountingengine.DefaultConfig())
posted, err := engine.Posting.PostEntryContext(ctx, entry, "actor")

// بعد (موصى به)
import (
    "github.com/osmai4603-collab/accounting_engine/pkg/sdk"
    "github.com/osmai4603-collab/accounting_engine/storage/memory"
)

adapter := memory.New()
engine, err := sdk.NewBuilder(sdk.WithStoragePorts(adapter.Ports())).Build(ctx)
posted, err := engine.Ledger().PostEntry(ctx, publicEntry)
```

## الرموز المهمَلة

- كل رمز في الجذر: `Engine`, `EngineBuilder`, `Option`, `With*`, `NewBuilder`,
  وأخطاء الأحداث. كلها مرادفات أو أغلفة رقيقة لـ`pkg/sdk`، ولا تحتوي منطقًا.
- `pkg/spi/legacy`: `CoreLedgerStore`, `LedgerApplication`, `LedgerApplicationProvider`.
  ليست جزءًا من Storage SPI ولا تُستخدم في مسار Ports.

## ما أُزيل من الجذر

- `New`, `NewEngine`, `NewWithPorts`, `Config`, `DefaultConfig`,
  `StorageDriver`, `StoragePorts`, `ObservabilityConfig`, `PolicyConfig`,
  `StandardsConfig`, `AccountingEngine`, وكل الحقول/الطرق التي كانت تعرض أنواعًا
  داخلية. التنفيذ الكامل محفوظ في `internal/legacyengine` لاستخدام `cmd/agy`
  والاختبارات الداخلية فقط.

## قواعد الاستمرار

- يُمنع أن يستورد `pkg/sdk` أو `internal/**` الجذر.
- `go doc -all .` لا يعرض أي مسار `internal/`.
- أي رمز جذري جديد يجب أن يكون مرادفًا عامًا فقط، وإلا يُرفض في مراجعة API.
