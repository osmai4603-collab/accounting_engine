package reporting

import (
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
)

type TrialBalanceLine struct {
	AccountID     types.AccountID
	AccountCode   string
	AccountName   string
	AccountType   string
	OpeningDebit  types.Money
	OpeningCredit types.Money
	PeriodDebit   types.Money
	PeriodCredit  types.Money
	EndingDebit   types.Money
	EndingCredit  types.Money
}

type TrialBalance struct {
	CompanyID   types.CompanyID
	AsOfDate    time.Time
	Currency    string
	Lines       []TrialBalanceLine
	TotalDebit  types.Money
	TotalCredit types.Money
	IsBalanced  bool
}

type GeneralLedgerEntry struct {
	EntryID        types.JournalEntryID
	EntryNumber    string
	EffectiveDate  time.Time
	Description    string
	Debit          types.Money
	Credit         types.Money
	RunningBalance types.Money
}

type GeneralLedgerReport struct {
	CompanyID      types.CompanyID
	AccountID      types.AccountID
	AccountCode    string
	AccountName    string
	StartDate      time.Time
	EndDate        time.Time
	OpeningBalance types.Money
	Entries        []GeneralLedgerEntry
	EndingBalance  types.Money
}

type BalanceSheetSection struct {
	Title       string
	Lines       []BalanceSheetLine
	TotalAmount types.Money
}

type BalanceSheetLine struct {
	AccountID   types.AccountID
	AccountCode string
	AccountName string
	Amount      types.Money
}

type BalanceSheetReport struct {
	CompanyID       types.CompanyID
	AsOfDate        time.Time
	Currency        string
	Assets          BalanceSheetSection
	Liabilities     BalanceSheetSection
	Equity          BalanceSheetSection
	TotalAssets     types.Money
	TotalLiabEquity types.Money
	IsBalanced      bool
}

type IncomeStatementSection struct {
	Title       string
	Lines       []IncomeStatementLine
	TotalAmount types.Money
}

type IncomeStatementLine struct {
	AccountID   types.AccountID
	AccountCode string
	AccountName string
	Amount      types.Money
}

type IncomeStatementReport struct {
	CompanyID types.CompanyID
	StartDate time.Time
	EndDate   time.Time
	Currency  string
	Revenues  IncomeStatementSection
	Expenses  IncomeStatementSection
	NetIncome types.Money
}
