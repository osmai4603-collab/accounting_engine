// Package posting manages journal entries, journal lines, and financial
// dimensions through their draft-to-posted lifecycle.
//
// Engine validates drafts and their accounting dimensions, checks posting
// eligibility and applicable approvals, assigns posting metadata and
// sequencing, then delegates the commit to repository atomic-posting
// operations. Posted entries are immutable; correction is represented by a
// reversal rather than rewriting the original. The package also supports
// bulk posting, receivable or payable open-line attachment, and dimension
// policy validation.
//
// A configured policy unit selects currency and posting rules as of the
// entry's effective date. It checks allowed currencies, ISO minor-unit
// rounding, backdating, closed periods, and configured analytic assignments
// before the atomic repository write. Hard-closed periods are never bypassed;
// soft-close and backdating exceptions require their respective authorizers.
//
// Repository and policy interfaces keep persistence and company-level rules
// outside the calculation engine. Context-aware methods propagate
// cancellation through reads and writes. Callers create a draft, complete
// required approval, and post it with an identified poster; failed atomic
// writes leave the entry in draft state. Validation and control failures are
// returned explicitly.
package posting
