package contractparity

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicmasterdata "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/shopspring/decimal"
)

// UnifiedFixture defines the standard deterministic master-data setup
// used across all adapters in the contract parity suite.
type UnifiedFixture struct {
	Company            publicmasterdata.Company
	Journal            publicmasterdata.Journal
	CashAccount        publicmasterdata.Account
	ReceivableAccount  publicmasterdata.Account
	PayableAccount     publicmasterdata.Account
	EquityAccount      publicmasterdata.Account
	RevenueAccount     publicmasterdata.Account
	ExpenseAccount     publicmasterdata.Account
	FiscalYear         publicmasterdata.FiscalYear
	OpenPeriod         publicmasterdata.FiscalPeriod
	ClosedPeriod       publicmasterdata.FiscalPeriod
	FunctionalCurrency string
	ExchangeRate       decimal.Decimal
}

// NewUnifiedFixture constructs a fresh fixture with deterministic structure
// but fresh UUIDs to prevent collisions across isolated test runs.
func NewUnifiedFixture() UnifiedFixture {
	companyID := types.NewCompanyID()
	journalID := publicmasterdata.NewJournalID()

	cashID := types.NewAccountID()
	receivableID := types.NewAccountID()
	payableID := types.NewAccountID()
	equityID := types.NewAccountID()
	revenueID := types.NewAccountID()
	expenseID := types.NewAccountID()

	yearID := types.NewFiscalYearID()
	openPeriodID := types.NewFiscalPeriodID()
	closedPeriodID := types.NewFiscalPeriodID()

	currency := "USD"

	return UnifiedFixture{
		Company: publicmasterdata.Company{
			ID:                   companyID,
			Code:                 fmt.Sprintf("PARITY-%s", uuid.UUID(companyID).String()[:8]),
			LegalName:            "Contract Parity Corp",
			FunctionalCurrency:   currency,
			PresentationCurrency: currency,
			Active:               true,
		},
		Journal: publicmasterdata.Journal{
			ID:        journalID,
			CompanyID: companyID,
			Code:      "GEN",
			Name:      "General Journal",
		},
		CashAccount: publicmasterdata.Account{
			ID:            cashID,
			CompanyID:     companyID,
			Code:          "1000",
			Name:          "Cash and Equivalents",
			Type:          publicmasterdata.AccountTypeAsset,
			NormalBalance: publicmasterdata.NormalBalanceDebit,
			Currency:      currency,
			IsActive:      true,
		},
		ReceivableAccount: publicmasterdata.Account{
			ID:            receivableID,
			CompanyID:     companyID,
			Code:          "1200",
			Name:          "Accounts Receivable",
			Type:          publicmasterdata.AccountTypeAsset,
			NormalBalance: publicmasterdata.NormalBalanceDebit,
			Currency:      currency,
			IsActive:      true,
		},
		PayableAccount: publicmasterdata.Account{
			ID:            payableID,
			CompanyID:     companyID,
			Code:          "2000",
			Name:          "Accounts Payable",
			Type:          publicmasterdata.AccountTypeLiability,
			NormalBalance: publicmasterdata.NormalBalanceCredit,
			Currency:      currency,
			IsActive:      true,
		},
		EquityAccount: publicmasterdata.Account{
			ID:            equityID,
			CompanyID:     companyID,
			Code:          "3000",
			Name:          "Retained Earnings",
			Type:          publicmasterdata.AccountTypeEquity,
			NormalBalance: publicmasterdata.NormalBalanceCredit,
			Currency:      currency,
			IsActive:      true,
		},
		RevenueAccount: publicmasterdata.Account{
			ID:            revenueID,
			CompanyID:     companyID,
			Code:          "4000",
			Name:          "Operating Revenue",
			Type:          publicmasterdata.AccountTypeIncome,
			NormalBalance: publicmasterdata.NormalBalanceCredit,
			Currency:      currency,
			IsActive:      true,
		},
		ExpenseAccount: publicmasterdata.Account{
			ID:            expenseID,
			CompanyID:     companyID,
			Code:          "5000",
			Name:          "Operating Expense",
			Type:          publicmasterdata.AccountTypeExpense,
			NormalBalance: publicmasterdata.NormalBalanceDebit,
			Currency:      currency,
			IsActive:      true,
		},
		FiscalYear: publicmasterdata.FiscalYear{
			ID:        yearID,
			CompanyID: companyID,
			Name:      "FY2026",
			StartDate: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
			EndDate:   time.Date(2026, time.December, 31, 0, 0, 0, 0, time.UTC),
			State:     publicmasterdata.FiscalYearOpen,
		},
		OpenPeriod: publicmasterdata.FiscalPeriod{
			ID:           openPeriodID,
			CompanyID:    companyID,
			FiscalYearID: yearID,
			Number:       1,
			Name:         "January 2026",
			StartDate:    time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
			EndDate:      time.Date(2026, time.January, 31, 0, 0, 0, 0, time.UTC),
			State:        publicmasterdata.PeriodOpen,
			Type:         publicmasterdata.FiscalPeriodTypeNormal,
		},
		ClosedPeriod: publicmasterdata.FiscalPeriod{
			ID:           closedPeriodID,
			CompanyID:    companyID,
			FiscalYearID: yearID,
			Number:       2,
			Name:         "February 2026 (Closed)",
			StartDate:    time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC),
			EndDate:      time.Date(2026, time.February, 28, 0, 0, 0, 0, time.UTC),
			State:        publicmasterdata.PeriodHardClosed,
			Type:         publicmasterdata.FiscalPeriodTypeNormal,
		},
		FunctionalCurrency: currency,
		ExchangeRate:       decimal.NewFromInt(1),
	}
}

// ProvisionFixture populates the master-data store with the unified fixture items.
func ProvisionFixture(ctx context.Context, store publicmasterdata.Store, f UnifiedFixture) error {
	if err := store.CreateCompany(ctx, f.Company); err != nil {
		return fmt.Errorf("create company: %w", err)
	}
	if err := store.CreateJournal(ctx, f.Journal); err != nil {
		return fmt.Errorf("create journal: %w", err)
	}
	for _, acc := range []publicmasterdata.Account{
		f.CashAccount,
		f.ReceivableAccount,
		f.PayableAccount,
		f.EquityAccount,
		f.RevenueAccount,
		f.ExpenseAccount,
	} {
		if err := store.CreateAccount(ctx, acc); err != nil {
			return fmt.Errorf("create account %s: %w", acc.Code, err)
		}
	}
	if err := store.CreateFiscalYear(ctx, f.FiscalYear); err != nil {
		return fmt.Errorf("create fiscal year: %w", err)
	}
	if err := store.CreateFiscalPeriod(ctx, f.OpenPeriod); err != nil {
		return fmt.Errorf("create open fiscal period: %w", err)
	}
	if err := store.CreateFiscalPeriod(ctx, f.ClosedPeriod); err != nil {
		return fmt.Errorf("create closed fiscal period: %w", err)
	}
	return nil
}
