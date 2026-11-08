package subledgers

import (
	"time"

	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
)

type PayableLedger struct {
	repo Repository
}

func NewPayableLedger(repo Repository) *PayableLedger {
	return &PayableLedger{repo: repo}
}

func (l *PayableLedger) AddInvoice(partnerID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	if partnerID == uuid.Nil || amount.Currency() == "" || amount.IsNegative() {
		return OpenLine{}, ErrInvalidAmount
	}
	line := OpenLine{
		ID:             uuid.New(),
		PartnerID:      &partnerID,
		Kind:           LinePayable,
		OriginalAmount: amount,
		ResidualAmount: amount,
		DocumentDate:   documentDate,
		DueDate:        dueDate,
		Reference:      reference,
	}
	if err := l.repo.SaveOpenLine(line); err != nil {
		return OpenLine{}, err
	}
	return line, nil
}

func (l *PayableLedger) Aging(asOf time.Time, partnerID *uuid.UUID) (AgingSchedule, error) {
	partnerKey := ""
	if partnerID != nil {
		partnerKey = partnerID.String()
	}
	lines, err := l.repo.ListOpenLines(partnerKey)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, nil
	}
	currency := lines[0].ResidualAmount.Currency()
	result := make(AgingSchedule)
	for _, bucket := range []AgingBucket{AgingCurrent, Aging1To30, Aging31To60, Aging61To90, AgingOver90} {
		zero, _ := financialtypes.Zero(currency)
		result[bucket] = zero
	}
	for _, line := range lines {
		if line.Kind != LinePayable || line.ResidualAmount.Amount().IsZero() {
			continue
		}
		bucket := agingBucket(asOf, line.DueDate)
		amount, err := result[bucket].Add(line.ResidualAmount)
		if err != nil {
			return nil, err
		}
		result[bucket] = amount
	}
	return result, nil
}
