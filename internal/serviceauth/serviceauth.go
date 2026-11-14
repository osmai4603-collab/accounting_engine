// Package serviceauth verifies caller identity at the transport boundary of
// the optional Standalone Service. It is transport-neutral: HTTP middleware and
// gRPC interceptors both translate their headers/metadata into Credentials,
// call a Verifier, and receive a verified Identity.
//
// The package deliberately contains no user management, no roles, and no
// authorization policy. It only answers "who is the caller and which companies
// did the trusted issuer grant?". The Embedded Library never calls this package
// and stays free of middleware, identity providers, and RBAC.
package serviceauth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
)

// DefaultClockSkew bounds how much clock drift is tolerated between the
// credential issuer and this service.
const DefaultClockSkew = time.Minute

// MinimumSecretBytes is the smallest shared secret accepted for HMAC signing
// (gateway HMAC and JWT HS256). Shorter material is rejected at startup.
const MinimumSecretBytes = 16

// Sentinel errors. Callers map all of them to a generic 401/Unauthenticated
// response; the wrapped detail is for local diagnostics only and must never
// contain the submitted secret, token, or signature.
var (
	// ErrUnauthenticated reports that the presented credential could not be
	// verified. Its public reason is intentionally generic.
	ErrUnauthenticated = errors.New("authentication required")
	// ErrInvalidPrincipal reports that an authenticated credential carries an
	// unusable identity (for example a missing subject or no valid company).
	ErrInvalidPrincipal = errors.New("invalid caller identity")
	// ErrMissingCredential reports that the caller supplied no credential at
	// all for the configured authentication mode.
	ErrMissingCredential = errors.New("missing credential")
	// ErrInvalidConfig reports incomplete or malformed verifier material.
	ErrInvalidConfig = errors.New("invalid service authentication configuration")
)

// Source identifies which mechanism produced a verified Identity.
type Source string

const (
	SourceGatewayHMAC Source = "gateway-hmac"
	SourceJWTHS256    Source = "jwt-hs256"
	SourceJWTRS256    Source = "jwt-rs256"
)

// Authentication mode names accepted by Config.
const (
	ModeNone        = "none"
	ModeGatewayHMAC = "gateway-hmac"
	ModeJWTHS256    = "jwt-hs256"
	ModeJWTRS256    = "jwt-rs256"
)

// Trusted identity header names. HTTP treats them case-insensitively and gRPC
// metadata keys are lower-case, so Credentials.Header performs the matching.
const (
	HeaderActorID       = "X-Accounting-Actor-ID"
	HeaderCompanyIDs    = "X-Accounting-Company-IDs"
	HeaderTenantID      = "X-Accounting-Tenant-ID"
	HeaderTimestamp     = "X-Accounting-Timestamp"
	HeaderSignature     = "X-Accounting-Signature"
	HeaderRequestID     = "X-Accounting-Request-ID"
	HeaderAuthorization = "Authorization"
)

// Identity is a verified caller principal. It contains only what a trusted
// issuer granted; client-supplied company or actor values remain untrusted.
type Identity struct {
	ActorID    string
	TenantID   string
	CompanyIDs []types.CompanyID
	Source     Source
}

// HasCompany reports whether the verified identity grants access to companyID.
func (i Identity) HasCompany(companyID types.CompanyID) bool {
	for _, allowed := range i.CompanyIDs {
		if allowed == companyID {
			return true
		}
	}
	return false
}

// Credentials is the transport-neutral form of caller authentication input.
// Authorization carries the HTTP Authorization header value (for example
// "Bearer <jwt>"). Headers carries lower- or mixed-case names; lookups are
// case-insensitive.
type Credentials struct {
	Authorization string
	Headers       map[string]string
}

// Header returns the first value for name, matching case-insensitively.
func (c Credentials) Header(name string) string {
	if value := c.Headers[name]; value != "" {
		return value
	}
	wanted := strings.ToLower(name)
	for key, value := range c.Headers {
		if strings.ToLower(key) == wanted {
			return value
		}
	}
	return ""
}

// Verifier verifies transport credentials and returns a trusted Identity.
// Implementations must not return credential material in errors.
type Verifier interface {
	Verify(ctx context.Context, credentials Credentials) (Identity, error)
}

// Config selects and parameterizes a Verifier.
type Config struct {
	// Mode is one of none, gateway-hmac, jwt-hs256, jwt-rs256 (aliases are
	// normalized by NewVerifier).
	Mode string
	// Secret is the shared HMAC secret for gateway-hmac / jwt-hs256.
	Secret string
	// JWTPublicKeyPEM is a PEM-encoded RSA public key for jwt-rs256.
	JWTPublicKeyPEM string
	// JWTAlg optionally pins the expected JWT algorithm. When empty it is
	// derived from Mode.
	JWTAlg string
	// JWTIssuer, when set, must match the token's iss claim exactly.
	JWTIssuer string
	// JWTAudience, when set, must appear in the token's aud claim.
	JWTAudience string
	// ClockSkew overrides DefaultClockSkew when positive.
	ClockSkew time.Duration
}

// NormalizeMode canonicalizes an authentication mode value. Unknown values are
// returned trimmed and lower-cased so NewVerifier can reject them explicitly.
func NormalizeMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "none", "disabled", "off":
		return ModeNone
	case "gateway", "gateway-hmac", "hmac", "hmac-sha256", "hmac_sha256":
		return ModeGatewayHMAC
	case "hs256", "jwt-hs256", "jwt_hs256":
		return ModeJWTHS256
	case "rs256", "jwt-rs256", "jwt_rs256", "jwt-rsa":
		return ModeJWTRS256
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

// NewVerifier builds the verifier for cfg. Explicit mode "none" (or an empty
// mode) returns (nil, nil), meaning the transport boundary is disabled. Any
// selected secure mode with missing or invalid material returns an error so the
// service can fail closed at startup.
func NewVerifier(cfg Config) (Verifier, error) {
	mode := NormalizeMode(cfg.Mode)
	skew := cfg.ClockSkew
	if skew <= 0 {
		skew = DefaultClockSkew
	}

	switch mode {
	case ModeNone:
		return nil, nil

	case ModeGatewayHMAC:
		if err := validateSecret(cfg.Secret); err != nil {
			return nil, fmt.Errorf("%w: gateway-hmac requires AUTH_SECRET: %v", ErrInvalidConfig, err)
		}
		return &GatewayHMACVerifier{
			secret:    []byte(cfg.Secret),
			clockSkew: skew,
			now:       time.Now,
		}, nil

	case ModeJWTHS256:
		if cfg.JWTAlg != "" && !strings.EqualFold(cfg.JWTAlg, "HS256") {
			return nil, fmt.Errorf("%w: jwt-hs256 only accepts HS256", ErrInvalidConfig)
		}
		if err := validateSecret(cfg.Secret); err != nil {
			return nil, fmt.Errorf("%w: jwt-hs256 requires AUTH_SECRET: %v", ErrInvalidConfig, err)
		}
		return newJWTVerifier(jwtVerifierOptions{
			alg:       "HS256",
			secret:    []byte(cfg.Secret),
			issuer:    cfg.JWTIssuer,
			audience:  cfg.JWTAudience,
			clockSkew: skew,
		})

	case ModeJWTRS256:
		if cfg.JWTAlg != "" && !strings.EqualFold(cfg.JWTAlg, "RS256") {
			return nil, fmt.Errorf("%w: jwt-rs256 only accepts RS256", ErrInvalidConfig)
		}
		publicKey, err := parseRSAPublicKeyPEM(cfg.JWTPublicKeyPEM)
		if err != nil {
			return nil, fmt.Errorf("%w: jwt-rs256 requires a valid RSA public key in AUTH_JWT_PUBLIC_KEY", ErrInvalidConfig)
		}
		return newJWTVerifier(jwtVerifierOptions{
			alg:       "RS256",
			publicKey: publicKey,
			issuer:    cfg.JWTIssuer,
			audience:  cfg.JWTAudience,
			clockSkew: skew,
		})

	default:
		return nil, fmt.Errorf("%w: unsupported authentication mode %q", ErrInvalidConfig, mode)
	}
}

func validateSecret(secret string) error {
	if strings.TrimSpace(secret) == "" {
		return errors.New("secret is not set")
	}
	if len(secret) < MinimumSecretBytes {
		return fmt.Errorf("secret must be at least %d bytes", MinimumSecretBytes)
	}
	return nil
}

func errUnauthenticated(reason string) error {
	return fmt.Errorf("%w: %s", ErrUnauthenticated, reason)
}

func errInvalidPrincipal(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidPrincipal, reason)
}

func errMissingCredential(reason string) error {
	return fmt.Errorf("%w: %s", ErrMissingCredential, reason)
}

func trimmedField(value string, limit int) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || len(trimmed) > limit {
		return "", false
	}
	return trimmed, true
}
