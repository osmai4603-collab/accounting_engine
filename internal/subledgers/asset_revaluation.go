package subledgers

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

type RevaluationAttachment struct {
	AssetID       uuid.UUID
	CompanyID     uuid.UUID
	FairValue     financialtypes.Money
	AsOf          time.Time
	EntryID       uuid.UUID
	expectedAsset FixedAsset
	UpdatedAsset  FixedAsset
}

func (a *RevaluationAttachment) AttachmentKind() string {
	return "fixed_asset_revaluation"
}

func (a *RevaluationAttachment) ValidateAttachment(ctx context.Context, entry posting.JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.AssetID == uuid.Nil || a.CompanyID == uuid.Nil || a.AsOf.IsZero() ||
		a.expectedAsset.ID != a.AssetID || a.expectedAsset.CompanyID != a.CompanyID ||
		entry.ID == uuid.Nil || entry.ID != a.EntryID ||
		entry.CompanyID != a.CompanyID || entry.Status != posting.EntryDraft ||
		!entry.EntryDate.Equal(a.AsOf) || !entry.Date.Equal(a.AsOf) ||
		entry.DocumentReference != revaluationReference(a.AssetID, a.AsOf) {
		return ErrInvalidAsset
	}
	_, updated, lines, err := revaluationUpdate(a.expectedAsset, a.FairValue, a.AsOf)
	if err != nil {
		return err
	}
	updated.AppliedEntryIDs = append(append([]uuid.UUID(nil), a.expectedAsset.AppliedEntryIDs...), entry.ID)
	if !reflect.DeepEqual(a.UpdatedAsset, updated) || !fixedAssetDraftLinesMatch(entry.Lines, lines, a.CompanyID) {
		return ErrInvalidAsset
	}
	return nil
}

func (a *RevaluationAttachment) ExecuteAtomicMutation(
	ctx context.Context,
	tx posting.AtomicTransaction,
	entry posting.JournalEntry,
) error {
	if entry.Status != posting.EntryPosted ||
		entry.ID != a.EntryID ||
		entry.CompanyID != a.CompanyID ||
		entry.DocumentReference != revaluationReference(a.AssetID, a.AsOf) ||
		!entry.EntryDate.Equal(a.AsOf) || !entry.Date.Equal(a.AsOf) {
		return ErrInvalidAsset
	}
	_, _, lines, err := revaluationUpdate(a.expectedAsset, a.FairValue, a.AsOf)
	if err != nil || !fixedAssetDraftLinesMatch(entry.Lines, lines, a.CompanyID) {
		return ErrInvalidAsset
	}
	err = tx.CustomMutation(ctx, "memory", func(state any) error {
		store, ok := state.(DepreciatedAssetMemoryStore)
		if !ok {
			return posting.ErrUnsupportedMutationBackend
		}
		return store.UpdateDepreciatedAsset(ctx, a.expectedAsset, a.UpdatedAsset)
	})
	if err == nil || !errors.Is(err, posting.ErrUnsupportedMutationBackend) {
		return err
	}
	expectedData, err := json.Marshal(a.expectedAsset)
	if err != nil {
		return err
	}
	updatedData, err := json.Marshal(a.UpdatedAsset)
	if err != nil {
		return err
	}
	var updatedID uuid.UUID
	err = tx.QueryRow(ctx, `UPDATE fixed_assets SET asset_data=$1::jsonb
		WHERE company_id=$2 AND id=$3 AND asset_data=$4::jsonb RETURNING id`,
		updatedData, a.CompanyID, a.AssetID, expectedData).Scan(&updatedID)
	if err != nil {
		return errors.Join(ErrConcurrentAssetUpdate, err)
	}
	return nil
}

func fixedAssetDraftLinesMatch(actual, expected []posting.JournalEntryLine, companyID uuid.UUID) bool {
	if len(actual) != len(expected) {
		return false
	}
	for i := range actual {
		if (actual[i].CompanyID != uuid.Nil && actual[i].CompanyID != companyID) ||
			actual[i].AccountID != expected[i].AccountID ||
			!actual[i].Debit.Equal(expected[i].Debit) || !actual[i].Credit.Equal(expected[i].Credit) {
			return false
		}
	}
	return true
}

// Deprecated: use RevalueAssetContext.
func (l *FixedAssetLedger) RevalueAsset(assetID, journalID uuid.UUID, fairValue financialtypes.Money, asOf time.Time) (posting.JournalEntry, error) {
	return l.RevalueAssetContext(context.Background(), assetID, journalID, fairValue, asOf)
}

func (l *FixedAssetLedger) RevalueAssetAtomic(
	ctx context.Context,
	engine *posting.Engine,
	assetID, journalID uuid.UUID,
	fairValue financialtypes.Money,
	asOf time.Time,
	posterID string,
) (posting.JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return posting.JournalEntry{}, err
	}
	if assetID == uuid.Nil || journalID == uuid.Nil {
		return posting.JournalEntry{}, ErrInvalidAsset
	}
	asset, err := l.repo.GetFixedAsset(ctx, l.companyID, assetID)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	_, updated, lines, err := revaluationUpdate(asset, fairValue, asOf)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	entry := posting.JournalEntry{
		CompanyID: asset.CompanyID, ID: uuid.New(), JournalID: journalID,
		EntryDate: asOf, Date: asOf, Status: posting.EntryDraft,
		DocumentReference: revaluationReference(asset.ID, asOf),
		CreatedAt:         time.Now().UTC(), Lines: lines,
	}
	if err := engine.CreateDraftContext(ctx, entry); err != nil {
		return posting.JournalEntry{}, err
	}
	updated.AppliedEntryIDs = append(append([]uuid.UUID(nil), asset.AppliedEntryIDs...), entry.ID)
	attachment := &RevaluationAttachment{
		AssetID: asset.ID, CompanyID: asset.CompanyID, FairValue: fairValue, AsOf: asOf,
		EntryID:       entry.ID,
		expectedAsset: cloneFixedAsset(asset), UpdatedAsset: updated,
	}
	if err := engine.PostEntryWithAttachmentContext(ctx, &entry, posterID, attachment); err != nil {
		return entry, err
	}
	return entry, nil
}

func (l *FixedAssetLedger) RevalueAssetContext(
	ctx context.Context,
	assetID, journalID uuid.UUID,
	fairValue financialtypes.Money,
	asOf time.Time,
) (posting.JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return posting.JournalEntry{}, err
	}
	if journalID == uuid.Nil {
		return posting.JournalEntry{}, ErrInvalidAsset
	}
	asset, err := l.repo.GetFixedAsset(ctx, l.companyID, assetID)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	_, updated, lines, err := revaluationUpdate(asset, fairValue, asOf)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	entry := posting.JournalEntry{
		CompanyID: asset.CompanyID, ID: uuid.New(), JournalID: journalID,
		EntryDate: asOf, Date: asOf, Status: posting.EntryDraft,
		DocumentReference: revaluationReference(asset.ID, asOf),
		CreatedAt:         time.Now().UTC(), Lines: lines,
	}
	if err := entry.Validate(); err != nil {
		return posting.JournalEntry{}, err
	}
	if err := l.saveAssetDraft(ctx, asset, entry); err != nil {
		return posting.JournalEntry{}, err
	}
	_ = updated
	return entry, nil
}

// Deprecated: use ConfirmRevaluationContext.
func (l *FixedAssetLedger) ConfirmRevaluation(assetID, postedEntryID uuid.UUID, fairValue financialtypes.Money, asOf time.Time) error {
	return l.ConfirmRevaluationContext(context.Background(), assetID, postedEntryID, fairValue, asOf)
}

func (l *FixedAssetLedger) ConfirmRevaluationContext(
	ctx context.Context,
	assetID, postedEntryID uuid.UUID,
	fairValue financialtypes.Money,
	asOf time.Time,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	asset, err := l.repo.GetFixedAsset(ctx, l.companyID, assetID)
	if err != nil {
		return err
	}
	_, updated, lines, err := revaluationUpdate(asset, fairValue, asOf)
	if err != nil {
		return err
	}
	expectedEntry := posting.JournalEntry{
		CompanyID: asset.CompanyID, ID: postedEntryID, EntryDate: asOf, Date: asOf,
		Status: posting.EntryPosted, DocumentReference: revaluationReference(asset.ID, asOf),
		Lines: lines,
	}
	return l.confirmAssetMutation(ctx, asset, updated, expectedEntry)
}

func revaluationUpdate(asset FixedAsset, fairValue financialtypes.Money, asOf time.Time) (financialtypes.Money, FixedAsset, []posting.JournalEntryLine, error) {
	if asset.DisposedAt != nil || asset.PendingOperation != "" || asset.ID == uuid.Nil || asOf.IsZero() ||
		fairValue.Currency() != asset.CurrentBookValue.Currency() || fairValue.IsNegative() ||
		(!asset.LastRevaluationDate.IsZero() && !asOf.After(asset.LastRevaluationDate)) ||
		(!asset.LastDepreciationDate.IsZero() && asOf.Before(asset.LastDepreciationDate)) ||
		asset.AssetAccountID == uuid.Nil {
		return financialtypes.Money{}, FixedAsset{}, nil, ErrInvalidAsset
	}
	movement, err := fairValue.Subtract(asset.CurrentBookValue)
	if err != nil || movement.Amount().IsZero() {
		return financialtypes.Money{}, FixedAsset{}, nil, ErrInvalidAsset
	}
	if asset.RevaluationSurplus.Currency() == "" {
		asset.RevaluationSurplus, err = financialtypes.Zero(fairValue.Currency())
		if err != nil {
			return financialtypes.Money{}, FixedAsset{}, nil, err
		}
	}
	zero, err := financialtypes.Zero(fairValue.Currency())
	if err != nil {
		return financialtypes.Money{}, FixedAsset{}, nil, err
	}
	updated := asset
	updated.CurrentBookValue = fairValue
	updated.LastRevaluationDate = asOf.UTC()
	updated.RevaluationAdjustment, err = updated.RevaluationAdjustment.Add(movement)
	if err != nil {
		return financialtypes.Money{}, FixedAsset{}, nil, err
	}
	lines := make([]posting.JournalEntryLine, 0, 4)
	if movement.IsNegative() {
		decrease, err := movement.Multiply(decimal.NewFromInt(-1))
		if err != nil {
			return financialtypes.Money{}, FixedAsset{}, nil, err
		}
		absorbed := asset.RevaluationSurplus
		if absorbed.GreaterThan(decrease) {
			absorbed = decrease
		}
		if !absorbed.Amount().IsZero() {
			if asset.RevaluationSurplusAccountID == uuid.Nil {
				return financialtypes.Money{}, FixedAsset{}, nil, ErrInvalidAsset
			}
			lines = append(lines,
				posting.JournalEntryLine{CompanyID: asset.CompanyID, ID: uuid.New(), AccountID: asset.RevaluationSurplusAccountID, Debit: absorbed, Credit: zero},
				posting.JournalEntryLine{CompanyID: asset.CompanyID, ID: uuid.New(), AccountID: asset.AssetAccountID, Debit: zero, Credit: absorbed},
			)
			updated.RevaluationSurplus, err = asset.RevaluationSurplus.Subtract(absorbed)
			if err != nil {
				return financialtypes.Money{}, FixedAsset{}, nil, err
			}
		}
		excess, err := decrease.Subtract(absorbed)
		if err != nil {
			return financialtypes.Money{}, FixedAsset{}, nil, err
		}
		if !excess.Amount().IsZero() {
			if asset.RevaluationLossAccountID == uuid.Nil {
				return financialtypes.Money{}, FixedAsset{}, nil, ErrInvalidAsset
			}
			lines = append(lines,
				posting.JournalEntryLine{CompanyID: asset.CompanyID, ID: uuid.New(), AccountID: asset.RevaluationLossAccountID, Debit: excess, Credit: zero},
				posting.JournalEntryLine{CompanyID: asset.CompanyID, ID: uuid.New(), AccountID: asset.AssetAccountID, Debit: zero, Credit: excess},
			)
		}
	} else {
		if asset.RevaluationSurplusAccountID == uuid.Nil {
			return financialtypes.Money{}, FixedAsset{}, nil, ErrInvalidAsset
		}
		updated.RevaluationSurplus, err = asset.RevaluationSurplus.Add(movement)
		if err != nil {
			return financialtypes.Money{}, FixedAsset{}, nil, err
		}
		lines = append(lines,
			posting.JournalEntryLine{CompanyID: asset.CompanyID, ID: uuid.New(), AccountID: asset.AssetAccountID, Debit: movement, Credit: zero},
			posting.JournalEntryLine{CompanyID: asset.CompanyID, ID: uuid.New(), AccountID: asset.RevaluationSurplusAccountID, Debit: zero, Credit: movement},
		)
	}
	return movement, updated, lines, nil
}

func revaluationReference(assetID uuid.UUID, asOf time.Time) string {
	return "FIXED_ASSET:REVALUATION:" + assetID.String() + ":" + asOf.UTC().Format("2006-01-02")
}
