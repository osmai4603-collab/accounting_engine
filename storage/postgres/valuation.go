package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"accounting_engine/internal/masterdata"
	financialtypes "accounting_engine/internal/types"
	"accounting_engine/internal/valuation"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

func (d *Driver) GetRate(ctx context.Context, from, to string, date time.Time) (decimal.Decimal, error) {
	return decimal.Zero, masterdata.ErrCompanyScopeRequired
}
func (d *Driver) GetRateOfType(ctx context.Context, rateType valuation.RateType, from, to string, date time.Time) (decimal.Decimal, error) {
	return decimal.Zero, masterdata.ErrCompanyScopeRequired
}

func (d *Driver) GetRateForCompany(ctx context.Context, companyID uuid.UUID, from, to string, date time.Time) (decimal.Decimal, error) {
	return d.getRateForCompany(ctx, companyID, "", from, to, date)
}

func (d *Driver) GetRateOfTypeForCompany(
	ctx context.Context,
	companyID uuid.UUID,
	rateType valuation.RateType,
	from, to string,
	date time.Time,
) (decimal.Decimal, error) {
	return d.getRateForCompany(ctx, companyID, rateType, from, to, date)
}

func (d *Driver) getRateForCompany(
	ctx context.Context,
	companyID uuid.UUID,
	rateType valuation.RateType,
	from, to string,
	date time.Time,
) (decimal.Decimal, error) {
	if companyID == uuid.Nil {
		return decimal.Zero, masterdata.ErrCompanyScopeRequired
	}
	if from == to {
		return decimal.NewFromInt(1), nil
	}
	query := `SELECT rate FROM exchange_rates
		WHERE company_id=$1 AND lower(from_currency)=lower($2) AND lower(to_currency)=lower($3)
		  AND effective_date<=$4::date`
	args := []any{companyID, from, to, date}
	if rateType != "" {
		query += ` AND rate_type=$5`
		args = append(args, rateType)
	}
	query += ` ORDER BY effective_date DESC LIMIT 1`
	var rate decimal.Decimal
	inverseRate := false
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, query, args...).Scan(&rate)
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		inverseRate = true
		query = `SELECT rate FROM exchange_rates
			WHERE company_id=$1 AND lower(from_currency)=lower($3) AND lower(to_currency)=lower($2)
			  AND effective_date<=$4::date`
		args = []any{companyID, from, to, date}
		if rateType != "" {
			query += ` AND rate_type=$5`
			args = append(args, rateType)
		}
		query += ` ORDER BY effective_date DESC LIMIT 1`
		err = tx.QueryRow(ctx, query, args...).Scan(&rate)
		if errors.Is(err, pgx.ErrNoRows) {
			return valuation.ErrRateNotFound
		}
		return err
	})
	if err != nil {
		return decimal.Zero, err
	}
	if inverseRate {
		return decimal.NewFromInt(1).DivRound(rate, financialtypes.InternalScale), nil
	}
	return rate, nil
}

func (d *Driver) UpsertExchangeRate(ctx context.Context, companyID uuid.UUID, rate valuation.ExchangeRate) error {
	if companyID == uuid.Nil {
		return masterdata.ErrInvalidCompany
	}
	if err := rate.Validate(); err != nil {
		return err
	}
	return d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO exchange_rates(
			company_id,from_currency,to_currency,rate,effective_date,rate_type
		) VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT(company_id,from_currency,to_currency,effective_date,rate_type)
		DO UPDATE SET rate=EXCLUDED.rate`,
			companyID, strings.ToUpper(strings.TrimSpace(rate.FromCurrency)),
			strings.ToUpper(strings.TrimSpace(rate.ToCurrency)), rate.Rate,
			rate.EffectiveDate, rate.Type)
		return err
	})
}
