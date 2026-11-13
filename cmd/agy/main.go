package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres"
	"github.com/osmai4603-collab/accounting_engine/internal/ifrsautomation"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

const commandUsage = "usage: agy [db migrate [up|down] [--steps N] | ifrs [daemon|run] [--interval DURATION] [--config PATH] | serve [--addr HOST:PORT]]"

func run(args []string) error {
	if len(args) < 1 {
		return errors.New(commandUsage)
	}

	switch args[0] {
	case "db":
		return runDB(args[1:])
	case "ifrs":
		return runIFRS(args[1:])
	case "serve":
		return runServe(args[1:])
	default:
		return errors.New(commandUsage)
	}
}

func runIFRS(args []string) error {
	mode, interval, configPath, err := parseIFRSArgs(args)
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	config, configured, err := loadAutomationConfig(configPath)
	if err != nil {
		return err
	}
	if !configured || len(config.Companies) == 0 {
		logger.Warn("IFRS automation skipped: configuration is not configured",
			"config_path", configPath)
		return nil
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

	jobs, err := ifrsautomation.BuildStandardJobs(config, driver)
	if err != nil {
		return fmt.Errorf("build standard IFRS automation jobs: %w", err)
	}
	scheduler, err := ifrsautomation.NewSchedulerWithLogger(runner, logger, jobs...)
	if err != nil {
		return fmt.Errorf("initialize ifrs scheduler: %w", err)
	}

	return runIFRSPeriod(ctx, scheduler, mode, interval, time.Now().UTC())
}

func runIFRSPeriod(
	ctx context.Context,
	scheduler *ifrsautomation.Scheduler,
	mode string,
	interval time.Duration,
	now time.Time,
) error {
	if mode == "daemon" {
		return scheduler.StartDaemon(ctx, interval)
	}
	prev := now.AddDate(0, -1, 0)
	period, err := ifrsautomation.Month(prev.Year(), prev.Month())
	if err != nil {
		return err
	}
	_, err = scheduler.RunPeriod(ctx, period)
	return err
}

func parseIFRSArgs(args []string) (mode string, interval time.Duration, configPath string, err error) {
	if len(args) == 0 || (args[0] != "daemon" && args[0] != "run") {
		return "", 0, "", errors.New("usage: agy ifrs [daemon|run] [--interval DURATION] [--config PATH]")
	}
	mode, interval = args[0], time.Hour
	configPath = os.Getenv("AGY_IFRS_CONFIG_PATH")
	for index := 1; index < len(args); {
		if index+1 >= len(args) {
			return "", 0, "", errors.New("usage: agy ifrs [daemon|run] [--interval DURATION] [--config PATH]")
		}
		switch args[index] {
		case "--interval":
			parsed, parseErr := time.ParseDuration(args[index+1])
			if parseErr != nil || parsed <= 0 {
				return "", 0, "", fmt.Errorf("invalid daemon interval %q", args[index+1])
			}
			interval = parsed
		case "--config":
			if strings.TrimSpace(args[index+1]) == "" {
				return "", 0, "", errors.New("IFRS config path must not be empty")
			}
			configPath = args[index+1]
		default:
			return "", 0, "", errors.New("usage: agy ifrs [daemon|run] [--interval DURATION] [--config PATH]")
		}
		index += 2
	}
	return mode, interval, configPath, nil
}

func loadAutomationConfig(path string) (ifrsautomation.AutomationConfig, bool, error) {
	if strings.TrimSpace(path) == "" {
		return ifrsautomation.AutomationConfig{}, false, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ifrsautomation.AutomationConfig{}, false, nil
	}
	if err != nil {
		return ifrsautomation.AutomationConfig{}, false, fmt.Errorf("read IFRS automation config: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return ifrsautomation.AutomationConfig{}, false, nil
	}
	var config ifrsautomation.AutomationConfig
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return ifrsautomation.AutomationConfig{}, false, fmt.Errorf("decode IFRS automation config: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return ifrsautomation.AutomationConfig{}, false, errors.New("decode IFRS automation config: multiple JSON values")
		}
		return ifrsautomation.AutomationConfig{}, false, fmt.Errorf("decode IFRS automation config: %w", err)
	}
	return config, true, nil
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
