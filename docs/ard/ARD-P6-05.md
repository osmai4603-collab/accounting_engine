# ARD-P6-05: HTTP Validation, Error Handling, and Service Boundaries

- **Task:** `P6-05`
- **Status:** Implemented for the optional HTTP service
- **Scope:** `cmd/accounting-service` HTTP handlers and middleware

## Context and release form

The HTTP service is an optional standalone deployment. This work hardens its existing boundary without changing ADR-0006: the first release remains the embedded Go SDK, and HTTP/gRPC are not made release requirements. The existing authentication boundary remains responsible for verified caller identity and company context. No business role or permission checks were added to handlers.

## Decisions and implementation

- The supported HTTP routes are `POST /api/v1/entries`, `POST /api/v1/events`, `GET /api/v1/trial-balance`, `GET /healthz`, `GET /readyz`, and `/metrics`.
- Entry requests require a non-zero company ID, are capped at 500 lines, and bound caller-controlled text fields. Event requests require event ID/type and effective date, cap dimensions and payload fields at 64, and bound dimension and metadata strings. Trial-balance requests require exactly one non-zero company UUID and a supported three-letter currency. Existing domain validation remains authoritative for accounting rules.
- JSON request bodies are capped at 1 MiB. HTTP server limits remain 5 seconds for headers, 15 seconds for reads, 30 seconds for writes, and 120 seconds for idle connections. API request contexts have a 25-second deadline; cancellation and deadline errors are translated to structured HTTP errors.
- `/api/v1/*` is limited to 60 requests per minute per direct peer IP. `Retry-After` is returned on throttling. Forwarded-IP headers are not trusted.
- Errors use `{"error":{"code":"...","message":"..."},"correlation_id":"..."}`. A valid incoming `X-Correlation-ID` or `X-Request-ID` is retained; otherwise a UUID is generated and returned in `X-Correlation-ID`. Internal errors do not expose causes, SQL, stack traces, or secrets.
- Responses set `Cache-Control: no-store`, `X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`, and a restrictive Content Security Policy. HSTS is set only when TLS is enabled/visible to the service. CORS remains disabled; TLS termination, HSTS, and any deployment-specific CORS policy belong to the trusted reverse proxy.

## Verification

The HTTP service tests passed with:

```sh
go test cmd/accounting-service/server.go cmd/accounting-service/boundary.go cmd/accounting-service/config.go cmd/accounting-service/http_middleware.go cmd/accounting-service/http_validation.go cmd/accounting-service/server_test.go
```

This includes malformed and boundary input, error schema/correlation IDs, authentication failures, request limits, timeout propagation, and client cancellation.

The normal package command `go test ./cmd/accounting-service` could not run the suite: package initialization panics in the currently modified generated protobuf descriptor (`proto/accounting/v1/ledger.pb.go`) before tests execute. Those gRPC/protobuf edits were not changed by this task. Re-run the package suite after that in-progress P6-04 generated code is valid.

## Acceptance status

HTTP handler and middleware behavior above is implemented and verified in isolation. Full package integration remains unverified because of the protobuf initialization failure. The first-release scope is unchanged; this work does not promote the optional HTTP service into the embedded-SDK release gate.
