package main

import (
	"accounting_engine/pkg/sdk"
)

type GRPCServer struct {
	engine sdk.Engine
}

func StartGRPCServer(cfg Config, engine sdk.Engine) error {
	// Standalone REST server is active; gRPC stub placeholder
	return nil
}
