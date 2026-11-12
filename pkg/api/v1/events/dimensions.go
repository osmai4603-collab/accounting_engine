package events

import (
	"accounting_engine/pkg/api/v1/types"
	"github.com/google/uuid"
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
			return &parsed
		}
	}
	return nil
}
