// Package types contains shared financial value types and domain errors.
//
// Money pairs a decimal amount with a currency and provides validated
// arithmetic that keeps currency compatibility explicit. Currency definitions
// and the registry expose supported currency metadata, including minor-unit
// precision. Shared errors identify financial state and validation failures
// used across accounting packages.
//
// Construct values through the package constructors and use the provided
// arithmetic rather than binary floating-point operations. Monetary results
// are rounded according to currency precision where appropriate; mismatched
// currencies and invalid values are reported as errors. This package has no
// persistence or service lifecycle.
package types
