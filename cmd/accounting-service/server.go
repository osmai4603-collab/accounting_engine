package main

import (
	"encoding/json"
	"log"
	"net/http"

	"accounting_engine/pkg/sdk"
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

	addr := ":" + cfg.HTTPPort
	log.Printf("Starting Standalone Accounting Service HTTP Daemon on %s", addr)
	return http.ListenAndServe(addr, mux)
}

func (s *Server) handleEntries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":   "success",
		"entry_id": "entry-stub-001",
	})
}

func (s *Server) handleTrialBalance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"company_id":  r.URL.Query().Get("company_id"),
		"is_balanced": true,
		"lines":       []any{},
	})
}
