package sdkapplication

import (
	"context"
	"reflect"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/policy"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
	"github.com/shopspring/decimal"
)

// PostingConfig selects optional controls that must be wired before the SDK
// application can be built.
type PostingConfig struct {
	RequireApproval            bool
	RequireFinancialDimensions bool
	RequireMakerCheckerPolicy  bool
	RequirePolicy              bool
}

// PostingCapabilitiesExtension is the PrivateExtensions key adapters may use
// to pass already-constructed, engine-specific posting controls.
const PostingCapabilitiesExtension = "accounting_engine.posting_capabilities"

// HasPrivatePostingPolicy reports whether the adapter supplied a policy unit
// through the engine-specific posting capabilities extension. The SDK
// bootstrap uses this to verify a required posting-policy capability without
// importing the posting package itself.
func HasPrivatePostingPolicy(ports storage.Ports) bool {
	supplied, ok := ports.Private[PostingCapabilitiesExtension].(posting.Capabilities)
	return ok && supplied.PolicyUnit != nil
}

func createPostingEngine(
	repository posting.Repository,
	masterData *masterdata.Service,
	config PostingConfig,
	capabilities posting.Capabilities,
) (*posting.Engine, error) {
	return posting.NewConfiguredEngine(repository, masterData, posting.Config{
		RequireApproval:            config.RequireApproval,
		RequireFinancialDimensions: config.RequireFinancialDimensions,
		RequireMakerCheckerPolicy:  config.RequireMakerCheckerPolicy,
		RequirePolicy:              config.RequirePolicy,
	}, capabilities)
}

func capabilitiesFromCore(core any, config PostingConfig) posting.Capabilities {
	capabilities := posting.Capabilities{}
	if config.RequireApproval {
		if dependency, ok := core.(posting.ApprovalGate); ok {
			capabilities.ApprovalGate = dependency
		}
	}
	if config.RequireFinancialDimensions {
		if dependency, ok := core.(posting.FinancialDimensionPolicy); ok {
			capabilities.FinancialDimensions = dependency
		}
	}
	if config.RequireMakerCheckerPolicy {
		if dependency, ok := core.(posting.MakerCheckerPolicy); ok {
			capabilities.MakerCheckerPolicy = dependency
		}
	}
	if config.RequirePolicy {
		if dependency, ok := core.(policy.Repository); ok {
			capabilities.PolicyUnit = policy.NewUnit(dependency)
		}
	}
	return capabilities
}

func capabilitiesFromPublic(public storage.PostingCapabilities) posting.Capabilities {
	capabilities := posting.Capabilities{}
	if !isNilDependency(public.ApprovalGate) {
		capabilities.ApprovalGate = publicApprovalGate{gate: public.ApprovalGate}
	}
	if !isNilDependency(public.FinancialDimensions) {
		capabilities.FinancialDimensions = publicDimensionPolicy{policy: public.FinancialDimensions}
	}
	if !isNilDependency(public.MakerCheckerPolicy) {
		capabilities.MakerCheckerPolicy = publicMakerCheckerPolicy{policy: public.MakerCheckerPolicy}
	}
	if !isNilDependency(public.Locker) {
		capabilities.PostingLocker = publicPostingLocker{locker: public.Locker}
	}
	return capabilities
}

func isNilDependency(dependency any) bool {
	if dependency == nil {
		return true
	}
	value := reflect.ValueOf(dependency)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func mergeCapabilities(primary, additional posting.Capabilities) posting.Capabilities {
	if primary.ApprovalGate == nil {
		primary.ApprovalGate = additional.ApprovalGate
	}
	if primary.FinancialDimensions == nil {
		primary.FinancialDimensions = additional.FinancialDimensions
	}
	if primary.MakerCheckerPolicy == nil {
		primary.MakerCheckerPolicy = additional.MakerCheckerPolicy
	}
	if primary.PolicyUnit == nil {
		primary.PolicyUnit = additional.PolicyUnit
	}
	if primary.PostingLocker == nil {
		primary.PostingLocker = additional.PostingLocker
	}
	return primary
}

type publicApprovalGate struct {
	gate storage.ApprovalGate
}

func (g publicApprovalGate) IsApproved(ctx context.Context, companyID, entryID uuid.UUID) (bool, error) {
	return g.gate.IsApproved(ctx, types.CompanyID(companyID), types.JournalEntryID(entryID))
}

type publicDimensionPolicy struct {
	policy storage.FinancialDimensionPolicy
}

func (p publicDimensionPolicy) RequiresCostCenter(ctx context.Context, companyID, accountID uuid.UUID) (bool, error) {
	return p.policy.RequiresCostCenter(ctx, types.CompanyID(companyID), types.AccountID(accountID))
}

func (p publicDimensionPolicy) ValidateCostCenter(ctx context.Context, companyID, costCenterID uuid.UUID) error {
	return p.policy.ValidateCostCenter(ctx, types.CompanyID(companyID), types.CostCenterID(costCenterID))
}

func (p publicDimensionPolicy) ValidateAnalyticalDimension(
	ctx context.Context,
	companyID uuid.UUID,
	kind masterdata.AnalyticalDimensionKind,
	dimensionID uuid.UUID,
) error {
	return p.policy.ValidateAnalyticalDimension(ctx, types.CompanyID(companyID), string(kind), dimensionID)
}

type publicMakerCheckerPolicy struct {
	policy storage.MakerCheckerPolicy
}

func (p publicMakerCheckerPolicy) MakerCheckerThreshold(ctx context.Context, companyID uuid.UUID) (decimal.Decimal, bool, error) {
	return p.policy.MakerCheckerThreshold(ctx, types.CompanyID(companyID))
}

type publicPostingLocker struct {
	locker storage.PostingLocker
}

func (l publicPostingLocker) WithinPostingLock(
	ctx context.Context,
	companyID uuid.UUID,
	journalID uuid.UUID,
	fn func(ctx context.Context) error,
) error {
	return l.locker.WithinPostingLock(ctx, types.CompanyID(companyID), types.JournalID(journalID), fn)
}
