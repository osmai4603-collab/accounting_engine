package serviceauth

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func testVerifier(t *testing.T, mode, secret string) Verifier {
	t.Helper()
	verifier, err := NewVerifier(Config{Mode: mode, Secret: secret, ClockSkew: 30 * time.Second})
	if err != nil {
		t.Fatalf("NewVerifier(%s): %v", mode, err)
	}
	if verifier == nil {
		t.Fatalf("NewVerifier(%s) returned nil", mode)
	}
	return verifier
}

func gatewayCredentials(actor string, companies []types.CompanyID, timestamp time.Time, secret string) Credentials {
	claims := GatewayClaims{ActorID: actor, CompanyIDs: companies, Timestamp: timestamp}
	return Credentials{Headers: map[string]string{
		HeaderActorID:    actor,
		HeaderCompanyIDs: companyIDHeader(companies),
		HeaderTimestamp:  strconv.FormatInt(timestamp.Unix(), 10),
		HeaderSignature:  SignGatewayHMAC(secret, claims),
	}}
}

func companyIDHeader(companies []types.CompanyID) string {
	values := make([]string, 0, len(companies))
	for _, companyID := range companies {
		values = append(values, uuid.UUID(companyID).String())
	}
	return strings.Join(values, ",")
}

func TestGatewayHMACVerifierAcceptsValidIdentity(t *testing.T) {
	companyA := types.NewCompanyID()
	companyB := types.NewCompanyID()
	verifier := testVerifier(t, ModeGatewayHMAC, testSecret)

	claims := GatewayClaims{
		ActorID:    "actor-1",
		TenantID:   "tenant-1",
		CompanyIDs: []types.CompanyID{companyB, companyA},
		Timestamp:  time.Now().UTC(),
		RequestID:  "request-1",
	}
	credentials := Credentials{Headers: map[string]string{
		strings.ToLower(HeaderActorID):    claims.ActorID,
		strings.ToLower(HeaderCompanyIDs): companyIDHeader(claims.CompanyIDs),
		strings.ToLower(HeaderTenantID):   claims.TenantID,
		strings.ToLower(HeaderTimestamp):  claims.Timestamp.Format(time.RFC3339),
		strings.ToLower(HeaderSignature):  SignGatewayHMAC(testSecret, claims),
		strings.ToLower(HeaderRequestID):  claims.RequestID,
	}}

	identity, err := verifier.Verify(context.Background(), credentials)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if identity.ActorID != "actor-1" || identity.TenantID != "tenant-1" {
		t.Fatalf("identity = %#v", identity)
	}
	if !identity.HasCompany(companyA) || !identity.HasCompany(companyB) {
		t.Fatalf("identity lost companies: %#v", identity.CompanyIDs)
	}
	if identity.Source != SourceGatewayHMAC {
		t.Fatalf("source = %q, want %q", identity.Source, SourceGatewayHMAC)
	}
}

func TestGatewayHMACVerifierRejectsMissingOrInvalidCredentials(t *testing.T) {
	companyID := types.NewCompanyID()
	verifier := testVerifier(t, ModeGatewayHMAC, testSecret)
	valid := gatewayCredentials("actor-1", []types.CompanyID{companyID}, time.Now().UTC(), testSecret)

	tests := []struct {
		name     string
		mutate   func(Credentials) Credentials
		sentinel error
	}{
		{
			name: "missing actor",
			mutate: func(c Credentials) Credentials {
				delete(c.Headers, HeaderActorID)
				return c
			},
			sentinel: ErrMissingCredential,
		},
		{
			name: "missing companies",
			mutate: func(c Credentials) Credentials {
				delete(c.Headers, HeaderCompanyIDs)
				return c
			},
			sentinel: ErrMissingCredential,
		},
		{
			name: "invalid company",
			mutate: func(c Credentials) Credentials {
				c.Headers[HeaderCompanyIDs] = "not-a-uuid"
				return c
			},
			sentinel: ErrInvalidPrincipal,
		},
		{
			name: "missing timestamp",
			mutate: func(c Credentials) Credentials {
				delete(c.Headers, HeaderTimestamp)
				return c
			},
			sentinel: ErrMissingCredential,
		},
		{
			name: "malformed timestamp",
			mutate: func(c Credentials) Credentials {
				c.Headers[HeaderTimestamp] = "yesterday"
				return c
			},
			sentinel: ErrUnauthenticated,
		},
		{
			name: "expired timestamp",
			mutate: func(c Credentials) Credentials {
				ts := time.Now().UTC().Add(-10 * time.Minute)
				return gatewayCredentials("actor-1", []types.CompanyID{companyID}, ts, testSecret)
			},
			sentinel: ErrUnauthenticated,
		},
		{
			name: "future timestamp",
			mutate: func(c Credentials) Credentials {
				ts := time.Now().UTC().Add(10 * time.Minute)
				return gatewayCredentials("actor-1", []types.CompanyID{companyID}, ts, testSecret)
			},
			sentinel: ErrUnauthenticated,
		},
		{
			name: "missing signature",
			mutate: func(c Credentials) Credentials {
				delete(c.Headers, HeaderSignature)
				return c
			},
			sentinel: ErrMissingCredential,
		},
		{
			name: "invalid signature hex",
			mutate: func(c Credentials) Credentials {
				c.Headers[HeaderSignature] = "zzzz"
				return c
			},
			sentinel: ErrUnauthenticated,
		},
		{
			name: "wrong secret",
			mutate: func(c Credentials) Credentials {
				return gatewayCredentials("actor-1", []types.CompanyID{companyID}, time.Now().UTC(), "ffffffffffffffffffffffffffffffff")
			},
			sentinel: ErrUnauthenticated,
		},
		{
			name: "actor impersonation after signing",
			mutate: func(c Credentials) Credentials {
				c.Headers[HeaderActorID] = "actor-2"
				return c
			},
			sentinel: ErrUnauthenticated,
		},
		{
			name: "company impersonation after signing",
			mutate: func(c Credentials) Credentials {
				c.Headers[HeaderCompanyIDs] = uuid.NewString()
				return c
			},
			sentinel: ErrUnauthenticated,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			credentials := Credentials{Headers: make(map[string]string, len(valid.Headers))}
			for key, value := range valid.Headers {
				credentials.Headers[key] = value
			}
			_, err := verifier.Verify(context.Background(), test.mutate(credentials))
			if !errors.Is(err, test.sentinel) {
				t.Fatalf("error = %v, want sentinel %v", err, test.sentinel)
			}
			assertNoCredentialLeak(t, err)
		})
	}
}

func TestGatewayHMACVerifierAcceptsUnixSeconds(t *testing.T) {
	companyID := types.NewCompanyID()
	verifier := testVerifier(t, ModeGatewayHMAC, testSecret)
	timestamp := time.Now().UTC().Truncate(time.Second)
	claims := GatewayClaims{ActorID: "actor-1", CompanyIDs: []types.CompanyID{companyID}, Timestamp: timestamp}
	identity, err := verifier.Verify(context.Background(), Credentials{Headers: map[string]string{
		HeaderActorID:    claims.ActorID,
		HeaderCompanyIDs: companyIDHeader(claims.CompanyIDs),
		HeaderTimestamp:  strconv.FormatInt(timestamp.Unix(), 10),
		HeaderSignature:  SignGatewayHMAC(testSecret, claims),
	}})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !identity.HasCompany(companyID) {
		t.Fatalf("identity lost company: %#v", identity)
	}
}

func TestGatewayHMACVerifierDeduplicatesCompanies(t *testing.T) {
	companyID := types.NewCompanyID()
	verifier := testVerifier(t, ModeGatewayHMAC, testSecret)
	timestamp := time.Now().UTC()
	claims := GatewayClaims{ActorID: "actor-1", CompanyIDs: []types.CompanyID{companyID, companyID}, Timestamp: timestamp}
	identity, err := verifier.Verify(context.Background(), Credentials{Headers: map[string]string{
		HeaderActorID:    claims.ActorID,
		HeaderCompanyIDs: companyIDHeader(claims.CompanyIDs),
		HeaderTimestamp:  strconv.FormatInt(timestamp.Unix(), 10),
		HeaderSignature:  SignGatewayHMAC(testSecret, claims),
	}})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(identity.CompanyIDs) != 1 {
		t.Fatalf("company count = %d, want 1", len(identity.CompanyIDs))
	}
}

func TestJWTVerifierHS256ValidToken(t *testing.T) {
	companyA := types.NewCompanyID()
	companyB := types.NewCompanyID()
	verifier := testVerifier(t, ModeJWTHS256, testSecret)
	now := time.Now().Unix()

	token := signHS256(t, testSecret, map[string]any{"alg": "HS256", "typ": "JWT"}, map[string]any{
		"sub":         "user-1",
		"iss":         "issuer-1",
		"aud":         []string{"other", "accounting"},
		"exp":         now + 300,
		"nbf":         now - 10,
		"iat":         now - 10,
		"tenant_id":   "tenant-1",
		"company_ids": []string{uuid.UUID(companyA).String(), uuid.UUID(companyB).String()},
		"permissions": []string{"ledger.post"},
	})

	verifierWithPolicy, err := NewVerifier(Config{
		Mode:        ModeJWTHS256,
		Secret:      testSecret,
		JWTIssuer:   "issuer-1",
		JWTAudience: "accounting",
		ClockSkew:   30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	identity, err := verifierWithPolicy.Verify(context.Background(), Credentials{Authorization: "Bearer " + token})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if identity.ActorID != "user-1" || identity.TenantID != "tenant-1" {
		t.Fatalf("identity = %#v", identity)
	}
	if !identity.HasCompany(companyA) || !identity.HasCompany(companyB) {
		t.Fatalf("identity lost companies: %#v", identity.CompanyIDs)
	}
	if identity.Source != SourceJWTHS256 {
		t.Fatalf("source = %q", identity.Source)
	}

	// A verifier without issuer/audience policy still validates the token.
	if _, err := verifier.Verify(context.Background(), Credentials{Headers: map[string]string{"authorization": "bearer " + token}}); err != nil {
		t.Fatalf("lower-case bearer Verify: %v", err)
	}
}

func TestJWTVerifierAcceptsSingleCompanyClaim(t *testing.T) {
	companyID := types.NewCompanyID()
	verifier := testVerifier(t, ModeJWTHS256, testSecret)
	token := signHS256(t, testSecret, map[string]any{"alg": "HS256"}, map[string]any{
		"sub":        "user-1",
		"iss":        "issuer-1",
		"exp":        time.Now().Unix() + 300,
		"company_id": uuid.UUID(companyID).String(),
		"aud":        "accounting",
	})
	identity, err := verifier.Verify(context.Background(), Credentials{Authorization: "Bearer " + token})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !identity.HasCompany(companyID) {
		t.Fatalf("identity lost company: %#v", identity)
	}
}

func TestJWTVerifierRejectsInvalidTokens(t *testing.T) {
	companyID := types.NewCompanyID()
	now := time.Now().Unix()
	baseClaims := func() map[string]any {
		return map[string]any{
			"sub":         "user-1",
			"iss":         "issuer-1",
			"aud":         "accounting",
			"exp":         now + 300,
			"company_ids": []string{uuid.UUID(companyID).String()},
		}
	}
	policyVerifier, err := NewVerifier(Config{
		Mode:        ModeJWTHS256,
		Secret:      testSecret,
		JWTIssuer:   "issuer-1",
		JWTAudience: "accounting",
		ClockSkew:   30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		token    func() string
		sentinel error
	}{
		{
			name:     "empty authorization",
			token:    func() string { return "" },
			sentinel: ErrMissingCredential,
		},
		{
			name:     "unsupported scheme",
			token:    func() string { return "Basic abc" },
			sentinel: ErrUnauthenticated,
		},
		{
			name:     "malformed token",
			token:    func() string { return "not-a-jwt" },
			sentinel: ErrUnauthenticated,
		},
		{
			name: "alg none",
			token: func() string {
				return signHS256(t, testSecret, map[string]any{"alg": "none"}, baseClaims())
			},
			sentinel: ErrUnauthenticated,
		},
		{
			name: "unexpected algorithm",
			token: func() string {
				return signHS256(t, testSecret, map[string]any{"alg": "RS256"}, baseClaims())
			},
			sentinel: ErrUnauthenticated,
		},
		{
			name: "bad signature",
			token: func() string {
				return signHS256(t, "ffffffffffffffffffffffffffffffff", map[string]any{"alg": "HS256"}, baseClaims())
			},
			sentinel: ErrUnauthenticated,
		},
		{
			name: "expired",
			token: func() string {
				claims := baseClaims()
				claims["exp"] = now - 3600
				return signHS256(t, testSecret, map[string]any{"alg": "HS256"}, claims)
			},
			sentinel: ErrUnauthenticated,
		},
		{
			name: "missing exp",
			token: func() string {
				claims := baseClaims()
				delete(claims, "exp")
				return signHS256(t, testSecret, map[string]any{"alg": "HS256"}, claims)
			},
			sentinel: ErrUnauthenticated,
		},
		{
			name: "future nbf",
			token: func() string {
				claims := baseClaims()
				claims["nbf"] = now + 3600
				return signHS256(t, testSecret, map[string]any{"alg": "HS256"}, claims)
			},
			sentinel: ErrUnauthenticated,
		},
		{
			name: "future iat",
			token: func() string {
				claims := baseClaims()
				claims["iat"] = now + 3600
				return signHS256(t, testSecret, map[string]any{"alg": "HS256"}, claims)
			},
			sentinel: ErrUnauthenticated,
		},
		{
			name: "wrong issuer",
			token: func() string {
				claims := baseClaims()
				claims["iss"] = "evil"
				return signHS256(t, testSecret, map[string]any{"alg": "HS256"}, claims)
			},
			sentinel: ErrUnauthenticated,
		},
		{
			name: "wrong audience",
			token: func() string {
				claims := baseClaims()
				claims["aud"] = []string{"other"}
				return signHS256(t, testSecret, map[string]any{"alg": "HS256"}, claims)
			},
			sentinel: ErrUnauthenticated,
		},
		{
			name: "missing subject",
			token: func() string {
				claims := baseClaims()
				delete(claims, "sub")
				return signHS256(t, testSecret, map[string]any{"alg": "HS256"}, claims)
			},
			sentinel: ErrInvalidPrincipal,
		},
		{
			name: "no company",
			token: func() string {
				claims := baseClaims()
				delete(claims, "company_ids")
				return signHS256(t, testSecret, map[string]any{"alg": "HS256"}, claims)
			},
			sentinel: ErrInvalidPrincipal,
		},
		{
			name: "invalid company",
			token: func() string {
				claims := baseClaims()
				claims["company_ids"] = []string{"not-a-uuid"}
				return signHS256(t, testSecret, map[string]any{"alg": "HS256"}, claims)
			},
			sentinel: ErrInvalidPrincipal,
		},
		{
			name: "non-string company claim",
			token: func() string {
				claims := baseClaims()
				claims["company_ids"] = []int{42}
				return signHS256(t, testSecret, map[string]any{"alg": "HS256"}, claims)
			},
			sentinel: ErrUnauthenticated,
		},
		{
			name: "tampered payload",
			token: func() string {
				token := signHS256(t, testSecret, map[string]any{"alg": "HS256"}, baseClaims())
				parts := strings.Split(token, ".")
				tampered := baseClaims()
				tampered["sub"] = "attacker"
				payload, _ := json.Marshal(tampered)
				parts[1] = base64.RawURLEncoding.EncodeToString(payload)
				return strings.Join(parts, ".")
			},
			sentinel: ErrUnauthenticated,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := test.token()
			credentials := Credentials{Authorization: value}
			if value != "" && !strings.Contains(value, " ") {
				credentials.Authorization = "Bearer " + value
			}
			_, err := policyVerifier.Verify(context.Background(), credentials)
			if !errors.Is(err, test.sentinel) {
				t.Fatalf("error = %v, want sentinel %v", err, test.sentinel)
			}
			assertNoCredentialLeak(t, err)
		})
	}
}

func TestJWTVerifierRS256(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	publicKeyPEM := encodeRSAPublicKeyPEM(t, key)
	verifier, err := NewVerifier(Config{
		Mode:            ModeJWTRS256,
		JWTPublicKeyPEM: publicKeyPEM,
		JWTIssuer:       "issuer-1",
		JWTAudience:     "accounting",
		ClockSkew:       30 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	companyID := types.NewCompanyID()
	token := signRS256(t, key, map[string]any{"alg": "RS256", "typ": "JWT"}, map[string]any{
		"sub":        "user-rsa",
		"iss":        "issuer-1",
		"aud":        "accounting",
		"exp":        time.Now().Unix() + 300,
		"company_id": uuid.UUID(companyID).String(),
	})

	identity, err := verifier.Verify(context.Background(), Credentials{Authorization: "Bearer " + token})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if identity.ActorID != "user-rsa" || !identity.HasCompany(companyID) {
		t.Fatalf("identity = %#v", identity)
	}
	if identity.Source != SourceJWTRS256 {
		t.Fatalf("source = %q", identity.Source)
	}

	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	foreignToken := signRS256(t, otherKey, map[string]any{"alg": "RS256"}, map[string]any{
		"sub":        "user-rsa",
		"iss":        "issuer-1",
		"aud":        "accounting",
		"exp":        time.Now().Unix() + 300,
		"company_id": uuid.UUID(companyID).String(),
	})
	if _, err := verifier.Verify(context.Background(), Credentials{Authorization: "Bearer " + foreignToken}); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("foreign signature error = %v, want ErrUnauthenticated", err)
	}
}

func TestNewVerifierFailsClosedOnInvalidMaterial(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "unknown mode", cfg: Config{Mode: "open-sesame"}},
		{name: "gateway without secret", cfg: Config{Mode: ModeGatewayHMAC}},
		{name: "gateway with short secret", cfg: Config{Mode: ModeGatewayHMAC, Secret: "short"}},
		{name: "hs256 without secret", cfg: Config{Mode: ModeJWTHS256}},
		{name: "hs256 with rs256 algorithm", cfg: Config{Mode: ModeJWTHS256, Secret: testSecret, JWTAlg: "RS256"}},
		{name: "rs256 without key", cfg: Config{Mode: ModeJWTRS256}},
		{name: "rs256 with malformed key", cfg: Config{Mode: ModeJWTRS256, JWTPublicKeyPEM: "not a pem"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verifier, err := NewVerifier(test.cfg)
			if err == nil || verifier != nil {
				t.Fatalf("NewVerifier = (%v, %v), want error", verifier, err)
			}
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("error = %v, want ErrInvalidConfig", err)
			}
			assertNoCredentialLeak(t, err)
		})
	}
}

func TestNewVerifierReturnsNilForExplicitNone(t *testing.T) {
	for _, mode := range []string{"", "none", "disabled", "off"} {
		verifier, err := NewVerifier(Config{Mode: mode})
		if err != nil {
			t.Fatalf("mode %q: %v", mode, err)
		}
		if verifier != nil {
			t.Fatalf("mode %q returned a verifier", mode)
		}
	}
}

func TestNormalizeModeAliases(t *testing.T) {
	tests := map[string]string{
		"":                  ModeNone,
		"NONE":              ModeNone,
		"gateway":           ModeGatewayHMAC,
		"gateway-hmac":      ModeGatewayHMAC,
		"HS256":             ModeJWTHS256,
		"jwt-hs256":         ModeJWTHS256,
		"rs256":             ModeJWTRS256,
		"JWT-RS256":         ModeJWTRS256,
		"something-unknown": "something-unknown",
	}
	for input, want := range tests {
		if got := NormalizeMode(input); got != want {
			t.Errorf("NormalizeMode(%q) = %q, want %q", input, got, want)
		}
	}
}

func signHS256(t *testing.T, secret string, header, claims map[string]any) string {
	t.Helper()
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func signRS256(t *testing.T, key *rsa.PrivateKey, header, claims map[string]any) string {
	t.Helper()
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func encodeRSAPublicKeyPEM(t *testing.T, key *rsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func assertNoCredentialLeak(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	message := err.Error()
	for _, secret := range []string{testSecret, "Bearer ", "eyJ"} {
		if strings.Contains(message, secret) {
			t.Fatalf("error string leaked credential material %q: %s", secret, message)
		}
	}
}
