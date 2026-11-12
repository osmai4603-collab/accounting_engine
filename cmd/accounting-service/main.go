package main

import (
	"context"
	"log"

	"accounting_engine/pkg/sdk"
	"accounting_engine/pkg/spi/storage"
)

type memoryCoreStore struct {
	storage.CoreLedgerStore
}

func main() {
	cfg := LoadConfig()
	ctx := context.Background()

	engine, err := sdk.NewBuilder(
		sdk.WithCoreStorage(&memoryCoreStore{}),
	).Build(ctx)
	if err != nil {
		log.Fatalf("Failed to build accounting engine: %v", err)
	}
	defer engine.Close()

	if err := RunServer(cfg, engine); err != nil {
		log.Fatalf("Accounting service exited with error: %v", err)
	}
}
