package subledgers

import (
	"strings"
	"time"

	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type ReceivableLedger struct {
	repo Repository
}

func NewReceivableLedger(repo Repository) *ReceivableLedger {
	return &ReceivableLedger{repo: repo}
}

func (l *ReceivableLedger) AddInvoice(partnerID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	if partnerID == uuid.Nil || amount.Currency() == "" || amount.IsNegative() {
		return OpenLine{}, ErrInvalidAmount
	}
	line := OpenLine{
		ID:             l.newLineID(),
		PartnerID:      &partnerID,
		Kind:           LineReceivable,
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

func (l *ReceivableLedger) newLineID() uuid.UUID { return uuid.New() }

func (l *ReceivableLedger) Aging(asOf time.Time, partnerID *uuid.UUID) (AgingSchedule, error) {
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
	result := make(AgingSchedule)
	currency := lines[0].ResidualAmount.Currency()
	for _, bucket := range []AgingBucket{AgingCurrent, Aging1To30, Aging31To60, Aging61To90, AgingOver90} {
		zero, _ := financialtypes.Zero(currency)
		result[bucket] = zero
	}
	for _, line := range lines {
		if line.Kind != LineReceivable || line.ResidualAmount.Amount().IsZero() {
			continue
		}
		bucket := agingBucket(asOf, line.DueDate)
		total, err := result[bucket].Add(line.ResidualAmount)
		if err != nil {
			return nil, err
		}
		result[bucket] = total
	}
	return result, nil
}

func agingBucket(asOf, dueDate time.Time) AgingBucket {
	if !dueDate.Before(asOf) || dueDate.Equal(asOf) {
		return AgingCurrent
	}
	days := int(asOf.Sub(dueDate) / (24 * time.Hour))
	if days <= 30 {
		return Aging1To30
	}
	if days <= 60 {
		return Aging31To60
	}
	if days <= 90 {
		return Aging61To90
	}
	return AgingOver90
}

func (l *ReceivableLedger) CalculateECL(asOf time.Time, partnerID *uuid.UUID, probabilityByBucket map[AgingBucket]decimal.Decimal) (financialtypes.Money, error) {
	schedule, err := l.Aging(asOf, partnerID)
	if err != nil || schedule == nil {
		return financialtypes.Money{}, err
	}
	currency := ""
	for _, amount := range schedule {
		currency = amount.Currency()
		break
	}
	total, _ := financialtypes.Zero(currency)
	for bucket, amount := range schedule {
		probability, ok := probabilityByBucket[bucket]
		if !ok {
			probability = decimal.Zero
		}
		expected, err := amount.Multiply(probability)
		if err != nil {
			return financialtypes.Money{}, err
		}
		total, err = total.Add(expected)
		if err != nil {
			return financialtypes.Money{}, err
		}
	}
	return total, nil
}

func (l *ReceivableLedger) CloseOpenLine(lineID uuid.UUID) error {
	line, err := l.repo.GetOpenLine(lineID)
	if err != nil {
		return err
	}
	line.Closed = true
	return l.repo.SaveOpenLine(line)
}

type ReceivableMatrix map[AgingBucket]decimal.Decimal

func (m ReceivableMatrix) Validate() error {
	for bucket, probability := range m {
		if strings.TrimSpace(string(bucket)) == "" ||
			probability.IsNegative() || probability.GreaterThan(decimal.NewFromInt(1)) {
			return ErrInvalidAmount
		}
	}
	return nil
}
