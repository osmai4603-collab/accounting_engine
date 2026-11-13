# 📚 Universal Accounting Engine SDK & Daemon Developer Guide (`v1.0.0-standalone`)

Welcome to the Universal Accounting Engine. This guide covers how to integrate, embed, and deploy the accounting engine either as a native Go library via `pkg/sdk` or as an independent daemon service via gRPC and REST.

---

## 🚀 1. Embedding as a Go Library (`pkg/sdk`)

To use the accounting engine directly in your Go application:

```go
package main

import (
 "context"
 "time"

 "accounting_engine/pkg/api/v1/ledger"
 "accounting_engine/pkg/api/v1/types"
 "accounting_engine/pkg/sdk"
 "accounting_engine/storage/memory"
 "accounting_engine/modules/ifrs15"
)

func main() {
 ctx := context.Background()
 store := memory.New()

 engine, err := sdk.NewBuilder(
  sdk.WithCoreStorage(store),
  sdk.WithReporting(store),
  sdk.WithGovernance(store),
  sdk.WithStandard(&ifrs15.Module{}),
 ).Build(ctx)
 if err != nil {
  panic(err)
 }
 defer engine.Close()

 companyID := types.NewCompanyID()
 amt, _ := types.NewMoneyFromString("500.00", "USD")

 entry := ledger.JournalEntry{
  ID:            types.NewJournalEntryID(),
  CompanyID:     companyID,
  Status:        ledger.EntryPosted,
  EffectiveDate: time.Now().UTC(),
  Description:   "Consulting Revenue",
  Lines: []ledger.EntryLine{
   {ID: types.NewAccountID(), AccountID: types.NewAccountID(), FunctionalDebit: amt, TransactionAmount: amt, TransactionCurrency: "USD"},
   {ID: types.NewAccountID(), AccountID: types.NewAccountID(), FunctionalCredit: amt, TransactionAmount: amt, TransactionCurrency: "USD"},
  },
 }

 posted, err := engine.Ledger().PostEntry(ctx, entry)
 if err != nil {
  panic(err)
 }
 println("Posted entry ID:", posted.ID.String())
}
```

---

## 🔌 2. Running as a Standalone Daemon (gRPC & REST)

Start the accounting service using Docker Compose:

```bash
cd deployments
# Set POSTGRES_PASSWORD and DATABASE_DSN through your environment or secret manager.
docker compose up --build
```

Keep these values out of source control and build logs.

### Endpoints

- **Health Check:** `GET /healthz`
- **Readiness:** `GET /readyz`
- **Metrics:** `GET /metrics`
- **Post Journal Entry:** `POST /api/v1/entries`
- **Get Trial Balance:** `GET /api/v1/trial-balance?company_id=<UUID>&currency=USD`
- **Process Event:** `POST /api/v1/events`
- **gRPC Service:** Port `50051` (`proto/accounting/v1/ledger.proto`, `events.proto`, `reporting.proto`)
