package masterdata

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
)

var (
	ErrInvalidCostCenter = errors.New("invalid cost center")
	ErrCostCenterCycle   = errors.New("cost center hierarchy contains a cycle")
)

type CostCenter struct {
	ID        uuid.UUID
	CompanyID uuid.UUID
	ParentID  *uuid.UUID
	Code      string
	Name      string
	Active    bool
}

func (c CostCenter) Validate() error {
	if c.ID == uuid.Nil || c.CompanyID == uuid.Nil || strings.TrimSpace(c.Code) == "" ||
		strings.TrimSpace(c.Name) == "" {
		return ErrInvalidCostCenter
	}
	if c.ParentID != nil && *c.ParentID == c.ID {
		return ErrCostCenterCycle
	}
	return nil
}

type CostCenterRepository interface {
	GetCostCenterForCompany(ctx context.Context, companyID, costCenterID uuid.UUID) (CostCenter, error)
}

func ValidateCostCenterHierarchy(centers []CostCenter) error {
	byID := make(map[uuid.UUID]CostCenter, len(centers))
	for _, center := range centers {
		if err := center.Validate(); err != nil {
			return err
		}
		if _, exists := byID[center.ID]; exists {
			return ErrInvalidCostCenter
		}
		byID[center.ID] = center
	}
	for _, center := range centers {
		seen := map[uuid.UUID]bool{center.ID: true}
		parent := center.ParentID
		for parent != nil {
			if seen[*parent] {
				return ErrCostCenterCycle
			}
			seen[*parent] = true
			parentCenter, exists := byID[*parent]
			if !exists || parentCenter.CompanyID != center.CompanyID {
				return ErrInvalidCostCenter
			}
			parent = parentCenter.ParentID
		}
	}
	return nil
}
