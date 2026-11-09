package subledgers

import (
	"time"

	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
)

type PayableLedger struct {
	repo      Repository
	companyID uuid.UUID
}

func NewPayableLedger(repo Repository) *PayableLedger {
	return &PayableLedger{repo: repo}
}

func NewPayableLedgerForCompany(repo Repository, companyID uuid.UUID) *PayableLedger {
	return &PayableLedger{repo: repo, companyID: companyID}
}

func (l *PayableLedger) AddInvoice(partnerID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	if l.companyID == uuid.Nil {
		return l.addInvoice(uuid.Nil, partnerID, uuid.Nil, uuid.Nil, amount, documentDate, dueDate, reference)
	}
	return l.addInvoice(l.companyID, partnerID, uuid.Nil, uuid.Nil, amount, documentDate, dueDate, reference)
}

func (l *PayableLedger) AddInvoiceWithControl(partnerID, controlAccountID, journalEntryID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	if controlAccountID == uuid.Nil {
		return OpenLine{}, ErrInvalidOpenLine
	}
	if l.companyID == uuid.Nil {
		return l.addInvoice(uuid.Nil, partnerID, controlAccountID, journalEntryID, amount, documentDate, dueDate, reference)
	}
	return l.AddInvoiceForCompanyWithControl(l.companyID, partnerID, controlAccountID, journalEntryID, amount, documentDate, dueDate, reference)
}

func (l *PayableLedger) AddInvoiceForCompany(companyID, partnerID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	if companyID == uuid.Nil {
		return OpenLine{}, ErrInvalidOpenLine
	}
	return l.addInvoice(companyID, partnerID, uuid.Nil, uuid.Nil, amount, documentDate, dueDate, reference)
}

func (l *PayableLedger) AddInvoiceForCompanyWithControl(companyID, partnerID, controlAccountID, journalEntryID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	if companyID == uuid.Nil || controlAccountID == uuid.Nil {
		return OpenLine{}, ErrInvalidOpenLine
	}
	return l.addInvoice(companyID, partnerID, controlAccountID, journalEntryID, amount, documentDate, dueDate, reference)
}

func (l *PayableLedger) addInvoice(companyID, partnerID, controlAccountID, journalEntryID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	if partnerID == uuid.Nil || amount.Currency() == "" || amount.IsNegative() {
		return OpenLine{}, ErrInvalidAmount
	}
	line := OpenLine{
		CompanyID:        companyID,
		ID:               uuid.New(),
		PartnerID:        &partnerID,
		ControlAccountID: controlAccountID,
		JournalEntryID:   journalEntryID,
		Kind:             LinePayable,
		OriginalAmount:   amount,
		ResidualAmount:   amount,
		DocumentDate:     documentDate,
		DueDate:          dueDate,
		Reference:        reference,
	}
	if err := l.repo.SaveOpenLine(line); err != nil {
		return OpenLine{}, err
	}
	return line, nil
}

func (l *PayableLedger) Aging(asOf time.Time, partnerID *uuid.UUID) (AgingSchedule, error) {
	partnerKey := ""
	if partnerID != nil {
		partnerKey = partnerID.String()
	}
	lines, err := l.listOpenLines(partnerKey)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, nil
	}
	currency := lines[0].ResidualAmount.Currency()
	result := make(AgingSchedule)
	for _, bucket := range []AgingBucket{AgingCurrent, Aging1To30, Aging31To60, Aging61To90, AgingOver90} {
		zero, err := financialtypes.Zero(currency)
		if err != nil {
			return nil, err
		}
		result[bucket] = zero
	}
	for _, line := range lines {
		if line.Kind != LinePayable || line.ResidualAmount.Amount().IsZero() {
			continue
		}
		bucket := agingBucket(asOf, line.DueDate)
		amount, err := result[bucket].Add(line.ResidualAmount)
		if err != nil {
			return nil, err
		}
		result[bucket] = amount
	}
	return result, nil
}

func (l *PayableLedger) listOpenLines(partnerID string) ([]OpenLine, error) {
	if l.companyID == uuid.Nil {
		return l.repo.ListOpenLines(partnerID)
	}
	repository, ok := l.repo.(CompanyOpenLineRepository)
	if !ok {
		return nil, ErrCompanyScopedOpenLinesRequired
	}
	return repository.ListOpenLinesForCompany(l.companyID, partnerID)
}
