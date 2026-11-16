package reconciliation

import (
	"context"
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

// SourceRecord represents a transaction or balance item from a reconciliation source.
type SourceRecord struct {
	ID        string
	MatchKey  string
	Amount    decimal.Decimal
	Date      time.Time
	Reference string
}

// ComparisonSource provides records from a financial source within a window and currency.
type ComparisonSource interface {
	LoadRecords(ctx context.Context, start, end time.Time, currency string) ([]SourceRecord, error)
}

// MatchedPair represents a matched pair from left and right sources.
type MatchedPair struct {
	Left       SourceRecord
	Right      SourceRecord
	Difference decimal.Decimal
}

// ComparisonResult summarizes the reconciliation outcome between two sources.
type ComparisonResult struct {
	Matched         []MatchedPair
	UnmatchedLeft   []SourceRecord
	UnmatchedRight  []SourceRecord
	DifferenceTotal decimal.Decimal
	WithinTolerance bool
	Exceptions      []string
}

// CompareSources matches records between two sources based on declared capability requirements.
func (c *Capability) CompareSources(
	ctx context.Context, leftSource, rightSource ComparisonSource,
) (ComparisonResult, error) {
	if c == nil {
		return ComparisonResult{}, ErrCapabilityDependencyMissing
	}
	if leftSource == nil || rightSource == nil {
		return ComparisonResult{}, fmt.Errorf("%w: comparison source is nil", ErrCapabilityDependencyMissing)
	}
	if err := c.requirements.Validate(); err != nil {
		return ComparisonResult{}, err
	}

	leftRecords, err := leftSource.LoadRecords(ctx, c.requirements.Left.Start, c.requirements.Left.End, c.requirements.Left.Currency)
	if err != nil {
		return ComparisonResult{}, fmt.Errorf("load left source records: %w", err)
	}
	rightRecords, err := rightSource.LoadRecords(ctx, c.requirements.Right.Start, c.requirements.Right.End, c.requirements.Right.Currency)
	if err != nil {
		return ComparisonResult{}, fmt.Errorf("load right source records: %w", err)
	}

	rightByKey := make(map[string][]SourceRecord, len(rightRecords))
	for _, rec := range rightRecords {
		key := strings.TrimSpace(rec.MatchKey)
		rightByKey[key] = append(rightByKey[key], rec)
	}

	result := ComparisonResult{
		WithinTolerance: true,
	}

	usedRightIndices := make(map[string]map[int]bool)

	for _, left := range leftRecords {
		key := strings.TrimSpace(left.MatchKey)
		candidates := rightByKey[key]
		matched := false
		if usedRightIndices[key] == nil {
			usedRightIndices[key] = make(map[int]bool)
		}

		for idx, right := range candidates {
			if usedRightIndices[key][idx] {
				continue
			}
			diff := left.Amount.Sub(right.Amount).Abs()
			if diff.LessThanOrEqual(c.requirements.Tolerance) {
				usedRightIndices[key][idx] = true
				result.Matched = append(result.Matched, MatchedPair{
					Left:       left,
					Right:      right,
					Difference: diff,
				})
				result.DifferenceTotal = result.DifferenceTotal.Add(diff)
				matched = true
				break
			}
		}

		if !matched {
			result.UnmatchedLeft = append(result.UnmatchedLeft, left)
			result.DifferenceTotal = result.DifferenceTotal.Add(left.Amount.Abs())
		}
	}

	for _, right := range rightRecords {
		key := strings.TrimSpace(right.MatchKey)
		usedMap := usedRightIndices[key]
		foundInMatched := false
		for idx, r := range rightByKey[key] {
			if r.ID == right.ID && usedMap[idx] {
				foundInMatched = true
				break
			}
		}
		if !foundInMatched {
			result.UnmatchedRight = append(result.UnmatchedRight, right)
			result.DifferenceTotal = result.DifferenceTotal.Add(right.Amount.Abs())
		}
	}

	if len(result.UnmatchedLeft) > 0 || len(result.UnmatchedRight) > 0 {
		result.WithinTolerance = false
		exc := fmt.Sprintf("unmatched records detected: left=%d right=%d total_variance=%s (treatment=%s)",
			len(result.UnmatchedLeft), len(result.UnmatchedRight), result.DifferenceTotal, c.requirements.ExceptionTreatment)
		result.Exceptions = append(result.Exceptions, exc)
	}

	return result, nil
}
