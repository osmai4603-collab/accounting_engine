# ARD-P5-03: Safe Lifecycle Teardown Rollback and Scheduled Jobs Framework

## Status

Implemented.

## Context and Problem

If module initialization failed after previous modules had already been initialized during engine bootstrap, partially initialized resources could remain dangling without cleanup. Furthermore, `Close()` was not guaranteed to be idempotent, and there was no unified scheduler framework enforcing job idempotency, readiness checks, or execution records.

## Decision

1. **Reverse Teardown Rollback on Initialization Failure**:
   - `EngineBuilder.initializeStandards` tracks initialized modules. If any module fails `Initialize()` or `RegisterRules()`, all previously initialized modules are torn down in reverse topological order (`Close(ctx)`), accumulating any close errors into a single result.

2. **Idempotent Engine Shutdown & Context Awareness**:
   - `Engine.Close()` and `Engine.CloseWithContext(ctx)` use `sync.Once` to guarantee idempotency across multiple calls.
   - Modules are torn down in reverse topological order, respecting context cancellation/deadline.

3. **Scheduled Jobs Framework & Idempotency Policy**:
   - Introduced `SchedulerPort`, `DefaultScheduler`, `JobIdempotencyPolicy`, and `JobExecutionRecord` in `pkg/spi/standards`.
   - Scheduled jobs cannot run before core dependencies and engine bootstrap are complete (`IsReady()` guard).
   - Idempotency policies prevent duplicate concurrent or repeated execution of accounting jobs.

## Verification

- `TestP5_03_Lifecycle_TeardownRollbackOnInitializationFailure` proves reverse teardown rollback when a downstream module initialization fails.
- `TestP5_03_Lifecycle_IdempotentCloseAndContextCancellation` proves `Close()` idempotency and context propagation.
- `TestScheduler_NotReady` and `TestScheduler_ExecutionAndIdempotency` in `pkg/spi/standards/scheduler_test.go` prove readiness checks, idempotency locking, execution history tracking, and job error reporting.
