package ifrs9

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

var (
	ErrECLPostingCapabilityUnavailable = errors.New("IFRS 9 ledger-posting capability unavailable")
	ErrECLJournalMappingUnavailable    = errors.New("IFRS 9 journal mapping unavailable")
	ErrECLZeroAdjustment               = errors.New("IFRS 9 ECL adjustment is zero")
	ErrECLInvalidAccounts              = errors.New("IFRS 9 ECL accounts invalid")
)

// ECLPostingCapability resolves configured accounts and checks posting availability.
type ECLPostingCapability interface {
	LedgerPostingAvailable() bool
	ResolveECLAccounts(ctx context.Context, companyID uuid.UUID, mappingID string) (ECLAccounts, error)
}

// ECLAccounts defines the general ledger accounts for impairment loss and allowance.
type ECLAccounts struct {
	ImpairmentLoss uuid.UUID
	Allowance      uuid.UUID
}

// ECLPostingApplication defines the host application service for posting ECL entries.
type ECLPostingApplication interface {
	PostECLEntry(ctx context.Context, entry posting.JournalEntry, idempotencyKey string) error
}

const (
	DiscountMethodEIRSingleExpectedLossDate = "eir_single_expected_loss_date"
	stage2BackstopDays                      = 30
	stage3BackstopDays                      = 90
	stage1MeasurementHorizon                = 365 * 24 * time.Hour
)

var (
	ErrInvalidVersionedInput  = errors.New("invalid versioned ECL input")
	ErrInvalidStageTransition = errors.New("invalid IFRS 9 stage transition")
)

type VersionedScenario struct {
	Name   string
	Weight decimal.Decimal
}

type ScenarioPDInput struct {
	ScenarioName string
	PD12Month    decimal.Decimal
	PDLifetime   decimal.Decimal
}

// StageEvidence is the host-supplied assessment needed to assign a bounded
// ECL stage. The host remains responsible for determining SICR and default.
type StageEvidence struct {
	PreviousStage Stage
	DaysPastDue   int
	SICR          bool
	Defaulted     bool
	CureConfirmed bool
}

type VersionedExposure struct {
	ID                    uuid.UUID
	EAD                   financialtypes.Money
	RemainingLife         time.Duration
	EffectiveInterestRate decimal.Decimal
	LGD                   decimal.Decimal
	StageEvidence         StageEvidence
	ScenarioPDs           []ScenarioPDInput
}

type NamedAssumption struct {
	Name  string
	Value string
}

// VersionedECLInput contains the complete explicit input needed for one
// reproducible general-approach ECL run. It is an in-memory measurement API;
// it does not persist inputs or create ledger entries.
type VersionedECLInput struct {
	EvaluationDate    time.Time
	ModelVersion      string
	SourceDataID      string
	SourceDataHash    string
	DiscountMethod    string
	Assumptions       []NamedAssumption
	Scenarios         []VersionedScenario
	Exposures         []VersionedExposure
	ExistingAllowance financialtypes.Money
}

type VersionedExposureResult struct {
	ExposureID    uuid.UUID
	PreviousStage Stage
	Stage         Stage
	Transition    string
	Horizon       time.Duration
	PD            decimal.Decimal
	LGD           decimal.Decimal
	Discount      decimal.Decimal
	ECL           financialtypes.Money
}

type VersionedECLResult struct {
	EvaluationDate    time.Time
	ModelVersion      string
	SourceDataID      string
	SourceDataHash    string
	InputFingerprint  string
	Assumptions       []NamedAssumption
	Exposures         []VersionedExposureResult
	RequiredAllowance financialtypes.Money
	ExistingAllowance financialtypes.Money
	AllowanceChange   financialtypes.Money
}

// EvaluateVersionedECL calculates a probability-weighted Stage 1/2/3 ECL
// allowance and fingerprints all model, assumption, source, and measurement
// inputs. Scenario PDs, exposure data, stage evidence, and source snapshots are
// supplied by the caller; this function does not infer or persist them.
func EvaluateVersionedECL(input VersionedECLInput) (VersionedECLResult, error) {
	input, scenarios, err := validateVersionedInput(input)
	if err != nil {
		return VersionedECLResult{}, err
	}
	fingerprint := fingerprintVersionedInput(input, scenarios)
	results := make([]VersionedExposureResult, 0, len(input.Exposures))
	total := decimal.Zero
	currency := input.ExistingAllowance.Currency()
	for _, exposure := range sortedVersionedExposures(input.Exposures) {
		stage, transition, err := assessStage(exposure.StageEvidence)
		if err != nil {
			return VersionedECLResult{}, fmt.Errorf("%w: exposure %s: %v", ErrInvalidStageTransition, exposure.ID, err)
		}
		pdByScenario := make(map[string]ScenarioPDInput, len(exposure.ScenarioPDs))
		for _, estimate := range exposure.ScenarioPDs {
			pdByScenario[estimate.ScenarioName] = estimate
		}
		weightedPD := decimal.Zero
		for _, scenario := range scenarios {
			estimate := pdByScenario[scenario.Name]
			pd := estimate.PDLifetime
			if stage == Stage1 {
				pd = estimate.PD12Month
			}
			weightedPD = weightedPD.Add(scenario.Weight.Mul(pd))
		}
		horizon := exposure.RemainingLife
		if stage == Stage1 && horizon > stage1MeasurementHorizon {
			horizon = stage1MeasurementHorizon
		}
		discount, err := DiscountAtEIR(exposure.EffectiveInterestRate, horizon)
		if err != nil {
			return VersionedECLResult{}, err
		}
		amount := exposure.EAD.Amount().Mul(weightedPD).Mul(exposure.LGD).Mul(discount)
		ecl, err := financialtypes.NewMoney(amount, currency)
		if err != nil {
			return VersionedECLResult{}, err
		}
		ecl = ecl.RoundForPosting()
		total = total.Add(ecl.Amount())
		results = append(results, VersionedExposureResult{
			ExposureID: exposure.ID, PreviousStage: exposure.StageEvidence.PreviousStage,
			Stage: stage, Transition: transition, Horizon: horizon,
			PD: weightedPD, LGD: exposure.LGD, Discount: discount, ECL: ecl,
		})
	}
	required, err := financialtypes.NewMoney(total, currency)
	if err != nil {
		return VersionedECLResult{}, err
	}
	required = required.RoundForPosting()
	change, err := required.Subtract(input.ExistingAllowance)
	if err != nil {
		return VersionedECLResult{}, err
	}
	change = change.RoundForPosting()
	return VersionedECLResult{
		EvaluationDate: input.EvaluationDate.UTC(), ModelVersion: input.ModelVersion,
		SourceDataID: input.SourceDataID, SourceDataHash: input.SourceDataHash,
		InputFingerprint: fingerprint, Assumptions: append([]NamedAssumption(nil), input.Assumptions...),
		Exposures: results, RequiredAllowance: required, ExistingAllowance: input.ExistingAllowance,
		AllowanceChange: change,
	}, nil
}

func validateVersionedInput(input VersionedECLInput) (VersionedECLInput, []VersionedScenario, error) {
	if input.EvaluationDate.IsZero() ||
		strings.TrimSpace(input.ModelVersion) == "" ||
		strings.TrimSpace(input.SourceDataID) == "" ||
		!validSHA256SourceHash(input.SourceDataHash) ||
		input.DiscountMethod != DiscountMethodEIRSingleExpectedLossDate ||
		len(input.Assumptions) == 0 || len(input.Scenarios) == 0 || len(input.Exposures) == 0 ||
		input.ExistingAllowance.Currency() == "" || input.ExistingAllowance.IsNegative() {
		return VersionedECLInput{}, nil, ErrInvalidVersionedInput
	}
	assumptions := append([]NamedAssumption(nil), input.Assumptions...)
	sort.Slice(assumptions, func(i, j int) bool { return assumptions[i].Name < assumptions[j].Name })
	for i, assumption := range assumptions {
		if strings.TrimSpace(assumption.Name) == "" || strings.TrimSpace(assumption.Value) == "" ||
			(i > 0 && assumptions[i-1].Name == assumption.Name) {
			return VersionedECLInput{}, nil, ErrInvalidVersionedInput
		}
	}
	requiredAssumptions := map[string]string{
		"stage1_horizon":       "12 months",
		"stage2_backstop_days": "30",
		"stage3_backstop_days": "90",
		"discount_method":      DiscountMethodEIRSingleExpectedLossDate,
	}
	for _, assumption := range assumptions {
		if expected, ok := requiredAssumptions[assumption.Name]; ok {
			if assumption.Value != expected {
				return VersionedECLInput{}, nil, ErrInvalidVersionedInput
			}
			delete(requiredAssumptions, assumption.Name)
		}
	}
	if len(requiredAssumptions) != 0 {
		return VersionedECLInput{}, nil, ErrInvalidVersionedInput
	}
	input.Assumptions = assumptions

	scenarios := append([]VersionedScenario(nil), input.Scenarios...)
	sort.Slice(scenarios, func(i, j int) bool { return scenarios[i].Name < scenarios[j].Name })
	totalWeight := decimal.Zero
	for i, scenario := range scenarios {
		if strings.TrimSpace(scenario.Name) == "" || !fraction(scenario.Weight) ||
			(i > 0 && scenarios[i-1].Name == scenario.Name) {
			return VersionedECLInput{}, nil, ErrInvalidVersionedInput
		}
		totalWeight = totalWeight.Add(scenario.Weight)
	}
	if !totalWeight.Equal(decimal.NewFromInt(1)) {
		return VersionedECLInput{}, nil, ErrInvalidVersionedInput
	}

	seen := make(map[uuid.UUID]struct{}, len(input.Exposures))
	for _, exposure := range input.Exposures {
		if exposure.ID == uuid.Nil || exposure.EAD.Currency() != input.ExistingAllowance.Currency() ||
			exposure.EAD.IsNegative() || exposure.RemainingLife <= 0 ||
			!exposure.EffectiveInterestRate.GreaterThan(decimal.NewFromInt(-1)) ||
			!fraction(exposure.LGD) {
			return VersionedECLInput{}, nil, ErrInvalidVersionedInput
		}
		if _, ok := seen[exposure.ID]; ok {
			return VersionedECLInput{}, nil, ErrInvalidVersionedInput
		}
		seen[exposure.ID] = struct{}{}
		if _, _, err := assessStage(exposure.StageEvidence); err != nil {
			return VersionedECLInput{}, nil, fmt.Errorf("%w: exposure %s: %v", ErrInvalidStageTransition, exposure.ID, err)
		}
		estimates := make(map[string]ScenarioPDInput, len(exposure.ScenarioPDs))
		for _, estimate := range exposure.ScenarioPDs {
			if !fraction(estimate.PD12Month) || !fraction(estimate.PDLifetime) {
				return VersionedECLInput{}, nil, ErrInvalidVersionedInput
			}
			if _, ok := estimates[estimate.ScenarioName]; ok {
				return VersionedECLInput{}, nil, ErrInvalidVersionedInput
			}
			estimates[estimate.ScenarioName] = estimate
		}
		if len(estimates) != len(scenarios) {
			return VersionedECLInput{}, nil, ErrInvalidVersionedInput
		}
		for _, scenario := range scenarios {
			if _, ok := estimates[scenario.Name]; !ok {
				return VersionedECLInput{}, nil, ErrInvalidVersionedInput
			}
		}
	}
	if _, err := financialtypes.NewMoney(decimal.Zero, input.ExistingAllowance.Currency()); err != nil {
		return VersionedECLInput{}, nil, ErrInvalidVersionedInput
	}
	return input, scenarios, nil
}

func validSHA256SourceHash(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	digest, err := hex.DecodeString(strings.TrimPrefix(value, prefix))
	return err == nil && len(digest) == sha256.Size
}

func assessStage(evidence StageEvidence) (Stage, string, error) {
	if (evidence.PreviousStage != "" && evidence.PreviousStage != Stage1 &&
		evidence.PreviousStage != Stage2 && evidence.PreviousStage != Stage3) ||
		evidence.DaysPastDue < 0 ||
		(evidence.CureConfirmed && evidence.PreviousStage != Stage3) {
		return "", "", ErrInvalidStageTransition
	}
	stage := Stage1
	switch {
	case evidence.Defaulted || evidence.DaysPastDue > stage3BackstopDays:
		stage = Stage3
	case evidence.PreviousStage == Stage3 && !evidence.CureConfirmed:
		stage = Stage3
	case evidence.SICR || evidence.DaysPastDue > stage2BackstopDays:
		stage = Stage2
	}
	transition := "initial"
	switch {
	case evidence.PreviousStage == "":
	case evidence.PreviousStage == stage:
		transition = "no_change"
	case evidence.PreviousStage == Stage3:
		transition = "cured_credit_impaired"
	case stage == Stage3:
		transition = "credit_impaired"
	case stage == Stage2:
		transition = "increased_credit_risk"
	case stage == Stage1:
		transition = "improved_credit_risk"
	}
	return stage, transition, nil
}

func sortedVersionedExposures(exposures []VersionedExposure) []VersionedExposure {
	sorted := append([]VersionedExposure(nil), exposures...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID.String() < sorted[j].ID.String() })
	return sorted
}

func fingerprintVersionedInput(input VersionedECLInput, scenarios []VersionedScenario) string {
	var canonical bytes.Buffer
	writeFingerprintField(&canonical, "ifrs9-versioned-ecl-v1")
	writeFingerprintField(&canonical, input.EvaluationDate.UTC().Format(time.RFC3339Nano))
	writeFingerprintField(&canonical, input.ModelVersion)
	writeFingerprintField(&canonical, input.SourceDataID)
	writeFingerprintField(&canonical, input.SourceDataHash)
	writeFingerprintField(&canonical, input.DiscountMethod)
	writeFingerprintField(&canonical, input.ExistingAllowance.Amount().String())
	writeFingerprintField(&canonical, input.ExistingAllowance.Currency())
	writeFingerprintField(&canonical, "assumptions")
	writeFingerprintField(&canonical, fmt.Sprint(len(input.Assumptions)))
	for _, assumption := range input.Assumptions {
		writeFingerprintField(&canonical, assumption.Name)
		writeFingerprintField(&canonical, assumption.Value)
	}
	writeFingerprintField(&canonical, "scenarios")
	writeFingerprintField(&canonical, fmt.Sprint(len(scenarios)))
	for _, scenario := range scenarios {
		writeFingerprintField(&canonical, scenario.Name)
		writeFingerprintField(&canonical, scenario.Weight.String())
	}
	exposures := sortedVersionedExposures(input.Exposures)
	writeFingerprintField(&canonical, "exposures")
	writeFingerprintField(&canonical, fmt.Sprint(len(exposures)))
	for _, exposure := range exposures {
		writeFingerprintField(&canonical, "exposure")
		writeFingerprintField(&canonical, exposure.ID.String())
		writeFingerprintField(&canonical, exposure.EAD.Amount().String())
		writeFingerprintField(&canonical, exposure.EAD.Currency())
		writeFingerprintField(&canonical, fmt.Sprint(int64(exposure.RemainingLife)))
		writeFingerprintField(&canonical, exposure.EffectiveInterestRate.String())
		writeFingerprintField(&canonical, exposure.LGD.String())
		writeFingerprintField(&canonical, string(exposure.StageEvidence.PreviousStage))
		writeFingerprintField(&canonical, fmt.Sprint(exposure.StageEvidence.DaysPastDue))
		writeFingerprintField(&canonical, fmt.Sprint(exposure.StageEvidence.SICR))
		writeFingerprintField(&canonical, fmt.Sprint(exposure.StageEvidence.Defaulted))
		writeFingerprintField(&canonical, fmt.Sprint(exposure.StageEvidence.CureConfirmed))
		estimates := append([]ScenarioPDInput(nil), exposure.ScenarioPDs...)
		sort.Slice(estimates, func(i, j int) bool { return estimates[i].ScenarioName < estimates[j].ScenarioName })
		writeFingerprintField(&canonical, "scenario-pds")
		writeFingerprintField(&canonical, fmt.Sprint(len(estimates)))
		for _, estimate := range estimates {
			writeFingerprintField(&canonical, estimate.ScenarioName)
			writeFingerprintField(&canonical, estimate.PD12Month.String())
			writeFingerprintField(&canonical, estimate.PDLifetime.String())
		}
	}
	sum := sha256.Sum256(canonical.Bytes())
	return hex.EncodeToString(sum[:])
}

func writeFingerprintField(buffer *bytes.Buffer, value string) {
	fmt.Fprintf(buffer, "%d:", len(value))
	buffer.WriteString(value)
}

// BuildVersionedECLEntry builds a balanced, deterministic journal draft for the ECL movement.
func BuildVersionedECLEntry(
	result VersionedECLResult,
	accounts ECLAccounts,
	companyID, journalID uuid.UUID,
	createdBy string,
) (posting.JournalEntry, error) {
	if companyID == uuid.Nil || journalID == uuid.Nil {
		return posting.JournalEntry{}, ErrInvalidConfiguration
	}
	if accounts.ImpairmentLoss == uuid.Nil || accounts.Allowance == uuid.Nil || accounts.ImpairmentLoss == accounts.Allowance {
		return posting.JournalEntry{}, ErrECLInvalidAccounts
	}
	if result.AllowanceChange.Amount().IsZero() {
		return posting.JournalEntry{}, ErrECLZeroAdjustment
	}
	currency := result.AllowanceChange.Currency()
	zeroMoney, err := financialtypes.Zero(currency)
	if err != nil {
		return posting.JournalEntry{}, err
	}

	lines := make([]posting.JournalEntryLine, 0, 2)
	absAmount := result.AllowanceChange.Amount().Abs()
	changeMoney, err := financialtypes.NewMoney(absAmount, currency)
	if err != nil {
		return posting.JournalEntry{}, err
	}

	if result.AllowanceChange.Amount().IsPositive() {
		// Increase in allowance: Dr Impairment Loss (P&L), Cr Allowance (Balance Sheet)
		lines = append(lines,
			posting.JournalEntryLine{
				ID: uuid.New(), CompanyID: companyID, AccountID: accounts.ImpairmentLoss,
				Debit: changeMoney, Credit: zeroMoney,
			},
			posting.JournalEntryLine{
				ID: uuid.New(), CompanyID: companyID, AccountID: accounts.Allowance,
				Debit: zeroMoney, Credit: changeMoney,
			},
		)
	} else {
		// Decrease in allowance (recovery): Dr Allowance, Cr Impairment Loss (or Gain)
		lines = append(lines,
			posting.JournalEntryLine{
				ID: uuid.New(), CompanyID: companyID, AccountID: accounts.Allowance,
				Debit: changeMoney, Credit: zeroMoney,
			},
			posting.JournalEntryLine{
				ID: uuid.New(), CompanyID: companyID, AccountID: accounts.ImpairmentLoss,
				Debit: zeroMoney, Credit: changeMoney,
			},
		)
	}

	keyMaterial := fmt.Sprintf("ifrs9|%s|%s|%s|%s",
		companyID, journalID, result.ModelVersion, result.InputFingerprint)
	keyHash := sha256.Sum256([]byte(keyMaterial))
	idempotencyKey := "ifrs9-ecl-" + hex.EncodeToString(keyHash[:])

	entry := posting.JournalEntry{
		ID:                uuid.NewSHA1(uuid.NameSpaceOID, []byte(idempotencyKey)),
		CompanyID:         companyID,
		JournalID:         journalID,
		EntryDate:         result.EvaluationDate.UTC(),
		Date:              result.EvaluationDate.UTC(),
		Status:            posting.EntryDraft,
		Classification:    posting.EntryClassRegular,
		Lines:             lines,
		CreatedBy:         createdBy,
		Reference:         fmt.Sprintf("ifrs9-ecl-%s", result.ModelVersion),
		DocumentReference: result.InputFingerprint,
		SourceModule:      "ifrs9",
		IdempotencyKey:    idempotencyKey,
	}

	payload := fmt.Sprintf("%s|%s|%s|%s|%s",
		entry.CompanyID, entry.JournalID, entry.EntryDate.Format(time.RFC3339Nano),
		entry.Reference, entry.DocumentReference)
	for _, line := range entry.Lines {
		payload += fmt.Sprintf("|%s:%s:%s", line.AccountID, line.Debit.Amount().String(), line.Credit.Amount().String())
	}
	payloadSum := sha256.Sum256([]byte(payload))
	entry.IdempotencyPayloadHash = hex.EncodeToString(payloadSum[:])

	if err := entry.Validate(); err != nil {
		return posting.JournalEntry{}, err
	}
	return entry, nil
}

// PostVersionedECL resolves accounts, builds the deterministic draft, and executes posting via application.
func PostVersionedECL(
	ctx context.Context,
	cap ECLPostingCapability,
	app ECLPostingApplication,
	result VersionedECLResult,
	companyID, journalID uuid.UUID,
	mappingID, createdBy string,
) (posting.JournalEntry, error) {
	if cap == nil || !cap.LedgerPostingAvailable() {
		return posting.JournalEntry{}, ErrECLPostingCapabilityUnavailable
	}
	if strings.TrimSpace(mappingID) == "" {
		return posting.JournalEntry{}, ErrECLJournalMappingUnavailable
	}
	if app == nil {
		return posting.JournalEntry{}, ErrECLPostingCapabilityUnavailable
	}
	accounts, err := cap.ResolveECLAccounts(ctx, companyID, mappingID)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	entry, err := BuildVersionedECLEntry(result, accounts, companyID, journalID, createdBy)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	if err := app.PostECLEntry(ctx, entry, entry.IdempotencyKey); err != nil {
		return posting.JournalEntry{}, err
	}
	entry.Status = posting.EntryPosted
	return entry, nil
}
