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
	if !asset.LastDepreciationDate.IsZero() &&
		asset.LastDepreciationDate.UTC().Format("2006-01") == asOf.UTC().Format("2006-01") {
		return posting.JournalEntry{}, financialtypes.Money{}, ErrAlreadyExists
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
	asset.LastDepreciationDate = asOf.UTC()
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
	_, err := l.ImpairAsset(assetID, loss, uuid.Nil, time.Now().UTC())
	return err
}

func (l *FixedAssetLedger) ImpairAsset(assetID uuid.UUID, loss financialtypes.Money, journalID uuid.UUID, asOf time.Time) (posting.JournalEntry, error) {
	asset, err := l.repo.GetFixedAsset(assetID)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	if loss.Currency() != asset.CurrentBookValue.Currency() || loss.Amount().IsNegative() ||
		loss.GreaterThan(asset.CurrentBookValue) {
		return posting.JournalEntry{}, ErrInvalidAsset
	}
	asset.CurrentBookValue, err = asset.CurrentBookValue.Subtract(loss)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	if err := l.repo.SaveFixedAsset(asset); err != nil {
		return posting.JournalEntry{}, err
	}

	expenseAccount := asset.ImpairmentAccountID
	if expenseAccount == uuid.Nil {
		expenseAccount = asset.ExpenseAccountID
	}
	accumulatedAccount := asset.AccumulatedAccountID
	if accumulatedAccount == uuid.Nil {
		accumulatedAccount = asset.AssetAccountID
	}

	zero, _ := financialtypes.Zero(loss.Currency())
	entry := posting.JournalEntry{
		CompanyID:         asset.CompanyID,
		ID:                uuid.New(),
		JournalID:         journalID,
		EntryDate:         asOf,
		Date:              asOf,
		Status:            posting.EntryDraft,
		DocumentReference: "IMPAIRMENT: " + asset.ID.String(),
		CreatedAt:         time.Now().UTC(),
		Lines: []posting.JournalEntryLine{
			{CompanyID: asset.CompanyID, ID: uuid.New(), AccountID: expenseAccount, Debit: loss, Credit: zero},
			{CompanyID: asset.CompanyID, ID: uuid.New(), AccountID: accumulatedAccount, Debit: zero, Credit: loss},
		},
	}
	if journalID != uuid.Nil && expenseAccount != uuid.Nil && accumulatedAccount != uuid.Nil {
		if err := entry.Validate(); err != nil {
			return posting.JournalEntry{}, err
		}
		if l.creator != nil {
			if err := l.creator.CreateDraft(entry); err != nil {
				return posting.JournalEntry{}, err
			}
		}
	}
	return entry, nil
}

func (l *FixedAssetLedger) DisposeAsset(assetID uuid.UUID, disposalDate time.Time, proceeds financialtypes.Money, journalID uuid.UUID) (posting.JournalEntry, error) {
	asset, err := l.repo.GetFixedAsset(assetID)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	netBookValue, err := asset.HistoricalCost.Subtract(asset.AccumulatedDepreciation)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	zeroCost, _ := financialtypes.Zero(asset.HistoricalCost.Currency())
	zeroProceeds, _ := financialtypes.Zero(asset.HistoricalCost.Currency())
	lines := []posting.JournalEntryLine{
		{ID: uuid.New(), AccountID: asset.AccumulatedAccountID, Debit: asset.AccumulatedDepreciation, Credit: zeroCost},
		{ID: uuid.New(), AccountID: asset.AssetAccountID, Credit: asset.HistoricalCost, Debit: zeroCost},
	}
	if proceeds.GreaterThan(zeroProceeds) {
		lines = append(lines, posting.JournalEntryLine{ID: uuid.New(), AccountID: asset.DisposalCashAccountID, Debit: proceeds, Credit: zeroProceeds})
	}
	gainLoss, err := proceeds.Subtract(netBookValue)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	if gainLoss.GreaterThan(zeroProceeds) {
		lines = append(lines, posting.JournalEntryLine{ID: uuid.New(), AccountID: asset.DisposalGainAccountID, Credit: gainLoss, Debit: zeroProceeds})
	} else if gainLoss.IsNegative() {
		loss, err := gainLoss.Multiply(decimal.NewFromInt(-1))
		if err != nil {
			return posting.JournalEntry{}, err
		}
		lines = append(lines, posting.JournalEntryLine{ID: uuid.New(), AccountID: asset.DisposalLossAccountID, Debit: loss, Credit: zeroProceeds})
	}
	entry := posting.JournalEntry{ID: uuid.New(), CompanyID: asset.CompanyID, JournalID: journalID, EntryDate: disposalDate, Date: disposalDate, Status: posting.EntryDraft, CreatedAt: time.Now().UTC(), Lines: lines}
	if err := entry.Validate(); err != nil {
		return posting.JournalEntry{}, err
	}
	if l.creator != nil {
		if err := l.creator.CreateDraft(entry); err != nil {
			return posting.JournalEntry{}, err
		}
	}
	return entry, nil
}
