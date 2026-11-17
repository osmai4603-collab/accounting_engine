package standards

import (
	"context"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/reporting"
)

// DisclosureProvider may be implemented by a StandardModule to generate
// structured, versioned financial statement disclosures for a requested scope.
// Providers must return disclosures for the requested company and period only.
type DisclosureProvider interface {
	Name() string
	GetDisclosures(
		ctx context.Context,
		query reporting.DisclosureQuery,
	) ([]reporting.StructuredDisclosure, error)
}
