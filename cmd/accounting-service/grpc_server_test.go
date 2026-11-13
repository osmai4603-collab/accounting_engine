package main

import (
	"context"
	"net"
	"testing"
	"time"

	"accounting_engine/pkg/api/v1/ledger"
	"accounting_engine/pkg/api/v1/types"
	"accounting_engine/pkg/sdk"
	accountingv1 "accounting_engine/proto/accounting/v1"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	reflectionv1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

func TestGRPCRegistersReflectsAndInvokesProtoServices(t *testing.T) {
	companyUUID := uuid.New()
	entryUUID := uuid.New()
	ledgerService := &grpcTestLedger{entry: ledger.JournalEntry{
		ID:        types.JournalEntryID(entryUUID),
		CompanyID: types.CompanyID(companyUUID),
		Status:    ledger.EntryPosted,
	}}
	server := newGRPCServer(&serviceTestEngine{ledger: ledgerService})
	listener := bufconn.Listen(1 << 20)
	go func() {
		_ = server.Serve(listener)
	}()
	defer server.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	for _, name := range []string{
		"accounting.v1.AccountingService",
		"accounting.v1.ReportingService",
		"accounting.v1.EventService",
	} {
		if _, ok := server.GetServiceInfo()[name]; !ok {
			t.Errorf("registered services missing %q", name)
		}
	}

	reflectionClient := reflectionv1.NewServerReflectionClient(conn)
	reflectionStream, err := reflectionClient.ServerReflectionInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := reflectionStream.Send(&reflectionv1.ServerReflectionRequest{
		MessageRequest: &reflectionv1.ServerReflectionRequest_ListServices{
			ListServices: "",
		},
	}); err != nil {
		t.Fatal(err)
	}
	reflectionResponse, err := reflectionStream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	reflected := make(map[string]bool)
	for _, service := range reflectionResponse.GetListServicesResponse().GetService() {
		reflected[service.GetName()] = true
	}
	for _, name := range []string{
		"accounting.v1.AccountingService",
		"accounting.v1.ReportingService",
		"accounting.v1.EventService",
	} {
		if !reflected[name] {
			t.Errorf("reflection did not advertise %q", name)
		}
	}

	response, err := accountingv1.NewAccountingServiceClient(conn).GetEntry(ctx, &accountingv1.GetEntryRequest{
		CompanyId: companyUUID.String(),
		EntryId:   entryUUID.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := response.GetEntryId(); got != entryUUID.String() {
		t.Errorf("entry_id = %q, want %q", got, entryUUID)
	}
	if got := response.GetStatus(); got != string(ledger.EntryPosted) {
		t.Errorf("status = %q, want %q", got, ledger.EntryPosted)
	}
	if !ledgerService.getEntryCalled {
		t.Fatal("GetEntry RPC did not invoke the SDK ledger service")
	}
}

func TestGeneratedProtobufMessageRoundTrip(t *testing.T) {
	want := &accountingv1.ProcessEventRequest{
		EventId:        "event-123",
		EventType:      "invoice.created",
		CompanyId:      uuid.NewString(),
		EffectiveDate:  "2026-11-13",
		Amount:         "42.50",
		Currency:       "USD",
		IdempotencyKey: "event-123",
	}
	encoded, err := proto.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	got := new(accountingv1.ProcessEventRequest)
	if err := proto.Unmarshal(encoded, got); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(want, got) {
		t.Fatalf("protobuf round trip mismatch:\n got: %v\nwant: %v", got, want)
	}
}

type grpcTestLedger struct {
	entry          ledger.JournalEntry
	getEntryCalled bool
}

func (s *grpcTestLedger) PostEntry(context.Context, ledger.JournalEntry) (ledger.JournalEntry, error) {
	return ledger.JournalEntry{}, nil
}

func (s *grpcTestLedger) GetEntry(_ context.Context, companyID types.CompanyID, entryID types.JournalEntryID) (ledger.JournalEntry, error) {
	s.getEntryCalled = companyID == s.entry.CompanyID && entryID == s.entry.ID
	return s.entry, nil
}

func (s *grpcTestLedger) ReverseEntry(context.Context, types.CompanyID, types.JournalEntryID, string) (ledger.JournalEntry, error) {
	return ledger.JournalEntry{}, nil
}

var _ sdk.LedgerService = (*grpcTestLedger)(nil)
var _ sdk.ReportingService = serviceTestReporting{}
