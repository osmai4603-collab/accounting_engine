package memory

import (
	"context"
	"reflect"
	"strings"

	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/subledgers"

	"github.com/google/uuid"
)

func (d *Driver) SaveFixedAsset(ctx context.Context, asset subledgers.FixedAsset) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if existing, ok := d.assets[asset.ID]; ok && existing.CompanyID != asset.CompanyID {
		return subledgers.ErrCompanyMismatch
	}
	d.assets[asset.ID] = cloneFixedAsset(asset)
	return nil
}

func (d *Driver) SaveFixedAssetDraft(ctx context.Context, expected subledgers.FixedAsset, entry posting.JournalEntry) error {
	return d.saveFixedAssetDraft(ctx, expected, expected, entry)
}

func (d *Driver) SaveFixedAssetOperationDraft(
	ctx context.Context,
	expected, updated subledgers.FixedAsset,
	entry posting.JournalEntry,
) error {
	if err := subledgers.ValidateFixedAssetOperationDraft(expected, updated, entry); err != nil {
		return err
	}
	return d.saveFixedAssetDraft(ctx, expected, updated, entry)
}

func (d *Driver) saveFixedAssetDraft(
	ctx context.Context,
	expected, updated subledgers.FixedAsset,
	entry posting.JournalEntry,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if expected.ID == uuid.Nil || expected.CompanyID == uuid.Nil || entry.ID == uuid.Nil ||
		entry.CompanyID != expected.CompanyID || entry.Status != posting.EntryDraft {
		return subledgers.ErrInvalidAsset
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	currentAsset, exists := d.assets[expected.ID]
	if !exists {
		return subledgers.ErrNotFound
	}
	if !reflect.DeepEqual(currentAsset, expected) {
		return subledgers.ErrConcurrentAssetUpdate
	}
	if expected.PendingOperation != "" || (updated.PendingOperation != "" &&
		(updated.PendingOperationEntryID != entry.ID ||
			(updated.PendingOperation != "DISPOSAL" && updated.PendingOperation != "IMPAIRMENT"))) {
		return subledgers.ErrAlreadyExists
	}
	if _, exists := d.entries[entry.ID]; exists {
		return subledgers.ErrAlreadyExists
	}
	if strings.HasPrefix(entry.DocumentReference, "FIXED_ASSET:") {
		for _, current := range d.entries {
			if current.DocumentReference == entry.DocumentReference &&
				(current.Status == posting.EntryDraft || current.Status == posting.EntryPosted) {
				return subledgers.ErrAlreadyExists
			}
		}
	}
	for i := range entry.Lines {
		if entry.Lines[i].CompanyID != uuid.Nil && entry.Lines[i].CompanyID != entry.CompanyID {
			return posting.ErrInvalidEntry
		}
		account, ok := d.accounts[entry.Lines[i].AccountID]
		if !ok || account.CompanyID != entry.CompanyID {
			return masterdata.ErrAccountNotFound
		}
		entry.Lines[i].CompanyID = entry.CompanyID
	}
	d.entries[entry.ID] = cloneEntry(entry)
	if updated.PendingOperation != "" {
		d.assets[updated.ID] = cloneFixedAsset(updated)
	}
	return nil
}

func (d *Driver) ConfirmFixedAssetMutation(ctx context.Context, expected, updated subledgers.FixedAsset, expectedEntry posting.JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if expected.ID == uuid.Nil || expected.CompanyID == uuid.Nil || updated.ID != expected.ID ||
		updated.CompanyID != expected.CompanyID || expectedEntry.ID == uuid.Nil ||
		expectedEntry.CompanyID != expected.CompanyID || expectedEntry.Status != posting.EntryPosted {
		return subledgers.ErrInvalidAsset
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	currentAsset, exists := d.assets[expected.ID]
	if !exists {
		return subledgers.ErrNotFound
	}
	if !reflect.DeepEqual(currentAsset, expected) {
		return subledgers.ErrConcurrentAssetUpdate
	}
	if expected.PendingOperation != "" &&
		(expected.PendingOperationEntryID != expectedEntry.ID ||
			updated.PendingOperation != "" || updated.PendingOperationEntryID != uuid.Nil) {
		return subledgers.ErrConcurrentAssetUpdate
	}
	actualEntry, exists := d.entries[expectedEntry.ID]
	if !exists {
		return posting.ErrEntryNotFound
	}
	if !subledgers.FixedAssetEntryMatches(actualEntry, expectedEntry) {
		return posting.ErrEntryNotPosted
	}
	d.assets[updated.ID] = cloneFixedAsset(updated)
	return nil
}

func (d *Driver) GetFixedAsset(ctx context.Context, companyID, id uuid.UUID) (subledgers.FixedAsset, error) {
	if err := ctx.Err(); err != nil {
		return subledgers.FixedAsset{}, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	asset, ok := d.assets[id]
	if !ok || asset.CompanyID != companyID {
		return subledgers.FixedAsset{}, subledgers.ErrNotFound
	}
	if asset.DisposedAt != nil {
		disposedAt := *asset.DisposedAt
		asset.DisposedAt = &disposedAt
	}
	return asset, nil
}

func (d *Driver) ListFixedAssets(ctx context.Context) ([]subledgers.FixedAsset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	assets := make([]subledgers.FixedAsset, 0, len(d.assets))
	for _, asset := range d.assets {
		if asset.DisposedAt != nil {
			disposedAt := *asset.DisposedAt
			asset.DisposedAt = &disposedAt
		}
		assets = append(assets, asset)
	}
	return assets, nil
}

func (d *Driver) ListFixedAssetsForCompany(ctx context.Context, companyID uuid.UUID) ([]subledgers.FixedAsset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	assets := make([]subledgers.FixedAsset, 0)
	for _, asset := range d.assets {
		if asset.CompanyID == companyID {
			assets = append(assets, asset)
		}
	}
	return assets, nil
}
func (d *Driver) ListFixedAssetsForCompanyContext(ctx context.Context, companyID uuid.UUID) ([]subledgers.FixedAsset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return d.ListFixedAssetsForCompany(ctx, companyID)
}
