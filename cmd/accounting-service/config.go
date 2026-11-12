package main

import (
	"os"
)

type Config struct {
	GRPCPort string
	HTTPPort string
	Database string
}

func LoadConfig() Config {
	grpcPort := os.Getenv("GRPC_PORT")
	if grpcPort == "" {
		grpcPort = "50051"
	}
	httpPort := os.Getenv("HTTP_PORT")
	if httpPort == "" {
		httpPort = "8080"
	}
	db := os.Getenv("DATABASE_DSN")
	if db == "" {
		db = "memory"
	}
	return Config{
		GRPCPort: grpcPort,
		HTTPPort: httpPort,
		Database: db,
	}
}
