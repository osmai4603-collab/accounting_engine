package governance

import (
	"errors"
	"fmt"
	"sync"
	"time"

	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	ErrApprovalNotFound     = errors.New("approval request not found")
	ErrApprovalComplete     = errors.New("approval request is already complete")
	ErrApprovalSequence     = errors.New("approval is not permitted at the current stage")
	ErrApprovalRoleRequired = errors.New("approver does not hold the required role")
)

type ApprovalStep struct {
	Role      Role
	ApprovedBy uuid.UUID
	ApprovedAt *time.Time
}

type ApprovalRequest struct {
	ID             uuid.UUID
	CompanyID      uuid.UUID
	EntryID        uuid.UUID
	JournalType    string
	CreatorID      uuid.UUID
	Amount         financialtypes.Money
	CreatedAt      time.Time
	CompletedAt    *time.Time
	Steps          []ApprovalStep
}

type ApprovalRepository interface {
	SaveApproval(ApprovalRequest) error
	GetApproval(companyID, approvalID uuid.UUID) (ApprovalRequest, error)
}

type ApprovalAuditWriter interface {
	RecordApproval(companyID, entryID, actorID uuid.UUID, role Role, at time.Time) error
}

type ApprovalEngine struct {
	repository ApprovalRepository
	authorizer *Authorizer
	audit      ApprovalAuditWriter
}

func NewApprovalEngine(repository ApprovalRepository, authorizer *Authorizer, audit ApprovalAuditWriter) *ApprovalEngine {
	return &ApprovalEngine{repository: repository, authorizer: authorizer, audit: audit}
}

func (e *ApprovalEngine) Start(companyID, entryID, creatorID uuid.UUID, journalType string, amount financialtypes.Money, at time.Time) (ApprovalRequest, error) {
	if companyID == uuid.Nil || entryID == uuid.Nil || creatorID == uuid.Nil ||
		amount.Currency() == "" || amount.IsNegative() || amount.Amount().IsZero() {
		return ApprovalRequest{}, financialtypes.ErrInvalidAmount
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	steps := approvalSteps(amount)
	request := ApprovalRequest{
		ID: uuid.New(), CompanyID: companyID, EntryID: entryID, JournalType: journalType,
		CreatorID: creatorID, Amount: amount, CreatedAt: at.UTC(), Steps: steps,
	}
	if err := e.repository.SaveApproval(request); err != nil {
		return ApprovalRequest{}, err
	}
	return request, nil
}

func approvalSteps(amount financialtypes.Money) []ApprovalStep {
	switch {
	case amount.Amount().GreaterThan(decimal.NewFromInt(500000)):
		return []ApprovalStep{{Role: RoleFinanceManager}, {Role: RoleController}, {Role: RoleCFO}}
	case amount.Amount().GreaterThanOrEqual(decimal.NewFromInt(50000)):
		return []ApprovalStep{{Role: RoleSeniorAccountant}, {Role: RoleFinanceManager}}
	default:
		return []ApprovalStep{{Role: RoleSeniorAccountant}}
	}
}

func (e *ApprovalEngine) Approve(companyID, approvalID, actorID uuid.UUID, at time.Time) (ApprovalRequest, error) {
	if actorID == uuid.Nil {
		return ApprovalRequest{}, ErrPermissionDenied
	}
	request, err := e.repository.GetApproval(companyID, approvalID)
	if err != nil {
		return ApprovalRequest{}, err
	}
	if request.CompletedAt != nil {
		return ApprovalRequest{}, ErrApprovalComplete
	}
	if actorID == request.CreatorID {
		return ApprovalRequest{}, ErrSelfApprovalProhibited
	}
	index := firstPendingApproval(request.Steps)
	if index == -1 {
		return ApprovalRequest{}, ErrApprovalComplete
	}
	step := &request.Steps[index]
	if err := e.authorizer.RequireRole(companyID, actorID, step.Role); err != nil {
		if errors.Is(err, ErrPermissionDenied) {
			return ApprovalRequest{}, fmt.Errorf("%w: %s", ErrApprovalRoleRequired, step.Role)
		}
		return ApprovalRequest{}, err
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	at = at.UTC()
	step.ApprovedBy = actorID
	step.ApprovedAt = &at
	if e.audit != nil {
		if err := e.audit.RecordApproval(companyID, request.EntryID, actorID, step.Role, at); err != nil {
			return ApprovalRequest{}, err
		}
	}
	if firstPendingApproval(request.Steps) == -1 {
		request.CompletedAt = &at
	}
	if err := e.repository.SaveApproval(request); err != nil {
		return ApprovalRequest{}, err
	}
	return request, nil
}

func firstPendingApproval(steps []ApprovalStep) int {
	for i := range steps {
		if steps[i].ApprovedAt == nil {
			return i
		}
	}
	return -1
}

type MemoryApprovalRepository struct {
	mu       sync.RWMutex
	requests map[uuid.UUID]ApprovalRequest
}

func NewMemoryApprovalRepository() *MemoryApprovalRepository {
	return &MemoryApprovalRepository{requests: make(map[uuid.UUID]ApprovalRequest)}
}

func (r *MemoryApprovalRepository) SaveApproval(request ApprovalRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if request.ID == uuid.Nil {
		return ErrApprovalNotFound
	}
	r.requests[request.ID] = cloneApproval(request)
	return nil
}

func (r *MemoryApprovalRepository) GetApproval(companyID, approvalID uuid.UUID) (ApprovalRequest, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	request, ok := r.requests[approvalID]
	if !ok || request.CompanyID != companyID {
		return ApprovalRequest{}, ErrApprovalNotFound
	}
	return cloneApproval(request), nil
}

func cloneApproval(request ApprovalRequest) ApprovalRequest {
	request.Steps = append([]ApprovalStep(nil), request.Steps...)
	for i := range request.Steps {
		if request.Steps[i].ApprovedAt != nil {
			at := *request.Steps[i].ApprovedAt
			request.Steps[i].ApprovedAt = &at
		}
	}
	if request.CompletedAt != nil {
		at := *request.CompletedAt
		request.CompletedAt = &at
	}
	return request
}
