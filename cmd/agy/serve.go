package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres"
	accountingengine "github.com/osmai4603-collab/accounting_engine/internal/legacyengine"
	"github.com/osmai4603-collab/accounting_engine/internal/observability"
)

// runServe exposes the engine's Prometheus metrics and a liveness endpoint. A
// DATABASE_URL is optional: when present the engine is constructed so its
// in-process posting/approval/reconciliation metrics are served; otherwise a
// standalone metrics registry is exposed.
func runServe(args []string) error {
	addr, err := parseServeArgs(args)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := observability.NewLogger(os.Stderr, nil)
	unit := observability.NewUnit(observability.Config{Logger: logger})

	var driver *postgres.Driver
	if connectionString := strings.TrimSpace(os.Getenv("DATABASE_URL")); connectionString != "" {
		driver, err = postgres.New(ctx, connectionString)
		if err != nil {
			return fmt.Errorf("connect to accounting postgres: %w", err)
		}
		defer driver.Close()
		engine, err := accountingengine.New(driver, accountingengine.Config{
			Observability: &accountingengine.ObservabilityConfig{
				Logger:  logger,
				Metrics: unit.Metrics,
			},
		})
		if err != nil {
			return fmt.Errorf("initialize accounting engine: %w", err)
		}
		unit = engine.Telemetry
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", unit.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
	})

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown metrics server: %w", err)
	}
	return nil
}

func parseServeArgs(args []string) (string, error) {
	addr := os.Getenv("AGY_HTTP_ADDR")
	if strings.TrimSpace(addr) == "" {
		addr = ":8080"
	}
	for index := 0; index < len(args); {
		if args[index] != "--addr" || index+1 >= len(args) || strings.TrimSpace(args[index+1]) == "" {
			return "", errors.New("usage: agy serve [--addr HOST:PORT]")
		}
		addr = strings.TrimSpace(args[index+1])
		index += 2
	}
	return addr, nil
}
