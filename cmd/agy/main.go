package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"accounting_engine/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 2 || args[0] != "db" || args[1] != "migrate" {
		return errors.New("usage: agy db migrate [up|down] [--steps N]")
	}

	direction := "up"
	args = args[2:]
	if len(args) > 0 && (args[0] == "up" || args[0] == "down") {
		direction = args[0]
		args = args[1:]
	}

	steps := 1
	if len(args) > 0 {
		if len(args) != 2 || args[0] != "--steps" {
			return errors.New("usage: agy db migrate [up|down] [--steps N]")
		}
		parsedSteps, err := strconv.Atoi(args[1])
		if err != nil || parsedSteps < 1 {
			return errors.New("migration --steps must be a positive integer")
		}
		steps = parsedSteps
	}

	connectionString := os.Getenv("DATABASE_URL")
	if connectionString == "" {
		return errors.New("DATABASE_URL must be set")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, connectionString)
	if err != nil {
		return fmt.Errorf("open database connection: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	switch direction {
	case "up":
		if err := postgres.EnsureSchema(ctx, pool); err != nil {
			return fmt.Errorf("apply database migrations: %w", err)
		}
	case "down":
		if err := postgres.MigrateDown(ctx, pool, steps); err != nil {
			return fmt.Errorf("roll back database migrations: %w", err)
		}
	}
	return nil
}
