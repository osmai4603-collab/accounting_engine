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

	// Extended P11-01 assessments:
	VariableEstimate   *VariableConsiderationEstimate
	FinancingComponent *FinancingComponent
}

// NewAssessedContract validates the supported five-step inputs, then applies
// relative standalone-selling-price allocation. Supports fixed transaction price,
// variable consideration (IFRS 15.56-58), and significant financing (IFRS 15.60-65).
func NewAssessedContract(input FiveStepAssessment) (Contract, error) {
	if input.ContractID == uuid.Nil || input.CustomerID == uuid.Nil ||
		!input.Approved || !input.EnforceableRightsIdentified ||
		!input.PaymentTermsIdentified || !input.CommercialSubstance ||
		!input.CollectibilityProbable {
		return Contract{}, ErrRevenueAssessmentRequired
	}
	if !input.FixedTransactionPrice && !input.HasVariableConsideration {
		return Contract{}, ErrRevenueAssessmentRequired
	}
	if input.HasVariableConsideration {
		if input.VariableEstimate == nil || input.VariableEstimate.FinalAmount.Currency() == "" {
			return Contract{}, ErrRevenueAssessmentRequired
		}
	}
	if input.HasSignificantFinancing {
		if input.FinancingComponent == nil {
			return Contract{}, ErrRevenueAssessmentRequired
		}
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

	effectivePrice := input.TransactionPrice
	if input.HasVariableConsideration && input.VariableEstimate != nil {
		if input.FixedTransactionPrice && input.TransactionPrice.Amount().IsPositive() {
			var err error
			effectivePrice, err = input.TransactionPrice.Add(input.VariableEstimate.FinalAmount)
			if err != nil {
				return Contract{}, err
			}
		} else {
			effectivePrice = input.VariableEstimate.FinalAmount
		}
	}
	if input.HasSignificantFinancing && input.FinancingComponent != nil {
		effectivePrice = input.FinancingComponent.TransactionPrice
	}

	contract, err := NewContract(input.ContractID, input.CustomerID, effectivePrice, input.Obligations)
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

// WorkflowOption configures optional RevenueWorkflow features like durable storage.
type WorkflowOption func(*RevenueWorkflow)

// WithWorkflowContractStore binds a durable ContractStore to the revenue workflow.
func WithWorkflowContractStore(store ContractStore) WorkflowOption {
	return func(w *RevenueWorkflow) {
		if store != nil {
			w.store = store
		}
	}
}

// RevenueWorkflow performs only measured recognition and commits durable
// contract state after the configured application confirms successful posting.
type RevenueWorkflow struct {
	mapping RevenueJournalMapping
	app     RevenuePostingApplication
	store   ContractStore

	mu             sync.RWMutex
	contracts      map[uuid.UUID]Contract
	recognizedByPO map[uuid.UUID]map[uuid.UUID]financialtypes.Money
	pending        map[string]pendingRecognition
	posted         map[string]postedRecognition
}

func NewRevenueWorkflow(mapping RevenueJournalMapping, app RevenuePostingApplication, opts ...WorkflowOption) (*RevenueWorkflow, error) {
	if err := mapping.Validate(); err != nil {
		return nil, err
	}
	if app == nil {
		return nil, ErrRevenueApplicationRequired
	}
	w := &RevenueWorkflow{
		mapping:        mapping,
		app:            app,
		store:          NewInMemoryContractStore(),
		contracts:      make(map[uuid.UUID]Contract),
		recognizedByPO: make(map[uuid.UUID]map[uuid.UUID]financialtypes.Money),
		pending:        make(map[string]pendingRecognition),
		posted:         make(map[string]postedRecognition),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(w)
		}
	}

	// Recover existing contracts from durable store if available
	if w.store != nil {
		existing, err := w.store.ListContracts(context.Background(), w.mapping.CompanyID)
		if err == nil {
			for _, c := range existing {
				w.contracts[c.ID] = cloneContract(c)
				recMap := make(map[uuid.UUID]financialtypes.Money, len(c.Obligations))
				zero, _ := financialtypes.Zero(c.Currency)
				for _, obl := range c.Obligations {
					if c.RecognizedPrices != nil {
						if amt, ok := c.RecognizedPrices[obl.ID]; ok {
							recMap[obl.ID] = amt
							continue
						}
					}
					recMap[obl.ID] = zero
				}
				w.recognizedByPO[c.ID] = recMap
			}
		}
	}

	return w, nil
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

	if w.store != nil {
		_ = w.store.SaveContract(context.Background(), w.mapping.CompanyID, contract)
	}

	return cloneContract(contract), nil
}

func (w *RevenueWorkflow) Contract(id uuid.UUID) (Contract, bool) {
	if w == nil {
		return Contract{}, false
	}
	w.mu.RLock()
	contract, ok := w.contracts[id]
	w.mu.RUnlock()
	if ok {
		return cloneContract(contract), true
	}
	if w.store != nil {
		stored, err := w.store.GetContract(context.Background(), w.mapping.CompanyID, id)
		if err == nil {
			w.mu.Lock()
			w.contracts[id] = cloneContract(stored)
			w.mu.Unlock()
			return stored, true
		}
	}
	return Contract{}, false
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

	if updated.RecognizedPrices == nil {
		updated.RecognizedPrices = make(map[uuid.UUID]financialtypes.Money, len(updated.Obligations))
	}
	updated.RecognizedPrices[event.ObligationID] = nextPOAmount
	if updated.RecognizedAmount.Equal(updated.TransactionPrice) {
		updated.Status = ContractStatusSatisfied
	}

	w.contracts[contractID] = cloneContract(updated)
	w.recognizedByPO[contractID][event.ObligationID] = nextPOAmount
	w.posted[key] = postedRecognition{hash: hash, contract: cloneContract(updated), entry: pending.entry}
	delete(w.pending, key)

	if w.store != nil {
		_ = w.store.SaveContract(ctx, w.mapping.CompanyID, updated)
	}

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
