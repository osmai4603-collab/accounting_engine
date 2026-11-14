module github.com/osmai4603-collab/accounting_engine/test/external_adapter_consumer

go 1.26.0

require (
	github.com/osmai4603-collab/accounting_engine v0.0.0
	github.com/shopspring/decimal v1.6.0
)

require github.com/google/uuid v1.6.0 // indirect

// LOCAL PRE-RELEASE VERIFICATION ONLY.
//
// This replace lets the independent module build against the working tree
// before a version is published. It is not part of the published-consumer
// contract. `scripts/check_external_adapter_consumer.sh --published` copies the
// module, removes this directive and builds it from a file-based module proxy,
// which proves the same project consumes a versioned module without a replace.
replace github.com/osmai4603-collab/accounting_engine => ../..
