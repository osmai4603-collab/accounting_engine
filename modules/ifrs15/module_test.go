package ifrs15

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	internalifrs15 "github.com/osmai4603-collab/accounting_engine/internal/ifrs15"
	reportinginternal "github.com/osmai4603-collab/accounting_engine/internal/reporting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/reporting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
)

func TestModuleMetadataAndDependencyErrors(t *testing.T) {
	module := &Module{}

	if err := module.ValidateDependencies(nil); err == nil {
		t.Fatal("ValidateDependencies(nil) returned nil, want dependency error")
	}
	if err := module.Initialize(context.Background(), nil); err == nil {
		t.Fatal("Initialize with nil core store returned nil, want dependency error")
	}
	if err := module.RegisterRules(nil); err != nil {
		t.Fatalf("RegisterRules(nil): %v", err)
	}

	router := events.NewRouter()
	if err := module.RegisterRules(router); err != nil {
		t.Fatalf("RegisterRules(router): %v", err)
	}
	if got := router.GetRules("IFRS15_REVENUE_RECOGNITION"); len(got) != 0 {
		t.Fatalf("registered hard-coded posting rules: %+v", got)
	}

	descriptor := module.Descriptor()
	if err := descriptor.Profile.Validate(); err != nil {
		t.Fatalf("invalid standard profile: %v", err)
	}
	if len(descriptor.Workflows) == 0 {
		t.Fatal("standard module has no declared workflows")
	}
	for _, workflow := range descriptor.Workflows {
		if err := workflow.Validate(*descriptor.Profile); err != nil {
			t.Errorf("invalid workflow %q: %v", workflow.ID, err)
		}
	}
}

func TestModuleDescriptorExclusionsAndScope(t *testing.T) {
	module := &Module{}
	descriptor := module.Descriptor()

	supportedMap := make(map[string]standards.StandardRequirement)
	for _, req := range descriptor.Profile.SupportedRequirements {
		supportedMap[req.ID] = req
	}

	requiredSupported := []string{
		"IFRS15-5STEP-01",
		"IFRS15-ALLOC-01",
		"IFRS15-SCHEDULE-01",
		"IFRS15-POST-01",
		"IFRS15-VARIABLE-CONSIDERATION",
		"IFRS15-FINANCING",
		"IFRS15-CONTRACT-MODIFICATION",
		"IFRS15-DURABLE-WORKFLOW",
		"IFRS15-DISCLOSURE",
	}
	for _, reqID := range requiredSupported {
		if req, ok := supportedMap[reqID]; !ok {
			t.Errorf("missing supported requirement: %s", reqID)
		} else if req.Status != standards.RequirementLimited && req.Status != standards.RequirementPartial {
			t.Errorf("requirement %s status = %s, want limited/partial", reqID, req.Status)
		}
	}

	unimplementedMap := make(map[string]standards.StandardRequirement)
	for _, req := range descriptor.Profile.UnimplementedRequirements {
		unimplementedMap[req.ID] = req
	}
	for _, reqID := range []string{"IFRS15-PRINCIPAL-VS-AGENT", "IFRS15-IP-LICENSING-COMPLEX"} {
		if req, ok := unimplementedMap[reqID]; !ok {
			t.Errorf("missing documented exclusion requirement: %s", reqID)
		} else if req.Status != standards.RequirementNotImplemented {
			t.Errorf("requirement %s status = %s, want not_implemented", reqID, req.Status)
		}
	}

	// Verify workflow IDs
	workflowMap := make(map[string]standards.WorkflowDescriptor)
	for _, wf := range descriptor.Workflows {
		workflowMap[wf.ID] = wf
	}
	expectedWorkflows := []string{
		"revenue-five-step-input-validation",
		"revenue-allocation",
		"revenue-satisfaction-posting",
		"revenue-recognition-schedule",
		"revenue-variable-consideration",
		"revenue-financing-component",
		"revenue-contract-modification",
		"revenue-disclosure",
	}
	for _, wfID := range expectedWorkflows {
		if _, ok := workflowMap[wfID]; !ok {
			t.Errorf("missing workflow descriptor: %s", wfID)
		}
	}
}

func TestModuleDoesNotPostWithoutConfiguredJournalMapping(t *testing.T) {
	ctx := context.Background()
	engine, err := sdk.NewBuilder(
		sdk.WithCoreStorage(memory.New()),
		sdk.WithStandard(&Module{}),
	).Build(ctx)
	if err != nil {
		t.Fatalf("build engine with IFRS 15 module: %v", err)
	}
	defer func() {
		if err := engine.Close(); err != nil {
			t.Errorf("close engine: %v", err)
		}
	}()

	amount, err := types.NewMoneyFromString("250.75", "USD")
	if err != nil {
		t.Fatalf("create event amount: %v", err)
	}
	_, err = engine.Events().ProcessEvent(ctx, events.AccountingEvent{
		EventID: "revenue-001", EventType: "IFRS15_REVENUE_RECOGNITION",
		CompanyID: types.NewCompanyID(), EffectiveDate: time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC),
		Amount: amount,
	})
	if err == nil || !strings.Contains(err.Error(), "no account mapping/rule registered") {
		t.Fatalf("ProcessEvent error = %v, want explicit missing mapping error", err)
	}
}

func TestModuleDisclosureProvider(t *testing.T) {
	ctx := context.Background()
	module := NewModule()

	companyID := types.NewCompanyID()

	// Contract 1: Point in time ($500 allocated, $500 recognized, $500 billed)
	obl1 := internalifrs15.PerformanceObligation{
		ID:                 uuid.New(),
		Description:        "Hardware delivery",
		StandalonePrice:    mustMoney("500.00", "USD"),
		RecognitionPattern: internalifrs15.PointInTime,
		StartDate:          time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
	}
	c1, err := internalifrs15.NewContract(uuid.New(), uuid.New(), mustMoney("500.00", "USD"), []internalifrs15.PerformanceObligation{obl1})
	if err != nil {
		t.Fatal(err)
	}
	c1, _ = c1.ApplyBilling(mustMoney("500.00", "USD"))
	c1, _ = c1.ApplyRecognition(mustMoney("500.00", "USD"))
	c1.RecognizedPrices[obl1.ID] = mustMoney("500.00", "USD")
	c1.Status = internalifrs15.ContractStatusSatisfied
	_ = module.ContractStore().SaveContract(ctx, uuid.UUID(companyID), c1)

	// Contract 2: Over time ($1,200 allocated, $400 recognized, $600 billed) -> $200 contract liability (deferred revenue), $800 remaining
	obl2 := internalifrs15.PerformanceObligation{
		ID:                 uuid.New(),
		Description:        "SaaS Subscription (1 year)",
		StandalonePrice:    mustMoney("1200.00", "USD"),
		RecognitionPattern: internalifrs15.OverTime,
		StartDate:          time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate:            time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
	}
	c2, err := internalifrs15.NewContract(uuid.New(), uuid.New(), mustMoney("1200.00", "USD"), []internalifrs15.PerformanceObligation{obl2})
	if err != nil {
		t.Fatal(err)
	}
	c2, _ = c2.ApplyBilling(mustMoney("600.00", "USD"))
	c2, _ = c2.ApplyRecognition(mustMoney("400.00", "USD"))
	c2.RecognizedPrices[obl2.ID] = mustMoney("400.00", "USD")
	_ = module.ContractStore().SaveContract(ctx, uuid.UUID(companyID), c2)

	// Wire module into P9-05 DisclosureRegistry
	registry, err := reportinginternal.NewDisclosureRegistry([]standards.DisclosureProvider{module})
	if err != nil {
		t.Fatalf("NewDisclosureRegistry failed: %v", err)
	}

	query := reporting.DisclosureQuery{
		CompanyID: companyID,
		Period: reporting.DisclosurePeriod{
			StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			EndDate:   time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
		},
		Currency:          "USD",
		StandardReference: "IFRS 15",
	}

	disclosures, err := registry.GetDisclosures(ctx, query)
	if err != nil {
		t.Fatalf("GetDisclosures failed: %v", err)
	}
	if len(disclosures) != 3 {
		t.Fatalf("expected 3 disclosures, got %d", len(disclosures))
	}

	discMap := make(map[string]reporting.StructuredDisclosure)
	for _, d := range disclosures {
		discMap[d.Code] = d
	}

	// 1. Check IFRS15-REV-DISAGG
	disagg, ok := discMap["IFRS15-REV-DISAGG"]
	if !ok {
		t.Fatal("missing IFRS15-REV-DISAGG disclosure")
	}
	if disagg.Data["point_in_time_revenue"] != "500.00" {
		t.Errorf("point_in_time_revenue = %v, want 500.00", disagg.Data["point_in_time_revenue"])
	}
	if disagg.Data["over_time_revenue"] != "400.00" {
		t.Errorf("over_time_revenue = %v, want 400.00", disagg.Data["over_time_revenue"])
	}
	if disagg.Data["total_revenue"] != "900.00" {
		t.Errorf("total_revenue = %v, want 900.00", disagg.Data["total_revenue"])
	}

	// 2. Check IFRS15-CONTRACT-BALANCES
	balances, ok := discMap["IFRS15-CONTRACT-BALANCES"]
	if !ok {
		t.Fatal("missing IFRS15-CONTRACT-BALANCES disclosure")
	}
	if balances.Data["total_billed"] != "1100.00" {
		t.Errorf("total_billed = %v, want 1100.00", balances.Data["total_billed"])
	}
	if balances.Data["total_recognized"] != "900.00" {
		t.Errorf("total_recognized = %v, want 900.00", balances.Data["total_recognized"])
	}
	if balances.Data["total_contract_liabilities"] != "200.00" {
		t.Errorf("total_contract_liabilities = %v, want 200.00", balances.Data["total_contract_liabilities"])
	}

	// 3. Check IFRS15-REMAINING-OBLIGATIONS
	rem, ok := discMap["IFRS15-REMAINING-OBLIGATIONS"]
	if !ok {
		t.Fatal("missing IFRS15-REMAINING-OBLIGATIONS disclosure")
	}
	if rem.Data["unsatisfied_amount"] != "800.00" {
		t.Errorf("unsatisfied_amount = %v, want 800.00", rem.Data["unsatisfied_amount"])
	}
	count, _ := rem.Data["obligations_count"].(json.Number).Int64()
	if count != 1 {
		t.Errorf("obligations_count = %v, want 1", rem.Data["obligations_count"])
	}
}

func TestModuleDisclosureRejectsMixedCurrenciesWithoutFilter(t *testing.T) {
	ctx := context.Background()
	companyID := uuid.New()
	module := NewModule()

	for _, currency := range []string{"USD", "EUR"} {
		amount := mustMoney("100.00", currency)
		obligation := internalifrs15.PerformanceObligation{
			ID:                 uuid.New(),
			Description:        "Delivered service",
			StandalonePrice:    amount,
			RecognitionPattern: internalifrs15.PointInTime,
			StartDate:          time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		}
		contract, err := internalifrs15.NewContract(
			uuid.New(), uuid.New(), amount, []internalifrs15.PerformanceObligation{obligation},
		)
		if err != nil {
			t.Fatalf("NewContract(%s): %v", currency, err)
		}
		contract, err = contract.ApplyRecognition(amount)
		if err != nil {
			t.Fatalf("ApplyRecognition(%s): %v", currency, err)
		}
		contract.RecognizedPrices[obligation.ID] = amount
		if err := module.ContractStore().SaveContract(ctx, companyID, contract); err != nil {
			t.Fatalf("SaveContract(%s): %v", currency, err)
		}
	}

	query := reporting.DisclosureQuery{CompanyID: types.CompanyID(companyID)}
	if _, err := module.GetDisclosures(ctx, query); err == nil {
		t.Fatal("GetDisclosures without a currency filter succeeded for mixed-currency contracts")
	}

	query.Currency = "usd"
	disclosures, err := module.GetDisclosures(ctx, query)
	if err != nil {
		t.Fatalf("GetDisclosures with a USD filter: %v", err)
	}
	if got := disclosures[0].Currency; got != "USD" {
		t.Errorf("disclosure currency = %q, want USD", got)
	}
	if got := disclosures[0].Data["total_revenue"]; got != "100.00" {
		t.Errorf("total revenue = %v, want 100.00 for the selected currency", got)
	}
}

func TestModuleDisclosureReturnsInvalidContractBalanceError(t *testing.T) {
	module := NewModule(WithContractStore(disclosureContractStore{
		contracts: []internalifrs15.Contract{{ID: uuid.New()}},
	}))
	_, err := module.GetDisclosures(context.Background(), reporting.DisclosureQuery{
		CompanyID: types.NewCompanyID(),
	})
	if err == nil || !strings.Contains(err.Error(), "balances") {
		t.Fatalf("GetDisclosures error = %v, want contract balance validation error", err)
	}
}

type disclosureContractStore struct {
	internalifrs15.ContractStore
	contracts []internalifrs15.Contract
}

func (s disclosureContractStore) ListContracts(context.Context, uuid.UUID) ([]internalifrs15.Contract, error) {
	return s.contracts, nil
}

func mustMoney(amount, currency string) financialtypes.Money {
	m, err := financialtypes.NewMoneyFromString(amount, currency)
	if err != nil {
		panic(err)
	}
	return m
}

type recordingAppService struct {
	publicapplication.Service
	createDraftFunc func(ctx context.Context, cmd publicapplication.CreateDraftCommand) (publicapplication.EntryResult, error)
	postFunc        func(ctx context.Context, cmd publicapplication.PostEntryCommand) (publicapplication.EntryResult, error)
	createdDraft    publicapplication.CreateDraftCommand
	postedCommand   publicapplication.PostEntryCommand
	entryID         types.JournalEntryID
	createCalls     int
	postCalls       int
}

func (r *recordingAppService) CreateDraft(ctx context.Context, cmd publicapplication.CreateDraftCommand) (publicapplication.EntryResult, error) {
	r.createCalls++
	r.createdDraft = cmd
	if r.entryID == (types.JournalEntryID{}) {
		r.entryID = types.NewJournalEntryID()
	}
	if r.createDraftFunc != nil {
		return r.createDraftFunc(ctx, cmd)
	}
	return publicapplication.EntryResult{
		Entry: ledger.JournalEntry{ID: r.entryID, Status: ledger.EntryDraft},
	}, nil
}

func (r *recordingAppService) CreateDraftWithID(ctx context.Context, cmd publicapplication.CreateDraftCommand, id types.JournalEntryID) (publicapplication.EntryResult, error) {
	r.createCalls++
	r.createdDraft = cmd
	r.entryID = id
	if r.createDraftFunc != nil {
		return r.createDraftFunc(ctx, cmd)
	}
	return publicapplication.EntryResult{
		Entry: ledger.JournalEntry{ID: r.entryID, Status: ledger.EntryDraft},
	}, nil
}

func (r *recordingAppService) Post(ctx context.Context, cmd publicapplication.PostEntryCommand) (publicapplication.EntryResult, error) {
	r.postCalls++
	r.postedCommand = cmd
	r.entryID = cmd.EntryID
	if r.postFunc != nil {
		return r.postFunc(ctx, cmd)
	}
	return publicapplication.EntryResult{
		Entry: ledger.JournalEntry{ID: cmd.EntryID, Status: ledger.EntryPosted},
	}, nil
}

func (r *recordingAppService) GetEntry(_ context.Context, query publicapplication.GetEntryQuery) (ledger.JournalEntry, error) {
	if r.entryID != (types.JournalEntryID{}) && query.EntryID == r.entryID {
		status := ledger.EntryDraft
		if r.postedCommand.EntryID == r.entryID {
			status = ledger.EntryPosted
		}
		return ledger.JournalEntry{ID: r.entryID, Status: status}, nil
	}
	return ledger.JournalEntry{}, errors.New("not found")
}

type fakeEntryReader struct{ standards.EntryReader }
type fakeMasterDataReader struct{}

func (fakeMasterDataReader) GetCompany(context.Context, types.CompanyID) (masterdata.Company, error) {
	return masterdata.Company{}, nil
}
func (fakeMasterDataReader) GetAccount(context.Context, types.CompanyID, types.AccountID) (masterdata.Account, error) {
	return masterdata.Account{}, nil
}
func (fakeMasterDataReader) GetAccountByCode(context.Context, types.CompanyID, string) (masterdata.Account, error) {
	return masterdata.Account{}, nil
}
func (fakeMasterDataReader) GetJournalByCode(context.Context, types.CompanyID, string) (masterdata.Journal, error) {
	return masterdata.Journal{}, nil
}

func TestIFRS15PostingWorkflow(t *testing.T) {
	app := &recordingAppService{}
	ports := standards.NewModulePorts(fakeEntryReader{}, nil, fakeMasterDataReader{}, app)
	store := internalifrs15.NewInMemoryContractStore()
	module := NewModule(WithContractStore(store))
	if err := module.Initialize(context.Background(), ports); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	companyID := uuid.New()
	journalID := uuid.New()
	mapping := internalifrs15.RevenueJournalMapping{
		CompanyID:                companyID,
		JournalID:                journalID,
		RevenueAccountID:         uuid.New(),
		ContractAssetAccountID:   uuid.New(),
		ContractLiabilityAccount: uuid.New(),
	}

	workflow, err := module.NewRevenueWorkflow(mapping)
	if err != nil {
		t.Fatalf("NewRevenueWorkflow: %v", err)
	}

	contractID := uuid.New()
	customerID := uuid.New()
	poID := uuid.New()
	price := mustMoney("1000.00", "USD")

	contract, err := workflow.AddContract(internalifrs15.FiveStepAssessment{
		ContractID:                  contractID,
		CustomerID:                  customerID,
		Approved:                    true,
		EnforceableRightsIdentified: true,
		PaymentTermsIdentified:      true,
		CommercialSubstance:         true,
		CollectibilityProbable:      true,
		FixedTransactionPrice:       true,
		TransactionPrice:            price,
		DistinctObligationIDs:       []uuid.UUID{poID},
		Obligations: []internalifrs15.PerformanceObligation{
			{
				ID:                 poID,
				Description:        "Software license",
				StandalonePrice:    price,
				RecognitionPattern: internalifrs15.PointInTime,
				StartDate:          time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC),
			},
		},
	})
	if err != nil {
		t.Fatalf("AddContract: %v", err)
	}

	ctx := publicapplication.WithPrincipal(context.Background(), publicapplication.Principal{
		ActorID:    "rev-officer",
		CompanyIDs: []types.CompanyID{types.CompanyID(companyID)},
	})

	eventID := uuid.New()
	event := internalifrs15.SatisfactionEvent{
		ID:           eventID,
		ObligationID: poID,
		Amount:       mustMoney("400.00", "USD"),
		Date:         time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC),
		Evidence: internalifrs15.ServiceDeliveryEvidence{
			Satisfied:         true,
			MeasureBasis:      "delivery-acceptance",
			EvidenceReference: "DLV-999",
		},
	}

	t.Run("successful revenue recognition posting", func(t *testing.T) {
		updated, _, err := workflow.Recognize(ctx, contract.ID, event)
		if err != nil {
			t.Fatalf("Recognize failed: %v", err)
		}

		if updated.RecognizedAmount.Amount().StringFixed(2) != "400.00" {
			t.Errorf("RecognizedAmount = %s, want 400.00", updated.RecognizedAmount.Amount().StringFixed(2))
		}

		// Verify audit metadata in application draft
		if app.createdDraft.SourceModule != "ifrs15" {
			t.Errorf("SourceModule = %s, want ifrs15", app.createdDraft.SourceModule)
		}
		if !strings.Contains(app.createdDraft.Description, "IFRS-15-REV-REC") {
			t.Errorf("Description = %s, want IFRS-15-REV-REC prefix", app.createdDraft.Description)
		}
		expectedKey := contractID.String() + ":" + eventID.String()
		if app.createdDraft.Context.IdempotencyKey != expectedKey {
			t.Errorf("IdempotencyKey = %s, want %s", app.createdDraft.Context.IdempotencyKey, expectedKey)
		}

		// Verify durable store was updated
		stored, err := store.GetContract(context.Background(), companyID, contractID)
		if err != nil {
			t.Fatalf("GetContract from store: %v", err)
		}
		if stored.RecognizedAmount.Amount().StringFixed(2) != "400.00" {
			t.Errorf("stored RecognizedAmount = %s, want 400.00", stored.RecognizedAmount.Amount().StringFixed(2))
		}
	})

	t.Run("strict idempotency on retry with same event ID", func(t *testing.T) {
		// Calling Recognize again with identical event must return cached result without double posting
		callsBefore := app.createCalls
		updated, _, err := workflow.Recognize(ctx, contract.ID, event)
		if err != nil {
			t.Fatalf("re-Recognize failed: %v", err)
		}
		if updated.RecognizedAmount.Amount().StringFixed(2) != "400.00" {
			t.Errorf("RecognizedAmount on replay = %s, want 400.00", updated.RecognizedAmount.Amount().StringFixed(2))
		}
		// Confirm createDraft was not called again
		if app.createCalls != callsBefore {
			t.Errorf("expected 0 new create calls on idempotent replay, got %d", app.createCalls-callsBefore)
		}
	})

	t.Run("failure injection preserves state uncommitted", func(t *testing.T) {
		failApp := &recordingAppService{
			createDraftFunc: func(ctx context.Context, cmd publicapplication.CreateDraftCommand) (publicapplication.EntryResult, error) {
				return publicapplication.EntryResult{}, errors.New("simulated network partition")
			},
		}
		failPorts := standards.NewModulePorts(fakeEntryReader{}, nil, fakeMasterDataReader{}, failApp)
		failStore := internalifrs15.NewInMemoryContractStore()
		failModule := NewModule(WithContractStore(failStore))
		_ = failModule.Initialize(context.Background(), failPorts)

		failWorkflow, err := failModule.NewRevenueWorkflow(mapping)
		if err != nil {
			t.Fatalf("fail NewRevenueWorkflow: %v", err)
		}

		failContract, err := failWorkflow.AddContract(internalifrs15.FiveStepAssessment{
			ContractID:                  uuid.New(),
			CustomerID:                  customerID,
			Approved:                    true,
			EnforceableRightsIdentified: true,
			PaymentTermsIdentified:      true,
			CommercialSubstance:         true,
			CollectibilityProbable:      true,
			FixedTransactionPrice:       true,
			TransactionPrice:            price,
			DistinctObligationIDs:       []uuid.UUID{poID},
			Obligations: []internalifrs15.PerformanceObligation{
				{
					ID:                 poID,
					Description:        "Software license",
					StandalonePrice:    price,
					RecognitionPattern: internalifrs15.PointInTime,
					StartDate:          time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC),
				},
			},
		})
		if err != nil {
			t.Fatalf("fail AddContract: %v", err)
		}

		failEvent := internalifrs15.SatisfactionEvent{
			ID:           uuid.New(),
			ObligationID: poID,
			Amount:       mustMoney("200.00", "USD"),
			Date:         time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC),
			Evidence: internalifrs15.ServiceDeliveryEvidence{
				Satisfied:         true,
				MeasureBasis:      "delivery",
				EvidenceReference: "DLV-FAIL",
			},
		}

		// Attempt recognition - MUST fail
		_, _, err = failWorkflow.Recognize(ctx, failContract.ID, failEvent)
		if err == nil {
			t.Fatal("expected Recognize to fail due to injected error")
		}

		// Assert contract state in workflow is NOT updated
		current, ok := failWorkflow.Contract(failContract.ID)
		if !ok {
			t.Fatal("contract not found in workflow")
		}
		if !current.RecognizedAmount.Amount().IsZero() {
			t.Errorf("RecognizedAmount = %s, want 0 (rollback on failure)", current.RecognizedAmount.Amount())
		}

		// Assert durable store was NOT updated
		stored, err := failStore.GetContract(context.Background(), companyID, failContract.ID)
		if err != nil {
			t.Fatalf("GetContract failed: %v", err)
		}
		if !stored.RecognizedAmount.Amount().IsZero() {
			t.Errorf("stored RecognizedAmount = %s, want 0 (uncommitted)", stored.RecognizedAmount.Amount())
		}
	})
}
