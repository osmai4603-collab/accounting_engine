package main

import (
	"log"
	"net"

	"accounting_engine/pkg/sdk"
	"google.golang.org/grpc"
)

type GRPCServer struct {
	engine sdk.Engine
}

func StartGRPCServer(cfg Config, engine sdk.Engine) error {
	addr := ":" + cfg.GRPCPort
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	grpcServer := grpc.NewServer()
	log.Printf("Starting Standalone Accounting Service gRPC Daemon on %s", addr)
	return grpcServer.Serve(listener)
}
