package subledgers

import (
	"context"
	"time"

	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type TreasuryLedger struct {
	repo      TreasuryRepository
	companyID uuid.UUID
}

type TreasuryRepository interface {
	BankItemRepository
	CompanyBankItemRepository
	ConditionalBankItemsAndDraftRepository
	ConditionalBankItemRepository
}

func NewTreasuryLedger(repo TreasuryRepository) *TreasuryLedger {
	return &TreasuryLedger{repo: repo}
}

func NewTreasuryLedgerForCompany(repo TreasuryRepository, companyID uuid.UUID) *TreasuryLedger {
	return &TreasuryLedger{repo: repo, companyID: companyID}
}

// Deprecated: use RecordItemContext.
func (l *TreasuryLedger) RecordItem(accountID uuid.UUID, date time.Time, amount financialtypes.Money, reference string, status BankItemStatus) (BankItem, error) {
	return l.RecordItemContext(context.Background(), accountID, date, amount, reference, status)
}

func (l *TreasuryLedger) RecordItemContext(ctx context.Context, accountID uuid.UUID, date time.Time, amount financialtypes.Money, reference string, status BankItemStatus) (BankItem, error) {
	if l.companyID == uuid.Nil {
		return l.recordItem(ctx, uuid.Nil, accountID, uuid.Nil, date, amount, reference, status)
	}
	return l.recordItem(ctx, l.companyID, accountID, uuid.Nil, date, amount, reference, status)
}

// Deprecated: use RecordItemForCompanyContext.
func (l *TreasuryLedger) RecordItemForCompany(companyID, accountID uuid.UUID, date time.Time, amount financialtypes.Money, reference string, status BankItemStatus) (BankItem, error) {
	return l.RecordItemForCompanyContext(context.Background(), companyID, accountID, date, amount, reference, status)
}

func (l *TreasuryLedger) RecordItemForCompanyContext(ctx context.Context, companyID, accountID uuid.UUID, date time.Time, amount financialtypes.Money, reference string, status BankItemStatus) (BankItem, error) {
	if companyID == uuid.Nil {
		return BankItem{}, ErrCompanyScopeRequired
	}
	if l.companyID != uuid.Nil && l.companyID != companyID {
		return BankItem{}, ErrCompanyMismatch
	}
	return l.recordItem(ctx, companyID, accountID, uuid.Nil, date, amount, reference, status)
}

// Deprecated: use RecordItemWithJournalContext.
func (l *TreasuryLedger) RecordItemWithJournal(accountID, journalEntryID uuid.UUID, date time.Time, amount financialtypes.Money, reference string, status BankItemStatus) (BankItem, error) {
	return l.RecordItemWithJournalContext(context.Background(), accountID, journalEntryID, date, amount, reference, status)
}

func (l *TreasuryLedger) RecordItemWithJournalContext(ctx context.Context, accountID, journalEntryID uuid.UUID, date time.Time, amount financialtypes.Money, reference string, status BankItemStatus) (BankItem, error) {
	companyID := l.companyID
	if companyID == uuid.Nil || journalEntryID == uuid.Nil {
		return BankItem{}, ErrInvalidOpenLine
	}
	return l.recordItem(ctx, companyID, accountID, journalEntryID, date, amount, reference, status)
}

func (l *TreasuryLedger) recordItem(ctx context.Context, companyID, accountID, journalEntryID uuid.UUID, date time.Time, amount financialtypes.Money, reference string, status BankItemStatus) (BankItem, error) {
	if ctx == nil {
		return BankItem{}, ErrInvalidOpenLine
	}
	if err := ctx.Err(); err != nil {
		return BankItem{}, err
	}
	if accountID == uuid.Nil || amount.Currency() == "" || status == "" {
		return BankItem{}, ErrInvalidAmount
	}
	item := BankItem{
		CompanyID: companyID, ID: uuid.New(), AccountID: accountID, JournalEntryID: journalEntryID,
		Date: date, Amount: amount, Reference: reference, Status: status,
	}
	if err := l.repo.SaveBankItem(ctx, item); err != nil {
		return BankItem{}, err
	}
	return item, nil
}

// Deprecated: use ReconcileMultiLineContext.
func (l *TreasuryLedger) ReconcileMultiLine(
	accountID uuid.UUID,
	statement []BankItem,
	toleranceDays int,
	feeParams BankFeeParams,
) (BankReconciliationMatch, *posting.JournalEntry, error) {
	return l.ReconcileMultiLineContext(context.Background(), accountID, statement, toleranceDays, feeParams)
}

func (l *TreasuryLedger) ReconcileMultiLineContext(
	ctx context.Context,
	accountID uuid.UUID,
	statement []BankItem,
	toleranceDays int,
	feeParams BankFeeParams,
) (BankReconciliationMatch, *posting.JournalEntry, error) {
	if ctx == nil {
		return BankReconciliationMatch{}, nil, ErrInvalidOpenLine
	}
	if err := ctx.Err(); err != nil {
		return BankReconciliationMatch{}, nil, err
	}
	if accountID == uuid.Nil || len(statement) == 0 || toleranceDays < 0 || toleranceDays > 5 {
		return BankReconciliationMatch{}, nil, ErrInvalidOpenLine
	}
	ledgerItems, err := l.listBankItems(ctx, accountID.String())
	if err != nil {
		return BankReconciliationMatch{}, nil, err
	}
	currency := ""
	statementTotal := decimal.Zero
	statementIDs := make([]uuid.UUID, 0, len(statement))
	seen := make(map[uuid.UUID]struct{}, len(statement))
	statementDates := make([]time.Time, 0, len(statement))
	for _, item := range statement {
		if item.ID == uuid.Nil || item.AccountID != accountID || item.Amount.Currency() == "" || item.Date.IsZero() ||
			(l.companyID != uuid.Nil && item.CompanyID != uuid.Nil && item.CompanyID != l.companyID) {
			return BankReconciliationMatch{}, nil, ErrInvalidOpenLine
		}
		if _, exists := seen[item.ID]; exists {
			return BankReconciliationMatch{}, nil, ErrInvalidOpenLine
		}
		seen[item.ID] = struct{}{}
		if currency == "" {
			currency = item.Amount.Currency()
		} else if currency != item.Amount.Currency() {
			return BankReconciliationMatch{}, nil, ErrMixedCurrencyReconciliation
		}
		statementTotal = statementTotal.Add(item.Amount.Amount())
		statementIDs = append(statementIDs, item.ID)
		statementDates = append(statementDates, item.Date)
	}
	if statementTotal.IsZero() {
		return BankReconciliationMatch{}, nil, ErrInvalidAmount
	}

	candidates := make([]BankItem, 0, len(ledgerItems))
	for _, item := range ledgerItems {
		if item.Status == BankItemMatched {
			continue
		}
		if item.CompanyID != l.companyID && l.companyID != uuid.Nil {
			return BankReconciliationMatch{}, nil, ErrCompanyMismatch
		}
		if item.AccountID != accountID || item.Amount.Currency() != currency || item.Amount.Currency() == "" ||
			item.Date.IsZero() {
			return BankReconciliationMatch{}, nil, ErrInvalidOpenLine
		}
		withinTolerance := false
		for _, statementDate := range statementDates {
			if withinValueDateTolerance(statementDate, item.Date, toleranceDays) {
				withinTolerance = true
				break
			}
		}
		if !withinTolerance {
			continue
		}
		candidates = append(candidates, item)
	}
	indices, ledgerTotal, err := selectBankReconciliationSubset(candidates, statementTotal)
	if err != nil {
		return BankReconciliationMatch{}, nil, err
	}
	if ledgerTotal.LessThan(statementTotal) {
		return BankReconciliationMatch{}, nil, ErrUnmatchedLine
	}
	difference := ledgerTotal.Sub(statementTotal)
	if difference.IsPositive() &&
		(feeParams.JournalID == uuid.Nil || feeParams.FeeAccountID == uuid.Nil || feeParams.BankAccountID != accountID) {
		return BankReconciliationMatch{}, nil, ErrUnmatchedLine
	}
	selected := make([]BankItem, 0, len(indices))
	for _, index := range indices {
		selected = append(selected, candidates[index])
	}
	expected := append([]BankItem(nil), selected...)
	updated := append([]BankItem(nil), selected...)
	ledgerIDs := make([]uuid.UUID, 0, len(selected))
	entryIDs := make([]uuid.UUID, 0, len(selected))
	matchedAt := feeParams.Date
	if matchedAt.IsZero() {
		matchedAt = time.Now().UTC()
	}
	for i := range updated {
		updated[i].Status = BankItemMatched
		updated[i].MatchedAt = matchedAt
		ledgerIDs = append(ledgerIDs, updated[i].ID)
		if updated[i].JournalEntryID != uuid.Nil {
			entryIDs = append(entryIDs, updated[i].JournalEntryID)
		}
	}
	fee, err := financialtypes.NewMoney(difference, currency)
	if err != nil {
		return BankReconciliationMatch{}, nil, err
	}
	zero, err := financialtypes.Zero(currency)
	if err != nil {
		return BankReconciliationMatch{}, nil, err
	}
	match := BankReconciliationMatch{
		StatementLineIDs: statementIDs, JournalEntryIDs: entryIDs, LedgerItemIDs: ledgerIDs,
		BankFeesAmount: zero, Difference: difference,
	}
	if !fee.Amount().IsZero() {
		if l.companyID == uuid.Nil {
			return BankReconciliationMatch{}, nil, ErrCompanyScopedBankItemsRequired
		}
		feeParams.BankAccountID = accountID
		feeDate := feeParams.Date
		if feeDate.IsZero() {
			feeDate = statement[0].Date
		}
		feeLines := []posting.JournalEntryLine{
			{CompanyID: l.companyID, ID: uuid.New(), AccountID: feeParams.FeeAccountID, Debit: fee, Credit: zero},
			{CompanyID: l.companyID, ID: uuid.New(), AccountID: accountID, Debit: zero, Credit: fee},
		}
		reference := feeParams.Description
		if reference == "" {
			reference = "BANK_FEE:MULTI_LINE"
		}
		draft := posting.JournalEntry{
			CompanyID: l.companyID, ID: uuid.New(), JournalID: feeParams.JournalID,
			EntryDate: feeDate, Date: feeDate, Status: posting.EntryDraft,
			DocumentReference: reference, Reference: reference, CreatedAt: time.Now().UTC(),
			Lines: feeLines,
		}
		if err := draft.Validate(); err != nil {
			return BankReconciliationMatch{}, nil, err
		}
		if err := l.repo.CompareAndSaveBankItemsAndDraft(ctx, expected, updated, draft); err != nil {
			return BankReconciliationMatch{}, nil, err
		}
		match.BankFeesAmount = fee
		match.Difference = decimal.Zero
		return match, &draft, nil
	}
	if err := l.repo.CompareAndSaveBankItems(ctx, expected, updated); err != nil {
		return BankReconciliationMatch{}, nil, err
	}
	return match, nil, nil
}

func selectBankReconciliationSubset(candidates []BankItem, target decimal.Decimal) ([]int, decimal.Decimal, error) {
	const maxCandidates = 40
	const maxStates = 50000
	if len(candidates) == 0 {
		return nil, decimal.Zero, ErrNotFound
	}
	if len(candidates) > maxCandidates {
		return nil, decimal.Zero, ErrBankReconciliationTooComplex
	}
	type subset struct {
		indices   []int
		ambiguous bool
	}
	states := map[string]subset{"0": {indices: []int{}}}
	sums := map[string]decimal.Decimal{"0": decimal.Zero}
	for index, candidate := range candidates {
		amount := candidate.Amount.Amount()
		nextStates := make(map[string]subset, len(states)*2)
		nextSums := make(map[string]decimal.Decimal, len(sums)*2)
		for key, existing := range states {
			nextStates[key] = existing
			nextSums[key] = sums[key]
		}
		for key, existing := range states {
			total := sums[key].Add(amount)
			totalKey := total.String()
			indices := append(append([]int(nil), existing.indices...), index)
			if current, exists := nextStates[totalKey]; exists {
				current.ambiguous = true
				current.ambiguous = current.ambiguous || existing.ambiguous
				nextStates[totalKey] = current
			} else {
				nextStates[totalKey] = subset{indices: indices, ambiguous: existing.ambiguous}
				nextSums[totalKey] = total
			}
		}
		if len(nextStates) > maxStates {
			return nil, decimal.Zero, ErrBankReconciliationTooComplex
		}
		states, sums = nextStates, nextSums
	}

	bestKey := ""
	bestTotal := decimal.Zero
	exactKey := target.String()
	if exact, exists := states[exactKey]; exists {
		if exact.ambiguous {
			return nil, decimal.Zero, ErrAmbiguousBankReconciliation
		}
		if len(exact.indices) > 0 {
			return exact.indices, target, nil
		}
	}
	for key, total := range sums {
		candidate := states[key]
		if len(candidate.indices) == 0 || !total.GreaterThan(target) {
			continue
		}
		if bestKey == "" || total.LessThan(bestTotal) {
			bestKey, bestTotal = key, total
		} else if total.Equal(bestTotal) {
			return nil, decimal.Zero, ErrAmbiguousBankReconciliation
		}
	}
	if bestKey == "" {
		return nil, decimal.Zero, ErrUnmatchedLine
	}
	if states[bestKey].ambiguous {
		return nil, decimal.Zero, ErrAmbiguousBankReconciliation
	}
	return states[bestKey].indices, bestTotal, nil
}

// Deprecated: use ReconcileContext.
func (l *TreasuryLedger) Reconcile(accountID uuid.UUID, statement []BankItem) (matched []BankItem, pending []BankItem, unrecorded []BankItem, err error) {
	return l.ReconcileContext(context.Background(), accountID, statement)
}

func (l *TreasuryLedger) ReconcileContext(ctx context.Context, accountID uuid.UUID, statement []BankItem) (matched []BankItem, pending []BankItem, unrecorded []BankItem, err error) {
	return l.ReconcileWithToleranceContext(ctx, accountID, statement, 0)
}

// Deprecated: use ReconcileWithToleranceContext.
func (l *TreasuryLedger) ReconcileWithTolerance(accountID uuid.UUID, statement []BankItem, toleranceDays int) (matched []BankItem, pending []BankItem, unrecorded []BankItem, err error) {
	return l.ReconcileWithToleranceContext(context.Background(), accountID, statement, toleranceDays)
}

func (l *TreasuryLedger) ReconcileWithToleranceContext(ctx context.Context, accountID uuid.UUID, statement []BankItem, toleranceDays int) (matched []BankItem, pending []BankItem, unrecorded []BankItem, err error) {
	if ctx == nil {
		return nil, nil, nil, ErrInvalidOpenLine
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, err
	}
	if l.companyID != uuid.Nil {
		for _, item := range statement {
			if item.CompanyID != uuid.Nil && item.CompanyID != l.companyID {
				return nil, nil, nil, ErrCompanyMismatch
			}
		}
	}
	ledger, err := l.listBankItems(ctx, accountID.String())
	if err != nil {
		return nil, nil, nil, err
	}
	statementMatched := make([]bool, len(statement))
	expectedMatched := make([]BankItem, 0)
	updatedMatched := make([]BankItem, 0)
	for _, ledgerItem := range ledger {
		found := false
		for i, statementItem := range statement {
			if statementMatched[i] {
				continue
			}
			if ledgerItem.Reference == statementItem.Reference &&
				ledgerItem.Amount.Equal(statementItem.Amount) &&
				withinValueDateTolerance(ledgerItem.Date, statementItem.Date, toleranceDays) {
				statementMatched[i] = true
				expectedMatched = append(expectedMatched, ledgerItem)
				ledgerItem.Status = BankItemMatched
				updatedMatched = append(updatedMatched, ledgerItem)
				matched = append(matched, ledgerItem)
				found = true
				break
			}
		}
		if !found && ledgerItem.Status != BankItemMatched {
			pending = append(pending, ledgerItem)
		}
	}
	if len(expectedMatched) != 0 {
		if err := l.repo.CompareAndSaveBankItems(ctx, expectedMatched, updatedMatched); err != nil {
			return nil, nil, nil, err
		}
	}
	for i, statementItem := range statement {
		if !statementMatched[i] {
			unrecorded = append(unrecorded, statementItem)
		}
	}
	return matched, pending, unrecorded, nil
}

func (l *TreasuryLedger) listBankItems(ctx context.Context, accountID string) ([]BankItem, error) {
	if l.companyID == uuid.Nil {
		return l.repo.ListBankItems(ctx, accountID)
	}
	return l.repo.ListBankItemsForCompany(ctx, l.companyID, accountID)
}

func sameDay(a, b time.Time) bool {
	return a.UTC().Format("2006-01-02") == b.UTC().Format("2006-01-02")
}

func withinValueDateTolerance(a, b time.Time, days int) bool {
	if days < 0 {
		days = 0
	} else if days > 5 {
		days = 5
	}
	dateA := time.Date(a.UTC().Year(), a.UTC().Month(), a.UTC().Day(), 0, 0, 0, 0, time.UTC)
	dateB := time.Date(b.UTC().Year(), b.UTC().Month(), b.UTC().Day(), 0, 0, 0, 0, time.UTC)
	delta := dateA.Sub(dateB)
	if delta < 0 {
		delta = -delta
	}
	return delta <= time.Duration(days)*24*time.Hour
}

// Deprecated: use ProcessBankFeeAtomicContext.
func (l *TreasuryLedger) ProcessBankFeeAtomic(item BankItem, params BankFeeParams) (BankItem, posting.JournalEntry, error) {
	return l.ProcessBankFeeAtomicContext(context.Background(), item, params)
}

func (l *TreasuryLedger) ProcessBankFeeAtomicContext(ctx context.Context, item BankItem, params BankFeeParams) (BankItem, posting.JournalEntry, error) {
	if ctx == nil {
		return BankItem{}, posting.JournalEntry{}, ErrInvalidOpenLine
	}
	if err := ctx.Err(); err != nil {
		return BankItem{}, posting.JournalEntry{}, err
	}
	if item.ID == uuid.Nil || item.CompanyID == uuid.Nil || item.Amount.Currency() == "" ||
		item.Amount.IsNegative() || item.Amount.Amount().IsZero() {
		return BankItem{}, posting.JournalEntry{}, ErrInvalidAmount
	}
	if params.JournalID == uuid.Nil || params.FeeAccountID == uuid.Nil || params.BankAccountID == uuid.Nil {
		return BankItem{}, posting.JournalEntry{}, ErrInvalidOpenLine
	}
	if l.companyID != uuid.Nil && item.CompanyID != l.companyID {
		return BankItem{}, posting.JournalEntry{}, ErrCompanyMismatch
	}
	expected := item
	updated := item
	updated.Status = BankItemMatched

	date := params.Date
	if date.IsZero() {
		date = item.Date
	}
	zero, _ := financialtypes.Zero(item.Amount.Currency())
	lines := []posting.JournalEntryLine{
		{CompanyID: item.CompanyID, ID: uuid.New(), AccountID: params.FeeAccountID, Debit: item.Amount, Credit: zero},
		{CompanyID: item.CompanyID, ID: uuid.New(), AccountID: params.BankAccountID, Credit: item.Amount, Debit: zero},
	}
	ref := item.Reference
	if ref == "" {
		ref = params.Description
	}
	draft := posting.JournalEntry{
		CompanyID:         item.CompanyID,
		ID:                uuid.New(),
		JournalID:         params.JournalID,
		EntryDate:         date,
		Date:              date,
		Status:            posting.EntryDraft,
		DocumentReference: ref,
		Reference:         ref,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
		Lines:             lines,
	}
	if err := draft.Validate(); err != nil {
		return BankItem{}, posting.JournalEntry{}, err
	}
	if err := l.repo.CompareAndSaveBankItemsAndDraft(ctx, []BankItem{expected}, []BankItem{updated}, draft); err != nil {
		return BankItem{}, posting.JournalEntry{}, err
	}
	return updated, draft, nil
}
