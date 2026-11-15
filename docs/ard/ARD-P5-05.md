# ARD-P5-05: Bounded IFRS 15 Revenue Workflow

## Status

Implemented as a limited, opt-in workflow. This is not a representation of full IFRS 15 compliance and has not received independent accounting or legal review.

## Decision and supported scope

The workflow accepts host-assessed contract facts, validates the supported five-step inputs, allocates a fixed transaction price using relative standalone selling prices, and recognizes a host-measured satisfaction event through an explicitly configured journal mapping and application-backed posting capability.

| IFRS 15 step | Inputs and validation | Measurement / output | Evidence |
|---|---|---|---|
| 1. Identify the contract | Host asserts approval, enforceable rights, payment terms, commercial substance, and probable collectibility. Required identifiers must be present. | A validated contract input; the engine does not make legal or credit judgments. | `FiveStepAssessment`, `NewAssessedContract`; `TestAssessedContractValidatesFiveStepInputs` |
| 2. Identify performance obligations | Host identifies each distinct obligation; each input must have a unique ID, description, positive standalone selling price, and supported recognition pattern. | A set of obligations for relative-SSP allocation. | `DistinctObligationIDs`, `NewAssessedContract`; `TestAssessedContractRejectsObligationSetMismatch` |
| 3. Determine transaction price | Host asserts the price is fixed. Variable consideration and a significant financing component are rejected. | A positive, currency-consistent transaction price. | `FixedTransactionPrice`, `HasVariableConsideration`, `HasSignificantFinancing`; `TestAssessedContractValidatesFiveStepInputs` |
| 4. Allocate transaction price | Each distinct obligation has a positive standalone selling price in the transaction-price currency. | Relative-SSP allocation with currency-minor-unit rounding; residual units are assigned deterministically so the allocations equal the price. | `NewContract`, `allocate`; `TestRelativeSSPWeightedAllocation`, `TestAllocationRoundingSumsExactly` |
| 5. Recognize revenue | Host supplies a unique event ID, obligation, positive measured amount, date, explicit satisfaction assertion, measurement basis, and evidence reference. | The host-measured amount is journaled against revenue and either contract asset or liability; no delivery or progress is inferred from dates. State is confirmed only after the configured poster succeeds. | `ServiceDeliveryEvidence`, `RevenueWorkflow.Recognize`; `TestRecognitionCommitsOnlyAfterPostingAndRetriesIdempotently` |

Daily/monthly schedules in the existing measurement API are arithmetic schedules only. They are not proof that a service was delivered or that a performance obligation was satisfied.

## Posting, retry, and state handling

`RevenueWorkflow` cannot be constructed without non-zero company, journal, revenue, contract-asset, and contract-liability IDs and a `RevenuePostingApplication`. That interface is an application-backed capability supplied by the host; it must persistently honor the supplied idempotency key and reject a changed payload for a reused key.

Recognition validates and builds the balanced draft before posting, then updates in-memory contract and per-obligation amounts only after the application returns success. A failed call leaves recognized state unchanged. A retry in the same process reuses the event key and exact draft; a successful duplicate returns the original result without posting again. The journal identifier is stable for a contract/event pair.

This package does not provide durable workflow storage, a transactional outbox, or recovery of in-memory pending/confirmed state after process loss. Production hosts must combine durable contract-state handling with an idempotent application poster; this implementation alone does not provide cross-system atomicity. Existing `Unit` draft APIs remain available for compatibility and are not the state-after-posting workflow; use `RevenueWorkflow` where that guarantee is required.

## Unsupported areas and review limitations

- Variable consideration and its constraint or subsequent updates.
- Significant financing components.
- Contract modifications, combination/termination accounting, and contract acquisition costs.
- Principal-versus-agent analysis, non-cash consideration, consideration payable to customers, and rights of return.
- Detailed control-transfer assessment, acceptance, and progress measurement: those judgments/evidence are provided by the host, not determined by the engine.
- Durable contract persistence, transactional posting/state atomicity, and process-restart recovery.
- Edition-, amendment-, jurisdiction-, and effective-date-specific interpretation.
- Full contract asset/liability presentation, billing/invoicing, and disclosure requirements.

The implementation provides targeted tests and arithmetic golden cases only. No external accountant or legal reviewer has validated the assumptions or the accounting treatment.
