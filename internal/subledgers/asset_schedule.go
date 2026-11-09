package subledgers

import (
	"time"

	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

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
