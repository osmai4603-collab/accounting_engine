package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/legacy"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
)

func main() {
	if err := run(); err != nil {
		log.Printf("Accounting service exited with error: %v", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("configuration failure: %w", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(signalCtx)
	defer cancel()

	var coreStore legacy.CoreLedgerStore
	var reportingStore storage.ReportingStore
	var governanceStore storage.GovernanceStore
	var closeStore func() error
	storageHealth := func(healthCtx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return healthCtx.Err()
	}

	if cfg.IsInMemory() {
		memStore := memory.New()
		coreStore = memStore
		reportingStore = memStore
		governanceStore = memStore
		closeStore = func() error { return nil }
		log.Printf("Starting service in %s environment with in-memory storage", cfg.AppEnv)
	} else {
		pgDriver, err := postgres.New(ctx, cfg.Database)
		if err != nil {
			return fmt.Errorf("connect to PostgreSQL (%s): %w", SanitizeDSN(cfg.Database), err)
		}
		coreStore = pgDriver
		reportingStore = pgDriver
		governanceStore = pgDriver
		closeStore = func() error {
			pgDriver.Close()
			return nil
		}
		storageHealth = func(healthCtx context.Context) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			pingCtx, pingCancel := context.WithTimeout(healthCtx, 2*time.Second)
			defer pingCancel()
			if err := pgDriver.Pool().Ping(pingCtx); err != nil {
				return fmt.Errorf("storage ping failed: %w", err)
			}
			if err := pgDriver.ValidateSchema(pingCtx); err != nil {
				return fmt.Errorf("storage schema validation failed: %w", err)
			}
			return nil
		}
		log.Printf("Starting service in %s environment with PostgreSQL storage (%s)", cfg.AppEnv, SanitizeDSN(cfg.Database))
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
		httpErrors <- RunServerContext(ctx, cfg, engine, storageHealth)
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
