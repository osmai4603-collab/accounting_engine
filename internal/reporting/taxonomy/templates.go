package taxonomy

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	TemplateIFRSFunctionIncome = "IFRS_IS_FUNCTION_2026"
	TemplateIFRSNatureIncome   = "IFRS_IS_NATURE_2026"
	TemplateUSGAAPIncome       = "US_GAAP_IS_2026"
	TemplateSOCPAZakat         = "SOCPA_ZAKAT_STATUTORY_2026"
	TemplateIFRSBalanceSheet   = "IFRS_BS_2026"
)

func BuiltInTemplates() []FinancialReportDefinition {
	return []FinancialReportDefinition{
		ifrsFunctionIncome(),
		ifrsNatureIncome(),
		usGAAPIncome(),
		socpaZakat(),
		ifrsBalanceSheet(),
	}
}

func BuiltInTemplate(code string) (FinancialReportDefinition, error) {
	for _, template := range BuiltInTemplates() {
		if template.Code == code {
			return template, nil
		}
	}
	return FinancialReportDefinition{}, fmt.Errorf("taxonomy: unknown built-in template %q", code)
}

func ifrsFunctionIncome() FinancialReportDefinition {
	const code = TemplateIFRSFunctionIncome
	return definition(code, "IFRS income statement by function", FrameworkIFRS, StatementIncome, []*ReportNode{
		tagNode(code, "REVENUE", "الإيرادات", "Revenue", 10, "revenue"),
		tagNode(code, "INVESTING_REVENUE", "إيرادات الاستثمارات", "Investing income", 15, "investing_revenue"),
		tagNode(code, "FINANCING_REVENUE", "إيرادات التمويل", "Financing income", 16, "financing_revenue"),
		tagNode(code, "COST_OF_SALES", "تكلفة المبيعات", "Cost of sales", 20, "cost_of_sales"),
		formulaNode(code, "GROSS_PROFIT", "مجمل الربح", "Gross profit", 30, "REVENUE - COST_OF_SALES"),
		tagNode(code, "SELLING_EXPENSES", "مصاريف البيع والتوزيع", "Selling expenses", 40, "selling_expense"),
		tagNode(code, "ADMIN_EXPENSES", "المصاريف العمومية والإدارية", "Administrative expenses", 50, "administrative_expense"),
		tagNode(code, "DEPRECIATION_EXPENSES", "مصاريف الاستهلاك والمخصصات", "Depreciation and provision expenses", 52, "depreciation_expense"),
		tagNode(code, "OTHER_OPERATING_EXPENSES", "المصاريف التشغيلية الأخرى", "Other operating expenses", 55, "other_operating_expense"),
		formulaNode(code, "OPERATING_PROFIT", "الربح التشغيلي", "Operating profit", 60, "GROSS_PROFIT - SELLING_EXPENSES - ADMIN_EXPENSES - DEPRECIATION_EXPENSES - OTHER_OPERATING_EXPENSES"),
		tagNode(code, "INVESTING_EXPENSES", "مصاريف الاستثمارات", "Investing expenses", 65, "investing_expense"),
		formulaNode(code, "NET_INVESTING_INCOME", "صافي إيرادات الاستثمارات", "Net investing income", 66, "INVESTING_REVENUE - INVESTING_EXPENSES"),
		formulaNode(code, "PROFIT_BEFORE_FINANCING_AND_TAX", "الربح قبل التمويل والضريبة", "Profit before financing and tax", 67, "OPERATING_PROFIT + NET_INVESTING_INCOME"),
		tagNode(code, "FINANCE_COSTS", "تكاليف التمويل", "Finance costs", 70, "finance_cost"),
		formulaNode(code, "FINANCE_INCOME", "إيرادات التمويل", "Finance income", 75, "NET_INVESTING_INCOME + FINANCING_REVENUE"),
		tagNode(code, "INCOME_TAX_ZAKAT", "ضريبة الدخل والزكاة", "Income tax and zakat", 80, "income_tax", "zakat_expense"),
		tagNode(code, "DISCONTINUED_INCOME", "نتيجة العمليات المتوقفة", "Discontinued operations", 85, "discontinued_income"),
		tagNode(code, "DISCONTINUED_EXPENSES", "مصاريف العمليات المتوقفة", "Discontinued operations expenses", 86, "discontinued_expense"),
		formulaNode(code, "DISCONTINUED_OPERATIONS", "نتيجة العمليات المتوقفة", "Discontinued operations", 87, "DISCONTINUED_INCOME - DISCONTINUED_EXPENSES"),
		formulaNode(code, "PROFIT_BEFORE_TAX", "الربح قبل الضريبة", "Profit before tax", 88, "OPERATING_PROFIT + NET_INVESTING_INCOME + FINANCING_REVENUE - FINANCE_COSTS"),
		formulaNode(code, "NET_PROFIT", "صافي ربح الفترة", "Net profit for the period", 90, "PROFIT_BEFORE_TAX - INCOME_TAX_ZAKAT + DISCONTINUED_OPERATIONS"),
		tagNode(code, "OCI_INCOME", "الدخل الشامل الآخر", "Other comprehensive income", 100, "oci_income"),
		tagNode(code, "OCI_EXPENSES", "مصروفات الدخل الشامل الآخر", "Other comprehensive income expenses", 101, "oci_expense"),
		formulaNode(code, "OCI", "الدخل الشامل الآخر", "Other comprehensive income", 102, "OCI_INCOME - OCI_EXPENSES"),
		formulaNode(code, "TOTAL_COMPREHENSIVE_INCOME", "إجمالي الدخل الشامل", "Total comprehensive income", 110, "NET_PROFIT + OCI"),
	})
}

func ifrsNatureIncome() FinancialReportDefinition {
	const code = TemplateIFRSNatureIncome
	return definition(code, "IFRS income statement by nature", FrameworkIFRS, StatementIncome, []*ReportNode{
		tagNode(code, "REVENUE", "الإيرادات", "Revenue", 10, "revenue"),
		tagNode(code, "INVENTORY_CHANGE", "التغير في مخزون الإنتاج التام", "Changes in inventories", 20, "inventory_change"),
		formulaNode(code, "GROSS_INCOME", "الدخل الإجمالي", "Gross income", 30, "REVENUE + INVENTORY_CHANGE"),
		tagNode(code, "MATERIALS", "المواد الخام المستهلكة", "Raw materials consumed", 40, "materials_consumed"),
		tagNode(code, "EMPLOYEE_COSTS", "تكاليف منافع الموظفين", "Employee benefit expenses", 50, "employee_expense"),
		tagNode(code, "DEPRECIATION", "مصاريف الاستهلاك والإطفاء", "Depreciation and amortization", 60, "depreciation_expense"),
		tagNode(code, "OTHER_OPERATING_EXPENSES", "المصاريف التشغيلية الأخرى", "Other operating expenses", 70, "other_operating_expense"),
		formulaNode(code, "OPERATING_PROFIT", "الربح التشغيلي", "Operating profit", 80, "GROSS_INCOME - MATERIALS - EMPLOYEE_COSTS - DEPRECIATION - OTHER_OPERATING_EXPENSES"),
		tagNode(code, "FINANCE_COSTS", "تكاليف التمويل", "Finance costs", 90, "finance_cost"),
		tagNode(code, "INCOME_TAX_ZAKAT", "ضريبة الدخل والزكاة", "Income tax and zakat", 100, "income_tax", "zakat_expense"),
		formulaNode(code, "NET_PROFIT", "صافي ربح الفترة", "Net profit for the period", 110, "OPERATING_PROFIT - FINANCE_COSTS - INCOME_TAX_ZAKAT"),
		tagNode(code, "OCI", "الدخل الشامل الآخر", "Other comprehensive income", 120, "oci"),
		formulaNode(code, "TOTAL_COMPREHENSIVE_INCOME", "إجمالي الدخل الشامل", "Total comprehensive income", 130, "NET_PROFIT + OCI"),
	})
}

func usGAAPIncome() FinancialReportDefinition {
	const code = TemplateUSGAAPIncome
	return definition(code, "US GAAP income statement", FrameworkUSGAAP, StatementIncome, []*ReportNode{
		tagNode(code, "NET_SALES", "صافي المبيعات", "Net sales", 10, "revenue"),
		tagNode(code, "COST_OF_REVENUE", "تكلفة الإيرادات", "Cost of revenue", 20, "cost_of_sales"),
		formulaNode(code, "GROSS_PROFIT", "مجمل الربح", "Gross profit", 30, "NET_SALES - COST_OF_REVENUE"),
		tagNode(code, "OPERATING_EXPENSES", "المصاريف التشغيلية", "Operating expenses", 40, "selling_expense", "administrative_expense", "depreciation_expense", "other_operating_expense"),
		formulaNode(code, "OPERATING_INCOME", "الدخل التشغيلي", "Operating income", 50, "GROSS_PROFIT - OPERATING_EXPENSES"),
		tagNode(code, "NONOPERATING_INCOME", "الإيرادات غير التشغيلية", "Non-operating income", 60, "nonoperating_income"),
		tagNode(code, "INTEREST_EXPENSE", "مصروف الفوائد", "Interest expense", 70, "finance_cost"),
		tagNode(code, "INCOME_TAX", "مصروف ضريبة الدخل", "Income tax expense", 80, "income_tax"),
		formulaNode(code, "NET_INCOME", "صافي الدخل", "Net income", 90, "OPERATING_INCOME + NONOPERATING_INCOME - INTEREST_EXPENSE - INCOME_TAX"),
		tagNode(code, "OCI", "الدخل الشامل الآخر", "Other comprehensive income", 100, "oci"),
		formulaNode(code, "COMPREHENSIVE_INCOME", "إجمالي الدخل الشامل", "Comprehensive income", 110, "NET_INCOME + OCI"),
	})
}

func socpaZakat() FinancialReportDefinition {
	const code = TemplateSOCPAZakat
	return definition(code, "SOCPA / ZATCA zakat base statement", FrameworkSOCPA, StatementBalanceSheet, []*ReportNode{
		headerNode(code, "ZAKAT_BASE_HEADER", "مكونات الوعاء الزكوي", "Zakat base components", 10),
		tagNode(code, "ZAKAT_ADDITIONS", "الإضافات إلى الوعاء", "Zakat base additions", 20, "zakat_addition"),
		tagNode(code, "ZAKAT_DEDUCTIONS", "الحسميات من الوعاء", "Zakat base deductions", 30, "zakat_deduction"),
		formulaNode(code, "ZAKAT_BASE", "وعاء الزكاة", "Zakat base", 40, "ZAKAT_ADDITIONS - ZAKAT_DEDUCTIONS"),
		formulaNode(code, "ZAKAT_DUE", "الزكاة المستحقة", "Zakat due", 50, "ZAKAT_BASE * 0.025"),
	})
}

func ifrsBalanceSheet() FinancialReportDefinition {
	const code = TemplateIFRSBalanceSheet
	return definition(code, "IFRS statement of financial position", FrameworkIFRS, StatementBalanceSheet, []*ReportNode{
		headerNode(code, "ASSETS", "الموجودات", "Assets", 10),
		tagNode(code, "CURRENT_ASSETS", "الموجودات المتداولة", "Current assets", 20, "current_asset"),
		tagNode(code, "NONCURRENT_ASSETS", "الموجودات غير المتداولة", "Non-current assets", 30, "noncurrent_asset"),
		formulaNode(code, "TOTAL_ASSETS", "إجمالي الموجودات", "Total assets", 40, "CURRENT_ASSETS + NONCURRENT_ASSETS"),
		headerNode(code, "LIABILITIES_AND_EQUITY", "المطلوبات وحقوق الملكية", "Liabilities and equity", 50),
		tagNode(code, "CURRENT_LIABILITIES", "المطلوبات المتداولة", "Current liabilities", 60, "current_liability"),
		tagNode(code, "NONCURRENT_LIABILITIES", "المطلوبات غير المتداولة", "Non-current liabilities", 70, "noncurrent_liability"),
		formulaNode(code, "TOTAL_LIABILITIES", "إجمالي المطلوبات", "Total liabilities", 75, "CURRENT_LIABILITIES + NONCURRENT_LIABILITIES"),
		tagNode(code, "EQUITY", "حقوق الملكية", "Equity", 80, "equity"),
		formulaNode(code, "TOTAL_LIABILITIES_AND_EQUITY", "إجمالي المطلوبات وحقوق الملكية", "Total liabilities and equity", 90, "TOTAL_LIABILITIES + EQUITY"),
	})
}

func definition(code, name string, framework ReportingFramework, statementType StatementType, nodes []*ReportNode) FinancialReportDefinition {
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("github.com/osmai4603-collab/accounting_engine/reporting/taxonomy/"+code))
	for _, node := range nodes {
		node.DefinitionID = id
		node.ID = uuid.NewSHA1(id, []byte(node.Code))
	}
	return FinancialReportDefinition{
		ID:        id,
		Code:      code,
		Name:      name,
		Framework: framework,
		Type:      statementType,
		RootNodes: nodes,
		Version:   1,
		CreatedAt: time.Time{},
	}
}

func headerNode(definitionCode, code, titleAr, titleEn string, order int) *ReportNode {
	return node(definitionCode, code, titleAr, titleEn, NodeHeader, order)
}

func formulaNode(definitionCode, code, titleAr, titleEn string, order int, expression string) *ReportNode {
	result := node(definitionCode, code, titleAr, titleEn, NodeFormula, order)
	result.FormulaExpr = expression
	return result
}

func tagNode(definitionCode, code, titleAr, titleEn string, order int, tags ...string) *ReportNode {
	result := node(definitionCode, code, titleAr, titleEn, NodeAccountTag, order)
	result.AccountTags = tags
	return result
}

func node(definitionCode, code, titleAr, titleEn string, nodeType NodeType, order int) *ReportNode {
	definitionID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("github.com/osmai4603-collab/accounting_engine/reporting/taxonomy/"+definitionCode))
	return &ReportNode{
		ID:             uuid.NewSHA1(definitionID, []byte(code)),
		DefinitionID:   definitionID,
		Code:           strings.ToUpper(code),
		TitleAr:        titleAr,
		TitleEn:        titleEn,
		NodeType:       nodeType,
		DisplayOrder:   order,
		SignMultiplier: 1,
	}
}
