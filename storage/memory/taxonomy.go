package memory

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/reporting/taxonomy"
	"github.com/shopspring/decimal"
)

var _ taxonomy.BalanceReader = (*Driver)(nil)

func (d *Driver) GetPeriodBalances(
	ctx context.Context,
	companyID, periodID uuid.UUID,
	currency string,
) (taxonomy.PeriodBalances, error) {
	if err := ctx.Err(); err != nil {
		return taxonomy.PeriodBalances{}, err
	}
	if companyID == uuid.Nil || periodID == uuid.Nil || strings.TrimSpace(currency) == "" {
		return taxonomy.PeriodBalances{}, errors.New("taxonomy: company, period, and currency are required")
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	period, exists := d.periods[periodID]
	if !exists || period.CompanyID != companyID {
		return taxonomy.PeriodBalances{}, masterdata.ErrFiscalPeriodNotFound
	}
	company, exists := d.companies[companyID]
	if !exists {
		return taxonomy.PeriodBalances{}, masterdata.ErrInvalidCompany
	}
	if company.FunctionalCurrency != currency {
		return taxonomy.PeriodBalances{AsOfDate: period.EndDate}, nil
	}

	type balanceTotals struct {
		beginningDebit  decimal.Decimal
		beginningCredit decimal.Decimal
		periodDebit     decimal.Decimal
		periodCredit    decimal.Decimal
		endingDebit     decimal.Decimal
		endingCredit    decimal.Decimal
	}
	totals := make(map[uuid.UUID]balanceTotals)
	for key, stored := range d.balances {
		if key.companyID != companyID || key.currency != currency {
			continue
		}
		storedPeriod, exists := d.periods[key.periodID]
		if !exists || storedPeriod.CompanyID != companyID ||
			storedPeriod.FiscalYearID != period.FiscalYearID || storedPeriod.EndDate.After(period.EndDate) ||
			(storedPeriod.EndDate.Equal(period.EndDate) && storedPeriod.Number > period.Number) {
			continue
		}
		balance := totals[key.accountID]
		balance.beginningDebit = balance.beginningDebit.Add(stored.beginningDebit)
		balance.beginningCredit = balance.beginningCredit.Add(stored.beginningCredit)
		balance.endingDebit = balance.endingDebit.Add(stored.beginningDebit).Add(stored.periodDebit)
		balance.endingCredit = balance.endingCredit.Add(stored.beginningCredit).Add(stored.periodCredit)
		if key.periodID == periodID {
			balance.periodDebit = stored.periodDebit
			balance.periodCredit = stored.periodCredit
		}
		totals[key.accountID] = balance
	}
	accountBalances := make([]taxonomy.AccountBalance, 0, len(totals))
	for accountID, balance := range totals {
		account, exists := d.accounts[accountID]
		if !exists || account.CompanyID != companyID {
			continue
		}
		periodAmount := balance.periodDebit.Sub(balance.periodCredit)
		endingAmount := balance.endingDebit.Sub(balance.endingCredit)
		if account.NormalBalance == masterdata.NormalBalanceCredit {
			periodAmount = periodAmount.Neg()
			endingAmount = endingAmount.Neg()
		}
		accountBalances = append(accountBalances, taxonomy.AccountBalance{
			AccountID:    accountID,
			Code:         account.Code,
			Tags:         taxonomy.TagsForAccount(account),
			Amount:       periodAmount,
			PeriodAmount: periodAmount,
			EndingAmount: endingAmount,
		})
	}
	sort.Slice(accountBalances, func(i, j int) bool {
		return accountBalances[i].Code < accountBalances[j].Code
	})
	return taxonomy.PeriodBalances{AsOfDate: period.EndDate, Accounts: accountBalances}, nil
}
