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
// Repository and policy interfaces keep persistence and company-level rules
// outside the calculation engine. Context-aware methods propagate
// cancellation through reads and writes. Callers create a draft, complete
// required approval, and post it with an identified poster; failed atomic
// writes leave the entry in draft state. Validation and control failures are
// returned explicitly.
package posting
