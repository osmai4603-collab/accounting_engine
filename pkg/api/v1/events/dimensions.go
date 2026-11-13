package events

import (
	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
)

type DimensionsRouter struct {
	mappings map[string]string
}

func NewDimensionsRouter(mappings map[string]string) *DimensionsRouter {
	if mappings == nil {
		mappings = make(map[string]string)
	}
	return &DimensionsRouter{mappings: mappings}
}

func (r *DimensionsRouter) ResolveDimensionID(dimensions map[string]string, key string) *types.CompanyID {
	if val, ok := dimensions[key]; ok {
		if parsed, err := uuid.Parse(val); err == nil {
			id := types.CompanyID(parsed)
			return &id
		}
	}
	return nil
}
