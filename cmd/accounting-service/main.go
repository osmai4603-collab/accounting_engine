package main

import (
	"context"
	"log"

	"accounting_engine/pkg/sdk"
	"accounting_engine/storage/memory"
)

func main() {
	cfg := LoadConfig()
	ctx := context.Background()

	store := memory.New()
	engine, err := sdk.NewBuilder(
		sdk.WithCoreStorage(store),
		sdk.WithReporting(store),
		sdk.WithGovernance(store),
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
