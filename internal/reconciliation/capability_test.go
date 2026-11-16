package reconciliation_test

import (
	"context"
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

type memoryComparisonSource struct {
	records []reconciliation.SourceRecord
}

func (m memoryComparisonSource) LoadRecords(_ context.Context, _, _ time.Time, _ string) ([]reconciliation.SourceRecord, error) {
	return m.records, nil
}

func TestReconciliationSourceComparison(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	req := reconciliation.Requirements{
		Left:               reconciliation.SourceRequirement{Name: "GL", Currency: "USD", Start: start, End: end},
		Right:              reconciliation.SourceRequirement{Name: "Bank", Currency: "USD", Start: start, End: end},
		MatchKeys:          []string{"reference"},
		Tolerance:          decimal.RequireFromString("0.05"),
		ExceptionTreatment: reconciliation.ExceptionQueueForReview,
	}

	cap, err := reconciliation.NewCapability(capabilityRepository{}, req)
	if err != nil {
		t.Fatalf("NewCapability: %v", err)
	}

	leftSource := memoryComparisonSource{
		records: []reconciliation.SourceRecord{
			{ID: "L1", MatchKey: "REF-001", Amount: decimal.RequireFromString("100.00"), Date: start},
			{ID: "L2", MatchKey: "REF-002", Amount: decimal.RequireFromString("250.00"), Date: start},
			{ID: "L3", MatchKey: "REF-003", Amount: decimal.RequireFromString("50.00"), Date: start},
		},
	}
	rightSource := memoryComparisonSource{
		records: []reconciliation.SourceRecord{
			{ID: "R1", MatchKey: "REF-001", Amount: decimal.RequireFromString("100.02"), Date: start}, // within tolerance 0.05
			{ID: "R2", MatchKey: "REF-002", Amount: decimal.RequireFromString("260.00"), Date: start}, // variance 10.00 > tolerance
			{ID: "R4", MatchKey: "REF-004", Amount: decimal.RequireFromString("75.00"), Date: start},  // unmatched right
		},
	}

	res, err := cap.CompareSources(context.Background(), leftSource, rightSource)
	if err != nil {
		t.Fatalf("CompareSources: %v", err)
	}

	if len(res.Matched) != 1 || res.Matched[0].Left.ID != "L1" {
		t.Fatalf("expected 1 matched pair L1-R1, got %+v", res.Matched)
	}
	if len(res.UnmatchedLeft) != 2 {
		t.Fatalf("expected 2 unmatched left records, got %d", len(res.UnmatchedLeft))
	}
	if len(res.UnmatchedRight) != 2 {
		t.Fatalf("expected 2 unmatched right records, got %d", len(res.UnmatchedRight))
	}
	if res.WithinTolerance {
		t.Fatalf("expected WithinTolerance=false due to unmatched records")
	}
	if len(res.Exceptions) != 1 {
		t.Fatalf("expected 1 exception string, got %d", len(res.Exceptions))
	}
}
