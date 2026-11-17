package standards_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
	"github.com/shopspring/decimal"
)

type fakeAppService struct {
	createDraftFunc func(ctx context.Context, command publicapplication.CreateDraftCommand) (publicapplication.EntryResult, error)
	postFunc        func(ctx context.Context, command publicapplication.PostEntryCommand) (publicapplication.EntryResult, error)
	createdDraft    publicapplication.CreateDraftCommand
	postedCommand   publicapplication.PostEntryCommand
}

func (f *fakeAppService) CreateDraft(ctx context.Context, command publicapplication.CreateDraftCommand) (publicapplication.EntryResult, error) {
	f.createdDraft = command
	if f.createDraftFunc != nil {
		return f.createDraftFunc(ctx, command)
	}
	return publicapplication.EntryResult{
		Entry: ledger.JournalEntry{
			ID:        types.NewJournalEntryID(),
			CompanyID: command.CompanyID,
			JournalID: command.JournalID,
			Status:    ledger.EntryDraft,
		},
	}, nil
}

func (f *fakeAppService) Validate(ctx context.Context, command publicapplication.ValidateEntryCommand) error {
	return nil
}

func (f *fakeAppService) Post(ctx context.Context, command publicapplication.PostEntryCommand) (publicapplication.EntryResult, error) {
	f.postedCommand = command
	if f.postFunc != nil {
		return f.postFunc(ctx, command)
	}
	return publicapplication.EntryResult{
		Entry: ledger.JournalEntry{
			ID:        command.EntryID,
			CompanyID: command.CompanyID,
			Status:    ledger.EntryPosted,
		},
	}, nil
}

func (f *fakeAppService) Reverse(ctx context.Context, command publicapplication.ReverseEntryCommand) (publicapplication.EntryResult, error) {
	return publicapplication.EntryResult{}, nil
}

func (f *fakeAppService) GetEntry(ctx context.Context, query publicapplication.GetEntryQuery) (ledger.JournalEntry, error) {
	return ledger.JournalEntry{}, nil
}

type fakeMasterDataStore struct {
	accounts map[string]masterdata.Account
}

func (m *fakeMasterDataStore) GetCompany(ctx context.Context, companyID types.CompanyID) (masterdata.Company, error) {
	return masterdata.Company{}, nil
}

func (m *fakeMasterDataStore) GetAccount(ctx context.Context, companyID types.CompanyID, accountID types.AccountID) (masterdata.Account, error) {
	key := fmtAccountKey(companyID, accountID)
	acc, ok := m.accounts[key]
	if !ok {
		return masterdata.Account{}, errors.New("account not found")
	}
	return acc, nil
}

func (m *fakeMasterDataStore) GetAccountByCode(ctx context.Context, companyID types.CompanyID, code string) (masterdata.Account, error) {
	return masterdata.Account{}, nil
}

func (m *fakeMasterDataStore) GetJournalByCode(ctx context.Context, companyID types.CompanyID, code string) (masterdata.Journal, error) {
	return masterdata.Journal{}, nil
}

func fmtAccountKey(companyID types.CompanyID, accountID types.AccountID) string {
	return uuid.UUID(companyID).String() + ":" + uuid.UUID(accountID).String()
}

func sampleContext(companyID types.CompanyID) context.Context {
	principal := publicapplication.Principal{
		ActorID:    "audit-officer",
		CompanyIDs: []types.CompanyID{companyID},
	}
	return publicapplication.WithPrincipal(context.Background(), principal)
}

func sampleBalancedLines(companyID types.CompanyID, acc1, acc2 types.AccountID) []ledger.EntryLine {
	amt, _ := types.NewMoney(decimal.RequireFromString("1000.00"), "USD")
	zero, _ := types.Zero("USD")
	return []ledger.EntryLine{
		{
			ID:                  types.NewJournalEntryLineID(),
			AccountID:           acc1,
			TransactionAmount:   amt,
			TransactionCurrency: "USD",
			ExchangeRate:        decimal.NewFromInt(1),
			FunctionalDebit:     amt,
			FunctionalCredit:    zero,
		},
		{
			ID:                  types.NewJournalEntryLineID(),
			AccountID:           acc2,
			TransactionAmount:   amt,
			TransactionCurrency: "USD",
			ExchangeRate:        decimal.NewFromInt(1),
			FunctionalDebit:     zero,
			FunctionalCredit:    amt,
		},
	}
}

func TestStandardPoster_Validations(t *testing.T) {
	companyID := types.CompanyID(uuid.New())
	journalID := types.JournalID(uuid.New())
	acc1 := types.AccountID(uuid.New())
	acc2 := types.AccountID(uuid.New())

	md := &fakeMasterDataStore{
		accounts: map[string]masterdata.Account{
			fmtAccountKey(companyID, acc1): {ID: acc1, Code: "1000"},
			fmtAccountKey(companyID, acc2): {ID: acc2, Code: "2000"},
		},
	}
	app := &fakeAppService{}
	ports := standards.NewModulePorts(nil, nil, md, app)
	poster := ports.Posting()

	validReq := func() standards.StandardPostingRequest {
		return standards.StandardPostingRequest{
			StandardRef:    "IFRS-15-REV-REC",
			EntityID:       "contract-123",
			IdempotencyKey: "idem-456",
			CompanyID:      companyID,
			JournalID:      journalID,
			EffectiveDate:  time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC),
			Description:    "Revenue recognition",
			Lines:          sampleBalancedLines(companyID, acc1, acc2),
		}
	}

	t.Run("nil app returns error", func(t *testing.T) {
		nilPoster := standards.NewStandardPoster(nil, nil)
		_, err := nilPoster.PostStandardEntry(sampleContext(companyID), validReq())
		if !errors.Is(err, standards.ErrPostingServiceRequired) {
			t.Fatalf("expected ErrPostingServiceRequired, got %v", err)
		}
	})

	t.Run("missing standard ref", func(t *testing.T) {
		req := validReq()
		req.StandardRef = ""
		_, err := poster.PostStandardEntry(sampleContext(companyID), req)
		if !errors.Is(err, standards.ErrStandardRefRequired) {
			t.Fatalf("expected ErrStandardRefRequired, got %v", err)
		}
	})

	t.Run("missing entity id", func(t *testing.T) {
		req := validReq()
		req.EntityID = ""
		_, err := poster.PostStandardEntry(sampleContext(companyID), req)
		if !errors.Is(err, standards.ErrEntityIDRequired) {
			t.Fatalf("expected ErrEntityIDRequired, got %v", err)
		}
	})

	t.Run("missing idempotency key", func(t *testing.T) {
		req := validReq()
		req.IdempotencyKey = ""
		_, err := poster.PostStandardEntry(sampleContext(companyID), req)
		if !errors.Is(err, standards.ErrIdempotencyKeyRequired) {
			t.Fatalf("expected ErrIdempotencyKeyRequired, got %v", err)
		}
	})

	t.Run("missing effective date", func(t *testing.T) {
		req := validReq()
		req.EffectiveDate = time.Time{}
		_, err := poster.PostStandardEntry(sampleContext(companyID), req)
		if !errors.Is(err, standards.ErrInvalidEffectiveDate) {
			t.Fatalf("expected ErrInvalidEffectiveDate, got %v", err)
		}
	})

	t.Run("unbalanced lines", func(t *testing.T) {
		req := validReq()
		amt500, _ := types.NewMoney(decimal.RequireFromString("500.00"), "USD")
		req.Lines[1].FunctionalCredit = amt500
		_, err := poster.PostStandardEntry(sampleContext(companyID), req)
		if !errors.Is(err, standards.ErrUnbalancedLines) {
			t.Fatalf("expected ErrUnbalancedLines, got %v", err)
		}
	})

	t.Run("account not in chart of accounts", func(t *testing.T) {
		req := validReq()
		req.Lines[1].AccountID = types.AccountID(uuid.New()) // fake unknown account
		_, err := poster.PostStandardEntry(sampleContext(companyID), req)
		if !errors.Is(err, standards.ErrAccountNotFound) {
			t.Fatalf("expected ErrAccountNotFound, got %v", err)
		}
	})

	t.Run("missing principal in context", func(t *testing.T) {
		req := validReq()
		_, err := poster.PostStandardEntry(context.Background(), req)
		if !errors.Is(err, standards.ErrPrincipalRequired) {
			t.Fatalf("expected ErrPrincipalRequired, got %v", err)
		}
	})

	t.Run("unauthorized company for principal", func(t *testing.T) {
		req := validReq()
		otherCompany := types.CompanyID(uuid.New())
		ctx := sampleContext(otherCompany)
		_, err := poster.PostStandardEntry(ctx, req)
		if !errors.Is(err, standards.ErrUnauthorizedCompany) {
			t.Fatalf("expected ErrUnauthorizedCompany, got %v", err)
		}
	})
}

func TestStandardPoster_SuccessfulPostingAndAuditTrail(t *testing.T) {
	companyID := types.CompanyID(uuid.New())
	journalID := types.JournalID(uuid.New())
	acc1 := types.AccountID(uuid.New())
	acc2 := types.AccountID(uuid.New())

	md := &fakeMasterDataStore{
		accounts: map[string]masterdata.Account{
			fmtAccountKey(companyID, acc1): {ID: acc1, Code: "1000"},
			fmtAccountKey(companyID, acc2): {ID: acc2, Code: "2000"},
		},
	}
	app := &fakeAppService{}
	ports := standards.NewModulePorts(nil, nil, md, app)
	poster := ports.Posting()

	effDate := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	req := standards.StandardPostingRequest{
		StandardRef:    "IFRS-15-REV-REC",
		EntityID:       "contract-999",
		IdempotencyKey: "idem-rev-999",
		CompanyID:      companyID,
		JournalID:      journalID,
		EffectiveDate:  effDate,
		Description:    "Period 1 Recognition",
		Lines:          sampleBalancedLines(companyID, acc1, acc2),
	}

	result, err := poster.PostStandardEntry(sampleContext(companyID), req)
	if err != nil {
		t.Fatalf("PostStandardEntry failed: %v", err)
	}

	if result.Entry.Status != ledger.EntryPosted {
		t.Fatalf("expected entry status %s, got %s", ledger.EntryPosted, result.Entry.Status)
	}

	// Verify CreateDraftCommand audit fields
	draft := app.createdDraft
	if draft.CompanyID != companyID {
		t.Errorf("expected companyID %s, got %s", companyID, draft.CompanyID)
	}
	if draft.JournalID != journalID {
		t.Errorf("expected journalID %s, got %s", journalID, draft.JournalID)
	}
	if draft.ReferenceID != "contract-999" {
		t.Errorf("expected ReferenceID contract-999, got %s", draft.ReferenceID)
	}
	if draft.SourceModule != "ifrs15" {
		t.Errorf("expected SourceModule ifrs15, got %s", draft.SourceModule)
	}
	if draft.Context.IdempotencyKey != "idem-rev-999" {
		t.Errorf("expected IdempotencyKey idem-rev-999, got %s", draft.Context.IdempotencyKey)
	}
	if draft.Description != "[IFRS-15-REV-REC] Period 1 Recognition" {
		t.Errorf("expected description with standard prefix, got %s", draft.Description)
	}
}

func TestStandardPoster_FailureInjection(t *testing.T) {
	companyID := types.CompanyID(uuid.New())
	journalID := types.JournalID(uuid.New())
	acc1 := types.AccountID(uuid.New())
	acc2 := types.AccountID(uuid.New())

	md := &fakeMasterDataStore{
		accounts: map[string]masterdata.Account{
			fmtAccountKey(companyID, acc1): {ID: acc1, Code: "1000"},
			fmtAccountKey(companyID, acc2): {ID: acc2, Code: "2000"},
		},
	}

	t.Run("CreateDraft failure", func(t *testing.T) {
		app := &fakeAppService{
			createDraftFunc: func(ctx context.Context, command publicapplication.CreateDraftCommand) (publicapplication.EntryResult, error) {
				return publicapplication.EntryResult{}, errors.New("simulated database deadlock")
			},
		}
		poster := standards.NewStandardPoster(app, md)
		req := standards.StandardPostingRequest{
			StandardRef:    "IFRS-9-ECL",
			EntityID:       "inst-1",
			IdempotencyKey: "key-1",
			CompanyID:      companyID,
			JournalID:      journalID,
			EffectiveDate:  time.Now().UTC(),
			Lines:          sampleBalancedLines(companyID, acc1, acc2),
		}
		_, err := poster.PostStandardEntry(sampleContext(companyID), req)
		if err == nil || !errors.Is(err, errors.Unwrap(err)) && err.Error() == "" {
			t.Fatal("expected failure on CreateDraft")
		}
	})

	t.Run("Post failure", func(t *testing.T) {
		app := &fakeAppService{
			postFunc: func(ctx context.Context, command publicapplication.PostEntryCommand) (publicapplication.EntryResult, error) {
				return publicapplication.EntryResult{}, errors.New("simulated period closed failure")
			},
		}
		poster := standards.NewStandardPoster(app, md)
		req := standards.StandardPostingRequest{
			StandardRef:    "IFRS-16-LEASE",
			EntityID:       "lease-1",
			IdempotencyKey: "key-2",
			CompanyID:      companyID,
			JournalID:      journalID,
			EffectiveDate:  time.Now().UTC(),
			Lines:          sampleBalancedLines(companyID, acc1, acc2),
		}
		_, err := poster.PostStandardEntry(sampleContext(companyID), req)
		if err == nil {
			t.Fatal("expected failure on Post")
		}
	})
}
