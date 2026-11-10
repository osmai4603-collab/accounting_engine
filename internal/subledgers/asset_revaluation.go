package subledgers

import (
	"context"
	"time"

	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func (l *FixedAssetLedger) RevalueAsset(assetID, journalID uuid.UUID, fairValue financialtypes.Money, asOf time.Time) (posting.JournalEntry, error) {
	return l.RevalueAssetContext(context.Background(), assetID, journalID, fairValue, asOf)
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
