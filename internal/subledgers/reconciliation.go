package subledgers

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/osmai4603-collab/accounting_engine/internal/valuation"
	"github.com/shopspring/decimal"
)

type MatchingEngine struct {
	repo      MatchingRepository
	companyID uuid.UUID
}

type MatchingRepository interface {
	OpenLineRepository
	ConditionalOpenLineRepository
	ConditionalOpenLinesAndDraftRepository
}

func NewMatchingEngine(repo MatchingRepository) *MatchingEngine {
	return &MatchingEngine{repo: repo}
}

func NewMatchingEngineForCompany(repo MatchingRepository, companyID uuid.UUID) *MatchingEngine {
	return &MatchingEngine{repo: repo, companyID: companyID}
}

// Deprecated: use MatchContext.
func (m *MatchingEngine) Match(lineID, counterpartID uuid.UUID, amount financialtypes.Money) (string, error) {
	return m.MatchContext(context.Background(), lineID, counterpartID, amount)
}

func (m *MatchingEngine) MatchContext(ctx context.Context, lineID, counterpartID uuid.UUID, amount financialtypes.Money) (string, error) {
	matchingNumber, _, err := m.MatchWithFXContext(ctx, lineID, counterpartID, amount, FXMatchingParams{})
	return matchingNumber, err
}

// Deprecated: use MatchWithFXContext.
func (m *MatchingEngine) MatchWithFX(lineID, counterpartID uuid.UUID, amount financialtypes.Money, params FXMatchingParams) (string, *posting.JournalEntry, error) {
	return m.MatchWithFXContext(context.Background(), lineID, counterpartID, amount, params)
}

func (m *MatchingEngine) MatchWithFXContext(ctx context.Context, lineID, counterpartID uuid.UUID, amount financialtypes.Money, params FXMatchingParams) (string, *posting.JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if lineID == counterpartID {
		return "", nil, ErrUnmatchedLine
	}
	line, err := m.repo.GetOpenLine(ctx, m.companyID, lineID)
	if err != nil {
		return "", nil, err
	}
	counterpart, err := m.repo.GetOpenLine(ctx, m.companyID, counterpartID)
	if err != nil {
		return "", nil, err
	}
	if line.CompanyID == uuid.Nil || counterpart.CompanyID == uuid.Nil ||
		line.ID == uuid.Nil || counterpart.ID == uuid.Nil {
		return "", nil, ErrInvalidOpenLine
	}
	if !validLineKind(line.Kind) || !validLineKind(counterpart.Kind) || line.Kind == counterpart.Kind {
		return "", nil, ErrUnmatchedLine
	}
	if line.CompanyID != counterpart.CompanyID {
		return "", nil, ErrCompanyMismatch
	}
	expectedLine := cloneOpenLine(line)
	expectedCounterpart := cloneOpenLine(counterpart)
	if line.Closed || counterpart.Closed ||
		line.OriginalAmount.Currency() != line.ResidualAmount.Currency() ||
		counterpart.OriginalAmount.Currency() != counterpart.ResidualAmount.Currency() ||
		line.ResidualAmount.Currency() != counterpart.ResidualAmount.Currency() ||
		line.ResidualAmount.Currency() != amount.Currency() ||
		amount.IsNegative() || amount.Amount().IsZero() ||
		line.OriginalAmount.IsNegative() || counterpart.OriginalAmount.IsNegative() ||
		line.ResidualAmount.IsNegative() || counterpart.ResidualAmount.IsNegative() ||
		line.ResidualAmount.GreaterThan(line.OriginalAmount) ||
		counterpart.ResidualAmount.GreaterThan(counterpart.OriginalAmount) ||
		amount.GreaterThan(line.ResidualAmount) || amount.GreaterThan(counterpart.ResidualAmount) {
		return "", nil, ErrInvalidAmount
	}

	matchingNumber := strings.ToUpper(uuid.New().String())
	lineMatches, err := openLineMatches(line)
	if err != nil {
		return "", nil, err
	}
	counterpartMatches, err := openLineMatches(counterpart)
	if err != nil {
		return "", nil, err
	}
	lineFunctionalAmount, err := matchedFunctionalAmount(line, amount)
	if err != nil {
		return "", nil, err
	}
	counterpartFunctionalAmount, err := matchedFunctionalAmount(counterpart, amount)
	if err != nil {
		return "", nil, err
	}
	matchedAt := params.Date
	if matchedAt.IsZero() {
		matchedAt = time.Now().UTC()
	}
	lineMatch := OpenLineMatch{
		MatchingNumber: matchingNumber, CounterpartID: counterpart.ID, Amount: amount,
		FunctionalAmount: lineFunctionalAmount, MatchedAt: matchedAt,
	}
	counterpartMatch := OpenLineMatch{
		MatchingNumber: matchingNumber, CounterpartID: line.ID, Amount: amount,
		FunctionalAmount: counterpartFunctionalAmount, MatchedAt: matchedAt,
	}
	line.Matches = append(lineMatches, lineMatch)
	counterpart.Matches = append(counterpartMatches, counterpartMatch)
	line.MatchHistory = append(line.MatchHistory, lineMatch)
	counterpart.MatchHistory = append(counterpart.MatchHistory, counterpartMatch)
	if err := applyMatch(&line, &counterpart, amount); err != nil {
		return "", nil, err
	}
	syncLegacyMatches(&line)
	syncLegacyMatches(&counterpart)

	// Check if realized FX entry is required
	if !params.OriginalRate.IsZero() && !params.SettlementRate.IsZero() &&
		!params.OriginalRate.Equal(params.SettlementRate) && params.FunctionalCurrency != "" {
		if params.JournalID == uuid.Nil || params.ARAPAccountID == uuid.Nil ||
			params.FXGainAccountID == uuid.Nil || params.FXLossAccountID == uuid.Nil {
			return "", nil, ErrInvalidOpenLine
		}
		fxDiff, err := valuation.RealizedFXDifference(amount.Amount(), params.OriginalRate, params.SettlementRate, params.FunctionalCurrency)
		if err != nil {
			return "", nil, err
		}
		if !fxDiff.Amount().IsZero() {
			entryDate := params.Date
			if entryDate.IsZero() {
				entryDate = time.Now().UTC()
			}
			isGain := false
			if line.Kind == LineReceivable {
				isGain = fxDiff.GreaterThan(mustZero(params.FunctionalCurrency))
			} else {
				isGain = fxDiff.LessThan(mustZero(params.FunctionalCurrency))
			}
			absDiff := fxDiff
			if absDiff.IsNegative() {
				absDiff, _ = absDiff.Multiply(decimal.NewFromInt(-1))
			}
			zero, _ := financialtypes.Zero(params.FunctionalCurrency)

			var lines []posting.JournalEntryLine
			if isGain {
				lines = []posting.JournalEntryLine{
					{CompanyID: line.CompanyID, ID: uuid.New(), AccountID: params.ARAPAccountID, PartnerID: line.PartnerID, Debit: absDiff, Credit: zero},
					{CompanyID: line.CompanyID, ID: uuid.New(), AccountID: params.FXGainAccountID, Credit: absDiff, Debit: zero},
				}
			} else {
				lines = []posting.JournalEntryLine{
					{CompanyID: line.CompanyID, ID: uuid.New(), AccountID: params.FXLossAccountID, Debit: absDiff, Credit: zero},
					{CompanyID: line.CompanyID, ID: uuid.New(), AccountID: params.ARAPAccountID, PartnerID: line.PartnerID, Credit: absDiff, Debit: zero},
				}
			}

			docRef := matchingNumber
			if params.Description != "" {
				docRef = params.Description
			}
			fxEntry := posting.JournalEntry{
				CompanyID:         line.CompanyID,
				ID:                uuid.New(),
				JournalID:         params.JournalID,
				EntryDate:         entryDate,
				Date:              entryDate,
				Status:            posting.EntryDraft,
				DocumentReference: docRef,
				Reference:         docRef,
				CreatedAt:         time.Now().UTC(),
				UpdatedAt:         time.Now().UTC(),
				Lines:             lines,
			}
			if err := fxEntry.Validate(); err != nil {
				return "", nil, err
			}

			if err := m.repo.CompareAndSaveOpenLinesAndDraft(ctx,
				[]OpenLine{expectedLine, expectedCounterpart},
				[]OpenLine{line, counterpart},
				fxEntry,
			); err != nil {
				return "", nil, err
			}
			return matchingNumber, &fxEntry, nil
		}
	}

	if err := m.repo.CompareAndSaveOpenLines(ctx,
		[]OpenLine{expectedLine, expectedCounterpart},
		[]OpenLine{line, counterpart},
	); err != nil {
		return "", nil, err
	}
	return matchingNumber, nil, nil
}

func mustZero(currency string) financialtypes.Money {
	z, _ := financialtypes.Zero(currency)
	return z
}

func validLineKind(kind LineKind) bool {
	return kind == LineReceivable || kind == LinePayable
}

func applyMatch(line, counterpart *OpenLine, amount financialtypes.Money) error {
	var err error
	lineMatchAmount, err := matchedFunctionalAmount(*line, amount)
	if err != nil {
		return err
	}
	counterpartMatchAmount, err := matchedFunctionalAmount(*counterpart, amount)
	if err != nil {
		return err
	}
	line.ResidualAmount, err = line.ResidualAmount.Subtract(amount)
	if err != nil {
		return err
	}
	counterpart.ResidualAmount, err = counterpart.ResidualAmount.Subtract(amount)
	if err != nil {
		return err
	}
	if line.FunctionalResidualAmount.Currency() != "" {
		line.FunctionalResidualAmount, err = line.FunctionalResidualAmount.Subtract(lineMatchAmount)
		if err != nil {
			return err
		}
	}
	if counterpart.FunctionalResidualAmount.Currency() != "" {
		counterpart.FunctionalResidualAmount, err = counterpart.FunctionalResidualAmount.Subtract(counterpartMatchAmount)
		if err != nil {
			return err
		}
	}
	line.Closed = line.ResidualAmount.Amount().IsZero()
	counterpart.Closed = counterpart.ResidualAmount.Amount().IsZero()
	return nil
}

func matchedFunctionalAmount(line OpenLine, amount financialtypes.Money) (financialtypes.Money, error) {
	functional := line.FunctionalResidualAmount
	if functional.Currency() == "" {
		functional = line.ResidualAmount
	}
	if line.ResidualAmount.Currency() == "" || amount.Currency() != line.ResidualAmount.Currency() ||
		line.ResidualAmount.Amount().IsZero() || amount.GreaterThan(line.ResidualAmount) {
		return financialtypes.Money{}, ErrInvalidOpenLine
	}
	if amount.Equal(line.ResidualAmount) {
		return functional, nil
	}
	result, err := financialtypes.NewMoney(
		functional.Amount().Mul(amount.Amount()).Div(line.ResidualAmount.Amount()),
		functional.Currency(),
	)
	if err != nil {
		return financialtypes.Money{}, err
	}
	return result.RoundForPosting(), nil
}

// Deprecated: use UnreconcileContext.
func (m *MatchingEngine) Unreconcile(lineID uuid.UUID) error {
	return m.UnreconcileContext(context.Background(), lineID)
}

func (m *MatchingEngine) UnreconcileContext(ctx context.Context, lineID uuid.UUID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	line, err := m.repo.GetOpenLine(ctx, m.companyID, lineID)
	if err != nil {
		return err
	}
	if line.CompanyID == uuid.Nil {
		return ErrInvalidOpenLine
	}
	matches, err := openLineMatches(line)
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return nil
	}

	expected := map[uuid.UUID]OpenLine{line.ID: cloneOpenLine(line)}
	updated := map[uuid.UUID]OpenLine{line.ID: cloneOpenLine(line)}
	for _, match := range matches {
		counterpart, ok := updated[match.CounterpartID]
		if !ok {
			counterpart, err = m.repo.GetOpenLine(ctx, line.CompanyID, match.CounterpartID)
			if err != nil {
				return err
			}
			expected[counterpart.ID] = cloneOpenLine(counterpart)
			counterpart = cloneOpenLine(counterpart)
		}
		if counterpart.CompanyID != line.CompanyID {
			return ErrCompanyMismatch
		}
		counterpartMatches, err := openLineMatches(counterpart)
		if err != nil {
			return err
		}
		reciprocalIndex := -1
		for i, reciprocal := range counterpartMatches {
			if reciprocal.MatchingNumber == match.MatchingNumber &&
				reciprocal.CounterpartID == line.ID && reciprocal.Amount.Equal(match.Amount) {
				reciprocalIndex = i
				break
			}
		}
		if reciprocalIndex < 0 {
			return ErrInvalidOpenLine
		}
		updatedLine := updated[line.ID]
		updatedLine.ResidualAmount, err = updatedLine.ResidualAmount.Add(match.Amount)
		if err != nil {
			return err
		}
		counterpart.ResidualAmount, err = counterpart.ResidualAmount.Add(match.Amount)
		if err != nil {
			return err
		}
		if updatedLine.ResidualAmount.GreaterThan(updatedLine.OriginalAmount) ||
			counterpart.ResidualAmount.GreaterThan(counterpart.OriginalAmount) {
			return ErrInvalidOpenLine
		}
		if match.FunctionalAmount.Currency() != "" && updatedLine.FunctionalResidualAmount.Currency() != "" {
			updatedLine.FunctionalResidualAmount, err = updatedLine.FunctionalResidualAmount.Add(match.FunctionalAmount)
			if err != nil {
				return err
			}
		}
		for _, reciprocal := range counterpartMatches {
			if reciprocal.MatchingNumber == match.MatchingNumber && reciprocal.CounterpartID == line.ID &&
				reciprocal.FunctionalAmount.Currency() != "" && counterpart.FunctionalResidualAmount.Currency() != "" {
				counterpart.FunctionalResidualAmount, err = counterpart.FunctionalResidualAmount.Add(reciprocal.FunctionalAmount)
				if err != nil {
					return err
				}
				break
			}
		}
		updatedLine.Matches = removeMatch(updatedLine.Matches, match.MatchingNumber, match.CounterpartID)
		counterpart.Matches = removeMatch(counterpartMatches, match.MatchingNumber, line.ID)
		reversedAt := time.Now().UTC()
		markMatchReversed(&updatedLine.MatchHistory, match.MatchingNumber, match.CounterpartID, reversedAt)
		markMatchReversed(&counterpart.MatchHistory, match.MatchingNumber, line.ID, reversedAt)
		updated[updatedLine.ID] = updatedLine
		updated[counterpart.ID] = counterpart
	}

	toSaveExpected := make([]OpenLine, 0, len(expected))
	toSaveUpdated := make([]OpenLine, 0, len(expected))
	for id, oldLine := range expected {
		current := updated[id]
		current.Closed = current.ResidualAmount.Amount().IsZero()
		syncLegacyMatches(&current)
		toSaveExpected = append(toSaveExpected, oldLine)
		toSaveUpdated = append(toSaveUpdated, current)
	}
	sort.Slice(toSaveExpected, func(i, j int) bool {
		return toSaveExpected[i].ID.String() < toSaveExpected[j].ID.String()
	})
	sort.Slice(toSaveUpdated, func(i, j int) bool {
		return toSaveUpdated[i].ID.String() < toSaveUpdated[j].ID.String()
	})
	return m.repo.CompareAndSaveOpenLines(ctx, toSaveExpected, toSaveUpdated)
}

func markMatchReversed(history *[]OpenLineMatch, matchingNumber string, counterpartID uuid.UUID, reversedAt time.Time) {
	for i := range *history {
		if (*history)[i].MatchingNumber == matchingNumber && (*history)[i].CounterpartID == counterpartID {
			(*history)[i].ReversedAt = reversedAt
			return
		}
	}
}

func openLineMatches(line OpenLine) ([]OpenLineMatch, error) {
	if len(line.Matches) != 0 {
		return append([]OpenLineMatch(nil), line.Matches...), nil
	}
	if line.MatchingNumber == "" && len(line.MatchIDs) == 0 {
		return nil, nil
	}
	if line.MatchingNumber == "" || len(line.MatchIDs) != 1 {
		return nil, ErrInvalidOpenLine
	}
	settled, err := line.OriginalAmount.Subtract(line.ResidualAmount)
	if err != nil || settled.IsNegative() || settled.Amount().IsZero() {
		return nil, ErrInvalidOpenLine
	}
	return []OpenLineMatch{{
		MatchingNumber: line.MatchingNumber,
		CounterpartID:  line.MatchIDs[0],
		Amount:         settled,
	}}, nil
}

func removeMatch(matches []OpenLineMatch, matchingNumber string, counterpartID uuid.UUID) []OpenLineMatch {
	result := make([]OpenLineMatch, 0, len(matches))
	for _, match := range matches {
		if match.MatchingNumber == matchingNumber && match.CounterpartID == counterpartID {
			continue
		}
		result = append(result, match)
	}
	return result
}

func syncLegacyMatches(line *OpenLine) {
	line.MatchIDs = line.MatchIDs[:0]
	line.MatchingNumber = ""
	for _, match := range line.Matches {
		found := false
		for _, id := range line.MatchIDs {
			if id == match.CounterpartID {
				found = true
				break
			}
		}
		if !found {
			line.MatchIDs = append(line.MatchIDs, match.CounterpartID)
		}
		line.MatchingNumber = match.MatchingNumber
	}
}
