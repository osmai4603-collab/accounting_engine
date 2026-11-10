// Package governance provides accounting controls and evidence services.
//
// Its components cover role-based access checks, segregation-of-duties
// policies, maker-checker approvals, financial audit records, tamper-evident
// audit chaining and verification, audit export, and write-once storage
// backends. Engine composes the audit-chain service over a Repository; callers
// append audit events as operations occur and can later verify the stored
// chain's integrity.
//
// Approval and authorization checks are designed to be invoked at the
// corresponding business-operation boundary, rather than after a transaction
// has been committed. Audit exporters can write immutable objects through a
// WORM implementation, including local and S3 Object Lock backends. Repository
// and WORM implementations own persistence and external-resource lifetimes;
// this package returns errors from policy, storage, and verification steps so
// the caller can fail closed where appropriate.
package governance
