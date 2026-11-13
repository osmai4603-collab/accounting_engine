// Package policy contains versioned, company-scoped accounting policies.
package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type RoundingMethod string

const (
	RoundingBankers RoundingMethod = "bankers"
	RoundingHalfUp  RoundingMethod = "half_up"
)

type PolicyKind string

const (
	CurrencyPolicyKind PolicyKind = "currency"
	PostingPolicyKind  PolicyKind = "posting"
)

var (
	ErrPolicyNotFound           = errors.New("accounting policy not found")
	ErrInvalidPolicy            = errors.New("invalid accounting policy")
	ErrCurrencyNotAllowed       = errors.New("currency is not allowed by company policy")
	ErrRoundingMismatch         = errors.New("amount does not conform to currency rounding policy")
	ErrBackdatingLimit          = errors.New("journal entry exceeds the permitted backdating threshold")
	ErrHardClosedPeriod         = errors.New("posting to a hard-closed period is prohibited")
	ErrSoftClosedPeriod         = errors.New("posting to a soft-closed period requires an accounting manager")
	ErrPolicyConflict           = errors.New("policy effective dates overlap")
	ErrPolicyVersionConflict    = errors.New("policy version is not the next version")
	ErrPolicyStorageUnavailable = errors.New("policy storage is required")
)

// EffectivePolicyHeader identifies an immutable version and its effective interval.
// EffectiveTo is exclusive; nil means the version has no scheduled end date.
type EffectivePolicyHeader struct {
	PolicyID      uuid.UUID
	CompanyID     uuid.UUID
	Version       int
	EffectiveFrom time.Time
	EffectiveTo   *time.Time
	CreatedBy     string
	ApprovedBy    string
	CreatedAt     time.Time
}

type VersionedPolicy struct {
	Header  EffectivePolicyHeader
	Kind    PolicyKind
	Payload json.RawMessage
}

type CurrencyPolicy struct {
	CompanyID                     uuid.UUID
	FunctionalCurrency            string
	PresentationCurrency          string
	RoundingMethod                RoundingMethod
	ToleranceScale                int
	AllowedCurrencies             []string
	RoundingDifferenceAccountID   uuid.UUID
	AutoSettleRoundingDifferences bool
	EffectiveDate                 time.Time
}

type PostingPolicy struct {
	CompanyID                                  uuid.UUID
	MaxBackdatingDays                          int
	AllowBackdatingException                   bool
	AllowSoftClosedPosting                     bool
	RequireManagerForSoftClosed                bool
	RequireCostCenterForIncomeExpense          bool
	RequireAnalyticalDimensionForIncomeExpense bool
	EffectiveDate                              time.Time
}

type Repository interface {
	SavePolicy(context.Context, VersionedPolicy) error
	GetPolicyAsOf(context.Context, uuid.UUID, PolicyKind, time.Time) (VersionedPolicy, error)
}

type AccountingManagerAuthorizer interface {
	IsAccountingManager(context.Context, uuid.UUID, string) (bool, error)
}

type AccountingManagerRoleRepository interface {
	HasAccountingManagerRole(context.Context, uuid.UUID, uuid.UUID) (bool, error)
}

type GovernanceManagerAuthorizer struct {
	repository AccountingManagerRoleRepository
}

func NewGovernanceManagerAuthorizer(repository AccountingManagerRoleRepository) *GovernanceManagerAuthorizer {
	return &GovernanceManagerAuthorizer{repository: repository}
}

func (a *GovernanceManagerAuthorizer) IsAccountingManager(
	ctx context.Context,
	companyID uuid.UUID,
	posterID string,
) (bool, error) {
	if a == nil || a.repository == nil {
		return false, nil
	}
	userID, err := uuid.Parse(strings.TrimSpace(posterID))
	if err != nil || userID == uuid.Nil {
		return false, nil
	}
	return a.repository.HasAccountingManagerRole(ctx, companyID, userID)
}

type BackdatingExceptionAuthorizer interface {
	IsBackdatingExceptionApproved(context.Context, uuid.UUID, uuid.UUID, string) (bool, error)
}

type EntryApprovalChecker interface {
	IsApproved(context.Context, uuid.UUID, uuid.UUID) (bool, error)
}

type ApprovalBackdatingExceptionAuthorizer struct {
	approvals EntryApprovalChecker
}

func NewApprovalBackdatingExceptionAuthorizer(approvals EntryApprovalChecker) *ApprovalBackdatingExceptionAuthorizer {
	return &ApprovalBackdatingExceptionAuthorizer{approvals: approvals}
}

func (a *ApprovalBackdatingExceptionAuthorizer) IsBackdatingExceptionApproved(
	ctx context.Context,
	companyID, entryID uuid.UUID,
	_ string,
) (bool, error) {
	if a == nil || a.approvals == nil {
		return false, nil
	}
	return a.approvals.IsApproved(ctx, companyID, entryID)
}

type Unit struct {
	repository Repository
	managers   AccountingManagerAuthorizer
	backdating BackdatingExceptionAuthorizer
}

func NewUnit(repository Repository) *Unit { return &Unit{repository: repository} }

func (u *Unit) SetAccountingManagerAuthorizer(authorizer AccountingManagerAuthorizer) {
	u.managers = authorizer
}

func (u *Unit) SetBackdatingExceptionAuthorizer(authorizer BackdatingExceptionAuthorizer) {
	u.backdating = authorizer
}

func (u *Unit) SaveCurrencyPolicy(ctx context.Context, header EffectivePolicyHeader, policy CurrencyPolicy) error {
	policy.CompanyID = header.CompanyID
	if policy.EffectiveDate.IsZero() {
		policy.EffectiveDate = header.EffectiveFrom
	}
	if !sameDate(policy.EffectiveDate, header.EffectiveFrom) {
		return ErrInvalidPolicy
	}
	policy.FunctionalCurrency = strings.ToUpper(strings.TrimSpace(policy.FunctionalCurrency))
	policy.PresentationCurrency = strings.ToUpper(strings.TrimSpace(policy.PresentationCurrency))
	for i := range policy.AllowedCurrencies {
		policy.AllowedCurrencies[i] = strings.ToUpper(strings.TrimSpace(policy.AllowedCurrencies[i]))
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	return u.save(ctx, header, CurrencyPolicyKind, policy)
}

func (u *Unit) SavePostingPolicy(ctx context.Context, header EffectivePolicyHeader, policy PostingPolicy) error {
	policy.CompanyID = header.CompanyID
	if policy.EffectiveDate.IsZero() {
		policy.EffectiveDate = header.EffectiveFrom
	}
	if !sameDate(policy.EffectiveDate, header.EffectiveFrom) {
		return ErrInvalidPolicy
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	return u.save(ctx, header, PostingPolicyKind, policy)
}

func (u *Unit) save(ctx context.Context, header EffectivePolicyHeader, kind PolicyKind, value any) error {
	if u == nil || u.repository == nil {
		return ErrPolicyStorageUnavailable
	}
	if err := validateHeader(header); err != nil {
		return err
	}
	if header.CreatedAt.IsZero() {
		header.CreatedAt = time.Now().UTC()
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return u.repository.SavePolicy(ctx, VersionedPolicy{Header: header, Kind: kind, Payload: payload})
}

func sameDate(left, right time.Time) bool {
	return !left.IsZero() && !right.IsZero() && left.UTC().Equal(right.UTC())
}

func (u *Unit) GetPolicyAsOf(ctx context.Context, companyID uuid.UUID, asOfDate time.Time) (VersionedPolicy, error) {
	return u.get(ctx, companyID, CurrencyPolicyKind, asOfDate)
}

func (u *Unit) GetCurrencyPolicyAsOf(ctx context.Context, companyID uuid.UUID, asOfDate time.Time) (CurrencyPolicy, error) {
	version, err := u.get(ctx, companyID, CurrencyPolicyKind, asOfDate)
	if err != nil {
		return CurrencyPolicy{}, err
	}
	var result CurrencyPolicy
	if err := json.Unmarshal(version.Payload, &result); err != nil {
		return CurrencyPolicy{}, err
	}
	return result, nil
}

func (u *Unit) GetPostingPolicyAsOf(ctx context.Context, companyID uuid.UUID, asOfDate time.Time) (PostingPolicy, error) {
	version, err := u.get(ctx, companyID, PostingPolicyKind, asOfDate)
	if err != nil {
		return PostingPolicy{}, err
	}
	var result PostingPolicy
	if err := json.Unmarshal(version.Payload, &result); err != nil {
		return PostingPolicy{}, err
	}
	return result, nil
}

func (u *Unit) get(ctx context.Context, companyID uuid.UUID, kind PolicyKind, asOf time.Time) (VersionedPolicy, error) {
	if u == nil || u.repository == nil {
		return VersionedPolicy{}, ErrPolicyStorageUnavailable
	}
	if companyID == uuid.Nil || asOf.IsZero() {
		return VersionedPolicy{}, ErrInvalidPolicy
	}
	return u.repository.GetPolicyAsOf(ctx, companyID, kind, asOf.UTC())
}

func (p CurrencyPolicy) Validate() error {
	if p.CompanyID == uuid.Nil || p.EffectiveDate.IsZero() || p.ToleranceScale < 0 ||
		p.ToleranceScale > financialtypes.InternalScale {
		return ErrInvalidPolicy
	}
	p.FunctionalCurrency = strings.ToUpper(strings.TrimSpace(p.FunctionalCurrency))
	p.PresentationCurrency = strings.ToUpper(strings.TrimSpace(p.PresentationCurrency))
	if _, ok := financialtypes.LookupCurrency(p.FunctionalCurrency); !ok {
		return financialtypes.ErrInvalidCurrency
	}
	if _, ok := financialtypes.LookupCurrency(p.PresentationCurrency); !ok {
		return financialtypes.ErrInvalidCurrency
	}
	if p.RoundingMethod != RoundingBankers && p.RoundingMethod != RoundingHalfUp {
		return ErrInvalidPolicy
	}
	if p.AutoSettleRoundingDifferences && p.RoundingDifferenceAccountID == uuid.Nil {
		return ErrInvalidPolicy
	}
	allowed := make(map[string]bool, len(p.AllowedCurrencies))
	for _, code := range p.AllowedCurrencies {
		code = strings.ToUpper(strings.TrimSpace(code))
		if _, ok := financialtypes.LookupCurrency(code); !ok {
			return financialtypes.ErrInvalidCurrency
		}
		allowed[code] = true
	}
	if !allowed[p.FunctionalCurrency] || !allowed[p.PresentationCurrency] {
		return ErrInvalidPolicy
	}
	return nil
}

func (p CurrencyPolicy) AllowsCurrency(currency string) bool {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	for _, allowed := range p.AllowedCurrencies {
		if strings.EqualFold(strings.TrimSpace(allowed), currency) {
			return true
		}
	}
	return false
}

func (p CurrencyPolicy) Round(amount financialtypes.Money) (financialtypes.Money, error) {
	currency, ok := financialtypes.LookupCurrency(amount.Currency())
	if !ok {
		return financialtypes.Money{}, financialtypes.ErrInvalidCurrency
	}
	value := amount.Amount()
	switch p.RoundingMethod {
	case RoundingBankers:
		value = value.RoundBank(currency.MinorUnit)
	case RoundingHalfUp:
		value = value.Round(currency.MinorUnit)
	default:
		return financialtypes.Money{}, ErrInvalidPolicy
	}
	return financialtypes.NewMoney(value, amount.Currency())
}

func (p CurrencyPolicy) ValidateMoney(amount financialtypes.Money) error {
	if !p.AllowsCurrency(amount.Currency()) {
		return ErrCurrencyNotAllowed
	}
	rounded, err := p.Round(amount)
	if err != nil {
		return err
	}
	if !rounded.Equal(amount) {
		return ErrRoundingMismatch
	}
	return nil
}

func (p PostingPolicy) Validate() error {
	if p.CompanyID == uuid.Nil || p.EffectiveDate.IsZero() || p.MaxBackdatingDays < 0 {
		return ErrInvalidPolicy
	}
	if p.RequireManagerForSoftClosed && !p.AllowSoftClosedPosting {
		return fmt.Errorf("%w: manager authorization cannot enable soft-closed posting", ErrInvalidPolicy)
	}
	return nil
}

func validateHeader(header EffectivePolicyHeader) error {
	if header.PolicyID == uuid.Nil || header.CompanyID == uuid.Nil || header.Version < 1 ||
		header.EffectiveFrom.IsZero() || strings.TrimSpace(header.CreatedBy) == "" ||
		strings.TrimSpace(header.ApprovedBy) == "" {
		return ErrInvalidPolicy
	}
	if header.EffectiveTo != nil && !header.EffectiveTo.After(header.EffectiveFrom) {
		return ErrInvalidPolicy
	}
	return nil
}

func ValidateVersionedPolicy(version VersionedPolicy) error {
	if err := validateHeader(version.Header); err != nil {
		return err
	}
	if version.Kind != CurrencyPolicyKind && version.Kind != PostingPolicyKind {
		return ErrInvalidPolicy
	}
	if !json.Valid(version.Payload) {
		return ErrInvalidPolicy
	}
	return nil
}

func (u *Unit) AuthorizeSoftClosedPosting(ctx context.Context, companyID uuid.UUID, posterID string) error {
	if u == nil || u.managers == nil || strings.TrimSpace(posterID) == "" {
		return ErrSoftClosedPeriod
	}
	ok, err := u.managers.IsAccountingManager(ctx, companyID, posterID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrSoftClosedPeriod
	}
	return nil
}

func (u *Unit) AuthorizeBackdatingException(ctx context.Context, companyID, entryID uuid.UUID, posterID string) (bool, error) {
	if u == nil || u.backdating == nil {
		return false, nil
	}
	return u.backdating.IsBackdatingExceptionApproved(ctx, companyID, entryID, posterID)
}

func BackdatingDays(entryDate, createdAt time.Time) int {
	entryDay := time.Date(entryDate.UTC().Year(), entryDate.UTC().Month(), entryDate.UTC().Day(), 0, 0, 0, 0, time.UTC)
	createdDay := time.Date(createdAt.UTC().Year(), createdAt.UTC().Month(), createdAt.UTC().Day(), 0, 0, 0, 0, time.UTC)
	return int(createdDay.Sub(entryDay).Hours() / 24)
}

// RoundedDifference returns the residual from the policy-rounded amount.
func (p CurrencyPolicy) RoundedDifference(amount financialtypes.Money) (financialtypes.Money, error) {
	rounded, err := p.Round(amount)
	if err != nil {
		return financialtypes.Money{}, err
	}
	diff, err := decimal.NewFromString(amount.Amount().Sub(rounded.Amount()).String())
	if err != nil {
		return financialtypes.Money{}, err
	}
	return financialtypes.NewMoney(diff, amount.Currency())
}
