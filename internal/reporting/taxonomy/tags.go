package taxonomy

import (
	"strings"

	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
)

func TagsForAccount(account masterdata.Account) []string {
	sub := strings.ToLower(strings.TrimSpace(account.SubClassification))
	tags := []string{
		string(account.AccountType),
		sub,
		strings.ToLower(strings.TrimSpace(account.IFRSCategory)),
	}
	switch sub {
	case masterdata.SubClassOperatingRevenue, masterdata.SubClassOtherOperatingIncome:
		tags = append(tags, "revenue")
	case masterdata.SubClassCostOfSales:
		tags = append(tags, "cost_of_sales", "materials_consumed")
	case masterdata.SubClassSellingDistributionExpense:
		tags = append(tags, "selling_expense")
	case masterdata.SubClassGeneralAdminExpense:
		tags = append(tags, "administrative_expense", "employee_expense")
	case masterdata.SubClassDepreciationAmortExpense, masterdata.SubClassExpectedCreditLossExpense:
		tags = append(tags, "depreciation_expense")
	case masterdata.SubClassOtherOperatingExpense:
		tags = append(tags, "other_operating_expense")
	case masterdata.SubClassFinanceIncome:
		tags = append(tags, "investing_revenue", "nonoperating_income")
	case masterdata.SubClassFinanceCost:
		tags = append(tags, "finance_cost")
	case masterdata.SubClassTaxExpense, masterdata.SubClassIncomeTaxExpense:
		tags = append(tags, "income_tax")
	case masterdata.SubClassOCINonRecyclable, masterdata.SubClassOCIRecyclable:
		tags = append(tags, "oci_income", "oci")
	}
	switch account.AccountType {
	case masterdata.AccountTypeAsset:
		if account.IsCurrentAsset() {
			tags = append(tags, "current_asset")
		} else {
			tags = append(tags, "noncurrent_asset")
		}
	case masterdata.AccountTypeLiability:
		if account.IsCurrent() {
			tags = append(tags, "current_liability")
		} else {
			tags = append(tags, "noncurrent_liability")
		}
	case masterdata.AccountTypeEquity:
		tags = append(tags, "equity")
	case masterdata.AccountTypeIncome:
		switch strings.ToLower(strings.TrimSpace(account.IFRSCategory)) {
		case masterdata.IFRSCategoryOperating:
			tags = append(tags, "revenue")
		case masterdata.IFRSCategoryInvesting:
			tags = append(tags, "investing_revenue", "nonoperating_income")
		case masterdata.IFRSCategoryFinancing:
			tags = append(tags, "financing_revenue", "nonoperating_income")
		case masterdata.IFRSCategoryTax, masterdata.IFRSCategoryIncomeTaxes:
			tags = append(tags, "income_tax")
		case masterdata.IFRSCategoryEquityMovements:
			tags = append(tags, "oci_income", "oci")
		case masterdata.IFRSCategoryDiscontinued, masterdata.IFRSCategoryDiscontinuedOperations:
			tags = append(tags, "discontinued_income")
		}
	case masterdata.AccountTypeExpense:
		switch strings.ToLower(strings.TrimSpace(account.IFRSCategory)) {
		case masterdata.IFRSCategoryTax, masterdata.IFRSCategoryIncomeTaxes:
			tags = append(tags, "income_tax")
		case masterdata.IFRSCategoryInvesting:
			tags = append(tags, "investing_expense")
		case masterdata.IFRSCategoryFinancing:
			tags = append(tags, "finance_cost")
		case masterdata.IFRSCategoryEquityMovements:
			tags = append(tags, "oci_expense", "oci")
		case masterdata.IFRSCategoryDiscontinued, masterdata.IFRSCategoryDiscontinuedOperations:
			tags = append(tags, "discontinued_expense")
		case masterdata.IFRSCategoryOperating:
			switch strings.ToLower(strings.TrimSpace(account.SubClassification)) {
			case masterdata.SubClassCostOfSales:
				tags = append(tags, "cost_of_sales", "materials_consumed")
			case masterdata.SubClassSellingDistributionExpense:
				tags = append(tags, "selling_expense")
			case masterdata.SubClassGeneralAdminExpense:
				tags = append(tags, "administrative_expense", "employee_expense")
			case masterdata.SubClassDepreciationAmortExpense, masterdata.SubClassExpectedCreditLossExpense:
				tags = append(tags, "depreciation_expense")
			case masterdata.SubClassOtherOperatingExpense:
				tags = append(tags, "other_operating_expense")
			default:
				tags = append(tags, "other_operating_expense")
			}
		}
	}
	return uniqueTagStrings(tags)
}

func uniqueTagStrings(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" {
			continue
		}
		if _, exists := seen[tag]; exists {
			continue
		}
		seen[tag] = struct{}{}
		result = append(result, tag)
	}
	return result
}
