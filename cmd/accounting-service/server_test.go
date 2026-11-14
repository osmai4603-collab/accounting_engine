package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/serviceauth"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/reporting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
)

func TestHTTPHandlersReturnBadRequestForInvalidInput(t *testing.T) {
	handler := newHTTPServer(Config{}, nil).Handler
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			name:   "invalid company UUID",
			method: http.MethodGet,
			path:   "/api/v1/trial-balance?company_id=not-a-uuid",
		},
		{
			name:   "invalid event UUID",
			method: http.MethodPost,
			path:   "/api/v1/events",
			body:   `{"CompanyID":"not-a-uuid"}`,
		},
		{
			name:   "invalid entry UUID",
			method: http.MethodPost,
			path:   "/api/v1/entries",
			body:   `{"CompanyID":"not-a-uuid"}`,
		},
		{
			name:   "malformed JSON",
			method: http.MethodPost,
			path:   "/api/v1/entries",
			body:   `{"Description":`,
		},
		{
			name:   "non-object JSON",
			method: http.MethodPost,
			path:   "/api/v1/entries",
			body:   `null`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body)
			}
		})
	}
}

func TestReadinessChecksRequiredStorage(t *testing.T) {
	healthy := true
	server := newHTTPServer(Config{}, nil, func(context.Context) error {
		if !healthy {
			return errors.New("storage unavailable")
		}
		return nil
	})

	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("healthy readiness status = %d, want %d", response.Code, http.StatusOK)
	}

	healthy = false
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unhealthy readiness status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestReadinessFailsClosedWithoutStorageHealthCheck(t *testing.T) {
	server := newHTTPServer(Config{}, nil)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestPostingMetricsReflectSuccessfulAndFailedRequests(t *testing.T) {
	ledgerService := &serviceTestLedger{}
	server := newHTTPServer(Config{}, &serviceTestEngine{ledger: ledgerService}, func(context.Context) error { return nil })
	body, err := json.Marshal(ledger.JournalEntry{CompanyID: types.NewCompanyID()})
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/entries", strings.NewReader(string(body))))
	if response.Code != http.StatusCreated {
		t.Fatalf("successful post status = %d, want %d; body: %s", response.Code, http.StatusCreated, response.Body)
	}

	ledgerService.err = errors.New("storage failure")
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/entries", strings.NewReader(string(body))))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("failed post status = %d, want %d", response.Code, http.StatusInternalServerError)
	}

	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	metrics := response.Body.String()
	for _, expected := range []string{
		"accounting_posting_attempts_total 2",
		"accounting_posted_entries_total 1",
		"accounting_posting_failures_total 1",
		"accounting_posting_duration_seconds_count 2",
	} {
		if !strings.Contains(metrics, expected) {
			t.Errorf("metrics output missing %q:\n%s", expected, metrics)
		}
	}
}

func TestHTTPEventHandlerPassesTransportMetadataToIngestion(t *testing.T) {
	captured := &capturingEventService{}
	server := newHTTPServer(Config{}, &serviceTestEngine{events: captured})
	body, err := json.Marshal(events.AccountingEvent{
		EventID: "event-1", EventType: "sale", CompanyID: types.NewCompanyID(),
		EffectiveDate:  time.Date(2026, time.November, 14, 0, 0, 0, 0, time.UTC),
		IdempotencyKey: "body-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/events",
		strings.NewReader(string(body)))
	request.Header.Set("X-Correlation-ID", "http-correlation")
	request.Header.Set("X-Source-System", "billing")
	request.Header.Set("Idempotency-Key", "header-key")
	response := httptest.NewRecorder()

	server.Handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusCreated, response.Body)
	}
	metadata, ok := publicapplication.RequestMetadataFromContext(captured.ctx)
	if !ok {
		t.Fatal("event handler did not propagate request metadata")
	}
	if metadata.CorrelationID != "http-correlation" ||
		metadata.IdempotencyKey != "header-key" ||
		metadata.SourceSystem != "billing" {
		t.Fatalf("request metadata = %#v", metadata)
	}
	if captured.event.EventID != "event-1" || captured.event.EventType != "sale" {
		t.Fatalf("event handler changed event payload: %#v", captured.event)
	}
}

func TestHTTPMapsApplicationErrorsWithoutLeakingCauses(t *testing.T) {
	response := httptest.NewRecorder()
	cause := errors.New("database password=secret")
	request := httptest.NewRequest(http.MethodPost, "/api/v1/entries", nil)
	writeApplicationError(response, request, publicapplication.NewError(publicapplication.ErrValidation, "invalid journal entry", cause))
	var validationBody httpAPIError
	if err := json.Unmarshal(response.Body.Bytes(), &validationBody); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusBadRequest || validationBody.Error.Message != "invalid journal entry" ||
		validationBody.Error.Code != "validation_error" {
		t.Fatalf("validation response = %d %q", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	writeApplicationError(response, request, cause)
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), cause.Error()) ||
		strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("internal error leaked details: %d %q", response.Code, response.Body.String())
	}
}

type serviceTestEngine struct {
	ledger    sdk.LedgerService
	events    sdk.EventIngestionService
	reporting sdk.ReportingService
}

func (e *serviceTestEngine) Ledger() sdk.LedgerService { return e.ledger }
func (e *serviceTestEngine) Reporting() sdk.ReportingService {
	if e.reporting != nil {
		return e.reporting
	}
	return serviceTestReporting{}
}
func (e *serviceTestEngine) Governance() sdk.GovernanceService { return serviceTestGovernance{} }
func (e *serviceTestEngine) Events() sdk.EventIngestionService {
	if e.events != nil {
		return e.events
	}
	return serviceTestEvents{}
}
func (e *serviceTestEngine) Standards() sdk.StandardsService            { return nil }
func (e *serviceTestEngine) Scheduler() standards.SchedulerPort         { return nil }
func (e *serviceTestEngine) Close() error                               { return nil }
func (e *serviceTestEngine) CloseWithContext(ctx context.Context) error { return nil }

type serviceTestLedger struct {
	err   error
	calls int
}

func (s *serviceTestLedger) PostEntry(_ context.Context, entry ledger.JournalEntry) (ledger.JournalEntry, error) {
	s.calls++
	return entry, s.err
}
func (*serviceTestLedger) GetEntry(context.Context, types.CompanyID, types.JournalEntryID) (ledger.JournalEntry, error) {
	return ledger.JournalEntry{}, nil
}
func (*serviceTestLedger) ReverseEntry(context.Context, types.CompanyID, types.JournalEntryID, string) (ledger.JournalEntry, error) {
	return ledger.JournalEntry{}, nil
}

type serviceTestReporting struct{}

func (serviceTestReporting) GetTrialBalance(context.Context, types.CompanyID, time.Time, string) (reporting.TrialBalance, error) {
	return reporting.TrialBalance{}, nil
}
func (serviceTestReporting) GetBalanceSheet(context.Context, types.CompanyID, time.Time, string) (reporting.BalanceSheetReport, error) {
	return reporting.BalanceSheetReport{}, nil
}
func (serviceTestReporting) GetIncomeStatement(context.Context, types.CompanyID, time.Time, time.Time, string) (reporting.IncomeStatementReport, error) {
	return reporting.IncomeStatementReport{}, nil
}
func (serviceTestReporting) GetGeneralLedger(context.Context, types.CompanyID, types.AccountID, time.Time, time.Time) (reporting.GeneralLedgerReport, error) {
	return reporting.GeneralLedgerReport{}, nil
}

type serviceTestGovernance struct{}

func (serviceTestGovernance) RecordAuditLog(context.Context, string, map[string]any) error {
	return nil
}
func (serviceTestGovernance) GetApprovalFingerprint(context.Context, types.JournalEntryID) (string, error) {
	return "", nil
}

type serviceTestEvents struct{}

func (serviceTestEvents) ProcessEvent(context.Context, events.AccountingEvent) (ledger.JournalEntry, error) {
	return ledger.JournalEntry{}, nil
}

type capturingEventService struct {
	ctx   context.Context
	event events.AccountingEvent
}

func (s *capturingEventService) ProcessEvent(ctx context.Context, event events.AccountingEvent) (ledger.JournalEntry, error) {
	s.ctx = ctx
	s.event = event
	return ledger.JournalEntry{ID: types.NewJournalEntryID(), Status: ledger.EntryPosted}, nil
}

type recordingTrialBalanceService struct {
	calls int
}

func (s *recordingTrialBalanceService) GetTrialBalance(context.Context, types.CompanyID, time.Time, string) (reporting.TrialBalance, error) {
	s.calls++
	return reporting.TrialBalance{}, nil
}
func (*recordingTrialBalanceService) GetBalanceSheet(context.Context, types.CompanyID, time.Time, string) (reporting.BalanceSheetReport, error) {
	return reporting.BalanceSheetReport{}, nil
}
func (*recordingTrialBalanceService) GetIncomeStatement(context.Context, types.CompanyID, time.Time, time.Time, string) (reporting.IncomeStatementReport, error) {
	return reporting.IncomeStatementReport{}, nil
}
func (*recordingTrialBalanceService) GetGeneralLedger(context.Context, types.CompanyID, types.AccountID, time.Time, time.Time) (reporting.GeneralLedgerReport, error) {
	return reporting.GeneralLedgerReport{}, nil
}

type contextAwareLedger struct {
	started       chan struct{}
	contextErrors chan error
}

func (s *contextAwareLedger) PostEntry(ctx context.Context, entry ledger.JournalEntry) (ledger.JournalEntry, error) {
	close(s.started)
	<-ctx.Done()
	s.contextErrors <- ctx.Err()
	return ledger.JournalEntry{}, ctx.Err()
}
func (*contextAwareLedger) GetEntry(context.Context, types.CompanyID, types.JournalEntryID) (ledger.JournalEntry, error) {
	return ledger.JournalEntry{}, nil
}
func (*contextAwareLedger) ReverseEntry(context.Context, types.CompanyID, types.JournalEntryID, string) (ledger.JournalEntry, error) {
	return ledger.JournalEntry{}, nil
}

func TestHTTPHandlersRejectOversizedRequestBody(t *testing.T) {
	handler := newHTTPServer(Config{}, nil).Handler
	body := `{"Description":"` + strings.Repeat("x", maxRequestBodyBytes) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/entries", strings.NewReader(body))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusRequestEntityTooLarge, response.Body)
	}
}

func TestHTTPServerTimeoutConfiguration(t *testing.T) {
	server := newHTTPServer(Config{}, nil)
	if server.ReadHeaderTimeout != 5*time.Second {
		t.Errorf("ReadHeaderTimeout = %s, want 5s", server.ReadHeaderTimeout)
	}
	if server.ReadTimeout != 15*time.Second {
		t.Errorf("ReadTimeout = %s, want 15s", server.ReadTimeout)
	}
	if server.WriteTimeout != 30*time.Second {
		t.Errorf("WriteTimeout = %s, want 30s", server.WriteTimeout)
	}
	if server.IdleTimeout != 120*time.Second {
		t.Errorf("IdleTimeout = %s, want 120s", server.IdleTimeout)
	}
}

func TestHTTPErrorSchemaCorrelationIDAndSecurityHeaders(t *testing.T) {
	server := newHTTPServer(Config{}, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/entries", strings.NewReader(`{"Description":`))
	request.Header.Set("X-Correlation-ID", "http-test-42")
	response := httptest.NewRecorder()

	server.Handler.ServeHTTP(response, request)

	var body httpAPIError
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("error response is not JSON: %v; body=%s", err, response.Body)
	}
	if response.Code != http.StatusBadRequest || body.Error.Code != "invalid_request" ||
		body.CorrelationID != "http-test-42" ||
		response.Header().Get("X-Correlation-ID") != body.CorrelationID {
		t.Fatalf("unexpected error response: status=%d body=%+v headers=%v", response.Code, body, response.Header())
	}
	for header, want := range map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
		"Cache-Control":           "no-store",
	} {
		if got := response.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if response.Header().Get("Strict-Transport-Security") != "" {
		t.Fatal("HSTS should be set only when TLS is enabled")
	}
}

func TestHTTPAuthenticationFailuresUseSharedErrorSchema(t *testing.T) {
	server := newHTTPServer(Config{
		AuthMode:      serviceauth.ModeGatewayHMAC,
		AuthSecret:    strings.Repeat("s", 32),
		AuthClockSkew: time.Minute,
	}, &serviceTestEngine{ledger: &serviceTestLedger{}})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/entries", strings.NewReader(`{"CompanyID":"`+uuid.NewString()+`"}`))
	request.Header.Set("X-Correlation-ID", "auth-correlation")
	response := httptest.NewRecorder()

	server.Handler.ServeHTTP(response, request)

	var body httpAPIError
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("authentication error is not JSON: %v; body=%s", err, response.Body)
	}
	if response.Code != http.StatusUnauthorized || body.Error.Code != "unauthorized" ||
		body.CorrelationID != "auth-correlation" ||
		response.Header().Get("WWW-Authenticate") != "HMAC-SHA256" {
		t.Fatalf("unexpected authentication response: status=%d body=%+v headers=%v", response.Code, body, response.Header())
	}
}

func TestHTTPRejectsEntryBoundsBeforeCallingService(t *testing.T) {
	ledgerService := &serviceTestLedger{}
	server := newHTTPServer(Config{}, &serviceTestEngine{ledger: ledgerService})
	entry := ledger.JournalEntry{CompanyID: types.NewCompanyID(), Lines: make([]ledger.EntryLine, maxEntryLines+1)}
	body, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/entries", strings.NewReader(string(body))))

	if response.Code != http.StatusBadRequest || ledgerService.calls != 0 {
		t.Fatalf("status = %d and service calls = %d; want 400 and no service call", response.Code, ledgerService.calls)
	}
}

func TestHTTPRejectsInvalidTrialBalanceCurrencyBeforeCallingService(t *testing.T) {
	reportingService := &recordingTrialBalanceService{}
	server := newHTTPServer(Config{}, &serviceTestEngine{reporting: reportingService})
	companyID := types.NewCompanyID()
	request := httptest.NewRequest(http.MethodGet,
		"/api/v1/trial-balance?company_id="+uuid.UUID(companyID).String()+"&currency=NOPE", nil)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest || reportingService.calls != 0 {
		t.Fatalf("status = %d and reporting calls = %d; want 400 and no service call", response.Code, reportingService.calls)
	}
}

func TestHTTPRateLimitReturnsStructuredError(t *testing.T) {
	handler := newRequestRateLimitMiddleware(newRequestRateLimiter(),
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	var response *httptest.ResponseRecorder
	for i := 0; i < httpRateLimitRequests+1; i++ {
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/test", nil))
	}
	var body httpAPIError
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("rate-limit response is not JSON: %v; body=%s", err, response.Body)
	}
	if response.Code != http.StatusTooManyRequests || body.Error.Code != "rate_limit_exceeded" ||
		response.Header().Get("Retry-After") == "" {
		t.Fatalf("unexpected rate-limit response: status=%d body=%+v headers=%v", response.Code, body, response.Header())
	}
}

func TestHTTPRequestDeadlinePropagatesToEngineAndReturnsGatewayTimeout(t *testing.T) {
	ledgerService := &contextAwareLedger{started: make(chan struct{}), contextErrors: make(chan error, 1)}
	server := newHTTPServer(Config{}, &serviceTestEngine{ledger: ledgerService})
	body, err := json.Marshal(ledger.JournalEntry{CompanyID: types.NewCompanyID()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/entries", strings.NewReader(string(body))).WithContext(ctx)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)

	if response.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusGatewayTimeout, response.Body)
	}
	if err := <-ledgerService.contextErrors; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("engine context error = %v, want deadline exceeded", err)
	}
}

func TestHTTPClientDisconnectCancelsEngineContext(t *testing.T) {
	ledgerService := &contextAwareLedger{started: make(chan struct{}), contextErrors: make(chan error, 1)}
	server := newHTTPServer(Config{}, &serviceTestEngine{ledger: ledgerService})
	body, err := json.Marshal(ledger.JournalEntry{CompanyID: types.NewCompanyID()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "/api/v1/entries", strings.NewReader(string(body))).WithContext(ctx)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		server.Handler.ServeHTTP(response, request)
		close(done)
	}()
	<-ledgerService.started
	cancel()
	select {
	case err := <-ledgerService.contextErrors:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("engine context error = %v, want canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("engine context was not cancelled after client disconnect")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not return after client disconnect")
	}
}

func TestHTTPHeaderReadTimeoutClosesSlowConnection(t *testing.T) {
	server := &http.Server{
		Handler:           http.NotFoundHandler(),
		ReadHeaderTimeout: 25 * time.Millisecond,
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- serveHTTP(ctx, server, listener)
	}()

	connection, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if _, err := io.WriteString(connection, "GET / HTTP/1.1\r\nHost: test\r\n"); err != nil {
		cancel()
		_ = connection.Close()
		t.Fatal(err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(time.Second))
	_, readErr := io.ReadAll(connection)
	_ = connection.Close()
	cancel()
	if readErr != nil {
		t.Fatalf("slow connection did not close after header timeout: %v", readErr)
	}
	if err := <-serverErrors; err != nil {
		t.Fatalf("serveHTTP returned error: %v", err)
	}
}

func TestHTTPServerGracefulShutdownDrainsActiveRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = fmt.Fprint(w, "finished")
	})
	server := newHTTPServer(Config{}, nil)
	server.Handler = handler
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- serveHTTP(ctx, server, listener)
	}()

	responseErrors := make(chan error, 1)
	go func() {
		response, requestErr := http.Get("http://" + listener.Addr().String())
		if requestErr == nil {
			_ = response.Body.Close()
		}
		responseErrors <- requestErr
	}()

	<-started
	cancel()
	select {
	case err := <-serverErrors:
		t.Fatalf("shutdown returned before active request completed: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	close(release)
	if err := <-serverErrors; err != nil {
		t.Fatalf("serveHTTP returned error: %v", err)
	}
	if err := <-responseErrors; err != nil {
		t.Fatalf("active request failed during shutdown: %v", err)
	}
}
