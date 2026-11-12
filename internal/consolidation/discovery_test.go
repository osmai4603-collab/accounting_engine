package consolidation_test

import (
	"testing"

	"accounting_engine/internal/consolidation"
)

func TestDiscoverEliminationRules(t *testing.T) {
	group := consolidation.Group{
		Entities: []consolidation.Entity{
			{ID: "ENT_PARENT"},
			{ID: "ENT_SUB1"},
		},
	}

	tags := []consolidation.IntercompanyAccountTag{
		{
			EntityID:             "ENT_PARENT",
			SourceAccountID:      "1100_AR_SUB1",
			CounterpartyEntityID: "ENT_SUB1",
			Kind:                 consolidation.KindBalanceReceivable,
		},
		{
			EntityID:             "ENT_SUB1",
			SourceAccountID:      "2100_AP_PARENT",
			CounterpartyEntityID: "ENT_PARENT",
			Kind:                 consolidation.KindBalancePayable,
		},
		{
			EntityID:             "ENT_SUB1",
			SourceAccountID:      "4100_SALES_PARENT",
			CounterpartyEntityID: "ENT_PARENT",
			Kind:                 consolidation.KindTradingSales,
		},
		{
			EntityID:             "ENT_PARENT",
			SourceAccountID:      "5100_EXP_SUB1",
			CounterpartyEntityID: "ENT_SUB1",
			Kind:                 consolidation.KindTradingExpense,
		},
	}

	rules := consolidation.DiscoverEliminationRules(group, tags)

	if len(rules.IntercompanyBalances) != 1 {
		t.Fatalf("expected 1 balance rule, got %d", len(rules.IntercompanyBalances))
	}
	bal := rules.IntercompanyBalances[0]
	if bal.ReceivableEntityID != "ENT_PARENT" || bal.PayableEntityID != "ENT_SUB1" {
		t.Fatalf("unexpected balance rule: %+v", bal)
	}

	if len(rules.IntercompanyTrading) != 1 {
		t.Fatalf("expected 1 trading rule, got %d", len(rules.IntercompanyTrading))
	}
	trade := rules.IntercompanyTrading[0]
	if trade.SellerEntityID != "ENT_SUB1" || trade.BuyerEntityID != "ENT_PARENT" {
		t.Fatalf("unexpected trading rule: %+v", trade)
	}
}

func TestAutoDiscoverFromAccountNames(t *testing.T) {
	group := consolidation.Group{
		Entities: []consolidation.Entity{
			{ID: "HOLDING"},
			{ID: "SUBSIDIARY"},
		},
		Mappings: []consolidation.AccountMapping{
			{EntityID: "HOLDING", SourceAccountID: "REC_SUBSIDIARY", GroupAccountID: "G_AR"},
			{EntityID: "SUBSIDIARY", SourceAccountID: "PAY_HOLDING", GroupAccountID: "G_AP"},
		},
	}

	tags := consolidation.AutoDiscoverFromAccountNames(group)
	if len(tags) != 2 {
		t.Fatalf("expected 2 tags discovered, got %d", len(tags))
	}

	rules := consolidation.DiscoverEliminationRules(group, tags)
	if len(rules.IntercompanyBalances) != 1 {
		t.Fatalf("expected 1 discovered balance elimination rule, got %d", len(rules.IntercompanyBalances))
	}
}
