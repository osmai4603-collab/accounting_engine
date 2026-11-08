package masterdata

import (
	"errors"
	"strings"

	"github.com/google/uuid"
)

var ErrDimensionRequired = errors.New("dimension code and name are required")

type Dimension struct {
	ID     uuid.UUID
	Code   string
	Name   string
	Active bool
}

func (d Dimension) Validate() error {
	if strings.TrimSpace(d.Code) == "" || strings.TrimSpace(d.Name) == "" {
		return ErrDimensionRequired
	}
	return nil
}

type DimensionValue struct {
	ID          uuid.UUID
	DimensionID uuid.UUID
	Code        string
	Name        string
	Active      bool
}

type AnalysisDimension struct {
	DimensionID uuid.UUID
	ValueID     uuid.UUID
}
