package posting

import (
	"context"
	"errors"
	"testing"

	"accounting_engine/internal/masterdata"
	"github.com/google/uuid"
)

type dimensionPolicyStub struct {
	required    bool
	validID     uuid.UUID
	validateErr error
}

type approvalGateStub bool

func (s approvalGateStub) IsApproved(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return bool(s), nil
}

func (s dimensionPolicyStub) RequiresCostCenter(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return s.required, nil
}

func (s dimensionPolicyStub) ValidateCostCenter(_ context.Context, _, centerID uuid.UUID) error {
	if s.validateErr != nil {
		return s.validateErr
	}
	if centerID != s.validID {
		return errors.New("invalid center")
	}
	return nil
}

func (s dimensionPolicyStub) ValidateAnalyticalDimension(_ context.Context, _ uuid.UUID, _ masterdata.AnalyticalDimensionKind, dimensionID uuid.UUID) error {
	if s.validateErr != nil {
		return s.validateErr
	}
	if dimensionID != s.validID {
		return masterdata.ErrInvalidAnalyticalDimension
	}
	return nil
}

func TestFinancialDimensionsRequireAndValidateCostCenter(t *testing.T) {
	companyID, accountID, centerID := uuid.New(), uuid.New(), uuid.New()
	engine := NewFinancialDimensionsEngine(dimensionPolicyStub{required: true, validID: centerID})
	entry := JournalEntry{CompanyID: companyID, Lines: []JournalEntryLine{{AccountID: accountID}}}
	if err := engine.ValidateEntry(context.Background(), entry); !errors.Is(err, ErrCostCenterRequired) {
		t.Fatalf("missing cost center error = %v", err)
	}
	entry.Lines[0].CostCenterID = &centerID
	if err := engine.ValidateEntry(context.Background(), entry); err != nil {
		t.Fatalf("valid cost center error = %v", err)
	}
	wrongCenter := uuid.New()
	entry.Lines[0].CostCenterID = &wrongCenter
	if err := engine.ValidateEntry(context.Background(), entry); err == nil {
		t.Fatal("expected invalid cost center to be rejected")
	}
}

func TestFinancialDimensionsValidateOptionalProjectAndSegment(t *testing.T) {
	companyID, accountID, projectID := uuid.New(), uuid.New(), uuid.New()
	engine := NewFinancialDimensionsEngine(dimensionPolicyStub{validID: projectID})
	entry := JournalEntry{
		CompanyID: companyID,
		Lines: []JournalEntryLine{{
			AccountID: accountID, ProjectID: &projectID,
		}},
	}
	if err := engine.ValidateEntry(context.Background(), entry); err != nil {
		t.Fatalf("valid project dimension error = %v", err)
	}
	invalidID := uuid.New()
	entry.Lines[0].SegmentID = &invalidID
	if err := engine.ValidateEntry(context.Background(), entry); !errors.Is(err, masterdata.ErrInvalidAnalyticalDimension) {
		t.Fatalf("invalid segment error = %v, want invalid dimension", err)
	}
}

func TestPostingRequiresConfiguredApprovalGateToPass(t *testing.T) {
	repository := NewMockRepository()
	engine := NewEngine(repository, nil)
	engine.SetApprovalGate(approvalGateStub(false))
	entry := balancedEntry(t, "maker")
	if err := engine.CreateDraft(entry); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.GetEntryByID(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.PostEntryContext(context.Background(), &stored, "checker"); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("posting without approvals error = %v, want approval required", err)
	}
}
