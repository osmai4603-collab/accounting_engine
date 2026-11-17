package reporting

import (
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
)

type DisclosurePeriod struct {
	StartDate time.Time
	EndDate   time.Time
}

type DisclosureSource struct {
	Kind      string
	Reference string
}

type DisclosureQuery struct {
	CompanyID         types.CompanyID
	Period            DisclosurePeriod
	Currency          string
	StandardReference string
	Version           string
}

// StructuredDisclosure is a versioned, reproducible note payload. Data must
// contain JSON-serializable structured values; Narrative supplements, but does
// not replace, that structured data.
type StructuredDisclosure struct {
	CompanyID         types.CompanyID
	Code              string
	Title             string
	StandardReference string
	Period            DisclosurePeriod
	Currency          string
	Version           string
	Source            []DisclosureSource
	Data              map[string]any
	Narrative         string
	ContentHash       string
}
