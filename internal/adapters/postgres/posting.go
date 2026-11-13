package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/subledgers"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (d *Driver) SaveDraft(ctx context.Context, entry posting.JournalEntry) error {
	return d.SaveDraftContext(ctx, entry)
}

func (d *Driver) SaveDraftContext(ctx context.Context, entry posting.JournalEntry) (result error) {
	if entry.Status != posting.EntryDraft {
		return posting.ErrEntryNotDraft
	}
	if entry.PeriodID == uuid.Nil {
		entryDate := entry.EntryDate
		if entryDate.IsZero() {
			entryDate = entry.Date
		}
		period, err := d.GetPeriodByDateForCompany(ctx, entry.CompanyID, entryDate)
		if err != nil {
			return err
		}
		entry.PeriodID = period.ID
	}
	tx, err := d.beginCompanyTx(ctx, entry.CompanyID)
	if err != nil {
		return err
	}
	defer func() {
		if rollbackErr := tx.Rollback(context.WithoutCancel(ctx)); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			result = errors.Join(result, fmt.Errorf("rollback draft transaction: %w", rollbackErr))
		}
	}()
	if err := d.saveEntryTx(ctx, tx, entry); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (d *Driver) GetEntryByID(ctx context.Context, companyID, id uuid.UUID) (posting.JournalEntry, error) {
	var entry posting.JournalEntry
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var data []byte
		err := tx.QueryRow(ctx,
			`SELECT entry FROM account_moves WHERE company_id=$1 AND id=$2`, companyID, id,
		).Scan(&data)
		if errors.Is(err, pgx.ErrNoRows) {
			return posting.ErrEntryNotFound
		}
		if err != nil {
			return err
		}
		return json.Unmarshal(data, &entry)
	})
	return entry, err
}
func (d *Driver) PostAtomic(ctx context.Context, entry posting.JournalEntry) error {
	return d.PostAtomicContext(ctx, entry)
}

func (d *Driver) PostAtomicContext(ctx context.Context, entry posting.JournalEntry) (result error) {
	if entry.SequenceNumber == 0 {
		_, err := d.PostAndSequenceContext(ctx, entry)
		return err
	}
	tx, err := d.beginCompanyTx(ctx, entry.CompanyID)
	if err != nil {
		return err
	}
	defer func() {
		if rollbackErr := tx.Rollback(context.WithoutCancel(ctx)); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			result = errors.Join(result, fmt.Errorf("rollback atomic posting transaction: %w", rollbackErr))
		}
	}()
	if err := d.saveEntryTx(ctx, tx, entry); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (d *Driver) PostAndSequence(ctx context.Context, entry posting.JournalEntry) (posting.JournalEntry, error) {
	return d.PostAndSequenceContext(ctx, entry)
}

func (d *Driver) PostAndSequenceContext(ctx context.Context, entry posting.JournalEntry) (posting.JournalEntry, error) {
	return d.postAndSequenceContext(ctx, entry, nil)
}

func (d *Driver) PostAndSequenceWithOpenLine(ctx context.Context,
	entry posting.JournalEntry,
	attachment posting.OpenLineAttachment,
) (posting.JournalEntry, error) {
	return d.PostAndSequenceWithOpenLineContext(ctx, entry, attachment)
}

func (d *Driver) PostAndSequenceWithOpenLineContext(ctx context.Context, entry posting.JournalEntry, attachment posting.OpenLineAttachment) (posting.JournalEntry, error) {
	return d.postAndSequenceContext(ctx, entry, &attachment)
}

type pgxTxAdapter struct {
	tx pgx.Tx
}

func (a *pgxTxAdapter) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := a.tx.Exec(ctx, sql, args...)
	return err
}

func (a *pgxTxAdapter) QueryRow(ctx context.Context, sql string, args ...any) posting.Row {
	return a.tx.QueryRow(ctx, sql, args...)
}

func (a *pgxTxAdapter) CustomMutation(ctx context.Context, target string, fn func(state any) error) error {
	return posting.ErrUnsupportedMutationBackend
}

func (d *Driver) PostWithAttachments(ctx context.Context, entry *posting.JournalEntry, attachments []posting.LedgerAttachment) error {
	return d.PostWithAttachmentsContext(ctx, entry, attachments)
}

func (d *Driver) PostWithAttachmentsContext(ctx context.Context, entry *posting.JournalEntry, attachments []posting.LedgerAttachment) (resultErr error) {
	tx, err := d.beginCompanyTx(ctx, entry.CompanyID)
	if err != nil {
		return err
	}
	defer func() {
		if rollbackErr := tx.Rollback(context.WithoutCancel(ctx)); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			resultErr = errors.Join(resultErr, fmt.Errorf("rollback posting transaction: %w", rollbackErr))
		}
	}()

	if entry.CompanyID == uuid.Nil || entry.JournalID == uuid.Nil {
		return posting.ErrInvalidEntry
	}

	var sequence int64
	if err := tx.QueryRow(ctx,
		`SELECT current_sequence FROM journals
		 WHERE company_id=$1 AND id=$2 FOR UPDATE`,
		entry.CompanyID, entry.JournalID).Scan(&sequence); err != nil {
		return err
	}

	var status posting.EntryStatus
	var draftData []byte
	if err := tx.QueryRow(ctx,
		`SELECT status, entry FROM account_moves
		 WHERE company_id=$1 AND id=$2 AND journal_id=$3 FOR UPDATE`,
		entry.CompanyID, entry.ID, entry.JournalID).Scan(&status, &draftData); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return posting.ErrEntryNotFound
		}
		return err
	}

	var current posting.JournalEntry
	if err := json.Unmarshal(draftData, &current); err != nil {
		return err
	}
	if status != posting.EntryDraft || current.Status != posting.EntryDraft {
		return posting.ErrEntryNotDraft
	}
	if entry.PeriodID == uuid.Nil {
		entry.PeriodID = current.PeriodID
	}
	if !posting.DraftContentMatches(current, *entry) {
		return financialtypes.ErrCannotModifyPostedMove
	}
	if err := validatePostingPeriodTx(ctx, tx, *entry); err != nil {
		return err
	}

	sequence++
	if _, err := tx.Exec(ctx,
		`UPDATE journals SET current_sequence=$3 WHERE company_id=$1 AND id=$2`,
		entry.CompanyID, entry.JournalID, sequence); err != nil {
		return err
	}

	entry.SequenceNumber = sequence
	if err := d.saveEntryTx(ctx, tx, *entry); err != nil {
		return err
	}

	txAdapter := &pgxTxAdapter{tx: tx}
	for _, att := range attachments {
		if att != nil {
			if err := att.ExecuteAtomicMutation(ctx, txAdapter, *entry); err != nil {
				return fmt.Errorf("attachment %s: %w", att.AttachmentKind(), err)
			}
		}
	}

	return tx.Commit(ctx)
}

func (d *Driver) postAndSequenceContext(ctx context.Context, entry posting.JournalEntry, attachment *posting.OpenLineAttachment) (result posting.JournalEntry, resultErr error) {
	tx, err := d.beginCompanyTx(ctx, entry.CompanyID)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	defer func() {
		if rollbackErr := tx.Rollback(context.WithoutCancel(ctx)); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			resultErr = errors.Join(resultErr, fmt.Errorf("rollback posting transaction: %w", rollbackErr))
		}
	}()

	if entry.CompanyID == uuid.Nil || entry.JournalID == uuid.Nil {
		return posting.JournalEntry{}, posting.ErrInvalidEntry
	}
	var sequence int64
	if err := tx.QueryRow(ctx,
		`SELECT current_sequence FROM journals
		 WHERE company_id=$1 AND id=$2 FOR UPDATE`,
		entry.CompanyID, entry.JournalID).Scan(&sequence); err != nil {
		return posting.JournalEntry{}, err
	}
	var status posting.EntryStatus
	var draftData []byte
	if err := tx.QueryRow(ctx,
		`SELECT status, entry FROM account_moves
		 WHERE company_id=$1 AND id=$2 AND journal_id=$3 FOR UPDATE`,
		entry.CompanyID, entry.ID, entry.JournalID).Scan(&status, &draftData); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return posting.JournalEntry{}, posting.ErrEntryNotFound
		}
		return posting.JournalEntry{}, err
	}
	var current posting.JournalEntry
	if err := json.Unmarshal(draftData, &current); err != nil {
		return posting.JournalEntry{}, err
	}
	if status != posting.EntryDraft || current.Status != posting.EntryDraft {
		return posting.JournalEntry{}, posting.ErrEntryNotDraft
	}
	if entry.PeriodID == uuid.Nil {
		entry.PeriodID = current.PeriodID
	}
	if !posting.DraftContentMatches(current, entry) {
		return posting.JournalEntry{}, financialtypes.ErrCannotModifyPostedMove
	}
	if err := validatePostingPeriodTx(ctx, tx, entry); err != nil {
		return posting.JournalEntry{}, err
	}
	sequence++
	if _, err := tx.Exec(ctx,
		`UPDATE journals SET current_sequence=$3 WHERE company_id=$1 AND id=$2`,
		entry.CompanyID, entry.JournalID, sequence); err != nil {
		return posting.JournalEntry{}, err
	}
	entry.SequenceNumber = sequence
	if err := d.saveEntryTx(ctx, tx, entry); err != nil {
		return posting.JournalEntry{}, err
	}
	if attachment != nil {
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
		account, err := scanAccount(tx.QueryRow(ctx,
			accountSelect+` WHERE company_id=$1 AND id=$2 FOR SHARE`, entry.CompanyID, attachment.ControlAccountID))
		if errors.Is(err, pgx.ErrNoRows) {
			return posting.JournalEntry{}, masterdata.ErrAccountNotFound
		}
		if err != nil {
			return posting.JournalEntry{}, err
		}
		if err := subledgers.ValidatePostedOpenLine(line, account, entry); err != nil {
			return posting.JournalEntry{}, err
		}
		data, err := json.Marshal(line)
		if err != nil {
			return posting.JournalEntry{}, err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO subledger_open_lines(id,company_id,partner_id,kind,line_data) VALUES($1,$2,$3,$4,$5)`,
			line.ID, line.CompanyID, partnerID, line.Kind, data); err != nil {
			return posting.JournalEntry{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return posting.JournalEntry{}, err
	}
	return entry, nil
}

func validatePostingPeriodTx(ctx context.Context, tx pgx.Tx, entry posting.JournalEntry) error {
	entryDate := entry.EntryDate
	if entryDate.IsZero() {
		entryDate = entry.Date
	}
	if entry.PeriodID == uuid.Nil || entryDate.IsZero() {
		return posting.ErrInvalidEntry
	}

	var startDate, endDate time.Time
	var state masterdata.PeriodState
	var periodNumber int
	var periodType masterdata.FiscalPeriodType
	err := tx.QueryRow(ctx, `SELECT start_date,end_date,state,period_number,period_type
		FROM fiscal_periods WHERE company_id=$1 AND id=$2 FOR UPDATE`,
		entry.CompanyID, entry.PeriodID).Scan(&startDate, &endDate, &state, &periodNumber, &periodType)
	if errors.Is(err, pgx.ErrNoRows) {
		return masterdata.ErrFiscalPeriodNotFound
	}
	if err != nil {
		return err
	}
	if state == masterdata.PeriodHardClosed {
		return masterdata.ErrFiscalPeriodClosed
	}
	if state != masterdata.PeriodOpen && state != masterdata.PeriodSoftClosed {
		return masterdata.ErrFiscalPeriodClosed
	}

	entryDate = entryDate.UTC()
	if entryDate.Before(startDate.UTC()) || entryDate.After(endDate.UTC()) {
		return masterdata.ErrFiscalPeriodNotFound
	}
	if entry.TargetPeriodNumber == 0 {
		if periodType != masterdata.FiscalPeriodTypeNormal {
			return masterdata.ErrFiscalPeriodNotFound
		}
		return nil
	}
	if periodNumber != entry.TargetPeriodNumber {
		return masterdata.ErrFiscalPeriodNotFound
	}
	switch entry.TargetPeriodNumber {
	case 13:
		if periodType != masterdata.FiscalPeriodTypeYearEndAdjustment {
			return masterdata.ErrFiscalPeriodNotFound
		}
	case 14:
		if periodType != masterdata.FiscalPeriodTypeAuditAdjustment {
			return masterdata.ErrFiscalPeriodNotFound
		}
	default:
		return masterdata.ErrFiscalPeriodNotFound
	}
	return nil
}

func (d *Driver) PostReversalAtomic(ctx context.Context, original, reversal posting.JournalEntry) (posting.JournalEntry, error) {
	return d.PostReversalAtomicContext(ctx, original, reversal)
}

func (d *Driver) PostReversalAtomicContext(ctx context.Context, original, reversal posting.JournalEntry) (result posting.JournalEntry, resultErr error) {
	if original.CompanyID == uuid.Nil || original.CompanyID != reversal.CompanyID {
		return posting.JournalEntry{}, posting.ErrInvalidReversal
	}
	tx, err := d.beginCompanyTx(ctx, reversal.CompanyID)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	defer func() {
		if rollbackErr := tx.Rollback(context.WithoutCancel(ctx)); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			resultErr = errors.Join(resultErr, fmt.Errorf("rollback reversal transaction: %w", rollbackErr))
		}
	}()

	var originalData []byte
	if err := tx.QueryRow(ctx,
		`SELECT entry FROM account_moves WHERE company_id=$1 AND id=$2 FOR UPDATE`,
		original.CompanyID, original.ID).Scan(&originalData); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return posting.JournalEntry{}, posting.ErrEntryNotFound
		}
		return posting.JournalEntry{}, err
	}
	var currentOriginal posting.JournalEntry
	if err := json.Unmarshal(originalData, &currentOriginal); err != nil {
		return posting.JournalEntry{}, err
	}
	if currentOriginal.Status != posting.EntryPosted || currentOriginal.ReversedEntryID != nil {
		return posting.JournalEntry{}, posting.ErrEntryAlreadyReversed
	}
	if err := posting.ValidateReversalLines(currentOriginal, reversal); err != nil {
		return posting.JournalEntry{}, err
	}
	reversal.Classification = posting.EntryClassReversing
	var reversalStatus posting.EntryStatus
	var reversalData []byte
	if err := tx.QueryRow(ctx,
		`SELECT status,entry FROM account_moves WHERE company_id=$1 AND id=$2 AND journal_id=$3 FOR UPDATE`,
		reversal.CompanyID, reversal.ID, reversal.JournalID).Scan(&reversalStatus, &reversalData); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return posting.JournalEntry{}, posting.ErrEntryNotFound
		}
		return posting.JournalEntry{}, err
	}
	if reversalStatus != posting.EntryDraft {
		return posting.JournalEntry{}, posting.ErrEntryNotDraft
	}
	var currentReversal posting.JournalEntry
	if err := json.Unmarshal(reversalData, &currentReversal); err != nil {
		return posting.JournalEntry{}, err
	}
	if reversal.PeriodID == uuid.Nil {
		reversal.PeriodID = currentReversal.PeriodID
	}
	if !posting.DraftContentMatches(currentReversal, reversal) {
		return posting.JournalEntry{}, financialtypes.ErrCannotModifyPostedMove
	}
	if err := validatePostingPeriodTx(ctx, tx, reversal); err != nil {
		return posting.JournalEntry{}, err
	}

	var sequence int64
	if err := tx.QueryRow(ctx,
		`SELECT current_sequence FROM journals WHERE company_id=$1 AND id=$2 FOR UPDATE`,
		reversal.CompanyID, reversal.JournalID).Scan(&sequence); err != nil {
		return posting.JournalEntry{}, err
	}
	sequence++
	if _, err := tx.Exec(ctx,
		`UPDATE journals SET current_sequence=$3 WHERE company_id=$1 AND id=$2`,
		reversal.CompanyID, reversal.JournalID, sequence); err != nil {
		return posting.JournalEntry{}, err
	}
	reversal.SequenceNumber = sequence
	if err := d.saveEntryTx(ctx, tx, reversal); err != nil {
		return posting.JournalEntry{}, err
	}
	reversalID := reversal.ID
	currentOriginal.ReversedEntryID = &reversalID
	currentOriginal.Status = posting.EntryReversed
	currentOriginal.UpdatedAt = time.Now().UTC()
	if err := d.saveEntryTx(ctx, tx, currentOriginal); err != nil {
		return posting.JournalEntry{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return posting.JournalEntry{}, err
	}
	return reversal, nil
}

func (d *Driver) saveEntryTx(ctx context.Context, tx pgx.Tx, entry posting.JournalEntry) error {
	if entry.ID == uuid.Nil || entry.CompanyID == uuid.Nil || entry.JournalID == uuid.Nil {
		return posting.ErrInvalidEntry
	}
	for i := range entry.Lines {
		if entry.Lines[i].CompanyID != uuid.Nil && entry.Lines[i].CompanyID != entry.CompanyID {
			return posting.ErrInvalidEntry
		}
		entry.Lines[i].CompanyID = entry.CompanyID
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	if entry.PeriodID == uuid.Nil {
		if entry.Status == posting.EntryPosted {
			return posting.ErrInvalidEntry
		}
		entryDate := entry.EntryDate
		if entryDate.IsZero() {
			entryDate = entry.Date
		}
		err := tx.QueryRow(ctx, `SELECT id FROM fiscal_periods
			WHERE company_id=$1 AND start_date <= $2 AND end_date >= $2
			  AND period_type=$3
			ORDER BY period_number ASC LIMIT 1`,
			entry.CompanyID, entryDate, masterdata.FiscalPeriodTypeNormal).Scan(&entry.PeriodID)
		if errors.Is(err, pgx.ErrNoRows) {
			return masterdata.ErrFiscalPeriodNotFound
		}
		if err != nil {
			return err
		}
	}
	var companyFunctionalCurrency string
	if err := tx.QueryRow(ctx,
		`SELECT functional_currency FROM companies WHERE id=$1`, entry.CompanyID,
	).Scan(&companyFunctionalCurrency); err != nil {
		return err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	classification := entry.Classification
	if classification == "" {
		classification = posting.EntryClassRegular
	}
	date := entry.EntryDate
	if date.IsZero() {
		date = entry.Date
	}
	if entry.Status == posting.EntryPosted || entry.Status == posting.EntryReversed {
		expectedStatus := posting.EntryDraft
		if entry.Status == posting.EntryReversed {
			expectedStatus = posting.EntryPosted
		}
		result, err := tx.Exec(ctx,
			`UPDATE account_moves SET journal_id=$3,period_id=$4,entry_date=$5,entry_number=$6,
			 status=$7,classification=$8,reversed_move_id=$9,entry=$10,source_event_id=$11
			 WHERE id=$1 AND company_id=$2 AND status=$12`,
			entry.ID, entry.CompanyID, entry.JournalID, entry.PeriodID, date, entry.SequenceNumber,
			entry.Status, classification, entry.ReversedEntryID, data, entry.SourceEventID, expectedStatus)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return posting.ErrEntryNotDraft
		}
		action := "POST"
		if entry.Status == posting.EntryReversed {
			action = "REVERSE"
		}
		return appendEntryAuditTx(ctx, tx, entry, action)
	}
	if entry.Status != posting.EntryDraft {
		return posting.ErrInvalidEntry
	}
	result, err := tx.Exec(ctx, `INSERT INTO account_moves(id,company_id,journal_id,period_id,entry_date,entry_number,status,classification,reversed_move_id,entry,source_event_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
	 ON CONFLICT(id) DO UPDATE SET journal_id=EXCLUDED.journal_id,period_id=EXCLUDED.period_id,
	 entry_date=EXCLUDED.entry_date,entry_number=EXCLUDED.entry_number,status=EXCLUDED.status,
	 classification=EXCLUDED.classification,reversed_move_id=EXCLUDED.reversed_move_id,entry=EXCLUDED.entry,
	 source_event_id=EXCLUDED.source_event_id
	 WHERE account_moves.status='draft'`,
		entry.ID, entry.CompanyID, entry.JournalID, entry.PeriodID, date, entry.SequenceNumber,
		entry.Status, classification, entry.ReversedEntryID, data, entry.SourceEventID)
	if err != nil {
		return err
	}
	if affected := result.RowsAffected(); affected != 1 {
		return posting.ErrEntryNotDraft
	}
	if _, err := tx.Exec(ctx, `DELETE FROM account_move_lines WHERE move_id=$1`, entry.ID); err != nil {
		return err
	}
	for _, line := range entry.Lines {
		line, err = posting.NormalizeLineCurrencies(line)
		if err != nil {
			return err
		}
		functionalCurrency := line.FunctionalDebit.Currency()
		if functionalCurrency != companyFunctionalCurrency {
			return posting.ErrFunctionalCurrencyMismatch
		}
		lineData, err := json.Marshal(line)
		if err != nil {
			return err
		}
		transactionAmount := line.TransactionAmount
		transactionMinorUnits, err := transactionAmount.MinorUnitsExact()
		if err != nil {
			return fmt.Errorf("line %s transaction amount %s: %w", line.ID, transactionAmount, err)
		}
		if line.FunctionalCredit.Amount().IsPositive() {
			transactionMinorUnits = -transactionMinorUnits
		}
		functionalDebitMinor, err := line.FunctionalDebit.MinorUnitsExact()
		if err != nil {
			return fmt.Errorf("line %s functional debit %s: %w", line.ID, line.FunctionalDebit, err)
		}
		functionalCreditMinor, err := line.FunctionalCredit.MinorUnitsExact()
		if err != nil {
			return fmt.Errorf("line %s functional credit %s: %w", line.ID, line.FunctionalCredit, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO account_move_lines (
			move_id,company_id,line_id,account_id,partner_id,cost_center_id,project_id,segment_id,business_unit_id,matching_number,reconciled_amount,
			debit,credit,amount_currency,currency_id,transaction_amount_minor,transaction_currency,exchange_rate,
			functional_debit_minor,functional_credit_minor,line_data
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
		ON CONFLICT(move_id,line_id) DO UPDATE SET
			account_id=EXCLUDED.account_id, debit=EXCLUDED.debit, credit=EXCLUDED.credit,
			amount_currency=EXCLUDED.amount_currency, currency_id=EXCLUDED.currency_id,
			transaction_amount_minor=EXCLUDED.transaction_amount_minor,
			transaction_currency=EXCLUDED.transaction_currency, exchange_rate=EXCLUDED.exchange_rate,
			functional_debit_minor=EXCLUDED.functional_debit_minor,
			functional_credit_minor=EXCLUDED.functional_credit_minor, line_data=EXCLUDED.line_data`,
			entry.ID, entry.CompanyID, line.ID, line.AccountID, line.PartnerID, line.CostCenterID, line.ProjectID, line.SegmentID, line.BusinessUnitID, line.MatchingNumber,
			line.ReconciledAmount, line.FunctionalDebit.Amount(), line.FunctionalCredit.Amount(), transactionAmount.Amount(),
			line.TransactionCurrency, transactionMinorUnits, line.TransactionCurrency, line.ExchangeRate,
			functionalDebitMinor, functionalCreditMinor, lineData); err != nil {
			return err
		}
	}
	return nil
}
func (d *Driver) GetNextSequence(ctx context.Context, companyID, id uuid.UUID) (int64, error) {
	return d.GetNextSequenceWithLock(ctx, companyID, id)
}

func (d *Driver) GetNextSequenceForCompanyContext(ctx context.Context, companyID, journalID uuid.UUID) (int64, error) {
	return d.GetNextSequenceWithLock(ctx, companyID, journalID)
}

// PostBulk atomically posts a batch of same-company entries. Sequence numbers
// are reserved as one contiguous range per journal (a single
// UPDATE ... RETURNING per journal under the journal row lock), drafts are
// verified against the caller's entries to prevent tampering, and the moves
// themselves are updated through a pipelined batch so the whole group commits
// or rolls back together.
func (d *Driver) PostBulk(ctx context.Context, entries []posting.JournalEntry) (resultErr error) {
	if len(entries) == 0 {
		return nil
	}
	companyID := entries[0].CompanyID
	if companyID == uuid.Nil {
		return posting.ErrInvalidEntry
	}
	seen := make(map[uuid.UUID]struct{}, len(entries))
	for i := range entries {
		if entries[i].CompanyID != companyID {
			return &posting.BulkPostError{Index: i, EntryID: entries[i].ID, Err: masterdata.ErrCompanyScopeRequired}
		}
		if entries[i].ID == uuid.Nil || entries[i].JournalID == uuid.Nil {
			return &posting.BulkPostError{Index: i, EntryID: entries[i].ID, Err: posting.ErrInvalidEntry}
		}
		if _, duplicate := seen[entries[i].ID]; duplicate {
			return &posting.BulkPostError{Index: i, EntryID: entries[i].ID, Err: posting.ErrEntryNotDraft}
		}
		seen[entries[i].ID] = struct{}{}
		if entries[i].Status != posting.EntryDraft && entries[i].Status != posting.EntryPosted {
			return &posting.BulkPostError{Index: i, EntryID: entries[i].ID, Err: posting.ErrInvalidEntry}
		}
		if err := entries[i].Validate(); err != nil {
			return &posting.BulkPostError{Index: i, EntryID: entries[i].ID, Err: err}
		}
	}

	tx, err := d.beginCompanyTx(ctx, companyID)
	if err != nil {
		return err
	}
	defer func() {
		if rollbackErr := tx.Rollback(context.WithoutCancel(ctx)); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			resultErr = errors.Join(resultErr, fmt.Errorf("rollback bulk posting transaction: %w", rollbackErr))
		}
	}()

	for i := range entries {
		if entries[i].Status != posting.EntryPosted || entries[i].PeriodID != uuid.Nil {
			continue
		}
		entryDate := entries[i].EntryDate
		if entryDate.IsZero() {
			entryDate = entries[i].Date
		}
		period, err := d.GetPeriodByDateForCompany(ctx, companyID, entryDate)
		if err != nil {
			return &posting.BulkPostError{Index: i, EntryID: entries[i].ID, Err: err}
		}
		entries[i].PeriodID = period.ID
	}

	posted := make([]int, 0, len(entries))
	journalGroups := make(map[uuid.UUID][]int)
	journalOrder := make([]uuid.UUID, 0)
	for i := range entries {
		if entries[i].Status != posting.EntryPosted {
			continue
		}
		journalID := entries[i].JournalID
		if _, known := journalGroups[journalID]; !known {
			journalOrder = append(journalOrder, journalID)
		}
		journalGroups[journalID] = append(journalGroups[journalID], i)
		posted = append(posted, i)
	}
	// Reserve journal locks in a deterministic (sorted) order so concurrent
	// bulk posts of multi-journal batches can never deadlock each other.
	sort.Slice(journalOrder, func(a, b int) bool {
		return bytes.Compare(journalOrder[a][:], journalOrder[b][:]) < 0
	})
	for _, journalID := range journalOrder {
		indexes := journalGroups[journalID]
		var first int64
		if err := tx.QueryRow(ctx,
			`UPDATE journals
			 SET current_sequence = current_sequence + $3
			 WHERE company_id=$1 AND id=$2
			 RETURNING current_sequence - $3 + 1`,
			companyID, journalID, len(indexes)).Scan(&first); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				err = masterdata.ErrJournalNotFound
			}
			return &posting.BulkPostError{Index: indexes[0], EntryID: entries[indexes[0]].ID, Err: err}
		}
		for offset, idx := range indexes {
			entries[idx].SequenceNumber = first + int64(offset)
		}
	}

	if len(posted) > 0 {
		ids := make([]uuid.UUID, len(posted))
		for k, idx := range posted {
			ids[k] = entries[idx].ID
		}
		drafts, err := fetchDraftsForBulk(ctx, tx, companyID, ids)
		if err != nil {
			return err
		}
		for _, idx := range posted {
			saved, ok := drafts[entries[idx].ID]
			if !ok {
				return &posting.BulkPostError{Index: idx, EntryID: entries[idx].ID, Err: posting.ErrEntryNotDraft}
			}
			if !posting.DraftContentMatches(saved, entries[idx]) {
				return &posting.BulkPostError{Index: idx, EntryID: entries[idx].ID, Err: financialtypes.ErrCannotModifyPostedMove}
			}
		}

		// Flip every draft to posted in one set-based UPDATE so the balance
		// and integrity triggers aggregate the whole batch once instead of
		// running per entry.
		records := make([]bulkMoveRecord, len(posted))
		for k, idx := range posted {
			entry := entries[idx]
			classification := entry.Classification
			if classification == "" {
				classification = posting.EntryClassRegular
			}
			date := entry.EntryDate
			if date.IsZero() {
				date = entry.Date
			}
			records[k] = bulkMoveRecord{
				ID: entry.ID, JournalID: entry.JournalID, PeriodID: entry.PeriodID,
				EntryDate: date, EntryNumber: entry.SequenceNumber, Status: entry.Status,
				Classification: classification, ReversedMoveID: entry.ReversedEntryID,
				Entry: entry,
			}
		}
		payload, err := json.Marshal(records)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE account_moves m
			SET journal_id = v.journal_id, period_id = v.period_id, entry_date = v.entry_date,
				entry_number = v.entry_number, status = v.status, classification = v.classification,
				reversed_move_id = v.reversed_move_id, entry = v.entry
			FROM jsonb_to_recordset($1::jsonb) AS v(
				id uuid, journal_id uuid, period_id uuid, entry_date date, entry_number bigint,
				status varchar, classification varchar, reversed_move_id uuid, entry jsonb)
			WHERE m.id = v.id AND m.company_id = $2 AND m.status = 'draft'`,
			string(payload), companyID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != int64(len(posted)) {
			// A concurrent transaction posted one of our drafts between the
			// verification read and this UPDATE inside READ COMMITTED.
			if err := d.bulkIdentifyNonDrafts(ctx, tx, companyID, posted, entries); err != nil {
				return err
			}
			return &posting.BulkPostError{Index: posted[0], EntryID: entries[posted[0]].ID, Err: posting.ErrEntryNotDraft}
		}
		postedEntries := make([]posting.JournalEntry, len(posted))
		for k, idx := range posted {
			postedEntries[k] = entries[idx]
		}
		if err := appendEntryAuditBatchTx(ctx, tx, postedEntries, "POST"); err != nil {
			return &posting.BulkPostError{Index: posted[0], EntryID: entries[posted[0]].ID, Err: err}
		}
	}

	for i := range entries {
		if entries[i].Status != posting.EntryDraft {
			continue
		}
		if err := d.saveEntryTx(ctx, tx, entries[i]); err != nil {
			return &posting.BulkPostError{Index: i, EntryID: entries[i].ID, Err: err}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return nil
}

func fetchDraftsForBulk(ctx context.Context, tx pgx.Tx, companyID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]posting.JournalEntry, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, entry FROM account_moves WHERE company_id=$1 AND id=ANY($2) AND status='draft'`,
		companyID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	drafts := make(map[uuid.UUID]posting.JournalEntry, len(ids))
	for rows.Next() {
		var id uuid.UUID
		var data []byte
		if err := rows.Scan(&id, &data); err != nil {
			return nil, err
		}
		var entry posting.JournalEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			return nil, err
		}
		drafts[id] = entry
	}
	return drafts, rows.Err()
}

// bulkMoveRecord is the row shape handed to jsonb_to_recordset for the
// set-based draft->posted flip inside PostBulk.
type bulkMoveRecord struct {
	ID             uuid.UUID                   `json:"id"`
	JournalID      uuid.UUID                   `json:"journal_id"`
	PeriodID       uuid.UUID                   `json:"period_id"`
	EntryDate      time.Time                   `json:"entry_date"`
	EntryNumber    int64                       `json:"entry_number"`
	Status         posting.EntryStatus         `json:"status"`
	Classification posting.EntryClassification `json:"classification"`
	ReversedMoveID *uuid.UUID                  `json:"reversed_move_id"`
	Entry          posting.JournalEntry        `json:"entry"`
}

// bulkIdentifyNonDrafts pinpoints which bulk entries are no longer stored as
// drafts after a set-based UPDATE skipped them, so the caller gets an error
// attributed to the offending entry rather than the whole batch.
func (d *Driver) bulkIdentifyNonDrafts(ctx context.Context, tx pgx.Tx, companyID uuid.UUID, posted []int, entries []posting.JournalEntry) error {
	ids := make([]uuid.UUID, len(posted))
	for k, idx := range posted {
		ids[k] = entries[idx].ID
	}
	rows, err := tx.Query(ctx,
		`SELECT id FROM account_moves WHERE company_id=$1 AND id=ANY($2) AND status <> 'draft'`,
		companyID, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	nonDrafts := make(map[uuid.UUID]struct{}, len(ids))
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return err
		}
		nonDrafts[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, idx := range posted {
		if _, notDraft := nonDrafts[entries[idx].ID]; notDraft {
			return &posting.BulkPostError{Index: idx, EntryID: entries[idx].ID, Err: posting.ErrEntryNotDraft}
		}
	}
	return nil
}
