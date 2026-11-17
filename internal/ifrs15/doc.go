// Package ifrs15 implements IFRS 15 Revenue from Contracts with Customers.
//
// The package supports the five-step revenue recognition model:
//  1. Identify the contract(s) with a customer.
//  2. Identify the performance obligations in the contract.
//  3. Determine the transaction price, including estimation and constraining of variable
//     consideration (IFRS 15.56-58) and adjustment for significant financing components (IFRS 15.60-65).
//  4. Allocate the transaction price to the performance obligations based on relative standalone selling prices.
//  5. Recognize revenue when (or as) the entity satisfies a performance obligation.
//
// Contract modifications (IFRS 15.18-21) are supported across all three standard treatments:
//   - Separate contract (IFRS 15.20): additional distinct goods/services at standalone selling prices.
//   - Prospective treatment (IFRS 15.21(a)): termination of existing contract and creation of new contract.
//   - Cumulative catch-up (IFRS 15.21(b)): single obligation with revised progress and price.
//
// Durable contract lifecycle states (Draft, Active, Modified, Satisfied) are persisted via ContractStore,
// enabling recovery and resumption of recognition across process restarts.
//
// Explicit Exclusions:
//   - Principal versus agent evaluations (IFRS 15.B34-B38) are not determined by the engine.
//   - Complex intellectual property licensing evaluations (IFRS 15.B52-B63) are excluded.
package ifrs15
