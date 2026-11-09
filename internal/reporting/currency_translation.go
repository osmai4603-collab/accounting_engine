package reporting

import (
	"context"
	"errors"
	"time"

	financialtypes "accounting_engine/internal/types"
	"accounting_engine/internal/valuation"
	"github.com/shopspring/decimal"
)

var ErrCurrencyTranslatorRequired = errors.New("currency translation requires an exchange-rate provider")

type TranslationClassification string

const (
	TranslationAsset     TranslationClassification = "asset"
	TranslationLiability TranslationClassification = "liability"
	TranslationEquity    TranslationClassification = "equity"
)

type BalanceSheetItem struct {
	Amount         decimal.Decimal
	Classification TranslationClassification
	HistoricalDate time.Time
}

type IncomeStatementItem struct {
	Amount decimal.Decimal
}

type EquityItem struct {
	Amount         decimal.Decimal
	HistoricalDate time.Time
}

type CurrencyTranslator struct {
	rateProvider valuation.RateProviderSPI
}

func NewCurrencyTranslator(rateProvider valuation.RateProviderSPI) *CurrencyTranslator {
	return &CurrencyTranslator{rateProvider: rateProvider}
}

func (ct *CurrencyTranslator) TranslateAmount(
	ctx context.Context,
	amount financialtypes.Money,
	toCurrency string,
	date time.Time,
	rateType valuation.RateType,
) (financialtypes.Money, error) {
	if amount.Currency() == toCurrency {
		return amount, nil
	}
	if ct == nil || ct.rateProvider == nil {
		return financialtypes.Money{}, ErrCurrencyTranslatorRequired
	}
	rate, err := getTranslationRate(ctx, ct.rateProvider, rateType, amount.Currency(), toCurrency, date)
	if err != nil {
		return financialtypes.Money{}, err
	}
	return valuation.ConvertAtRate(amount, toCurrency, rate)
}

func (ct *CurrencyTranslator) TranslateBalanceSheet(
	items []BalanceSheetItem,
	fromCurrency, toCurrency string,
	closingDate time.Time,
) ([]BalanceSheetItem, financialtypes.Money, error) {
	translated := make([]BalanceSheetItem, len(items))
	netAssets := decimal.Zero
	for i, item := range items {
		rateType, rateDate := valuation.ClosingRate, closingDate
		if item.Classification == TranslationEquity {
			rateType, rateDate = valuation.SpotRate, item.HistoricalDate
		}
		amount, err := ct.translateDecimal(context.Background(), item.Amount, fromCurrency, toCurrency, rateDate, rateType)
		if err != nil {
			return nil, financialtypes.Money{}, err
		}
		translated[i] = item
		translated[i].Amount = amount
		switch item.Classification {
		case TranslationAsset:
			netAssets = netAssets.Add(amount)
		case TranslationLiability:
			netAssets = netAssets.Sub(amount)
		case TranslationEquity:
			netAssets = netAssets.Sub(amount)
		default:
			return nil, financialtypes.Money{}, errors.New("unknown balance-sheet translation classification")
		}
	}
	difference, err := financialtypes.NewMoney(netAssets, toCurrency)
	if err != nil {
		return nil, financialtypes.Money{}, err
	}
	return translated, difference, nil
}

func (ct *CurrencyTranslator) TranslateIncomeStatement(
	items []IncomeStatementItem,
	fromCurrency, toCurrency string,
	periodStart, periodEnd time.Time,
) ([]IncomeStatementItem, error) {
	rateDate := periodEnd
	if rateDate.IsZero() {
		rateDate = periodStart
	}
	translated := make([]IncomeStatementItem, len(items))
	for i, item := range items {
		amount, err := ct.translateDecimal(context.Background(), item.Amount, fromCurrency, toCurrency, rateDate, valuation.AverageRate)
		if err != nil {
			return nil, err
		}
		translated[i] = IncomeStatementItem{Amount: amount}
	}
	return translated, nil
}

func (ct *CurrencyTranslator) TranslateEquity(
	items []EquityItem,
	fromCurrency, toCurrency string,
) ([]EquityItem, error) {
	translated := make([]EquityItem, len(items))
	for i, item := range items {
		amount, err := ct.translateDecimal(context.Background(), item.Amount, fromCurrency, toCurrency, item.HistoricalDate, valuation.SpotRate)
		if err != nil {
			return nil, err
		}
		translated[i] = EquityItem{Amount: amount, HistoricalDate: item.HistoricalDate}
	}
	return translated, nil
}

func (ct *CurrencyTranslator) translateDecimal(
	ctx context.Context,
	amount decimal.Decimal,
	fromCurrency, toCurrency string,
	date time.Time,
	rateType valuation.RateType,
) (decimal.Decimal, error) {
	money, err := financialtypes.NewMoney(amount, fromCurrency)
	if err != nil {
		return decimal.Zero, err
	}
	translated, err := ct.TranslateAmount(ctx, money, toCurrency, date, rateType)
	if err != nil {
		return decimal.Zero, err
	}
	return translated.Amount(), nil
}

func getTranslationRate(
	ctx context.Context,
	provider valuation.RateProviderSPI,
	rateType valuation.RateType,
	from, to string,
	date time.Time,
) (decimal.Decimal, error) {
	if typed, ok := provider.(interface {
		GetRateOfType(context.Context, valuation.RateType, string, string, time.Time) (decimal.Decimal, error)
	}); ok {
		return typed.GetRateOfType(ctx, rateType, from, to, date)
	}
	return provider.GetRate(ctx, from, to, date)
}
