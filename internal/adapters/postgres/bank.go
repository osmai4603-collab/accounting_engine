package postgres

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/subledgers"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (d *Driver) SaveBankItem(ctx context.Context, item subledgers.BankItem) error {
	if item.ID == uuid.Nil || item.CompanyID == uuid.Nil || item.AccountID == uuid.Nil {
		return subledgers.ErrInvalidOpenLine
	}
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	return d.WithCompanyTx(ctx, item.CompanyID, func(tx pgx.Tx) error {
		result, err := tx.Exec(ctx,
			`INSERT INTO bank_items(company_id,account_id,item_id,item_data) VALUES($1,$2,$3,$4)
			 ON CONFLICT(item_id) DO UPDATE SET item_data=EXCLUDED.item_data
			 WHERE bank_items.company_id=EXCLUDED.company_id AND bank_items.account_id=EXCLUDED.account_id`,
			item.CompanyID, item.AccountID, item.ID, data)
		if err == nil && result.RowsAffected() != 1 {
			return subledgers.ErrCompanyMismatch
		}
		return err
	})
}
func (d *Driver) ListBankItems(ctx context.Context, accountID string) ([]subledgers.BankItem, error) {
	return nil, subledgers.ErrCompanyScopedBankItemsRequired
}

func (d *Driver) ListBankItemsForCompany(ctx context.Context, companyID uuid.UUID, accountID string) ([]subledgers.BankItem, error) {
	result := make([]subledgers.BankItem, 0)
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT item_data FROM bank_items WHERE company_id=$1 AND account_id=$2::uuid`,
			companyID, accountID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var data []byte
			if err := rows.Scan(&data); err != nil {
				return err
			}
			var item subledgers.BankItem
			if err := json.Unmarshal(data, &item); err != nil {
				return err
			}
			result = append(result, item)
		}
		return rows.Err()
	})
	return result, err
}

func (d *Driver) CompareAndSaveBankItems(ctx context.Context, expected, updated []subledgers.BankItem) error {
	if len(expected) == 0 || len(expected) != len(updated) {
		return subledgers.ErrInvalidOpenLine
	}
	companyID := expected[0].CompanyID
	if companyID == uuid.Nil {
		return subledgers.ErrInvalidOpenLine
	}
	tx, err := d.beginCompanyTx(ctx, companyID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	expectedByID := make(map[uuid.UUID]subledgers.BankItem, len(expected))
	ids := make([]uuid.UUID, 0, len(expected))
	for i, item := range expected {
		if item.ID == uuid.Nil || item.CompanyID != companyID || item.AccountID == uuid.Nil ||
			updated[i].ID != item.ID || updated[i].CompanyID != item.CompanyID ||
			updated[i].AccountID != item.AccountID {
			return subledgers.ErrInvalidOpenLine
		}
		if _, exists := expectedByID[item.ID]; exists {
			return subledgers.ErrInvalidOpenLine
		}
		expectedByID[item.ID] = item
		ids = append(ids, item.ID)
	}
	rows, err := tx.Query(ctx,
		`SELECT item_id, company_id, account_id, item_data FROM bank_items
		 WHERE item_id = ANY($1) ORDER BY item_id FOR UPDATE`, ids)
	if err != nil {
		return err
	}
	found := 0
	for rows.Next() {
		var id, companyID, accountID uuid.UUID
		var data []byte
		if err := rows.Scan(&id, &companyID, &accountID, &data); err != nil {
			rows.Close()
			return err
		}
		var current subledgers.BankItem
		if err := json.Unmarshal(data, &current); err != nil {
			rows.Close()
			return err
		}
		oldItem, ok := expectedByID[id]
		if !ok {
			rows.Close()
			return subledgers.ErrInvalidOpenLine
		}
		if companyID != oldItem.CompanyID || accountID != oldItem.AccountID ||
			current.CompanyID != oldItem.CompanyID || current.AccountID != oldItem.AccountID {
			rows.Close()
			return subledgers.ErrCompanyMismatch
		}
		if !reflect.DeepEqual(current, oldItem) {
			rows.Close()
			return subledgers.ErrConcurrentBankItem
		}
		found++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if found != len(expected) {
		return subledgers.ErrNotFound
	}
	for _, item := range updated {
		data, err := json.Marshal(item)
		if err != nil {
			return err
		}
		result, err := tx.Exec(ctx,
			`UPDATE bank_items SET item_data=$4
			 WHERE item_id=$1 AND company_id=$2 AND account_id=$3`,
			item.ID, item.CompanyID, item.AccountID, data)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return subledgers.ErrConcurrentBankItem
		}
	}
	return tx.Commit(ctx)
}

func (d *Driver) CompareAndSaveBankItemsAndDraft(ctx context.Context, expected, updated []subledgers.BankItem, draft posting.JournalEntry) error {
	if len(expected) == 0 || len(expected) != len(updated) {
		return subledgers.ErrInvalidOpenLine
	}
	if draft.ID == uuid.Nil || draft.Status != posting.EntryDraft {
		return posting.ErrInvalidEntry
	}
	if err := draft.Validate(); err != nil {
		return err
	}
	companyID := expected[0].CompanyID
	if companyID == uuid.Nil || draft.CompanyID != companyID {
		return subledgers.ErrInvalidOpenLine
	}
	tx, err := d.beginCompanyTx(ctx, companyID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	expectedByID := make(map[uuid.UUID]subledgers.BankItem, len(expected))
	ids := make([]uuid.UUID, 0, len(expected))
	for i, item := range expected {
		if item.ID == uuid.Nil || item.CompanyID != companyID || item.AccountID == uuid.Nil ||
			updated[i].ID != item.ID || updated[i].CompanyID != item.CompanyID ||
			updated[i].AccountID != item.AccountID || draft.CompanyID != item.CompanyID {
			return subledgers.ErrInvalidOpenLine
		}
		if _, exists := expectedByID[item.ID]; exists {
			return subledgers.ErrInvalidOpenLine
		}
		expectedByID[item.ID] = item
		ids = append(ids, item.ID)
	}
	rows, err := tx.Query(ctx,
		`SELECT item_id, company_id, account_id, item_data FROM bank_items
		 WHERE item_id = ANY($1) ORDER BY item_id FOR UPDATE`, ids)
	if err != nil {
		return err
	}
	found := 0
	for rows.Next() {
		var id, companyID, accountID uuid.UUID
		var data []byte
		if err := rows.Scan(&id, &companyID, &accountID, &data); err != nil {
			rows.Close()
			return err
		}
		var current subledgers.BankItem
		if err := json.Unmarshal(data, &current); err != nil {
			rows.Close()
			return err
		}
		oldItem, ok := expectedByID[id]
		if !ok {
			rows.Close()
			return subledgers.ErrInvalidOpenLine
		}
		if companyID != oldItem.CompanyID || accountID != oldItem.AccountID ||
			current.CompanyID != oldItem.CompanyID || current.AccountID != oldItem.AccountID {
			rows.Close()
			return subledgers.ErrCompanyMismatch
		}
		if !reflect.DeepEqual(current, oldItem) {
			rows.Close()
			return subledgers.ErrConcurrentBankItem
		}
		found++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if found != len(expected) {
		return subledgers.ErrNotFound
	}
	if err := d.saveEntryTx(ctx, tx, draft); err != nil {
		return err
	}
	for _, item := range updated {
		data, err := json.Marshal(item)
		if err != nil {
			return err
		}
		result, err := tx.Exec(ctx,
			`UPDATE bank_items SET item_data=$4
			 WHERE item_id=$1 AND company_id=$2 AND account_id=$3`,
			item.ID, item.CompanyID, item.AccountID, data)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return subledgers.ErrConcurrentBankItem
		}
	}
	return tx.Commit(ctx)
}
