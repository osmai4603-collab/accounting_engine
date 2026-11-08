package subledgers

import (
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
	line, err := m.repo.GetOpenLine(lineID)
	if err != nil {
		return "", err
	}
	counterpart, err := m.repo.GetOpenLine(counterpartID)
	if err != nil {
		return "", err
	}
	if line.Kind == counterpart.Kind {
		return "", ErrUnmatchedLine
	}
	if line.ResidualAmount.Currency() != amount.Currency() ||
		counterpart.ResidualAmount.Currency() != amount.Currency() ||
		amount.IsNegative() || amount.Amount().IsZero() {
		return "", ErrInvalidAmount
	}
	if amount.GreaterThan(line.ResidualAmount) || amount.GreaterThan(counterpart.ResidualAmount) {
		return "", ErrInvalidAmount
	}

	matchingNumber := strings.ToUpper(uuid.New().String())
	if err := applyMatch(&line, &counterpart, amount, matchingNumber); err != nil {
		return "", err
	}
	if err := m.repo.SaveOpenLine(line); err != nil {
		return "", err
	}
	if err := m.repo.SaveOpenLine(counterpart); err != nil {
		return "", err
	}
	return matchingNumber, nil
}

func applyMatch(line, counterpart *OpenLine, amount financialtypes.Money, matchingNumber string) error {
	var err error
	line.ResidualAmount, err = line.ResidualAmount.Subtract(amount)
	if err != nil {
		return err
	}
	counterpart.ResidualAmount, err = counterpart.ResidualAmount.Subtract(amount)
	if err != nil {
		return err
	}
	line.MatchingNumber = matchingNumber
	counterpart.MatchingNumber = matchingNumber
	line.MatchIDs = append(line.MatchIDs, counterpart.ID)
	counterpart.MatchIDs = append(counterpart.MatchIDs, line.ID)
	line.Closed = line.ResidualAmount.Amount().IsZero()
	counterpart.Closed = counterpart.ResidualAmount.Amount().IsZero()
	return nil
}

func (m *MatchingEngine) Unreconcile(lineID uuid.UUID) error {
	line, err := m.repo.GetOpenLine(lineID)
	if err != nil {
		return err
	}
	if line.MatchingNumber == "" {
		return nil
	}
	matchingNumber := line.MatchingNumber
	for _, counterpartID := range line.MatchIDs {
		counterpart, err := m.repo.GetOpenLine(counterpartID)
		if err != nil {
			continue
		}
		if counterpart.MatchingNumber != matchingNumber {
			continue
		}
		lineSettled, err := line.OriginalAmount.Subtract(line.ResidualAmount)
		if err != nil {
			return err
		}
		counterpartSettled, err := counterpart.OriginalAmount.Subtract(counterpart.ResidualAmount)
		if err != nil {
			return err
		}
		portion := minAmount(lineSettled, counterpartSettled)
		line.ResidualAmount, err = line.ResidualAmount.Add(portion)
		if err == nil {
			counterpart.ResidualAmount, err = counterpart.ResidualAmount.Add(portion)
		}
		if err != nil {
			return err
		}
		line.Closed = false
		counterpart.Closed = false
		line.MatchingNumber = ""
		counterpart.MatchingNumber = ""
		line.MatchIDs = nil
		counterpart.MatchIDs = nil
		if err := m.repo.SaveOpenLine(counterpart); err != nil {
			return err
		}
	}
	return m.repo.SaveOpenLine(line)
}

func minAmount(a, b financialtypes.Money) financialtypes.Money {
	if a.Amount().LessThan(b.Amount()) {
		return a
	}
	return b
}
