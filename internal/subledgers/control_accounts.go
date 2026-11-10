package subledgers

import (
	"context"
	"errors"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var ErrInvalidControlAccount = errors.New("subledger: account is not a valid control account for this ledger")

type ControlAccountReader interface {
	GetAccountByIDForCompany(context.Context, uuid.UUID, uuid.UUID) (masterdata.Account, error)
}

type PostedJournalEntryReader interface {
	GetEntryByIDForCompany(context.Context, uuid.UUID, uuid.UUID) (posting.JournalEntry, error)
}

type AtomicPostedOpenLineRepository interface {
	SaveOpenLineForPostedEntry(context.Context, OpenLine) error
}

type PostedInvoiceRepository interface {
	ControlAccountReader
	PostedJournalEntryReader
	AtomicPostedOpenLineRepository
}

func NewPostingOpenLineAttachment(line OpenLine) (posting.OpenLineAttachment, error) {
	if line.ID == uuid.Nil || line.CompanyID == uuid.Nil || line.JournalEntryID == uuid.Nil ||
		line.PartnerID == nil || *line.PartnerID == uuid.Nil || line.ControlAccountID == uuid.Nil ||
		!validLineKind(line.Kind) || line.OriginalAmount.Currency() == "" ||
		line.OriginalAmount.IsNegative() || line.OriginalAmount.Amount().IsZero() ||
		!line.ResidualAmount.Equal(line.OriginalAmount) {
		return posting.OpenLineAttachment{}, ErrInvalidOpenLine
	}
	return posting.OpenLineAttachment{
		ID: line.ID, CompanyID: line.CompanyID, JournalEntryID: line.JournalEntryID,
		PartnerID: *line.PartnerID, ControlAccountID: line.ControlAccountID,
		Kind: string(line.Kind), Amount: line.OriginalAmount, FunctionalAmount: line.FunctionalOriginalAmount,
		PostingDate:  line.PostingDate,
		DocumentDate: line.DocumentDate, DueDate: line.DueDate, Reference: line.Reference,
	}, nil
}

func postInvoiceEntry(
	engine *posting.Engine,
	entry *posting.JournalEntry,
	companyID, partnerID, controlAccountID uuid.UUID,
	kind LineKind,
	amount financialtypes.Money,
	documentDate, dueDate time.Time,
	reference, posterID string,
) (OpenLine, error) {
	if engine == nil || entry == nil || companyID == uuid.Nil || entry.CompanyID != companyID ||
		partnerID == uuid.Nil || controlAccountID == uuid.Nil {
		return OpenLine{}, ErrInvalidOpenLine
	}
	line := OpenLine{
		CompanyID: companyID, ID: uuid.New(), PartnerID: &partnerID,
		ControlAccountID: controlAccountID, JournalEntryID: entry.ID, Kind: kind,
		OriginalAmount: amount, ResidualAmount: amount,
		DocumentDate: documentDate, DueDate: dueDate, Reference: reference,
	}
	line.PostingDate = entryPostingDate(*entry)
	functionalAmount, err := postedFunctionalControlAmount(*entry, line)
	if err != nil {
		return OpenLine{}, err
	}
	line.FunctionalOriginalAmount = functionalAmount
	line.FunctionalResidualAmount = functionalAmount
	attachment, err := NewPostingOpenLineAttachment(line)
	if err != nil {
		return OpenLine{}, err
	}
	if err := engine.PostEntryWithOpenLine(entry, attachment, posterID); err != nil {
		return OpenLine{}, err
	}
	return line, nil
}

func ValidatePostedOpenLine(line OpenLine, account masterdata.Account, entry posting.JournalEntry) error {
	if line.CompanyID == uuid.Nil || line.ID == uuid.Nil || line.ControlAccountID == uuid.Nil ||
		line.JournalEntryID == uuid.Nil || line.PartnerID == nil || *line.PartnerID == uuid.Nil ||
		account.CompanyID != line.CompanyID || account.ID != line.ControlAccountID ||
		entry.ID != line.JournalEntryID || entry.CompanyID != line.CompanyID || !validLineKind(line.Kind) ||
		line.OriginalAmount.Currency() == "" || line.OriginalAmount.IsNegative() ||
		line.OriginalAmount.Amount().IsZero() ||
		!line.ResidualAmount.Equal(line.OriginalAmount) {
		return ErrInvalidOpenLine
	}
	if entry.Status != posting.EntryPosted {
		return posting.ErrEntryNotPosted
	}
	if !account.IsControlAccount || !account.IsReconcilable {
		return ErrInvalidControlAccount
	}
	if (line.Kind == LineReceivable && account.AccountType != masterdata.AccountTypeAsset) ||
		(line.Kind == LinePayable && account.AccountType != masterdata.AccountTypeLiability) {
		return ErrInvalidControlAccount
	}
	postedAmount, err := postedControlAmount(entry, line)
	if err != nil {
		return err
	}
	if !postedAmount.Equal(line.OriginalAmount) {
		return ErrInvalidOpenLine
	}
	return nil
}

func postedControlAmount(entry posting.JournalEntry, line OpenLine) (financialtypes.Money, error) {
	total, err := financialtypes.Zero(line.OriginalAmount.Currency())
	if err != nil {
		return financialtypes.Money{}, err
	}
	for _, entryLine := range entry.Lines {
		if entryLine.AccountID != line.ControlAccountID ||
			(entryLine.PartnerID != nil && *entryLine.PartnerID != *line.PartnerID) {
			continue
		}
		amount := entryLine.Debit
		functionalAmount := entryLine.FunctionalDebit
		if line.Kind == LinePayable {
			amount = entryLine.Credit
			functionalAmount = entryLine.FunctionalCredit
		}
		if amount.Amount().IsZero() && functionalAmount.Amount().IsZero() {
			continue
		}
		if entryLine.AmountCurrency.Currency() != "" {
			amount = entryLine.AmountCurrency
		} else if amount.Amount().IsZero() {
			amount = functionalAmount
		}
		if amount.Currency() != line.OriginalAmount.Currency() {
			return financialtypes.Money{}, ErrMixedCurrencyReconciliation
		}
		total, err = total.Add(amount)
		if err != nil {
			return financialtypes.Money{}, err
		}
	}
	return total, nil
}

func addPostedInvoice(
	repo PostedInvoiceRepository,
	companyID, partnerID, controlAccountID, journalEntryID uuid.UUID,
	amount financialtypes.Money,
	documentDate, dueDate time.Time,
	reference string,
	kind LineKind,
) (OpenLine, error) {
	if companyID == uuid.Nil || partnerID == uuid.Nil || controlAccountID == uuid.Nil || journalEntryID == uuid.Nil ||
		amount.Currency() == "" || amount.IsNegative() || amount.Amount().IsZero() {
		return OpenLine{}, ErrInvalidOpenLine
	}
	ctx := context.Background()
	account, err := repo.GetAccountByIDForCompany(ctx, companyID, controlAccountID)
	if err != nil {
		return OpenLine{}, err
	}
	entry, err := repo.GetEntryByIDForCompany(ctx, companyID, journalEntryID)
	if err != nil {
		return OpenLine{}, err
	}
	partner := partnerID
	line := OpenLine{
		CompanyID: companyID, ID: uuid.New(), PartnerID: &partner,
		ControlAccountID: controlAccountID, JournalEntryID: journalEntryID, Kind: kind,
		OriginalAmount: amount, ResidualAmount: amount,
		DocumentDate: documentDate, DueDate: dueDate, Reference: reference,
	}
	line.PostingDate = entryPostingDate(entry)
	functionalAmount, err := postedFunctionalControlAmount(entry, line)
	if err != nil {
		return OpenLine{}, err
	}
	line.FunctionalOriginalAmount = functionalAmount
	line.FunctionalResidualAmount = functionalAmount
	if err := ValidatePostedOpenLine(line, account, entry); err != nil {
		return OpenLine{}, err
	}
	if err := repo.SaveOpenLineForPostedEntry(ctx, line); err != nil {
		return OpenLine{}, err
	}
	return line, nil
}

func postedFunctionalControlAmount(entry posting.JournalEntry, line OpenLine) (financialtypes.Money, error) {
	total := decimal.Zero
	currency := ""
	for _, entryLine := range entry.Lines {
		if entryLine.AccountID != line.ControlAccountID ||
			(entryLine.PartnerID != nil && (line.PartnerID == nil || *entryLine.PartnerID != *line.PartnerID)) {
			continue
		}

		amount := entryLine.FunctionalDebit
		if line.Kind == LinePayable {
			amount = entryLine.FunctionalCredit
		}
		if amount.Currency() == "" {
			amount = entryLine.Debit
			if line.Kind == LinePayable {
				amount = entryLine.Credit
			}
		}
		if amount.Currency() == "" {
			continue
		}
		if currency == "" {
			currency = amount.Currency()
		} else if currency != amount.Currency() {
			return financialtypes.Money{}, ErrControlBalanceCurrencyMismatch
		}
		total = total.Add(amount.Amount())
	}
	if currency == "" || total.IsZero() {
		return financialtypes.Money{}, ErrInvalidOpenLine
	}
	return financialtypes.NewMoney(total, currency)
}

func entryPostingDate(entry posting.JournalEntry) time.Time {
	if !entry.EntryDate.IsZero() {
		return entry.EntryDate
	}
	return entry.Date
}
