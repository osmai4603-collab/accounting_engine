package consolidation

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"golang.org/x/text/currency"
)

var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

type Engine struct {
	rates RateProvider
}

func NewEngine(rates RateProvider) *Engine {
	return &Engine{rates: rates}
}

type sourceAccount struct {
	entity  string
	account string
}

type validatedGroup struct {
	entities  map[string]Entity
	accounts  map[string]GroupAccount
	mappings  map[sourceAccount]AccountMapping
	ownership map[string]Ownership
}

func (e *Engine) Consolidate(group Group, trialBalances []TrialBalance) (Result, error) {
	validated, err := validateGroup(group, trialBalances)
	if err != nil {
		return Result{}, err
	}

	result := Result{
		GroupID:  group.ID,
		Period:   group.Period,
		Currency: group.ReportingCurrency,
	}
	balances := make(map[string]decimal.Decimal, len(group.Accounts))
	sourceBalances := make(map[sourceAccount]decimal.Decimal)
	entityIncome := make(map[string]decimal.Decimal)

	orderedTBs := append([]TrialBalance(nil), trialBalances...)
	sort.Slice(orderedTBs, func(i, j int) bool { return orderedTBs[i].EntityID < orderedTBs[j].EntityID })
	for _, tb := range orderedTBs {
		entityNet := decimal.Zero
		lines := append([]TrialBalanceLine(nil), tb.Lines...)
		sort.Slice(lines, func(i, j int) bool { return lines[i].AccountID < lines[j].AccountID })
		for _, line := range lines {
			mapping := validated.mappings[sourceAccount{entity: tb.EntityID, account: line.AccountID}]
			rate, rateErr := e.exchangeRate(group, validated.entities[tb.EntityID], mapping)
			if rateErr != nil {
				return Result{}, rateErr
			}
			net := line.EndingDebit.Sub(line.EndingCredit).Mul(rate)
			balances[mapping.GroupAccountID] = balances[mapping.GroupAccountID].Add(net)
			sourceBalances[sourceAccount{entity: tb.EntityID, account: line.AccountID}] = net
			entityNet = entityNet.Add(net)

			accountType := validated.accounts[mapping.GroupAccountID].Type
			periodNet := line.PeriodDebit.Sub(line.PeriodCredit).Mul(rate)
			switch accountType {
			case AccountRevenue:
				entityIncome[tb.EntityID] = entityIncome[tb.EntityID].Sub(periodNet)
			case AccountExpense:
				entityIncome[tb.EntityID] = entityIncome[tb.EntityID].Sub(periodNet)
			}
		}
		if !entityNet.IsZero() {
			if group.TranslationReserveAccountID == "" {
				return Result{}, fmt.Errorf("%w: entity %q has translated imbalance %s and no translation reserve account",
					ErrInvalidInput, tb.EntityID, entityNet)
			}
			reserve := validated.accounts[group.TranslationReserveAccountID]
			if reserve.Type != AccountEquity {
				return Result{}, fmt.Errorf("%w: translation reserve account %q must be equity",
					ErrInvalidInput, reserve.ID)
			}
			adjustment := entityNet.Neg()
			balances[reserve.ID] = balances[reserve.ID].Add(adjustment)
			result.TranslationAdjustments = append(result.TranslationAdjustments, TranslationAdjustment{
				EntityID: tb.EntityID, AccountID: reserve.ID, Amount: adjustment,
			})
		}
	}

	if err := e.eliminateIntercompanyBalances(group, validated, sourceBalances, balances, &result); err != nil {
		return Result{}, err
	}
	if err := e.eliminateIntercompanyTrading(group, validated, sourceBalances, balances, entityIncome, &result); err != nil {
		return Result{}, err
	}
	if err := e.eliminateInventory(group, validated, sourceBalances, balances, entityIncome, &result); err != nil {
		return Result{}, err
	}
	if err := e.eliminateInvestments(group, validated, sourceBalances, balances, &result); err != nil {
		return Result{}, err
	}

	nciIncome := decimal.Zero
	for _, subsidiaryID := range sortedOwnershipIDs(validated.ownership) {
		groupShare := effectiveGroupOwnership(group.ParentEntityID, subsidiaryID, validated.ownership)
		nciIncome = nciIncome.Add(decimal.NewFromInt(1).Sub(groupShare).Mul(entityIncome[subsidiaryID]))
	}
	result.Totals = calculateTotals(validated, balances, nciIncome)
	for _, account := range group.Accounts {
		result.Accounts = append(result.Accounts, AccountBalance{
			AccountID: account.ID, Name: account.Name, Type: account.Type, Amount: balances[account.ID],
		})
	}
	sort.Slice(result.Accounts, func(i, j int) bool { return result.Accounts[i].AccountID < result.Accounts[j].AccountID })
	sort.Slice(result.TranslationAdjustments, func(i, j int) bool {
		if result.TranslationAdjustments[i].EntityID == result.TranslationAdjustments[j].EntityID {
			return result.TranslationAdjustments[i].AccountID < result.TranslationAdjustments[j].AccountID
		}
		return result.TranslationAdjustments[i].EntityID < result.TranslationAdjustments[j].EntityID
	})
	if !result.Totals.Balanced {
		return result, fmt.Errorf("%w: difference %s", ErrUnbalancedConsolidation, result.Totals.BalanceDifference)
	}
	return result, nil
}

func (e *Engine) exchangeRate(group Group, entity Entity, mapping AccountMapping) (decimal.Decimal, error) {
	if entity.FunctionalCurrency == group.ReportingCurrency {
		return decimal.NewFromInt(1), nil
	}
	if e == nil || e.rates == nil {
		return decimal.Zero, fmt.Errorf("%w: %s to %s (%s)", ErrMissingExchangeRate,
			entity.FunctionalCurrency, group.ReportingCurrency, mapping.RateClass)
	}
	request := RateRequest{
		EntityID:     entity.ID,
		FromCurrency: entity.FunctionalCurrency,
		ToCurrency:   group.ReportingCurrency,
		Class:        mapping.RateClass,
		StartDate:    group.Period.StartDate,
		EndDate:      group.Period.EndDate,
	}
	switch mapping.RateClass {
	case RateClosing:
		request.AsOfDate = group.Period.EndDate
	case RateAverage:
		request.AsOfDate = group.Period.EndDate
	case RateHistorical:
		request.AsOfDate = mapping.HistoricalDate
	default:
		return decimal.Zero, fmt.Errorf("%w: unsupported rate class %q", ErrInvalidInput, mapping.RateClass)
	}
	rate, err := e.rates.Rate(request)
	if err != nil {
		return decimal.Zero, fmt.Errorf("%w: %s to %s, %s at %s: %v",
			ErrMissingExchangeRate, request.FromCurrency, request.ToCurrency, request.Class,
			request.AsOfDate.Format("2006-01-02"), err)
	}
	if !rate.IsPositive() {
		return decimal.Zero, fmt.Errorf("%w: rate must be positive for %s to %s, %s",
			ErrInvalidInput, request.FromCurrency, request.ToCurrency, request.Class)
	}
	return rate, nil
}

func validateGroup(group Group, tbs []TrialBalance) (validatedGroup, error) {
	fail := func(format string, args ...any) (validatedGroup, error) {
		return validatedGroup{}, fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, args...))
	}
	if !nonblank(group.ID) || !nonblank(group.ParentEntityID) {
		return fail("group and parent entity IDs are required")
	}
	if !nonblank(group.Policies.AccountingPolicyID) ||
		!nonblank(group.Policies.ControlPolicyID) ||
		!nonblank(group.Policies.CurrencyTranslationPolicyID) ||
		!nonblank(group.Policies.EliminationPolicyID) ||
		!nonblank(group.Policies.IntercompanyPolicyID) ||
		(group.Policies.EliminationScope != PolicyScopeNone && group.Policies.EliminationScope != PolicyScopeConfigured) ||
		(group.Policies.IntercompanyScope != PolicyScopeNone && group.Policies.IntercompanyScope != PolicyScopeConfigured) {
		return validatedGroup{}, fmt.Errorf("%w: policy identifiers and explicit elimination/intercompany scopes are required",
			ErrMissingConsolidationPolicy)
	}
	if !validCurrency(group.ReportingCurrency) {
		return fail("invalid reporting currency %q", group.ReportingCurrency)
	}
	if !validDate(group.Period.StartDate) || !validDate(group.Period.EndDate) ||
		dateAfter(group.Period.StartDate, group.Period.EndDate) {
		return fail("invalid reporting period")
	}

	result := validatedGroup{
		entities:  make(map[string]Entity, len(group.Entities)),
		accounts:  make(map[string]GroupAccount, len(group.Accounts)),
		mappings:  make(map[sourceAccount]AccountMapping, len(group.Mappings)),
		ownership: make(map[string]Ownership, len(group.Ownerships)),
	}
	for _, entity := range group.Entities {
		entityUUID, uuidErr := uuid.Parse(entity.ID)
		if uuidErr != nil || entityUUID == uuid.Nil || !validCurrency(entity.FunctionalCurrency) {
			return fail("entity ID must be a non-nil UUID with a three-letter uppercase functional currency")
		}
		if _, exists := result.entities[entity.ID]; exists {
			return fail("duplicate entity %q", entity.ID)
		}
		result.entities[entity.ID] = entity
	}
	if _, ok := result.entities[group.ParentEntityID]; !ok {
		return fail("parent entity %q is not in the group", group.ParentEntityID)
	}
	if len(group.Entities) == 0 || len(tbs) != len(group.Entities) {
		return fail("exactly one trial balance is required for every group entity")
	}
	for _, account := range group.Accounts {
		if !nonblank(account.ID) || !validAccountType(account.Type) {
			return fail("group account ID and valid account type are required")
		}
		if _, exists := result.accounts[account.ID]; exists {
			return fail("duplicate group account %q", account.ID)
		}
		result.accounts[account.ID] = account
	}
	for _, mapping := range group.Mappings {
		if !nonblank(mapping.EntityID) || !nonblank(mapping.SourceAccountID) || !nonblank(mapping.GroupAccountID) {
			return fail("mapping entity, source account, and group account are required")
		}
		if _, ok := result.entities[mapping.EntityID]; !ok {
			return fail("mapping references unknown entity %q", mapping.EntityID)
		}
		if _, ok := result.accounts[mapping.GroupAccountID]; !ok {
			return fail("mapping references unknown group account %q", mapping.GroupAccountID)
		}
		if mapping.RateClass != RateClosing && mapping.RateClass != RateAverage && mapping.RateClass != RateHistorical {
			return fail("mapping for %q has invalid rate class %q", mapping.SourceAccountID, mapping.RateClass)
		}
		if mapping.RateClass == RateHistorical &&
			(!validDate(mapping.HistoricalDate) || dateAfter(mapping.HistoricalDate, group.Period.EndDate)) {
			return fail("historical mapping for %q requires a date no later than period end", mapping.SourceAccountID)
		}
		key := sourceAccount{entity: mapping.EntityID, account: mapping.SourceAccountID}
		if _, exists := result.mappings[key]; exists {
			return fail("duplicate mapping for %s/%s", mapping.EntityID, mapping.SourceAccountID)
		}
		result.mappings[key] = mapping
	}

	seenTB := make(map[string]bool, len(tbs))
	for _, tb := range tbs {
		entity, ok := result.entities[tb.EntityID]
		if !ok || seenTB[tb.EntityID] {
			return fail("unknown or duplicate trial balance entity %q", tb.EntityID)
		}
		seenTB[tb.EntityID] = true
		if !validCurrency(tb.Currency) || tb.Currency != entity.FunctionalCurrency {
			return fail("trial balance currency for %q must match functional currency %q",
				tb.EntityID, entity.FunctionalCurrency)
		}
		if !sameDate(tb.StartDate, group.Period.StartDate) || !sameDate(tb.EndDate, group.Period.EndDate) {
			return fail("trial balance period for %q does not match group period", tb.EntityID)
		}
		if len(tb.Lines) == 0 {
			return fail("trial balance for %q has no lines", tb.EntityID)
		}
		debits, credits := decimal.Zero, decimal.Zero
		lineIDs := make(map[string]bool, len(tb.Lines))
		for _, line := range tb.Lines {
			if !nonblank(line.AccountID) || line.PeriodDebit.IsNegative() || line.PeriodCredit.IsNegative() ||
				line.EndingDebit.IsNegative() || line.EndingCredit.IsNegative() ||
				(line.EndingDebit.IsPositive() && line.EndingCredit.IsPositive()) {
				return fail("invalid ending balance on %s/%s", tb.EntityID, line.AccountID)
			}
			if lineIDs[line.AccountID] {
				return fail("duplicate trial balance account %s/%s", tb.EntityID, line.AccountID)
			}
			lineIDs[line.AccountID] = true
			if _, ok := result.mappings[sourceAccount{entity: tb.EntityID, account: line.AccountID}]; !ok {
				return validatedGroup{}, fmt.Errorf("%w: %s/%s", ErrMissingAccountMapping, tb.EntityID, line.AccountID)
			}
			debits = debits.Add(line.EndingDebit)
			credits = credits.Add(line.EndingCredit)
		}
		if !debits.Equal(credits) {
			return validatedGroup{}, fmt.Errorf("%w: entity %s debit %s, credit %s",
				ErrUnbalancedTrialBalance, tb.EntityID, debits, credits)
		}
	}

	for _, ownership := range group.Ownerships {
		if !nonblank(ownership.ControlBasis) || !nonblank(ownership.ControlEvidenceReference) {
			return validatedGroup{}, fmt.Errorf("%w: control basis and evidence reference are required for subsidiary %q",
				ErrMissingConsolidationPolicy, ownership.SubsidiaryEntityID)
		}
		if ownership.ParentEntityID == ownership.SubsidiaryEntityID || ownership.SubsidiaryEntityID == group.ParentEntityID ||
			!nonblank(ownership.SubsidiaryEntityID) {
			return fail("ownership must link distinct entities in the group")
		}
		if _, ok := result.entities[ownership.ParentEntityID]; !ok {
			return fail("ownership references unknown parent %q", ownership.ParentEntityID)
		}
		if _, ok := result.entities[ownership.SubsidiaryEntityID]; !ok {
			return fail("ownership references unknown subsidiary %q", ownership.SubsidiaryEntityID)
		}
		if result.ownership[ownership.SubsidiaryEntityID].SubsidiaryEntityID != "" {
			return fail("duplicate ownership for subsidiary %q", ownership.SubsidiaryEntityID)
		}
		if !ownership.Percentage.IsPositive() || ownership.Percentage.GreaterThan(decimal.NewFromInt(1)) ||
			!validDate(ownership.AcquisitionDate) || dateAfter(ownership.AcquisitionDate, group.Period.StartDate) ||
			ownership.Consideration.IsNegative() || ownership.FairValueNetAssets.IsNegative() ||
			(ownership.GoodwillMethod != "" && ownership.GoodwillMethod != PartialGoodwill && ownership.GoodwillMethod != FullGoodwill) ||
			(ownership.GoodwillMethod == FullGoodwill && !ownership.NCIAtFairValue.IsPositive()) ||
			ownership.NCIAtFairValue.IsNegative() {
			return fail("ownership must predate or equal the reporting period; invalid acquisition values for %q", ownership.SubsidiaryEntityID)
		}
		if !mappedAccountType(result, ownership.ParentEntityID, ownership.InvestmentAccountID, AccountAsset) {
			return fail("investment account %q must map to an asset of the parent", ownership.InvestmentAccountID)
		}
		for _, item := range []struct {
			id    string
			type_ AccountType
		}{
			{ownership.GoodwillAccountID, AccountAsset},
			{ownership.NCIAccountID, AccountEquity},
			{ownership.ParentRetainedEarningsAccountID, AccountEquity},
		} {
			account, ok := result.accounts[item.id]
			if !ok || account.Type != item.type_ {
				return fail("account %q must have type %s", item.id, item.type_)
			}
		}
		result.ownership[ownership.SubsidiaryEntityID] = ownership
	}
	for id := range result.entities {
		if id != group.ParentEntityID && result.ownership[id].SubsidiaryEntityID == "" {
			return fail("subsidiary %q has no ownership/acquisition data", id)
		}
	}
	for id := range result.ownership {
		seen := map[string]bool{}
		current := id
		for current != group.ParentEntityID {
			if seen[current] {
				return fail("ownership chain contains a cycle at %q", current)
			}
			seen[current] = true
			ownership, ok := result.ownership[current]
			if !ok {
				return fail("ownership chain for %q does not reach group parent", id)
			}
			current = ownership.ParentEntityID
		}
	}
	if group.TranslationReserveAccountID != "" {
		account, ok := result.accounts[group.TranslationReserveAccountID]
		if !ok || account.Type != AccountEquity {
			return fail("translation reserve account must be a defined equity account")
		}
	}
	if group.BargainPurchaseGainAccountID != "" {
		account, ok := result.accounts[group.BargainPurchaseGainAccountID]
		if !ok || account.Type != AccountRevenue {
			return fail("bargain purchase gain account must be a defined revenue account")
		}
	}
	if err := validateRules(group, result); err != nil {
		return validatedGroup{}, err
	}
	hasEliminationRules := len(group.Rules.IntercompanyBalances) > 0 ||
		len(group.Rules.IntercompanyTrading) > 0 || len(group.Rules.UnrealizedInventory) > 0
	hasIntercompanyRules := len(group.Rules.IntercompanyBalances) > 0 || len(group.Rules.IntercompanyTrading) > 0
	if (group.Policies.EliminationScope == PolicyScopeConfigured) != hasEliminationRules ||
		(group.Policies.IntercompanyScope == PolicyScopeConfigured) != hasIntercompanyRules {
		return validatedGroup{}, fmt.Errorf("%w: declared elimination/intercompany scope does not match configured rules",
			ErrMissingConsolidationPolicy)
	}
	return result, nil
}

func validateRules(group Group, v validatedGroup) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, args...))
	}
	ids := make(map[string]bool)
	checkID := func(id string) error {
		if !nonblank(id) || ids[id] {
			return fail("elimination rule IDs must be nonempty and unique: %q", id)
		}
		ids[id] = true
		return nil
	}
	for _, rule := range group.Rules.IntercompanyBalances {
		if err := checkID(rule.ID); err != nil {
			return err
		}
		if !mappedAccountType(v, rule.ReceivableEntityID, rule.ReceivableAccountID, AccountAsset) ||
			!mappedAccountType(v, rule.PayableEntityID, rule.PayableAccountID, AccountLiability) {
			return fail("intercompany balance rule %q requires mapped receivable and payable accounts", rule.ID)
		}
	}
	for _, rule := range group.Rules.IntercompanyTrading {
		if err := checkID(rule.ID); err != nil {
			return err
		}
		if !mappedAccountType(v, rule.SellerEntityID, rule.SalesAccountID, AccountRevenue) ||
			!mappedAccountType(v, rule.BuyerEntityID, rule.ExpenseAccountID, AccountExpense) {
			return fail("intercompany trading rule %q requires mapped sales and expense accounts", rule.ID)
		}
	}
	for _, rule := range group.Rules.UnrealizedInventory {
		if err := checkID(rule.ID); err != nil {
			return err
		}
		if rule.UnrealizedProfit.IsNegative() || !rule.UnrealizedProfit.IsPositive() ||
			!nonblank(rule.SellerEntityID) || v.entities[rule.SellerEntityID].ID == "" ||
			!mappedAccountType(v, rule.InventoryEntityID, rule.InventoryAccountID, AccountAsset) {
			return fail("inventory rule %q requires a seller, positive profit, and mapped inventory account", rule.ID)
		}
		expense, ok := v.accounts[rule.ExpenseGroupAccountID]
		if !ok || expense.Type != AccountExpense {
			return fail("inventory rule %q requires a group expense account", rule.ID)
		}
	}
	return nil
}

func mappedAccountType(v validatedGroup, entityID, sourceID string, expected AccountType) bool {
	mapping, ok := v.mappings[sourceAccount{entity: entityID, account: sourceID}]
	if !ok {
		return false
	}
	account, ok := v.accounts[mapping.GroupAccountID]
	return ok && account.Type == expected
}

func (e *Engine) eliminateIntercompanyBalances(group Group, v validatedGroup, source map[sourceAccount]decimal.Decimal, balances map[string]decimal.Decimal, result *Result) error {
	rules := append([]IntercompanyBalanceRule(nil), group.Rules.IntercompanyBalances...)
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	for _, rule := range rules {
		arKey := sourceAccount{entity: rule.ReceivableEntityID, account: rule.ReceivableAccountID}
		apKey := sourceAccount{entity: rule.PayableEntityID, account: rule.PayableAccountID}
		ar, ap := source[arKey], source[apKey]
		if ar.IsNegative() || ap.IsPositive() {
			return fmt.Errorf("%w: rule %s", ErrInsufficientIntercompany, rule.ID)
		}
		amount := decimal.Min(ar, ap.Abs())
		if amount.IsZero() {
			continue
		}
		arMapping, apMapping := v.mappings[arKey], v.mappings[apKey]
		entry := makeEntry(rule.ID, "intercompany-ar-ap", "Eliminate reciprocal intercompany receivable/payable",
			map[string]decimal.Decimal{apMapping.GroupAccountID: amount, arMapping.GroupAccountID: amount.Neg()})
		if err := appendEntry(result, balances, entry); err != nil {
			return err
		}
		source[arKey] = ar.Sub(amount)
		source[apKey] = ap.Add(amount)
	}
	return nil
}

func (e *Engine) eliminateIntercompanyTrading(group Group, v validatedGroup, source map[sourceAccount]decimal.Decimal, balances map[string]decimal.Decimal, entityIncome map[string]decimal.Decimal, result *Result) error {
	rules := append([]IntercompanyTradingRule(nil), group.Rules.IntercompanyTrading...)
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	for _, rule := range rules {
		salesKey := sourceAccount{entity: rule.SellerEntityID, account: rule.SalesAccountID}
		expenseKey := sourceAccount{entity: rule.BuyerEntityID, account: rule.ExpenseAccountID}
		sales, expense := source[salesKey], source[expenseKey]
		if sales.IsPositive() || expense.IsNegative() {
			return fmt.Errorf("%w: trading rule %s has unexpected account signs", ErrInsufficientIntercompany, rule.ID)
		}
		amount := decimal.Min(sales.Abs(), expense)
		if amount.IsZero() {
			continue
		}
		salesAccount := v.mappings[salesKey].GroupAccountID
		expenseAccount := v.mappings[expenseKey].GroupAccountID
		entry := makeEntry(rule.ID, "intercompany-sales-expense", "Eliminate intercompany sales and expense",
			map[string]decimal.Decimal{salesAccount: amount, expenseAccount: amount.Neg()})
		if err := appendEntry(result, balances, entry); err != nil {
			return err
		}
		source[salesKey] = sales.Add(amount)
		source[expenseKey] = expense.Sub(amount)
		entityIncome[rule.SellerEntityID] = entityIncome[rule.SellerEntityID].Sub(amount)
		entityIncome[rule.BuyerEntityID] = entityIncome[rule.BuyerEntityID].Add(amount)
	}
	return nil
}

func (e *Engine) eliminateInventory(group Group, v validatedGroup, source map[sourceAccount]decimal.Decimal, balances map[string]decimal.Decimal, entityIncome map[string]decimal.Decimal, result *Result) error {
	rules := append([]UnrealizedInventoryRule(nil), group.Rules.UnrealizedInventory...)
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	for _, rule := range rules {
		key := sourceAccount{entity: rule.InventoryEntityID, account: rule.InventoryAccountID}
		inventory := source[key]
		if inventory.IsNegative() || rule.UnrealizedProfit.GreaterThan(inventory) {
			return fmt.Errorf("%w: rule %s", ErrInsufficientInventory, rule.ID)
		}
		inventoryAccount := v.mappings[key].GroupAccountID
		entry := makeEntry(rule.ID, "unrealized-inventory-profit", "Defer unrealized profit in closing inventory",
			map[string]decimal.Decimal{
				rule.ExpenseGroupAccountID: rule.UnrealizedProfit,
				inventoryAccount:           rule.UnrealizedProfit.Neg(),
			})
		if err := appendEntry(result, balances, entry); err != nil {
			return err
		}
		source[key] = inventory.Sub(rule.UnrealizedProfit)
		entityIncome[rule.SellerEntityID] = entityIncome[rule.SellerEntityID].Sub(rule.UnrealizedProfit)
	}
	return nil
}

func (e *Engine) eliminateInvestments(group Group, v validatedGroup, source map[sourceAccount]decimal.Decimal, balances map[string]decimal.Decimal, result *Result) error {
	for _, subsidiaryID := range ownershipLeafFirst(v.ownership) {
		ownership := v.ownership[subsidiaryID]
		deltas := make(map[string]decimal.Decimal)
		var currentNetAssets decimal.Decimal
		for key, mapping := range v.mappings {
			if key.entity != subsidiaryID {
				continue
			}
			account := v.accounts[mapping.GroupAccountID]
			amount := source[key]
			if account.Type == AccountAsset || account.Type == AccountLiability {
				currentNetAssets = currentNetAssets.Add(amount)
			}
			if account.Type == AccountEquity && !amount.IsZero() {
				deltas[mapping.GroupAccountID] = deltas[mapping.GroupAccountID].Sub(amount)
				source[key] = decimal.Zero
			}
		}
		investmentKey := sourceAccount{entity: ownership.ParentEntityID, account: ownership.InvestmentAccountID}
		investmentBalance := source[investmentKey]
		if investmentBalance.IsNegative() {
			return fmt.Errorf("%w: investment account %s/%s is not a debit balance",
				ErrInvalidInput, investmentKey.entity, investmentKey.account)
		}
		if !investmentBalance.IsZero() {
			deltas[v.mappings[investmentKey].GroupAccountID] =
				deltas[v.mappings[investmentKey].GroupAccountID].Sub(investmentBalance)
			source[investmentKey] = decimal.Zero
		}

		method := ownership.GoodwillMethod
		if method == "" {
			method = PartialGoodwill
		}
		goodwill := ownership.Consideration.Sub(ownership.Percentage.Mul(ownership.FairValueNetAssets))
		if method == FullGoodwill {
			goodwill = ownership.Consideration.Add(ownership.NCIAtFairValue).Sub(ownership.FairValueNetAssets)
		}
		if goodwill.IsPositive() {
			deltas[ownership.GoodwillAccountID] = deltas[ownership.GoodwillAccountID].Add(goodwill)
		} else if goodwill.IsNegative() {
			if group.BargainPurchaseGainAccountID == "" {
				return fmt.Errorf("%w: bargain purchase for %s requires a gain account",
					ErrInvalidInput, subsidiaryID)
			}
			deltas[group.BargainPurchaseGainAccountID] =
				deltas[group.BargainPurchaseGainAccountID].Add(goodwill)
		}
		nci := decimal.NewFromInt(1).Sub(ownership.Percentage).Mul(currentNetAssets)
		if method == FullGoodwill {
			nci = ownership.NCIAtFairValue.Add(
				decimal.NewFromInt(1).Sub(ownership.Percentage).
					Mul(currentNetAssets.Sub(ownership.FairValueNetAssets)),
			)
		}
		deltas[ownership.NCIAccountID] = deltas[ownership.NCIAccountID].Sub(nci)

		var netDelta decimal.Decimal
		for _, delta := range deltas {
			netDelta = netDelta.Add(delta)
		}
		deltas[ownership.ParentRetainedEarningsAccountID] =
			deltas[ownership.ParentRetainedEarningsAccountID].Sub(netDelta)
		entry := makeEntry("investment-"+subsidiaryID, "investment-equity-goodwill-nci",
			"Eliminate parent investment and subsidiary equity; recognize goodwill and NCI", deltas)
		if err := appendEntry(result, balances, entry); err != nil {
			return err
		}
	}
	return nil
}

func appendEntry(result *Result, balances map[string]decimal.Decimal, entry JournalEntry) error {
	if entry.Debit.IsZero() && entry.Credit.IsZero() {
		return nil
	}
	if !entry.Debit.Equal(entry.Credit) {
		return fmt.Errorf("%w: %s has debit %s, credit %s", ErrUnbalancedJournal, entry.ID, entry.Debit, entry.Credit)
	}
	for _, line := range entry.Lines {
		balances[line.AccountID] = balances[line.AccountID].Add(line.Debit).Sub(line.Credit)
	}
	result.Entries = append(result.Entries, entry)
	return nil
}

func makeEntry(id, kind, memo string, deltas map[string]decimal.Decimal) JournalEntry {
	entry := JournalEntry{ID: id, Kind: kind, Memo: memo}
	ids := make([]string, 0, len(deltas))
	for id, amount := range deltas {
		if !amount.IsZero() {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		amount := deltas[id]
		line := JournalLine{AccountID: id}
		if amount.IsPositive() {
			line.Debit = amount
			entry.Debit = entry.Debit.Add(amount)
		} else {
			line.Credit = amount.Abs()
			entry.Credit = entry.Credit.Add(amount.Abs())
		}
		entry.Lines = append(entry.Lines, line)
	}
	return entry
}

func calculateTotals(v validatedGroup, balances map[string]decimal.Decimal, nciIncome decimal.Decimal) FinancialTotals {
	totals := FinancialTotals{NCIIncome: nciIncome}
	for id, account := range v.accounts {
		amount := balances[id]
		switch account.Type {
		case AccountAsset:
			totals.Assets = totals.Assets.Add(amount)
		case AccountLiability:
			totals.Liabilities = totals.Liabilities.Sub(amount)
		case AccountEquity:
			totals.Equity = totals.Equity.Sub(amount)
		case AccountRevenue:
			totals.Revenue = totals.Revenue.Sub(amount)
		case AccountExpense:
			totals.Expenses = totals.Expenses.Add(amount)
		}
	}
	totals.NetIncome = totals.Revenue.Sub(totals.Expenses)
	totals.ParentNetIncome = totals.NetIncome.Sub(nciIncome)
	totals.BalanceDifference = totals.Assets.Sub(totals.Liabilities).Sub(totals.Equity).Sub(totals.NetIncome)
	totals.Balanced = totals.BalanceDifference.IsZero()
	return totals
}

func sortedOwnershipIDs(ownership map[string]Ownership) []string {
	ids := make([]string, 0, len(ownership))
	for id := range ownership {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func effectiveGroupOwnership(groupParent, entity string, ownership map[string]Ownership) decimal.Decimal {
	share := decimal.NewFromInt(1)
	for entity != groupParent {
		link, ok := ownership[entity]
		if !ok {
			return decimal.Zero
		}
		share = share.Mul(link.Percentage)
		entity = link.ParentEntityID
	}
	return share
}

func ownershipLeafFirst(ownership map[string]Ownership) []string {
	ids := sortedOwnershipIDs(ownership)
	depth := func(id string) int {
		n := 0
		for current := id; ; {
			link, ok := ownership[current]
			if !ok {
				return n
			}
			n++
			current = link.ParentEntityID
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		di, dj := depth(ids[i]), depth(ids[j])
		if di != dj {
			return di > dj
		}
		return ids[i] < ids[j]
	})
	return ids
}

func validAccountType(accountType AccountType) bool {
	switch accountType {
	case AccountAsset, AccountLiability, AccountEquity, AccountRevenue, AccountExpense:
		return true
	default:
		return false
	}
}

func validCurrency(currencyCode string) bool {
	if !currencyPattern.MatchString(currencyCode) {
		return false
	}
	_, err := currency.ParseISO(currencyCode)
	return err == nil
}
func nonblank(value string) bool     { return strings.TrimSpace(value) != "" }
func validDate(value time.Time) bool { return !value.IsZero() }

func sameDate(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func dateAfter(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return time.Date(ay, am, ad, 0, 0, 0, 0, time.UTC).After(time.Date(by, bm, bd, 0, 0, 0, 0, time.UTC))
}
