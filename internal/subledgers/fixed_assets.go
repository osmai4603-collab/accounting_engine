package subledgers

import (
	"time"

	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type FixedAssetLedger struct {
	repo Repository
}

func NewFixedAssetLedger(repo Repository, _ JournalCreator) *FixedAssetLedger {
	return &FixedAssetLedger{repo: repo}
}

func (l *FixedAssetLedger) RegisterAsset(asset FixedAsset) error {
	if asset.CompanyID == uuid.Nil {
		return ErrInvalidAsset
	}
	if asset.ID == uuid.Nil {
		asset.ID = uuid.New()
	}
	if asset.HistoricalCost.Currency() == "" ||
		asset.SalvageValue.Currency() != asset.HistoricalCost.Currency() ||
		asset.SalvageValue.GreaterThan(asset.HistoricalCost) ||
		asset.UsefulLifeMonths <= 0 ||
		(asset.Method != StraightLine && asset.Method != DecliningBalance) ||
		(asset.Method == DecliningBalance &&
			(asset.DecliningRate.IsNegative() || asset.DecliningRate.GreaterThan(decimal.NewFromInt(1)))) {
		return ErrInvalidAsset
	}
	if asset.AccumulatedDepreciation.Currency() == "" {
		zero, err := financialtypes.Zero(asset.HistoricalCost.Currency())
		if err != nil {
			return err
		}
		asset.AccumulatedDepreciation = zero
	}
	if asset.AccumulatedImpairment.Currency() == "" {
		zero, err := financialtypes.Zero(asset.HistoricalCost.Currency())
		if err != nil {
			return err
		}
		asset.AccumulatedImpairment = zero
	}
	if asset.AccumulatedDepreciation.Currency() != asset.HistoricalCost.Currency() ||
		asset.AccumulatedImpairment.Currency() != asset.HistoricalCost.Currency() {
		return ErrInvalidAsset
	}
	bookValue, err := asset.HistoricalCost.Subtract(asset.AccumulatedDepreciation)
	if err != nil {
		return err
	}
	bookValue, err = bookValue.Subtract(asset.AccumulatedImpairment)
	if err != nil {
		return err
	}
	if asset.CurrentBookValue.Currency() == "" {
		asset.CurrentBookValue = bookValue
	}
	if asset.CurrentBookValue.Currency() != asset.HistoricalCost.Currency() ||
		asset.CurrentBookValue.IsNegative() || asset.AccumulatedDepreciation.IsNegative() ||
		asset.AccumulatedImpairment.IsNegative() ||
		asset.CurrentBookValue.GreaterThan(asset.HistoricalCost) ||
		!asset.CurrentBookValue.Equal(bookValue) {
		return ErrInvalidAsset
	}
	return l.repo.SaveFixedAsset(asset)
}

func (l *FixedAssetLedger) MonthlyDepreciation(assetID uuid.UUID, journalID uuid.UUID, asOf time.Time) (posting.JournalEntry, financialtypes.Money, error) {
	if journalID == uuid.Nil {
		return posting.JournalEntry{}, financialtypes.Money{}, ErrInvalidAsset
	}
	asset, err := l.repo.GetFixedAsset(assetID)
	if err != nil {
		return posting.JournalEntry{}, financialtypes.Money{}, err
	}
	if asset.DisposedAt != nil {
		return posting.JournalEntry{}, financialtypes.Money{}, ErrInvalidAsset
	}
	if !asset.LastDepreciationDate.IsZero() &&
		asset.LastDepreciationDate.UTC().Format("2006-01") == asOf.UTC().Format("2006-01") {
		return posting.JournalEntry{}, financialtypes.Money{}, ErrAlreadyExists
	}
	if !asset.LastDepreciationDate.IsZero() && asOf.Before(asset.LastDepreciationDate) {
		return posting.JournalEntry{}, financialtypes.Money{}, ErrInvalidAsset
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
	updatedAsset := asset
	updatedAsset.AccumulatedDepreciation = assetAccumulated
	updatedAsset.CurrentBookValue = bookValue
	updatedAsset.LastDepreciationDate = asOf.UTC()

	entry := posting.JournalEntry{
		CompanyID: asset.CompanyID, ID: uuid.New(), JournalID: journalID, EntryDate: asOf, Date: asOf,
		Status: posting.EntryDraft, CreatedAt: time.Now().UTC(),
		Lines: []posting.JournalEntryLine{
			{CompanyID: asset.CompanyID, ID: uuid.New(), AccountID: asset.ExpenseAccountID, Debit: expense},
			{CompanyID: asset.CompanyID, ID: uuid.New(), AccountID: asset.AccumulatedAccountID, Credit: expense},
		},
	}
	if err := entry.Validate(); err != nil {
		return posting.JournalEntry{}, financialtypes.Money{}, err
	}
	if err := l.saveAssetAndDraft(asset, updatedAsset, entry); err != nil {
		return posting.JournalEntry{}, financialtypes.Money{}, err
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

func (l *FixedAssetLedger) PostImpairment(assetID uuid.UUID, recoverableAmount financialtypes.Money, journalID uuid.UUID, asOf time.Time) (posting.JournalEntry, error) {
	asset, err := l.repo.GetFixedAsset(assetID)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	if asset.DisposedAt != nil || recoverableAmount.Currency() != asset.CurrentBookValue.Currency() ||
		recoverableAmount.IsNegative() || recoverableAmount.GreaterThan(asset.CurrentBookValue) {
		return posting.JournalEntry{}, ErrInvalidAsset
	}
	loss, err := asset.CurrentBookValue.Subtract(recoverableAmount)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	if loss.Amount().IsZero() {
		return posting.JournalEntry{}, ErrInvalidAsset
	}
	return l.ImpairAsset(assetID, loss, journalID, asOf)
}

func (l *FixedAssetLedger) ImpairAsset(assetID uuid.UUID, loss financialtypes.Money, journalID uuid.UUID, asOf time.Time) (posting.JournalEntry, error) {
	if journalID == uuid.Nil {
		return posting.JournalEntry{}, ErrInvalidAsset
	}
	asset, err := l.repo.GetFixedAsset(assetID)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	if asset.DisposedAt != nil {
		return posting.JournalEntry{}, ErrInvalidAsset
	}
	if loss.Currency() != asset.CurrentBookValue.Currency() || loss.Amount().IsNegative() || loss.Amount().IsZero() ||
		loss.GreaterThan(asset.CurrentBookValue) {
		return posting.JournalEntry{}, ErrInvalidAsset
	}
	updatedAsset := asset
	updatedAsset.CurrentBookValue, err = asset.CurrentBookValue.Subtract(loss)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	if updatedAsset.AccumulatedImpairment.Currency() == "" {
		updatedAsset.AccumulatedImpairment, err = financialtypes.Zero(loss.Currency())
		if err != nil {
			return posting.JournalEntry{}, err
		}
	}
	updatedAsset.AccumulatedImpairment, err = updatedAsset.AccumulatedImpairment.Add(loss)
	if err != nil {
		return posting.JournalEntry{}, err
	}

	expenseAccount := asset.ImpairmentAccountID
	if expenseAccount == uuid.Nil {
		expenseAccount = asset.ExpenseAccountID
	}
	accumulatedAccount := asset.AccumulatedAccountID
	if asset.AccumulatedImpairmentAccountID != uuid.Nil {
		accumulatedAccount = asset.AccumulatedImpairmentAccountID
	}
	if accumulatedAccount == uuid.Nil {
		accumulatedAccount = asset.AssetAccountID
	}

	zero, err := financialtypes.Zero(loss.Currency())
	if err != nil {
		return posting.JournalEntry{}, err
	}
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
	if expenseAccount == uuid.Nil || accumulatedAccount == uuid.Nil {
		return posting.JournalEntry{}, ErrInvalidAsset
	}
	if err := entry.Validate(); err != nil {
		return posting.JournalEntry{}, err
	}
	if err := l.saveAssetAndDraft(asset, updatedAsset, entry); err != nil {
		return posting.JournalEntry{}, err
	}
	return entry, nil
}

func (l *FixedAssetLedger) DisposeAsset(assetID uuid.UUID, disposalDate time.Time, proceeds financialtypes.Money, journalID uuid.UUID) (posting.JournalEntry, error) {
	if journalID == uuid.Nil {
		return posting.JournalEntry{}, ErrInvalidAsset
	}
	asset, err := l.repo.GetFixedAsset(assetID)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	if asset.DisposedAt != nil || proceeds.Currency() != asset.HistoricalCost.Currency() || proceeds.IsNegative() {
		return posting.JournalEntry{}, ErrInvalidAsset
	}
	netBookValue, err := asset.HistoricalCost.Subtract(asset.AccumulatedDepreciation)
	if err == nil && asset.AccumulatedImpairment.Currency() != "" {
		netBookValue, err = netBookValue.Subtract(asset.AccumulatedImpairment)
	}
	if err != nil {
		return posting.JournalEntry{}, err
	}
	if netBookValue.IsNegative() {
		return posting.JournalEntry{}, ErrInvalidAsset
	}
	zeroCost, err := financialtypes.Zero(asset.HistoricalCost.Currency())
	if err != nil {
		return posting.JournalEntry{}, err
	}
	zeroProceeds, err := financialtypes.Zero(asset.HistoricalCost.Currency())
	if err != nil {
		return posting.JournalEntry{}, err
	}
	lines := []posting.JournalEntryLine{
		{CompanyID: asset.CompanyID, ID: uuid.New(), AccountID: asset.AccumulatedAccountID, Debit: asset.AccumulatedDepreciation, Credit: zeroCost},
		{CompanyID: asset.CompanyID, ID: uuid.New(), AccountID: asset.AssetAccountID, Credit: asset.HistoricalCost, Debit: zeroCost},
	}
	if asset.AccumulatedImpairment.Currency() != "" && !asset.AccumulatedImpairment.Amount().IsZero() {
		accumulatedImpairmentAccount := asset.AccumulatedImpairmentAccountID
		if accumulatedImpairmentAccount == uuid.Nil {
			accumulatedImpairmentAccount = asset.AccumulatedAccountID
		}
		lines = append(lines, posting.JournalEntryLine{
			CompanyID: asset.CompanyID, ID: uuid.New(), AccountID: accumulatedImpairmentAccount,
			Debit: asset.AccumulatedImpairment, Credit: zeroCost,
		})
	}
	if proceeds.GreaterThan(zeroProceeds) {
		lines = append(lines, posting.JournalEntryLine{CompanyID: asset.CompanyID, ID: uuid.New(), AccountID: asset.DisposalCashAccountID, Debit: proceeds, Credit: zeroProceeds})
	}
	gainLoss, err := proceeds.Subtract(netBookValue)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	if gainLoss.GreaterThan(zeroProceeds) {
		lines = append(lines, posting.JournalEntryLine{CompanyID: asset.CompanyID, ID: uuid.New(), AccountID: asset.DisposalGainAccountID, Credit: gainLoss, Debit: zeroProceeds})
	} else if gainLoss.IsNegative() {
		loss, err := gainLoss.Multiply(decimal.NewFromInt(-1))
		if err != nil {
			return posting.JournalEntry{}, err
		}
		lines = append(lines, posting.JournalEntryLine{CompanyID: asset.CompanyID, ID: uuid.New(), AccountID: asset.DisposalLossAccountID, Debit: loss, Credit: zeroProceeds})
	}
	entry := posting.JournalEntry{ID: uuid.New(), CompanyID: asset.CompanyID, JournalID: journalID, EntryDate: disposalDate, Date: disposalDate, Status: posting.EntryDraft, CreatedAt: time.Now().UTC(), Lines: lines}
	if err := entry.Validate(); err != nil {
		return posting.JournalEntry{}, err
	}
	disposedAsset := asset
	disposedAt := disposalDate.UTC()
	disposedAsset.DisposedAt = &disposedAt
	if err := l.saveAssetAndDraft(asset, disposedAsset, entry); err != nil {
		return posting.JournalEntry{}, err
	}
	return entry, nil
}

func (l *FixedAssetLedger) saveAssetAndDraft(expected, updated FixedAsset, entry posting.JournalEntry) error {
	repository, ok := l.repo.(AtomicFixedAssetRepository)
	if !ok {
		return ErrAtomicFixedAssetRequired
	}
	return repository.SaveFixedAssetAndDraft(expected, updated, entry)
}
