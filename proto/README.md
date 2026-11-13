# Protocol Buffers

The `.proto` files are the source of truth for the public gRPC API. To
reproducibly generate Go client and server bindings, install:

- `protoc` 27.3 (`libprotoc 27.3`)
- `protoc-gen-go` v1.36.12
- `protoc-gen-go-grpc` 1.5.1

Then run `./proto/generate.sh` from any working directory. It writes the
generated bindings beside the source definitions under `proto/accounting/v1`.
The generated Go message, client, and server bindings are checked in. Keep
them in sync with the `.proto` files by running the script with the pinned
tool versions; the script rejects version mismatches.

The accounting-service registers the generated accounting, reporting, and
event server handlers and serves protobuf reflection from their generated
descriptors. Go callers can use the generated clients, while non-Go callers
can use the same public protobuf contract.
