package governance

import (
	"errors"

	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"

	"github.com/google/uuid"
)

var (
	ErrSelfApprovalProhibited       = errors.New("self-approval is prohibited")
	ErrBankDutySegregationViolation = errors.New("bank reconciliation and disbursement duties must be separated")
	ErrFinancialControllerRequired  = errors.New("annual closing requires an authenticated financial controller")
)

type Permission string

const (
	PermissionDraft         Permission = "DRAFT"
	PermissionPost          Permission = "POST"
	PermissionReverse       Permission = "REVERSE"
	PermissionClose         Permission = "CLOSE"
	PermissionBankReconcile Permission = "BANK_RECONCILE"
	PermissionDisburse      Permission = "DISBURSE"
	PermissionController    Permission = "FINANCIAL_CONTROLLER"
)

func ValidatePostingApproval(creatorID, approverID uuid.UUID, amount, threshold financialtypes.Money) error {
	if creatorID == approverID && amount.GreaterThan(threshold) {
		return ErrSelfApprovalProhibited
	}
	return nil
}

func ValidatePostingApprovalAtOrAbove(creatorID, approverID uuid.UUID, amount, threshold financialtypes.Money) error {
	if creatorID == approverID && !amount.LessThan(threshold) {
		return ErrSelfApprovalProhibited
	}
	return nil
}

func ValidateBankDutySegregation(reconciliationActorID, disbursementActorID uuid.UUID) error {
	if reconciliationActorID == disbursementActorID {
		return ErrBankDutySegregationViolation
	}
	return nil
}

func ValidateAnnualClose(actorUUID uuid.UUID, authenticated bool, permissions []Permission) error {
	if !authenticated {
		return ErrFinancialControllerRequired
	}
	for _, permission := range permissions {
		if permission == PermissionController {
			return nil
		}
	}
	return ErrFinancialControllerRequired
}
