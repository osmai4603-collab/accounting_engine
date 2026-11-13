package sdk

import (
	"context"
	"errors"
	"testing"

	"accounting_engine/internal/masterdata"
	"accounting_engine/pkg/api/v1/events"
	"accounting_engine/pkg/api/v1/types"
	"github.com/google/uuid"
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
	repository := masterdata.NewMockRepository()
	companyID := types.NewCompanyID()
	if err := repository.CreateCompany(masterdata.Company{
		ID: uuid.UUID(companyID), Code: "EVENT-TEST", LegalName: "Event Test",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	service := &defaultEventService{
		masterService: masterdata.NewService(repository),
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
