package templates

import (
	"accounting_engine/pkg/api/v1/events"
)

func LoadRetailTemplate() []events.PostingRule {
	return []events.PostingRule{
		{
			RuleID:              "retail-pos-sale",
			EventType:           "POS_SALE",
			DebitAccountCode:    "1010", // Cash
			CreditAccountCode:   "4010", // Retail Revenue
			DescriptionTemplate: "Retail POS Sale",
		},
	}
}

func LoadAgricultureTemplate() []events.PostingRule {
	return []events.PostingRule{
		{
			RuleID:              "agri-harvest",
			EventType:           "HARVEST_CROP",
			DebitAccountCode:    "1200", // Biological Assets / Inventory
			CreditAccountCode:   "4100", // Agricultural Income
			DescriptionTemplate: "Agricultural Harvest Valuation",
		},
	}
}

func LoadSaaSTemplate() []events.PostingRule {
	return []events.PostingRule{
		{
			RuleID:              "saas-subscription",
			EventType:           "SUBSCRIPTION_REVENUE",
			DebitAccountCode:    "1020", // Bank / Accounts Receivable
			CreditAccountCode:   "2200", // Deferred Revenue / Unearned Revenue
			DescriptionTemplate: "SaaS Subscription Revenue Recognition",
		},
	}
}
