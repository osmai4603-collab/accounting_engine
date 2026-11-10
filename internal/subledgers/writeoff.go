package subledgers

import (
	"fmt"
	"time"

	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
)

func (l *ReceivableLedger) PrepareWriteOff(lineID uuid.UUID, amount financialtypes.Money, params WriteOffParams) (posting.JournalEntry, error) {
	return prepareOpenLineWriteOff(l.repo, l.companyID, lineID, amount, params, LineReceivable)
}

func (l *PayableLedger) PrepareWriteOff(lineID uuid.UUID, amount financialtypes.Money, params WriteOffParams) (posting.JournalEntry, error) {
	return prepareOpenLineWriteOff(l.repo, l.companyID, lineID, amount, params, LinePayable)
}

func PrepareWriteOffForCompany(repo Repository, companyID, lineID uuid.UUID, amount financialtypes.Money, params WriteOffParams) (posting.JournalEntry, error) {
	line, err := repo.GetOpenLine(lineID)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	return prepareOpenLineWriteOff(repo, companyID, lineID, amount, params, line.Kind)
}

func prepareOpenLineWriteOff(repo Repository, companyID, lineID uuid.UUID, amount financialtypes.Money, params WriteOffParams, kind LineKind) (posting.JournalEntry, error) {
	line, err := repo.GetOpenLine(lineID)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	if line.CompanyID == uuid.Nil || (companyID != uuid.Nil && line.CompanyID != companyID) {
		return posting.JournalEntry{}, ErrCompanyMismatch
	}
	if line.Kind != kind || !validLineKind(kind) || line.Closed || line.PendingWriteOffID != uuid.Nil ||
		line.ControlAccountID == uuid.Nil || amount.Currency() != line.ResidualAmount.Currency() ||
		amount.IsNegative() || amount.Amount().IsZero() || amount.GreaterThan(line.ResidualAmount) ||
		params.JournalID == uuid.Nil || params.AdjustmentAccountID == uuid.Nil {
		if line.PendingWriteOffID != uuid.Nil {
			return posting.JournalEntry{}, ErrWriteOffPending
		}
		return posting.JournalEntry{}, ErrInvalidOpenLine
	}
	date := params.Date
	if date.IsZero() {
		date = time.Now().UTC()
	}
	zero, err := financialtypes.Zero(amount.Currency())
	if err != nil {
		return posting.JournalEntry{}, err
	}
	lines := writeOffJournalLines(line, params.AdjustmentAccountID, amount, zero)
	entryID := uuid.New()
	reference := writeOffReference(line.ID, entryID)
	entry := posting.JournalEntry{
		CompanyID: line.CompanyID, ID: entryID, JournalID: params.JournalID,
		EntryDate: date, Date: date, Status: posting.EntryDraft,
		DocumentReference: reference, Reference: params.Description,
		CreatedAt: time.Now().UTC(), Lines: lines,
	}
	if err := entry.Validate(); err != nil {
		return posting.JournalEntry{}, err
	}
	updated := cloneOpenLine(line)
	updated.PendingWriteOffID = entry.ID
	updated.PendingWriteOffAmount = amount
	updated.WriteOffAdjustmentAccountID = params.AdjustmentAccountID
	repository, ok := repo.(ConditionalOpenLinesAndDraftRepository)
	if !ok {
		return posting.JournalEntry{}, ErrAtomicMatchRequired
	}
	if err := repository.CompareAndSaveOpenLinesAndDraft([]OpenLine{line}, []OpenLine{updated}, entry); err != nil {
		return posting.JournalEntry{}, err
	}
	return entry, nil
}

func (l *ReceivableLedger) ConfirmWriteOff(lineID, postedEntryID uuid.UUID) error {
	return confirmOpenLineWriteOff(l.repo, l.companyID, lineID, postedEntryID)
}

func (l *PayableLedger) ConfirmWriteOff(lineID, postedEntryID uuid.UUID) error {
	return confirmOpenLineWriteOff(l.repo, l.companyID, lineID, postedEntryID)
}

func ConfirmWriteOffForCompany(repo Repository, companyID, lineID, postedEntryID uuid.UUID) error {
	return confirmOpenLineWriteOff(repo, companyID, lineID, postedEntryID)
}

func confirmOpenLineWriteOff(repo Repository, companyID, lineID, postedEntryID uuid.UUID) error {
	line, err := repo.GetOpenLine(lineID)
	if err != nil {
		return err
	}
	if companyID != uuid.Nil && line.CompanyID != companyID {
		return ErrCompanyMismatch
	}
	if line.PendingWriteOffID == uuid.Nil || line.PendingWriteOffID != postedEntryID ||
		line.PendingWriteOffAmount.Currency() != line.ResidualAmount.Currency() ||
		line.PendingWriteOffAmount.Amount().IsZero() || line.WriteOffAdjustmentAccountID == uuid.Nil {
		return ErrNoPendingWriteOff
	}
	entryReader, ok := repo.(PostedJournalEntryReader)
	if !ok {
		return posting.ErrEntryNotFound
	}
	entry, err := entryReader.GetEntryByID(postedEntryID)
	if err != nil {
		return err
	}
	if entry.Status != posting.EntryPosted || entry.CompanyID != line.CompanyID ||
		entry.DocumentReference != writeOffReference(line.ID, postedEntryID) {
		return posting.ErrEntryNotPosted
	}
	zero, err := financialtypes.Zero(line.ResidualAmount.Currency())
	if err != nil {
		return err
	}
	expected := posting.JournalEntry{
		CompanyID: line.CompanyID, ID: entry.ID, EntryDate: entry.EntryDate, Date: entry.Date,
		Status: posting.EntryPosted, DocumentReference: writeOffReference(line.ID, postedEntryID),
		Lines: writeOffJournalLines(line, line.WriteOffAdjustmentAccountID, line.PendingWriteOffAmount, zero),
	}
	updated := cloneOpenLine(line)
	updated.ResidualAmount, err = updated.ResidualAmount.Subtract(line.PendingWriteOffAmount)
	if err != nil {
		return err
	}
	if updated.WrittenOffAmount.Currency() == "" {
		updated.WrittenOffAmount = zero
	}
	updated.WrittenOffAmount, err = updated.WrittenOffAmount.Add(line.PendingWriteOffAmount)
	if err != nil {
		return err
	}
	updated.Closed = updated.ResidualAmount.Amount().IsZero()
	updated.PendingWriteOffID = uuid.Nil
	updated.PendingWriteOffAmount = financialtypes.Money{}
	updated.WriteOffAdjustmentAccountID = uuid.Nil
	repository, ok := repo.(AtomicOpenLineWriteOffConfirmationRepository)
	if !ok {
		return ErrAtomicMatchRequired
	}
	return repository.ConfirmOpenLineWriteOff(line, updated, expected)
}

func writeOffJournalLines(line OpenLine, adjustmentAccountID uuid.UUID, amount, zero financialtypes.Money) []posting.JournalEntryLine {
	adjustment := posting.JournalEntryLine{
		CompanyID: line.CompanyID, ID: uuid.New(), AccountID: adjustmentAccountID, Debit: amount, Credit: zero,
	}
	control := posting.JournalEntryLine{
		CompanyID: line.CompanyID, ID: uuid.New(), AccountID: line.ControlAccountID, Debit: zero, Credit: amount,
	}
	if line.Kind == LinePayable {
		adjustment.Debit, adjustment.Credit = zero, amount
		control.Debit, control.Credit = amount, zero
	}
	return []posting.JournalEntryLine{adjustment, control}
}

func writeOffReference(lineID, entryID uuid.UUID) string {
	return fmt.Sprintf("SUBLEDGER:WRITEOFF:%s:%s", lineID, entryID)
}
