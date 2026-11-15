package ifrs15

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
)

var (
	ErrRevenueMappingRequired     = errors.New("IFRS 15 journal mapping is required")
	ErrRevenueApplicationRequired = errors.New("IFRS 15 posting application is required")
	ErrRevenueEvidenceRequired    = errors.New("external service-delivery evidence is required")
	ErrRevenueEventConflict       = errors.New("revenue event ID was reused with different inputs")
	ErrRevenueAssessmentRequired  = errors.New("IFRS 15 contract assessment is incomplete or unsupported")
)

// FiveStepAssessment contains host-assessed contract facts. The engine validates
// their presence; it does not make legal, credit, or control-transfer judgments.
type FiveStepAssessment struct {
	ContractID                  uuid.UUID
	CustomerID                  uuid.UUID
	Approved                    bool
	EnforceableRightsIdentified bool
	PaymentTermsIdentified      bool
	CommercialSubstance         bool
	CollectibilityProbable      bool
	FixedTransactionPrice       bool
	HasVariableConsideration    bool
	HasSignificantFinancing     bool
	DistinctObligationIDs       []uuid.UUID
	TransactionPrice            financialtypes.Money
	Obligations                 []PerformanceObligation
}

// NewAssessedContract validates the supported five-step inputs, then applies
// relative standalone-selling-price allocation. Variable consideration and
// significant financing components are rejected rather than estimated.
func NewAssessedContract(input FiveStepAssessment) (Contract, error) {
	if input.ContractID == uuid.Nil || input.CustomerID == uuid.Nil ||
		!input.Approved || !input.EnforceableRightsIdentified ||
		!input.PaymentTermsIdentified || !input.CommercialSubstance ||
		!input.CollectibilityProbable || !input.FixedTransactionPrice ||
		input.HasVariableConsideration || input.HasSignificantFinancing {
		return Contract{}, ErrRevenueAssessmentRequired
	}
	if len(input.DistinctObligationIDs) != len(input.Obligations) || len(input.Obligations) == 0 {
		return Contract{}, fmt.Errorf("%w: all performance obligations must be host-assessed as distinct", ErrRevenueAssessmentRequired)
	}
	distinct := make(map[uuid.UUID]struct{}, len(input.DistinctObligationIDs))
	for _, id := range input.DistinctObligationIDs {
		if id == uuid.Nil {
			return Contract{}, ErrRevenueAssessmentRequired
		}
		if _, exists := distinct[id]; exists {
			return Contract{}, fmt.Errorf("%w: duplicate distinct-obligation assessment", ErrRevenueAssessmentRequired)
		}
		distinct[id] = struct{}{}
	}
	for _, obligation := range input.Obligations {
		if _, ok := distinct[obligation.ID]; !ok {
			return Contract{}, fmt.Errorf("%w: obligation %s has no distinct assessment", ErrRevenueAssessmentRequired, obligation.ID)
		}
		delete(distinct, obligation.ID)
	}
	if len(distinct) != 0 {
		return Contract{}, ErrRevenueAssessmentRequired
	}
	contract, err := NewContract(input.ContractID, input.CustomerID, input.TransactionPrice, input.Obligations)
	if err != nil {
		return Contract{}, err
	}
	return contract, nil
}

// RevenueJournalMapping binds the workflow to real ledger identifiers.
type RevenueJournalMapping struct {
	CompanyID                uuid.UUID
	JournalID                uuid.UUID
	RevenueAccountID         uuid.UUID
	ContractAssetAccountID   uuid.UUID
	ContractLiabilityAccount uuid.UUID
}

func (m RevenueJournalMapping) Validate() error {
	if m.CompanyID == uuid.Nil || m.JournalID == uuid.Nil ||
		m.RevenueAccountID == uuid.Nil || m.ContractAssetAccountID == uuid.Nil ||
		m.ContractLiabilityAccount == uuid.Nil ||
		m.RevenueAccountID == m.ContractAssetAccountID ||
		m.RevenueAccountID == m.ContractLiabilityAccount ||
		m.ContractAssetAccountID == m.ContractLiabilityAccount {
		return ErrRevenueMappingRequired
	}
	return nil
}

// RevenuePostingApplication is an application-layer posting capability. Its
// implementation must honor idempotencyKey: replaying the same key and entry
// must return success for the original posting, while a changed payload must fail.
type RevenuePostingApplication interface {
	PostRevenueEntry(ctx context.Context, entry posting.JournalEntry, idempotencyKey string) error
}

// ServiceDeliveryEvidence is supplied by the host; IFRS 15 does not infer that
// delivery, acceptance, or progress occurred from dates or schedules.
type ServiceDeliveryEvidence struct {
	Satisfied         bool
	MeasureBasis      string
	EvidenceReference string
}

// SatisfactionEvent is a host-measured amount for one performance obligation.
type SatisfactionEvent struct {
	ID           uuid.UUID
	ObligationID uuid.UUID
	Amount       financialtypes.Money
	Date         time.Time
	Evidence     ServiceDeliveryEvidence
}

type pendingRecognition struct {
	hash  string
	entry posting.JournalEntry
}

type postedRecognition struct {
	hash     string
	contract Contract
	entry    posting.JournalEntry
}

// RevenueWorkflow performs only measured recognition and commits in-memory
// contract state after the configured application confirms successful posting.
type RevenueWorkflow struct {
	mapping RevenueJournalMapping
	app     RevenuePostingApplication

	mu             sync.RWMutex
	contracts      map[uuid.UUID]Contract
	recognizedByPO map[uuid.UUID]map[uuid.UUID]financialtypes.Money
	pending        map[string]pendingRecognition
	posted         map[string]postedRecognition
}

func NewRevenueWorkflow(mapping RevenueJournalMapping, app RevenuePostingApplication) (*RevenueWorkflow, error) {
	if err := mapping.Validate(); err != nil {
		return nil, err
	}
	if app == nil {
		return nil, ErrRevenueApplicationRequired
	}
	return &RevenueWorkflow{
		mapping: mapping, app: app,
		contracts:      make(map[uuid.UUID]Contract),
		recognizedByPO: make(map[uuid.UUID]map[uuid.UUID]financialtypes.Money),
		pending:        make(map[string]pendingRecognition),
		posted:         make(map[string]postedRecognition),
	}, nil
}

func (w *RevenueWorkflow) AddContract(input FiveStepAssessment) (Contract, error) {
	if w == nil {
		return Contract{}, ErrInvalidContract
	}
	contract, err := NewAssessedContract(input)
	if err != nil {
		return Contract{}, err
	}
	if !contract.RecognizedAmount.Amount().IsZero() {
		return Contract{}, ErrInvalidContract
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, exists := w.contracts[contract.ID]; exists {
		return Contract{}, ErrInvalidContract
	}
	contract = cloneContract(contract)
	zero, err := financialtypes.Zero(contract.Currency)
	if err != nil {
		return Contract{}, err
	}
	recognized := make(map[uuid.UUID]financialtypes.Money, len(contract.Obligations))
	for _, obligation := range contract.Obligations {
		recognized[obligation.ID] = zero
	}
	w.contracts[contract.ID] = contract
	w.recognizedByPO[contract.ID] = recognized
	return cloneContract(contract), nil
}

func (w *RevenueWorkflow) Contract(id uuid.UUID) (Contract, bool) {
	if w == nil {
		return Contract{}, false
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	contract, ok := w.contracts[id]
	return cloneContract(contract), ok
}

// Recognize posts the amount measured by the host and commits contract state
// only after posting succeeds. Event IDs are idempotency keys; a retry after an
// uncertain posting response reuses the exact journal draft and key.
func (w *RevenueWorkflow) Recognize(
	ctx context.Context,
	contractID uuid.UUID,
	event SatisfactionEvent,
) (Contract, posting.JournalEntry, error) {
	if w == nil {
		return Contract{}, posting.JournalEntry{}, ErrInvalidContract
	}
	if err := ctx.Err(); err != nil {
		return Contract{}, posting.JournalEntry{}, err
	}
	if event.ID == uuid.Nil || event.ObligationID == uuid.Nil || event.Date.IsZero() ||
		event.Amount.Currency() == "" || !event.Amount.Amount().IsPositive() ||
		!event.Evidence.Satisfied || strings.TrimSpace(event.Evidence.MeasureBasis) == "" ||
		strings.TrimSpace(event.Evidence.EvidenceReference) == "" {
		return Contract{}, posting.JournalEntry{}, ErrRevenueEvidenceRequired
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	contract, ok := w.contracts[contractID]
	if !ok {
		return Contract{}, posting.JournalEntry{}, ErrContractNotFound
	}
	key := contractID.String() + ":" + event.ID.String()
	hash, err := satisfactionHash(contractID, event)
	if err != nil {
		return Contract{}, posting.JournalEntry{}, err
	}
	if result, exists := w.posted[key]; exists {
		if result.hash != hash {
			return Contract{}, posting.JournalEntry{}, ErrRevenueEventConflict
		}
		return cloneContract(result.contract), result.entry, nil
	}
	pending, hasPending := w.pending[key]
	if hasPending && pending.hash != hash {
		return Contract{}, posting.JournalEntry{}, ErrRevenueEventConflict
	}
	allocated, exists := contract.AllocatedPrices[event.ObligationID]
	if !exists {
		return Contract{}, posting.JournalEntry{}, ErrObligationNotFound
	}
	if event.Amount.Currency() != contract.Currency {
		return Contract{}, posting.JournalEntry{}, ErrInvalidAmount
	}
	nextPOAmount, err := w.recognizedByPO[contractID][event.ObligationID].Add(event.Amount)
	if err != nil || nextPOAmount.GreaterThan(allocated) {
		return Contract{}, posting.JournalEntry{}, ErrRecognitionExceeded
	}
	updated, err := contract.ApplyRecognition(event.Amount)
	if err != nil {
		return Contract{}, posting.JournalEntry{}, err
	}
	if !hasPending {
		entry, buildErr := BuildRecognitionJournal(
			contract, event.Amount, w.mapping.JournalID, event.Date,
			w.mapping.RevenueAccountID, w.mapping.ContractLiabilityAccount,
			w.mapping.ContractAssetAccountID,
		)
		if buildErr != nil {
			return Contract{}, posting.JournalEntry{}, buildErr
		}
		entry.SourceModule = "ifrs15"
		entry.DocumentReference = event.ID.String()
		entry.IdempotencyKey = key
		entry.IdempotencyPayloadHash = hash
		entry.ID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(key))
		customerID := contract.CustomerID
		for i := range entry.Lines {
			entry.Lines[i].CompanyID = w.mapping.CompanyID
			entry.Lines[i].PartnerID = &customerID
		}
		if entry.CompanyID == uuid.Nil {
			entry.CompanyID = w.mapping.CompanyID
		}
		pending = pendingRecognition{hash: hash, entry: entry}
		w.pending[key] = pending
	}
	if err := w.app.PostRevenueEntry(ctx, pending.entry, key); err != nil {
		return Contract{}, posting.JournalEntry{}, err
	}

	w.contracts[contractID] = cloneContract(updated)
	w.recognizedByPO[contractID][event.ObligationID] = nextPOAmount
	w.posted[key] = postedRecognition{hash: hash, contract: cloneContract(updated), entry: pending.entry}
	delete(w.pending, key)
	return cloneContract(updated), pending.entry, nil
}

func satisfactionHash(contractID uuid.UUID, event SatisfactionEvent) (string, error) {
	payload := struct {
		ContractID        string `json:"contract_id"`
		EventID           string `json:"event_id"`
		ObligationID      string `json:"obligation_id"`
		Amount            string `json:"amount"`
		Currency          string `json:"currency"`
		Date              string `json:"date"`
		Satisfied         bool   `json:"satisfied"`
		MeasureBasis      string `json:"measure_basis"`
		EvidenceReference string `json:"evidence_reference"`
	}{
		ContractID: contractID.String(), EventID: event.ID.String(),
		ObligationID: event.ObligationID.String(), Amount: event.Amount.Amount().String(),
		Currency: event.Amount.Currency(), Date: event.Date.UTC().Format(time.RFC3339Nano),
		Satisfied:         event.Evidence.Satisfied,
		MeasureBasis:      strings.TrimSpace(event.Evidence.MeasureBasis),
		EvidenceReference: strings.TrimSpace(event.Evidence.EvidenceReference),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func cloneContract(contract Contract) Contract {
	contract.Obligations = append([]PerformanceObligation(nil), contract.Obligations...)
	allocations := make(map[uuid.UUID]financialtypes.Money, len(contract.AllocatedPrices))
	for id, amount := range contract.AllocatedPrices {
		allocations[id] = amount
	}
	contract.AllocatedPrices = allocations
	return contract
}
