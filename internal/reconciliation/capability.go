package reconciliation

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

var (
	ErrCapabilityDependencyMissing = errors.New("reconciliation capability dependency missing")
	ErrInvalidRequirements         = errors.New("invalid reconciliation requirements")
)

type ExceptionTreatment string

const (
	ExceptionReject         ExceptionTreatment = "reject"
	ExceptionQueueForReview ExceptionTreatment = "queue_for_review"
	ExceptionApprovedAdjust ExceptionTreatment = "approved_adjustment"
)

type SourceRequirement struct {
	Name     string
	Currency string
	Start    time.Time
	End      time.Time
}

// Requirements is the explicit comparison contract for a reconciliation.
// Both sources must use the same currency and reporting window, and every
// mismatch must have a declared tolerance and exception treatment.
type Requirements struct {
	Left               SourceRequirement
	Right              SourceRequirement
	MatchKeys          []string
	Tolerance          decimal.Decimal
	ExceptionTreatment ExceptionTreatment
}

type Capability struct {
	repository   Repository
	requirements Requirements
}

func NewCapability(repository Repository, requirements Requirements) (*Capability, error) {
	if repository == nil {
		return nil, fmt.Errorf("%w: reconciliation repository", ErrCapabilityDependencyMissing)
	}
	if err := requirements.Validate(); err != nil {
		return nil, err
	}
	requirements.MatchKeys = append([]string(nil), requirements.MatchKeys...)
	return &Capability{repository: repository, requirements: requirements}, nil
}

func (r Requirements) Validate() error {
	fail := func(reason string) error {
		return fmt.Errorf("%w: %s", ErrInvalidRequirements, reason)
	}
	if strings.TrimSpace(r.Left.Name) == "" || strings.TrimSpace(r.Right.Name) == "" ||
		strings.EqualFold(strings.TrimSpace(r.Left.Name), strings.TrimSpace(r.Right.Name)) {
		return fail("two distinct source names are required")
	}
	if strings.TrimSpace(r.Left.Currency) == "" || !strings.EqualFold(r.Left.Currency, r.Right.Currency) {
		return fail("sources must use the same explicit comparison currency")
	}
	if r.Left.Start.IsZero() || r.Left.End.IsZero() || r.Right.Start.IsZero() || r.Right.End.IsZero() ||
		!sameDate(r.Left.Start, r.Right.Start) || !sameDate(r.Left.End, r.Right.End) ||
		r.Left.Start.After(r.Left.End) {
		return fail("sources must cover the same valid reporting window")
	}
	if len(r.MatchKeys) == 0 {
		return fail("at least one match key is required")
	}
	seen := make(map[string]struct{}, len(r.MatchKeys))
	for _, key := range r.MatchKeys {
		key = strings.TrimSpace(key)
		if key == "" {
			return fail("match keys cannot be blank")
		}
		if _, exists := seen[key]; exists {
			return fail("match keys must be unique")
		}
		seen[key] = struct{}{}
	}
	if r.Tolerance.IsNegative() {
		return fail("tolerance cannot be negative")
	}
	switch r.ExceptionTreatment {
	case ExceptionReject, ExceptionQueueForReview, ExceptionApprovedAdjust:
	default:
		return fail("exception treatment must be explicit")
	}
	return nil
}

func (c *Capability) Requirements() Requirements {
	if c == nil {
		return Requirements{}
	}
	result := c.requirements
	result.MatchKeys = append([]string(nil), result.MatchKeys...)
	return result
}

func sameDate(left, right time.Time) bool {
	y1, m1, d1 := left.Date()
	y2, m2, d2 := right.Date()
	return y1 == y2 && m1 == m2 && d1 == d2
}
