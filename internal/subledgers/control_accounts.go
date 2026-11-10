package subledgers

import (
	"errors"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
)

var ErrInvalidControlAccount = errors.New("subledger: account is not a valid control account for this ledger")

type ControlAccountReader interface {
	GetAccountByIDForCompany(companyID, accountID uuid.UUID) (masterdata.Account, error)
}

type PostedJournalEntryReader interface {
	GetEntryByID(uuid.UUID) (posting.JournalEntry, error)
}

type AtomicPostedOpenLineRepository interface {
	SaveOpenLineForPostedEntry(OpenLine) error
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
		Kind: string(line.Kind), Amount: line.OriginalAmount,
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
	repo Repository,
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
	accounts, ok := repo.(ControlAccountReader)
	if !ok {
		return OpenLine{}, ErrInvalidControlAccount
	}
	entries, ok := repo.(PostedJournalEntryReader)
	if !ok {
		return OpenLine{}, posting.ErrEntryNotFound
	}
	lines, ok := repo.(AtomicPostedOpenLineRepository)
	if !ok {
		return OpenLine{}, ErrAtomicMatchRequired
	}
	account, err := accounts.GetAccountByIDForCompany(companyID, controlAccountID)
	if err != nil {
		return OpenLine{}, err
	}
	entry, err := entries.GetEntryByID(journalEntryID)
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
	if err := ValidatePostedOpenLine(line, account, entry); err != nil {
		return OpenLine{}, err
	}
	if err := lines.SaveOpenLineForPostedEntry(line); err != nil {
		return OpenLine{}, err
	}
	return line, nil
}
