package subledgers

import (
	"time"

	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
)

type TreasuryLedger struct {
	repo Repository
}

func NewTreasuryLedger(repo Repository) *TreasuryLedger {
	return &TreasuryLedger{repo: repo}
}

func (l *TreasuryLedger) RecordItem(accountID uuid.UUID, date time.Time, amount financialtypes.Money, reference string, status BankItemStatus) (BankItem, error) {
	if accountID == uuid.Nil || amount.Currency() == "" || status == "" {
		return BankItem{}, ErrInvalidAmount
	}
	item := BankItem{ID: uuid.New(), AccountID: accountID, Date: date, Amount: amount, Reference: reference, Status: status}
	if err := l.repo.SaveBankItem(item); err != nil {
		return BankItem{}, err
	}
	return item, nil
}

func (l *TreasuryLedger) Reconcile(accountID uuid.UUID, statement []BankItem) (matched []BankItem, pending []BankItem, unrecorded []BankItem, err error) {
	ledger, err := l.repo.ListBankItems(accountID.String())
	if err != nil {
		return nil, nil, nil, err
	}
	statementMatched := make(map[uuid.UUID]bool)
	for _, ledgerItem := range ledger {
		found := false
		for i, statementItem := range statement {
			if statementMatched[statementItem.ID] {
				continue
			}
			if ledgerItem.Reference == statementItem.Reference &&
				ledgerItem.Amount.Equal(statementItem.Amount) &&
				sameDay(ledgerItem.Date, statementItem.Date) {
				statementMatched[statementItem.ID] = true
				ledgerItem.Status = BankItemMatched
				matched = append(matched, ledgerItem)
				found = true
				break
			}
			if i == len(statement)-1 {
				break
			}
		}
		if !found && ledgerItem.Status != BankItemMatched {
			pending = append(pending, ledgerItem)
		}
	}
	for _, statementItem := range statement {
		if !statementMatched[statementItem.ID] {
			unrecorded = append(unrecorded, statementItem)
		}
	}
	return matched, pending, unrecorded, nil
}

func sameDay(a, b time.Time) bool {
	return a.UTC().Format("2006-01-02") == b.UTC().Format("2006-01-02")
}
