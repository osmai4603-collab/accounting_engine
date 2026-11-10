package governance

import (
	"errors"
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
	HasRole(companyID, userID uuid.UUID, role Role) (bool, error)
}

type Authorizer struct {
	repository AuthorizationRepository
}

func NewAuthorizer(repository AuthorizationRepository) *Authorizer {
	return &Authorizer{repository: repository}
}

func (a *Authorizer) RequireRole(companyID, userID uuid.UUID, role Role) error {
	if companyID == uuid.Nil || userID == uuid.Nil || !validRole(role) {
		return ErrPermissionDenied
	}
	granted, err := a.repository.HasRole(companyID, userID, role)
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
