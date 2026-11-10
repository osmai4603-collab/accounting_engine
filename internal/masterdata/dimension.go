package masterdata

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
)

var ErrDimensionRequired = errors.New("dimension code and name are required")
var ErrInvalidAnalyticalDimension = errors.New("invalid or inactive analytical dimension")

type AnalyticalDimensionKind string

const (
	DimensionProject      AnalyticalDimensionKind = "project"
	DimensionSegment      AnalyticalDimensionKind = "segment"
	DimensionBusinessUnit AnalyticalDimensionKind = "business_unit"
)

type AnalyticalDimensionValue struct {
	ID        uuid.UUID
	CompanyID uuid.UUID
	Kind      AnalyticalDimensionKind
	Code      string
	Name      string
	Active    bool
}

func (value AnalyticalDimensionValue) Validate() error {
	if value.ID == uuid.Nil || value.CompanyID == uuid.Nil ||
		(value.Kind != DimensionProject && value.Kind != DimensionSegment && value.Kind != DimensionBusinessUnit) ||
		strings.TrimSpace(value.Code) == "" || strings.TrimSpace(value.Name) == "" {
		return ErrInvalidAnalyticalDimension
	}
	return nil
}

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

type FinancialDimensionRepository interface {
	SaveCostCenter(context.Context, CostCenter) error
	SaveAnalyticalDimension(context.Context, AnalyticalDimensionValue) error
}
