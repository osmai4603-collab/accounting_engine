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
	ErrInvalidControlAccount           = errors.New("control accounts must be reconcilable asset or liability accounts")
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
	SubClassDistributions    = "dividends_distributions"

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

const (
	IFRSCategoryOperating              = "operating"
	IFRSCategoryInvesting              = "investing"
	IFRSCategoryFinancing              = "financing"
	IFRSCategoryTax                    = "tax"
	IFRSCategoryIncomeTaxes            = "income_taxes"
	IFRSCategoryDiscontinued           = "discontinued"
	IFRSCategoryDiscontinuedOperations = "discontinued_operations"
	IFRSCategoryEquityMovements        = "equity_movements"
	IFRSCategoryBalanceSheetPerpetual  = "balance_sheet_perpetual"
	CashFlowOperating                  = "operating"
	CashFlowInvesting                  = "investing"
	CashFlowFinancing                  = "financing"
	CashFlowCashEquivalent             = "cash_equivalents"
	CashFlowNonCashAdjustment          = "non_cash"
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
	IsControlAccount    bool
	IsActive            bool
	IsCashEquivalent    bool
	IsNonCashAdjustment bool
	CashFlowActivity    string
	IFRSCategory        string
	SubClassification   string
	IsHeader            bool
	IsContraAccount     bool
	HasPostedEntries    bool
	RequiresCostCenter  bool
	DisplayOrder        int
	EffectiveFrom       time.Time
	EffectiveTo         *time.Time
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
	if a.IsControlAccount && (!a.IsReconcilable ||
		(a.AccountType != AccountTypeAsset && a.AccountType != AccountTypeLiability)) {
		return ErrInvalidControlAccount
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
		if !valid[sub] {
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
		if !valid[sub] {
			return ErrInvalidSubClassificationForType
		}
	case AccountTypeEquity:
		valid := map[string]bool{
			SubClassShareCapital: true, SubClassSharePremium: true,
			SubClassStatutoryReserve: true, SubClassOCIReserves: true,
			SubClassRetainedEarnings: true, SubClassTreasuryShares: true,
			SubClassDistributions: true,
			"capital":             true, "reserve": true, "equity": true,
		}
		if !valid[sub] {
			return ErrInvalidSubClassificationForType
		}
	case AccountTypeIncome:
		valid := map[string]bool{
			SubClassOperatingRevenue: true, SubClassOtherOperatingIncome: true,
			SubClassFinanceIncome: true, SubClassOCINonRecyclable: true,
			SubClassOCIRecyclable: true, "revenue": true, "sales": true, "income": true,
		}
		if !valid[sub] {
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
		if !valid[sub] {
			return ErrInvalidSubClassificationForType
		}
	}
	return nil
}

func (a Account) IsCurrentAsset() bool {
	if a.AccountType != AccountTypeAsset {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(a.SubClassification)) {
	case SubClassCashAndEquivalents, SubClassAccountsReceivable, SubClassTradeReceivables,
		SubClassInventories, SubClassPrepaymentsOtherDebit, SubClassCurrentAssets,
		SubClassBank, "cash":
		return true
	default:
		return false
	}
}

func (a Account) EffectiveIsCashEquivalent() bool {
	classification := strings.ToLower(strings.TrimSpace(a.SubClassification))
	if a.IsCashEquivalent {
		return true
	}
	return a.AccountType == AccountTypeAsset &&
		(classification == SubClassCashAndEquivalents || classification == SubClassBank || classification == "cash")
}

func (a Account) IsPPE() bool {
	if a.AccountType != AccountTypeAsset {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(a.SubClassification)) {
	case SubClassPropertyPlantEquipment, "fixed_asset", "fixed_assets", "ppe":
		return true
	default:
		return false
	}
}

func (a Account) IsCurrent() bool {
	classification := strings.ToLower(strings.TrimSpace(a.SubClassification))
	switch a.AccountType {
	case AccountTypeAsset:
		return a.IsCurrentAsset()
	case AccountTypeLiability:
		switch classification {
		case SubClassShortTermBorrowings, SubClassCurrentLeaseLiabilities,
			SubClassTradePayables, SubClassAccrualsOtherCredit,
			SubClassCurrentTaxLiabilities, SubClassCurrentLiabilities,
			SubClassAccountsPayable, "payable":
			return true
		default:
			return false
		}
	default:
		return false
	}
}
