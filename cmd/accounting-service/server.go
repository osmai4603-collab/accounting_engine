package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"accounting_engine/pkg/api/v1/events"
	"accounting_engine/pkg/api/v1/ledger"
	"accounting_engine/pkg/api/v1/types"
	"accounting_engine/pkg/sdk"
	"github.com/google/uuid"
)

type Server struct {
	engine sdk.Engine
}

func RunServer(cfg Config, engine sdk.Engine) error {
	srv := &Server{engine: engine}
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("READY"))
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("# HELP accounting_engine_up Accounting engine status\naccounting_engine_up 1\n"))
	})
	mux.HandleFunc("/api/v1/entries", srv.handleEntries)
	mux.HandleFunc("/api/v1/trial-balance", srv.handleTrialBalance)
	mux.HandleFunc("/api/v1/events", srv.handleEvents)

	addr := ":" + cfg.HTTPPort
	log.Printf("Starting Standalone Accounting Service HTTP Daemon on %s", addr)
	return http.ListenAndServe(addr, mux)
}

func (s *Server) handleEntries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var entry ledger.JournalEntry
	if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	posted, err := s.engine.Ledger().PostEntry(r.Context(), entry)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
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
	companyID := types.CompanyID(uuid.MustParse(companyIDStr))
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
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	entry, err := s.engine.Events().ProcessEvent(r.Context(), ev)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(entry)
}
