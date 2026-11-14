package adapter

import (
	"fmt"
	"strings"
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicmasterdata "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/shopspring/decimal"
)

// Fixture is the explicit master-data provision for the experimental adapter.
// The adapter never invents identifiers: the caller supplies the company,
// journal, accounts, fiscal year and fiscal period the scenario uses.
type Fixture struct {
	Company            publicmasterdata.Company
	Journal            publicmasterdata.Journal
	Accounts           []publicmasterdata.Account
	FiscalYear         publicmasterdata.FiscalYear
	FiscalPeriod       publicmasterdata.FiscalPeriod
	FunctionalCurrency string
	// ExchangeRate is the transaction-to-functional rate supplied by the
	// scenario's currency source.
	ExchangeRate decimal.Decimal
}

// DefaultFixture returns a self-contained January 2026 USD fixture with one
// asset account and one income account.
func DefaultFixture() Fixture {
	companyID := types.NewCompanyID()
	journalID := publicmasterdata.NewJournalID()

	cashAccountID := types.NewAccountID()
	revenueAccountID := types.NewAccountID()

	fiscalYear := publicmasterdata.FiscalYear{
		ID:        types.NewFiscalYearID(),
		CompanyID: companyID,
		Name:      "FY2026",
		StartDate: date(2026, 1, 1),
		EndDate:   date(2026, 12, 31),
		State:     publicmasterdata.FiscalYearOpen,
	}

	return Fixture{
		Company: publicmasterdata.Company{
			ID:                   companyID,
			Code:                 "EXT",
			LegalName:            "External Adapter Consumer Ltd",
			FunctionalCurrency:   "USD",
			PresentationCurrency: "USD",
			Active:               true,
		},
		Journal: publicmasterdata.Journal{
			ID:        journalID,
			CompanyID: companyID,
			Code:      "GENERAL",
			Name:      "General journal",
		},
		Accounts: []publicmasterdata.Account{
			{
				ID:            cashAccountID,
				CompanyID:     companyID,
				Code:          "1000",
				Name:          "Cash",
				Type:          publicmasterdata.AccountTypeAsset,
				NormalBalance: publicmasterdata.NormalBalanceDebit,
				Currency:      "USD",
				IsActive:      true,
			},
			{
				ID:            revenueAccountID,
				CompanyID:     companyID,
				Code:          "4000",
				Name:          "Revenue",
				Type:          publicmasterdata.AccountTypeIncome,
				NormalBalance: publicmasterdata.NormalBalanceCredit,
				Currency:      "USD",
				IsActive:      true,
			},
		},
		FiscalYear: fiscalYear,
		FiscalPeriod: publicmasterdata.FiscalPeriod{
			ID:           types.NewFiscalPeriodID(),
			CompanyID:    companyID,
			FiscalYearID: fiscalYear.ID,
			Number:       1,
			Name:         "January 2026",
			StartDate:    date(2026, 1, 1),
			EndDate:      date(2026, 1, 31),
			State:        publicmasterdata.PeriodOpen,
			Type:         publicmasterdata.FiscalPeriodTypeNormal,
		},
		FunctionalCurrency: "USD",
		ExchangeRate:       decimal.NewFromInt(1),
	}
}

// Account returns the fixture account with the given code.
func (f Fixture) Account(code string) (publicmasterdata.Account, error) {
	for _, account := range f.Accounts {
		if strings.EqualFold(strings.TrimSpace(account.Code), strings.TrimSpace(code)) {
			return account, nil
		}
	}
	return publicmasterdata.Account{}, fmt.Errorf("%w: account code %q", ErrUnknownAccount, code)
}

func date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}
