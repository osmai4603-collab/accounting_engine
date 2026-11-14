package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/serviceauth"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	accountingv1 "github.com/osmai4603-collab/accounting_engine/proto/accounting/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	reflectionv1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func gatewayTestConfig() Config {
	return Config{
		AuthMode:      serviceauth.ModeGatewayHMAC,
		AuthSecret:    testAuthSecret,
		AuthClockSkew: 30 * time.Second,
	}
}

func gatewayClaims(actor string, companies []types.CompanyID, timestamp time.Time) serviceauth.GatewayClaims {
	return serviceauth.GatewayClaims{ActorID: actor, CompanyIDs: companies, Timestamp: timestamp}
}

func gatewayHTTPHeaders(actor string, companies []types.CompanyID, timestamp time.Time) map[string]string {
	claims := gatewayClaims(actor, companies, timestamp)
	values := make([]string, 0, len(companies))
	for _, companyID := range companies {
		values = append(values, uuid.UUID(companyID).String())
	}
	return map[string]string{
		serviceauth.HeaderActorID:    actor,
		serviceauth.HeaderCompanyIDs: strings.Join(values, ","),
		serviceauth.HeaderTimestamp:  strconv.FormatInt(timestamp.Unix(), 10),
		serviceauth.HeaderSignature:  serviceauth.SignGatewayHMAC(testAuthSecret, claims),
	}
}

func signedHTTPRequest(t *testing.T, method, path string, body []byte, headers map[string]string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(string(body)))
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	return request
}

type boundaryTestLedger struct {
	postCtx   context.Context
	getCtx    context.Context
	postCalls int
	getCalls  int
	entry     ledger.JournalEntry
}

func (l *boundaryTestLedger) PostEntry(ctx context.Context, entry ledger.JournalEntry) (ledger.JournalEntry, error) {
	l.postCalls++
	l.postCtx = ctx
	return entry, nil
}

func (l *boundaryTestLedger) GetEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID) (ledger.JournalEntry, error) {
	l.getCalls++
	l.getCtx = ctx
	return ledger.JournalEntry{ID: entryID, CompanyID: companyID}, nil
}

func (*boundaryTestLedger) ReverseEntry(context.Context, types.CompanyID, types.JournalEntryID, string) (ledger.JournalEntry, error) {
	return ledger.JournalEntry{}, nil
}

func TestHTTPBoundaryRejectsMissingCredentials(t *testing.T) {
	ledgerService := &boundaryTestLedger{}
	companyID := types.NewCompanyID()
	handler := newHTTPServer(gatewayTestConfig(), &serviceTestEngine{ledger: ledgerService}).Handler
	body, err := json.Marshal(ledger.JournalEntry{CompanyID: companyID})
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/entries", strings.NewReader(string(body))))

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusUnauthorized, response.Body)
	}
	if ledgerService.postCalls != 0 {
		t.Fatal("missing credentials reached the engine")
	}
	if challenge := response.Header().Get("WWW-Authenticate"); challenge != "HMAC-SHA256" {
		t.Fatalf("WWW-Authenticate = %q, want HMAC-SHA256", challenge)
	}
	if strings.Contains(response.Body.String(), testAuthSecret) {
		t.Fatal("401 body leaked the gateway secret")
	}
}

func TestHTTPBoundaryInjectsVerifiedPrincipal(t *testing.T) {
	ledgerService := &boundaryTestLedger{}
	companyID := types.NewCompanyID()
	actorID := "gateway-user-1"
	handler := newHTTPServer(gatewayTestConfig(), &serviceTestEngine{ledger: ledgerService}).Handler
	body, err := json.Marshal(ledger.JournalEntry{CompanyID: companyID})
	if err != nil {
		t.Fatal(err)
	}

	headers := gatewayHTTPHeaders(actorID, []types.CompanyID{companyID}, time.Now().UTC())
	headers["X-Correlation-ID"] = "corr-1"
	headers["X-Request-ID"] = "request-ignored"
	headers["Idempotency-Key"] = "idem-1"
	headers["X-Source-System"] = "spoofed-client-source"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, signedHTTPRequest(t, http.MethodPost, "/api/v1/entries", body, headers))

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusCreated, response.Body)
	}
	if ledgerService.postCalls != 1 {
		t.Fatalf("engine calls = %d, want 1", ledgerService.postCalls)
	}
	principal, ok := publicapplication.PrincipalFromContext(ledgerService.postCtx)
	if !ok {
		t.Fatal("verified principal was not injected")
	}
	if principal.ActorID != actorID || !principalHasCompany(principal, companyID) {
		t.Fatalf("principal = %#v", principal)
	}
	requestMetadata, ok := publicapplication.RequestMetadataFromContext(ledgerService.postCtx)
	if !ok {
		t.Fatal("request metadata was not injected")
	}
	if requestMetadata.CorrelationID != "corr-1" || requestMetadata.IdempotencyKey != "idem-1" {
		t.Fatalf("request metadata = %#v", requestMetadata)
	}
	if requestMetadata.SourceSystem != trustedServiceSource {
		t.Fatalf("SourceSystem = %q, want trusted %q", requestMetadata.SourceSystem, trustedServiceSource)
	}
}

func TestHTTPBoundaryUsesRequestIDWhenCorrelationIDIsAbsent(t *testing.T) {
	ledgerService := &boundaryTestLedger{}
	companyID := types.NewCompanyID()
	handler := newHTTPServer(gatewayTestConfig(), &serviceTestEngine{ledger: ledgerService}).Handler
	body, _ := json.Marshal(ledger.JournalEntry{CompanyID: companyID})

	headers := gatewayHTTPHeaders("gateway-user-1", []types.CompanyID{companyID}, time.Now().UTC())
	headers["X-Request-ID"] = "request-77"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, signedHTTPRequest(t, http.MethodPost, "/api/v1/entries", body, headers))
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusCreated)
	}
	requestMetadata, _ := publicapplication.RequestMetadataFromContext(ledgerService.postCtx)
	if requestMetadata.CorrelationID != "request-77" {
		t.Fatalf("CorrelationID = %q, want request-77", requestMetadata.CorrelationID)
	}
}

func TestHTTPBoundaryRejectsCompanyImpersonation(t *testing.T) {
	grantedCompany := types.NewCompanyID()
	foreignCompany := types.NewCompanyID()
	ledgerService := &boundaryTestLedger{}
	handler := newHTTPServer(gatewayTestConfig(), &serviceTestEngine{ledger: ledgerService}).Handler
	body, _ := json.Marshal(ledger.JournalEntry{CompanyID: foreignCompany})
	headers := gatewayHTTPHeaders("gateway-user-1", []types.CompanyID{grantedCompany}, time.Now().UTC())

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, signedHTTPRequest(t, http.MethodPost, "/api/v1/entries", body, headers))

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusForbidden, response.Body)
	}
	if ledgerService.postCalls != 0 {
		t.Fatal("foreign company request reached the engine")
	}
}

func TestHTTPBoundaryRejectsTrialBalanceImpersonation(t *testing.T) {
	grantedCompany := types.NewCompanyID()
	foreignCompany := types.NewCompanyID()
	handler := newHTTPServer(gatewayTestConfig(), &serviceTestEngine{ledger: &boundaryTestLedger{}}).Handler
	headers := gatewayHTTPHeaders("gateway-user-1", []types.CompanyID{grantedCompany}, time.Now().UTC())

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, signedHTTPRequest(t, http.MethodGet,
		"/api/v1/trial-balance?company_id="+uuid.UUID(foreignCompany).String(), nil, headers))
	if response.Code != http.StatusForbidden {
		t.Fatalf("foreign trial balance status = %d, want %d", response.Code, http.StatusForbidden)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, signedHTTPRequest(t, http.MethodGet,
		"/api/v1/trial-balance?company_id="+uuid.UUID(grantedCompany).String(), nil, headers))
	if response.Code != http.StatusOK {
		t.Fatalf("granted trial balance status = %d, want %d; body: %s", response.Code, http.StatusOK, response.Body)
	}
}

func TestHTTPBoundaryRejectsInvalidAndExpiredCredentials(t *testing.T) {
	ledgerService := &boundaryTestLedger{}
	companyID := types.NewCompanyID()
	handler := newHTTPServer(gatewayTestConfig(), &serviceTestEngine{ledger: ledgerService}).Handler
	body, _ := json.Marshal(ledger.JournalEntry{CompanyID: companyID})

	tamperedHeaders := gatewayHTTPHeaders("gateway-user-1", []types.CompanyID{companyID}, time.Now().UTC())
	tamperedHeaders[serviceauth.HeaderActorID] = "attacker"

	expiredHeaders := gatewayHTTPHeaders("gateway-user-1", []types.CompanyID{companyID}, time.Now().UTC().Add(-10*time.Minute))

	for name, headers := range map[string]map[string]string{
		"tampered signature": tamperedHeaders,
		"expired timestamp":  expiredHeaders,
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, signedHTTPRequest(t, http.MethodPost, "/api/v1/entries", body, headers))
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusUnauthorized, response.Body)
			}
		})
	}
	if ledgerService.postCalls != 0 {
		t.Fatal("invalid credentials reached the engine")
	}
}

func TestHTTPBoundaryInjectsPrincipalForEvents(t *testing.T) {
	captured := &capturingEventService{}
	companyID := types.NewCompanyID()
	handler := newHTTPServer(gatewayTestConfig(), &serviceTestEngine{events: captured}).Handler
	body, err := json.Marshal(events.AccountingEvent{
		EventID: "event-1", EventType: "sale", CompanyID: companyID,
		EffectiveDate: time.Date(2026, time.November, 14, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	headers := gatewayHTTPHeaders("gateway-user-1", []types.CompanyID{companyID}, time.Now().UTC())

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, signedHTTPRequest(t, http.MethodPost, "/api/v1/events", body, headers))
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusCreated, response.Body)
	}
	principal, ok := publicapplication.PrincipalFromContext(captured.ctx)
	if !ok || principal.ActorID != "gateway-user-1" {
		t.Fatalf("principal = %#v, present = %v", principal, ok)
	}
}

func TestHTTPBoundaryLeavesHealthReadinessAndMetricsOpen(t *testing.T) {
	server := newHTTPServer(gatewayTestConfig(), &serviceTestEngine{ledger: &boundaryTestLedger{}}, func(context.Context) error { return nil })
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want %d", path, response.Code, http.StatusOK)
		}
	}
}

func TestHTTPBoundaryDisabledPreservesEmbeddedBehaviour(t *testing.T) {
	captured := &capturingEventService{}
	companyID := types.NewCompanyID()
	handler := newHTTPServer(Config{}, &serviceTestEngine{events: captured}).Handler
	body, _ := json.Marshal(events.AccountingEvent{
		EventID: "event-1", EventType: "sale", CompanyID: companyID,
		EffectiveDate: time.Date(2026, time.November, 14, 0, 0, 0, 0, time.UTC),
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader(string(body)))
	request.Header.Set("X-Source-System", "billing")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusCreated, response.Body)
	}
	if _, ok := publicapplication.PrincipalFromContext(captured.ctx); ok {
		t.Fatal("disabled boundary injected a principal")
	}
	requestMetadata, _ := publicapplication.RequestMetadataFromContext(captured.ctx)
	if requestMetadata.SourceSystem != "billing" {
		t.Fatalf("SourceSystem = %q, want billing", requestMetadata.SourceSystem)
	}
}

func TestGRPCBoundaryRejectsMissingMetadata(t *testing.T) {
	ledgerService := &boundaryTestLedger{}
	conn := startBoundaryGRPCServer(t, &serviceTestEngine{ledger: ledgerService}, true)
	companyID := types.NewCompanyID()

	_, err := accountingv1.NewAccountingServiceClient(conn).GetEntry(context.Background(), &accountingv1.GetEntryRequest{
		CompanyId: uuid.UUID(companyID).String(),
		EntryId:   uuid.NewString(),
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("status = %s, want Unauthenticated (%v)", status.Code(err), err)
	}
	if ledgerService.getCalls != 0 {
		t.Fatal("unauthenticated request reached the engine")
	}
}

func TestGRPCBoundaryInjectsPrincipalAndRejectsForeignCompany(t *testing.T) {
	grantedCompany := types.NewCompanyID()
	foreignCompany := types.NewCompanyID()
	entryID := types.NewJournalEntryID()
	ledgerService := &boundaryTestLedger{}
	conn := startBoundaryGRPCServer(t, &serviceTestEngine{ledger: ledgerService}, true)
	client := accountingv1.NewAccountingServiceClient(conn)

	validCtx := metadata.NewOutgoingContext(context.Background(),
		gatewayGRPCMetadata("gateway-user-1", []types.CompanyID{grantedCompany}, time.Now().UTC()))
	response, err := client.GetEntry(validCtx, &accountingv1.GetEntryRequest{
		CompanyId: uuid.UUID(grantedCompany).String(),
		EntryId:   uuid.UUID(entryID).String(),
	})
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}
	if response.GetEntryId() != uuid.UUID(entryID).String() {
		t.Fatalf("entry_id = %q", response.GetEntryId())
	}
	principal, ok := publicapplication.PrincipalFromContext(ledgerService.getCtx)
	if !ok || principal.ActorID != "gateway-user-1" || !principalHasCompany(principal, grantedCompany) {
		t.Fatalf("principal = %#v, present = %v", principal, ok)
	}
	requestMetadata, _ := publicapplication.RequestMetadataFromContext(ledgerService.getCtx)
	if requestMetadata.SourceSystem != trustedServiceSource {
		t.Fatalf("SourceSystem = %q, want %q", requestMetadata.SourceSystem, trustedServiceSource)
	}

	_, err = client.GetEntry(validCtx, &accountingv1.GetEntryRequest{
		CompanyId: uuid.UUID(foreignCompany).String(),
		EntryId:   uuid.NewString(),
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("foreign company status = %s, want PermissionDenied (%v)", status.Code(err), err)
	}
}

func TestGRPCBoundaryRejectsExpiredMetadata(t *testing.T) {
	ledgerService := &boundaryTestLedger{}
	companyID := types.NewCompanyID()
	conn := startBoundaryGRPCServer(t, &serviceTestEngine{ledger: ledgerService}, true)

	ctx := metadata.NewOutgoingContext(context.Background(),
		gatewayGRPCMetadata("gateway-user-1", []types.CompanyID{companyID}, time.Now().UTC().Add(-10*time.Minute)))
	_, err := accountingv1.NewAccountingServiceClient(conn).GetEntry(ctx, &accountingv1.GetEntryRequest{
		CompanyId: uuid.UUID(companyID).String(),
		EntryId:   uuid.NewString(),
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("status = %s, want Unauthenticated (%v)", status.Code(err), err)
	}
	if ledgerService.getCalls != 0 {
		t.Fatal("expired credentials reached the engine")
	}
}

func TestGRPCBoundaryRejectsForeignCompanyOnEvents(t *testing.T) {
	grantedCompany := types.NewCompanyID()
	foreignCompany := types.NewCompanyID()
	conn := startBoundaryGRPCServer(t, &serviceTestEngine{events: &capturingEventService{}}, true)

	ctx := metadata.NewOutgoingContext(context.Background(),
		gatewayGRPCMetadata("gateway-user-1", []types.CompanyID{grantedCompany}, time.Now().UTC()))
	_, err := accountingv1.NewEventServiceClient(conn).ProcessEvent(ctx, &accountingv1.ProcessEventRequest{
		EventId: "event-1", EventType: "sale", CompanyId: uuid.UUID(foreignCompany).String(),
		EffectiveDate: "2026-11-14", Amount: "10.00", Currency: "USD",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("status = %s, want PermissionDenied (%v)", status.Code(err), err)
	}
}

func TestGRPCBoundaryProtectsReflectionStreams(t *testing.T) {
	companyID := types.NewCompanyID()
	conn := startBoundaryGRPCServer(t, &serviceTestEngine{ledger: &boundaryTestLedger{}}, true)
	reflectionClient := reflectionv1.NewServerReflectionClient(conn)

	unauthorized, err := reflectionClient.ServerReflectionInfo(context.Background())
	if err == nil {
		err = unauthorized.Send(&reflectionv1.ServerReflectionRequest{
			MessageRequest: &reflectionv1.ServerReflectionRequest_ListServices{ListServices: ""},
		})
		if err == nil {
			_, err = unauthorized.Recv()
		}
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("reflection without credentials status = %s, want Unauthenticated (%v)", status.Code(err), err)
	}

	authorizedCtx := metadata.NewOutgoingContext(context.Background(),
		gatewayGRPCMetadata("gateway-user-1", []types.CompanyID{companyID}, time.Now().UTC()))
	authorized, err := reflectionClient.ServerReflectionInfo(authorizedCtx)
	if err != nil {
		t.Fatalf("authorized reflection: %v", err)
	}
	if err := authorized.Send(&reflectionv1.ServerReflectionRequest{
		MessageRequest: &reflectionv1.ServerReflectionRequest_ListServices{ListServices: ""},
	}); err != nil {
		t.Fatalf("authorized reflection send: %v", err)
	}
	if _, err := authorized.Recv(); err != nil {
		t.Fatalf("authorized reflection recv: %v", err)
	}
}

func startBoundaryGRPCServer(t *testing.T, engine *serviceTestEngine, withAuth bool) *grpc.ClientConn {
	t.Helper()
	var server *grpc.Server
	if withAuth {
		verifier, err := newServiceVerifier(gatewayTestConfig())
		if err != nil {
			t.Fatalf("newServiceVerifier: %v", err)
		}
		server = newGRPCServerWithVerifier(engine, verifier)
	} else {
		server = newGRPCServer(engine)
	}
	listener := bufconn.Listen(1 << 20)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func gatewayGRPCMetadata(actor string, companies []types.CompanyID, timestamp time.Time) metadata.MD {
	headers := gatewayHTTPHeaders(actor, companies, timestamp)
	pairs := make([]string, 0, len(headers)*2)
	for key, value := range headers {
		pairs = append(pairs, strings.ToLower(key), value)
	}
	return metadata.Pairs(pairs...)
}

func principalHasCompany(principal publicapplication.Principal, companyID types.CompanyID) bool {
	for _, allowed := range principal.CompanyIDs {
		if allowed == companyID {
			return true
		}
	}
	return false
}

func TestRunServerContextFailsClosedOnInvalidAuthConfig(t *testing.T) {
	err := runServerContext(context.Background(), Config{
		HTTPPort: "8080",
		AuthMode: serviceauth.ModeJWTRS256,
	}, &serviceTestEngine{}, nil)
	if err == nil {
		t.Fatal("expected startup error for jwt-rs256 without a public key")
	}
	if !strings.Contains(err.Error(), "authentication") {
		t.Fatalf("error = %v, want an authentication configuration error", err)
	}
}

func TestRunGRPCServerFailsClosedOnInvalidAuthConfig(t *testing.T) {
	err := RunGRPCServer(context.Background(), Config{
		GRPCPort: "50051",
		AuthMode: serviceauth.ModeJWTRS256,
	}, &serviceTestEngine{})
	if err == nil {
		t.Fatal("expected startup error for jwt-rs256 without a public key")
	}
}

func TestHTTPServerFailsClosedWhenAuthConfigInvalid(t *testing.T) {
	server := newHTTPServer(Config{
		HTTPPort: "8080",
		AuthMode: serviceauth.ModeJWTRS256,
	}, &serviceTestEngine{ledger: &boundaryTestLedger{}})

	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}
