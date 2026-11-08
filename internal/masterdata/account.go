package masterdata

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrAccountCodeRequired = errors.New("account code is required")
	ErrAccountNameRequired = errors.New("account name is required")
	ErrInvalidAccountType  = errors.New("invalid account type")
	ErrInvalidParent       = errors.New("invalid parent account")
	ErrAccountCycle        = errors.New("account hierarchy contains a cycle")
)

type AccountType string

const (
	AccountTypeAsset     AccountType = "asset"
	AccountTypeLiability AccountType = "liability"
	AccountTypeEquity     AccountType = "equity"
	AccountTypeIncome    AccountType = "income"
	AccountTypeExpense   AccountType = "expense"
)

type NormalBalance string

const (
	NormalBalanceDebit  NormalBalance = "debit"
	NormalBalanceCredit NormalBalance = "credit"
)

type Account struct {
	ID                uuid.UUID
	Code              string
	Name              string
	AccountType       AccountType
	NormalBalance     NormalBalance
	SubClassification string
	ParentID          *uuid.UUID
	IsHeader          bool
	HasPostedEntries  bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func DefaultNormalBalance(accountType AccountType) NormalBalance {
	switch accountType {
	case AccountTypeAsset, AccountTypeExpense:
		return NormalBalanceDebit
	case AccountTypeLiability, AccountTypeEquity, AccountTypeIncome:
		return NormalBalanceCredit
	default:
		return ""
	}
}

func (a Account) Validate() error {
	if strings.TrimSpace(a.Code) == "" {
		return ErrAccountCodeRequired
	}
	if strings.TrimSpace(a.Name) == "" {
		return ErrAccountNameRequired
	}
	if DefaultNormalBalance(a.AccountType) == "" {
		return ErrInvalidAccountType
	}
	if a.NormalBalance == "" {
		return DefaultNormalBalance(a.AccountType)
	}
	if a.NormalBalance != DefaultNormalBalance(a.AccountType) {
		return ErrInvalidAccountType
	}
	return nil
}
