package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"accounting_engine/internal/ifrsautomation"
	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
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
	if len(args) < 2 {
		return errors.New("usage: agy [db migrate [up|down] [--steps N] | ifrs [daemon|run] [--interval DURATION]]")
	}

	switch args[0] {
	case "db":
		return runDB(args[1:])
	case "ifrs":
		return runIFRS(args[1:])
	default:
		return errors.New("usage: agy [db migrate [up|down] [--steps N] | ifrs [daemon|run] [--interval DURATION]]")
	}
}

func runIFRS(args []string) error {
	if len(args) == 0 || (args[0] != "daemon" && args[0] != "run") {
		return errors.New("usage: agy ifrs [daemon|run] [--interval DURATION]")
	}

	interval := time.Hour
	if len(args) >= 3 && args[1] == "--interval" {
		d, err := time.ParseDuration(args[2])
		if err != nil {
			return fmt.Errorf("invalid daemon interval: %w", err)
		}
		interval = d
	}

	connectionString := os.Getenv("DATABASE_URL")
	if connectionString == "" {
		return errors.New("DATABASE_URL must be set")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	driver, err := postgres.New(ctx, connectionString)
	if err != nil {
		return fmt.Errorf("connect to accounting postgres: %w", err)
	}
	defer driver.Close()

	masterData := masterdata.NewService(masterdata.NewRepositoryAdapter(driver))
	postingEngine := posting.NewEngine(driver, masterData)
	postingEngine.SetFinancialDimensionsEngine(posting.NewFinancialDimensionsEngine(driver))
	runner, err := ifrsautomation.NewRunner(postingEngine, driver, "agy-cli")
	if err != nil {
		return fmt.Errorf("initialize ifrs automation runner: %w", err)
	}

	scheduler, err := ifrsautomation.NewScheduler(runner)
	if err != nil {
		return fmt.Errorf("initialize ifrs scheduler: %w", err)
	}

	if args[0] == "daemon" {
		return scheduler.StartDaemon(ctx, interval)
	}

	// Immediate run for previous month
	now := time.Now().UTC()
	prev := now.AddDate(0, -1, 0)
	period, err := ifrsautomation.Month(prev.Year(), prev.Month())
	if err != nil {
		return err
	}
	_, err = scheduler.RunPeriod(ctx, period)
	return err
}

func runDB(args []string) error {
	if len(args) < 1 || args[0] != "migrate" {
		return errors.New("usage: agy db migrate [up|down] [--steps N]")
	}
	args = args[1:]

	direction := "up"
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
