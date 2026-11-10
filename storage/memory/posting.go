package memory

import (
	"context"
	"reflect"
	"sort"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/subledgers"
	financialtypes "accounting_engine/internal/types"

	"github.com/google/uuid"
)

func cloneEntry(entry posting.JournalEntry) posting.JournalEntry {
	entry.Lines = append([]posting.JournalEntryLine(nil), entry.Lines...)
	return entry
}

func cloneFixedAsset(asset subledgers.FixedAsset) subledgers.FixedAsset {
	asset.AppliedEntryIDs = append([]uuid.UUID(nil), asset.AppliedEntryIDs...)
	asset.Components = append([]subledgers.AssetComponent(nil), asset.Components...)
	if asset.DisposedAt != nil {
		disposedAt := *asset.DisposedAt
		asset.DisposedAt = &disposedAt
	}
	return asset
}

func (d *Driver) SaveDraft(entry posting.JournalEntry) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if entry.CompanyID != uuid.Nil {
		if entry.ID == uuid.Nil || entry.JournalID == uuid.Nil {
			return posting.ErrInvalidEntry
		}
		for i := range entry.Lines {
			if entry.Lines[i].CompanyID != uuid.Nil && entry.Lines[i].CompanyID != entry.CompanyID {
				return posting.ErrInvalidEntry
			}
			account, ok := d.accounts[entry.Lines[i].AccountID]
			if !ok || account.CompanyID != entry.CompanyID {
				return masterdata.ErrAccountNotFound
			}
			entry.Lines[i].CompanyID = entry.CompanyID
		}
	}
	if current, ok := d.entries[entry.ID]; ok && current.Status != posting.EntryDraft {
		return financialtypes.ErrCannotModifyPostedMove
	}
	d.entries[entry.ID] = cloneEntry(entry)
	return nil
}

func (d *Driver) GetEntryByID(id uuid.UUID) (posting.JournalEntry, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	entry, ok := d.entries[id]
	if !ok {
		return posting.JournalEntry{}, posting.ErrEntryNotFound
	}
	return cloneEntry(entry), nil
}

func (d *Driver) GetEntryByIDForCompany(ctx context.Context, companyID, entryID uuid.UUID) (posting.JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return posting.JournalEntry{}, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	entry, ok := d.entries[entryID]
	if !ok || entry.CompanyID != companyID {
		return posting.JournalEntry{}, posting.ErrEntryNotFound
	}
	return cloneEntry(entry), nil
}

func (d *Driver) GetEntryByIDForCompanyContext(ctx context.Context, companyID, entryID uuid.UUID) (posting.JournalEntry, error) {
	return d.GetEntryByIDForCompany(ctx, companyID, entryID)
}

func (d *Driver) PostAtomic(entry posting.JournalEntry) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	current, exists := d.entries[entry.ID]
	if entry.Status == posting.EntryPosted {
		if !exists {
			return posting.ErrEntryNotFound
		}
		if current.Status != posting.EntryDraft {
			return posting.ErrEntryNotDraft
		}
		if !posting.DraftContentMatches(current, entry) {
			return financialtypes.ErrCannotModifyPostedMove
		}
		if err := d.updateAccountBalancesLocked(entry); err != nil {
			return err
		}
	}
	if current, ok := d.entries[entry.ID]; ok && current.Status == posting.EntryPosted && !reflect.DeepEqual(current.Lines, entry.Lines) {
		return financialtypes.ErrCannotModifyPostedMove
	}
	d.entries[entry.ID] = cloneEntry(entry)
	return nil
}

func (d *Driver) PostAndSequence(entry posting.JournalEntry) (posting.JournalEntry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	current, ok := d.entries[entry.ID]
	if !ok {
		return posting.JournalEntry{}, posting.ErrEntryNotFound
	}
	if current.Status != posting.EntryDraft {
		return posting.JournalEntry{}, posting.ErrEntryNotDraft
	}
	if !posting.DraftContentMatches(current, entry) {
		return posting.JournalEntry{}, financialtypes.ErrCannotModifyPostedMove
	}
	if err := d.updateAccountBalancesLocked(entry); err != nil {
		return posting.JournalEntry{}, err
	}
	key := sequenceKey(entry.CompanyID, entry.JournalID)
	d.journalSeq[key]++
	entry.SequenceNumber = d.journalSeq[key]
	d.entries[entry.ID] = cloneEntry(entry)
	return cloneEntry(entry), nil
}

func (d *Driver) PostAndSequenceWithOpenLine(entry posting.JournalEntry, attachment posting.OpenLineAttachment) (posting.JournalEntry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	current, ok := d.entries[entry.ID]
	if !ok {
		return posting.JournalEntry{}, posting.ErrEntryNotFound
	}
	if current.Status != posting.EntryDraft || entry.Status != posting.EntryPosted {
		return posting.JournalEntry{}, posting.ErrEntryNotDraft
	}
	if !posting.DraftContentMatches(current, entry) {
		return posting.JournalEntry{}, financialtypes.ErrCannotModifyPostedMove
	}
	account, ok := d.accounts[attachment.ControlAccountID]
	if !ok || account.CompanyID != entry.CompanyID {
		return posting.JournalEntry{}, masterdata.ErrAccountNotFound
	}
	if _, exists := d.lines[attachment.ID]; exists {
		return posting.JournalEntry{}, subledgers.ErrAlreadyExists
	}
	partnerID := attachment.PartnerID
	postingDate := attachment.PostingDate
	if postingDate.IsZero() {
		postingDate = entry.EntryDate
		if postingDate.IsZero() {
			postingDate = entry.Date
		}
	}
	line := subledgers.OpenLine{
		CompanyID: attachment.CompanyID, ID: attachment.ID, PartnerID: &partnerID,
		ControlAccountID: attachment.ControlAccountID, JournalEntryID: attachment.JournalEntryID,
		Kind: subledgers.LineKind(attachment.Kind), OriginalAmount: attachment.Amount,
		ResidualAmount: attachment.Amount, FunctionalOriginalAmount: attachment.FunctionalAmount,
		FunctionalResidualAmount: attachment.FunctionalAmount, PostingDate: postingDate,
		DocumentDate: attachment.DocumentDate,
		DueDate:      attachment.DueDate, Reference: attachment.Reference,
	}
	if err := subledgers.ValidatePostedOpenLine(line, account, entry); err != nil {
		return posting.JournalEntry{}, err
	}
	if err := d.updateAccountBalancesLocked(entry); err != nil {
		return posting.JournalEntry{}, err
	}
	key := sequenceKey(entry.CompanyID, entry.JournalID)
	d.journalSeq[key]++
	entry.SequenceNumber = d.journalSeq[key]
	d.entries[entry.ID] = cloneEntry(entry)
	d.lines[line.ID] = line
	return cloneEntry(entry), nil
}

func (d *Driver) PostReversalAtomic(original, reversal posting.JournalEntry) (posting.JournalEntry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	currentOriginal, ok := d.entries[original.ID]
	if !ok {
		return posting.JournalEntry{}, posting.ErrEntryNotFound
	}
	if currentOriginal.Status != posting.EntryPosted || currentOriginal.ReversedEntryID != nil {
		return posting.JournalEntry{}, posting.ErrEntryAlreadyReversed
	}
	currentReversal, ok := d.entries[reversal.ID]
	if !ok || currentReversal.Status != posting.EntryDraft {
		return posting.JournalEntry{}, posting.ErrEntryNotDraft
	}
	if err := d.updateAccountBalancesLocked(reversal); err != nil {
		return posting.JournalEntry{}, err
	}
	key := sequenceKey(reversal.CompanyID, reversal.JournalID)
	d.journalSeq[key]++
	reversal.SequenceNumber = d.journalSeq[key]
	reversal = cloneEntry(reversal)
	reversal.Status = posting.EntryPosted
	reversalID := reversal.ID
	currentOriginal.ReversedEntryID = &reversalID
	currentOriginal.Status = posting.EntryReversed
	currentOriginal.UpdatedAt = time.Now().UTC()
	d.entries[reversal.ID] = reversal
	d.entries[original.ID] = cloneEntry(currentOriginal)
	return cloneEntry(reversal), nil
}

func (d *Driver) SaveDraftContext(ctx context.Context, entry posting.JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return d.SaveDraft(entry)
}

func (d *Driver) PostAtomicContext(ctx context.Context, entry posting.JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return d.PostAtomic(entry)
}

func (d *Driver) PostAndSequenceContext(ctx context.Context, entry posting.JournalEntry) (posting.JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return posting.JournalEntry{}, err
	}
	return d.PostAndSequence(entry)
}

func (d *Driver) PostAndSequenceWithOpenLineContext(ctx context.Context, entry posting.JournalEntry, attachment posting.OpenLineAttachment) (posting.JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return posting.JournalEntry{}, err
	}
	return d.PostAndSequenceWithOpenLine(entry, attachment)
}

func (d *Driver) PostReversalAtomicContext(ctx context.Context, original, reversal posting.JournalEntry) (posting.JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return posting.JournalEntry{}, err
	}
	return d.PostReversalAtomic(original, reversal)
}

func (d *Driver) SaveOpenLineFromAttachment(ctx context.Context, attachment posting.OpenLineAttachment, entry posting.JournalEntry) error {
	account, ok := d.accounts[attachment.ControlAccountID]
	if !ok || account.CompanyID != entry.CompanyID {
		return masterdata.ErrAccountNotFound
	}
	if _, exists := d.lines[attachment.ID]; exists {
		return subledgers.ErrAlreadyExists
	}
	partnerID := attachment.PartnerID
	postingDate := attachment.PostingDate
	if postingDate.IsZero() {
		postingDate = entry.EntryDate
		if postingDate.IsZero() {
			postingDate = entry.Date
		}
	}
	line := subledgers.OpenLine{
		CompanyID: attachment.CompanyID, ID: attachment.ID, PartnerID: &partnerID,
		ControlAccountID: attachment.ControlAccountID, JournalEntryID: attachment.JournalEntryID,
		Kind: subledgers.LineKind(attachment.Kind), OriginalAmount: attachment.Amount,
		ResidualAmount: attachment.Amount, FunctionalOriginalAmount: attachment.FunctionalAmount,
		FunctionalResidualAmount: attachment.FunctionalAmount, PostingDate: postingDate,
		DocumentDate: attachment.DocumentDate,
		DueDate:      attachment.DueDate, Reference: attachment.Reference,
	}
	if err := subledgers.ValidatePostedOpenLine(line, account, entry); err != nil {
		return err
	}
	d.lines[line.ID] = line
	return nil
}

type memoryRow struct{}

func (r *memoryRow) Scan(dest ...any) error {
	return nil
}

type memoryTxAdapter struct {
	state *memoryMutationState
}

type memoryMutationState struct {
	driver *Driver
	assets map[uuid.UUID]subledgers.FixedAsset
	lines  map[uuid.UUID]subledgers.OpenLine
}

func (s *memoryMutationState) SaveFixedAsset(ctx context.Context, asset subledgers.FixedAsset) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if existing, ok := s.driver.assets[asset.ID]; ok && existing.CompanyID != asset.CompanyID {
		return subledgers.ErrCompanyMismatch
	}
	s.assets[asset.ID] = cloneFixedAsset(asset)
	return nil
}

func (s *memoryMutationState) ApplyFixedAssetOperation(
	ctx context.Context,
	expected, updated subledgers.FixedAsset,
	entry posting.JournalEntry,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := subledgers.ValidateFixedAssetOperationMutation(expected, updated, entry); err != nil {
		return err
	}
	current, ok := s.assets[expected.ID]
	if !ok {
		current, ok = s.driver.assets[expected.ID]
	}
	if !ok || !reflect.DeepEqual(current, expected) {
		return subledgers.ErrConcurrentAssetUpdate
	}
	s.assets[updated.ID] = cloneFixedAsset(updated)
	return nil
}

func (s *memoryMutationState) SaveOpenLine(ctx context.Context, line subledgers.OpenLine) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if existing, ok := s.driver.lines[line.ID]; ok && existing.CompanyID != line.CompanyID {
		return subledgers.ErrCompanyMismatch
	}
	line.MatchIDs = append([]uuid.UUID(nil), line.MatchIDs...)
	line.Matches = append([]subledgers.OpenLineMatch(nil), line.Matches...)
	s.lines[line.ID] = line
	return nil
}

func (s *memoryMutationState) SaveOpenLineFromAttachment(
	ctx context.Context,
	attachment posting.OpenLineAttachment,
	entry posting.JournalEntry,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	account, ok := s.driver.accounts[attachment.ControlAccountID]
	if !ok || account.CompanyID != entry.CompanyID {
		return masterdata.ErrAccountNotFound
	}
	if _, exists := s.driver.lines[attachment.ID]; exists {
		return subledgers.ErrAlreadyExists
	}
	if _, exists := s.lines[attachment.ID]; exists {
		return subledgers.ErrAlreadyExists
	}
	partnerID := attachment.PartnerID
	postingDate := attachment.PostingDate
	if postingDate.IsZero() {
		postingDate = entry.EntryDate
		if postingDate.IsZero() {
			postingDate = entry.Date
		}
	}
	line := subledgers.OpenLine{
		CompanyID: attachment.CompanyID, ID: attachment.ID, PartnerID: &partnerID,
		ControlAccountID: attachment.ControlAccountID, JournalEntryID: attachment.JournalEntryID,
		Kind: subledgers.LineKind(attachment.Kind), OriginalAmount: attachment.Amount,
		ResidualAmount: attachment.Amount, FunctionalOriginalAmount: attachment.FunctionalAmount,
		FunctionalResidualAmount: attachment.FunctionalAmount, PostingDate: postingDate,
		DocumentDate: attachment.DocumentDate,
		DueDate:      attachment.DueDate, Reference: attachment.Reference,
	}
	if err := subledgers.ValidatePostedOpenLine(line, account, entry); err != nil {
		return err
	}
	s.lines[line.ID] = line
	return nil
}

func (a *memoryTxAdapter) Exec(ctx context.Context, sql string, args ...any) error {
	return posting.ErrUnsupportedMutationBackend
}

func (a *memoryTxAdapter) QueryRow(ctx context.Context, sql string, args ...any) posting.Row {
	return &memoryRow{}
}

func (a *memoryTxAdapter) CustomMutation(ctx context.Context, target string, fn func(state any) error) error {
	if target != "memory" {
		return posting.ErrUnsupportedMutationBackend
	}
	return fn(a.state)
}

func (d *Driver) PostWithAttachments(ctx context.Context, entry *posting.JournalEntry, attachments []posting.LedgerAttachment) error {
	return d.PostWithAttachmentsContext(ctx, entry, attachments)
}

func (d *Driver) PostWithAttachmentsContext(ctx context.Context, entry *posting.JournalEntry, attachments []posting.LedgerAttachment) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	current, ok := d.entries[entry.ID]
	if !ok {
		return posting.ErrEntryNotFound
	}
	if current.Status != posting.EntryDraft || entry.Status != posting.EntryPosted {
		return posting.ErrEntryNotDraft
	}
	if !posting.DraftContentMatches(current, *entry) {
		return financialtypes.ErrCannotModifyPostedMove
	}

	mutationState := &memoryMutationState{
		driver: d,
		assets: make(map[uuid.UUID]subledgers.FixedAsset),
		lines:  make(map[uuid.UUID]subledgers.OpenLine),
	}
	memAdapter := &memoryTxAdapter{state: mutationState}
	for _, att := range attachments {
		if att != nil {
			if err := att.ExecuteAtomicMutation(ctx, memAdapter, *entry); err != nil {
				return err
			}
		}
	}

	if err := d.updateAccountBalancesLocked(*entry); err != nil {
		return err
	}
	for id, asset := range mutationState.assets {
		d.assets[id] = cloneFixedAsset(asset)
	}
	for id, line := range mutationState.lines {
		d.lines[id] = line
	}

	key := sequenceKey(entry.CompanyID, entry.JournalID)
	d.journalSeq[key]++
	entry.SequenceNumber = d.journalSeq[key]
	d.entries[entry.ID] = cloneEntry(*entry)
	return nil
}

func (d *Driver) GetNextSequence(id uuid.UUID) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := sequenceKey(uuid.Nil, id)
	d.journalSeq[key]++
	return d.journalSeq[key], nil
}

func (d *Driver) GetNextSequenceForCompanyContext(ctx context.Context, companyID, journalID uuid.UUID) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if companyID == uuid.Nil || journalID == uuid.Nil {
		return 0, masterdata.ErrCompanyScopeRequired
	}
	return d.GetNextSequenceWithLock(ctx, companyID, journalID)
}

func sequenceKey(companyID, journalID uuid.UUID) string {
	return companyID.String() + "\x00" + journalID.String()
}

func (d *Driver) PostBulk(ctx context.Context, entries []posting.JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	pending := make(map[accountBalanceKey]accountBalance)
	adjustingPeriods := make(map[companyPeriodKey]bool)
	seenEntries := make(map[uuid.UUID]struct{}, len(entries))
	for _, entry := range entries {
		if _, duplicate := seenEntries[entry.ID]; duplicate {
			return posting.ErrEntryNotDraft
		}
		seenEntries[entry.ID] = struct{}{}
		current, ok := d.entries[entry.ID]
		if !ok {
			return posting.ErrEntryNotFound
		}
		if current.Status != posting.EntryDraft {
			return posting.ErrEntryNotDraft
		}
		if !posting.DraftContentMatches(current, entry) {
			return financialtypes.ErrCannotModifyPostedMove
		}
		deltas, err := d.accountBalanceDeltasLocked(entry)
		if err != nil {
			return err
		}
		for key, delta := range deltas {
			aggregate := pending[key]
			aggregate.periodDebit = aggregate.periodDebit.Add(delta.periodDebit)
			aggregate.periodCredit = aggregate.periodCredit.Add(delta.periodCredit)
			pending[key] = aggregate
		}
		if entry.Classification == posting.EntryClassAdjusting && entry.CompanyID != uuid.Nil {
			adjustingPeriods[companyPeriodKey{companyID: entry.CompanyID, periodID: entry.PeriodID}] = true
		}
	}
	for key, delta := range pending {
		balance := d.balances[key]
		balance.periodDebit = balance.periodDebit.Add(delta.periodDebit)
		balance.periodCredit = balance.periodCredit.Add(delta.periodCredit)
		d.balances[key] = balance
	}
	for key := range adjustingPeriods {
		d.adjustingPeriods[key] = true
	}
	// Sequence numbers are allocated only after every entry in the batch has
	// been validated, mirroring the atomic range reservation semantics of the
	// PostgreSQL driver: a failed bulk post consumes no sequence numbers.
	for i := range entries {
		if entries[i].Status != posting.EntryPosted {
			continue
		}
		key := sequenceKey(entries[i].CompanyID, entries[i].JournalID)
		d.journalSeq[key]++
		entries[i].SequenceNumber = d.journalSeq[key]
	}
	for _, entry := range entries {
		d.entries[entry.ID] = cloneEntry(entry)
	}
	return nil
}

func (d *Driver) updateAccountBalancesLocked(entry posting.JournalEntry) error {
	deltas, err := d.accountBalanceDeltasLocked(entry)
	if err != nil {
		return err
	}
	for key, delta := range deltas {
		balance := d.balances[key]
		balance.periodDebit = balance.periodDebit.Add(delta.periodDebit)
		balance.periodCredit = balance.periodCredit.Add(delta.periodCredit)
		d.balances[key] = balance
	}
	if entry.Classification == posting.EntryClassAdjusting && entry.CompanyID != uuid.Nil {
		d.adjustingPeriods[companyPeriodKey{companyID: entry.CompanyID, periodID: entry.PeriodID}] = true
	}
	return nil
}

func (d *Driver) accountBalanceDeltasLocked(entry posting.JournalEntry) (map[accountBalanceKey]accountBalance, error) {
	if entry.Status != posting.EntryPosted {
		return nil, posting.ErrInvalidEntryState
	}
	if entry.CompanyID == uuid.Nil {
		return map[accountBalanceKey]accountBalance{}, nil
	}
	if entry.PeriodID == uuid.Nil {
		return nil, masterdata.ErrFiscalPeriodNotFound
	}
	period, ok := d.periods[entry.PeriodID]
	if !ok || period.CompanyID != entry.CompanyID {
		return nil, masterdata.ErrFiscalPeriodNotFound
	}
	company, ok := d.companies[entry.CompanyID]
	if !ok {
		return nil, masterdata.ErrInvalidCompany
	}
	deltas := make(map[accountBalanceKey]accountBalance, len(entry.Lines))
	for _, line := range entry.Lines {
		account, ok := d.accounts[line.AccountID]
		if !ok || account.CompanyID != entry.CompanyID {
			return nil, masterdata.ErrAccountNotFound
		}
		debit, credit, currency := posting.LineFunctionalAmounts(line)
		if currency != "" && currency != company.FunctionalCurrency {
			return nil, posting.ErrFunctionalCurrencyMismatch
		}
		key := accountBalanceKey{
			companyID: entry.CompanyID,
			accountID: line.AccountID,
			periodID:  entry.PeriodID,
			currency:  company.FunctionalCurrency,
		}
		delta := deltas[key]
		delta.periodDebit = delta.periodDebit.Add(debit)
		delta.periodCredit = delta.periodCredit.Add(credit)
		deltas[key] = delta
	}
	return deltas, nil
}

func (d *Driver) CarryForwardBalancesForCompanyContext(
	ctx context.Context,
	companyID, closedYearID, newPeriodID uuid.UUID,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	closedYear, ok := d.years[closedYearID]
	if !ok || closedYear.CompanyID != companyID {
		return masterdata.ErrFiscalYearNotFound
	}
	if closedYear.State != masterdata.FiscalYearHardClosed {
		return masterdata.ErrInvalidFiscalYearTransition
	}
	destination, ok := d.periods[newPeriodID]
	if !ok || destination.CompanyID != companyID || destination.Number != 1 {
		return masterdata.ErrFiscalPeriodNotFound
	}
	newYear, ok := d.years[destination.FiscalYearID]
	if !ok || newYear.CompanyID != companyID || !newYear.StartDate.After(closedYear.EndDate) {
		return masterdata.ErrFiscalPeriodNotFound
	}
	var lastPeriod masterdata.FiscalPeriod
	foundLastPeriod := false
	for _, period := range d.periods {
		if period.CompanyID == companyID && period.FiscalYearID == closedYearID &&
			(!foundLastPeriod || period.Number > lastPeriod.Number) {
			lastPeriod = period
			foundLastPeriod = true
		}
	}
	if !foundLastPeriod {
		return masterdata.ErrFiscalPeriodNotFound
	}
	type balanceKey struct {
		accountID uuid.UUID
		currency  string
	}
	sourceBalances := make(map[balanceKey]accountBalance)
	periodNumbers := make(map[uuid.UUID]int, len(d.periods))
	for _, period := range d.periods {
		if period.CompanyID == companyID && period.FiscalYearID == closedYearID {
			periodNumbers[period.ID] = period.Number
		}
	}
	for key, source := range d.balances {
		periodNumber, inClosedYear := periodNumbers[key.periodID]
		if key.companyID != companyID || !inClosedYear || periodNumber > lastPeriod.Number {
			continue
		}
		account, ok := d.accounts[key.accountID]
		if !ok {
			return masterdata.ErrAccountNotFound
		}
		switch account.AccountType {
		case masterdata.AccountTypeAsset, masterdata.AccountTypeLiability, masterdata.AccountTypeEquity:
		default:
			continue
		}
		sourceKey := balanceKey{accountID: key.accountID, currency: key.currency}
		balance := sourceBalances[sourceKey]
		balance.beginningDebit = balance.beginningDebit.Add(source.beginningDebit)
		balance.beginningCredit = balance.beginningCredit.Add(source.beginningCredit)
		balance.periodDebit = balance.periodDebit.Add(source.periodDebit)
		balance.periodCredit = balance.periodCredit.Add(source.periodCredit)
		sourceBalances[sourceKey] = balance
	}
	for sourceKey, source := range sourceBalances {
		destinationKey := accountBalanceKey{
			companyID: companyID,
			accountID: sourceKey.accountID,
			periodID:  newPeriodID,
			currency:  sourceKey.currency,
		}
		balance := d.balances[destinationKey]
		balance.beginningDebit = source.beginningDebit.Add(source.periodDebit)
		balance.beginningCredit = source.beginningCredit.Add(source.periodCredit)
		d.balances[destinationKey] = balance
	}
	return nil
}

func (d *Driver) ListPostedEntries(ctx context.Context, start, end time.Time) ([]posting.JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return d.ListEntries(ctx, start, end)
}

func (d *Driver) ListEntries(ctx context.Context, start, end time.Time) ([]posting.JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	entries := make([]posting.JournalEntry, 0, len(d.entries))
	for _, entry := range d.entries {
		date := entry.EntryDate
		if date.IsZero() {
			date = entry.Date
		}
		if entry.Status == posting.EntryPosted && (start.IsZero() || !date.Before(start)) && (end.IsZero() || !date.After(end)) {
			entries = append(entries, cloneEntry(entry))
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].EntryDate.Before(entries[j].EntryDate) })
	return entries, nil
}

func (d *Driver) ListEntriesForCompany(companyID uuid.UUID, start, end time.Time) ([]posting.JournalEntry, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	entries := make([]posting.JournalEntry, 0, len(d.entries))
	for _, entry := range d.entries {
		date := entry.EntryDate
		if date.IsZero() {
			date = entry.Date
		}

		if entry.CompanyID == companyID && entry.Status == posting.EntryPosted &&
			(start.IsZero() || !date.Before(start)) && (end.IsZero() || !date.After(end)) {
			entries = append(entries, cloneEntry(entry))
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].EntryDate.Before(entries[j].EntryDate) })
	return entries, nil
}

func (d *Driver) ListEntriesForCompanyContext(ctx context.Context, companyID uuid.UUID, start, end time.Time) ([]posting.JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return d.ListEntriesForCompany(companyID, start, end)
}

func (d *Driver) ReadAccountPeriodBalancesForCompanyContext(
	ctx context.Context,
	companyID uuid.UUID,
	start, end time.Time,
	currency string,
	includeAdjusting bool,
) (map[uuid.UUID]posting.AccountPeriodBalance, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if companyID == uuid.Nil || end.IsZero() || (!start.IsZero() && end.Before(start)) {
		return nil, false, nil
	}

	d.mu.RLock()
	defer d.mu.RUnlock()
	company, ok := d.companies[companyID]
	if !ok {
		return nil, false, nil
	}
	if company.FunctionalCurrency != currency {
		return nil, false, nil
	}
	var year masterdata.FiscalYear
	foundYear := false
	for _, candidate := range d.years {
		if candidate.CompanyID == companyID &&
			!end.Before(candidate.StartDate) && !end.After(candidate.EndDate) {
			year, foundYear = candidate, true
			break
		}
	}
	if !foundYear {
		return nil, false, nil
	}
	if !start.IsZero() && start.Before(year.StartDate) {
		return nil, false, nil
	}

	periods := append([]masterdata.FiscalPeriod(nil), year.Periods...)
	if len(periods) == 0 {
		for _, period := range d.periods {
			if period.CompanyID == companyID && period.FiscalYearID == year.ID {
				periods = append(periods, period)
			}
		}
	}
	sort.Slice(periods, func(i, j int) bool {
		if periods[i].StartDate.Equal(periods[j].StartDate) {
			return periods[i].Number < periods[j].Number
		}
		return periods[i].StartDate.Before(periods[j].StartDate)
	})
	if len(periods) == 0 {
		return nil, false, nil
	}
	periodIndexes := make(map[uuid.UUID]int, len(periods))
	for i, period := range periods {
		periodIndexes[period.ID] = i
	}
	rangeStart := start
	if rangeStart.IsZero() {
		rangeStart = year.StartDate
	}
	first, last := -1, -1
	for i, period := range periods {
		if period.StartDate.Before(rangeStart) && !period.EndDate.Before(rangeStart) {
			return nil, false, nil
		}
		if !period.StartDate.After(end) && period.EndDate.After(end) {
			return nil, false, nil
		}
		if !period.StartDate.Before(rangeStart) && !period.EndDate.After(end) {
			if first == -1 {
				first = i
			}
			last = i
		}
	}
	if first == -1 || last == -1 {
		return nil, false, nil
	}
	if !includeAdjusting {
		for i := 0; i <= last; i++ {
			if d.adjustingPeriods[companyPeriodKey{companyID: companyID, periodID: periods[i].ID}] {
				return nil, false, nil
			}
		}
	}

	balances := make(map[uuid.UUID]posting.AccountPeriodBalance, len(d.balances))
	for key, stored := range d.balances {
		if key.companyID != companyID || key.currency != currency {
			continue
		}
		periodIndex, ok := periodIndexes[key.periodID]
		if !ok || periodIndex > last {
			continue
		}
		balance := balances[key.accountID]
		balance.AccountID = key.accountID
		if !stored.beginningDebit.IsZero() {
			balance.BeginningDebit = balance.BeginningDebit.Add(stored.beginningDebit)
		}
		if !stored.beginningCredit.IsZero() {
			balance.BeginningCredit = balance.BeginningCredit.Add(stored.beginningCredit)
		}
		if periodIndex < first {
			if !stored.periodDebit.IsZero() {
				balance.BeginningDebit = balance.BeginningDebit.Add(stored.periodDebit)
			}
			if !stored.periodCredit.IsZero() {
				balance.BeginningCredit = balance.BeginningCredit.Add(stored.periodCredit)
			}
		} else {
			if balance.PeriodDebit.IsZero() {
				balance.PeriodDebit = stored.periodDebit
			} else if !stored.periodDebit.IsZero() {
				balance.PeriodDebit = balance.PeriodDebit.Add(stored.periodDebit)
			}
			if balance.PeriodCredit.IsZero() {
				balance.PeriodCredit = stored.periodCredit
			} else if !stored.periodCredit.IsZero() {
				balance.PeriodCredit = balance.PeriodCredit.Add(stored.periodCredit)
			}
		}
		balance.EndingDebit = balance.BeginningDebit
		if !balance.PeriodDebit.IsZero() {
			if balance.EndingDebit.IsZero() {
				balance.EndingDebit = balance.PeriodDebit
			} else {
				balance.EndingDebit = balance.EndingDebit.Add(balance.PeriodDebit)
			}
		}
		balance.EndingCredit = balance.BeginningCredit
		if !balance.PeriodCredit.IsZero() {
			if balance.EndingCredit.IsZero() {
				balance.EndingCredit = balance.PeriodCredit
			} else {
				balance.EndingCredit = balance.EndingCredit.Add(balance.PeriodCredit)
			}
		}
		balances[key.accountID] = balance
	}
	return balances, true, nil
}

func (d *Driver) GetAccountBalance(ctx context.Context, companyID, accountID uuid.UUID) (financialtypes.Money, error) {
	if err := ctx.Err(); err != nil {
		return financialtypes.Money{}, err
	}
	d.mu.RLock()
	account, exists := d.accounts[accountID]
	company, companyExists := d.companies[companyID]
	entries := make([]posting.JournalEntry, 0)
	for _, entry := range d.entries {
		if entry.CompanyID == companyID && entry.Status == posting.EntryPosted {
			entries = append(entries, cloneEntry(entry))
		}
	}
	d.mu.RUnlock()
	if !exists || account.CompanyID != companyID {
		return financialtypes.Money{}, masterdata.ErrAccountNotFound
	}
	if !companyExists {
		return financialtypes.Money{}, masterdata.ErrInvalidCompany
	}
	return subledgers.ControlAccountBalanceFromEntries(account, entries, company.FunctionalCurrency)
}

func (d *Driver) GetAccountBalanceAsOf(ctx context.Context, companyID, accountID uuid.UUID, asOf time.Time) (financialtypes.Money, error) {
	if asOf.IsZero() {
		return financialtypes.Money{}, subledgers.ErrReconciliationHistoryUnavailable
	}
	if err := ctx.Err(); err != nil {
		return financialtypes.Money{}, err
	}
	d.mu.RLock()
	account, exists := d.accounts[accountID]
	company, companyExists := d.companies[companyID]
	entries := make([]posting.JournalEntry, 0)
	for _, entry := range d.entries {
		entryDate := entry.EntryDate
		if entryDate.IsZero() {
			entryDate = entry.Date
		}
		if entry.CompanyID == companyID && entry.Status == posting.EntryPosted && !entryDate.After(asOf) {
			entries = append(entries, cloneEntry(entry))
		}
	}
	d.mu.RUnlock()
	if !exists || account.CompanyID != companyID {
		return financialtypes.Money{}, masterdata.ErrAccountNotFound
	}
	if !companyExists {
		return financialtypes.Money{}, masterdata.ErrInvalidCompany
	}
	return subledgers.ControlAccountBalanceFromEntries(account, entries, company.FunctionalCurrency)
}
