CREATE TABLE IF NOT EXISTS reporting_taxonomies (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id uuid REFERENCES companies(id) ON DELETE CASCADE,
    code varchar(64) NOT NULL UNIQUE,
    name varchar(255) NOT NULL,
    framework varchar(32) NOT NULL CHECK (framework IN ('IFRS', 'US_GAAP', 'SOCPA')),
    statement_type varchar(32) NOT NULL CHECK (statement_type IN (
        'BALANCE_SHEET', 'INCOME_STATEMENT', 'CASH_FLOW', 'EQUITY_STATEMENT'
    )),
    version int NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS reporting_taxonomy_nodes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    taxonomy_id uuid NOT NULL REFERENCES reporting_taxonomies(id) ON DELETE CASCADE,
    parent_id uuid,
    code varchar(64) NOT NULL,
    title_ar varchar(255) NOT NULL,
    title_en varchar(255) NOT NULL,
    node_type varchar(32) NOT NULL CHECK (node_type IN (
        'HEADER', 'ACCOUNT_RANGE', 'ACCOUNT_TAG', 'FORMULA', 'SUBTOTAL'
    )),
    display_order int NOT NULL DEFAULT 100,
    sign_multiplier smallint NOT NULL DEFAULT 1 CHECK (sign_multiplier IN (1, -1)),
    account_from varchar(32),
    account_to varchar(32),
    account_tags text[],
    formula_expr text,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    UNIQUE (taxonomy_id, code),
    UNIQUE (taxonomy_id, id),
    FOREIGN KEY (taxonomy_id, parent_id)
        REFERENCES reporting_taxonomy_nodes(taxonomy_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_taxonomy_nodes_lookup
    ON reporting_taxonomy_nodes(taxonomy_id, parent_id, display_order);

CREATE TABLE IF NOT EXISTS reporting_account_tags (
    company_id uuid NOT NULL,
    account_id uuid NOT NULL,
    tag varchar(64) NOT NULL,
    PRIMARY KEY (company_id, account_id, tag),
    FOREIGN KEY (company_id, account_id) REFERENCES accounts(company_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_reporting_account_tags_match
    ON reporting_account_tags(company_id, tag, account_id);

INSERT INTO reporting_taxonomies (code, name, framework, statement_type) VALUES
    ('IFRS_IS_FUNCTION_2026', 'IFRS income statement by function', 'IFRS', 'INCOME_STATEMENT'),
    ('IFRS_IS_NATURE_2026', 'IFRS income statement by nature', 'IFRS', 'INCOME_STATEMENT'),
    ('US_GAAP_IS_2026', 'US GAAP income statement', 'US_GAAP', 'INCOME_STATEMENT'),
    ('SOCPA_ZAKAT_STATUTORY_2026', 'SOCPA / ZATCA zakat base statement', 'SOCPA', 'BALANCE_SHEET'),
    ('IFRS_BS_2026', 'IFRS statement of financial position', 'IFRS', 'BALANCE_SHEET')
ON CONFLICT (code) DO NOTHING;

INSERT INTO reporting_taxonomy_nodes (taxonomy_id, code, title_ar, title_en, node_type, display_order, account_tags, formula_expr)
SELECT taxonomy.id, node.code, node.title_ar, node.title_en, node.node_type, node.display_order, node.account_tags, node.formula_expr
FROM reporting_taxonomies taxonomy
JOIN (VALUES
    ('IFRS_IS_FUNCTION_2026', 'REVENUE', 'الإيرادات', 'Revenue', 'ACCOUNT_TAG', 10, ARRAY['revenue']::text[], NULL::text),
    ('IFRS_IS_FUNCTION_2026', 'INVESTING_REVENUE', 'إيرادات الاستثمارات', 'Investing income', 'ACCOUNT_TAG', 15, ARRAY['investing_revenue']::text[], NULL::text),
    ('IFRS_IS_FUNCTION_2026', 'FINANCING_REVENUE', 'إيرادات التمويل', 'Financing income', 'ACCOUNT_TAG', 16, ARRAY['financing_revenue']::text[], NULL::text),
    ('IFRS_IS_FUNCTION_2026', 'COST_OF_SALES', 'تكلفة المبيعات', 'Cost of sales', 'ACCOUNT_TAG', 20, ARRAY['cost_of_sales']::text[], NULL::text),
    ('IFRS_IS_FUNCTION_2026', 'GROSS_PROFIT', 'مجمل الربح', 'Gross profit', 'FORMULA', 30, NULL::text[], 'REVENUE - COST_OF_SALES'),
    ('IFRS_IS_FUNCTION_2026', 'SELLING_EXPENSES', 'مصاريف البيع والتوزيع', 'Selling expenses', 'ACCOUNT_TAG', 40, ARRAY['selling_expense']::text[], NULL::text),
    ('IFRS_IS_FUNCTION_2026', 'ADMIN_EXPENSES', 'المصاريف العمومية والإدارية', 'Administrative expenses', 'ACCOUNT_TAG', 50, ARRAY['administrative_expense']::text[], NULL::text),
    ('IFRS_IS_FUNCTION_2026', 'DEPRECIATION_EXPENSES', 'مصاريف الاستهلاك والمخصصات', 'Depreciation and provision expenses', 'ACCOUNT_TAG', 52, ARRAY['depreciation_expense']::text[], NULL::text),
    ('IFRS_IS_FUNCTION_2026', 'OTHER_OPERATING_EXPENSES', 'المصاريف التشغيلية الأخرى', 'Other operating expenses', 'ACCOUNT_TAG', 55, ARRAY['other_operating_expense']::text[], NULL::text),
    ('IFRS_IS_FUNCTION_2026', 'OPERATING_PROFIT', 'الربح التشغيلي', 'Operating profit', 'FORMULA', 60, NULL::text[], 'GROSS_PROFIT - SELLING_EXPENSES - ADMIN_EXPENSES - DEPRECIATION_EXPENSES - OTHER_OPERATING_EXPENSES'),
    ('IFRS_IS_FUNCTION_2026', 'INVESTING_EXPENSES', 'مصاريف الاستثمارات', 'Investing expenses', 'ACCOUNT_TAG', 65, ARRAY['investing_expense']::text[], NULL::text),
    ('IFRS_IS_FUNCTION_2026', 'NET_INVESTING_INCOME', 'صافي إيرادات الاستثمارات', 'Net investing income', 'FORMULA', 66, NULL::text[], 'INVESTING_REVENUE - INVESTING_EXPENSES'),
    ('IFRS_IS_FUNCTION_2026', 'PROFIT_BEFORE_FINANCING_AND_TAX', 'الربح قبل التمويل والضريبة', 'Profit before financing and tax', 'FORMULA', 67, NULL::text[], 'OPERATING_PROFIT + NET_INVESTING_INCOME'),
    ('IFRS_IS_FUNCTION_2026', 'FINANCE_COSTS', 'تكاليف التمويل', 'Finance costs', 'ACCOUNT_TAG', 70, ARRAY['finance_cost']::text[], NULL::text),
    ('IFRS_IS_FUNCTION_2026', 'FINANCE_INCOME', 'إيرادات التمويل', 'Finance income', 'FORMULA', 75, NULL::text[], 'NET_INVESTING_INCOME + FINANCING_REVENUE'),
    ('IFRS_IS_FUNCTION_2026', 'INCOME_TAX_ZAKAT', 'ضريبة الدخل والزكاة', 'Income tax and zakat', 'ACCOUNT_TAG', 80, ARRAY['income_tax','zakat_expense']::text[], NULL::text),
    ('IFRS_IS_FUNCTION_2026', 'DISCONTINUED_INCOME', 'نتيجة العمليات المتوقفة', 'Discontinued operations', 'ACCOUNT_TAG', 85, ARRAY['discontinued_income']::text[], NULL::text),
    ('IFRS_IS_FUNCTION_2026', 'DISCONTINUED_EXPENSES', 'مصاريف العمليات المتوقفة', 'Discontinued operations expenses', 'ACCOUNT_TAG', 86, ARRAY['discontinued_expense']::text[], NULL::text),
    ('IFRS_IS_FUNCTION_2026', 'DISCONTINUED_OPERATIONS', 'نتيجة العمليات المتوقفة', 'Discontinued operations', 'FORMULA', 87, NULL::text[], 'DISCONTINUED_INCOME - DISCONTINUED_EXPENSES'),
    ('IFRS_IS_FUNCTION_2026', 'PROFIT_BEFORE_TAX', 'الربح قبل الضريبة', 'Profit before tax', 'FORMULA', 88, NULL::text[], 'OPERATING_PROFIT + NET_INVESTING_INCOME + FINANCING_REVENUE - FINANCE_COSTS'),
    ('IFRS_IS_FUNCTION_2026', 'NET_PROFIT', 'صافي ربح الفترة', 'Net profit for the period', 'FORMULA', 90, NULL::text[], 'PROFIT_BEFORE_TAX - INCOME_TAX_ZAKAT + DISCONTINUED_OPERATIONS'),
    ('IFRS_IS_FUNCTION_2026', 'OCI_INCOME', 'الدخل الشامل الآخر', 'Other comprehensive income', 'ACCOUNT_TAG', 100, ARRAY['oci_income']::text[], NULL::text),
    ('IFRS_IS_FUNCTION_2026', 'OCI_EXPENSES', 'مصروفات الدخل الشامل الآخر', 'Other comprehensive income expenses', 'ACCOUNT_TAG', 101, ARRAY['oci_expense']::text[], NULL::text),
    ('IFRS_IS_FUNCTION_2026', 'OCI', 'الدخل الشامل الآخر', 'Other comprehensive income', 'FORMULA', 102, NULL::text[], 'OCI_INCOME - OCI_EXPENSES'),
    ('IFRS_IS_FUNCTION_2026', 'TOTAL_COMPREHENSIVE_INCOME', 'إجمالي الدخل الشامل', 'Total comprehensive income', 'FORMULA', 110, NULL::text[], 'NET_PROFIT + OCI'),
    ('IFRS_IS_NATURE_2026', 'REVENUE', 'الإيرادات', 'Revenue', 'ACCOUNT_TAG', 10, ARRAY['revenue']::text[], NULL::text),
    ('IFRS_IS_NATURE_2026', 'INVENTORY_CHANGE', 'التغير في مخزون الإنتاج التام', 'Changes in inventories', 'ACCOUNT_TAG', 20, ARRAY['inventory_change']::text[], NULL::text),
    ('IFRS_IS_NATURE_2026', 'GROSS_INCOME', 'الدخل الإجمالي', 'Gross income', 'FORMULA', 30, NULL::text[], 'REVENUE + INVENTORY_CHANGE'),
    ('IFRS_IS_NATURE_2026', 'MATERIALS', 'المواد الخام المستهلكة', 'Raw materials consumed', 'ACCOUNT_TAG', 40, ARRAY['materials_consumed']::text[], NULL::text),
    ('IFRS_IS_NATURE_2026', 'EMPLOYEE_COSTS', 'تكاليف منافع الموظفين', 'Employee benefit expenses', 'ACCOUNT_TAG', 50, ARRAY['employee_expense']::text[], NULL::text),
    ('IFRS_IS_NATURE_2026', 'DEPRECIATION', 'مصاريف الاستهلاك والإطفاء', 'Depreciation and amortization', 'ACCOUNT_TAG', 60, ARRAY['depreciation_expense']::text[], NULL::text),
    ('IFRS_IS_NATURE_2026', 'OTHER_OPERATING_EXPENSES', 'المصاريف التشغيلية الأخرى', 'Other operating expenses', 'ACCOUNT_TAG', 70, ARRAY['other_operating_expense']::text[], NULL::text),
    ('IFRS_IS_NATURE_2026', 'OPERATING_PROFIT', 'الربح التشغيلي', 'Operating profit', 'FORMULA', 80, NULL::text[], 'GROSS_INCOME - MATERIALS - EMPLOYEE_COSTS - DEPRECIATION - OTHER_OPERATING_EXPENSES'),
    ('IFRS_IS_NATURE_2026', 'FINANCE_COSTS', 'تكاليف التمويل', 'Finance costs', 'ACCOUNT_TAG', 90, ARRAY['finance_cost']::text[], NULL::text),
    ('IFRS_IS_NATURE_2026', 'INCOME_TAX_ZAKAT', 'ضريبة الدخل والزكاة', 'Income tax and zakat', 'ACCOUNT_TAG', 100, ARRAY['income_tax','zakat_expense']::text[], NULL::text),
    ('IFRS_IS_NATURE_2026', 'NET_PROFIT', 'صافي ربح الفترة', 'Net profit for the period', 'FORMULA', 110, NULL::text[], 'OPERATING_PROFIT - FINANCE_COSTS - INCOME_TAX_ZAKAT'),
    ('IFRS_IS_NATURE_2026', 'OCI', 'الدخل الشامل الآخر', 'Other comprehensive income', 'ACCOUNT_TAG', 120, ARRAY['oci']::text[], NULL::text),
    ('IFRS_IS_NATURE_2026', 'TOTAL_COMPREHENSIVE_INCOME', 'إجمالي الدخل الشامل', 'Total comprehensive income', 'FORMULA', 130, NULL::text[], 'NET_PROFIT + OCI'),
    ('US_GAAP_IS_2026', 'NET_SALES', 'صافي المبيعات', 'Net sales', 'ACCOUNT_TAG', 10, ARRAY['revenue']::text[], NULL::text),
    ('US_GAAP_IS_2026', 'COST_OF_REVENUE', 'تكلفة الإيرادات', 'Cost of revenue', 'ACCOUNT_TAG', 20, ARRAY['cost_of_sales']::text[], NULL::text),
    ('US_GAAP_IS_2026', 'GROSS_PROFIT', 'مجمل الربح', 'Gross profit', 'FORMULA', 30, NULL::text[], 'NET_SALES - COST_OF_REVENUE'),
    ('US_GAAP_IS_2026', 'OPERATING_EXPENSES', 'المصاريف التشغيلية', 'Operating expenses', 'ACCOUNT_TAG', 40, ARRAY['selling_expense','administrative_expense','depreciation_expense','other_operating_expense']::text[], NULL::text),
    ('US_GAAP_IS_2026', 'OPERATING_INCOME', 'الدخل التشغيلي', 'Operating income', 'FORMULA', 50, NULL::text[], 'GROSS_PROFIT - OPERATING_EXPENSES'),
    ('US_GAAP_IS_2026', 'NONOPERATING_INCOME', 'الإيرادات غير التشغيلية', 'Non-operating income', 'ACCOUNT_TAG', 60, ARRAY['nonoperating_income']::text[], NULL::text),
    ('US_GAAP_IS_2026', 'INTEREST_EXPENSE', 'مصروف الفوائد', 'Interest expense', 'ACCOUNT_TAG', 70, ARRAY['finance_cost']::text[], NULL::text),
    ('US_GAAP_IS_2026', 'INCOME_TAX', 'مصروف ضريبة الدخل', 'Income tax expense', 'ACCOUNT_TAG', 80, ARRAY['income_tax']::text[], NULL::text),
    ('US_GAAP_IS_2026', 'NET_INCOME', 'صافي الدخل', 'Net income', 'FORMULA', 90, NULL::text[], 'OPERATING_INCOME + NONOPERATING_INCOME - INTEREST_EXPENSE - INCOME_TAX'),
    ('US_GAAP_IS_2026', 'OCI', 'الدخل الشامل الآخر', 'Other comprehensive income', 'ACCOUNT_TAG', 100, ARRAY['oci']::text[], NULL::text),
    ('US_GAAP_IS_2026', 'COMPREHENSIVE_INCOME', 'إجمالي الدخل الشامل', 'Comprehensive income', 'FORMULA', 110, NULL::text[], 'NET_INCOME + OCI'),
    ('SOCPA_ZAKAT_STATUTORY_2026', 'ZAKAT_BASE_HEADER', 'مكونات الوعاء الزكوي', 'Zakat base components', 'HEADER', 10, NULL::text[], NULL::text),
    ('SOCPA_ZAKAT_STATUTORY_2026', 'ZAKAT_ADDITIONS', 'الإضافات إلى الوعاء', 'Zakat base additions', 'ACCOUNT_TAG', 20, ARRAY['zakat_addition']::text[], NULL::text),
    ('SOCPA_ZAKAT_STATUTORY_2026', 'ZAKAT_DEDUCTIONS', 'الحسميات من الوعاء', 'Zakat base deductions', 'ACCOUNT_TAG', 30, ARRAY['zakat_deduction']::text[], NULL::text),
    ('SOCPA_ZAKAT_STATUTORY_2026', 'ZAKAT_BASE', 'وعاء الزكاة', 'Zakat base', 'FORMULA', 40, NULL::text[], 'ZAKAT_ADDITIONS - ZAKAT_DEDUCTIONS'),
    ('SOCPA_ZAKAT_STATUTORY_2026', 'ZAKAT_DUE', 'الزكاة المستحقة', 'Zakat due', 'FORMULA', 50, NULL::text[], 'ZAKAT_BASE * 0.025'),
    ('IFRS_BS_2026', 'ASSETS', 'الموجودات', 'Assets', 'HEADER', 10, NULL::text[], NULL::text),
    ('IFRS_BS_2026', 'CURRENT_ASSETS', 'الموجودات المتداولة', 'Current assets', 'ACCOUNT_TAG', 20, ARRAY['current_asset']::text[], NULL::text),
    ('IFRS_BS_2026', 'NONCURRENT_ASSETS', 'الموجودات غير المتداولة', 'Non-current assets', 'ACCOUNT_TAG', 30, ARRAY['noncurrent_asset']::text[], NULL::text),
    ('IFRS_BS_2026', 'TOTAL_ASSETS', 'إجمالي الموجودات', 'Total assets', 'FORMULA', 40, NULL::text[], 'CURRENT_ASSETS + NONCURRENT_ASSETS'),
    ('IFRS_BS_2026', 'LIABILITIES_AND_EQUITY', 'المطلوبات وحقوق الملكية', 'Liabilities and equity', 'HEADER', 50, NULL::text[], NULL::text),
    ('IFRS_BS_2026', 'CURRENT_LIABILITIES', 'المطلوبات المتداولة', 'Current liabilities', 'ACCOUNT_TAG', 60, ARRAY['current_liability']::text[], NULL::text),
    ('IFRS_BS_2026', 'NONCURRENT_LIABILITIES', 'المطلوبات غير المتداولة', 'Non-current liabilities', 'ACCOUNT_TAG', 70, ARRAY['noncurrent_liability']::text[], NULL::text),
    ('IFRS_BS_2026', 'TOTAL_LIABILITIES', 'إجمالي المطلوبات', 'Total liabilities', 'FORMULA', 75, NULL::text[], 'CURRENT_LIABILITIES + NONCURRENT_LIABILITIES'),
    ('IFRS_BS_2026', 'EQUITY', 'حقوق الملكية', 'Equity', 'ACCOUNT_TAG', 80, ARRAY['equity']::text[], NULL::text),
    ('IFRS_BS_2026', 'TOTAL_LIABILITIES_AND_EQUITY', 'إجمالي المطلوبات وحقوق الملكية', 'Total liabilities and equity', 'FORMULA', 90, NULL::text[], 'TOTAL_LIABILITIES + EQUITY')
) AS node(definition_code, code, title_ar, title_en, node_type, display_order, account_tags, formula_expr)
    ON taxonomy.code = node.definition_code
ON CONFLICT (taxonomy_id, code) DO NOTHING;

ALTER TABLE reporting_taxonomies ENABLE ROW LEVEL SECURITY;
ALTER TABLE reporting_taxonomies FORCE ROW LEVEL SECURITY;
CREATE POLICY reporting_taxonomies_tenant_policy ON reporting_taxonomies
    USING (company_id IS NULL OR company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());

ALTER TABLE reporting_taxonomy_nodes ENABLE ROW LEVEL SECURITY;
ALTER TABLE reporting_taxonomy_nodes FORCE ROW LEVEL SECURITY;
CREATE POLICY reporting_taxonomy_nodes_tenant_policy ON reporting_taxonomy_nodes
    USING (EXISTS (
        SELECT 1 FROM reporting_taxonomies taxonomy
        WHERE taxonomy.id = taxonomy_id
    ))
    WITH CHECK (EXISTS (
        SELECT 1 FROM reporting_taxonomies taxonomy
        WHERE taxonomy.id = taxonomy_id AND taxonomy.company_id = app_current_company_id()
    ));

ALTER TABLE reporting_account_tags ENABLE ROW LEVEL SECURITY;
ALTER TABLE reporting_account_tags FORCE ROW LEVEL SECURITY;
CREATE POLICY reporting_account_tags_tenant_policy ON reporting_account_tags
    USING (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());
