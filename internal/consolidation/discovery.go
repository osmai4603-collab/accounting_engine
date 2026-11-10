package consolidation

import (
	"fmt"
	"strings"
)

type IntercompanyKind string

const (
	KindBalanceReceivable IntercompanyKind = "balance_receivable"
	KindBalancePayable    IntercompanyKind = "balance_payable"
	KindTradingSales      IntercompanyKind = "trading_sales"
	KindTradingExpense    IntercompanyKind = "trading_expense"
)

// IntercompanyAccountTag identifies a ledger account that records transactions with a specific counterparty entity.
// This is completely generic and abstract, relying only on entity IDs and account identifiers.
type IntercompanyAccountTag struct {
	EntityID             string
	SourceAccountID      string
	CounterpartyEntityID string
	Kind                 IntercompanyKind
}

// DiscoverEliminationRules automatically pairs counterparty accounts across entities
// and constructs EliminationRules without requiring manual rule-by-rule configuration.
func DiscoverEliminationRules(group Group, tags []IntercompanyAccountTag) EliminationRules {
	receivables := make(map[string]IntercompanyAccountTag) // key: "EntityID->CounterpartyID"
	payables := make(map[string]IntercompanyAccountTag)    // key: "EntityID->CounterpartyID"
	sales := make(map[string]IntercompanyAccountTag)       // key: "EntityID->CounterpartyID"
	expenses := make(map[string]IntercompanyAccountTag)    // key: "EntityID->CounterpartyID"

	for _, tag := range tags {
		key := fmt.Sprintf("%s->%s", tag.EntityID, tag.CounterpartyEntityID)
		switch tag.Kind {
		case KindBalanceReceivable:
			receivables[key] = tag
		case KindBalancePayable:
			payables[key] = tag
		case KindTradingSales:
			sales[key] = tag
		case KindTradingExpense:
			expenses[key] = tag
		}
	}

	rules := EliminationRules{
		IntercompanyBalances: make([]IntercompanyBalanceRule, 0),
		IntercompanyTrading:  make([]IntercompanyTradingRule, 0),
		UnrealizedInventory:  group.Rules.UnrealizedInventory,
	}

	// 1. Match Intercompany Balances:
	// A has receivable from B <=> B has payable to A
	for _, recTag := range receivables {
		mirrorKey := fmt.Sprintf("%s->%s", recTag.CounterpartyEntityID, recTag.EntityID)
		if payTag, ok := payables[mirrorKey]; ok {
			rules.IntercompanyBalances = append(rules.IntercompanyBalances, IntercompanyBalanceRule{
				ID:                  fmt.Sprintf("ELIM-BAL-%s-%s", recTag.EntityID, payTag.EntityID),
				ReceivableEntityID:  recTag.EntityID,
				ReceivableAccountID: recTag.SourceAccountID,
				PayableEntityID:     payTag.EntityID,
				PayableAccountID:    payTag.SourceAccountID,
			})
		}
	}

	// 2. Match Intercompany Trading:
	// A has sales to B <=> B has expenses/purchases from A
	for _, saleTag := range sales {
		mirrorKey := fmt.Sprintf("%s->%s", saleTag.CounterpartyEntityID, saleTag.EntityID)
		if expTag, ok := expenses[mirrorKey]; ok {
			rules.IntercompanyTrading = append(rules.IntercompanyTrading, IntercompanyTradingRule{
				ID:               fmt.Sprintf("ELIM-TRADE-%s-%s", saleTag.EntityID, expTag.EntityID),
				SellerEntityID:   saleTag.EntityID,
				SalesAccountID:   saleTag.SourceAccountID,
				BuyerEntityID:    expTag.EntityID,
				ExpenseAccountID: expTag.SourceAccountID,
			})
		}
	}

	return rules
}

// AutoDiscoverFromAccountNames inspects account descriptions or IDs in group mappings
// and automatically creates counterparty tags where account IDs contain entity references.
// Example: an account with code or ID "IC_REC_SUB2" on parent entity "PARENT" is paired with "SUB2".
func AutoDiscoverFromAccountNames(group Group) []IntercompanyAccountTag {
	tags := make([]IntercompanyAccountTag, 0)
	entitySet := make(map[string]struct{}, len(group.Entities))
	for _, ent := range group.Entities {
		entitySet[ent.ID] = struct{}{}
	}

	for _, m := range group.Mappings {
		upper := strings.ToUpper(m.SourceAccountID)
		for targetEntity := range entitySet {
			if targetEntity == m.EntityID {
				continue
			}
			if strings.Contains(upper, strings.ToUpper(targetEntity)) {
				if strings.Contains(upper, "REC") || strings.Contains(upper, "AR") {
					tags = append(tags, IntercompanyAccountTag{
						EntityID:             m.EntityID,
						SourceAccountID:      m.SourceAccountID,
						CounterpartyEntityID: targetEntity,
						Kind:                 KindBalanceReceivable,
					})
				} else if strings.Contains(upper, "PAY") || strings.Contains(upper, "AP") {
					tags = append(tags, IntercompanyAccountTag{
						EntityID:             m.EntityID,
						SourceAccountID:      m.SourceAccountID,
						CounterpartyEntityID: targetEntity,
						Kind:                 KindBalancePayable,
					})
				} else if strings.Contains(upper, "SALE") || strings.Contains(upper, "REV") {
					tags = append(tags, IntercompanyAccountTag{
						EntityID:             m.EntityID,
						SourceAccountID:      m.SourceAccountID,
						CounterpartyEntityID: targetEntity,
						Kind:                 KindTradingSales,
					})
				} else if strings.Contains(upper, "EXP") || strings.Contains(upper, "COST") || strings.Contains(upper, "PURCH") {
					tags = append(tags, IntercompanyAccountTag{
						EntityID:             m.EntityID,
						SourceAccountID:      m.SourceAccountID,
						CounterpartyEntityID: targetEntity,
						Kind:                 KindTradingExpense,
					})
				}
			}
		}
	}
	return tags
}
