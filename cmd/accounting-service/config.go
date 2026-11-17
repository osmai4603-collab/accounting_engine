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

	"github.com/osmai4603-collab/accounting_engine/internal/ratelimit"
	"github.com/osmai4603-collab/accounting_engine/internal/serviceauth"
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
	ErrProductionAuthRequired = errors.New("production environment requires an explicit secure AUTH_MODE (gateway-hmac, jwt-hs256, or jwt-rs256); anonymous HTTP/gRPC access is prohibited")
	ErrInvalidAuthConfig      = errors.New("invalid service authentication configuration")
	ErrInvalidRateLimitConfig = errors.New("invalid rate limit configuration")
)

// defaultAuthClockSkew bounds accepted credential clock drift.
const defaultAuthClockSkew = time.Minute

var dsnKVPasswordRegex = regexp.MustCompile(`(?i)(password\s*=\s*)([^\s]+)`)

// Config contains runtime service settings.
type Config struct {
	AppEnv        Environment
	GRPCPort      string
	HTTPPort      string
	Database      string // DSN or "memory"
	RequireTLS    bool
	DBMaxConns    int
	DBMinConns    int
	DBMaxConnIdle time.Duration
	DBMaxConnLife time.Duration
	AllowInMemory bool
	RateLimit     ratelimit.Config

	// Service boundary authentication. Embedded Library deployments never read
	// these fields; they apply only to the optional Standalone Service.
	AuthMode         string
	AuthSecret       string
	AuthJWTPublicKey string
	AuthJWTIssuer    string
	AuthJWTAudience  string
	AuthClockSkew    time.Duration
}

// AuthEnabled reports whether a transport authentication boundary is selected.
func (c Config) AuthEnabled() bool {
	return serviceauth.NormalizeMode(c.AuthMode) != serviceauth.ModeNone
}

// authConfig maps service settings onto the transport-neutral verifier config.
func (c Config) authConfig() serviceauth.Config {
	return serviceauth.Config{
		Mode:            c.AuthMode,
		Secret:          c.AuthSecret,
		JWTPublicKeyPEM: c.AuthJWTPublicKey,
		JWTIssuer:       c.AuthJWTIssuer,
		JWTAudience:     c.AuthJWTAudience,
		ClockSkew:       c.AuthClockSkew,
	}
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

	if err := c.validateAuth(); err != nil {
		return err
	}
	if err := c.rateLimitConfig().Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRateLimitConfig, err)
	}

	return nil
}

func (c Config) rateLimitConfig() ratelimit.Config {
	if c.RateLimit == (ratelimit.Config{}) {
		return ratelimit.DefaultConfig()
	}
	return c.RateLimit
}

// validateAuth enforces fail-closed service boundary authentication. Embedded
// Library deployments do not build a Config, so they are unaffected.
func (c Config) validateAuth() error {
	mode := serviceauth.NormalizeMode(c.AuthMode)
	if c.AppEnv == EnvProduction && mode == serviceauth.ModeNone {
		return ErrProductionAuthRequired
	}
	if _, err := serviceauth.NewVerifier(c.authConfig()); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAuthConfig, err)
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

	authMode := serviceauth.NormalizeMode(os.Getenv("AUTH_MODE"))

	authSecret := os.Getenv("AUTH_SECRET")

	authPublicKey := strings.TrimSpace(os.Getenv("AUTH_JWT_PUBLIC_KEY"))
	if authPublicKey == "" {
		if keyFile := strings.TrimSpace(os.Getenv("AUTH_JWT_PUBLIC_KEY_FILE")); keyFile != "" {
			contents, err := os.ReadFile(keyFile)
			if err != nil {
				return Config{}, fmt.Errorf("%w: cannot read AUTH_JWT_PUBLIC_KEY_FILE", ErrInvalidAuthConfig)
			}
			authPublicKey = strings.TrimSpace(string(contents))
		}
	}

	authClockSkew, err := parseAuthClockSkew(os.Getenv("AUTH_CLOCK_SKEW"))
	if err != nil {
		return Config{}, err
	}
	rateLimit, err := parseRateLimitConfig()
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		AppEnv:           appEnv,
		GRPCPort:         grpcPort,
		HTTPPort:         httpPort,
		Database:         db,
		RequireTLS:       requireTLS,
		DBMaxConns:       maxConns,
		DBMinConns:       minConns,
		AllowInMemory:    allowMemory,
		AuthMode:         authMode,
		AuthSecret:       authSecret,
		AuthJWTPublicKey: authPublicKey,
		AuthJWTIssuer:    strings.TrimSpace(os.Getenv("AUTH_JWT_ISSUER")),
		AuthJWTAudience:  strings.TrimSpace(os.Getenv("AUTH_JWT_AUDIENCE")),
		AuthClockSkew:    authClockSkew,
		RateLimit:        rateLimit,
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func parseRateLimitConfig() (ratelimit.Config, error) {
	config := ratelimit.DefaultConfig()
	if raw, exists := os.LookupEnv("RATE_LIMIT_ENABLED"); exists {
		enabled, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return ratelimit.Config{}, fmt.Errorf("%w: RATE_LIMIT_ENABLED must be a boolean", ErrInvalidRateLimitConfig)
		}
		config.Enabled = enabled
	}
	for _, setting := range []struct {
		name  string
		value *int
	}{
		{name: "RATE_LIMIT_READ_REQUESTS", value: &config.ReadLimit},
		{name: "RATE_LIMIT_WRITE_REQUESTS", value: &config.WriteLimit},
		{name: "RATE_LIMIT_CLIENT_READ_REQUESTS", value: &config.ClientReadLimit},
		{name: "RATE_LIMIT_CLIENT_WRITE_REQUESTS", value: &config.ClientWriteLimit},
		{name: "RATE_LIMIT_MAX_KEYS", value: &config.MaxKeys},
	} {
		raw := strings.TrimSpace(os.Getenv(setting.name))
		if raw == "" {
			continue
		}
		value, err := strconv.Atoi(raw)
		if err != nil {
			return ratelimit.Config{}, fmt.Errorf("%w: %s must be a positive integer", ErrInvalidRateLimitConfig, setting.name)
		}
		*setting.value = value
	}
	if raw := strings.TrimSpace(os.Getenv("RATE_LIMIT_WINDOW")); raw != "" {
		window, err := time.ParseDuration(raw)
		if err != nil {
			return ratelimit.Config{}, fmt.Errorf("%w: RATE_LIMIT_WINDOW must be a Go duration (for example 1m)", ErrInvalidRateLimitConfig)
		}
		config.Window = window
	}
	if err := config.Validate(); err != nil {
		return ratelimit.Config{}, fmt.Errorf("%w: %v", ErrInvalidRateLimitConfig, err)
	}
	return config, nil
}

func parseAuthClockSkew(raw string) (time.Duration, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultAuthClockSkew, nil
	}
	if duration, err := time.ParseDuration(trimmed); err == nil {
		if duration < 0 {
			return 0, fmt.Errorf("%w: AUTH_CLOCK_SKEW cannot be negative", ErrInvalidAuthConfig)
		}
		return duration, nil
	}
	if seconds, err := strconv.Atoi(trimmed); err == nil {
		if seconds < 0 {
			return 0, fmt.Errorf("%w: AUTH_CLOCK_SKEW cannot be negative", ErrInvalidAuthConfig)
		}
		return time.Duration(seconds) * time.Second, nil
	}
	return 0, fmt.Errorf("%w: AUTH_CLOCK_SKEW must be a Go duration (for example 60s) or a number of seconds", ErrInvalidAuthConfig)
}
