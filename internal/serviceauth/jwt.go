package serviceauth

import (
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
)

// maxTokenBytes bounds the accepted compact JWT size to keep verification cost
// predictable.
const maxTokenBytes = 16 << 10

type jwtVerifierOptions struct {
	alg       string
	secret    []byte
	publicKey *rsa.PublicKey
	issuer    string
	audience  string
	clockSkew time.Duration
}

// JWTVerifier verifies compact JWTs signed with HS256 or RS256 using only the
// standard library. It enforces algorithm allowlisting, signature, exp/nbf/iat
// with clock skew, exact issuer, and audience membership. It intentionally
// ignores permissions claims: authorization remains a host capability.
type JWTVerifier struct {
	alg       string
	secret    []byte
	publicKey *rsa.PublicKey
	issuer    string
	audience  string
	clockSkew time.Duration
	now       func() time.Time
}

func newJWTVerifier(options jwtVerifierOptions) (*JWTVerifier, error) {
	if options.alg != "HS256" && options.alg != "RS256" {
		return nil, fmt.Errorf("%w: unsupported JWT algorithm", ErrInvalidConfig)
	}
	if options.alg == "HS256" && len(options.secret) == 0 {
		return nil, fmt.Errorf("%w: HS256 requires a shared secret", ErrInvalidConfig)
	}
	if options.alg == "RS256" && options.publicKey == nil {
		return nil, fmt.Errorf("%w: RS256 requires an RSA public key", ErrInvalidConfig)
	}
	return &JWTVerifier{
		alg:       options.alg,
		secret:    options.secret,
		publicKey: options.publicKey,
		issuer:    options.issuer,
		audience:  options.audience,
		clockSkew: options.clockSkew,
		now:       time.Now,
	}, nil
}

// Verify implements Verifier.
func (v *JWTVerifier) Verify(_ context.Context, credentials Credentials) (Identity, error) {
	token, err := bearerToken(credentials)
	if err != nil {
		return Identity{}, err
	}
	if len(token) > maxTokenBytes {
		return Identity{}, errUnauthenticated("token is too large")
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Identity{}, errUnauthenticated("malformed token")
	}

	headerBytes, err := decodeJWTSegment(parts[0])
	if err != nil {
		return Identity{}, errUnauthenticated("malformed token header")
	}
	var header struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return Identity{}, errUnauthenticated("malformed token header")
	}
	if header.Alg == "" || header.Alg == "none" || header.Alg != v.alg {
		return Identity{}, errUnauthenticated("unsupported token algorithm")
	}

	signature, err := decodeJWTSegment(parts[2])
	if err != nil {
		return Identity{}, errUnauthenticated("malformed token signature")
	}
	signingInput := []byte(parts[0] + "." + parts[1])
	if !v.validSignature(signingInput, signature) {
		return Identity{}, errUnauthenticated("invalid token signature")
	}

	payload, err := decodeJWTSegment(parts[1])
	if err != nil {
		return Identity{}, errUnauthenticated("malformed token claims")
	}
	claims, err := decodeJWTClaims(payload)
	if err != nil {
		return Identity{}, err
	}

	now := v.now().UTC()
	if claims.ExpiresAt == nil {
		return Identity{}, errUnauthenticated("token expiration is required")
	}
	if now.After(time.Unix(*claims.ExpiresAt, 0).Add(v.clockSkew)) {
		return Identity{}, errUnauthenticated("token expired")
	}
	if claims.NotBefore != nil && now.Add(v.clockSkew).Before(time.Unix(*claims.NotBefore, 0)) {
		return Identity{}, errUnauthenticated("token is not yet valid")
	}
	if claims.IssuedAt != nil && now.Add(v.clockSkew).Before(time.Unix(*claims.IssuedAt, 0)) {
		return Identity{}, errUnauthenticated("token was issued in the future")
	}
	if v.issuer != "" && claims.Issuer != v.issuer {
		return Identity{}, errUnauthenticated("token issuer mismatch")
	}
	if v.audience != "" && !containsString(claims.Audience, v.audience) {
		return Identity{}, errUnauthenticated("token audience mismatch")
	}

	actorID, ok := trimmedField(claims.Subject, maxIdentityFieldLength)
	if !ok {
		return Identity{}, errInvalidPrincipal("token subject is required")
	}
	if len(claims.TenantID) > maxIdentityFieldLength {
		return Identity{}, errInvalidPrincipal("token tenant is too long")
	}

	companies := make([]types.CompanyID, 0, len(claims.CompanyIDs)+1)
	seen := make(map[uuid.UUID]struct{}, len(claims.CompanyIDs)+1)
	for _, rawCompany := range claims.CompanyIDs {
		companyID, err := parseClaimCompanyID(rawCompany)
		if err != nil {
			return Identity{}, err
		}
		if _, duplicate := seen[uuid.UUID(companyID)]; duplicate {
			continue
		}
		seen[uuid.UUID(companyID)] = struct{}{}
		companies = append(companies, companyID)
	}
	if rawCompany := strings.TrimSpace(claims.CompanyID); rawCompany != "" {
		companyID, err := parseClaimCompanyID(rawCompany)
		if err != nil {
			return Identity{}, err
		}
		if _, duplicate := seen[uuid.UUID(companyID)]; !duplicate {
			seen[uuid.UUID(companyID)] = struct{}{}
			companies = append(companies, companyID)
		}
	}
	if len(companies) == 0 {
		return Identity{}, errInvalidPrincipal("token must grant at least one company")
	}

	source := SourceJWTHS256
	if v.alg == "RS256" {
		source = SourceJWTRS256
	}
	return Identity{
		ActorID:    actorID,
		TenantID:   claims.TenantID,
		CompanyIDs: companies,
		Source:     source,
	}, nil
}

func (v *JWTVerifier) validSignature(signingInput, signature []byte) bool {
	switch v.alg {
	case "HS256":
		mac := hmac.New(sha256.New, v.secret)
		_, _ = mac.Write(signingInput)
		return hmac.Equal(mac.Sum(nil), signature)
	case "RS256":
		digest := sha256.Sum256(signingInput)
		return rsa.VerifyPKCS1v15(v.publicKey, crypto.SHA256, digest[:], signature) == nil
	default:
		return false
	}
}

func bearerToken(credentials Credentials) (string, error) {
	raw := strings.TrimSpace(credentials.Authorization)
	if raw == "" {
		raw = strings.TrimSpace(credentials.Header(HeaderAuthorization))
	}
	if raw == "" {
		return "", errMissingCredential("missing authorization header")
	}
	const prefix = "bearer "
	if len(raw) <= len(prefix) || !strings.EqualFold(raw[:len(prefix)], prefix) {
		return "", errUnauthenticated("unsupported authorization scheme")
	}
	token := strings.TrimSpace(raw[len(prefix):])
	if token == "" {
		return "", errMissingCredential("missing bearer token")
	}
	return token, nil
}

func decodeJWTSegment(segment string) ([]byte, error) {
	if segment == "" {
		return nil, errors.New("empty token segment")
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(segment); err == nil {
		return decoded, nil
	}
	return base64.URLEncoding.DecodeString(segment)
}

type jwtClaims struct {
	Subject    string
	Issuer     string
	Audience   []string
	ExpiresAt  *int64
	NotBefore  *int64
	IssuedAt   *int64
	TenantID   string
	CompanyIDs []string
	CompanyID  string
}

func decodeJWTClaims(payload []byte) (jwtClaims, error) {
	var raw map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return jwtClaims{}, errUnauthenticated("malformed token claims")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return jwtClaims{}, errUnauthenticated("malformed token claims")
	}

	var claims jwtClaims
	var err error
	if claims.Subject, err = stringClaim(raw, "sub"); err != nil {
		return jwtClaims{}, err
	}
	if claims.Issuer, err = stringClaim(raw, "iss"); err != nil {
		return jwtClaims{}, err
	}
	if claims.TenantID, err = stringClaim(raw, "tenant_id"); err != nil {
		return jwtClaims{}, err
	}
	if claims.CompanyID, err = stringClaim(raw, "company_id"); err != nil {
		return jwtClaims{}, err
	}
	if claims.Audience, err = audienceClaim(raw); err != nil {
		return jwtClaims{}, err
	}
	if claims.ExpiresAt, err = numericDateClaim(raw, "exp"); err != nil {
		return jwtClaims{}, err
	}
	if claims.NotBefore, err = numericDateClaim(raw, "nbf"); err != nil {
		return jwtClaims{}, err
	}
	if claims.IssuedAt, err = numericDateClaim(raw, "iat"); err != nil {
		return jwtClaims{}, err
	}
	if claims.CompanyIDs, err = stringListClaim(raw, "company_ids"); err != nil {
		return jwtClaims{}, err
	}
	return claims, nil
}

func stringClaim(raw map[string]json.RawMessage, key string) (string, error) {
	value, ok := raw[key]
	if !ok {
		return "", nil
	}
	var parsed string
	if err := json.Unmarshal(value, &parsed); err != nil {
		return "", errUnauthenticated("invalid " + key + " claim")
	}
	return parsed, nil
}

func stringListClaim(raw map[string]json.RawMessage, key string) ([]string, error) {
	value, ok := raw[key]
	if !ok || len(bytes.TrimSpace(value)) == 0 || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return nil, nil
	}
	var parsed []string
	if err := json.Unmarshal(value, &parsed); err != nil {
		return nil, errUnauthenticated("invalid " + key + " claim")
	}
	return parsed, nil
}

func audienceClaim(raw map[string]json.RawMessage) ([]string, error) {
	value, ok := raw["aud"]
	if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return nil, nil
	}
	var single string
	if err := json.Unmarshal(value, &single); err == nil {
		return []string{single}, nil
	}
	var many []string
	if err := json.Unmarshal(value, &many); err != nil {
		return nil, errUnauthenticated("invalid aud claim")
	}
	return many, nil
}

func numericDateClaim(raw map[string]json.RawMessage, key string) (*int64, error) {
	value, ok := raw[key]
	if !ok {
		return nil, nil
	}
	var number json.Number
	if err := json.Unmarshal(value, &number); err != nil {
		return nil, errUnauthenticated("invalid " + key + " claim")
	}
	if seconds, err := strconv.ParseInt(number.String(), 10, 64); err == nil {
		return &seconds, nil
	}
	fractional, err := strconv.ParseFloat(number.String(), 64)
	if err != nil {
		return nil, errUnauthenticated("invalid " + key + " claim")
	}
	seconds := int64(fractional)
	return &seconds, nil
}

func parseClaimCompanyID(raw string) (types.CompanyID, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return types.CompanyID{}, errInvalidPrincipal("token contains an invalid company claim")
	}
	return types.CompanyID(parsed), nil
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func parseRSAPublicKeyPEM(raw string) (*rsa.PublicKey, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("public key is empty")
	}
	block, _ := pem.Decode([]byte(trimmed))
	if block == nil {
		return nil, errors.New("public key is not PEM encoded")
	}
	switch block.Type {
	case "CERTIFICATE":
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, errors.New("invalid certificate")
		}
		publicKey, ok := certificate.PublicKey.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("certificate does not contain an RSA public key")
		}
		return publicKey, nil
	case "RSA PUBLIC KEY":
		return x509.ParsePKCS1PublicKey(block.Bytes)
	default:
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err == nil {
			publicKey, ok := parsed.(*rsa.PublicKey)
			if !ok {
				return nil, errors.New("public key is not RSA")
			}
			return publicKey, nil
		}
		return x509.ParsePKCS1PublicKey(block.Bytes)
	}
}
