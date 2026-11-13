package masterdata

import (
	"errors"
	"strings"

	"github.com/google/uuid"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
)

var (
	ErrInvalidCompany  = errors.New("invalid legal entity")
	ErrCompanyInactive = errors.New("legal entity is inactive")
)

type Company struct {
	ID                   uuid.UUID
	Code                 string
	LegalName            string
	FunctionalCurrency   string
	PresentationCurrency string
	Active               bool
}

func (c Company) Validate() error {
	if c.ID == uuid.Nil || strings.TrimSpace(c.Code) == "" || strings.TrimSpace(c.LegalName) == "" {
		return ErrInvalidCompany
	}
	if _, ok := financialtypes.LookupCurrency(c.FunctionalCurrency); !ok {
		return financialtypes.ErrInvalidCurrency
	}
	if _, ok := financialtypes.LookupCurrency(c.PresentationCurrency); !ok {
		return financialtypes.ErrInvalidCurrency
	}
	return nil
}

type CompanyRepository interface {
	CreateCompany(Company) error
	GetCompanyByID(uuid.UUID) (Company, error)
}
