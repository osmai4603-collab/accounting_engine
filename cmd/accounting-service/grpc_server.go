package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	"accounting_engine/pkg/sdk"
	"google.golang.org/grpc"
)

type GRPCServer struct {
	engine sdk.Engine
}

func StartGRPCServer(cfg Config, engine sdk.Engine) error {
	return RunGRPCServer(context.Background(), cfg, engine)
}

func RunGRPCServer(ctx context.Context, cfg Config, engine sdk.Engine) error {
	addr := ":" + cfg.GRPCPort
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	grpcServer := grpc.NewServer()
	log.Printf("Starting Standalone Accounting Service gRPC Daemon on %s", addr)

	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- grpcServer.Serve(listener)
	}()

	select {
	case err := <-serverErrors:
		return err
	case <-ctx.Done():
	}

	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()
	timer := time.NewTimer(serverShutdownTimeout)
	defer timer.Stop()
	select {
	case <-stopped:
		return nil
	case <-timer.C:
		grpcServer.Stop()
		<-stopped
		return fmt.Errorf("graceful gRPC shutdown exceeded %s", serverShutdownTimeout)
	}
}
