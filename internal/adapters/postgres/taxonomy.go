package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/reporting/taxonomy"
	"github.com/shopspring/decimal"
)

var (
	_ taxonomy.BalanceReader = (*Driver)(nil)

	ErrTaxonomyAccountNotFound = errors.New("taxonomy: account not found")
)

func (d *Driver) GetPeriodBalances(
	ctx context.Context,
	companyID, periodID uuid.UUID,
	currency string,
) (result taxonomy.PeriodBalances, resultErr error) {
	if companyID == uuid.Nil || periodID == uuid.Nil || strings.TrimSpace(currency) == "" {
		return taxonomy.PeriodBalances{}, errors.New("taxonomy: company, period, and currency are required")
	}
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var fiscalYearID uuid.UUID
		var periodNumber int
		if err := tx.QueryRow(ctx, `SELECT year_id, COALESCE(period_number,0), end_date FROM fiscal_periods
			WHERE company_id=$1 AND id=$2`, companyID, periodID).
			Scan(&fiscalYearID, &periodNumber, &result.AsOfDate); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `WITH balance_totals AS (
				SELECT balance.account_id,
					SUM(balance.beginning_debit) AS beginning_debit,
					SUM(balance.beginning_credit) AS beginning_credit,
					COALESCE(SUM(balance.period_debit) FILTER (WHERE period.id=$2), 0) AS period_debit,
					COALESCE(SUM(balance.period_credit) FILTER (WHERE period.id=$2), 0) AS period_credit,
					SUM(balance.period_debit) AS ending_period_debit,
					SUM(balance.period_credit) AS ending_period_credit
				FROM account_balances balance
				JOIN fiscal_periods period
				  ON period.company_id=balance.company_id AND period.id=balance.fiscal_period_id
				WHERE balance.company_id=$1 AND period.year_id=$4
				  AND (period.end_date < $5 OR
					(period.end_date = $5 AND COALESCE(period.period_number,0) <= $6))
				  AND balance.currency_id=$3
				GROUP BY balance.account_id
			), account_tags AS (
				SELECT company_id, account_id, array_agg(DISTINCT tag) AS tags
				FROM reporting_account_tags
				WHERE company_id=$1
				GROUP BY company_id, account_id
			)
			SELECT account.id, account.code, account.account_type, account.normal_balance,
				account.sub_classification, account.ifrs_category,
				COALESCE(account_tags.tags, '{}'::text[]),
				balance_totals.beginning_debit, balance_totals.beginning_credit,
				balance_totals.period_debit, balance_totals.period_credit,
				balance_totals.ending_period_debit, balance_totals.ending_period_credit
			FROM balance_totals
			JOIN accounts account
			  ON account.company_id=$1 AND account.id=balance_totals.account_id
			LEFT JOIN account_tags
			  ON account_tags.company_id=account.company_id AND account_tags.account_id=account.id
			ORDER BY account.code`, companyID, periodID, currency, fiscalYearID, result.AsOfDate, periodNumber)
		if err != nil {
			return err
		}
		defer rows.Close()
		result.Accounts = make([]taxonomy.AccountBalance, 0)
		for rows.Next() {
			var accountID uuid.UUID
			var code, accountType, normalBalance, subClassification, ifrsCategory string
			var explicitTags []string
			var beginningDebit, beginningCredit, periodDebit, periodCredit, endingPeriodDebit, endingPeriodCredit decimal.Decimal
			if err := rows.Scan(
				&accountID, &code, &accountType, &normalBalance, &subClassification, &ifrsCategory,
				&explicitTags, &beginningDebit, &beginningCredit, &periodDebit, &periodCredit,
				&endingPeriodDebit, &endingPeriodCredit,
			); err != nil {
				return err
			}
			tags := append(explicitTags, taxonomy.TagsForAccount(masterdata.Account{
				AccountType:       masterdata.AccountType(accountType),
				SubClassification: subClassification,
				IFRSCategory:      ifrsCategory,
			})...)
			periodAmount := periodDebit.Sub(periodCredit)
			endingAmount := beginningDebit.Add(endingPeriodDebit).Sub(beginningCredit).Sub(endingPeriodCredit)
			if normalBalance == string(masterdata.NormalBalanceCredit) {
				periodAmount = periodAmount.Neg()
				endingAmount = endingAmount.Neg()
			}
			result.Accounts = append(result.Accounts, taxonomy.AccountBalance{
				AccountID:    accountID,
				Code:         code,
				Tags:         uniqueStrings(tags),
				Amount:       periodAmount,
				PeriodAmount: periodAmount,
				EndingAmount: endingAmount,
			})
		}
		return rows.Err()
	})
	if err != nil {
		return taxonomy.PeriodBalances{}, fmt.Errorf("read taxonomy period balances: %w", err)
	}
	return result, nil
}

func (d *Driver) GetReportingTaxonomy(
	ctx context.Context,
	companyID uuid.UUID,
	code string,
) (result taxonomy.FinancialReportDefinition, resultErr error) {
	if companyID == uuid.Nil {
		return result, errors.New("taxonomy: company id is required")
	}
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		return loadReportingTaxonomy(ctx, tx, companyID, code, &result)
	})
	if err != nil {
		return taxonomy.FinancialReportDefinition{}, fmt.Errorf("load reporting taxonomy %q: %w", code, err)
	}
	return result, nil
}

func (d *Driver) ListReportingTaxonomies(
	ctx context.Context,
	companyID uuid.UUID,
) (definitions []taxonomy.FinancialReportDefinition, resultErr error) {
	if companyID == uuid.Nil {
		return nil, errors.New("taxonomy: company id is required")
	}
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, company_id, code, name, framework, statement_type, version, created_at
			FROM reporting_taxonomies ORDER BY company_id NULLS FIRST, code`)
		if err != nil {
			return err
		}
		baseDefinitions := make([]taxonomy.FinancialReportDefinition, 0)
		for rows.Next() {
			definition, err := scanTaxonomyDefinition(rows)
			if err != nil {
				rows.Close()
				return err
			}
			baseDefinitions = append(baseDefinitions, definition)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		definitions = make([]taxonomy.FinancialReportDefinition, 0, len(baseDefinitions))
		for _, definition := range baseDefinitions {
			nodes, err := loadTaxonomyNodes(ctx, tx, definition.ID)
			if err != nil {
				return err
			}
			definition.RootNodes = nodes
			definitions = append(definitions, definition)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list reporting taxonomies: %w", err)
	}
	return definitions, nil
}

func (d *Driver) SaveReportingTaxonomy(
	ctx context.Context,
	companyID uuid.UUID,
	definition taxonomy.FinancialReportDefinition,
) (resultErr error) {
	if companyID == uuid.Nil {
		return errors.New("taxonomy: company id is required")
	}
	if err := taxonomy.ValidateDefinition(definition); err != nil {
		return err
	}
	if definition.CompanyID != nil && *definition.CompanyID != companyID {
		return errors.New("taxonomy: definition belongs to a different company")
	}
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		id := definition.ID
		if id == uuid.Nil {
			id = uuid.New()
		}
		var savedID uuid.UUID
		err := tx.QueryRow(ctx, `INSERT INTO reporting_taxonomies
			(id, company_id, code, name, framework, statement_type, version)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (code) DO UPDATE SET
				name=EXCLUDED.name, framework=EXCLUDED.framework,
				statement_type=EXCLUDED.statement_type, version=EXCLUDED.version
			WHERE reporting_taxonomies.company_id=EXCLUDED.company_id
			RETURNING id`, id, companyID, definition.Code, definition.Name,
			definition.Framework, definition.Type, positiveVersion(definition.Version)).Scan(&savedID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("taxonomy: code %q already belongs to another company or a global template", definition.Code)
			}
			return err
		}
		if _, err := tx.Exec(ctx, "DELETE FROM reporting_taxonomy_nodes WHERE taxonomy_id=$1", savedID); err != nil {
			return err
		}
		for _, root := range sortedTaxonomyNodes(definition.RootNodes) {
			if err := insertTaxonomyNode(ctx, tx, savedID, root, nil); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("save reporting taxonomy %q: %w", definition.Code, err)
	}
	return nil
}

func (d *Driver) SetAccountReportingTags(
	ctx context.Context,
	companyID, accountID uuid.UUID,
	tags []string,
) error {
	if companyID == uuid.Nil || accountID == uuid.Nil {
		return errors.New("taxonomy: company and account ids are required")
	}
	for _, tag := range tags {
		trimmed := strings.TrimSpace(tag)
		if trimmed == "" || len(trimmed) > 64 {
			return fmt.Errorf("taxonomy: reporting tag %q must contain 1 to 64 characters", tag)
		}
	}
	normalized := uniqueStrings(tags)
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var accountExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM accounts WHERE company_id=$1 AND id=$2
		)`, companyID, accountID).Scan(&accountExists); err != nil {
			return err
		}
		if !accountExists {
			return ErrTaxonomyAccountNotFound
		}
		if _, err := tx.Exec(ctx, `DELETE FROM reporting_account_tags
			WHERE company_id=$1 AND account_id=$2`, companyID, accountID); err != nil {
			return err
		}
		for _, tag := range normalized {
			if _, err := tx.Exec(ctx, `INSERT INTO reporting_account_tags(company_id,account_id,tag)
				VALUES($1,$2,$3)`, companyID, accountID, tag); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("set reporting tags for account %s: %w", accountID, err)
	}
	return nil
}

type taxonomyRowScanner interface {
	Scan(...any) error
}

func scanTaxonomyDefinition(row taxonomyRowScanner) (taxonomy.FinancialReportDefinition, error) {
	var definition taxonomy.FinancialReportDefinition
	var companyID *uuid.UUID
	if err := row.Scan(&definition.ID, &companyID, &definition.Code, &definition.Name,
		&definition.Framework, &definition.Type, &definition.Version, &definition.CreatedAt); err != nil {
		return taxonomy.FinancialReportDefinition{}, err
	}
	definition.CompanyID = companyID
	return definition, nil
}

func loadReportingTaxonomy(ctx context.Context, tx pgx.Tx, companyID uuid.UUID, code string, out *taxonomy.FinancialReportDefinition) error {
	row := tx.QueryRow(ctx, `SELECT id, company_id, code, name, framework, statement_type, version, created_at
		FROM reporting_taxonomies WHERE code=$1
		  AND (company_id IS NULL OR company_id=$2)`, code, companyID)
	definition, err := scanTaxonomyDefinition(row)
	if err != nil {
		return err
	}
	definition.RootNodes, err = loadTaxonomyNodes(ctx, tx, definition.ID)
	if err != nil {
		return err
	}
	*out = definition
	return nil
}

func loadTaxonomyNodes(ctx context.Context, tx pgx.Tx, definitionID uuid.UUID) ([]*taxonomy.ReportNode, error) {
	rows, err := tx.Query(ctx, `SELECT id, parent_id, code, title_ar, title_en, node_type,
		display_order, sign_multiplier, account_from, account_to, account_tags, formula_expr
		FROM reporting_taxonomy_nodes WHERE taxonomy_id=$1
		ORDER BY display_order, code`, definitionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := make(map[uuid.UUID]*taxonomy.ReportNode)
	parentIDs := make(map[uuid.UUID]*uuid.UUID)
	for rows.Next() {
		node := &taxonomy.ReportNode{DefinitionID: definitionID}
		var parentID *uuid.UUID
		var accountFrom, accountTo, formulaExpr *string
		if err := rows.Scan(&node.ID, &parentID, &node.Code, &node.TitleAr, &node.TitleEn,
			&node.NodeType, &node.DisplayOrder, &node.SignMultiplier, &accountFrom, &accountTo,
			&node.AccountTags, &formulaExpr); err != nil {
			return nil, err
		}
		if accountFrom != nil {
			node.AccountFrom = *accountFrom
		}
		if accountTo != nil {
			node.AccountTo = *accountTo
		}
		if formulaExpr != nil {
			node.FormulaExpr = *formulaExpr
		}
		nodes[node.ID] = node
		parentIDs[node.ID] = parentID
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	roots := make([]*taxonomy.ReportNode, 0)
	for id, node := range nodes {
		parentID := parentIDs[id]
		if parentID == nil {
			roots = append(roots, node)
			continue
		}
		parent, ok := nodes[*parentID]
		if !ok {
			return nil, fmt.Errorf("taxonomy: node %s references a missing parent", id)
		}
		node.ParentID = parentID
		parent.Children = append(parent.Children, node)
	}
	sort.Slice(roots, func(i, j int) bool {
		if roots[i].DisplayOrder == roots[j].DisplayOrder {
			return roots[i].Code < roots[j].Code
		}
		return roots[i].DisplayOrder < roots[j].DisplayOrder
	})
	return roots, nil
}

func insertTaxonomyNode(ctx context.Context, tx pgx.Tx, taxonomyID uuid.UUID, node *taxonomy.ReportNode, parentID *uuid.UUID) error {
	id := node.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	var accountFrom, accountTo, formulaExpr any
	if node.AccountFrom != "" {
		accountFrom = node.AccountFrom
	}
	if node.AccountTo != "" {
		accountTo = node.AccountTo
	}
	if node.FormulaExpr != "" {
		formulaExpr = node.FormulaExpr
	}
	sign := node.SignMultiplier
	if sign == 0 {
		sign = 1
	}
	if _, err := tx.Exec(ctx, `INSERT INTO reporting_taxonomy_nodes
		(id, taxonomy_id, parent_id, code, title_ar, title_en, node_type, display_order,
		 sign_multiplier, account_from, account_to, account_tags, formula_expr)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		id, taxonomyID, parentID, node.Code, node.TitleAr, node.TitleEn, node.NodeType, node.DisplayOrder,
		sign, accountFrom, accountTo, node.AccountTags, formulaExpr); err != nil {
		return err
	}
	for _, child := range sortedTaxonomyNodes(node.Children) {
		if err := insertTaxonomyNode(ctx, tx, taxonomyID, child, &id); err != nil {
			return err
		}
	}
	return nil
}

func sortedTaxonomyNodes(nodes []*taxonomy.ReportNode) []*taxonomy.ReportNode {
	sorted := append([]*taxonomy.ReportNode(nil), nodes...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].DisplayOrder == sorted[j].DisplayOrder {
			return sorted[i].Code < sorted[j].Code
		}
		return sorted[i].DisplayOrder < sorted[j].DisplayOrder
	})
	return sorted
}

func positiveVersion(version int) int {
	if version < 1 {
		return 1
	}
	return version
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
