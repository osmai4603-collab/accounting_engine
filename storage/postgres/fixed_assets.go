package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/subledgers"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (d *Driver) SaveFixedAsset(ctx context.Context, asset subledgers.FixedAsset) error {
	if asset.CompanyID == uuid.Nil {
		return subledgers.ErrInvalidAsset
	}
	data, err := json.Marshal(asset)
	if err != nil {
		return err
	}
	return d.WithCompanyTx(ctx, asset.CompanyID, func(tx pgx.Tx) error {
		result, err := tx.Exec(ctx,
			`INSERT INTO fixed_assets(id,company_id,asset_data) VALUES($1,$2,$3)
			 ON CONFLICT(id) DO UPDATE SET asset_data=EXCLUDED.asset_data
			 WHERE fixed_assets.company_id=EXCLUDED.company_id`,
			asset.ID, asset.CompanyID, data)
		if err == nil && result.RowsAffected() != 1 {
			return subledgers.ErrCompanyMismatch
		}
		return err
	})
}

func (d *Driver) SaveFixedAssetDraft(ctx context.Context, expected subledgers.FixedAsset, entry posting.JournalEntry) error {
	if expected.ID == uuid.Nil || expected.CompanyID == uuid.Nil || entry.ID == uuid.Nil ||
		entry.CompanyID != expected.CompanyID || entry.Status != posting.EntryDraft {
		return subledgers.ErrInvalidAsset
	}
	tx, err := d.beginCompanyTx(ctx, expected.CompanyID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var currentData []byte
	err = tx.QueryRow(ctx,
		`SELECT asset_data FROM fixed_assets WHERE company_id=$1 AND id=$2 FOR UPDATE`,
		expected.CompanyID, expected.ID).Scan(&currentData)
	if errors.Is(err, pgx.ErrNoRows) {
		return subledgers.ErrNotFound
	}
	if err != nil {
		return err
	}
	var current subledgers.FixedAsset
	if err := json.Unmarshal(currentData, &current); err != nil {
		return err
	}
	if !reflect.DeepEqual(current, expected) {
		return subledgers.ErrConcurrentAssetUpdate
	}
	if strings.HasPrefix(entry.DocumentReference, "FIXED_ASSET:DEPRECIATION:") ||
		strings.HasPrefix(entry.DocumentReference, "FIXED_ASSET:REVALUATION:") {
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (
				SELECT 1 FROM account_moves
				WHERE company_id=$1 AND status IN ('draft', 'posted')
				  AND entry->>'DocumentReference'=$2
			)`,
			entry.CompanyID, entry.DocumentReference).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return subledgers.ErrAlreadyExists
		}
	}
	if err := d.saveEntryTx(ctx, tx, entry); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (d *Driver) ConfirmFixedAssetMutation(ctx context.Context, expected, updated subledgers.FixedAsset, expectedEntry posting.JournalEntry) error {
	if expected.ID == uuid.Nil || expected.CompanyID == uuid.Nil || updated.ID != expected.ID ||
		updated.CompanyID != expected.CompanyID || expectedEntry.ID == uuid.Nil ||
		expectedEntry.CompanyID != expected.CompanyID || expectedEntry.Status != posting.EntryPosted {
		return subledgers.ErrInvalidAsset
	}
	tx, err := d.beginCompanyTx(ctx, expected.CompanyID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var currentData []byte
	err = tx.QueryRow(ctx,
		`SELECT asset_data FROM fixed_assets WHERE company_id=$1 AND id=$2 FOR UPDATE`,
		expected.CompanyID, expected.ID).Scan(&currentData)
	if errors.Is(err, pgx.ErrNoRows) {
		return subledgers.ErrNotFound
	}
	if err != nil {
		return err
	}
	var current subledgers.FixedAsset
	if err := json.Unmarshal(currentData, &current); err != nil {
		return err
	}
	if !reflect.DeepEqual(current, expected) {
		return subledgers.ErrConcurrentAssetUpdate
	}

	var moveStatus posting.EntryStatus
	var entryData []byte
	err = tx.QueryRow(ctx,
		`SELECT status, entry FROM account_moves
		 WHERE company_id=$1 AND id=$2 FOR UPDATE`,
		expectedEntry.CompanyID, expectedEntry.ID).Scan(&moveStatus, &entryData)
	if errors.Is(err, pgx.ErrNoRows) {
		return posting.ErrEntryNotFound
	}
	if err != nil {
		return err
	}
	var actualEntry posting.JournalEntry
	if err := json.Unmarshal(entryData, &actualEntry); err != nil {
		return err
	}
	if moveStatus != posting.EntryPosted || !subledgers.FixedAssetEntryMatches(actualEntry, expectedEntry) {
		return posting.ErrEntryNotPosted
	}

	updatedData, err := json.Marshal(updated)
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx,
		`UPDATE fixed_assets SET asset_data=$3 WHERE company_id=$1 AND id=$2`,
		updated.CompanyID, updated.ID, updatedData)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return subledgers.ErrCompanyMismatch
	}
	return tx.Commit(ctx)
}

func (d *Driver) GetFixedAsset(ctx context.Context, companyID, id uuid.UUID) (subledgers.FixedAsset, error) {
	var asset subledgers.FixedAsset
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var data []byte
		err := tx.QueryRow(ctx, `SELECT asset_data FROM fixed_assets WHERE company_id=$1 AND id=$2`,
			companyID, id).Scan(&data)
		if errors.Is(err, pgx.ErrNoRows) {
			return subledgers.ErrNotFound
		}
		if err != nil {
			return err
		}
		return json.Unmarshal(data, &asset)
	})
	return asset, err
}

func (d *Driver) ListFixedAssets(ctx context.Context) ([]subledgers.FixedAsset, error) {
	return nil, masterdata.ErrCompanyScopeRequired
}

func (d *Driver) ListFixedAssetsForCompany(ctx context.Context, companyID uuid.UUID) ([]subledgers.FixedAsset, error) {
	assets := make([]subledgers.FixedAsset, 0)
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT asset_data FROM fixed_assets WHERE company_id=$1`, companyID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var data []byte
			if err := rows.Scan(&data); err != nil {
				return err
			}
			var asset subledgers.FixedAsset
			if err := json.Unmarshal(data, &asset); err != nil {
				return err
			}
			assets = append(assets, asset)
		}
		return rows.Err()
	})
	return assets, err
}
func (d *Driver) ListFixedAssetsForCompanyContext(ctx context.Context, companyID uuid.UUID) ([]subledgers.FixedAsset, error) {
	return d.ListFixedAssetsForCompany(ctx, companyID)
}
