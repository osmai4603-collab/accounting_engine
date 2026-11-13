package subledgers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
)

// WriteOffAttachment ينفذ تحديث سطر الذمة المفتوح ذرياً
// مع ترحيل قيد الشطب.
// المرجع: IFRS 9.5.4 — شطب الذمم يجب أن يكون ذرياً
type WriteOffAttachment struct {
	OpenLineID          uuid.UUID
	CompanyID           uuid.UUID
	WriteOffAmount      financialtypes.Money
	AdjustmentAccountID uuid.UUID
	UpdatedLine         OpenLine
}

func (a *WriteOffAttachment) AttachmentKind() string {
	return "receivable_writeoff"
}

func (a *WriteOffAttachment) ValidateAttachment(ctx context.Context, entry posting.JournalEntry) error {
	if a.OpenLineID == uuid.Nil || a.CompanyID == uuid.Nil {
		return ErrInvalidOpenLine
	}
	if a.CompanyID != entry.CompanyID {
		return ErrCompanyMismatch
	}
	return nil
}

type WriteOffMemoryStore interface {
	SaveOpenLine(ctx context.Context, line OpenLine) error
}

func (a *WriteOffAttachment) ExecuteAtomicMutation(ctx context.Context, tx posting.AtomicTransaction, entry posting.JournalEntry) error {
	// 1. المحاولة في ذاكرة الخادم (Memory Driver)
	err := tx.CustomMutation(ctx, "memory", func(state any) error {
		store, ok := state.(WriteOffMemoryStore)
		if !ok {
			return posting.ErrUnsupportedMutationBackend
		}
		return store.SaveOpenLine(ctx, a.UpdatedLine)
	})
	if err == nil {
		return nil
	}
	if !errors.Is(err, posting.ErrUnsupportedMutationBackend) {
		return err
	}

	// 2. المحاولة في قاعدة البيانات (PostgreSQL Driver)
	data, err := json.Marshal(a.UpdatedLine)
	if err != nil {
		return err
	}
	return tx.Exec(ctx,
		`UPDATE subledger_open_lines SET line_data = $1 WHERE company_id = $2 AND id = $3`,
		data, a.CompanyID, a.OpenLineID,
	)
}

func (l *ReceivableLedger) WriteOffAtomic(ctx context.Context, engine *posting.Engine, lineID uuid.UUID, amount financialtypes.Money, params WriteOffParams, posterID string) (posting.JournalEntry, error) {
	return writeOffAtomic(ctx, l.repo, engine, l.companyID, lineID, amount, params, LineReceivable, posterID)
}

func (l *PayableLedger) WriteOffAtomic(ctx context.Context, engine *posting.Engine, lineID uuid.UUID, amount financialtypes.Money, params WriteOffParams, posterID string) (posting.JournalEntry, error) {
	return writeOffAtomic(ctx, l.repo, engine, l.companyID, lineID, amount, params, LinePayable, posterID)
}

func writeOffAtomic(ctx context.Context, repo PrepareWriteOffRepository, engine *posting.Engine, companyID, lineID uuid.UUID, amount financialtypes.Money, params WriteOffParams, kind LineKind, posterID string) (posting.JournalEntry, error) {
	line, err := repo.GetOpenLine(ctx, companyID, lineID)
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
	if err := engine.CreateDraftContext(ctx, entry); err != nil {
		return posting.JournalEntry{}, err
	}

	updated := cloneOpenLine(line)
	updated.ResidualAmount, err = updated.ResidualAmount.Subtract(amount)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	if updated.WrittenOffAmount.Currency() == "" {
		updated.WrittenOffAmount = zero
	}
	updated.WrittenOffAmount, err = updated.WrittenOffAmount.Add(amount)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	updated.Closed = updated.ResidualAmount.Amount().IsZero()

	attachment := &WriteOffAttachment{
		OpenLineID:          lineID,
		CompanyID:           line.CompanyID,
		WriteOffAmount:      amount,
		AdjustmentAccountID: params.AdjustmentAccountID,
		UpdatedLine:         updated,
	}

	if err := engine.PostEntryWithAttachmentContext(ctx, &entry, posterID, attachment); err != nil {
		return posting.JournalEntry{}, err
	}
	return entry, nil
}

// Deprecated: Use WriteOffAtomic instead.
func (l *ReceivableLedger) PrepareWriteOff(lineID uuid.UUID, amount financialtypes.Money, params WriteOffParams) (posting.JournalEntry, error) {
	return prepareOpenLineWriteOff(l.repo, l.companyID, lineID, amount, params, LineReceivable)
}

// Deprecated: Use WriteOffAtomic instead.
func (l *PayableLedger) PrepareWriteOff(lineID uuid.UUID, amount financialtypes.Money, params WriteOffParams) (posting.JournalEntry, error) {
	return prepareOpenLineWriteOff(l.repo, l.companyID, lineID, amount, params, LinePayable)
}

// Deprecated: Use WriteOffAtomic instead.
func PrepareWriteOffForCompany(repo PrepareWriteOffRepository, companyID, lineID uuid.UUID, amount financialtypes.Money, params WriteOffParams) (posting.JournalEntry, error) {
	line, err := repo.GetOpenLine(context.Background(), companyID, lineID)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	return prepareOpenLineWriteOff(repo, companyID, lineID, amount, params, line.Kind)
}

type PrepareWriteOffRepository interface {
	OpenLineRepository
	ConditionalOpenLinesAndDraftRepository
}

func prepareOpenLineWriteOff(repo PrepareWriteOffRepository, companyID, lineID uuid.UUID, amount financialtypes.Money, params WriteOffParams, kind LineKind) (posting.JournalEntry, error) {
	line, err := repo.GetOpenLine(context.Background(), companyID, lineID)
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
	if err := repo.CompareAndSaveOpenLinesAndDraft(context.Background(), []OpenLine{line}, []OpenLine{updated}, entry); err != nil {
		return posting.JournalEntry{}, err
	}
	return entry, nil
}

// Deprecated: Use WriteOffAtomic instead.
func (l *ReceivableLedger) ConfirmWriteOff(lineID, postedEntryID uuid.UUID) error {
	return confirmOpenLineWriteOff(l.repo, l.companyID, lineID, postedEntryID)
}

// Deprecated: Use WriteOffAtomic instead.
func (l *PayableLedger) ConfirmWriteOff(lineID, postedEntryID uuid.UUID) error {
	return confirmOpenLineWriteOff(l.repo, l.companyID, lineID, postedEntryID)
}

type WriteOffRepository interface {
	PrepareWriteOffRepository
	PostedJournalEntryReader
	AtomicOpenLineWriteOffConfirmationRepository
}

// Deprecated: Use WriteOffAtomic instead.
func ConfirmWriteOffForCompany(repo WriteOffRepository, companyID, lineID, postedEntryID uuid.UUID) error {
	return confirmOpenLineWriteOff(repo, companyID, lineID, postedEntryID)
}

func confirmOpenLineWriteOff(repo WriteOffRepository, companyID, lineID, postedEntryID uuid.UUID) error {
	line, err := repo.GetOpenLine(context.Background(), companyID, lineID)
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
	entry, err := repo.GetEntryByIDForCompany(context.Background(), companyID, postedEntryID)
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
	return repo.ConfirmOpenLineWriteOff(context.Background(), line, updated, expected)
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
