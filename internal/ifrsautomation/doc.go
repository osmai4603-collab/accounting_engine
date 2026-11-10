// Package ifrsautomation coordinates recurring IFRS accounting work.
//
// Its job definitions describe scheduled accounting tasks, while the monthly
// workflow organizes the operations that need to run for a reporting period.
// The package is an orchestration layer: it connects domain calculations and
// accounting services to a recurring process, without replacing those
// services' validation, authorization, or persistence responsibilities.
//
// Callers configure the available job dependencies, select a company and
// period, and execute the relevant monthly workflow. Job and workflow errors
// are surfaced to the caller so that failures can be monitored and retried
// according to application policy; this package does not silently mark
// incomplete accounting work as successful.
package ifrsautomation
