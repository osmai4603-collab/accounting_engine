package subledgers

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	apisubledger "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/subledger"
	apitypes "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	spisubledger "github.com/osmai4603-collab/accounting_engine/pkg/spi/subledger"
	"github.com/shopspring/decimal"
)

var _ spisubledger.DepreciationCalculator = (*FixedAssetLedger)(nil)
var _ spisubledger.FixedAssetStore = (*FixedAssetLedger)(nil)

func (l *FixedAssetLedger) SaveAsset(
	ctx context.Context,
	companyID apitypes.CompanyID,
	public apisubledger.FixedAsset,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	companyUUID := uuid.UUID(companyID)
	publicCompanyUUID := uuid.UUID(public.CompanyID)
	if companyUUID == uuid.Nil || publicCompanyUUID != companyUUID {
		return ErrCompanyMismatch
	}
	if l.companyID != uuid.Nil && l.companyID != companyUUID {
		return ErrCompanyMismatch
	}
	if strings.TrimSpace(public.ID) == "" || public.AcquisitionDate.IsZero() {
		return ErrInvalidAsset
	}
	assetID, err := uuid.Parse(public.ID)
	if err != nil || assetID == uuid.Nil {
		return ErrInvalidAsset
	}
	cost, err := internalAssetMoney(public.AcquisitionCost)
	if err != nil {
		return err
	}
	salvage, err := internalAssetMoney(public.SalvageValue)
	if err != nil {
		return err
	}
	accumulated, err := internalAssetMoney(public.AccumulatedDep)
	if err != nil {
		return err
	}
	if accumulated.Currency() == "" {
		accumulated, err = financialtypes.Zero(cost.Currency())
		if err != nil {
			return err
		}
	}
	netBookValue, err := internalAssetMoney(public.NetBookValue)
	if err != nil {
		return err
	}
	bookBeforeAdjustment, err := cost.Subtract(accumulated)
	if err != nil {
		return err
	}
	revaluationAdjustment, err := financialtypes.Zero(cost.Currency())
	if err != nil {
		return err
	}
	if netBookValue.Currency() != "" {
		revaluationAdjustment, err = netBookValue.Subtract(bookBeforeAdjustment)
		if err != nil {
			return err
		}
	} else {
		netBookValue = bookBeforeAdjustment
	}
	method := DepreciationMethod(public.DepreciationMethod)
	if method != StraightLine && method != DecliningBalance {
		return ErrInvalidAsset
	}
	switch strings.ToLower(strings.TrimSpace(public.Status)) {
	case "", "active":
	case "cwip":
		return fmt.Errorf("%w: public fixed-asset contract has no CWIP account mapping", ErrInvalidAsset)
	default:
		return ErrInvalidAsset
	}
	if _, err := l.repo.GetFixedAsset(ctx, companyUUID, assetID); err == nil {
		return ErrAlreadyExists
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	asset := FixedAsset{
		CompanyID: companyUUID, ID: assetID, HistoricalCost: cost, SalvageValue: salvage,
		UsefulLifeMonths: public.UsefulLifeMonths, Method: method, InServiceDate: public.AcquisitionDate,
		AccumulatedDepreciation: accumulated, CurrentBookValue: netBookValue,
		RevaluationAdjustment: revaluationAdjustment,
	}
	if err := l.RegisterAssetContext(ctx, asset); err != nil {
		return err
	}
	return nil
}

func (l *FixedAssetLedger) GetAsset(
	ctx context.Context,
	companyID apitypes.CompanyID,
	id string,
) (apisubledger.FixedAsset, error) {
	if err := ctx.Err(); err != nil {
		return apisubledger.FixedAsset{}, err
	}
	if uuid.UUID(companyID) == uuid.Nil ||
		(l.companyID != uuid.Nil && l.companyID != uuid.UUID(companyID)) {
		return apisubledger.FixedAsset{}, ErrCompanyMismatch
	}
	assetID, err := uuid.Parse(id)
	if err != nil || assetID == uuid.Nil {
		return apisubledger.FixedAsset{}, ErrInvalidAsset
	}
	asset, err := l.repo.GetFixedAsset(ctx, uuid.UUID(companyID), assetID)
	if err != nil {
		return apisubledger.FixedAsset{}, err
	}
	return publicFixedAsset(asset)
}

func (l *FixedAssetLedger) ListAssets(
	ctx context.Context,
	companyID apitypes.CompanyID,
) ([]apisubledger.FixedAsset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if uuid.UUID(companyID) == uuid.Nil ||
		(l.companyID != uuid.Nil && l.companyID != uuid.UUID(companyID)) {
		return nil, ErrCompanyMismatch
	}
	assets, err := l.repo.ListFixedAssets(ctx)
	if err != nil {
		return nil, err
	}
	companyUUID := uuid.UUID(companyID)
	result := make([]apisubledger.FixedAsset, 0)
	for _, asset := range assets {
		if asset.CompanyID != companyUUID {
			continue
		}
		public, err := publicFixedAsset(asset)
		if err != nil {
			return nil, err
		}
		result = append(result, public)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func internalAssetMoney(value apitypes.Money) (financialtypes.Money, error) {
	if value.Currency() == "" {
		return financialtypes.Money{}, nil
	}
	return financialtypes.NewMoney(value.Amount(), value.Currency())
}

func publicAssetMoney(value financialtypes.Money) (apitypes.Money, error) {
	if value.Currency() == "" {
		return apitypes.Money{}, nil
	}
	return apitypes.NewMoney(value.Amount(), value.Currency())
}

func publicFixedAsset(asset FixedAsset) (apisubledger.FixedAsset, error) {
	cost, err := publicAssetMoney(asset.HistoricalCost)
	if err != nil {
		return apisubledger.FixedAsset{}, err
	}
	salvage, err := publicAssetMoney(asset.SalvageValue)
	if err != nil {
		return apisubledger.FixedAsset{}, err
	}
	accumulated, err := publicAssetMoney(asset.AccumulatedDepreciation)
	if err != nil {
		return apisubledger.FixedAsset{}, err
	}
	netBookValue, err := publicAssetMoney(asset.CurrentBookValue)
	if err != nil {
		return apisubledger.FixedAsset{}, err
	}
	status := "active"
	if asset.DisposedAt != nil {
		status = "disposed"
	} else if asset.IsCWIP {
		status = "cwip"
	}
	return apisubledger.FixedAsset{
		ID: asset.ID.String(), CompanyID: apitypes.CompanyID(asset.CompanyID),
		AcquisitionDate: asset.InServiceDate, AcquisitionCost: cost,
		UsefulLifeMonths: asset.UsefulLifeMonths, SalvageValue: salvage,
		DepreciationMethod: string(asset.Method), AccumulatedDep: accumulated,
		NetBookValue: netBookValue, Status: status,
	}, nil
}

func (l *FixedAssetLedger) ComputeMonthlyDepreciation(asset apisubledger.FixedAsset) (apitypes.Money, error) {
	cost := asset.AcquisitionCost
	salvage := asset.SalvageValue
	if cost.Currency() == "" || salvage.Currency() != cost.Currency() ||
		cost.IsNegative() || salvage.IsNegative() || salvage.GreaterThan(cost) ||
		asset.UsefulLifeMonths <= 0 {
		return apitypes.Money{}, ErrInvalidAsset
	}
	accumulated := asset.AccumulatedDep
	if accumulated.Currency() == "" {
		var err error
		accumulated, err = apitypes.Zero(cost.Currency())
		if err != nil {
			return apitypes.Money{}, err
		}
	}
	if accumulated.Currency() != cost.Currency() || accumulated.IsNegative() {
		return apitypes.Money{}, ErrInvalidAsset
	}
	bookValue := asset.NetBookValue
	if bookValue.Currency() == "" {
		var err error
		bookValue, err = cost.Subtract(accumulated)
		if err != nil {
			return apitypes.Money{}, err
		}
	}
	if bookValue.Currency() != cost.Currency() || bookValue.IsNegative() || bookValue.LessThan(salvage) {
		return apitypes.Money{}, ErrInvalidAsset
	}
	depreciable, err := bookValue.Subtract(salvage)
	if err != nil {
		return apitypes.Money{}, err
	}
	amount := decimal.Zero
	switch asset.DepreciationMethod {
	case string(StraightLine):
		originalBasis, err := cost.Subtract(salvage)
		if err != nil {
			return apitypes.Money{}, err
		}
		amount = originalBasis.Amount().Div(decimal.NewFromInt(int64(asset.UsefulLifeMonths)))
	case string(DecliningBalance):
		rate := decimal.NewFromInt(2).Div(decimal.NewFromInt(int64(asset.UsefulLifeMonths)))
		amount = bookValue.Amount().Mul(rate)
	default:
		return apitypes.Money{}, ErrInvalidAsset
	}
	if amount.GreaterThan(depreciable.Amount()) {
		amount = depreciable.Amount()
	}
	internalAmount, err := financialtypes.NewMoney(amount, cost.Currency())
	if err != nil {
		return apitypes.Money{}, err
	}
	return apitypes.NewMoney(internalAmount.RoundForPosting().Amount(), internalAmount.Currency())
}

type AssetDepreciationScheduleLine struct {
	AssetID     uuid.UUID
	ComponentID uuid.UUID
	Period      time.Time
	Amount      financialtypes.Money
	BookValue   financialtypes.Money
}

func CalculateProrataDepreciation(cost, salvage decimal.Decimal, usefulLifeMonths int, startDate, targetMonth time.Time) (decimal.Decimal, error) {
	if cost.IsNegative() || salvage.IsNegative() || salvage.GreaterThan(cost) || usefulLifeMonths <= 0 ||
		startDate.IsZero() || targetMonth.IsZero() {
		return decimal.Zero, ErrInvalidAsset
	}
	serviceDate := startDate.UTC()
	lifeStart := time.Date(serviceDate.Year(), serviceDate.Month(), serviceDate.Day(), 0, 0, 0, 0, time.UTC)
	lifeEnd := lifeStart.AddDate(0, usefulLifeMonths, 0)
	periodStart := time.Date(targetMonth.UTC().Year(), targetMonth.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	periodEnd := periodStart.AddDate(0, 1, 0)
	if periodStart.Before(lifeStart) {
		periodStart = lifeStart
	}
	if periodEnd.After(lifeEnd) {
		periodEnd = lifeEnd
	}
	if !periodEnd.After(periodStart) {
		return decimal.Zero, nil
	}
	totalDays := decimal.NewFromInt(int64(lifeEnd.Sub(lifeStart).Hours() / 24))
	if totalDays.IsZero() {
		return decimal.Zero, ErrInvalidAsset
	}
	days := decimal.NewFromInt(int64(periodEnd.Sub(periodStart).Hours() / 24))
	return cost.Sub(salvage).Mul(days).Div(totalDays), nil
}

func GenerateDepreciationSchedule(asset FixedAsset) ([]AssetDepreciationScheduleLine, error) {
	if asset.ID == uuid.Nil || asset.HistoricalCost.Currency() == "" || asset.SalvageValue.Currency() != asset.HistoricalCost.Currency() ||
		asset.SalvageValue.GreaterThan(asset.HistoricalCost) || asset.InServiceDate.IsZero() ||
		asset.UsefulLifeMonths <= 0 {
		return nil, ErrInvalidAsset
	}
	components, err := depreciationComponents(asset)
	if err != nil {
		return nil, err
	}
	lines := make([]AssetDepreciationScheduleLine, 0)
	for _, component := range components {
		componentLines, err := generateComponentSchedule(asset.ID, component)
		if err != nil {
			return nil, err
		}
		lines = append(lines, componentLines...)
	}
	return lines, nil
}

func depreciationComponents(asset FixedAsset) ([]AssetComponent, error) {
	if asset.IsCWIP {
		return nil, ErrInvalidAsset
	}
	if len(asset.Components) == 0 {
		return []AssetComponent{{
			ID: uuid.Nil, Name: "asset", Cost: asset.HistoricalCost, SalvageValue: asset.SalvageValue,
			UsefulLifeMonths: asset.UsefulLifeMonths, Method: asset.Method, InServiceDate: asset.InServiceDate,
			DecliningRate: asset.DecliningRate,
		}}, nil
	}
	total := decimal.Zero
	components := append([]AssetComponent(nil), asset.Components...)
	for i := range components {
		component := &components[i]
		if component.ID == uuid.Nil || component.Name == "" || component.Cost.Currency() != asset.HistoricalCost.Currency() ||
			component.SalvageValue.Currency() != component.Cost.Currency() ||
			component.Cost.IsNegative() || component.SalvageValue.IsNegative() ||
			component.SalvageValue.GreaterThan(component.Cost) || component.UsefulLifeMonths <= 0 ||
			(component.Method != StraightLine && component.Method != DecliningBalance) {
			return nil, ErrInvalidAsset
		}
		if component.InServiceDate.IsZero() {
			component.InServiceDate = asset.InServiceDate
		}
		total = total.Add(component.Cost.Amount())
	}
	if !total.Equal(asset.HistoricalCost.Amount()) {
		return nil, ErrInvalidAsset
	}
	return components, nil
}

func generateComponentSchedule(assetID uuid.UUID, component AssetComponent) ([]AssetDepreciationScheduleLine, error) {
	var err error
	bookValue := component.Cost
	lifeEnd := component.InServiceDate.AddDate(0, component.UsefulLifeMonths, 0)
	period := time.Date(component.InServiceDate.UTC().Year(), component.InServiceDate.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	lines := make([]AssetDepreciationScheduleLine, 0, component.UsefulLifeMonths+1)
	for period.Before(lifeEnd) && bookValue.GreaterThan(component.SalvageValue) {
		amount := decimal.Zero
		switch component.Method {
		case StraightLine:
			amount, err = CalculateProrataDepreciation(
				component.Cost.Amount(), component.SalvageValue.Amount(), component.UsefulLifeMonths,
				component.InServiceDate, period,
			)
		case DecliningBalance:
			rate := component.DecliningRate
			if rate.IsZero() {
				rate = decimal.NewFromInt(2).Div(decimal.NewFromInt(int64(component.UsefulLifeMonths)))
			}
			if rate.IsNegative() || rate.GreaterThan(decimal.NewFromInt(1)) {
				return nil, ErrInvalidAsset
			}
			nextPeriod := period.AddDate(0, 1, 0)
			start := period
			if start.Before(component.InServiceDate) {
				start = component.InServiceDate
			}
			end := nextPeriod
			if end.After(lifeEnd) {
				end = lifeEnd
			}
			daysInPeriod := decimal.NewFromInt(int64(nextPeriod.Sub(period).Hours() / 24))
			days := decimal.NewFromInt(int64(end.Sub(start).Hours() / 24))
			amount = bookValue.Amount().Mul(rate).Mul(days).Div(daysInPeriod)
		default:
			return nil, ErrInvalidAsset
		}
		if err != nil {
			return nil, err
		}
		depreciation, err := financialtypes.NewMoney(amount, component.Cost.Currency())
		if err != nil {
			return nil, err
		}
		depreciation = depreciation.RoundForPosting()
		remaining, err := bookValue.Subtract(component.SalvageValue)
		if err != nil {
			return nil, err
		}
		if depreciation.GreaterThan(remaining) || period.AddDate(0, 1, 0).After(lifeEnd) {
			depreciation = remaining.RoundForPosting()
		}
		bookValue, err = bookValue.Subtract(depreciation)
		if err != nil {
			return nil, err
		}
		if !depreciation.Amount().IsZero() {
			lines = append(lines, AssetDepreciationScheduleLine{
				AssetID: assetID, ComponentID: component.ID, Period: period,
				Amount: depreciation, BookValue: bookValue,
			})
		}
		period = period.AddDate(0, 1, 0)
	}
	return lines, nil
}
