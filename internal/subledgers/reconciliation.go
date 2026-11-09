package subledgers

import (
	"sort"
	"strings"

	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
)

type MatchingEngine struct {
	repo Repository
}

func NewMatchingEngine(repo Repository) *MatchingEngine {
	return &MatchingEngine{repo: repo}
}

func (m *MatchingEngine) Match(lineID, counterpartID uuid.UUID, amount financialtypes.Money) (string, error) {
	if lineID == counterpartID {
		return "", ErrUnmatchedLine
	}
	line, err := m.repo.GetOpenLine(lineID)
	if err != nil {
		return "", err
	}
	counterpart, err := m.repo.GetOpenLine(counterpartID)
	if err != nil {
		return "", err
	}
	if line.CompanyID == uuid.Nil || counterpart.CompanyID == uuid.Nil ||
		line.ID == uuid.Nil || counterpart.ID == uuid.Nil {
		return "", ErrInvalidOpenLine
	}
	if !validLineKind(line.Kind) || !validLineKind(counterpart.Kind) || line.Kind == counterpart.Kind {
		return "", ErrUnmatchedLine
	}
	if line.CompanyID != counterpart.CompanyID {
		return "", ErrCompanyMismatch
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
		return "", ErrInvalidAmount
	}

	matchingNumber := strings.ToUpper(uuid.New().String())
	lineMatches, err := openLineMatches(line)
	if err != nil {
		return "", err
	}
	counterpartMatches, err := openLineMatches(counterpart)
	if err != nil {
		return "", err
	}
	line.Matches = append(lineMatches, OpenLineMatch{
		MatchingNumber: matchingNumber, CounterpartID: counterpart.ID, Amount: amount,
	})
	counterpart.Matches = append(counterpartMatches, OpenLineMatch{
		MatchingNumber: matchingNumber, CounterpartID: line.ID, Amount: amount,
	})
	if err := applyMatch(&line, &counterpart, amount); err != nil {
		return "", err
	}
	syncLegacyMatches(&line)
	syncLegacyMatches(&counterpart)

	repository, ok := m.repo.(ConditionalOpenLineRepository)
	if !ok {
		return "", ErrAtomicMatchRequired
	}
	if err := repository.CompareAndSaveOpenLines(
		[]OpenLine{expectedLine, expectedCounterpart},
		[]OpenLine{line, counterpart},
	); err != nil {
		return "", err
	}
	return matchingNumber, nil
}

func validLineKind(kind LineKind) bool {
	return kind == LineReceivable || kind == LinePayable
}

func applyMatch(line, counterpart *OpenLine, amount financialtypes.Money) error {
	var err error
	line.ResidualAmount, err = line.ResidualAmount.Subtract(amount)
	if err != nil {
		return err
	}
	counterpart.ResidualAmount, err = counterpart.ResidualAmount.Subtract(amount)
	if err != nil {
		return err
	}
	line.Closed = line.ResidualAmount.Amount().IsZero()
	counterpart.Closed = counterpart.ResidualAmount.Amount().IsZero()
	return nil
}

func (m *MatchingEngine) Unreconcile(lineID uuid.UUID) error {
	line, err := m.repo.GetOpenLine(lineID)
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
			counterpart, err = m.repo.GetOpenLine(match.CounterpartID)
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
		updatedLine.Matches = removeMatch(updatedLine.Matches, match.MatchingNumber, match.CounterpartID)
		counterpart.Matches = removeMatch(counterpartMatches, match.MatchingNumber, line.ID)
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
	repository, ok := m.repo.(ConditionalOpenLineRepository)
	if !ok {
		return ErrAtomicMatchRequired
	}
	return repository.CompareAndSaveOpenLines(toSaveExpected, toSaveUpdated)
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
