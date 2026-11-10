package governance

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrPermissionDenied = errors.New("permission denied")
	ErrInvalidRole      = errors.New("invalid role")
)

type Role string

const (
	RoleDataEntry        Role = "data_entry"
	RoleSeniorAccountant Role = "senior_accountant"
	RoleFinanceManager   Role = "finance_manager"
	RoleController       Role = "controller"
	RoleAuditor          Role = "auditor"
	RoleSystemAdmin      Role = "system_admin"
	RoleCFO              Role = "cfo"
)

type RoleAssignment struct {
	CompanyID uuid.UUID
	UserID    uuid.UUID
	Role      Role
	GrantedBy uuid.UUID
	GrantedAt time.Time
}

type AuthorizationRepository interface {
	HasRole(ctx context.Context, companyID, userID uuid.UUID, role Role) (bool, error)
}

type Authorizer struct {
	repository AuthorizationRepository
}

func NewAuthorizer(repository AuthorizationRepository) *Authorizer {
	return &Authorizer{repository: repository}
}

func (a *Authorizer) RequireRole(ctx context.Context, companyID, userID uuid.UUID, role Role) error {
	if a == nil || a.repository == nil || companyID == uuid.Nil || userID == uuid.Nil || !validRole(role) {
		return ErrPermissionDenied
	}
	granted, err := a.repository.HasRole(ctx, companyID, userID, role)
	if err != nil {
		return err
	}
	if !granted {
		return ErrPermissionDenied
	}
	return nil
}

func validRole(role Role) bool {
	switch role {
	case RoleDataEntry, RoleSeniorAccountant, RoleFinanceManager, RoleController,
		RoleAuditor, RoleSystemAdmin, RoleCFO:
		return true
	default:
		return false
	}
}

func ValidRole(role Role) bool {
	return validRole(role)
}

type MemoryAuthorizationRepository struct {
	mu    sync.RWMutex
	roles map[string]map[Role]struct{}
}

func NewMemoryAuthorizationRepository() *MemoryAuthorizationRepository {
	return &MemoryAuthorizationRepository{roles: make(map[string]map[Role]struct{})}
}

func (r *MemoryAuthorizationRepository) GrantRole(assignment RoleAssignment) error {
	if assignment.CompanyID == uuid.Nil || assignment.UserID == uuid.Nil || !validRole(assignment.Role) {
		return ErrInvalidRole
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := assignment.CompanyID.String() + ":" + assignment.UserID.String()
	if r.roles[key] == nil {
		r.roles[key] = make(map[Role]struct{})
	}
	r.roles[key][assignment.Role] = struct{}{}
	return nil
}

func (r *MemoryAuthorizationRepository) HasRole(ctx context.Context, companyID, userID uuid.UUID, role Role) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.roles[companyID.String()+":"+userID.String()][role]
	return ok, nil
}
