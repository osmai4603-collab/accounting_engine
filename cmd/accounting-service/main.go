package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"

	"accounting_engine/pkg/sdk"
	"accounting_engine/pkg/spi/storage"
	"accounting_engine/storage/memory"
	"accounting_engine/storage/postgres"
)

func main() {
	if err := run(); err != nil {
		log.Printf("Accounting service exited with error: %v", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := LoadConfig()
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(signalCtx)
	defer cancel()

	var coreStore storage.CoreLedgerStore
	var reportingStore storage.ReportingStore
	var governanceStore storage.GovernanceStore
	var closeStore func() error

	if cfg.Database == "memory" || cfg.Database == "" {
		memStore := memory.New()
		coreStore = memStore
		reportingStore = memStore
		governanceStore = memStore
		closeStore = func() error { return nil }
		log.Println("Using In-Memory storage backend")
	} else {
		pgDriver, err := postgres.New(ctx, cfg.Database)
		if err != nil {
			return err
		}
		coreStore = pgDriver
		reportingStore = pgDriver
		governanceStore = pgDriver
		closeStore = func() error {
			pgDriver.Close()
			return nil
		}
		log.Println("Using PostgreSQL storage backend")
	}
	defer func() {
		if err := closeStore(); err != nil {
			log.Printf("Error closing storage backend: %v", err)
		}
	}()

	engine, err := sdk.NewBuilder(
		sdk.WithCoreStorage(coreStore),
		sdk.WithReporting(reportingStore),
		sdk.WithGovernance(governanceStore),
	).Build(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err := engine.Close(); err != nil {
			log.Printf("Error closing accounting engine: %v", err)
		}
	}()

	grpcErrors := make(chan error, 1)
	go func() {
		grpcErrors <- RunGRPCServer(ctx, cfg, engine)
	}()

	httpErrors := make(chan error, 1)
	go func() {
		httpErrors <- RunServerContext(ctx, cfg, engine)
	}()

	var httpErr, grpcErr error
	select {
	case httpErr = <-httpErrors:
		cancel()
		grpcErr = <-grpcErrors
	case grpcErr = <-grpcErrors:
		cancel()
		httpErr = <-httpErrors
	case <-signalCtx.Done():
		cancel()
		httpErr = <-httpErrors
		grpcErr = <-grpcErrors
	}
	return errors.Join(httpErr, grpcErr)
}
