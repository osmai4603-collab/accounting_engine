package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/serviceauth"
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
	for _, k := range []string{
		"APP_ENV", "ENV", "ENVIRONMENT", "DATABASE_DSN", "ALLOW_IN_MEMORY", "GRPC_PORT", "HTTP_PORT",
		"AUTH_MODE", "AUTH_SECRET", "AUTH_JWT_PUBLIC_KEY", "AUTH_JWT_PUBLIC_KEY_FILE",
		"AUTH_JWT_ISSUER", "AUTH_JWT_AUDIENCE", "AUTH_CLOCK_SKEW",
	} {
		unsetEnvHelper(t, k)
	}
}

const testAuthSecret = "0123456789abcdef0123456789abcdef"

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
	setEnvHelper(t, "AUTH_MODE", "gateway-hmac")
	setEnvHelper(t, "AUTH_SECRET", testAuthSecret)

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
	if !cfg.AuthEnabled() {
		t.Fatal("expected authentication boundary enabled in production")
	}
	if cfg.AuthMode != serviceauth.ModeGatewayHMAC {
		t.Fatalf("AuthMode = %q, want %q", cfg.AuthMode, serviceauth.ModeGatewayHMAC)
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

func productionStorageEnv(t *testing.T) {
	t.Helper()
	clearEnv(t)
	setEnvHelper(t, "APP_ENV", "production")
	setEnvHelper(t, "DATABASE_DSN", "postgres://user:secret@localhost:5432/ledger?sslmode=require")
}

func TestProductionDefaultAuthModeRejected(t *testing.T) {
	productionStorageEnv(t)

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected production to reject an unset AUTH_MODE")
	}
	if !errors.Is(err, ErrProductionAuthRequired) {
		t.Fatalf("expected ErrProductionAuthRequired, got: %v", err)
	}
}

func TestProductionExplicitNoneAuthModeRejected(t *testing.T) {
	productionStorageEnv(t)
	setEnvHelper(t, "AUTH_MODE", "none")

	_, err := LoadConfig()
	if !errors.Is(err, ErrProductionAuthRequired) {
		t.Fatalf("expected ErrProductionAuthRequired for AUTH_MODE=none, got: %v", err)
	}
}

func TestProductionAuthMissingMaterialRejected(t *testing.T) {
	productionStorageEnv(t)
	setEnvHelper(t, "AUTH_MODE", "gateway-hmac")

	_, err := LoadConfig()
	if !errors.Is(err, ErrInvalidAuthConfig) {
		t.Fatalf("expected ErrInvalidAuthConfig for missing AUTH_SECRET, got: %v", err)
	}
	assertSecretNotInError(t, err)
}

func TestProductionUnknownAuthModeRejected(t *testing.T) {
	productionStorageEnv(t)
	setEnvHelper(t, "AUTH_MODE", "anonymous")

	_, err := LoadConfig()
	if !errors.Is(err, ErrInvalidAuthConfig) {
		t.Fatalf("expected ErrInvalidAuthConfig for unknown AUTH_MODE, got: %v", err)
	}
}

func TestDevelopmentAuthDisabledByDefault(t *testing.T) {
	clearEnv(t)
	setEnvHelper(t, "APP_ENV", "development")
	setEnvHelper(t, "DATABASE_DSN", "memory")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("expected development to allow disabled auth, got: %v", err)
	}
	if cfg.AuthEnabled() {
		t.Fatalf("AuthMode = %q, want disabled", cfg.AuthMode)
	}
	verifier, err := newServiceVerifier(cfg)
	if err != nil {
		t.Fatalf("newServiceVerifier: %v", err)
	}
	if verifier != nil {
		t.Fatal("expected a nil verifier when auth is disabled")
	}
}

func TestDevelopmentSecureModeRequiresMaterial(t *testing.T) {
	clearEnv(t)
	setEnvHelper(t, "APP_ENV", "development")
	setEnvHelper(t, "DATABASE_DSN", "memory")
	setEnvHelper(t, "AUTH_MODE", "jwt-hs256")

	_, err := LoadConfig()
	if !errors.Is(err, ErrInvalidAuthConfig) {
		t.Fatalf("expected ErrInvalidAuthConfig for jwt-hs256 without a secret, got: %v", err)
	}
}

func TestGatewayAuthConfigParsing(t *testing.T) {
	clearEnv(t)
	setEnvHelper(t, "APP_ENV", "development")
	setEnvHelper(t, "DATABASE_DSN", "memory")
	setEnvHelper(t, "AUTH_MODE", "gateway-hmac")
	setEnvHelper(t, "AUTH_SECRET", testAuthSecret)
	setEnvHelper(t, "AUTH_CLOCK_SKEW", "45s")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.AuthMode != serviceauth.ModeGatewayHMAC {
		t.Fatalf("AuthMode = %q, want %q", cfg.AuthMode, serviceauth.ModeGatewayHMAC)
	}
	if cfg.AuthSecret != testAuthSecret {
		t.Fatal("AUTH_SECRET was not loaded")
	}
	if cfg.AuthClockSkew != 45*time.Second {
		t.Fatalf("AuthClockSkew = %s, want 45s", cfg.AuthClockSkew)
	}
	verifier, err := newServiceVerifier(cfg)
	if err != nil {
		t.Fatalf("newServiceVerifier: %v", err)
	}
	if verifier == nil {
		t.Fatal("expected a gateway verifier")
	}
}

func TestNumericAuthClockSkewParsing(t *testing.T) {
	clearEnv(t)
	setEnvHelper(t, "APP_ENV", "development")
	setEnvHelper(t, "DATABASE_DSN", "memory")
	setEnvHelper(t, "AUTH_MODE", "gateway-hmac")
	setEnvHelper(t, "AUTH_SECRET", testAuthSecret)
	setEnvHelper(t, "AUTH_CLOCK_SKEW", "90")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.AuthClockSkew != 90*time.Second {
		t.Fatalf("AuthClockSkew = %s, want 90s", cfg.AuthClockSkew)
	}
}

func TestInvalidAuthClockSkewRejected(t *testing.T) {
	clearEnv(t)
	setEnvHelper(t, "APP_ENV", "development")
	setEnvHelper(t, "DATABASE_DSN", "memory")
	setEnvHelper(t, "AUTH_MODE", "gateway-hmac")
	setEnvHelper(t, "AUTH_SECRET", testAuthSecret)
	setEnvHelper(t, "AUTH_CLOCK_SKEW", "soon")

	_, err := LoadConfig()
	if !errors.Is(err, ErrInvalidAuthConfig) {
		t.Fatalf("expected ErrInvalidAuthConfig for invalid AUTH_CLOCK_SKEW, got: %v", err)
	}
}

func TestJWTRS256ConfigParsingFromPublicKeyFile(t *testing.T) {
	publicKeyPEM := generatePublicKeyPEM(t)
	keyPath := filepath.Join(t.TempDir(), "jwt-public.pem")
	if err := os.WriteFile(keyPath, []byte(publicKeyPEM), 0o600); err != nil {
		t.Fatal(err)
	}

	clearEnv(t)
	setEnvHelper(t, "APP_ENV", "development")
	setEnvHelper(t, "DATABASE_DSN", "memory")
	setEnvHelper(t, "AUTH_MODE", "jwt-rs256")
	setEnvHelper(t, "AUTH_JWT_PUBLIC_KEY_FILE", keyPath)
	setEnvHelper(t, "AUTH_JWT_ISSUER", "https://issuer.example")
	setEnvHelper(t, "AUTH_JWT_AUDIENCE", "accounting-service")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !strings.Contains(cfg.AuthJWTPublicKey, "BEGIN PUBLIC KEY") {
		t.Fatalf("expected public key contents, got %q", cfg.AuthJWTPublicKey)
	}
	if cfg.AuthJWTIssuer != "https://issuer.example" || cfg.AuthJWTAudience != "accounting-service" {
		t.Fatalf("issuer/audience = %q/%q", cfg.AuthJWTIssuer, cfg.AuthJWTAudience)
	}
	verifier, err := newServiceVerifier(cfg)
	if err != nil {
		t.Fatalf("newServiceVerifier: %v", err)
	}
	if verifier == nil {
		t.Fatal("expected an RS256 verifier")
	}
}

func TestInvalidJWTPublicKeyDoesNotLeakMaterial(t *testing.T) {
	clearEnv(t)
	setEnvHelper(t, "APP_ENV", "development")
	setEnvHelper(t, "DATABASE_DSN", "memory")
	setEnvHelper(t, "AUTH_MODE", "jwt-rs256")
	setEnvHelper(t, "AUTH_JWT_PUBLIC_KEY", "private-looking-material-that-must-not-leak")

	_, err := LoadConfig()
	if !errors.Is(err, ErrInvalidAuthConfig) {
		t.Fatalf("expected ErrInvalidAuthConfig, got: %v", err)
	}
	if strings.Contains(err.Error(), "private-looking-material") {
		t.Fatalf("error leaked key material: %v", err)
	}
}

func TestAuthSecretNotLeakedInErrors(t *testing.T) {
	clearEnv(t)
	setEnvHelper(t, "APP_ENV", "development")
	setEnvHelper(t, "DATABASE_DSN", "memory")
	setEnvHelper(t, "AUTH_MODE", "gateway-hmac")
	setEnvHelper(t, "AUTH_SECRET", "shortsecret")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected a short secret to be rejected")
	}
	assertSecretNotInError(t, err)
}

func assertSecretNotInError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	for _, secret := range []string{testAuthSecret, "shortsecret"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked secret %q: %v", secret, err)
		}
	}
}

func generatePublicKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}
