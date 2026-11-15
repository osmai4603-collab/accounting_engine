package reconciliation_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/reconciliation"
	"github.com/shopspring/decimal"
)

type capabilityRepository struct{}

func (capabilityRepository) Create(reconciliation.Reconciliation) error   { return nil }
func (capabilityRepository) AddItem(uuid.UUID, reconciliation.Item) error { return nil }
func (capabilityRepository) List(uuid.UUID) ([]reconciliation.Reconciliation, error) {
	return nil, nil
}

func TestReconciliationCapabilityRequiresComparableSources(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	valid := reconciliation.Requirements{
		Left:      reconciliation.SourceRequirement{Name: "general-ledger", Currency: "USD", Start: start, End: end},
		Right:     reconciliation.SourceRequirement{Name: "bank-statement", Currency: "USD", Start: start, End: end},
		MatchKeys: []string{"account", "reference", "date"},
		Tolerance: decimal.NewFromFloat(0.01), ExceptionTreatment: reconciliation.ExceptionQueueForReview,
	}
	if _, err := reconciliation.NewCapability(nil, valid); !errors.Is(err, reconciliation.ErrCapabilityDependencyMissing) {
		t.Fatalf("core-only error = %v", err)
	}
	if _, err := reconciliation.NewCapability(capabilityRepository{}, valid); err != nil {
		t.Fatalf("valid capability error = %v", err)
	}

	invalid := valid
	invalid.Right.Currency = "EUR"
	if _, err := reconciliation.NewCapability(capabilityRepository{}, invalid); !errors.Is(err, reconciliation.ErrInvalidRequirements) {
		t.Fatalf("incomparable source error = %v", err)
	}
	invalid = valid
	invalid.MatchKeys = nil
	if _, err := reconciliation.NewCapability(capabilityRepository{}, invalid); !errors.Is(err, reconciliation.ErrInvalidRequirements) {
		t.Fatalf("missing match keys error = %v", err)
	}
	invalid = valid
	invalid.ExceptionTreatment = ""
	if _, err := reconciliation.NewCapability(capabilityRepository{}, invalid); !errors.Is(err, reconciliation.ErrInvalidRequirements) {
		t.Fatalf("missing exception treatment error = %v", err)
	}
	invalid = valid
	invalid.Tolerance = decimal.NewFromFloat(-0.01)
	if _, err := reconciliation.NewCapability(capabilityRepository{}, invalid); !errors.Is(err, reconciliation.ErrInvalidRequirements) {
		t.Fatalf("negative tolerance error = %v", err)
	}
}
