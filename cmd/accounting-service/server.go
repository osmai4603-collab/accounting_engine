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
	"syscall"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/observability"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"

	"github.com/google/uuid"
)

const (
	maxRequestBodyBytes   = 1 << 20
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
	listener, err := net.Listen("tcp", ":"+cfg.HTTPPort)
	if err != nil {
		return err
	}

	server := newHTTPServer(cfg, engine, health)
	log.Printf("Starting Standalone Accounting Service HTTP Daemon on %s", server.Addr)
	return serveHTTP(ctx, server, listener)
}

func newHTTPServer(cfg Config, engine sdk.Engine, healthChecks ...func(context.Context) error) *http.Server {
	var health func(context.Context) error
	if len(healthChecks) > 0 {
		health = healthChecks[0]
	}
	srv := &Server{engine: engine, metrics: observability.NewMetrics(), health: health}
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if srv.health == nil {
			http.Error(w, "NOT READY", http.StatusServiceUnavailable)
			return
		}
		if err := srv.health(r.Context()); err != nil {
			http.Error(w, "NOT READY", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("READY"))
	})
	mux.Handle("/metrics", srv.metrics.Handler())
	mux.HandleFunc("/api/v1/entries", srv.handleEntries)
	mux.HandleFunc("/api/v1/trial-balance", srv.handleTrialBalance)
	mux.HandleFunc("/api/v1/events", srv.handleEvents)

	return &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
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
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var entry ledger.JournalEntry
	if err := decodeRequestJSON(w, r, &entry); err != nil {
		writeRequestDecodeError(w, err)
		return
	}
	started := time.Now()
	s.metrics.IncPostingAttempts()
	ctx := publicapplication.WithRequestMetadata(r.Context(), requestMetadataFromHTTP(r, "http"))
	posted, err := s.engine.Ledger().PostEntry(ctx, entry)
	s.metrics.ObservePostingDuration(time.Since(started).Seconds())
	if err != nil {
		s.metrics.IncPostingFailure("service")
		writeApplicationError(w, err)
		return
	}
	s.metrics.IncPostedEntries()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(posted)
}

func (s *Server) handleTrialBalance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	companyIDStr := r.URL.Query().Get("company_id")
	currency := r.URL.Query().Get("currency")
	if currency == "" {
		currency = "USD"
	}
	parsedCompanyID, err := uuid.Parse(companyIDStr)
	if err != nil {
		http.Error(w, "invalid company_id", http.StatusBadRequest)
		return
	}
	companyID := types.CompanyID(parsedCompanyID)
	tb, err := s.engine.Reporting().GetTrialBalance(r.Context(), companyID, time.Now().UTC(), currency)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(tb)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var ev events.AccountingEvent
	if err := decodeRequestJSON(w, r, &ev); err != nil {
		writeRequestDecodeError(w, err)
		return
	}
	started := time.Now()
	s.metrics.IncPostingAttempts()
	ctx := publicapplication.WithRequestMetadata(r.Context(), requestMetadataFromHTTP(r, "accounting-service"))
	entry, err := s.engine.Events().ProcessEvent(ctx, ev)
	s.metrics.ObservePostingDuration(time.Since(started).Seconds())
	if err != nil {
		s.metrics.IncPostingFailure("service")
		writeApplicationError(w, err)
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

func writeApplicationError(w http.ResponseWriter, err error) {
	statusCode := http.StatusInternalServerError
	message := "internal error"
	var classified interface {
		PublicMessage() string
	}
	if errors.As(err, &classified) {
		message = classified.PublicMessage()
		switch {
		case errors.Is(err, publicapplication.ErrValidation):
			statusCode = http.StatusBadRequest
		case errors.Is(err, publicapplication.ErrUnauthorized):
			statusCode = http.StatusUnauthorized
		case errors.Is(err, publicapplication.ErrForbidden):
			statusCode = http.StatusForbidden
		case errors.Is(err, publicapplication.ErrNotFound):
			statusCode = http.StatusNotFound
		case errors.Is(err, publicapplication.ErrConflict), errors.Is(err, publicapplication.ErrDuplicateRequest):
			statusCode = http.StatusConflict
		case errors.Is(err, publicapplication.ErrDependencyUnavailable):
			statusCode = http.StatusServiceUnavailable
		}
	}
	http.Error(w, message, statusCode)
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

func writeRequestDecodeError(w http.ResponseWriter, err error) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, "invalid request body", http.StatusBadRequest)
}
