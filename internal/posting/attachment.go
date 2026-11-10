package posting

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var (
	ErrUnsupportedMutationBackend = errors.New("unsupported mutation backend")
)

// LedgerAttachment هو عقد واجهة يسمح لأي دفتر مساعد بتسجيل
// عملية تشغيلية يُنفذها محرك الترحيل ذرياً مع القيد.
//
// المرجع: COSO IC-IF Principle 10 — أنشطة الرقابة الذرية
// المرجع: Martin Fowler — Single Writer Pattern
type LedgerAttachment interface {
	// AttachmentKind يُرجع نوع الملحق (e.g., "fixed_asset_depreciation",
	// "receivable_writeoff", "open_line")
	AttachmentKind() string

	// ValidateAttachment يتحقق من صحة البيانات التشغيلية
	// قبل فتح معاملة قاعدة البيانات
	ValidateAttachment(ctx context.Context, entry JournalEntry) error

	// ExecuteAtomicMutation ينفذ التحديث التشغيلي ضمن المعاملة الذرية
	// للترحيل. يجب أن يستخدم الـ tx المقدم وليس اتصالاً مستقلاً.
	// المرجع: IAS 16 — تحديث مجمع الاستهلاك ذرياً مع القيد
	ExecuteAtomicMutation(ctx context.Context, tx AtomicTransaction, entry JournalEntry) error
}

// AtomicTransaction يمثل معاملة قاعدة بيانات أو ذاكرة مفتوحة
// يستخدمها الملحق لتنفيذ التحديثات ضمن نفس المعاملة
type AtomicTransaction interface {
	Exec(ctx context.Context, sql string, args ...any) error
	QueryRow(ctx context.Context, sql string, args ...any) Row
	CustomMutation(ctx context.Context, target string, fn func(state any) error) error
}

// Row واجهة مبسطة لقراءة صف واحد
type Row interface {
	Scan(dest ...any) error
}

// OpenLineMemoryStore واجهة تحديث المطرودات بالذاكرة
type OpenLineMemoryStore interface {
	SaveOpenLineFromAttachment(ctx context.Context, attachment OpenLineAttachment, entry JournalEntry) error
}

// OpenLineLedgerAttachment يوفر التوافقية العكسية مع OpenLineAttachment عبر واجهة LedgerAttachment
type OpenLineLedgerAttachment struct {
	Attachment OpenLineAttachment
}

func (a *OpenLineLedgerAttachment) AttachmentKind() string {
	return "open_line"
}

func (a *OpenLineLedgerAttachment) ValidateAttachment(ctx context.Context, entry JournalEntry) error {
	line := a.Attachment
	if entry.CompanyID == uuid.Nil || line.ID == uuid.Nil || line.CompanyID != entry.CompanyID ||
		line.JournalEntryID != entry.ID ||
		line.PartnerID == uuid.Nil || line.ControlAccountID == uuid.Nil ||
		(line.Kind != "receivable" && line.Kind != "payable") ||
		line.Amount.Currency() == "" || line.Amount.IsNegative() || line.Amount.Amount().IsZero() {
		return ErrInvalidEntry
	}
	return nil
}

func (a *OpenLineLedgerAttachment) ExecuteAtomicMutation(ctx context.Context, tx AtomicTransaction, entry JournalEntry) error {
	// 1. المحاولة في بيئة الذاكرة (Memory Driver)
	err := tx.CustomMutation(ctx, "memory", func(state any) error {
		store, ok := state.(OpenLineMemoryStore)
		if !ok {
			return ErrUnsupportedMutationBackend
		}
		return store.SaveOpenLineFromAttachment(ctx, a.Attachment, entry)
	})
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrUnsupportedMutationBackend) {
		return err
	}

	// 2. المحاولة في بيئة SQL (PostgreSQL Driver)
	var accountCompanyID uuid.UUID
	row := tx.QueryRow(ctx,
		`SELECT company_id FROM accounts WHERE company_id=$1 AND id=$2`,
		entry.CompanyID, a.Attachment.ControlAccountID,
	)
	if err := row.Scan(&accountCompanyID); err != nil {
		return fmt.Errorf("control account scan: %w", err)
	}

	partnerID := a.Attachment.PartnerID
	data := fmt.Sprintf(`{"id":"%s","company_id":"%s","partner_id":"%s","control_account_id":"%s","journal_entry_id":"%s","kind":"%s","original_amount":{"currency":"%s","amount":"%s"},"residual_amount":{"currency":"%s","amount":"%s"},"document_date":"%s","due_date":"%s","reference":"%s"}`,
		a.Attachment.ID, a.Attachment.CompanyID, partnerID, a.Attachment.ControlAccountID, a.Attachment.JournalEntryID,
		a.Attachment.Kind, a.Attachment.Amount.Currency(), a.Attachment.Amount.Amount().String(),
		a.Attachment.Amount.Currency(), a.Attachment.Amount.Amount().String(),
		a.Attachment.DocumentDate.Format(time.RFC3339), a.Attachment.DueDate.Format(time.RFC3339),
		a.Attachment.Reference,
	)

	return tx.Exec(ctx,
		`INSERT INTO subledger_open_lines(id,company_id,partner_id,kind,line_data) VALUES($1,$2,$3,$4,$5)`,
		a.Attachment.ID, a.Attachment.CompanyID, partnerID, a.Attachment.Kind, data,
	)
}
