package ifrs9

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/subledgers"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

var (
	ErrInvalidConfiguration           = errors.New("invalid IFRS 9 ECL configuration")
	ErrInvalidExposure                = errors.New("invalid receivable exposure")
	ErrMissingRate                    = errors.New("missing provision matrix rate")
	ErrInvalidRate                    = errors.New("invalid provision matrix rate")
	ErrInvalidAllowance               = errors.New("invalid existing allowance")
	ErrInvalidDate                    = errors.New("invalid ECL measurement date")
	ErrDependenciesRequired           = errors.New("ECL data sources are required")
	ErrHistoricalAllowanceUnavailable = errors.New("historical allowance is unavailable")
)

type Stage string

const (
	Stage1 Stage = "stage_1"
	Stage2 Stage = "stage_2"
	Stage3 Stage = "stage_3"
)

type Exposure struct {
	Receivable subledgers.OpenLine
	SICR       bool
	Defaulted  bool
}

type Config struct {
	CompanyID           uuid.UUID
	JournalID           uuid.UUID
	ImpairmentAccountID uuid.UUID
	AllowanceAccountID  uuid.UUID
	Currency            string
	ProvisionMatrix     map[subledgers.AgingBucket]decimal.Decimal
}

type PortfolioSource interface {
	ListReceivables(context.Context, uuid.UUID) ([]Exposure, error)
}

type AllowanceSource interface {
	ExistingAllowance(context.Context, uuid.UUID, uuid.UUID, string) (financialtypes.Money, error)
}

type AsOfAllowanceSource interface {
	ExistingAllowanceAsOf(context.Context, uuid.UUID, uuid.UUID, string, time.Time) (financialtypes.Money, error)
}

type Engine struct {
	config    Config
	portfolio PortfolioSource
	allowance AllowanceSource
}

type ReceivableResult struct {
	ReceivableID uuid.UUID
	Stage        Stage
	DaysPastDue  int
	AgingBucket  subledgers.AgingBucket
	ECL          financialtypes.Money
}

type CalculationResult struct {
	AsOf              time.Time
	Receivables       []ReceivableResult
	RequiredAllowance financialtypes.Money
	ExistingAllowance financialtypes.Money
	Adjustment        financialtypes.Money
	JournalEntry      *posting.JournalEntry
}

func NewEngine(config Config, portfolio PortfolioSource, allowance AllowanceSource) (*Engine, error) {
	var err error
	config, err = normalizeConfig(config)
	if err != nil {
		return nil, err
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if portfolio == nil || allowance == nil {
		return nil, ErrDependenciesRequired
	}
	return &Engine{config: config, portfolio: portfolio, allowance: allowance}, nil
}

func (c Config) Validate() error {
	if c.CompanyID == uuid.Nil || c.JournalID == uuid.Nil ||
		c.ImpairmentAccountID == uuid.Nil || c.AllowanceAccountID == uuid.Nil ||
		c.ImpairmentAccountID == c.AllowanceAccountID ||
		strings.TrimSpace(c.Currency) == "" {
		return ErrInvalidConfiguration
	}
	if _, err := financialtypes.Zero(c.Currency); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfiguration, err)
	}
	for _, bucket := range allAgingBuckets {
		rate, ok := c.ProvisionMatrix[bucket]
		if !ok {
			return fmt.Errorf("%w: %s", ErrMissingRate, bucket)
		}
		if rate.IsNegative() || rate.GreaterThan(decimal.NewFromInt(1)) {
			return fmt.Errorf("%w: %s", ErrInvalidRate, bucket)
		}
	}
	for bucket := range c.ProvisionMatrix {
		if !validAgingBucket(bucket) {
			return fmt.Errorf("%w: unknown aging bucket %q", ErrInvalidConfiguration, bucket)
		}
	}
	return nil
}

func normalizeConfig(config Config) (Config, error) {
	currency, err := financialtypes.NewMoney(decimal.Zero, config.Currency)
	if err != nil {
		return Config{}, fmt.Errorf("%w: %v", ErrInvalidConfiguration, err)
	}
	config.Currency = currency.Currency()
	return config, nil
}

func (e *Engine) Calculate(ctx context.Context, asOf time.Time) (CalculationResult, error) {
	if e == nil || e.portfolio == nil || e.allowance == nil {
		return CalculationResult{}, ErrDependenciesRequired
	}
	if asOf.IsZero() {
		return CalculationResult{}, ErrInvalidDate
	}
	if err := ctx.Err(); err != nil {
		return CalculationResult{}, err
	}
	exposures, err := e.portfolio.ListReceivables(ctx, e.config.CompanyID)
	if err != nil {
		return CalculationResult{}, err
	}
	var existing financialtypes.Money
	if source, ok := e.allowance.(AsOfAllowanceSource); ok {
		existing, err = source.ExistingAllowanceAsOf(
			ctx, e.config.CompanyID, e.config.AllowanceAccountID, e.config.Currency, asOf,
		)
	} else if dateOnlyUTC(asOf).Before(dateOnlyUTC(time.Now())) {
		return CalculationResult{}, ErrHistoricalAllowanceUnavailable
	} else {
		existing, err = e.allowance.ExistingAllowance(
			ctx, e.config.CompanyID, e.config.AllowanceAccountID, e.config.Currency,
		)
	}
	if err != nil {
		return CalculationResult{}, err
	}
	return Calculate(asOf, e.config, exposures, existing)
}

// Unit is the simplified IFRS 9 provision-matrix unit for trade receivables.
// The general PD/LGD/EAD method is exposed separately by CalculateGeneralECL.
type Unit struct{ engine *Engine }

func NewUnit(config Config, portfolio PortfolioSource, allowance AllowanceSource) (*Unit, error) {
	engine, err := NewEngine(config, portfolio, allowance)
	if err != nil {
		return nil, err
	}
	return &Unit{engine: engine}, nil
}

// NewGeneralUnit creates a unit instance for caller-supplied PD/LGD models.
func NewGeneralUnit() *Unit { return &Unit{} }

func (u *Unit) CalculateGeneralECL(
	asOf time.Time,
	exposures []GeneralExposure,
	pdModel PDModel,
	lgdModel LGDModel,
	eirDiscount EIRDiscount,
) (GeneralCalculationResult, error) {
	if u == nil {
		return GeneralCalculationResult{}, ErrDependenciesRequired
	}
	return CalculateGeneralECL(asOf, exposures, pdModel, lgdModel, eirDiscount)
}

func (u *Unit) Calculate(ctx context.Context, asOf time.Time) (CalculationResult, error) {
	if u == nil || u.engine == nil {
		return CalculationResult{}, ErrDependenciesRequired
	}
	return u.engine.Calculate(ctx, asOf)
}

// CalculateSimplifiedECL calculates trade receivable losses using the
// lifetime provision matrix required by the simplified approach.
func CalculateSimplifiedECL(asOf time.Time, matrix map[subledgers.AgingBucket]decimal.Decimal, receivables []Exposure) (CalculationResult, error) {
	if asOf.IsZero() {
		return CalculationResult{}, ErrInvalidDate
	}
	if len(receivables) == 0 {
		return CalculationResult{AsOf: asOf.UTC()}, nil
	}
	currency := ""
	var companyID uuid.UUID
	for _, exposure := range receivables {
		line := exposure.Receivable
		if currency == "" {
			currency, companyID = line.ResidualAmount.Currency(), line.CompanyID
		} else if currency != line.ResidualAmount.Currency() || companyID != line.CompanyID {
			return CalculationResult{}, ErrInvalidExposure
		}
	}
	if currency == "" {
		return CalculationResult{}, ErrInvalidConfiguration
	}
	config := Config{CompanyID: companyID, JournalID: uuid.New(), ImpairmentAccountID: uuid.New(),
		AllowanceAccountID: uuid.New(), Currency: currency, ProvisionMatrix: matrix}
	zero, err := financialtypes.Zero(currency)
	if err != nil {
		return CalculationResult{}, err
	}
	result, err := Calculate(asOf, config, receivables, zero)
	if err != nil {
		return CalculationResult{}, err
	}
	// This convenience API has no posting-account configuration; only the
	// configured Unit can produce a journal draft suitable for posting.
	result.JournalEntry = nil
	return result, nil
}

func Calculate(asOf time.Time, config Config, exposures []Exposure, existingAllowance financialtypes.Money) (CalculationResult, error) {
	var err error
	config, err = normalizeConfig(config)
	if err != nil {
		return CalculationResult{}, err
	}
	if err := config.Validate(); err != nil {
		return CalculationResult{}, err
	}
	if asOf.IsZero() {
		return CalculationResult{}, ErrInvalidDate
	}
	if existingAllowance.Currency() != config.Currency || existingAllowance.IsNegative() {
		return CalculationResult{}, ErrInvalidAllowance
	}
	required, err := financialtypes.Zero(config.Currency)
	if err != nil {
		return CalculationResult{}, err
	}
	results := make([]ReceivableResult, 0, len(exposures))
	seen := make(map[uuid.UUID]struct{}, len(exposures))

	sorted := append([]Exposure(nil), exposures...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Receivable.ID.String() < sorted[j].Receivable.ID.String()
	})
	for _, exposure := range sorted {
		line := exposure.Receivable
		if line.ID == uuid.Nil || line.CompanyID != config.CompanyID ||
			line.Kind != subledgers.LineReceivable || line.DueDate.IsZero() ||
			line.ResidualAmount.Currency() != config.Currency ||
			line.OriginalAmount.Currency() != config.Currency ||
			line.ResidualAmount.IsNegative() || line.OriginalAmount.IsNegative() {
			return CalculationResult{}, fmt.Errorf("%w: %s", ErrInvalidExposure, line.ID)
		}
		if _, exists := seen[line.ID]; exists {
			return CalculationResult{}, fmt.Errorf("%w: duplicate receivable %s", ErrInvalidExposure, line.ID)
		}
		seen[line.ID] = struct{}{}
		if line.Closed || line.ResidualAmount.Amount().IsZero() {
			continue
		}

		daysPastDue := daysPastDue(asOf, line.DueDate)
		bucket := agingBucket(daysPastDue)
		stage := classifyStage(daysPastDue, exposure.SICR, exposure.Defaulted)
		expected, err := line.ResidualAmount.Multiply(config.ProvisionMatrix[bucket])
		if err != nil {
			return CalculationResult{}, err
		}
		expected = expected.RoundForPosting()
		required, err = required.Add(expected)
		if err != nil {
			return CalculationResult{}, err
		}
		results = append(results, ReceivableResult{
			ReceivableID: line.ID,
			Stage:        stage,
			DaysPastDue:  daysPastDue,
			AgingBucket:  bucket,
			ECL:          expected,
		})
	}

	adjustment, err := required.Subtract(existingAllowance)
	if err != nil {
		return CalculationResult{}, err
	}
	adjustment = adjustment.RoundForPosting()
	result := CalculationResult{
		AsOf:              asOf.UTC(),
		Receivables:       results,
		RequiredAllowance: required,
		ExistingAllowance: existingAllowance,
		Adjustment:        adjustment,
	}
	if !adjustment.Amount().IsZero() {
		entry, err := makeAdjustmentEntry(config, asOf, adjustment)
		if err != nil {
			return CalculationResult{}, err
		}
		result.JournalEntry = &entry
	}
	return result, nil
}

func classifyStage(days int, sicr, defaulted bool) Stage {
	if defaulted || days > 90 {
		return Stage3
	}
	if sicr || days > 30 {
		return Stage2
	}
	return Stage1
}

func daysPastDue(asOf, dueDate time.Time) int {
	asOf = dateOnlyUTC(asOf)
	dueDate = dateOnlyUTC(dueDate)
	if !dueDate.Before(asOf) {
		return 0
	}
	return int(asOf.Sub(dueDate) / (24 * time.Hour))
}

func dateOnlyUTC(value time.Time) time.Time {
	year, month, day := value.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func agingBucket(days int) subledgers.AgingBucket {
	switch {
	case days <= 0:
		return subledgers.AgingCurrent
	case days <= 30:
		return subledgers.Aging1To30
	case days <= 60:
		return subledgers.Aging31To60
	case days <= 90:
		return subledgers.Aging61To90
	default:
		return subledgers.AgingOver90
	}
}

func makeAdjustmentEntry(config Config, asOf time.Time, adjustment financialtypes.Money) (posting.JournalEntry, error) {
	amount := adjustment
	if amount.IsNegative() {
		var err error
		amount, err = financialtypes.NewMoney(amount.Amount().Neg(), amount.Currency())
		if err != nil {
			return posting.JournalEntry{}, err
		}
	}
	impairment := posting.JournalEntryLine{CompanyID: config.CompanyID}
	allowance := posting.JournalEntryLine{CompanyID: config.CompanyID}
	if adjustment.Amount().IsPositive() {
		impairment.AccountID = config.ImpairmentAccountID
		impairment.Debit = amount
		allowance.AccountID = config.AllowanceAccountID
		allowance.Credit = amount
	} else {
		allowance.AccountID = config.AllowanceAccountID
		allowance.Debit = amount
		impairment.AccountID = config.ImpairmentAccountID
		impairment.Credit = amount
	}

	seed := strings.Join([]string{
		config.CompanyID.String(), config.JournalID.String(),
		config.ImpairmentAccountID.String(), config.AllowanceAccountID.String(),
		asOf.UTC().Format(time.RFC3339Nano), config.Currency, adjustment.Amount().String(),
	}, ":")
	entryID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(seed))
	impairment.ID = uuid.NewSHA1(entryID, []byte("impairment"))
	allowance.ID = uuid.NewSHA1(entryID, []byte("allowance"))
	date := asOf.UTC()
	entry := posting.JournalEntry{
		CompanyID:      config.CompanyID,
		ID:             entryID,
		JournalID:      config.JournalID,
		EntryDate:      date,
		Date:           date,
		Status:         posting.EntryDraft,
		CreatedAt:      date,
		Classification: posting.EntryClassAdjusting,
		Lines:          []posting.JournalEntryLine{impairment, allowance},
	}
	if err := entry.Validate(); err != nil {
		return posting.JournalEntry{}, err
	}
	return entry, nil
}

var allAgingBuckets = [...]subledgers.AgingBucket{
	subledgers.AgingCurrent,
	subledgers.Aging1To30,
	subledgers.Aging31To60,
	subledgers.Aging61To90,
	subledgers.AgingOver90,
}

func validAgingBucket(bucket subledgers.AgingBucket) bool {
	for _, valid := range allAgingBuckets {
		if bucket == valid {
			return true
		}
	}
	return false
}
