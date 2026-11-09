package masterdata

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrAccountCodeRequired             = errors.New("account code is required")
	ErrAccountNameRequired             = errors.New("account name is required")
	ErrInvalidAccountType              = errors.New("invalid account type")
	ErrInvalidParent                   = errors.New("invalid parent account")
	ErrAccountCycle                    = errors.New("account hierarchy contains a cycle")
	ErrInvalidSubClassificationForType = errors.New("invalid sub-classification for the specified account type")
)

const (
	SubClassCashAndEquivalents     = "cash_and_equivalents"
	SubClassTradeReceivables       = "trade_receivables"
	SubClassInventories            = "inventories"
	SubClassPropertyPlantEquipment = "property_plant_equipment"
	SubClassIntangibleAssets       = "intangible_assets"
	SubClassRightOfUseAssets       = "right_of_use_assets"
	SubClassFinancialInvestments   = "financial_investments"
	SubClassPrepaymentsOtherDebit  = "prepayments_and_other_debit"
	SubClassOtherNonCurrentAssets  = "other_non_current_assets"
	SubClassCurrentAssets          = "current_assets"
	SubClassNonCurrentAssets       = "non_current_assets"

	SubClassTradePayables              = "trade_payables"
	SubClassShortTermBorrowings        = "short_term_borrowings"
	SubClassCurrentLeaseLiabilities    = "current_lease_liabilities"
	SubClassAccrualsOtherCredit        = "accruals_and_other_credit"
	SubClassCurrentTaxLiabilities      = "current_tax_liabilities"
	SubClassLongTermBorrowings         = "long_term_borrowings"
	SubClassNonCurrentLeaseLiabilities = "non_current_lease_liabilities"
	SubClassEndOfServiceProvisions     = "end_of_service_provisions"
	SubClassOtherNonCurrentLiabilities = "other_non_current_liabilities"
	SubClassCurrentLiabilities         = "current_liabilities"
	SubClassNonCurrentLiabilities      = "non_current_liabilities"

	SubClassShareCapital     = "share_capital"
	SubClassSharePremium     = "share_premium"
	SubClassStatutoryReserve = "statutory_reserve"
	SubClassOCIReserves      = "oci_reserves"
	SubClassRetainedEarnings = "retained_earnings"
	SubClassTreasuryShares   = "treasury_shares"

	SubClassOperatingRevenue           = "operating_revenue"
	SubClassCostOfSales                = "cost_of_sales"
	SubClassSellingDistributionExpense = "selling_distribution_expense"
	SubClassGeneralAdminExpense        = "general_admin_expense"
	SubClassDepreciationAmortExpense   = "depreciation_amortization_expense"
	SubClassExpectedCreditLossExpense  = "expected_credit_loss_expense"
	SubClassOtherOperatingIncome       = "other_operating_income"
	SubClassOtherOperatingExpense      = "other_operating_expense"
	SubClassFinanceIncome              = "finance_income"
	SubClassFinanceCost                = "finance_cost"
	SubClassTaxExpense                 = "tax_expense"
	SubClassIncomeTaxExpense           = "income_tax_expense"
	SubClassOCINonRecyclable           = "oci_non_recyclable"
	SubClassOCIRecyclable              = "oci_recyclable"

	// Legacy aliases
	SubClassAccountsReceivable = "accounts_receivable"
	SubClassAccountsPayable    = "accounts_payable"
	SubClassBank               = "bank"
)

type AccountType string

const (
	AccountTypeAsset     AccountType = "asset"
	AccountTypeLiability AccountType = "liability"
	AccountTypeEquity    AccountType = "equity"
	AccountTypeIncome    AccountType = "income"
	AccountTypeExpense   AccountType = "expense"
	AccountTypeRevenue   AccountType = "income"
)

type NormalBalance string

const (
	NormalBalanceDebit  NormalBalance = "debit"
	NormalBalanceCredit NormalBalance = "credit"
)

type Account struct {
	CompanyID           uuid.UUID
	ID                  uuid.UUID
	Code                string
	Name                string
	AccountType         AccountType
	NormalBalance       NormalBalance
	CurrencyID          string
	ParentID            *uuid.UUID
	IsReconcilable      bool
	IsActive            bool
	IsCashEquivalent    bool
	IsNonCashAdjustment bool
	CashFlowActivity    string
	IFRSCategory        string
	SubClassification   string
	IsHeader            bool
	IsContraAccount     bool
	HasPostedEntries    bool
	DisplayOrder        int
	CreatedAt           time.Time
	UpdatedAt           time.Time
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

// Validate verifies code, name, normal balance, and strict SubClassification compatibility.
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
		a.NormalBalance = DefaultNormalBalance(a.AccountType)
		if a.NormalBalance == "" {
			return ErrInvalidAccountType
		}
	}
	if !a.IsContraAccount && a.NormalBalance != DefaultNormalBalance(a.AccountType) {
		return ErrInvalidAccountType
	}
	if a.SubClassification != "" {
		if err := validateSubClassification(a.AccountType, a.SubClassification); err != nil {
			return err
		}
	}
	return nil
}

func validateSubClassification(accType AccountType, sub string) error {
	sub = strings.ToLower(strings.TrimSpace(sub))
	switch accType {
	case AccountTypeAsset:
		valid := map[string]bool{
			SubClassCashAndEquivalents: true, SubClassTradeReceivables: true,
			SubClassInventories: true, SubClassPropertyPlantEquipment: true,
			SubClassIntangibleAssets: true, SubClassRightOfUseAssets: true,
			SubClassFinancialInvestments: true, SubClassPrepaymentsOtherDebit: true,
			SubClassOtherNonCurrentAssets: true, SubClassCurrentAssets: true,
			SubClassNonCurrentAssets: true, SubClassAccountsReceivable: true,
			SubClassBank: true, "cash": true, "fixed_asset": true, "fixed_assets": true, "ppe": true,
		}
		if !valid[sub] && !strings.Contains(sub, "asset") && !strings.Contains(sub, "cash") && !strings.Contains(sub, "receiv") {
			return ErrInvalidSubClassificationForType
		}
	case AccountTypeLiability:
		valid := map[string]bool{
			SubClassTradePayables: true, SubClassShortTermBorrowings: true,
			SubClassCurrentLeaseLiabilities: true, SubClassAccrualsOtherCredit: true,
			SubClassCurrentTaxLiabilities: true, SubClassLongTermBorrowings: true,
			SubClassNonCurrentLeaseLiabilities: true, SubClassEndOfServiceProvisions: true,
			SubClassOtherNonCurrentLiabilities: true, SubClassCurrentLiabilities: true,
			SubClassNonCurrentLiabilities: true, SubClassAccountsPayable: true,
			"payable": true, "loan": true, "lease": true,
		}
		if !valid[sub] && !strings.Contains(sub, "liabilit") && !strings.Contains(sub, "payable") && !strings.Contains(sub, "borrow") {
			return ErrInvalidSubClassificationForType
		}
	case AccountTypeEquity:
		valid := map[string]bool{
			SubClassShareCapital: true, SubClassSharePremium: true,
			SubClassStatutoryReserve: true, SubClassOCIReserves: true,
			SubClassRetainedEarnings: true, SubClassTreasuryShares: true,
			"capital": true, "reserve": true, "equity": true,
		}
		if !valid[sub] && !strings.Contains(sub, "equity") && !strings.Contains(sub, "share") && !strings.Contains(sub, "reserve") && !strings.Contains(sub, "retained") {
			return ErrInvalidSubClassificationForType
		}
	case AccountTypeIncome:
		valid := map[string]bool{
			SubClassOperatingRevenue: true, SubClassOtherOperatingIncome: true,
			SubClassFinanceIncome: true, SubClassOCINonRecyclable: true,
			SubClassOCIRecyclable: true, "revenue": true, "sales": true, "income": true,
		}
		if !valid[sub] && !strings.Contains(sub, "revenue") && !strings.Contains(sub, "income") && !strings.Contains(sub, "sale") {
			return ErrInvalidSubClassificationForType
		}
	case AccountTypeExpense:
		valid := map[string]bool{
			SubClassCostOfSales: true, SubClassSellingDistributionExpense: true,
			SubClassGeneralAdminExpense: true, SubClassDepreciationAmortExpense: true,
			SubClassExpectedCreditLossExpense: true, SubClassOtherOperatingExpense: true,
			SubClassFinanceCost: true, SubClassTaxExpense: true,
			SubClassIncomeTaxExpense: true, "expense": true, "cost": true, "depreciation": true, "provision": true,
		}
		if !valid[sub] && !strings.Contains(sub, "expense") && !strings.Contains(sub, "cost") && !strings.Contains(sub, "deprec") && !strings.Contains(sub, "tax") {
			return ErrInvalidSubClassificationForType
		}
	}
	return nil
}

func (a Account) IsCurrentAsset() bool {
	return a.AccountType == AccountTypeAsset && taxonomyContains(a.SubClassification, "current")
}

func (a Account) EffectiveIsCashEquivalent() bool {
	classification := strings.ToLower(strings.TrimSpace(a.SubClassification))
	if a.IsCashEquivalent {
		return true
	}
	return a.AccountType == AccountTypeAsset &&
		(classification == SubClassCashAndEquivalents || classification == SubClassBank || strings.Contains(classification, "cash"))
}

func (a Account) IsPPE() bool {
	return a.AccountType == AccountTypeAsset && taxonomyContains(a.SubClassification,
		SubClassPropertyPlantEquipment, "fixed_asset", "fixed_assets", "ppe")
}

func (a Account) IsCurrent() bool {
	classification := strings.ToLower(strings.TrimSpace(a.SubClassification))
	return strings.Contains(classification, "current") ||
		classification == SubClassShortTermBorrowings ||
		classification == SubClassCurrentLeaseLiabilities ||
		classification == SubClassTradePayables ||
		classification == SubClassAccountsPayable
}

func taxonomyContains(value string, terms ...string) bool {
	value = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "-", "_"))
	for _, term := range terms {
		term = strings.ToLower(strings.ReplaceAll(term, "-", "_"))
		if strings.Contains(value, term) || strings.Contains(term, value) && value != "" {
			return true
		}
	}
	return false
}
