package taxonomy

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	ErrInvalidDefinition = errors.New("taxonomy: invalid report definition")
	ErrInvalidNode       = errors.New("taxonomy: invalid report node")
	ErrUnknownFormulaRef = errors.New("taxonomy: formula references an unknown or not-yet-evaluated node")
)

const (
	maxTaxonomyNodeCount = 10000
	maxTaxonomyNodeDepth = 128
)

type TaxonomyEvaluator struct {
	balancesRepo BalanceReader
	now          func() time.Time
}

func NewTaxonomyEvaluator(repo BalanceReader) *TaxonomyEvaluator {
	return &TaxonomyEvaluator{balancesRepo: repo, now: time.Now}
}

func (e *TaxonomyEvaluator) EvaluateStatement(
	ctx context.Context,
	companyID uuid.UUID,
	def FinancialReportDefinition,
	periodID uuid.UUID,
	currency string,
) (*EvaluatedStatement, error) {
	if e == nil || e.balancesRepo == nil {
		return nil, errors.New("taxonomy: balance reader is required")
	}
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is required", ErrInvalidDefinition)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if companyID == uuid.Nil || periodID == uuid.Nil || strings.TrimSpace(currency) == "" {
		return nil, fmt.Errorf("%w: company, period, and currency are required", ErrInvalidDefinition)
	}
	if err := ValidateDefinition(def); err != nil {
		return nil, err
	}
	period, err := e.balancesRepo.GetPeriodBalances(ctx, companyID, periodID, currency)
	if err != nil {
		return nil, fmt.Errorf("taxonomy: read period balances: %w", err)
	}

	values := make(map[string]decimal.Decimal)
	drilldowns := make(map[string][]uuid.UUID)
	lines := make([]EvaluatedLine, 0)
	accounts := accountsForStatement(period.Accounts, def.Type)
	for _, root := range sortedNodes(def.RootNodes) {
		nodeLines, _, err := evaluateNode(root, 0, accounts, values, drilldowns)
		if err != nil {
			return nil, err
		}
		lines = append(lines, nodeLines...)
	}
	now := time.Now
	if e.now != nil {
		now = e.now
	}
	return &EvaluatedStatement{
		DefinitionCode: def.Code,
		Framework:      def.Framework,
		Type:           def.Type,
		AsOfDate:       period.AsOfDate,
		Currency:       currency,
		Lines:          lines,
		GeneratedAt:    now().UTC(),
	}, nil
}

func accountsForStatement(accounts []AccountBalance, statementType StatementType) []AccountBalance {
	result := append([]AccountBalance(nil), accounts...)
	for i := range result {
		switch statementType {
		case StatementBalanceSheet:
			if !result[i].EndingAmount.IsZero() || !result[i].PeriodAmount.IsZero() {
				result[i].Amount = result[i].EndingAmount
			}
		default:
			if !result[i].PeriodAmount.IsZero() || !result[i].EndingAmount.IsZero() {
				result[i].Amount = result[i].PeriodAmount
			}
		}
	}
	return result
}

func ValidateDefinition(def FinancialReportDefinition) error {
	if strings.TrimSpace(def.Code) == "" || strings.TrimSpace(def.Name) == "" ||
		!validFramework(def.Framework) || !validStatementType(def.Type) {
		return fmt.Errorf("%w: code, name, framework, and statement type are required", ErrInvalidDefinition)
	}
	if len(def.RootNodes) == 0 {
		return fmt.Errorf("%w: at least one root node is required", ErrInvalidDefinition)
	}
	codes := make(map[string]struct{})
	ids := make(map[uuid.UUID]struct{})
	nodeCount := 0
	for _, node := range def.RootNodes {
		if err := validateNode(node, codes, ids, 0, &nodeCount); err != nil {
			return err
		}
	}
	return nil
}

func validateNode(node *ReportNode, codes map[string]struct{}, ids map[uuid.UUID]struct{}, depth int, nodeCount *int) error {
	if depth > maxTaxonomyNodeDepth {
		return fmt.Errorf("%w: tree depth exceeds %d levels", ErrInvalidDefinition, maxTaxonomyNodeDepth)
	}
	*nodeCount = *nodeCount + 1
	if *nodeCount > maxTaxonomyNodeCount {
		return fmt.Errorf("%w: tree exceeds %d nodes", ErrInvalidDefinition, maxTaxonomyNodeCount)
	}
	if node == nil || strings.TrimSpace(node.Code) == "" || strings.TrimSpace(node.TitleEn) == "" ||
		strings.TrimSpace(node.TitleAr) == "" {
		return fmt.Errorf("%w: code and localized titles are required", ErrInvalidNode)
	}
	codeKey := strings.ToUpper(node.Code)
	if _, exists := codes[codeKey]; exists {
		return fmt.Errorf("%w: duplicate code %q", ErrInvalidNode, node.Code)
	}
	codes[codeKey] = struct{}{}
	if node.ID != uuid.Nil {
		if _, exists := ids[node.ID]; exists {
			return fmt.Errorf("%w: duplicate node id %s", ErrInvalidNode, node.ID)
		}
		ids[node.ID] = struct{}{}
	}
	if node.SignMultiplier < -1 || node.SignMultiplier > 1 {
		return fmt.Errorf("%w: sign multiplier for %q must be -1, 0, or 1", ErrInvalidNode, node.Code)
	}
	switch node.NodeType {
	case NodeHeader:
		if node.FormulaExpr != "" || node.AccountFrom != "" || node.AccountTo != "" || len(node.AccountTags) != 0 {
			return fmt.Errorf("%w: header %q cannot define an amount source", ErrInvalidNode, node.Code)
		}
	case NodeAccountRange:
		if node.AccountFrom == "" || node.AccountTo == "" || node.AccountFrom > node.AccountTo {
			return fmt.Errorf("%w: account range %q must have ordered bounds", ErrInvalidNode, node.Code)
		}
	case NodeAccountTag:
		if len(node.AccountTags) == 0 {
			return fmt.Errorf("%w: account tag node %q has no tags", ErrInvalidNode, node.Code)
		}
		for _, tag := range node.AccountTags {
			if strings.TrimSpace(tag) == "" {
				return fmt.Errorf("%w: account tag node %q contains an empty tag", ErrInvalidNode, node.Code)
			}
		}
	case NodeFormula:
		if strings.TrimSpace(node.FormulaExpr) == "" {
			return fmt.Errorf("%w: formula node %q has no expression", ErrInvalidNode, node.Code)
		}
	case NodeSubtotal:
		if len(node.Children) == 0 {
			return fmt.Errorf("%w: subtotal %q has no children", ErrInvalidNode, node.Code)
		}
	default:
		return fmt.Errorf("%w: unsupported node type %q", ErrInvalidNode, node.NodeType)
	}
	for _, child := range node.Children {
		if err := validateNode(child, codes, ids, depth+1, nodeCount); err != nil {
			return err
		}
	}
	return nil
}

func evaluateNode(
	node *ReportNode,
	level int,
	accounts []AccountBalance,
	values map[string]decimal.Decimal,
	drilldowns map[string][]uuid.UUID,
) ([]EvaluatedLine, decimal.Decimal, error) {
	children := sortedNodes(node.Children)
	childLines := make([]EvaluatedLine, 0)
	childTotal := decimal.Zero
	childIDs := make([]uuid.UUID, 0)
	for _, child := range children {
		lines, amount, err := evaluateNode(child, level+1, accounts, values, drilldowns)
		if err != nil {
			return nil, decimal.Zero, err
		}
		childLines = append(childLines, lines...)
		childTotal = childTotal.Add(amount)
		childIDs = append(childIDs, drilldowns[strings.ToUpper(child.Code)]...)
	}

	amount := decimal.Zero
	var ids []uuid.UUID
	switch node.NodeType {
	case NodeHeader:
		ids = uniqueIDs(childIDs)
	case NodeAccountRange, NodeAccountTag:
		for _, account := range accounts {
			if matchesNode(node, account) {
				amount = amount.Add(account.Amount)
				if account.AccountID != uuid.Nil {
					ids = append(ids, account.AccountID)
				}
			}
		}
	case NodeSubtotal:
		amount = childTotal
		ids = childIDs
	case NodeFormula:
		var err error
		amount, err = evaluateFormula(node.FormulaExpr, values)
		if err != nil {
			return nil, decimal.Zero, fmt.Errorf("taxonomy: evaluate formula %q: %w", node.Code, err)
		}
		for _, ref := range formulaReferences(node.FormulaExpr) {
			ids = append(ids, drilldowns[ref]...)
		}
	}
	if node.SignMultiplier == -1 {
		amount = amount.Neg()
	}
	ids = uniqueIDs(ids)
	values[strings.ToUpper(node.Code)] = amount
	drilldowns[strings.ToUpper(node.Code)] = ids

	line := EvaluatedLine{
		Code:         node.Code,
		TitleAr:      node.TitleAr,
		TitleEn:      node.TitleEn,
		Level:        level,
		Amount:       amount,
		IsSubtotal:   node.NodeType == NodeSubtotal || node.NodeType == NodeFormula,
		DrilldownIDs: append([]uuid.UUID(nil), ids...),
	}
	return append([]EvaluatedLine{line}, childLines...), amount, nil
}

func matchesNode(node *ReportNode, account AccountBalance) bool {
	if node.NodeType == NodeAccountRange {
		return account.Code >= node.AccountFrom && account.Code <= node.AccountTo
	}
	accountTags := make(map[string]struct{}, len(account.Tags))
	for _, tag := range account.Tags {
		accountTags[strings.ToLower(strings.TrimSpace(tag))] = struct{}{}
	}
	for _, tag := range node.AccountTags {
		if _, ok := accountTags[strings.ToLower(strings.TrimSpace(tag))]; ok {
			return true
		}
	}
	return false
}

func sortedNodes(nodes []*ReportNode) []*ReportNode {
	sorted := append([]*ReportNode(nil), nodes...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].DisplayOrder == sorted[j].DisplayOrder {
			return sorted[i].Code < sorted[j].Code
		}
		return sorted[i].DisplayOrder < sorted[j].DisplayOrder
	})
	return sorted
}

func uniqueIDs(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	result := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func validFramework(framework ReportingFramework) bool {
	switch framework {
	case FrameworkIFRS, FrameworkUSGAAP, FrameworkSOCPA:
		return true
	default:
		return false
	}
}

func validStatementType(statementType StatementType) bool {
	switch statementType {
	case StatementBalanceSheet, StatementIncome, StatementCashFlow, StatementEquityMovement:
		return true
	default:
		return false
	}
}
