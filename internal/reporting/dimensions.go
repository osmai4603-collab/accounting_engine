package reporting

import (
	"context"
	"errors"
	"sort"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/valuation"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var ErrDimensionAccountNotFound = errors.New("dimension report references an unknown account")

type DimensionProfitKey struct {
	AccountID      uuid.UUID
	CostCenterID   uuid.UUID
	ProjectID      uuid.UUID
	SegmentID      uuid.UUID
	BusinessUnitID uuid.UUID
}

type DimensionProfitLine struct {
	Key    DimensionProfitKey
	Amount decimal.Decimal
}

type DimensionProfitAndLoss struct {
	StartDate time.Time
	EndDate   time.Time
	Currency  string
	Lines     []DimensionProfitLine
}

func (e *Engine) GenerateDimensionProfitAndLoss(start, end time.Time, currency string) (DimensionProfitAndLoss, error) {
	accounts, err := listAccounts(e.TrialBalance.repository)
	if err != nil {
		return DimensionProfitAndLoss{}, err
	}
	entries, err := listPostedEntries(e.TrialBalance.repository, start, end)
	if err != nil {
		return DimensionProfitAndLoss{}, err
	}
	accountByID := make(map[uuid.UUID]masterdata.Account, len(accounts))
	for _, account := range accounts {
		accountByID[account.ID] = account
	}
	amounts := make(map[DimensionProfitKey]decimal.Decimal)
	for _, entry := range entries {
		if entry.Status != posting.EntryPosted {
			continue
		}
		date := entryEffectiveDate(entry)
		if date.Before(start) || date.After(end) {
			continue
		}
		for _, line := range entry.Lines {
			account, ok := accountByID[line.AccountID]
			if !ok {
				return DimensionProfitAndLoss{}, ErrDimensionAccountNotFound
			}
			if account.AccountType != masterdata.AccountTypeIncome && account.AccountType != masterdata.AccountTypeExpense {
				continue
			}
			debit, credit, err := translateLineAmountsUsingRate(
				line, currency, rateProvider(e.TrialBalance.repository), valuation.AverageRate, date,
			)
			if err != nil {
				return DimensionProfitAndLoss{}, err
			}
			net := credit.Sub(debit)
			if account.AccountType == masterdata.AccountTypeExpense {
				net = debit.Sub(credit)
			}
			key := DimensionProfitKey{
				AccountID: account.ID, CostCenterID: uuidValue(line.CostCenterID),
				ProjectID: uuidValue(line.ProjectID), SegmentID: uuidValue(line.SegmentID),
				BusinessUnitID: uuidValue(line.BusinessUnitID),
			}
			amounts[key] = amounts[key].Add(net)
		}
	}
	report := DimensionProfitAndLoss{StartDate: start, EndDate: end, Currency: currency}
	for key, amount := range amounts {
		report.Lines = append(report.Lines, DimensionProfitLine{Key: key, Amount: amount})
	}
	sort.Slice(report.Lines, func(i, j int) bool {
		left, right := report.Lines[i].Key, report.Lines[j].Key
		if left.AccountID != right.AccountID {
			return left.AccountID.String() < right.AccountID.String()
		}
		if left.CostCenterID != right.CostCenterID {
			return left.CostCenterID.String() < right.CostCenterID.String()
		}
		if left.ProjectID != right.ProjectID {
			return left.ProjectID.String() < right.ProjectID.String()
		}
		if left.SegmentID != right.SegmentID {
			return left.SegmentID.String() < right.SegmentID.String()
		}
		return left.BusinessUnitID.String() < right.BusinessUnitID.String()
	})
	return report, nil
}

func (e *Engine) GenerateDimensionProfitAndLossForCompanyContext(
	ctx context.Context,
	companyID uuid.UUID,
	start, end time.Time,
	currency string,
) (DimensionProfitAndLoss, error) {
	repository, err := newCompanyReportingRepository(ctx, e.TrialBalance.repository, companyID, end)
	if err != nil {
		return DimensionProfitAndLoss{}, err
	}
	return NewEngine(repository).GenerateDimensionProfitAndLoss(start, end, currency)
}

func uuidValue(value *uuid.UUID) uuid.UUID {
	if value == nil {
		return uuid.Nil
	}
	return *value
}
