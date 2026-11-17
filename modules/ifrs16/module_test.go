package ifrs16

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	internalifrs16 "github.com/osmai4603-collab/accounting_engine/internal/ifrs16"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
	"github.com/shopspring/decimal"
)

type fakeApplication struct{ publicapplication.Service }
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

func validPorts() standards.ModulePorts {
	return standards.NewModulePorts(fakeEntryReader{}, nil, fakeMasterDataReader{}, fakeApplication{})
}

func TestModuleDependencies(t *testing.T) {
	module := &Module{}

	if got := module.Dependencies(); len(got) != 0 {
		t.Fatalf("Dependencies() = %v, want no module dependencies", got)
	}
	if err := module.ValidateDependencies(nil); err == nil {
		t.Fatal("ValidateDependencies(nil) returned nil, want module ports error")
	}
	if err := module.Initialize(context.Background(), nil); err == nil {
		t.Fatal("Initialize(nil) returned nil, want module ports error")
	}
	if err := module.ValidateDependencies(validPorts()); err != nil {
		t.Fatalf("ValidateDependencies with complete module ports: %v", err)
	}
	if err := module.Initialize(context.Background(), validPorts()); err != nil {
		t.Fatalf("Initialize with complete module ports: %v", err)
	}
}

func TestRegisterRules(t *testing.T) {
	module := &Module{}
	if err := module.RegisterRules(nil); err != nil {
		t.Fatalf("RegisterRules(nil): %v", err)
	}

	router := events.NewRouter()
	if err := module.RegisterRules(router); err != nil {
		t.Fatalf("RegisterRules(router): %v", err)
	}
	for _, eventType := range []string{"IFRS16_LEASE_PAYMENT", "IFRS9_ECL_PROVISION"} {
		if got := router.GetRules(eventType); len(got) != 0 {
			t.Errorf("unexpected hard-coded posting rules for %s: %+v", eventType, got)
		}
	}

	descriptor := module.Descriptor()
	if err := descriptor.Profile.Validate(); err != nil {
		t.Fatalf("invalid standard profile: %v", err)
	}
	for _, workflow := range descriptor.Workflows {
		if err := workflow.Validate(*descriptor.Profile); err != nil {
			t.Errorf("invalid workflow %q: %v", workflow.ID, err)
		}
		if workflow.ProducesJournalEntries {
			t.Errorf("module unexpectedly claims application posting in workflow %q", workflow.ID)
		}
	}
	postingRequirementSupported := false
	for _, requirement := range descriptor.Profile.SupportedRequirements {
		if requirement.ID == "IFRS16-POSTING-WORKFLOW" &&
			requirement.Status == standards.RequirementLimited {
			postingRequirementSupported = true
		}
	}
	if !postingRequirementSupported {
		t.Fatal("module must claim application posting workflow in SupportedRequirements")
	}
}

type recordingAppService struct {
	publicapplication.Service
	created publicapplication.CreateDraftCommand
	posted  publicapplication.PostEntryCommand
	entryID types.JournalEntryID
}

func (r *recordingAppService) CreateDraft(_ context.Context, cmd publicapplication.CreateDraftCommand) (publicapplication.EntryResult, error) {
	r.created = cmd
	if r.entryID == (types.JournalEntryID{}) {
		r.entryID = types.NewJournalEntryID()
	}
	return publicapplication.EntryResult{
		Entry: ledger.JournalEntry{ID: r.entryID, Status: ledger.EntryDraft},
	}, nil
}

func (r *recordingAppService) CreateDraftWithID(_ context.Context, cmd publicapplication.CreateDraftCommand, id types.JournalEntryID) (publicapplication.EntryResult, error) {
	r.created = cmd
	r.entryID = id
	return publicapplication.EntryResult{
		Entry: ledger.JournalEntry{ID: r.entryID, Status: ledger.EntryDraft},
	}, nil
}

func (r *recordingAppService) Post(_ context.Context, cmd publicapplication.PostEntryCommand) (publicapplication.EntryResult, error) {
	r.posted = cmd
	r.entryID = cmd.EntryID
	return publicapplication.EntryResult{
		Entry: ledger.JournalEntry{ID: cmd.EntryID, Status: ledger.EntryPosted},
	}, nil
}

func (r *recordingAppService) GetEntry(_ context.Context, query publicapplication.GetEntryQuery) (ledger.JournalEntry, error) {
	if r.entryID != (types.JournalEntryID{}) && query.EntryID == r.entryID {
		status := ledger.EntryDraft
		if r.posted.EntryID == r.entryID {
			status = ledger.EntryPosted
		}
		return ledger.JournalEntry{ID: r.entryID, Status: status}, nil
	}
	return ledger.JournalEntry{}, errors.New("not found")
}

func TestIFRS16PostingWorkflow(t *testing.T) {
	app := &recordingAppService{}
	ports := standards.NewModulePorts(fakeEntryReader{}, nil, fakeMasterDataReader{}, app)
	module := &Module{}
	if err := module.Initialize(context.Background(), ports); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	companyID := uuid.New()
	journalID := uuid.New()
	ctx := publicapplication.WithPrincipal(context.Background(), publicapplication.Principal{
		ActorID:    "lease-officer",
		CompanyIDs: []types.CompanyID{types.CompanyID(companyID)},
	})

	accounts := internalifrs16.LeaseAccounts{
		ROUAsset:                uuid.New(),
		LeaseLiability:          uuid.New(),
		Cash:                    uuid.New(),
		PrepaidLease:            uuid.New(),
		InterestExpense:         uuid.New(),
		DepreciationExpense:     uuid.New(),
		AccumulatedDepreciation: uuid.New(),
	}

	t.Run("post commencement draft and commit through application", func(t *testing.T) {
		commencement := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		contract, err := internalifrs16.NewLeaseContract(
			commencement,
			decimal.RequireFromString("0.05"),
			[]internalifrs16.LeasePayment{
				{DueDate: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC), Amount: sampleLeaseMoney(t, "1000")},
			},
			sampleLeaseMoney(t, "0"), sampleLeaseMoney(t, "0"), sampleLeaseMoney(t, "0"),
			12,
		)
		if err != nil {
			t.Fatalf("NewLeaseContract: %v", err)
		}
		contract.Reference = "LEASE-2026-001"

		result, err := module.PostCommencement(ctx, contract, accounts, companyID, journalID, "lease-officer", "idem-commence-1")
		if err != nil {
			t.Fatalf("PostCommencement: %v", err)
		}
		if result.Entry.Status != ledger.EntryPosted {
			t.Errorf("status = %s, want %s", result.Entry.Status, ledger.EntryPosted)
		}
		if app.created.ReferenceID != "LEASE-2026-001" {
			t.Errorf("ReferenceID = %s, want LEASE-2026-001", app.created.ReferenceID)
		}
		if app.created.Context.IdempotencyKey != "idem-commence-1" {
			t.Errorf("IdempotencyKey = %s, want idem-commence-1", app.created.Context.IdempotencyKey)
		}
		if app.created.SourceModule != "ifrs16" {
			t.Errorf("SourceModule = %s, want ifrs16", app.created.SourceModule)
		}
	})
}

func sampleLeaseMoney(t *testing.T, amount string) financialtypes.Money {
	t.Helper()
	m, err := financialtypes.NewMoney(decimal.RequireFromString(amount), "USD")
	if err != nil {
		t.Fatal(err)
	}
	return m
}
