package ifrs15

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	ErrInvalidContract     = errors.New("invalid revenue contract")
	ErrInvalidObligation   = errors.New("invalid performance obligation")
	ErrInvalidAmount       = errors.New("invalid revenue amount")
	ErrInvalidPeriod       = errors.New("invalid recognition period")
	ErrObligationNotFound  = errors.New("performance obligation not found")
	ErrRecognitionExceeded = errors.New("recognized revenue exceeds transaction price")
)

type RecognitionPattern string

const (
	PointInTime RecognitionPattern = "point_in_time"
	OverTime    RecognitionPattern = "over_time"
)

type ScheduleStatus string

const (
	SchedulePending    ScheduleStatus = "pending"
	ScheduleRecognized ScheduleStatus = "recognized"
	ScheduleDeferred   ScheduleStatus = "deferred"
)

type ScheduleFrequency string

const (
	Daily   ScheduleFrequency = "daily"
	Monthly ScheduleFrequency = "monthly"
)

type PerformanceObligation struct {
	ID                 uuid.UUID
	Description        string
	StandalonePrice    financialtypes.Money
	RecognitionPattern RecognitionPattern
	StartDate          time.Time
	EndDate            time.Time
}

type Contract struct {
	ID               uuid.UUID
	CustomerID       uuid.UUID
	Currency         string
	TransactionPrice financialtypes.Money
	Obligations      []PerformanceObligation
	AllocatedPrices  map[uuid.UUID]financialtypes.Money
	BilledAmount     financialtypes.Money
	RecognizedAmount financialtypes.Money
}

type RevenueScheduleEntry struct {
	ContractID              uuid.UUID
	PerformanceObligationID uuid.UUID
	RecognitionDate         time.Time
	AmountToRecognize       financialtypes.Money
	Status                  ScheduleStatus
}

// NewContract validates the five-step inputs and allocates transaction price
// using the relative standalone selling prices of its obligations.
func NewContract(id, customerID uuid.UUID, transactionPrice financialtypes.Money, obligations []PerformanceObligation) (Contract, error) {
	if id == uuid.Nil || customerID == uuid.Nil || transactionPrice.Currency() == "" || !transactionPrice.Amount().IsPositive() {
		return Contract{}, ErrInvalidContract
	}
	var err error
	transactionPrice, err = transactionPrice.Rounded()
	if err != nil || !transactionPrice.Amount().IsPositive() {
		return Contract{}, ErrInvalidAmount
	}
	if len(obligations) == 0 {
		return Contract{}, ErrInvalidObligation
	}
	currency := transactionPrice.Currency()
	seen := make(map[uuid.UUID]struct{}, len(obligations))
	standaloneTotal := decimal.Zero
	for _, obligation := range obligations {
		if obligation.ID == uuid.Nil || obligation.Description == "" {
			return Contract{}, ErrInvalidObligation
		}
		if _, exists := seen[obligation.ID]; exists {
			return Contract{}, fmt.Errorf("%w: duplicate obligation ID", ErrInvalidObligation)
		}
		seen[obligation.ID] = struct{}{}
		if obligation.StandalonePrice.Currency() != currency ||
			!obligation.StandalonePrice.Amount().IsPositive() {
			return Contract{}, ErrInvalidObligation
		}
		if obligation.RecognitionPattern != PointInTime && obligation.RecognitionPattern != OverTime {
			return Contract{}, ErrInvalidObligation
		}
		if obligation.RecognitionPattern == OverTime &&
			(dateOnly(obligation.StartDate).After(dateOnly(obligation.EndDate)) ||
				obligation.StartDate.IsZero() || obligation.EndDate.IsZero()) {
			return Contract{}, ErrInvalidPeriod
		}
		if obligation.RecognitionPattern == PointInTime && obligation.StartDate.IsZero() {
			return Contract{}, ErrInvalidPeriod
		}
		standaloneTotal = standaloneTotal.Add(obligation.StandalonePrice.Amount())
	}

	allocations, err := allocate(transactionPrice, obligations, standaloneTotal)
	if err != nil {
		return Contract{}, err
	}
	zero, err := financialtypes.Zero(currency)
	if err != nil {
		return Contract{}, err
	}
	copied := append([]PerformanceObligation(nil), obligations...)
	return Contract{
		ID:               id,
		CustomerID:       customerID,
		Currency:         currency,
		TransactionPrice: transactionPrice,
		Obligations:      copied,
		AllocatedPrices:  allocations,
		BilledAmount:     zero,
		RecognizedAmount: zero,
	}, nil
}

func (c Contract) Validate() error {
	if c.ID == uuid.Nil || c.CustomerID == uuid.Nil || c.Currency == "" ||
		c.TransactionPrice.Currency() != c.Currency || !c.TransactionPrice.Amount().IsPositive() ||
		len(c.Obligations) == 0 || len(c.AllocatedPrices) != len(c.Obligations) {
		return ErrInvalidContract
	}
	if c.BilledAmount.Currency() != c.Currency || c.RecognizedAmount.Currency() != c.Currency ||
		c.BilledAmount.IsNegative() || c.RecognizedAmount.IsNegative() {
		return ErrInvalidContract
	}
	total := decimal.Zero
	seen := make(map[uuid.UUID]struct{}, len(c.Obligations))
	for _, obligation := range c.Obligations {
		if obligation.ID == uuid.Nil || obligation.Description == "" ||
			obligation.StandalonePrice.Currency() != c.Currency ||
			!obligation.StandalonePrice.Amount().IsPositive() {
			return ErrInvalidObligation
		}
		if obligation.RecognitionPattern != PointInTime && obligation.RecognitionPattern != OverTime {
			return ErrInvalidObligation
		}
		if obligation.RecognitionPattern == OverTime &&
			(obligation.StartDate.IsZero() || obligation.EndDate.IsZero() ||
				dateOnly(obligation.StartDate).After(dateOnly(obligation.EndDate))) {
			return ErrInvalidPeriod
		}
		if obligation.RecognitionPattern == PointInTime && obligation.StartDate.IsZero() {
			return ErrInvalidPeriod
		}
		if _, exists := seen[obligation.ID]; exists {
			return ErrInvalidObligation
		}
		seen[obligation.ID] = struct{}{}
		allocated, ok := c.AllocatedPrices[obligation.ID]
		if !ok || allocated.Currency() != c.Currency || allocated.IsNegative() {
			return ErrInvalidContract
		}
		total = total.Add(allocated.Amount())
	}
	if !total.Equal(c.TransactionPrice.Amount().RoundBank(financialtypes.CurrencyMinorUnit(c.Currency))) {
		return ErrInvalidContract
	}
	if c.BilledAmount.GreaterThan(c.TransactionPrice) ||
		c.RecognizedAmount.GreaterThan(c.TransactionPrice) {
		return ErrRecognitionExceeded
	}
	return nil
}

// AllocateTransactionPrice exposes relative-SSP allocation for callers that
// need to preview the five-step allocation before creating a contract.
func AllocateTransactionPrice(transactionPrice financialtypes.Money, obligations []PerformanceObligation) (map[uuid.UUID]financialtypes.Money, error) {
	if transactionPrice.Currency() == "" || !transactionPrice.Amount().IsPositive() || len(obligations) == 0 {
		return nil, ErrInvalidAmount
	}
	var err error
	transactionPrice, err = transactionPrice.Rounded()
	if err != nil || !transactionPrice.Amount().IsPositive() {
		return nil, ErrInvalidAmount
	}
	total := decimal.Zero
	seen := make(map[uuid.UUID]struct{}, len(obligations))
	for _, obligation := range obligations {
		if obligation.ID == uuid.Nil || obligation.StandalonePrice.Currency() != transactionPrice.Currency() ||
			!obligation.StandalonePrice.Amount().IsPositive() {
			return nil, ErrInvalidObligation
		}
		if _, ok := seen[obligation.ID]; ok {
			return nil, ErrInvalidObligation
		}
		seen[obligation.ID] = struct{}{}
		total = total.Add(obligation.StandalonePrice.Amount())
	}
	return allocate(transactionPrice, obligations, total)
}

type allocationRemainder struct {
	index    int
	fraction decimal.Decimal
}

func allocate(price financialtypes.Money, obligations []PerformanceObligation, sspTotal decimal.Decimal) (map[uuid.UUID]financialtypes.Money, error) {
	unit := decimal.New(1, -financialtypes.CurrencyMinorUnit(price.Currency()))
	totalUnits := price.Amount().Div(unit)
	if !totalUnits.IsPositive() || sspTotal.IsZero() {
		return nil, ErrInvalidAmount
	}
	units := make([]decimal.Decimal, len(obligations))
	remainders := make([]allocationRemainder, len(obligations))
	allocatedUnits := decimal.Zero
	for i, obligation := range obligations {
		exact := totalUnits.Mul(obligation.StandalonePrice.Amount()).Div(sspTotal)
		units[i] = exact.Floor()
		allocatedUnits = allocatedUnits.Add(units[i])
		remainders[i] = allocationRemainder{index: i, fraction: exact.Sub(units[i])}
	}
	sort.SliceStable(remainders, func(i, j int) bool {
		return remainders[i].fraction.GreaterThan(remainders[j].fraction)
	})
	left := totalUnits.Sub(allocatedUnits).IntPart()
	for i := int64(0); i < left; i++ {
		units[remainders[i%int64(len(remainders))].index] =
			units[remainders[i%int64(len(remainders))].index].Add(decimal.NewFromInt(1))
	}

	result := make(map[uuid.UUID]financialtypes.Money, len(obligations))
	for i, obligation := range obligations {
		amount, err := financialtypes.NewMoney(units[i].Mul(unit), price.Currency())
		if err != nil {
			return nil, err
		}
		result[obligation.ID] = amount
	}
	return result, nil
}

func (c Contract) Schedule(obligationID uuid.UUID, frequency ScheduleFrequency) ([]RevenueScheduleEntry, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if frequency != Daily && frequency != Monthly {
		return nil, ErrInvalidPeriod
	}
	var obligation *PerformanceObligation
	for i := range c.Obligations {
		if c.Obligations[i].ID == obligationID {
			obligation = &c.Obligations[i]
			break
		}
	}
	if obligation == nil {
		return nil, ErrObligationNotFound
	}
	amount := c.AllocatedPrices[obligationID]
	if obligation.RecognitionPattern == PointInTime {
		return []RevenueScheduleEntry{{
			ContractID: c.ID, PerformanceObligationID: obligation.ID,
			RecognitionDate: dateOnly(obligation.StartDate), AmountToRecognize: amount,
			Status: SchedulePending,
		}}, nil
	}
	if frequency == Daily {
		return c.dailySchedule(*obligation, amount)
	}
	return c.monthlySchedule(*obligation, amount)
}

func (c Contract) dailySchedule(obligation PerformanceObligation, total financialtypes.Money) ([]RevenueScheduleEntry, error) {
	start, end := dateOnly(obligation.StartDate), dateOnly(obligation.EndDate)
	days := int(end.Sub(start).Hours()/24) + 1
	if days <= 0 {
		return nil, ErrInvalidPeriod
	}
	result := make([]RevenueScheduleEntry, 0, days)
	rounded, err := total.Divide(decimal.NewFromInt(int64(days)))
	if err != nil {
		return nil, err
	}
	rounded, err = rounded.Rounded()
	if err != nil {
		return nil, err
	}
	remaining := total
	for i := 0; i < days; i++ {
		amount := rounded
		if i == days-1 {
			amount = remaining
		} else {
			remaining, err = remaining.Subtract(amount)
			if err != nil {
				return nil, err
			}
		}
		result = append(result, RevenueScheduleEntry{
			ContractID: c.ID, PerformanceObligationID: obligation.ID,
			RecognitionDate: start.AddDate(0, 0, i), AmountToRecognize: amount,
			Status: SchedulePending,
		})
	}
	return result, nil
}

func (c Contract) monthlySchedule(obligation PerformanceObligation, total financialtypes.Money) ([]RevenueScheduleEntry, error) {
	start, end := dateOnly(obligation.StartDate), dateOnly(obligation.EndDate)
	totalDays := int(end.Sub(start).Hours()/24) + 1
	if totalDays <= 0 {
		return nil, ErrInvalidPeriod
	}
	entries := make([]RevenueScheduleEntry, 0)
	remaining := total
	for monthStart := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC); !monthStart.After(end); monthStart = monthStart.AddDate(0, 1, 0) {
		monthEnd := monthStart.AddDate(0, 1, -1)
		periodStart, periodEnd := monthStart, monthEnd
		if periodStart.Before(start) {
			periodStart = start
		}
		if periodEnd.After(end) {
			periodEnd = end
		}
		periodDays := int(periodEnd.Sub(periodStart).Hours()/24) + 1
		amount := remaining
		if periodEnd.Before(end) {
			var err error
			amount, err = total.Multiply(decimal.NewFromInt(int64(periodDays)))
			if err != nil {
				return nil, err
			}
			amount, err = amount.Divide(decimal.NewFromInt(int64(totalDays)))
			if err != nil {
				return nil, err
			}
			amount, err = amount.Rounded()
			if err != nil {
				return nil, err
			}
			remaining, err = remaining.Subtract(amount)
			if err != nil {
				return nil, err
			}
		}
		entries = append(entries, RevenueScheduleEntry{
			ContractID: c.ID, PerformanceObligationID: obligation.ID,
			RecognitionDate: periodEnd, AmountToRecognize: amount,
			Status: SchedulePending,
		})
	}
	return entries, nil
}

func UpdateScheduleStatus(entries []RevenueScheduleEntry, throughDate time.Time) []RevenueScheduleEntry {
	result := append([]RevenueScheduleEntry(nil), entries...)
	throughDate = dateOnly(throughDate)
	for i := range result {
		if !dateOnly(result[i].RecognitionDate).After(throughDate) {
			result[i].Status = ScheduleRecognized
		} else {
			result[i].Status = ScheduleDeferred
		}
	}
	return result
}

func (c Contract) ApplyBilling(amount financialtypes.Money) (Contract, error) {
	if err := c.validateAmount(amount); err != nil {
		return Contract{}, err
	}
	billed, err := c.BilledAmount.Add(amount)
	if err != nil {
		return Contract{}, err
	}
	if billed.GreaterThan(c.TransactionPrice) {
		return Contract{}, ErrInvalidAmount
	}
	c.BilledAmount = billed
	return c, nil
}

func (c Contract) ApplyRecognition(amount financialtypes.Money) (Contract, error) {
	if err := c.validateAmount(amount); err != nil {
		return Contract{}, err
	}
	recognized, err := c.RecognizedAmount.Add(amount)
	if err != nil {
		return Contract{}, err
	}
	if recognized.GreaterThan(c.TransactionPrice) {
		return Contract{}, ErrRecognitionExceeded
	}
	c.RecognizedAmount = recognized
	return c, nil
}

func (c Contract) ContractBalances() (asset, liability financialtypes.Money, err error) {
	if err = c.Validate(); err != nil {
		return financialtypes.Money{}, financialtypes.Money{}, err
	}
	difference, err := c.RecognizedAmount.Subtract(c.BilledAmount)
	if err != nil {
		return financialtypes.Money{}, financialtypes.Money{}, err
	}
	zero, err := financialtypes.Zero(c.Currency)
	if err != nil {
		return financialtypes.Money{}, financialtypes.Money{}, err
	}
	if difference.IsNegative() {
		liability, multiplyErr := difference.Multiply(decimal.NewFromInt(-1))
		return zero, liability, multiplyErr
	}
	return difference, zero, nil
}

func (c Contract) validateAmount(amount financialtypes.Money) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if amount.Currency() != c.Currency || !amount.Amount().IsPositive() {
		return ErrInvalidAmount
	}
	return nil
}

// BuildRecognitionJournal debits deferred revenue first and records any
// unbilled earned portion as a contract asset, with earned revenue credited.
func BuildRecognitionJournal(c Contract, amount financialtypes.Money, journalID uuid.UUID, date time.Time, revenueAccount, contractLiabilityAccount, contractAssetAccount uuid.UUID) (posting.JournalEntry, error) {
	if err := c.validateAmount(amount); err != nil {
		return posting.JournalEntry{}, err
	}
	if _, err := c.ApplyRecognition(amount); err != nil {
		return posting.JournalEntry{}, err
	}
	if journalID == uuid.Nil || revenueAccount == uuid.Nil || contractLiabilityAccount == uuid.Nil ||
		contractAssetAccount == uuid.Nil || date.IsZero() {
		return posting.JournalEntry{}, ErrInvalidContract
	}
	liability := c.BilledAmount.Amount().Sub(c.RecognizedAmount.Amount())
	if liability.IsNegative() {
		liability = decimal.Zero
	}
	deferredDebit := decimal.Min(liability, amount.Amount())
	assetDebit := amount.Amount().Sub(deferredDebit)
	lines := make([]posting.JournalEntryLine, 0, 3)
	if deferredDebit.IsPositive() {
		debit, err := financialtypes.NewMoney(deferredDebit, c.Currency)
		if err != nil {
			return posting.JournalEntry{}, err
		}
		lines = append(lines, posting.JournalEntryLine{AccountID: contractLiabilityAccount, Debit: debit})
	}
	if assetDebit.IsPositive() {
		debit, err := financialtypes.NewMoney(assetDebit, c.Currency)
		if err != nil {
			return posting.JournalEntry{}, err
		}
		lines = append(lines, posting.JournalEntryLine{AccountID: contractAssetAccount, Debit: debit})
	}
	credit, err := financialtypes.NewMoney(amount.Amount(), c.Currency)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	lines = append(lines, posting.JournalEntryLine{AccountID: revenueAccount, Credit: credit})
	return posting.NewJournalEntry(journalID, date, "ifrs15", lines)
}

func dateOnly(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}
