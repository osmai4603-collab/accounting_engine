package subledgers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	ErrInvalidReconciliationException  = errors.New("subledger: invalid reconciliation exception")
	ErrReconciliationExceptionNotFound = errors.New("subledger: reconciliation exception not found")
	ErrReconciliationExceptionConflict = errors.New("subledger: reconciliation exception status changed")
	ErrInvalidReconciliationTransition = errors.New("subledger: invalid reconciliation exception transition")
)

type ReconciliationExceptionStatus string

const (
	ReconciliationExceptionUnmatched ReconciliationExceptionStatus = "unmatched"
	ReconciliationExceptionMatching  ReconciliationExceptionStatus = "matching"
	ReconciliationExceptionDisputed  ReconciliationExceptionStatus = "disputed"
	ReconciliationExceptionResolved  ReconciliationExceptionStatus = "resolved"
)

type ReconciliationException struct {
	ID               uuid.UUID
	CompanyID        uuid.UUID
	Domain           string
	ControlAccountID uuid.UUID
	AsOfDate         time.Time
	FunctionalCurr   string
	SubledgerBalance decimal.Decimal
	GLBalance        decimal.Decimal
	Difference       decimal.Decimal
	Status           ReconciliationExceptionStatus
	ActorID          uuid.UUID
	Reason           string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type ReconciliationExceptionTransition struct {
	ExceptionID uuid.UUID
	CompanyID   uuid.UUID
	FromStatus  ReconciliationExceptionStatus
	ToStatus    ReconciliationExceptionStatus
	ActorID     uuid.UUID
	Reason      string
	ChangedAt   time.Time
}

type ReconciliationExceptionRepository interface {
	CreateReconciliationException(context.Context, ReconciliationException) error
	GetReconciliationException(context.Context, uuid.UUID, uuid.UUID) (ReconciliationException, error)
	ListReconciliationExceptions(context.Context, uuid.UUID) ([]ReconciliationException, error)
	ListReconciliationExceptionTransitions(context.Context, uuid.UUID, uuid.UUID) ([]ReconciliationExceptionTransition, error)
	TransitionReconciliationException(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		ReconciliationExceptionStatus,
		ReconciliationExceptionStatus,
		uuid.UUID,
		string,
		time.Time,
	) error
}

func NewReconciliationException(report DiscrepancyReport) ReconciliationException {
	now := report.DiscoveredAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return ReconciliationException{
		ID: uuid.New(), CompanyID: report.CompanyID, Domain: report.Domain,
		ControlAccountID: report.ControlAccountID, AsOfDate: report.AsOfDate,
		FunctionalCurr: report.FunctionalCurr, SubledgerBalance: report.SubledgerBalance,
		GLBalance: report.GLBalance, Difference: report.Difference,
		Status: ReconciliationExceptionUnmatched, ActorID: reconciliationDaemonActorID(),
		Reason:    fmt.Sprintf("Automated reconciliation detected a variance of %s %s.", report.Difference, report.FunctionalCurr),
		CreatedAt: now, UpdatedAt: now,
	}
}

func ValidateReconciliationException(exception ReconciliationException) error {
	if exception.ID == uuid.Nil || exception.CompanyID == uuid.Nil || exception.ControlAccountID == uuid.Nil ||
		strings.TrimSpace(exception.Domain) == "" || exception.AsOfDate.IsZero() ||
		exception.FunctionalCurr == "" || exception.Difference.IsZero() ||
		exception.Status != ReconciliationExceptionUnmatched || exception.ActorID == uuid.Nil ||
		strings.TrimSpace(exception.Reason) == "" || exception.CreatedAt.IsZero() || exception.UpdatedAt.IsZero() {
		return ErrInvalidReconciliationException
	}
	return nil
}

func ValidateReconciliationOutcome(report DiscrepancyReport, exception *ReconciliationException) error {
	if report.CompanyID == uuid.Nil || report.ControlAccountID == uuid.Nil ||
		strings.TrimSpace(report.Domain) == "" || report.FunctionalCurr == "" || report.AsOfDate.IsZero() {
		return ErrInvalidOpenLine
	}
	if report.HasVariance() != (exception != nil) {
		return ErrInvalidOpenLine
	}
	if exception == nil {
		return nil
	}
	if err := ValidateReconciliationException(*exception); err != nil {
		return err
	}
	if exception.CompanyID != report.CompanyID || exception.Domain != report.Domain ||
		exception.ControlAccountID != report.ControlAccountID ||
		!exception.AsOfDate.Equal(report.AsOfDate) ||
		exception.FunctionalCurr != report.FunctionalCurr ||
		!exception.SubledgerBalance.Equal(report.SubledgerBalance) ||
		!exception.GLBalance.Equal(report.GLBalance) ||
		!exception.Difference.Equal(report.Difference) {
		return ErrInvalidOpenLine
	}
	return nil
}

func ValidReconciliationExceptionTransition(from, to ReconciliationExceptionStatus) bool {
	switch from {
	case ReconciliationExceptionUnmatched:
		return to == ReconciliationExceptionMatching || to == ReconciliationExceptionDisputed
	case ReconciliationExceptionMatching:
		return to == ReconciliationExceptionUnmatched || to == ReconciliationExceptionDisputed ||
			to == ReconciliationExceptionResolved
	case ReconciliationExceptionDisputed:
		return to == ReconciliationExceptionUnmatched || to == ReconciliationExceptionMatching ||
			to == ReconciliationExceptionResolved
	default:
		return false
	}
}
