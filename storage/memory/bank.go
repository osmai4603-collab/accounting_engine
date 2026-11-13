package memory

import (
	"context"
	"reflect"

	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/subledgers"

	"github.com/google/uuid"
)

func (d *Driver) SaveBankItem(ctx context.Context, item subledgers.BankItem) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, current := range d.bank[item.AccountID.String()] {
		if current.ID == item.ID {
			if current.CompanyID != item.CompanyID {
				return subledgers.ErrCompanyMismatch
			}
			return subledgers.ErrAlreadyExists
		}
	}
	d.bank[item.AccountID.String()] = append(d.bank[item.AccountID.String()], item)
	return nil
}
func (d *Driver) ListBankItems(ctx context.Context, accountID string) ([]subledgers.BankItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return append([]subledgers.BankItem(nil), d.bank[accountID]...), nil
}

func (d *Driver) ListBankItemsForCompany(ctx context.Context, companyID uuid.UUID, accountID string) ([]subledgers.BankItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	items := make([]subledgers.BankItem, 0)
	for _, item := range d.bank[accountID] {
		if item.CompanyID == companyID {
			items = append(items, item)
		}
	}
	return items, nil
}

func (d *Driver) CompareAndSaveBankItems(ctx context.Context, expected, updated []subledgers.BankItem) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(expected) != len(updated) {
		return subledgers.ErrInvalidOpenLine
	}
	for i, oldItem := range expected {
		if updated[i].ID != oldItem.ID || updated[i].CompanyID != oldItem.CompanyID ||
			updated[i].AccountID != oldItem.AccountID {
			return subledgers.ErrCompanyMismatch
		}
		items := d.bank[oldItem.AccountID.String()]
		found := false
		for _, current := range items {
			if current.ID != oldItem.ID {
				continue
			}
			found = true
			if !reflect.DeepEqual(current, oldItem) {
				return subledgers.ErrConcurrentBankItem
			}
			break
		}
		if !found {
			return subledgers.ErrNotFound
		}
	}
	for _, updatedItem := range updated {
		items := d.bank[updatedItem.AccountID.String()]
		for i := range items {
			if items[i].ID == updatedItem.ID {
				items[i] = updatedItem
				break
			}
		}
		d.bank[updatedItem.AccountID.String()] = items
	}
	return nil
}

func (d *Driver) CompareAndSaveBankItemsAndDraft(ctx context.Context, expected, updated []subledgers.BankItem, draft posting.JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if draft.ID == uuid.Nil || draft.Status != posting.EntryDraft {
		return posting.ErrInvalidEntry
	}
	if err := draft.Validate(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(expected) != len(updated) {
		return subledgers.ErrInvalidOpenLine
	}
	for i, oldItem := range expected {
		if updated[i].ID != oldItem.ID || updated[i].CompanyID != oldItem.CompanyID ||
			updated[i].AccountID != oldItem.AccountID || draft.CompanyID != oldItem.CompanyID {
			return subledgers.ErrCompanyMismatch
		}
		items := d.bank[oldItem.AccountID.String()]
		found := false
		for _, current := range items {
			if current.ID != oldItem.ID {
				continue
			}
			found = true
			if !reflect.DeepEqual(current, oldItem) {
				return subledgers.ErrConcurrentBankItem
			}
			break
		}
		if !found {
			return subledgers.ErrNotFound
		}
	}
	if current, exists := d.entries[draft.ID]; exists && current.Status != posting.EntryDraft {
		return posting.ErrEntryNotDraft
	}
	for i := range draft.Lines {
		if draft.Lines[i].CompanyID != uuid.Nil && draft.Lines[i].CompanyID != draft.CompanyID {
			return posting.ErrInvalidEntry
		}
		account, ok := d.accounts[draft.Lines[i].AccountID]
		if !ok || account.CompanyID != draft.CompanyID {
			return masterdata.ErrAccountNotFound
		}
		draft.Lines[i].CompanyID = draft.CompanyID
	}
	for _, updatedItem := range updated {
		items := d.bank[updatedItem.AccountID.String()]
		for i := range items {
			if items[i].ID == updatedItem.ID {
				items[i] = updatedItem
				break
			}
		}
		d.bank[updatedItem.AccountID.String()] = items
	}
	d.entries[draft.ID] = cloneEntry(draft)
	return nil
}
