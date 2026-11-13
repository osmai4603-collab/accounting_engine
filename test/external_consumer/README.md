# External consumer contract check

This independent Go module exercises company/account/fiscal-year/fiscal-period
setup through `pkg/spi/masterdata`, then posts and reads a journal entry through
`pkg/sdk` using the public `pkg/api/v1` DTOs. The in-memory storage package is
used only as the backend fixture; the consumer imports no `internal/*` package.
The SDK currently requires a backend that also implements internal posting and
master-data repository ports, so this fixture uses the repository's exported
`storage/memory` driver; a backend implemented solely from public SPI contracts
cannot yet be supplied to `sdk.NewBuilder`.

Run from this directory:

```sh
go build ./...
go test ./...
```
