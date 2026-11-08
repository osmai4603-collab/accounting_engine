package subledgers

import (
	"time"

	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type FixedAssetLedger struct {
	repo    Repository
	creator JournalCreator
}

func NewFixedAssetLedger(repo Repository, creator JournalCreator) *FixedAssetLedger {
	return &FixedAssetLedger{repo: repo, creator: creator}
}

func (l *FixedAssetLedger) RegisterAsset(asset FixedAsset) error {
	if asset.ID == uuid.Nil {
		asset.ID = uuid.New()
	}
	if asset.HistoricalCost.Currency() == "" ||
		asset.SalvageValue.Currency() != asset.HistoricalCost.Currency() ||
		asset.UsefulLifeMonths <= 0 ||
		(asset.Method != StraightLine && asset.Method != DecliningBalance) {
		return ErrInvalidAsset
	}
	if asset.CurrentBookValue.Currency() == "" {
		asset.CurrentBookValue = asset.HistoricalCost
	}
	if asset.AccumulatedDepreciation.Currency() == "" {
		asset.AccumulatedDepreciation, _ = financialtypes.Zero(asset.HistoricalCost.Currency())
	}
	return l.repo.SaveFixedAsset(asset)
}

func (l *FixedAssetLedger) MonthlyDepreciation(assetID uuid.UUID, journalID uuid.UUID, asOf time.Time) (posting.JournalEntry, financialtypes.Money, error) {
	asset, err := l.repo.GetFixedAsset(assetID)
	if err != nil {
		return posting.JournalEntry{}, financialtypes.Money{}, err
	}
	expense, err := l.calculateDepreciation(asset, asOf)
	if err != nil {
		return posting.JournalEntry{}, financialtypes.Money{}, err
	}
	assetAccumulated, err := asset.AccumulatedDepreciation.Add(expense)
	if err != nil {
		return posting.JournalEntry{}, financialtypes.Money{}, err
	}
	bookValue, err := asset.CurrentBookValue.Subtract(expense)
	if err != nil {
		return posting.JournalEntry{}, financialtypes.Money{}, err
	}
	if bookValue.LessThan(asset.SalvageValue) {
		expense, err = asset.CurrentBookValue.Subtract(asset.SalvageValue)
		if err != nil {
			return posting.JournalEntry{}, financialtypes.Money{}, err
		}
		assetAccumulated, err = asset.AccumulatedDepreciation.Add(expense)
		if err != nil {
			return posting.JournalEntry{}, financialtypes.Money{}, err
		}
		bookValue = asset.SalvageValue
	}
	asset.AccumulatedDepreciation = assetAccumulated
	asset.CurrentBookValue = bookValue
	if err := l.repo.SaveFixedAsset(asset); err != nil {
		return posting.JournalEntry{}, financialtypes.Money{}, err
	}

	entry := posting.JournalEntry{
		ID: uuid.New(), JournalID: journalID, EntryDate: asOf, Date: asOf,
		Status: posting.EntryDraft, CreatedAt: time.Now().UTC(),
		Lines: []posting.JournalEntryLine{
			{ID: uuid.New(), AccountID: asset.ExpenseAccountID, Debit: expense},
			{ID: uuid.New(), AccountID: asset.AccumulatedAccountID, Credit: expense},
		},
	}
	if err := entry.Validate(); err != nil {
		return posting.JournalEntry{}, financialtypes.Money{}, err
	}
	if l.creator != nil {
		if err := l.creator.CreateDraft(entry); err != nil {
			return posting.JournalEntry{}, financialtypes.Money{}, err
		}
	}
	return entry, expense, nil
}

func (l *FixedAssetLedger) calculateDepreciation(asset FixedAsset, asOf time.Time) (financialtypes.Money, error) {
	if asOf.Before(asset.InServiceDate) {
		return financialtypes.Zero(asset.HistoricalCost.Currency())
	}
	switch asset.Method {
	case StraightLine:
		depreciable, err := asset.HistoricalCost.Subtract(asset.SalvageValue)
		if err != nil {
			return financialtypes.Money{}, err
		}
		return depreciable.Divide(decimal.NewFromInt(int64(asset.UsefulLifeMonths)))
	case DecliningBalance:
		rate := asset.DecliningRate
		if rate.IsZero() {
			rate = decimal.NewFromFloat(2.0).Div(decimal.NewFromInt(int64(asset.UsefulLifeMonths)))
		}
		return asset.CurrentBookValue.Multiply(rate)
	default:
		return financialtypes.Money{}, ErrInvalidAsset
	}
}

func (l *FixedAssetLedger) Impair(assetID uuid.UUID, loss financialtypes.Money) error {
	asset, err := l.repo.GetFixedAsset(assetID)
	if err != nil {
		return err
	}
	if loss.Currency() != asset.CurrentBookValue.Currency() || loss.Amount().IsNegative() ||
		loss.GreaterThan(asset.CurrentBookValue) {
		return ErrInvalidAsset
	}
	asset.CurrentBookValue, err = asset.CurrentBookValue.Subtract(loss)
	return l.repo.SaveFixedAsset(asset)
}
