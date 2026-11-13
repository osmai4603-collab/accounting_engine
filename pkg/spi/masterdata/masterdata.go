// Package masterdata defines the public storage contract for accounting master data.
package masterdata

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"

	"github.com/google/uuid"
)

var ErrCapabilityNotImplemented = types.ErrCapabilityNotImplemented

var (
	ErrInvalidJournal       = errors.New("invalid journal")
	ErrCompanyNotFound      = errors.New("company not found")
	ErrJournalNotFound      = errors.New("journal not found")
	ErrDuplicateJournalCode = errors.New("journal code already exists")
	ErrDuplicateJournalID   = errors.New("journal id already exists")
)

type AccountType string

const (
	AccountTypeAsset     AccountType = "asset"
	AccountTypeLiability AccountType = "liability"
	AccountTypeEquity    AccountType = "equity"
	AccountTypeIncome    AccountType = "income"
	AccountTypeExpense   AccountType = "expense"
)

type NormalBalance string

const (
	NormalBalanceDebit  NormalBalance = "debit"
	NormalBalanceCredit NormalBalance = "credit"
)

type FiscalYearState string

const (
	FiscalYearOpen       FiscalYearState = "open"
	FiscalYearSoftClosed FiscalYearState = "soft_closed"
	FiscalYearHardClosed FiscalYearState = "hard_closed"
)

type PeriodState string

const (
	PeriodOpen       PeriodState = "open"
	PeriodSoftClosed PeriodState = "soft_closed"
	PeriodHardClosed PeriodState = "hard_closed"
)

type FiscalPeriodType string

const (
	FiscalPeriodTypeNormal            FiscalPeriodType = "normal"
	FiscalPeriodTypeYearEndAdjustment FiscalPeriodType = "year_end_adjustment"
	FiscalPeriodTypeAuditAdjustment   FiscalPeriodType = "audit_adjustment"
)

type Company struct {
	ID                   types.CompanyID
	Code                 string
	LegalName            string
	FunctionalCurrency   string
	PresentationCurrency string
	Active               bool
}

type JournalID uuid.UUID

func NewJournalID() JournalID { return JournalID(uuid.New()) }

// Journal is the public, company-scoped master-data representation of a
// posting journal.
type Journal struct {
	ID        JournalID
	CompanyID types.CompanyID
	Code      string
	Name      string
}

func (j Journal) Validate() error {
	if j.ID == (JournalID{}) || j.CompanyID == (types.CompanyID{}) ||
		strings.TrimSpace(j.Code) == "" || strings.TrimSpace(j.Name) == "" ||
		utf8.RuneCountInString(j.Code) > 32 || utf8.RuneCountInString(j.Name) > 100 {
		return ErrInvalidJournal
	}
	return nil
}

type Account struct {
	ID                  types.AccountID
	CompanyID           types.CompanyID
	Code                string
	Name                string
	Type                AccountType
	NormalBalance       NormalBalance
	Currency            string
	ParentID            *types.AccountID
	IsReconcilable      bool
	IsControlAccount    bool
	IsContraAccount     bool
	IsActive            bool
	RequiresCostCenter  bool
	IsCashEquivalent    bool
	IsNonCashAdjustment bool
	CashFlowActivity    string
	SubClassification   string
	IFRSCategory        string
	DisplayOrder        int
}

type FiscalYear struct {
	ID        types.FiscalYearID
	CompanyID types.CompanyID
	Name      string
	StartDate time.Time
	EndDate   time.Time
	State     FiscalYearState
}

type FiscalPeriod struct {
	ID           types.FiscalPeriodID
	CompanyID    types.CompanyID
	FiscalYearID types.FiscalYearID
	Number       int
	Name         string
	StartDate    time.Time
	EndDate      time.Time
	State        PeriodState
	Type         FiscalPeriodType
}

// Store exposes company-scoped reads and typed creation of the minimum master
// data required to prepare an accounting company for use.
type Store interface {
	CreateCompany(context.Context, Company) error
	GetCompany(context.Context, types.CompanyID) (Company, error)
	CreateJournal(context.Context, Journal) error
	GetJournalByCode(context.Context, types.CompanyID, string) (Journal, error)
	CreateAccount(context.Context, Account) error
	GetAccount(context.Context, types.CompanyID, types.AccountID) (Account, error)
	GetAccountByCode(context.Context, types.CompanyID, string) (Account, error)
	CreateFiscalYear(context.Context, FiscalYear) error
	GetFiscalYear(context.Context, types.CompanyID, types.FiscalYearID) (FiscalYear, error)
	CreateFiscalPeriod(context.Context, FiscalPeriod) error
	GetFiscalPeriod(context.Context, types.CompanyID, types.FiscalPeriodID) (FiscalPeriod, error)
}
