package main

import (
	"context"
	"log"

	"accounting_engine/pkg/sdk"
	"accounting_engine/pkg/spi/storage"
	"accounting_engine/storage/memory"
	"accounting_engine/storage/postgres"
)

func main() {
	cfg := LoadConfig()
	ctx := context.Background()

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
			log.Fatalf("Failed to connect to PostgreSQL at %s: %v", cfg.Database, err)
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
		log.Fatalf("Failed to build accounting engine: %v", err)
	}
	defer engine.Close()

	go func() {
		if err := StartGRPCServer(cfg, engine); err != nil {
			log.Printf("gRPC server error: %v", err)
		}
	}()

	if err := RunServer(cfg, engine); err != nil {
		log.Fatalf("Accounting service exited with error: %v", err)
	}
}
