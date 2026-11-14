package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	accountingv1 "github.com/osmai4603-collab/accounting_engine/proto/accounting/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	reflectionv1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
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

func TestGRPCEventHandlerPassesTransportMetadataToIngestion(t *testing.T) {
	captured := &capturingEventService{}
	server := &GRPCServer{engine: &serviceTestEngine{events: captured}}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"x-correlation-id", "grpc-correlation",
		"x-source-system", "billing",
		"idempotency-key", "metadata-key",
	))

	response, err := server.ProcessEvent(ctx, &accountingv1.ProcessEventRequest{
		EventId: "event-1", EventType: "sale", CompanyId: uuid.NewString(),
		EffectiveDate: "2026-11-14", Amount: "10.00", Currency: "USD",
		IdempotencyKey: "request-key",
	})
	if err != nil {
		t.Fatalf("ProcessEvent: %v", err)
	}
	if response.GetStatus() != string(ledger.EntryPosted) {
		t.Fatalf("status = %q, want posted", response.GetStatus())
	}
	requestMetadata, ok := publicapplication.RequestMetadataFromContext(captured.ctx)
	if !ok {
		t.Fatal("event handler did not propagate request metadata")
	}
	if requestMetadata.CorrelationID != "grpc-correlation" ||
		requestMetadata.IdempotencyKey != "request-key" ||
		requestMetadata.SourceSystem != "billing" {
		t.Fatalf("request metadata = %#v", requestMetadata)
	}
	if captured.event.EventID != "event-1" || captured.event.EventType != "sale" {
		t.Fatalf("event handler changed event payload: %#v", captured.event)
	}
}

func TestGRPCPostEntryRejectsIncompleteContract(t *testing.T) {
	ledgerService := &grpcTestLedger{}
	server := &GRPCServer{engine: &serviceTestEngine{ledger: ledgerService}}
	_, err := server.PostEntry(context.Background(), &accountingv1.PostEntryRequest{
		CompanyId: uuid.NewString(), Description: "incomplete entry",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("PostEntry status = %s, want InvalidArgument", status.Code(err))
	}
	if ledgerService.postCalled {
		t.Fatal("incomplete PostEntry request reached the ledger application")
	}
}

func TestGRPCEventRequiresExplicitDateAndCurrency(t *testing.T) {
	server := &GRPCServer{engine: &serviceTestEngine{}}
	base := &accountingv1.ProcessEventRequest{
		EventId: "event-1", EventType: "sale", CompanyId: uuid.NewString(),
		EffectiveDate: "2026-11-14", Amount: "10.00", Currency: "USD",
	}
	for _, test := range []struct {
		name   string
		mutate func(*accountingv1.ProcessEventRequest)
	}{
		{name: "date", mutate: func(request *accountingv1.ProcessEventRequest) { request.EffectiveDate = "" }},
		{name: "currency", mutate: func(request *accountingv1.ProcessEventRequest) { request.Currency = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := *base
			test.mutate(&request)
			if _, err := server.ProcessEvent(context.Background(), &request); status.Code(err) != codes.InvalidArgument {
				t.Fatalf("ProcessEvent error = %v, want InvalidArgument", err)
			}
		})
	}
}

func TestGRPCMapsApplicationErrorCategories(t *testing.T) {
	err := publicapplication.NewError(publicapplication.ErrForbidden, "company access denied", errors.New("private cause"))
	mapped := grpcApplicationError(err)
	if status.Code(mapped) != codes.PermissionDenied || status.Convert(mapped).Message() != "company access denied" {
		t.Fatalf("mapped error = %v, want PermissionDenied with public message", mapped)
	}
}

func TestHTTPAndGRPCIngestionNormalizeTheSameCommand(t *testing.T) {
	companyID := types.NewCompanyID()
	effectiveDate := time.Date(2026, time.November, 14, 0, 0, 0, 0, time.UTC)
	amount, err := types.NewMoneyFromString("42.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	httpCapture := &capturingEventService{}
	event := events.AccountingEvent{
		EventID: "event-42", EventType: "invoice.created", CompanyID: companyID,
		EffectiveDate: effectiveDate, Amount: amount, IdempotencyKey: "event-key",
	}
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	httpRequest := httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader(string(body)))
	httpRequest.Header.Set("X-Correlation-ID", "corr-42")
	httpRequest.Header.Set("Idempotency-Key", "event-key")
	httpResponse := httptest.NewRecorder()
	newHTTPServer(Config{}, &serviceTestEngine{events: httpCapture}).Handler.ServeHTTP(httpResponse, httpRequest)
	if httpResponse.Code != http.StatusCreated {
		t.Fatalf("HTTP status = %d, want %d: %s", httpResponse.Code, http.StatusCreated, httpResponse.Body)
	}

	grpcCapture := &capturingEventService{}
	grpcCtx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"x-correlation-id", "corr-42", "idempotency-key", "event-key",
	))
	grpcServer := &GRPCServer{engine: &serviceTestEngine{events: grpcCapture}}
	_, err = grpcServer.ProcessEvent(grpcCtx, &accountingv1.ProcessEventRequest{
		EventId: event.EventID, EventType: event.EventType, CompanyId: uuid.UUID(companyID).String(),
		EffectiveDate: "2026-11-14", Amount: "42.00", Currency: "USD",
		IdempotencyKey: event.IdempotencyKey,
	})
	if err != nil {
		t.Fatalf("gRPC ProcessEvent: %v", err)
	}

	if httpCapture.event.EventID != grpcCapture.event.EventID ||
		httpCapture.event.EventType != grpcCapture.event.EventType ||
		httpCapture.event.CompanyID != grpcCapture.event.CompanyID ||
		!httpCapture.event.EffectiveDate.Equal(grpcCapture.event.EffectiveDate) ||
		!httpCapture.event.Amount.Equal(grpcCapture.event.Amount) ||
		httpCapture.event.IdempotencyKey != grpcCapture.event.IdempotencyKey {
		t.Fatalf("HTTP and gRPC events differ:\nHTTP: %#v\n gRPC: %#v", httpCapture.event, grpcCapture.event)
	}
	httpMetadata, _ := publicapplication.RequestMetadataFromContext(httpCapture.ctx)
	grpcMetadata, _ := publicapplication.RequestMetadataFromContext(grpcCapture.ctx)
	if httpMetadata != grpcMetadata {
		t.Fatalf("HTTP and gRPC request contexts differ: HTTP %#v, gRPC %#v", httpMetadata, grpcMetadata)
	}
}

type grpcTestLedger struct {
	entry          ledger.JournalEntry
	getEntryCalled bool
	postCalled     bool
}

func (s *grpcTestLedger) PostEntry(context.Context, ledger.JournalEntry) (ledger.JournalEntry, error) {
	s.postCalled = true
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
