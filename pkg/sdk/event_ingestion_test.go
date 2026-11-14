package sdk

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	publicmasterdata "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
)

func TestPublicEventFingerprintCoversAccountingInputs(t *testing.T) {
	amount, err := types.NewMoneyFromString("125.50", "USD")
	if err != nil {
		t.Fatal(err)
	}
	event := events.AccountingEvent{
		EventID: "event-1", EventType: "sale", CompanyID: types.NewCompanyID(),
		Amount: amount, Dimensions: map[string]string{"region": "north"},
		Payload: map[string]any{"invoice": "INV-1"},
	}
	rule := events.PostingRule{
		EventType: "sale", DebitAccountCode: "1000", CreditAccountCode: "4000",
		DescriptionTemplate: "Sale {{.EventID}}",
	}
	fingerprint, err := publicEventFingerprint(event, rule)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := publicEventFingerprint(event, rule)
	if err != nil {
		t.Fatal(err)
	}
	if repeated != fingerprint {
		t.Fatal("same event produced different fingerprints")
	}

	cases := map[string]func(*events.AccountingEvent, *events.PostingRule){
		"event ID": func(event *events.AccountingEvent, _ *events.PostingRule) {
			event.EventID = "event-2"
		},
		"payload": func(event *events.AccountingEvent, _ *events.PostingRule) {
			event.Payload["invoice"] = "INV-2"
		},
		"dimensions": func(event *events.AccountingEvent, _ *events.PostingRule) {
			event.Dimensions["region"] = "south"
		},
		"posting rule": func(_ *events.AccountingEvent, rule *events.PostingRule) {
			rule.CreditAccountCode = "4100"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			mutatedEvent := event
			mutatedEvent.Dimensions = map[string]string{"region": "north"}
			mutatedEvent.Payload = map[string]any{"invoice": "INV-1"}
			mutatedRule := rule
			mutate(&mutatedEvent, &mutatedRule)
			got, err := publicEventFingerprint(mutatedEvent, mutatedRule)
			if err != nil {
				t.Fatal(err)
			}
			if got == fingerprint {
				t.Fatalf("fingerprint ignored changed %s", name)
			}
		})
	}
}

func TestPublicEventFingerprintRejectsUnserializablePayload(t *testing.T) {
	event := events.AccountingEvent{Payload: map[string]any{"invalid": make(chan int)}}
	if _, err := publicEventFingerprint(event, events.PostingRule{}); err == nil {
		t.Fatal("expected an error for an unserializable event payload")
	}
}

func TestEventDescriptionTemplateUsesPayload(t *testing.T) {
	event := events.AccountingEvent{
		EventID: "evt-1", EventType: "sale", Payload: map[string]any{"invoice": "INV-1"},
	}
	got, err := renderEventDescription("Invoice {{.Payload.invoice}}", event)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Invoice INV-1" {
		t.Fatalf("description = %q, want %q", got, "Invoice INV-1")
	}
}

func TestConstructEventEntryRejectsCurrencyWithoutRateSource(t *testing.T) {
	driver := memory.New()
	masterData := driver.MasterDataStore()
	companyID := types.NewCompanyID()
	if err := masterData.CreateCompany(context.Background(), publicmasterdata.Company{
		ID: companyID, Code: "EVENT-TEST", LegalName: "Event Test",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	service := &defaultEventService{
		publicMasterData: masterData,
	}
	amount, err := types.NewMoneyFromString("100", "EUR")
	if err != nil {
		t.Fatal(err)
	}
	event := events.AccountingEvent{EventType: "sale", CompanyID: companyID, Amount: amount}
	rule := events.PostingRule{DebitAccountCode: "1000", CreditAccountCode: "4000"}
	if _, err := service.constructEventEntry(context.Background(), rule, event); !errors.Is(err, ErrExchangeRateUnavailable) {
		t.Fatalf("constructEventEntry error = %v, want %v", err, ErrExchangeRateUnavailable)
	}
}

type eventUseCaseRecorder struct {
	create publicapplication.CreateDraftCommand
	post   publicapplication.PostEntryCommand
}

func (s *eventUseCaseRecorder) CreateDraft(_ context.Context, command publicapplication.CreateDraftCommand) (publicapplication.EntryResult, error) {
	s.create = command
	return publicapplication.EntryResult{Entry: ledger.JournalEntry{
		ID: types.NewJournalEntryID(), CompanyID: command.CompanyID, Status: ledger.EntryDraft,
	}}, nil
}

func (s *eventUseCaseRecorder) Validate(context.Context, publicapplication.ValidateEntryCommand) error {
	return nil
}

func (s *eventUseCaseRecorder) Post(_ context.Context, command publicapplication.PostEntryCommand) (publicapplication.EntryResult, error) {
	s.post = command
	return publicapplication.EntryResult{Entry: ledger.JournalEntry{
		ID: command.EntryID, CompanyID: command.CompanyID, Status: ledger.EntryPosted,
	}}, nil
}

func (s *eventUseCaseRecorder) Reverse(context.Context, publicapplication.ReverseEntryCommand) (publicapplication.EntryResult, error) {
	return publicapplication.EntryResult{}, nil
}

func (s *eventUseCaseRecorder) GetEntry(context.Context, publicapplication.GetEntryQuery) (ledger.JournalEntry, error) {
	return ledger.JournalEntry{}, nil
}

func TestProcessEventUsesApplicationCommandsAndTrustedContext(t *testing.T) {
	companyID := types.NewCompanyID()
	now := time.Date(2026, time.November, 14, 0, 0, 0, 0, time.UTC)
	recorder := &eventUseCaseRecorder{}
	service := &defaultEventService{useCases: recorder}
	principal := publicapplication.Principal{ActorID: "host-actor", CompanyIDs: []types.CompanyID{companyID}}
	ctx := publicapplication.WithPrincipal(context.Background(), principal)
	ctx = publicapplication.WithRequestMetadata(ctx, publicapplication.RequestMetadata{
		CorrelationID: "corr-http-grpc", IdempotencyKey: "event-key", SourceSystem: "accounting-service",
	})
	draft := &ledger.JournalEntry{
		CompanyID: companyID, JournalID: types.NewJournalID(), EffectiveDate: now, PostingDate: now,
		SourceModule: "adapter", Lines: []ledger.EntryLine{{}, {}},
	}

	result, err := service.ProcessEvent(ctx, events.AccountingEvent{
		EventID: "event-42", EventType: "invoice.created", CompanyID: companyID,
		JournalDraft: draft,
	})
	if err != nil {
		t.Fatalf("process event: %v", err)
	}
	if result.Status != ledger.EntryPosted {
		t.Fatalf("result status = %q, want posted", result.Status)
	}
	if recorder.create.Context.Principal.ActorID != principal.ActorID ||
		recorder.create.Context.CorrelationID != "corr-http-grpc" ||
		recorder.create.Context.IdempotencyKey != "event-key" ||
		recorder.create.Context.SourceSystem != "accounting-service" {
		t.Fatalf("create context did not preserve verified request metadata: %#v", recorder.create.Context)
	}
	if recorder.post.Context.Principal.ActorID != principal.ActorID ||
		recorder.post.EntryID != result.ID || recorder.post.CompanyID != companyID {
		t.Fatalf("post command did not use the same application context and created entry: %#v", recorder.post)
	}
	if recorder.create.ReferenceID != "event-42" || recorder.create.SourceModule != "adapter" {
		t.Fatalf("draft command lost event references: %#v", recorder.create)
	}
}

func TestEventCommandContextDoesNotInventVerifiedPrincipalWhenRequired(t *testing.T) {
	companyID := types.NewCompanyID()
	commandContext := eventCommandContext(context.Background(), events.AccountingEvent{
		EventID: "event-1", CompanyID: companyID,
	}, true)
	if commandContext.Principal.ActorID != "" || len(commandContext.Principal.CompanyIDs) != 0 {
		t.Fatalf("unverified principal was synthesized: %#v", commandContext.Principal)
	}
}

func TestLedgerPostRejectsIncompleteEntryBeforeUseCase(t *testing.T) {
	service := &defaultLedgerService{useCases: &eventUseCaseRecorder{}}
	_, err := service.PostEntry(context.Background(), ledger.JournalEntry{
		CompanyID: types.NewCompanyID(),
	})
	if !errors.Is(err, publicapplication.ErrValidation) {
		t.Fatalf("PostEntry error = %v, want validation error", err)
	}
}

func TestLedgerPostUsesVerifiedPrincipalAndRequestMetadata(t *testing.T) {
	companyID := types.NewCompanyID()
	recorder := &eventUseCaseRecorder{}
	service := &defaultLedgerService{useCases: recorder}
	ctx := publicapplication.WithPrincipal(context.Background(), publicapplication.Principal{
		ActorID: "host-actor", CompanyIDs: []types.CompanyID{companyID},
	})
	ctx = publicapplication.WithRequestMetadata(ctx, publicapplication.RequestMetadata{
		CorrelationID: "corr-post", IdempotencyKey: "post-key", SourceSystem: "http",
	})
	entry := ledger.JournalEntry{
		CompanyID: companyID, JournalID: types.NewJournalID(), CreatedBy: "untrusted-payload-actor",
		EffectiveDate: time.Date(2026, time.November, 14, 0, 0, 0, 0, time.UTC),
		Lines:         []ledger.EntryLine{{}, {}},
	}
	if _, err := service.PostEntry(ctx, entry); err != nil {
		t.Fatalf("PostEntry: %v", err)
	}
	if recorder.create.Context.Principal.ActorID != "host-actor" ||
		recorder.create.Context.CorrelationID != "corr-post" ||
		recorder.create.Context.IdempotencyKey != "post-key" ||
		recorder.create.Context.SourceSystem != "http" {
		t.Fatalf("create command context = %#v", recorder.create.Context)
	}
}
