package governance

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	ErrApprovalNotFound            = errors.New("approval request not found")
	ErrApprovalComplete            = errors.New("approval request is already complete")
	ErrApprovalSequence            = errors.New("approval is not permitted at the current stage")
	ErrApprovalRoleRequired        = errors.New("approver does not hold the required role")
	ErrApproverReused              = errors.New("each approval stage requires a different approver")
	ErrApprovalAuditRequired       = errors.New("approval audit writer is required")
	ErrApprovalRepositoryRequired  = errors.New("approval repository is required")
	ErrInvalidApprovalRequest      = errors.New("invalid approval request")
	ErrApprovalAmountMismatch      = errors.New("approval amount does not match the stored entry functional total")
	ErrApprovalEntrySourceRequired = errors.New("approval entry source is required")
	ErrApprovalAlreadyExists       = errors.New("approval request already exists for this entry")
)

type ApprovalStep struct {
	Role       Role
	ApprovedBy *uuid.UUID
	ApprovedAt *time.Time
}

type ApprovalRequest struct {
	ID               uuid.UUID
	CompanyID        uuid.UUID
	EntryID          uuid.UUID
	JournalType      string
	CreatorID        uuid.UUID
	Amount           financialtypes.Money
	FunctionalAmount financialtypes.Money
	EntryHash        string
	CreatedAt        time.Time
	CompletedAt      *time.Time
	Steps            []ApprovalStep
}

func (request ApprovalRequest) Validate() error {
	if request.ID == uuid.Nil || request.CompanyID == uuid.Nil || request.EntryID == uuid.Nil ||
		request.CreatorID == uuid.Nil || request.Amount.Currency() == "" || request.Amount.IsNegative() ||
		request.Amount.Amount().IsZero() || len(request.Steps) == 0 || request.CreatedAt.IsZero() ||
		len(request.EntryHash) != 64 || request.FunctionalAmount.Currency() == "" ||
		request.FunctionalAmount.IsNegative() || request.FunctionalAmount.Amount().IsZero() {
		return ErrInvalidApprovalRequest
	}
	roles := make(map[Role]struct{}, len(request.Steps))
	actors := make(map[uuid.UUID]struct{}, len(request.Steps))
	foundPending := false
	for _, step := range request.Steps {
		if !validRole(step.Role) {
			return ErrInvalidApprovalRequest
		}
		if _, exists := roles[step.Role]; exists {
			return ErrInvalidApprovalRequest
		}
		roles[step.Role] = struct{}{}
		if step.ApprovedAt == nil {
			if step.ApprovedBy != nil || request.CompletedAt != nil {
				return ErrInvalidApprovalRequest
			}
			foundPending = true
			continue
		}
		if foundPending || step.ApprovedBy == nil || *step.ApprovedBy == uuid.Nil ||
			*step.ApprovedBy == request.CreatorID {
			return ErrInvalidApprovalRequest
		}
		if _, exists := actors[*step.ApprovedBy]; exists {
			return ErrInvalidApprovalRequest
		}
		actors[*step.ApprovedBy] = struct{}{}
	}
	if (request.CompletedAt != nil) == foundPending {
		return ErrInvalidApprovalRequest
	}
	return nil
}

type ApprovalRepository interface {
	SaveApproval(ctx context.Context, request ApprovalRequest) error
	GetApproval(ctx context.Context, companyID, approvalID uuid.UUID) (ApprovalRequest, error)
	IsEntryApproved(ctx context.Context, companyID, entryID uuid.UUID, entryHash string) (bool, error)
}

// ApprovalEntryRepository supplies the stored draft that an approval request is
// bound to. Start and IsApproved use it to derive the functional amount and the
// content fingerprint instead of trusting caller-provided values.
type ApprovalEntryRepository interface {
	GetEntryByIDForCompanyContext(ctx context.Context, companyID, entryID uuid.UUID) (posting.JournalEntry, error)
}

type AtomicApprovalAuditRepository interface {
	SaveApprovalWithAudit(ctx context.Context, request ApprovalRequest, actorID uuid.UUID, role Role, at time.Time) error
}

type ApprovalAuditWriter interface {
	RecordApproval(ctx context.Context, companyID, entryID, actorID uuid.UUID, role Role, at time.Time) error
}

type ApprovalEngine struct {
	mu         sync.Mutex
	repository ApprovalRepository
	authorizer *Authorizer
	audit      ApprovalAuditWriter
	matrix     []ApprovalRule
}

type ApprovalRule struct {
	JournalType      string
	Minimum          decimal.Decimal
	MinimumExclusive bool
	Roles            []Role
}

func NewApprovalEngine(repository ApprovalRepository, authorizer *Authorizer, audit ApprovalAuditWriter) *ApprovalEngine {
	return NewApprovalEngineWithMatrix(repository, authorizer, audit, []ApprovalRule{
		{Minimum: decimal.NewFromInt(500000), MinimumExclusive: true, Roles: []Role{RoleFinanceManager, RoleController, RoleCFO}},
		{Minimum: decimal.NewFromInt(50000), Roles: []Role{RoleSeniorAccountant, RoleFinanceManager}},
		{Minimum: decimal.Zero, Roles: []Role{RoleSeniorAccountant}},
	})
}

func NewApprovalEngineWithMatrix(repository ApprovalRepository, authorizer *Authorizer, audit ApprovalAuditWriter, matrix []ApprovalRule) *ApprovalEngine {
	copied := make([]ApprovalRule, len(matrix))
	for i, rule := range matrix {
		copied[i] = ApprovalRule{
			JournalType: rule.JournalType, Minimum: rule.Minimum, MinimumExclusive: rule.MinimumExclusive,
			Roles: append([]Role(nil), rule.Roles...),
		}
	}
	return &ApprovalEngine{repository: repository, authorizer: authorizer, audit: audit, matrix: copied}
}

func (e *ApprovalEngine) Start(ctx context.Context, companyID, entryID, creatorID uuid.UUID, journalType string, amount financialtypes.Money, at time.Time) (ApprovalRequest, error) {
	if e == nil || e.repository == nil {
		return ApprovalRequest{}, ErrApprovalRepositoryRequired
	}
	if err := ctx.Err(); err != nil {
		return ApprovalRequest{}, err
	}
	if companyID == uuid.Nil || entryID == uuid.Nil || creatorID == uuid.Nil ||
		amount.Currency() == "" || amount.IsNegative() || amount.Amount().IsZero() {
		return ApprovalRequest{}, financialtypes.ErrInvalidAmount
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	entries, ok := e.repository.(ApprovalEntryRepository)
	if !ok {
		return ApprovalRequest{}, ErrApprovalEntrySourceRequired
	}
	entry, err := entries.GetEntryByIDForCompanyContext(ctx, companyID, entryID)
	if err != nil {
		return ApprovalRequest{}, err
	}
	functionalAmount, err := posting.EntryFunctionalTotal(entry)
	if err != nil {
		return ApprovalRequest{}, err
	}
	if !amount.Equal(functionalAmount) {
		return ApprovalRequest{}, ErrApprovalAmountMismatch
	}
	entryHash, err := posting.EntryContentHash(entry)
	if err != nil {
		return ApprovalRequest{}, err
	}
	steps, err := e.approvalSteps(functionalAmount, journalType)
	if err != nil {
		return ApprovalRequest{}, err
	}
	request := ApprovalRequest{
		ID: uuid.New(), CompanyID: companyID, EntryID: entryID, JournalType: journalType,
		CreatorID: creatorID, Amount: functionalAmount, FunctionalAmount: functionalAmount,
		EntryHash: entryHash, CreatedAt: at.UTC(), Steps: steps,
	}
	if err := request.Validate(); err != nil {
		return ApprovalRequest{}, err
	}
	if err := e.repository.SaveApproval(ctx, request); err != nil {
		return ApprovalRequest{}, err
	}
	return request, nil
}

func (e *ApprovalEngine) approvalSteps(amount financialtypes.Money, journalType string) ([]ApprovalStep, error) {
	var selected *ApprovalRule
	selectedSpecific := false
	for i := range e.matrix {
		rule := &e.matrix[i]
		if rule.JournalType != "" && rule.JournalType != journalType {
			continue
		}
		specific := rule.JournalType != ""
		meetsMinimum := amount.Amount().GreaterThanOrEqual(rule.Minimum)
		if rule.MinimumExclusive {
			meetsMinimum = amount.Amount().GreaterThan(rule.Minimum)
		}
		if meetsMinimum &&
			(selected == nil || rule.Minimum.GreaterThan(selected.Minimum) ||
				(rule.Minimum.Equal(selected.Minimum) && specific && !selectedSpecific)) {
			selected = rule
			selectedSpecific = specific
		}
	}
	if selected == nil || len(selected.Roles) == 0 {
		return nil, errors.New("no approval rule matches the journal and amount")
	}
	steps := make([]ApprovalStep, len(selected.Roles))
	seen := map[Role]struct{}{}
	for i, role := range selected.Roles {
		if !validRole(role) {
			return nil, ErrInvalidRole
		}
		if _, exists := seen[role]; exists {
			return nil, errors.New("approval rule repeats a role")
		}
		seen[role] = struct{}{}
		steps[i] = ApprovalStep{Role: role}
	}
	return steps, nil
}

func (e *ApprovalEngine) IsApproved(ctx context.Context, companyID, entryID uuid.UUID) (bool, error) {
	if e == nil || e.repository == nil {
		return false, ErrApprovalRepositoryRequired
	}
	entries, ok := e.repository.(ApprovalEntryRepository)
	if !ok {
		return false, ErrApprovalEntrySourceRequired
	}
	entry, err := entries.GetEntryByIDForCompanyContext(ctx, companyID, entryID)
	if err != nil {
		return false, err
	}
	entryHash, err := posting.EntryContentHash(entry)
	if err != nil {
		return false, err
	}
	return e.repository.IsEntryApproved(ctx, companyID, entryID, entryHash)
}

func (e *ApprovalEngine) Approve(ctx context.Context, companyID, approvalID, actorID uuid.UUID, at time.Time) (ApprovalRequest, error) {
	if e == nil || e.repository == nil {
		return ApprovalRequest{}, ErrApprovalRepositoryRequired
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if actorID == uuid.Nil {
		return ApprovalRequest{}, ErrPermissionDenied
	}
	request, err := e.repository.GetApproval(ctx, companyID, approvalID)
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
	for _, prior := range request.Steps[:index] {
		if prior.ApprovedBy != nil && *prior.ApprovedBy == actorID {
			return ApprovalRequest{}, ErrApproverReused
		}
	}
	if err := e.authorizer.RequireRole(ctx, companyID, actorID, step.Role); err != nil {
		if errors.Is(err, ErrPermissionDenied) {
			return ApprovalRequest{}, fmt.Errorf("%w: %s", ErrApprovalRoleRequired, step.Role)
		}
		return ApprovalRequest{}, err
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	at = at.UTC()
	step.ApprovedBy = &actorID
	step.ApprovedAt = &at
	if firstPendingApproval(request.Steps) == -1 {
		request.CompletedAt = &at
	}
	if err := request.Validate(); err != nil {
		return ApprovalRequest{}, err
	}
	repository, ok := e.repository.(AtomicApprovalAuditRepository)
	if !ok {
		return ApprovalRequest{}, ErrApprovalAuditRequired
	}
	if err := repository.SaveApprovalWithAudit(ctx, request, actorID, step.Role, at); err != nil {
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
	entries  map[uuid.UUID]posting.JournalEntry
	audit    []FinancialAuditLog
}

func NewMemoryApprovalRepository() *MemoryApprovalRepository {
	return &MemoryApprovalRepository{
		requests: make(map[uuid.UUID]ApprovalRequest),
		entries:  make(map[uuid.UUID]posting.JournalEntry),
	}
}

// SaveEntryForApproval registers the draft that approval requests are bound to.
// It mirrors the stored-entry lookup implemented by persistent repositories.
func (r *MemoryApprovalRepository) SaveEntryForApproval(entry posting.JournalEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[entry.ID] = entry
}

func (r *MemoryApprovalRepository) GetEntryByIDForCompanyContext(
	ctx context.Context,
	companyID, entryID uuid.UUID,
) (posting.JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return posting.JournalEntry{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.entries[entryID]
	if !ok || entry.CompanyID != companyID {
		return posting.JournalEntry{}, posting.ErrEntryNotFound
	}
	return entry, nil
}

func (r *MemoryApprovalRepository) SaveApproval(ctx context.Context, request ApprovalRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := request.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if request.ID == uuid.Nil {
		return ErrApprovalNotFound
	}
	r.requests[request.ID] = cloneApproval(request)
	return nil
}

func (r *MemoryApprovalRepository) SaveApprovalWithAudit(
	ctx context.Context,
	request ApprovalRequest,
	actorID uuid.UUID,
	role Role,
	at time.Time,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := request.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	stored, ok := r.requests[request.ID]
	if !ok || stored.CompanyID != request.CompanyID {
		return ErrApprovalNotFound
	}
	if stored.CompletedAt != nil {
		return ErrApprovalComplete
	}
	if stored.CreatorID != request.CreatorID || actorID == stored.CreatorID {
		return ErrSelfApprovalProhibited
	}
	index := firstPendingApproval(stored.Steps)
	if index < 0 || index >= len(request.Steps) ||
		request.Steps[index].Role != role || request.Steps[index].ApprovedBy == nil ||
		*request.Steps[index].ApprovedBy != actorID || request.Steps[index].ApprovedAt == nil ||
		!request.Steps[index].ApprovedAt.Equal(at) {
		return ErrApprovalSequence
	}
	if len(stored.Steps) != len(request.Steps) {
		return ErrApprovalSequence
	}
	for i := range stored.Steps {
		if i == index {
			continue
		}
		if stored.Steps[i].Role != request.Steps[i].Role ||
			!approvalActorMatches(stored.Steps[i].ApprovedBy, request.Steps[i].ApprovedBy) ||
			!approvalTimeMatches(stored.Steps[i].ApprovedAt, request.Steps[i].ApprovedAt) {
			return ErrApprovalSequence
		}
	}
	r.requests[request.ID] = cloneApproval(request)
	r.audit = append(r.audit, FinancialAuditLog{
		CompanyID: request.CompanyID, EntityType: "journal_entry", EntityID: request.EntryID,
		Action: "APPROVAL:" + string(role), ActorID: actorID, Timestamp: at.UTC(),
	})
	return nil
}

func approvalActorMatches(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func approvalTimeMatches(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func (r *MemoryApprovalRepository) ApprovalAuditRecords() []FinancialAuditLog {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]FinancialAuditLog(nil), r.audit...)
}

func (r *MemoryApprovalRepository) GetApproval(ctx context.Context, companyID, approvalID uuid.UUID) (ApprovalRequest, error) {
	if err := ctx.Err(); err != nil {
		return ApprovalRequest{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	request, ok := r.requests[approvalID]
	if !ok || request.CompanyID != companyID {
		return ApprovalRequest{}, ErrApprovalNotFound
	}
	return cloneApproval(request), nil
}

func (r *MemoryApprovalRepository) IsEntryApproved(ctx context.Context, companyID, entryID uuid.UUID, entryHash string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, request := range r.requests {
		if request.CompanyID == companyID && request.EntryID == entryID &&
			request.CompletedAt != nil && request.EntryHash == entryHash {
			return true, nil
		}
	}
	return false, nil
}

func cloneApproval(request ApprovalRequest) ApprovalRequest {
	request.Steps = append([]ApprovalStep(nil), request.Steps...)
	for i := range request.Steps {
		if request.Steps[i].ApprovedBy != nil {
			approverID := *request.Steps[i].ApprovedBy
			request.Steps[i].ApprovedBy = &approverID
		}
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
