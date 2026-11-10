package subledgers

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type FixedAssetLedger struct {
	repo      FixedAssetLedgerRepository
	companyID uuid.UUID
}

type FixedAssetLedgerRepository interface {
	FixedAssetRepository
	AtomicFixedAssetDraftRepository
	AtomicFixedAssetConfirmationRepository
}

func NewFixedAssetLedger(repo FixedAssetLedgerRepository, _ JournalCreator) *FixedAssetLedger {
	return &FixedAssetLedger{repo: repo}
}

func NewFixedAssetLedgerForCompany(repo FixedAssetLedgerRepository, _ JournalCreator, companyID uuid.UUID) *FixedAssetLedger {
	return &FixedAssetLedger{repo: repo, companyID: companyID}
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
		asset.RemainingUsefulLifeMonths < 0 || asset.RemainingUsefulLifeMonths > asset.UsefulLifeMonths ||
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
	if asset.RevaluationAdjustment.Currency() == "" {
		zero, err := financialtypes.Zero(asset.HistoricalCost.Currency())
		if err != nil {
			return err
		}
		asset.RevaluationAdjustment = zero
	}
	if asset.RevaluationSurplus.Currency() == "" {
		zero, err := financialtypes.Zero(asset.HistoricalCost.Currency())
		if err != nil {
			return err
		}
		asset.RevaluationSurplus = zero
	}
	if asset.AccumulatedDepreciation.Currency() != asset.HistoricalCost.Currency() ||
		asset.AccumulatedImpairment.Currency() != asset.HistoricalCost.Currency() ||
		asset.RevaluationAdjustment.Currency() != asset.HistoricalCost.Currency() ||
		asset.RevaluationSurplus.Currency() != asset.HistoricalCost.Currency() ||
		asset.RevaluationSurplus.IsNegative() {
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
	bookValue, err = bookValue.Add(asset.RevaluationAdjustment)
	if err != nil {
		return err
	}
	if asset.CurrentBookValue.Currency() == "" {
		asset.CurrentBookValue = bookValue
	}
	if asset.CurrentBookValue.Currency() != asset.HistoricalCost.Currency() ||
		asset.CurrentBookValue.IsNegative() || asset.AccumulatedDepreciation.IsNegative() ||
		asset.AccumulatedImpairment.IsNegative() ||
		!asset.CurrentBookValue.Equal(bookValue) {
		return ErrInvalidAsset
	}
	for i := range asset.Components {
		component := &asset.Components[i]
		if component.ID == uuid.Nil {
			component.ID = uuid.New()
		}
		if component.InServiceDate.IsZero() {
			component.InServiceDate = asset.InServiceDate
		}
		if component.AccumulatedDepreciation.Currency() == "" {
			component.AccumulatedDepreciation, err = financialtypes.Zero(asset.HistoricalCost.Currency())
			if err != nil {
				return err
			}
		}
	}
	if len(asset.Components) > 0 {
		if _, err := depreciationComponents(asset); err != nil {
			return err
		}
	}
	if asset.RemainingUsefulLifeMonths == 0 && asset.CurrentBookValue.GreaterThan(asset.SalvageValue) {
		remainingBasis, err := asset.CurrentBookValue.Subtract(asset.SalvageValue)
		if err != nil {
			return err
		}
		originalBasis, err := asset.HistoricalCost.Subtract(asset.SalvageValue)
		if err != nil {
			return err
		}
		if originalBasis.Amount().IsZero() {
			return ErrInvalidAsset
		}
		months := remainingBasis.Amount().Mul(decimal.NewFromInt(int64(asset.UsefulLifeMonths))).
			DivRound(originalBasis.Amount(), 12).Ceil().IntPart()
		if months < 1 {
			months = 1
		}
		if months > int64(asset.UsefulLifeMonths) {
			months = int64(asset.UsefulLifeMonths)
		}
		asset.RemainingUsefulLifeMonths = int(months)
	}
	return l.repo.SaveFixedAsset(context.Background(), asset)
}

// DepreciationAttachment ينفذ تحديث سجل الأصل الثابت ذرياً
// مع ترحيل قيد الاستهلاك. يحل محل نمط PrepareDepreciation + ConfirmDepreciation
// المرجع: IAS 16.43 — الاستهلاك يجب أن يُثبت بالتزامن مع القيد
type DepreciationAttachment struct {
	AssetID      uuid.UUID
	CompanyID    uuid.UUID
	Amount       financialtypes.Money
	AsOf         time.Time
	UpdatedAsset FixedAsset
}

func (a *DepreciationAttachment) AttachmentKind() string {
	return "fixed_asset_depreciation"
}

func (a *DepreciationAttachment) ValidateAttachment(ctx context.Context, entry posting.JournalEntry) error {
	if a.AssetID == uuid.Nil || a.CompanyID == uuid.Nil {
		return ErrInvalidAsset
	}
	if a.CompanyID != entry.CompanyID {
		return ErrCompanyMismatch
	}
	return nil
}

type FixedAssetMemoryStore interface {
	SaveFixedAsset(ctx context.Context, asset FixedAsset) error
}

func (a *DepreciationAttachment) ExecuteAtomicMutation(ctx context.Context, tx posting.AtomicTransaction, entry posting.JournalEntry) error {
	err := tx.CustomMutation(ctx, "memory", func(state any) error {
		store, ok := state.(FixedAssetMemoryStore)
		if !ok {
			return posting.ErrUnsupportedMutationBackend
		}
		return store.SaveFixedAsset(ctx, a.UpdatedAsset)
	})
	if err == nil {
		return nil
	}
	if !errors.Is(err, posting.ErrUnsupportedMutationBackend) {
		return err
	}

	data, err := json.Marshal(a.UpdatedAsset)
	if err != nil {
		return err
	}
	return tx.Exec(ctx,
		`UPDATE fixed_assets SET asset_data = $1 WHERE company_id = $2 AND id = $3`,
		data, a.CompanyID, a.AssetID,
	)
}

// DepreciateAssetAtomic يولد قيد الاستهلاك ويرفقه بملحق ذري
// لتحديث سجل الأصل — في معاملة واحدة عبر PostEntryWithAttachment.
// المرجع: IAS 16.43–62
func (l *FixedAssetLedger) DepreciateAssetAtomic(
	ctx context.Context,
	engine *posting.Engine,
	assetID, journalID uuid.UUID,
	asOf time.Time,
	posterID string,
) (posting.JournalEntry, error) {
	if journalID == uuid.Nil {
		return posting.JournalEntry{}, ErrInvalidAsset
	}
	asset, err := l.repo.GetFixedAsset(ctx, l.companyID, assetID)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	if asset.DisposedAt != nil {
		return posting.JournalEntry{}, ErrInvalidAsset
	}
	if !asset.LastDepreciationDate.IsZero() &&
		asset.LastDepreciationDate.UTC().Format("2006-01") == asOf.UTC().Format("2006-01") {
		return posting.JournalEntry{}, ErrAlreadyExists
	}
	if !asset.LastDepreciationDate.IsZero() && asOf.Before(asset.LastDepreciationDate) {
		return posting.JournalEntry{}, ErrInvalidAsset
	}
	expense, updated, err := depreciationUpdate(asset, asOf)
	if err != nil {
		return posting.JournalEntry{}, err
	}

	lines, err := depreciationJournalLines(asset, asOf, expense)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	entry := posting.JournalEntry{
		CompanyID: asset.CompanyID, ID: uuid.New(), JournalID: journalID, EntryDate: asOf, Date: asOf,
		Status: posting.EntryDraft, CreatedAt: time.Now().UTC(),
		DocumentReference: depreciationReference(asset.ID, asOf),
		Lines:             lines,
	}
	if err := engine.CreateDraftContext(ctx, entry); err != nil {
		return posting.JournalEntry{}, err
	}

	attachment := &DepreciationAttachment{
		AssetID: assetID, CompanyID: l.companyID,
		Amount: expense, AsOf: asOf, UpdatedAsset: updated,
	}

	if err := engine.PostEntryWithAttachmentContext(ctx, &entry, posterID, attachment); err != nil {
		return posting.JournalEntry{}, err
	}
	return entry, nil
}

// Deprecated: Use DepreciateAssetAtomic instead.
func (l *FixedAssetLedger) MonthlyDepreciation(assetID uuid.UUID, journalID uuid.UUID, asOf time.Time) (posting.JournalEntry, financialtypes.Money, error) {
	if journalID == uuid.Nil {
		return posting.JournalEntry{}, financialtypes.Money{}, ErrInvalidAsset
	}
	asset, err := l.repo.GetFixedAsset(context.Background(), l.companyID, assetID)
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
	expense, _, err := depreciationUpdate(asset, asOf)
	if err != nil {
		return posting.JournalEntry{}, financialtypes.Money{}, err
	}

	lines, err := depreciationJournalLines(asset, asOf, expense)
	if err != nil {
		return posting.JournalEntry{}, financialtypes.Money{}, err
	}
	entry := posting.JournalEntry{
		CompanyID: asset.CompanyID, ID: uuid.New(), JournalID: journalID, EntryDate: asOf, Date: asOf,
		Status: posting.EntryDraft, CreatedAt: time.Now().UTC(),
		DocumentReference: depreciationReference(asset.ID, asOf),
		Lines:             lines,
	}
	if err := entry.Validate(); err != nil {
		return posting.JournalEntry{}, financialtypes.Money{}, err
	}
	if err := l.saveAssetDraft(asset, entry); err != nil {
		return posting.JournalEntry{}, financialtypes.Money{}, err
	}
	return entry, expense, nil
}

// Deprecated: Use DepreciateAssetAtomic instead.
func (l *FixedAssetLedger) ConfirmDepreciation(assetID, postedEntryID uuid.UUID, depAmount financialtypes.Money, asOf time.Time) error {
	asset, err := l.repo.GetFixedAsset(context.Background(), l.companyID, assetID)
	if err != nil {
		return err
	}
	if asset.DisposedAt != nil {
		return ErrInvalidAsset
	}
	if !asset.LastDepreciationDate.IsZero() {
		if asset.LastDepreciationDate.UTC().Format("2006-01") == asOf.UTC().Format("2006-01") {
			return ErrAlreadyExists
		}
		if asOf.Before(asset.LastDepreciationDate) {
			return ErrInvalidAsset
		}
	}
	expectedAmount, updated, err := depreciationUpdate(asset, asOf)
	if err != nil {
		return err
	}
	if !depAmount.Equal(expectedAmount) {
		return ErrInvalidAsset
	}
	lines, err := depreciationJournalLines(asset, asOf, expectedAmount)
	if err != nil {
		return err
	}
	expectedEntry := posting.JournalEntry{
		CompanyID: asset.CompanyID, ID: postedEntryID, EntryDate: asOf, Date: asOf,
		Status: posting.EntryPosted, DocumentReference: depreciationReference(asset.ID, asOf),
		Lines: lines,
	}
	return l.confirmAssetMutation(asset, updated, expectedEntry)
}

func depreciationUpdate(asset FixedAsset, asOf time.Time) (financialtypes.Money, FixedAsset, error) {
	expense, updated, err := componentDepreciationUpdate(asset, asOf)
	if err != nil {
		return financialtypes.Money{}, FixedAsset{}, err
	}
	if len(asset.Components) == 0 {
		expense, err = calculateDepreciationAmount(asset, asOf)
		if err != nil {
			return financialtypes.Money{}, FixedAsset{}, err
		}
		updated = asset
	}
	if expense.Amount().IsZero() {
		return financialtypes.Money{}, FixedAsset{}, ErrInvalidAsset
	}
	assetAccumulated, err := asset.AccumulatedDepreciation.Add(expense)
	if err != nil {
		return financialtypes.Money{}, FixedAsset{}, err
	}
	bookValue, err := asset.CurrentBookValue.Subtract(expense)
	if err != nil {
		return financialtypes.Money{}, FixedAsset{}, err
	}
	if bookValue.LessThan(asset.SalvageValue) {
		expense, err = asset.CurrentBookValue.Subtract(asset.SalvageValue)
		if err != nil {
			return financialtypes.Money{}, FixedAsset{}, err
		}
		assetAccumulated, err = asset.AccumulatedDepreciation.Add(expense)
		if err != nil {
			return financialtypes.Money{}, FixedAsset{}, err
		}
		bookValue = asset.SalvageValue
	}
	updated.AccumulatedDepreciation = assetAccumulated
	updated.CurrentBookValue = bookValue
	updated.LastDepreciationDate = asOf.UTC()
	if len(asset.Components) == 0 {
		updated.RemainingUsefulLifeMonths = remainingUsefulLifeMonths(asset, asOf)
	}
	return expense, updated, nil
}

func componentDepreciationUpdate(asset FixedAsset, asOf time.Time) (financialtypes.Money, FixedAsset, error) {
	if len(asset.Components) == 0 {
		return financialtypes.Money{}, asset, nil
	}
	schedule, err := GenerateDepreciationSchedule(asset)
	if err != nil {
		return financialtypes.Money{}, FixedAsset{}, err
	}
	total, err := financialtypes.Zero(asset.HistoricalCost.Currency())
	if err != nil {
		return financialtypes.Money{}, FixedAsset{}, err
	}
	updated := cloneFixedAsset(asset)
	for _, line := range schedule {
		if line.Period.Year() != asOf.UTC().Year() || line.Period.Month() != asOf.UTC().Month() {
			continue
		}
		total, err = total.Add(line.Amount)
		if err != nil {
			return financialtypes.Money{}, FixedAsset{}, err
		}
		for i := range updated.Components {
			component := &updated.Components[i]
			if component.ID != line.ComponentID {
				continue
			}
			if component.AccumulatedDepreciation.Currency() == "" {
				component.AccumulatedDepreciation, err = financialtypes.Zero(line.Amount.Currency())
				if err != nil {
					return financialtypes.Money{}, FixedAsset{}, err
				}
			}
			component.AccumulatedDepreciation, err = component.AccumulatedDepreciation.Add(line.Amount)
			if err != nil {
				return financialtypes.Money{}, FixedAsset{}, err
			}
			break
		}
	}
	return total, updated, nil
}

func depreciationJournalLines(asset FixedAsset, asOf time.Time, total financialtypes.Money) ([]posting.JournalEntryLine, error) {
	zero, err := financialtypes.Zero(total.Currency())
	if err != nil {
		return nil, err
	}
	lines := make([]posting.JournalEntryLine, 0)
	if len(asset.Components) == 0 {
		if asset.ExpenseAccountID == uuid.Nil || asset.AccumulatedAccountID == uuid.Nil {
			return nil, ErrInvalidAsset
		}
		return []posting.JournalEntryLine{
			{CompanyID: asset.CompanyID, ID: uuid.New(), AccountID: asset.ExpenseAccountID, Debit: total, Credit: zero},
			{CompanyID: asset.CompanyID, ID: uuid.New(), AccountID: asset.AccumulatedAccountID, Debit: zero, Credit: total},
		}, nil
	}
	schedule, err := GenerateDepreciationSchedule(asset)
	if err != nil {
		return nil, err
	}
	for _, line := range schedule {
		if line.Period.Year() != asOf.UTC().Year() || line.Period.Month() != asOf.UTC().Month() {
			continue
		}
		var component *AssetComponent
		for i := range asset.Components {
			if asset.Components[i].ID == line.ComponentID {
				component = &asset.Components[i]
				break
			}
		}
		if component == nil {
			return nil, ErrInvalidAsset
		}
		expenseAccount := component.ExpenseAccountID
		if expenseAccount == uuid.Nil {
			expenseAccount = asset.ExpenseAccountID
		}
		accumulatedAccount := component.AccumulatedAccountID
		if accumulatedAccount == uuid.Nil {
			accumulatedAccount = asset.AccumulatedAccountID
		}
		if expenseAccount == uuid.Nil || accumulatedAccount == uuid.Nil {
			return nil, ErrInvalidAsset
		}
		lines = append(lines,
			posting.JournalEntryLine{CompanyID: asset.CompanyID, ID: uuid.New(), AccountID: expenseAccount, Debit: line.Amount, Credit: zero},
			posting.JournalEntryLine{CompanyID: asset.CompanyID, ID: uuid.New(), AccountID: accumulatedAccount, Debit: zero, Credit: line.Amount},
		)
	}
	if len(lines) == 0 {
		return nil, ErrInvalidAsset
	}
	return lines, nil
}

func depreciationReference(assetID uuid.UUID, asOf time.Time) string {
	return "FIXED_ASSET:DEPRECIATION:" + assetID.String() + ":" + asOf.UTC().Format("2006-01")
}

func calculateDepreciationAmount(asset FixedAsset, asOf time.Time) (financialtypes.Money, error) {
	if asOf.Before(asset.InServiceDate) {
		return financialtypes.Zero(asset.HistoricalCost.Currency())
	}
	if asset.RevaluationAdjustment.Amount().IsZero() && asset.AccumulatedImpairment.Amount().IsZero() {
		schedule, err := GenerateDepreciationSchedule(asset)
		if err != nil {
			return financialtypes.Money{}, err
		}
		total, err := financialtypes.Zero(asset.HistoricalCost.Currency())
		if err != nil {
			return financialtypes.Money{}, err
		}
		for _, line := range schedule {
			if line.Period.Year() == asOf.UTC().Year() && line.Period.Month() == asOf.UTC().Month() {
				total, err = total.Add(line.Amount)
				if err != nil {
					return financialtypes.Money{}, err
				}
			}
		}
		return total, nil
	}
	zero, err := financialtypes.Zero(asset.HistoricalCost.Currency())
	if err != nil {
		return financialtypes.Money{}, err
	}
	lifeEnd := asset.InServiceDate.AddDate(0, asset.UsefulLifeMonths, 0)
	monthStart := time.Date(asOf.UTC().Year(), asOf.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	periodStart := monthStart
	if periodStart.Before(asset.InServiceDate) {
		periodStart = asset.InServiceDate
	}
	if !asset.LastRevaluationDate.IsZero() && asset.LastRevaluationDate.After(periodStart) &&
		asset.LastRevaluationDate.Before(periodStart.AddDate(0, 1, 0)) {
		periodStart = asset.LastRevaluationDate
	}
	periodEnd := monthStart.AddDate(0, 1, 0)
	if periodEnd.After(lifeEnd) {
		periodEnd = lifeEnd
	}
	if !periodEnd.After(periodStart) {
		return zero, nil
	}
	activeDays := decimal.NewFromInt(int64(periodEnd.Sub(periodStart).Hours() / 24))
	switch asset.Method {
	case StraightLine:
		depreciable, err := asset.CurrentBookValue.Subtract(asset.SalvageValue)
		if err != nil {
			return financialtypes.Money{}, err
		}
		remainingDays := decimal.NewFromInt(int64(lifeEnd.Sub(periodStart).Hours() / 24))
		if remainingDays.IsZero() || !depreciable.Amount().IsPositive() {
			return zero, nil
		}
		amount := depreciable.Amount().Mul(activeDays).Div(remainingDays)
		money, err := financialtypes.NewMoney(amount, asset.HistoricalCost.Currency())
		if err != nil {
			return financialtypes.Money{}, err
		}
		return money.RoundForPosting(), nil
	case DecliningBalance:
		rate := asset.DecliningRate
		if rate.IsZero() {
			rate = decimal.NewFromFloat(2.0).Div(decimal.NewFromInt(int64(asset.UsefulLifeMonths)))
		}
		daysInPeriod := decimal.NewFromInt(int64(monthStart.AddDate(0, 1, 0).Sub(monthStart).Hours() / 24))
		if daysInPeriod.IsZero() {
			return zero, nil
		}
		amount := asset.CurrentBookValue.Amount().Mul(rate).Mul(activeDays).Div(daysInPeriod)
		remaining, err := asset.CurrentBookValue.Subtract(asset.SalvageValue)
		if err != nil {
			return financialtypes.Money{}, err
		}
		if amount.GreaterThan(remaining.Amount()) || !periodEnd.Before(lifeEnd) {
			amount = remaining.Amount()
		}
		money, err := financialtypes.NewMoney(amount, asset.HistoricalCost.Currency())
		if err != nil {
			return financialtypes.Money{}, err
		}
		return money.RoundForPosting(), nil
	default:
		return financialtypes.Money{}, ErrInvalidAsset
	}
}

func remainingUsefulLifeMonths(asset FixedAsset, asOf time.Time) int {
	lifeEnd := asset.InServiceDate.AddDate(0, asset.UsefulLifeMonths, 0)
	cursor := time.Date(asOf.UTC().Year(), asOf.UTC().Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
	months := 0
	for cursor.Before(lifeEnd) {
		months++
		cursor = cursor.AddDate(0, 1, 0)
	}
	return months
}

func (l *FixedAssetLedger) Impair(assetID uuid.UUID, loss financialtypes.Money) error {
	_, err := l.ImpairAsset(assetID, loss, uuid.Nil, time.Now().UTC())
	return err
}

func (l *FixedAssetLedger) PostImpairment(assetID uuid.UUID, recoverableAmount financialtypes.Money, journalID uuid.UUID, asOf time.Time) (posting.JournalEntry, error) {
	asset, err := l.repo.GetFixedAsset(context.Background(), l.companyID, assetID)
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
	asset, err := l.repo.GetFixedAsset(context.Background(), l.companyID, assetID)
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
	entryID := uuid.New()
	entry := posting.JournalEntry{
		CompanyID:         asset.CompanyID,
		ID:                entryID,
		JournalID:         journalID,
		EntryDate:         asOf,
		Date:              asOf,
		Status:            posting.EntryDraft,
		DocumentReference: impairmentReference(asset.ID, entryID),
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
	if err := l.saveAssetDraft(asset, entry); err != nil {
		return posting.JournalEntry{}, err
	}
	return entry, nil
}

func impairmentReference(assetID, entryID uuid.UUID) string {
	return "FIXED_ASSET:IMPAIRMENT:" + assetID.String() + ":" + entryID.String()
}

func (l *FixedAssetLedger) ConfirmImpairment(assetID, postedEntryID uuid.UUID, loss financialtypes.Money, asOf time.Time) error {
	asset, err := l.repo.GetFixedAsset(context.Background(), l.companyID, assetID)
	if err != nil {
		return err
	}
	if asset.DisposedAt != nil || loss.Currency() != asset.CurrentBookValue.Currency() ||
		loss.Amount().IsNegative() || loss.Amount().IsZero() || loss.GreaterThan(asset.CurrentBookValue) {
		return ErrInvalidAsset
	}
	updated := asset
	updated.CurrentBookValue, err = asset.CurrentBookValue.Subtract(loss)
	if err != nil {
		return err
	}
	if updated.AccumulatedImpairment.Currency() == "" {
		updated.AccumulatedImpairment, err = financialtypes.Zero(loss.Currency())
		if err != nil {
			return err
		}
	}
	updated.AccumulatedImpairment, err = updated.AccumulatedImpairment.Add(loss)
	if err != nil {
		return err
	}
	expectedEntry, err := impairmentEntry(asset, postedEntryID, loss, asOf)
	if err != nil {
		return err
	}
	return l.confirmAssetMutation(asset, updated, expectedEntry)
}

func (l *FixedAssetLedger) DisposeAsset(assetID uuid.UUID, disposalDate time.Time, proceeds financialtypes.Money, journalID uuid.UUID) (posting.JournalEntry, error) {
	if journalID == uuid.Nil {
		return posting.JournalEntry{}, ErrInvalidAsset
	}
	asset, err := l.repo.GetFixedAsset(context.Background(), l.companyID, assetID)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	if asset.DisposedAt != nil || proceeds.Currency() != asset.HistoricalCost.Currency() || proceeds.IsNegative() {
		return posting.JournalEntry{}, ErrInvalidAsset
	}
	lines, err := disposalLines(asset, proceeds)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	entryID := uuid.New()
	entry := posting.JournalEntry{
		ID: entryID, CompanyID: asset.CompanyID, JournalID: journalID,
		EntryDate: disposalDate, Date: disposalDate, Status: posting.EntryDraft,
		DocumentReference: disposalReference(asset.ID, entryID), CreatedAt: time.Now().UTC(), Lines: lines,
	}
	if err := entry.Validate(); err != nil {
		return posting.JournalEntry{}, err
	}
	if err := l.saveAssetDraft(asset, entry); err != nil {
		return posting.JournalEntry{}, err
	}
	return entry, nil
}

func disposalLines(asset FixedAsset, proceeds financialtypes.Money) ([]posting.JournalEntryLine, error) {
	grossCarryingAmount, err := asset.HistoricalCost.Add(asset.RevaluationAdjustment)
	if err != nil || grossCarryingAmount.IsNegative() {
		return nil, ErrInvalidAsset
	}
	netBookValue, err := grossCarryingAmount.Subtract(asset.AccumulatedDepreciation)
	if err == nil && asset.AccumulatedImpairment.Currency() != "" {
		netBookValue, err = netBookValue.Subtract(asset.AccumulatedImpairment)
	}
	if err != nil {
		return nil, err
	}
	if netBookValue.IsNegative() || !netBookValue.Equal(asset.CurrentBookValue) {
		return nil, ErrInvalidAsset
	}
	zero, err := financialtypes.Zero(asset.HistoricalCost.Currency())
	if err != nil {
		return nil, err
	}
	lines := []posting.JournalEntryLine{
		{CompanyID: asset.CompanyID, ID: uuid.New(), AccountID: asset.AccumulatedAccountID, Debit: asset.AccumulatedDepreciation, Credit: zero},
		{CompanyID: asset.CompanyID, ID: uuid.New(), AccountID: asset.AssetAccountID, Credit: grossCarryingAmount, Debit: zero},
	}
	if asset.AccumulatedImpairment.Currency() != "" && !asset.AccumulatedImpairment.Amount().IsZero() {
		accountID := asset.AccumulatedImpairmentAccountID
		if accountID == uuid.Nil {
			accountID = asset.AccumulatedAccountID
		}
		lines = append(lines, posting.JournalEntryLine{
			CompanyID: asset.CompanyID, ID: uuid.New(), AccountID: accountID,
			Debit: asset.AccumulatedImpairment, Credit: zero,
		})
	}
	if proceeds.GreaterThan(zero) {
		lines = append(lines, posting.JournalEntryLine{CompanyID: asset.CompanyID, ID: uuid.New(), AccountID: asset.DisposalCashAccountID, Debit: proceeds, Credit: zero})
	}
	gainLoss, err := proceeds.Subtract(netBookValue)
	if err != nil {
		return nil, err
	}
	if gainLoss.GreaterThan(zero) {
		lines = append(lines, posting.JournalEntryLine{CompanyID: asset.CompanyID, ID: uuid.New(), AccountID: asset.DisposalGainAccountID, Credit: gainLoss, Debit: zero})
	} else if gainLoss.IsNegative() {
		loss, err := gainLoss.Multiply(decimal.NewFromInt(-1))
		if err != nil {
			return nil, err
		}
		lines = append(lines, posting.JournalEntryLine{CompanyID: asset.CompanyID, ID: uuid.New(), AccountID: asset.DisposalLossAccountID, Debit: loss, Credit: zero})
	}
	return lines, nil
}

func disposalReference(assetID, entryID uuid.UUID) string {
	return "FIXED_ASSET:DISPOSAL:" + assetID.String() + ":" + entryID.String()
}

func (l *FixedAssetLedger) ConfirmDisposal(assetID, postedEntryID uuid.UUID, disposalDate time.Time, proceeds financialtypes.Money) error {
	asset, err := l.repo.GetFixedAsset(context.Background(), l.companyID, assetID)
	if err != nil {
		return err
	}
	if asset.DisposedAt != nil || proceeds.Currency() != asset.HistoricalCost.Currency() || proceeds.IsNegative() {
		return ErrInvalidAsset
	}
	lines, err := disposalLines(asset, proceeds)
	if err != nil {
		return err
	}
	disposedAt := disposalDate.UTC()
	updated := asset
	updated.DisposedAt = &disposedAt
	expectedEntry := posting.JournalEntry{
		ID: postedEntryID, CompanyID: asset.CompanyID, EntryDate: disposalDate, Date: disposalDate,
		Status: posting.EntryPosted, DocumentReference: disposalReference(asset.ID, postedEntryID), Lines: lines,
	}
	return l.confirmAssetMutation(asset, updated, expectedEntry)
}

func impairmentEntry(asset FixedAsset, entryID uuid.UUID, loss financialtypes.Money, asOf time.Time) (posting.JournalEntry, error) {
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
	if expenseAccount == uuid.Nil || accumulatedAccount == uuid.Nil {
		return posting.JournalEntry{}, ErrInvalidAsset
	}
	zero, err := financialtypes.Zero(loss.Currency())
	if err != nil {
		return posting.JournalEntry{}, err
	}
	return posting.JournalEntry{
		CompanyID: asset.CompanyID, ID: entryID, EntryDate: asOf, Date: asOf,
		Status: posting.EntryPosted, DocumentReference: impairmentReference(asset.ID, entryID),
		Lines: []posting.JournalEntryLine{
			{CompanyID: asset.CompanyID, AccountID: expenseAccount, Debit: loss, Credit: zero},
			{CompanyID: asset.CompanyID, AccountID: accumulatedAccount, Debit: zero, Credit: loss},
		},
	}, nil
}

func (l *FixedAssetLedger) saveAssetDraft(expected FixedAsset, entry posting.JournalEntry) error {
	return l.repo.SaveFixedAssetDraft(context.Background(), expected, entry)
}

func (l *FixedAssetLedger) confirmAssetMutation(expected, updated FixedAsset, expectedEntry posting.JournalEntry) error {
	for _, appliedID := range expected.AppliedEntryIDs {
		if appliedID == expectedEntry.ID {
			return ErrAlreadyExists
		}
	}
	updated.AppliedEntryIDs = append(append([]uuid.UUID(nil), expected.AppliedEntryIDs...), expectedEntry.ID)
	return l.repo.ConfirmFixedAssetMutation(context.Background(), expected, updated, expectedEntry)
}
