package ledger

import (
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
)

type AccountType string

const (
	AccountTypeAsset     AccountType = "asset"
	AccountTypeLiability AccountType = "liability"
	AccountTypeEquity    AccountType = "equity"
	AccountTypeIncome    AccountType = "income"
	AccountTypeExpense   AccountType = "expense"

	// AccountTypeRevenue is a deprecated alias for AccountTypeIncome. Both
	// constants carry the same wire value ("income"), so persisted accounts and
	// reports remain unchanged.
	//
	// Deprecated: Use AccountTypeIncome instead.
	AccountTypeRevenue AccountType = "income"
)

type NormalBalance string

const (
	NormalBalanceDebit  NormalBalance = "debit"
	NormalBalanceCredit NormalBalance = "credit"
)

type Account struct {
	ID                types.AccountID
	CompanyID         types.CompanyID
	Code              string
	Name              string
	Type              AccountType
	SubClassification string
	NormalBalance     NormalBalance
	ParentID          *types.AccountID
	IsReconcilable    bool
	IsActive          bool
	Currency          string
}
