package subledgers

import (
	"time"

	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
)

type TreasuryLedger struct {
	repo      Repository
	companyID uuid.UUID
}

func NewTreasuryLedger(repo Repository) *TreasuryLedger {
	return &TreasuryLedger{repo: repo}
}

func NewTreasuryLedgerForCompany(repo Repository, companyID uuid.UUID) *TreasuryLedger {
	return &TreasuryLedger{repo: repo, companyID: companyID}
}

func (l *TreasuryLedger) RecordItem(accountID uuid.UUID, date time.Time, amount financialtypes.Money, reference string, status BankItemStatus) (BankItem, error) {
	if l.companyID == uuid.Nil {
		return l.recordItem(uuid.Nil, accountID, date, amount, reference, status)
	}
	return l.RecordItemForCompany(l.companyID, accountID, date, amount, reference, status)
}

func (l *TreasuryLedger) RecordItemForCompany(companyID, accountID uuid.UUID, date time.Time, amount financialtypes.Money, reference string, status BankItemStatus) (BankItem, error) {
	if companyID == uuid.Nil {
		return BankItem{}, ErrInvalidOpenLine
	}
	return l.recordItem(companyID, accountID, date, amount, reference, status)
}

func (l *TreasuryLedger) recordItem(companyID, accountID uuid.UUID, date time.Time, amount financialtypes.Money, reference string, status BankItemStatus) (BankItem, error) {
	if accountID == uuid.Nil || amount.Currency() == "" || status == "" {
		return BankItem{}, ErrInvalidAmount
	}
	item := BankItem{
		CompanyID: companyID, ID: uuid.New(), AccountID: accountID,
		Date: date, Amount: amount, Reference: reference, Status: status,
	}
	if err := l.repo.SaveBankItem(item); err != nil {
		return BankItem{}, err
	}
	return item, nil
}

func (l *TreasuryLedger) Reconcile(accountID uuid.UUID, statement []BankItem) (matched []BankItem, pending []BankItem, unrecorded []BankItem, err error) {
	return l.ReconcileWithTolerance(accountID, statement, 0)
}

func (l *TreasuryLedger) ReconcileWithTolerance(accountID uuid.UUID, statement []BankItem, toleranceDays int) (matched []BankItem, pending []BankItem, unrecorded []BankItem, err error) {
	if l.companyID != uuid.Nil {
		for _, item := range statement {
			if item.CompanyID != uuid.Nil && item.CompanyID != l.companyID {
				return nil, nil, nil, ErrCompanyMismatch
			}
		}
	}
	ledger, err := l.listBankItems(accountID.String())
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
		repository, ok := l.repo.(ConditionalBankItemRepository)
		if !ok {
			return nil, nil, nil, ErrAtomicBankReconciliationRequired
		}
		if err := repository.CompareAndSaveBankItems(expectedMatched, updatedMatched); err != nil {
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

func (l *TreasuryLedger) listBankItems(accountID string) ([]BankItem, error) {
	if l.companyID == uuid.Nil {
		return l.repo.ListBankItems(accountID)
	}
	repository, ok := l.repo.(CompanyBankItemRepository)
	if !ok {
		return nil, ErrCompanyScopedBankItemsRequired
	}
	return repository.ListBankItemsForCompany(l.companyID, accountID)
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
