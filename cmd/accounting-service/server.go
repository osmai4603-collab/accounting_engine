package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/observability"
	"github.com/osmai4603-collab/accounting_engine/internal/serviceauth"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"

	"github.com/google/uuid"
)

const (
	maxRequestBodyBytes   = 1 << 20
	maxEntryLines         = 500
	maxEventDimensions    = 64
	serverShutdownTimeout = 10 * time.Second
)

type Server struct {
	engine  sdk.Engine
	metrics *observability.Metrics
	health  func(context.Context) error
}

func RunServer(cfg Config, engine sdk.Engine, healthChecks ...func(context.Context) error) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return RunServerContext(ctx, cfg, engine, healthChecks...)
}

func RunServerContext(ctx context.Context, cfg Config, engine sdk.Engine, healthChecks ...func(context.Context) error) error {
	var health func(context.Context) error
	if len(healthChecks) > 0 {
		health = healthChecks[0]
	}
	return runServerContext(ctx, cfg, engine, health)
}

func runServerContext(ctx context.Context, cfg Config, engine sdk.Engine, health func(context.Context) error) error {
	verifier, err := newServiceVerifier(cfg)
	if err != nil {
		return fmt.Errorf("service authentication configuration: %w", err)
	}
	listener, err := net.Listen("tcp", ":"+cfg.HTTPPort)
	if err != nil {
		return err
	}

	server := newHTTPServerWithVerifier(cfg, engine, verifier, health)
	log.Printf("Starting Standalone Accounting Service HTTP Daemon on %s", server.Addr)
	return serveHTTP(ctx, server, listener)
}

func newHTTPServer(cfg Config, engine sdk.Engine, healthChecks ...func(context.Context) error) *http.Server {
	verifier, err := newServiceVerifier(cfg)
	if err != nil {
		logAuthConfigurationError(err)
		return configureHTTPServer(cfg, &http.Server{
			Addr:    ":" + cfg.HTTPPort,
			Handler: configurationErrorHandler{reason: err},
		})
	}
	return newHTTPServerWithVerifier(cfg, engine, verifier, healthChecks...)
}

func newHTTPServerWithVerifier(cfg Config, engine sdk.Engine, verifier serviceauth.Verifier, healthChecks ...func(context.Context) error) *http.Server {
	var health func(context.Context) error
	if len(healthChecks) > 0 {
		health = healthChecks[0]
	}
	srv := &Server{engine: engine, metrics: observability.NewMetrics(), health: health}
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeHTTPError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	mux.HandleFunc("/startupz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeHTTPError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("STARTED"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeHTTPError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		if srv.health == nil {
			writeHTTPError(w, r, http.StatusServiceUnavailable, "not_ready", "service is not ready")
			return
		}
		if err := srv.health(r.Context()); err != nil {
			writeHTTPError(w, r, http.StatusServiceUnavailable, "not_ready", "service is not ready")
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("READY"))
	})
	mux.Handle("/metrics", srv.metrics.Handler())
	mux.HandleFunc("/api/v1/entries", srv.handleEntries)
	mux.HandleFunc("/api/v1/trial-balance", srv.handleTrialBalance)
	mux.HandleFunc("/api/v1/events", srv.handleEvents)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeHTTPError(w, r, http.StatusNotFound, "not_found", "endpoint not found")
	})

	var handler http.Handler = mux
	if boundary := newHTTPAuthBoundary(cfg, verifier); boundary != nil {
		handler = boundary.middleware(handler)
	}
	handler = newRequestRateLimitMiddleware(newRequestRateLimiter(), handler)

	return configureHTTPServer(cfg, &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	})
}

func configureHTTPServer(cfg Config, server *http.Server) *http.Server {
	if server.Handler == nil {
		server.Handler = http.NotFoundHandler()
	}
	server.Handler = newHTTPRequestContextMiddleware(cfg.RequireTLS, httpRequestTimeout, server.Handler)
	return server
}

func serveHTTP(ctx context.Context, server *http.Server, listener net.Listener) error {
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.Serve(listener)
	}()

	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), serverShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}
	if err := <-serverErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) handleEntries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeHTTPError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	var entry ledger.JournalEntry
	if err := decodeRequestJSON(w, r, &entry); err != nil {
		writeRequestDecodeError(w, r, err)
		return
	}
	if err := validateHTTPEntry(entry); err != nil {
		writeHTTPError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := authorizeCompany(r.Context(), entry.CompanyID); err != nil {
		writeApplicationError(w, r, err)
		return
	}
	started := time.Now()
	s.metrics.IncPostingAttempts()
	ctx := withHTTPRequestMetadata(r, "http")
	posted, err := s.engine.Ledger().PostEntry(ctx, entry)
	s.metrics.ObservePostingDuration(time.Since(started).Seconds())
	if err != nil {
		s.metrics.IncPostingFailure("service")
		writeApplicationError(w, r, err)
		return
	}
	s.metrics.IncPostedEntries()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(posted)
}

func (s *Server) handleTrialBalance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeHTTPError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	query := r.URL.Query()
	companyIDs := query["company_id"]
	currencies := query["currency"]
	if len(r.URL.RawQuery) > 2048 || len(companyIDs) != 1 || len(companyIDs[0]) > 36 || len(currencies) > 1 {
		writeHTTPError(w, r, http.StatusBadRequest, "invalid_request", "invalid query parameters")
		return
	}
	companyIDStr := companyIDs[0]
	currency := ""
	if len(currencies) == 1 {
		currency = strings.TrimSpace(currencies[0])
	}
	if currency == "" {
		currency = "USD"
	}
	if len(currency) > 3 {
		writeHTTPError(w, r, http.StatusBadRequest, "invalid_request", "invalid currency")
		return
	}
	currency = strings.ToUpper(currency)
	if _, ok := types.LookupCurrency(currency); !ok {
		writeHTTPError(w, r, http.StatusBadRequest, "invalid_request", "invalid currency")
		return
	}
	parsedCompanyID, err := uuid.Parse(companyIDStr)
	if err != nil || parsedCompanyID == uuid.Nil {
		writeHTTPError(w, r, http.StatusBadRequest, "invalid_request", "invalid company_id")
		return
	}
	companyID := types.CompanyID(parsedCompanyID)
	if err := authorizeCompany(r.Context(), companyID); err != nil {
		writeApplicationError(w, r, err)
		return
	}
	tb, err := s.engine.Reporting().GetTrialBalance(r.Context(), companyID, time.Now().UTC(), currency)
	if err != nil {
		writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(tb)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeHTTPError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	var ev events.AccountingEvent
	if err := decodeRequestJSON(w, r, &ev); err != nil {
		writeRequestDecodeError(w, r, err)
		return
	}
	if err := validateHTTPEvent(ev); err != nil {
		writeHTTPError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := authorizeCompany(r.Context(), ev.CompanyID); err != nil {
		writeApplicationError(w, r, err)
		return
	}
	started := time.Now()
	s.metrics.IncPostingAttempts()
	ctx := withHTTPRequestMetadata(r, "accounting-service")
	entry, err := s.engine.Events().ProcessEvent(ctx, ev)
	s.metrics.ObservePostingDuration(time.Since(started).Seconds())
	if err != nil {
		s.metrics.IncPostingFailure("service")
		writeApplicationError(w, r, err)
		return
	}
	s.metrics.IncPostedEntries()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(entry)
}

func requestMetadataFromHTTP(r *http.Request, fallbackSource string) publicapplication.RequestMetadata {
	return publicapplication.RequestMetadata{
		CorrelationID:  r.Header.Get("X-Correlation-ID"),
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		SourceSystem:   headerOrDefault(r.Header.Get("X-Source-System"), fallbackSource),
	}
}

func headerOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func writeApplicationError(w http.ResponseWriter, r *http.Request, err error) {
	statusCode := http.StatusInternalServerError
	message := "internal error"
	code := "internal_error"
	var classified interface {
		PublicMessage() string
	}
	if errors.As(err, &classified) {
		if publicMessage := strings.TrimSpace(classified.PublicMessage()); publicMessage != "" && len(publicMessage) <= 1024 {
			message = publicMessage
		}
		code = "application_error"
		switch {
		case errors.Is(err, publicapplication.ErrValidation):
			statusCode = http.StatusBadRequest
			code = "validation_error"
		case errors.Is(err, publicapplication.ErrUnauthorized):
			statusCode = http.StatusUnauthorized
			code = "unauthorized"
		case errors.Is(err, publicapplication.ErrForbidden):
			statusCode = http.StatusForbidden
			code = "forbidden"
		case errors.Is(err, publicapplication.ErrNotFound):
			statusCode = http.StatusNotFound
			code = "not_found"
		case errors.Is(err, publicapplication.ErrConflict), errors.Is(err, publicapplication.ErrDuplicateRequest):
			statusCode = http.StatusConflict
			code = "conflict"
		case errors.Is(err, publicapplication.ErrDependencyUnavailable):
			statusCode = http.StatusServiceUnavailable
			code = "dependency_unavailable"
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		statusCode, code, message = http.StatusGatewayTimeout, "request_timeout", "request timed out"
	} else if errors.Is(err, context.Canceled) {
		statusCode, code, message = http.StatusRequestTimeout, "request_cancelled", "request was cancelled"
	}
	writeHTTPError(w, r, statusCode, code, message)
}

func decodeRequestJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	decoder := json.NewDecoder(r.Body)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	if len(raw) == 0 || raw[0] != '{' {
		return errors.New("request body must be a JSON object")
	}
	if err := json.Unmarshal(raw, destination); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON value")
		}
		return err
	}
	return nil
}

func writeRequestDecodeError(w http.ResponseWriter, r *http.Request, err error) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		writeHTTPError(w, r, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
		return
	}
	writeHTTPError(w, r, http.StatusBadRequest, "invalid_request", "invalid request body")
}
