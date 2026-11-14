package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Environment denotes the operational environment tier.
type Environment string

const (
	EnvProduction  Environment = "production"
	EnvDevelopment Environment = "development"
	EnvTest        Environment = "test"
)

var (
	ErrProductionMissingDSN   = errors.New("production environment requires DATABASE_DSN to be set (fail-closed)")
	ErrProductionMemoryStore  = errors.New("in-memory storage is strictly prohibited in production")
	ErrUnconfiguredStorage    = errors.New("DATABASE_DSN is not configured; specify a PostgreSQL DSN or explicitly select in-memory storage (DATABASE_DSN=memory or ALLOW_IN_MEMORY=true)")
	ErrInvalidEnvironment     = errors.New("invalid APP_ENV: must be production, development, or test")
	ErrInvalidPort            = errors.New("invalid port configuration")
	ErrInvalidPostgresDSN     = errors.New("invalid PostgreSQL DSN: must begin with postgres:// or postgresql://")
)

var dsnKVPasswordRegex = regexp.MustCompile(`(?i)(password\s*=\s*)([^\s]+)`)

// Config contains runtime service settings.
type Config struct {
	AppEnv         Environment
	GRPCPort       string
	HTTPPort       string
	Database       string // DSN or "memory"
	RequireTLS     bool
	DBMaxConns     int
	DBMinConns     int
	DBMaxConnIdle  time.Duration
	DBMaxConnLife  time.Duration
	AllowInMemory  bool
}

// ParseEnvironment normalizes environment strings.
func ParseEnvironment(raw string) (Environment, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	switch trimmed {
	case "production", "prod":
		return EnvProduction, nil
	case "development", "dev", "local", "":
		return EnvDevelopment, nil
	case "test", "testing":
		return EnvTest, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidEnvironment, raw)
	}
}

// SanitizeDSN redacts secrets (passwords, tokens) from DSN strings to prevent leaking them in logs or errors.
func SanitizeDSN(raw string) string {
	if raw == "" || raw == "memory" {
		return raw
	}
	// Try parsing as standard URL
	if parsed, err := url.Parse(raw); err == nil && parsed.Scheme != "" {
		if parsed.User != nil {
			if _, hasPassword := parsed.User.Password(); hasPassword {
				parsed.User = url.UserPassword(parsed.User.Username(), "REDACTED")
			}
			return parsed.String()
		}
		return raw
	}
	// Fallback for key-value format (libpq connection strings)
	return dsnKVPasswordRegex.ReplaceAllString(raw, "${1}REDACTED")
}

// IsInMemory returns true if storage backend is explicitly configured for memory.
func (c Config) IsInMemory() bool {
	return c.Database == "memory" || (c.Database == "" && c.AllowInMemory)
}

// Validate checks that configuration adheres to fail-closed rules.
func (c Config) Validate() error {
	// Validate ports
	if c.HTTPPort == "" {
		return fmt.Errorf("%w: HTTPPort cannot be empty", ErrInvalidPort)
	}
	if port, err := strconv.Atoi(c.HTTPPort); err != nil || port <= 0 || port > 65535 {
		return fmt.Errorf("%w: HTTPPort %q must be between 1 and 65535", ErrInvalidPort, c.HTTPPort)
	}
	if c.GRPCPort == "" {
		return fmt.Errorf("%w: GRPCPort cannot be empty", ErrInvalidPort)
	}
	if port, err := strconv.Atoi(c.GRPCPort); err != nil || port <= 0 || port > 65535 {
		return fmt.Errorf("%w: GRPCPort %q must be between 1 and 65535", ErrInvalidPort, c.GRPCPort)
	}

	// Validate environment & storage
	switch c.AppEnv {
	case EnvProduction:
		if c.Database == "" {
			return ErrProductionMissingDSN
		}
		if c.Database == "memory" || c.AllowInMemory {
			return ErrProductionMemoryStore
		}
		if !strings.HasPrefix(c.Database, "postgres://") && !strings.HasPrefix(c.Database, "postgresql://") {
			return fmt.Errorf("%w: received %q", ErrInvalidPostgresDSN, SanitizeDSN(c.Database))
		}
	case EnvDevelopment, EnvTest:
		if c.Database == "" && !c.AllowInMemory {
			return ErrUnconfiguredStorage
		}
		if c.Database != "" && c.Database != "memory" {
			if !strings.HasPrefix(c.Database, "postgres://") && !strings.HasPrefix(c.Database, "postgresql://") {
				return fmt.Errorf("%w: received %q", ErrInvalidPostgresDSN, SanitizeDSN(c.Database))
			}
		}
	default:
		return fmt.Errorf("%w: %q", ErrInvalidEnvironment, c.AppEnv)
	}

	return nil
}

// LoadConfig loads environment variables and applies fail-closed validation.
func LoadConfig() (Config, error) {
	rawEnv := os.Getenv("APP_ENV")
	if rawEnv == "" {
		rawEnv = os.Getenv("ENV")
	}
	if rawEnv == "" {
		rawEnv = os.Getenv("ENVIRONMENT")
	}

	appEnv, err := ParseEnvironment(rawEnv)
	if err != nil {
		return Config{}, err
	}

	grpcPort := os.Getenv("GRPC_PORT")
	if grpcPort == "" {
		grpcPort = "50051"
	}

	httpPort := os.Getenv("HTTP_PORT")
	if httpPort == "" {
		httpPort = "8080"
	}

	db := strings.TrimSpace(os.Getenv("DATABASE_DSN"))

	allowMemory := false
	if rawAllow := strings.ToLower(os.Getenv("ALLOW_IN_MEMORY")); rawAllow == "true" || rawAllow == "1" {
		allowMemory = true
	}

	requireTLS := false
	if rawTLS := strings.ToLower(os.Getenv("REQUIRE_TLS")); rawTLS == "true" || rawTLS == "1" {
		requireTLS = true
	}

	maxConns := 0
	if rawMax := os.Getenv("DB_MAX_CONNS"); rawMax != "" {
		if val, err := strconv.Atoi(rawMax); err == nil && val > 0 {
			maxConns = val
		}
	}

	minConns := 0
	if rawMin := os.Getenv("DB_MIN_CONNS"); rawMin != "" {
		if val, err := strconv.Atoi(rawMin); err == nil && val >= 0 {
			minConns = val
		}
	}

	cfg := Config{
		AppEnv:        appEnv,
		GRPCPort:      grpcPort,
		HTTPPort:      httpPort,
		Database:      db,
		RequireTLS:    requireTLS,
		DBMaxConns:    maxConns,
		DBMinConns:    minConns,
		AllowInMemory: allowMemory,
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}
