package sdk

import "github.com/osmai4603-collab/accounting_engine/internal/sdkapplication"

// PostingConfig declares the posting controls that are required for an SDK
// engine. Requirements are checked during Build; they are not enabled by
// setting these flags alone.
type PostingConfig struct {
	RequireApproval            bool
	RequireFinancialDimensions bool
	RequireMakerCheckerPolicy  bool
	RequirePolicy              bool
}

func (c PostingConfig) internal() sdkapplication.PostingConfig {
	return sdkapplication.PostingConfig{
		RequireApproval:            c.RequireApproval,
		RequireFinancialDimensions: c.RequireFinancialDimensions,
		RequireMakerCheckerPolicy:  c.RequireMakerCheckerPolicy,
		RequirePolicy:              c.RequirePolicy,
	}
}

func (c PostingConfig) requiresCapabilities() bool {
	return c.RequireApproval || c.RequireFinancialDimensions ||
		c.RequireMakerCheckerPolicy || c.RequirePolicy
}
