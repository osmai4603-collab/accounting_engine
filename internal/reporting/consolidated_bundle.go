package reporting

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/consolidation"
	"github.com/osmai4603-collab/accounting_engine/internal/valuation"
	"github.com/shopspring/decimal"
)

var ErrInvalidConsolidationEntity = errors.New("reporting: consolidation entity must be a non-nil company UUID")
var ErrConsolidationAccessCheckerRequired = errors.New("reporting: consolidation access checker is required")

func (e *BundleEngine) GenerateConsolidated(
	ctx context.Context,
	group consolidation.Group,
) (ConsolidatedStatementBundle, error) {
	if ctx == nil {
		return ConsolidatedStatementBundle{}, errors.New("reporting: consolidation context is required")
	}
	if err := ctx.Err(); err != nil {
		return ConsolidatedStatementBundle{}, err
	}
	if e == nil || e.repository == nil {
		return ConsolidatedStatementBundle{}, ErrCompanyScopeRequired
	}
	companies := make(map[string]uuid.UUID, len(group.Entities))
	companyIDs := make([]uuid.UUID, 0, len(group.Entities))
	for _, entity := range group.Entities {
		companyID, err := uuid.Parse(entity.ID)
		if err != nil || companyID == uuid.Nil {
			return ConsolidatedStatementBundle{}, fmt.Errorf("%w: %q", ErrInvalidConsolidationEntity, entity.ID)
		}
		if _, exists := companies[entity.ID]; exists {
			return ConsolidatedStatementBundle{}, fmt.Errorf("%w: duplicate entity %s", consolidation.ErrInvalidInput, entity.ID)
		}
		companies[entity.ID] = companyID
		companyIDs = append(companyIDs, companyID)
	}
	if e.consolidationAccessChecker == nil {
		return ConsolidatedStatementBundle{}, ErrConsolidationAccessCheckerRequired
	}
	if err := e.consolidationAccessChecker.AuthorizeConsolidation(ctx, group.ID, companyIDs); err != nil {
		return ConsolidatedStatementBundle{}, fmt.Errorf("authorize consolidated reporting: %w", err)
	}

	trialBalances := make([]consolidation.TrialBalance, 0, len(group.Entities))
	for _, entity := range group.Entities {
		companyID := companies[entity.ID]
		trialBalance, err := NewTrialBalanceEngine(e.repository).GenerateForCompanyContext(
			ctx, companyID, group.Period.StartDate, group.Period.EndDate, entity.FunctionalCurrency,
		)
		if err != nil {
			return ConsolidatedStatementBundle{}, fmt.Errorf("generate trial balance for %s: %w", entity.ID, err)
		}
		converted, err := consolidateTrialBalance(entity.ID, trialBalance)
		if err != nil {
			return ConsolidatedStatementBundle{}, err
		}
		trialBalances = append(trialBalances, converted)
	}
	rateProvider := reportingConsolidationRates{ctx: ctx, repository: e.repository, companies: companies}
	result, err := consolidation.NewEngine(rateProvider).Consolidate(group, trialBalances)
	if err != nil {
		return ConsolidatedStatementBundle{}, err
	}
	return ConsolidatedStatementBundle{Statements: result, TrialBalances: trialBalances}, nil
}

func consolidateTrialBalance(entityID string, trialBalance TrialBalance) (consolidation.TrialBalance, error) {
	result := consolidation.TrialBalance{
		EntityID: entityID, StartDate: trialBalance.StartDate,
		EndDate: trialBalance.EndDate, Currency: trialBalance.Currency,
		Lines: make([]consolidation.TrialBalanceLine, 0, len(trialBalance.Lines)),
	}
	for _, line := range trialBalance.Lines {
		result.Lines = append(result.Lines, consolidation.TrialBalanceLine{
			AccountID: line.AccountID.String(), AccountCode: line.AccountCode,
			PeriodDebit: line.PeriodDebit, PeriodCredit: line.PeriodCredit,
			EndingDebit: line.EndingDebit, EndingCredit: line.EndingCredit,
		})
	}
	return result, nil
}

type reportingConsolidationRates struct {
	ctx        context.Context
	repository Repository
	companies  map[string]uuid.UUID
}

func (r reportingConsolidationRates) Rate(request consolidation.RateRequest) (decimal.Decimal, error) {
	companyID, ok := r.companies[request.EntityID]
	if !ok {
		return decimal.Zero, fmt.Errorf("%w: entity %s has no company mapping", consolidation.ErrMissingExchangeRate, request.EntityID)
	}
	rateType, err := consolidationRateType(request.Class)
	if err != nil {
		return decimal.Zero, err
	}
	return r.repository.GetRateOfTypeForCompany(
		r.ctx, companyID, rateType, request.FromCurrency, request.ToCurrency, request.AsOfDate,
	)
}

func consolidationRateType(rateClass consolidation.RateClass) (valuation.RateType, error) {
	switch rateClass {
	case consolidation.RateClosing:
		return valuation.ClosingRate, nil
	case consolidation.RateAverage:
		return valuation.AverageRate, nil
	case consolidation.RateHistorical:
		return valuation.SpotRate, nil
	default:
		return "", consolidation.ErrInvalidInput
	}
}
