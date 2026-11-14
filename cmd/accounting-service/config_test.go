package main

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func setEnvHelper(t *testing.T, key, value string) {
	t.Helper()
	prev, exists := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("setenv %s: %v", key, err)
	}
	t.Cleanup(func() {
		if exists {
			_ = os.Setenv(key, prev)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func unsetEnvHelper(t *testing.T, key string) {
	t.Helper()
	prev, exists := os.LookupEnv(key)
	_ = os.Unsetenv(key)
	t.Cleanup(func() {
		if exists {
			_ = os.Setenv(key, prev)
		}
	})
}

func clearEnv(t *testing.T) {
	for _, k := range []string{"APP_ENV", "ENV", "ENVIRONMENT", "DATABASE_DSN", "ALLOW_IN_MEMORY", "GRPC_PORT", "HTTP_PORT"} {
		unsetEnvHelper(t, k)
	}
}

func TestProductionMissingDSNFailsClosed(t *testing.T) {
	clearEnv(t)
	setEnvHelper(t, "APP_ENV", "production")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for production without DATABASE_DSN, got nil")
	}
	if !errors.Is(err, ErrProductionMissingDSN) {
		t.Fatalf("expected ErrProductionMissingDSN, got: %v", err)
	}
}

func TestProductionMemoryProhibited(t *testing.T) {
	clearEnv(t)
	setEnvHelper(t, "APP_ENV", "production")
	setEnvHelper(t, "DATABASE_DSN", "memory")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for production with memory DSN, got nil")
	}
	if !errors.Is(err, ErrProductionMemoryStore) {
		t.Fatalf("expected ErrProductionMemoryStore, got: %v", err)
	}
}

func TestProductionMemoryViaFlagProhibited(t *testing.T) {
	clearEnv(t)
	setEnvHelper(t, "APP_ENV", "production")
	setEnvHelper(t, "ALLOW_IN_MEMORY", "true")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for production with ALLOW_IN_MEMORY, got nil")
	}
	if !errors.Is(err, ErrProductionMissingDSN) && !errors.Is(err, ErrProductionMemoryStore) {
		t.Fatalf("expected fail-closed error, got: %v", err)
	}
}

func TestProductionValidPostgresDSN(t *testing.T) {
	clearEnv(t)
	setEnvHelper(t, "APP_ENV", "production")
	setEnvHelper(t, "DATABASE_DSN", "postgres://user:secret@localhost:5432/ledger?sslmode=require")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("expected success with valid postgres DSN, got: %v", err)
	}
	if cfg.AppEnv != EnvProduction {
		t.Fatalf("expected AppEnv=production, got: %s", cfg.AppEnv)
	}
	if cfg.IsInMemory() {
		t.Fatal("expected IsInMemory() == false in production")
	}
}

func TestProductionInvalidPostgresScheme(t *testing.T) {
	clearEnv(t)
	setEnvHelper(t, "APP_ENV", "production")
	setEnvHelper(t, "DATABASE_DSN", "mysql://user:pass@localhost:3306/db")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for non-postgres DSN in production")
	}
	if !errors.Is(err, ErrInvalidPostgresDSN) {
		t.Fatalf("expected ErrInvalidPostgresDSN, got: %v", err)
	}
}

func TestDevelopmentExplicitMemoryAllowed(t *testing.T) {
	clearEnv(t)
	setEnvHelper(t, "APP_ENV", "development")
	setEnvHelper(t, "DATABASE_DSN", "memory")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("expected success with explicit memory DSN in dev, got: %v", err)
	}
	if !cfg.IsInMemory() {
		t.Fatal("expected IsInMemory() == true")
	}
}

func TestDevelopmentAllowInMemoryFlagAllowed(t *testing.T) {
	clearEnv(t)
	setEnvHelper(t, "APP_ENV", "development")
	setEnvHelper(t, "ALLOW_IN_MEMORY", "1")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("expected success with ALLOW_IN_MEMORY=1 in dev, got: %v", err)
	}
	if !cfg.IsInMemory() {
		t.Fatal("expected IsInMemory() == true")
	}
}

func TestDevelopmentUnsetDSNFailsClosed(t *testing.T) {
	clearEnv(t)
	setEnvHelper(t, "APP_ENV", "development")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for unconfigured storage in dev (no accidental memory fallback), got nil")
	}
	if !errors.Is(err, ErrUnconfiguredStorage) {
		t.Fatalf("expected ErrUnconfiguredStorage, got: %v", err)
	}
}

func TestTestEnvironmentExplicitMemoryAllowed(t *testing.T) {
	clearEnv(t)
	setEnvHelper(t, "APP_ENV", "test")
	setEnvHelper(t, "DATABASE_DSN", "memory")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("expected success with explicit memory in test, got: %v", err)
	}
	if !cfg.IsInMemory() {
		t.Fatal("expected IsInMemory() == true")
	}
}

func TestSanitizeDSNRedactsSecrets(t *testing.T) {
	rawURL := "postgres://admin:super_secret_pw@db.prod.internal:5432/ledger?sslmode=verify-full"
	sanitized := SanitizeDSN(rawURL)

	if strings.Contains(sanitized, "super_secret_pw") {
		t.Fatalf("SanitizeDSN leaked secret password: %s", sanitized)
	}
	if !strings.Contains(sanitized, "REDACTED") {
		t.Fatalf("SanitizeDSN should contain REDACTED: %s", sanitized)
	}
	if !strings.Contains(sanitized, "admin") || !strings.Contains(sanitized, "db.prod.internal") {
		t.Fatalf("SanitizeDSN should preserve non-sensitive connection coordinates: %s", sanitized)
	}

	rawKV := "host=localhost user=osm password=topsecret dbname=acct"
	sanitizedKV := SanitizeDSN(rawKV)
	if strings.Contains(sanitizedKV, "topsecret") {
		t.Fatalf("SanitizeDSN leaked secret in KV string: %s", sanitizedKV)
	}
	if !strings.Contains(sanitizedKV, "password=REDACTED") {
		t.Fatalf("SanitizeDSN should replace password with REDACTED in KV: %s", sanitizedKV)
	}

	if SanitizeDSN("memory") != "memory" {
		t.Fatalf("SanitizeDSN(memory) should be memory")
	}
	if SanitizeDSN("") != "" {
		t.Fatalf("SanitizeDSN('') should be ''")
	}
}

func TestInvalidEnvironmentFails(t *testing.T) {
	clearEnv(t)
	setEnvHelper(t, "APP_ENV", "staging_unknown")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for invalid APP_ENV")
	}
	if !errors.Is(err, ErrInvalidEnvironment) {
		t.Fatalf("expected ErrInvalidEnvironment, got: %v", err)
	}
}

func TestInvalidPortConfigurations(t *testing.T) {
	clearEnv(t)
	setEnvHelper(t, "APP_ENV", "test")
	setEnvHelper(t, "DATABASE_DSN", "memory")

	// Invalid HTTP port
	setEnvHelper(t, "HTTP_PORT", "not-a-number")
	if _, err := LoadConfig(); !errors.Is(err, ErrInvalidPort) {
		t.Fatalf("expected ErrInvalidPort for non-numeric HTTP_PORT, got %v", err)
	}

	setEnvHelper(t, "HTTP_PORT", "999999")
	if _, err := LoadConfig(); !errors.Is(err, ErrInvalidPort) {
		t.Fatalf("expected ErrInvalidPort for out-of-range HTTP_PORT, got %v", err)
	}

	setEnvHelper(t, "HTTP_PORT", "8080")
	setEnvHelper(t, "GRPC_PORT", "-1")
	if _, err := LoadConfig(); !errors.Is(err, ErrInvalidPort) {
		t.Fatalf("expected ErrInvalidPort for negative GRPC_PORT, got %v", err)
	}
}

