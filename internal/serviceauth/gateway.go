package serviceauth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
)

const (
	gatewayCanonicalVersion = "accounting-gateway-hmac-v1"
	maxIdentityFieldLength  = 256
)

// GatewayClaims is the canonical payload a trusted gateway signs. CompanyIDs
// are sorted before signing so ordering never affects the signature.
type GatewayClaims struct {
	ActorID    string
	TenantID   string
	CompanyIDs []types.CompanyID
	Timestamp  time.Time
	RequestID  string
}

// GatewayCanonicalString renders the exact bytes covered by the gateway HMAC.
// The timestamp is canonicalized to Unix seconds.
func GatewayCanonicalString(claims GatewayClaims) string {
	companies := make([]string, 0, len(claims.CompanyIDs))
	for _, companyID := range claims.CompanyIDs {
		companies = append(companies, uuid.UUID(companyID).String())
	}
	sort.Strings(companies)

	return strings.Join([]string{
		gatewayCanonicalVersion,
		strings.TrimSpace(claims.ActorID),
		strings.TrimSpace(claims.TenantID),
		strings.Join(companies, ","),
		strconv.FormatInt(claims.Timestamp.Unix(), 10),
		strings.TrimSpace(claims.RequestID),
	}, "\n")
}

// SignGatewayHMAC returns the lower-case hex HMAC-SHA256 signature for claims.
// It is exposed so gateway implementers and tests can produce matching headers.
func SignGatewayHMAC(secret string, claims GatewayClaims) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(GatewayCanonicalString(claims)))
	return hex.EncodeToString(mac.Sum(nil))
}

// GatewayHMACVerifier verifies identity headers signed by a trusted gateway.
//
// Replay limitation: the timestamp check only bounds a request to the clock
// skew window; this verifier is stateless and does not track nonces or request
// IDs. A gateway (or an upstream replay cache) must reject replayed requests
// within that window.
type GatewayHMACVerifier struct {
	secret    []byte
	clockSkew time.Duration
	now       func() time.Time
}

// Verify implements Verifier.
func (v *GatewayHMACVerifier) Verify(_ context.Context, credentials Credentials) (Identity, error) {
	actorID, ok := trimmedField(credentials.Header(HeaderActorID), maxIdentityFieldLength)
	if !ok {
		return Identity{}, errMissingCredential("missing actor identity header")
	}

	rawCompanies := strings.TrimSpace(credentials.Header(HeaderCompanyIDs))
	if rawCompanies == "" {
		return Identity{}, errMissingCredential("missing company identity header")
	}
	companies, err := parseCompanyIDList(rawCompanies)
	if err != nil {
		return Identity{}, err
	}

	tenantID := strings.TrimSpace(credentials.Header(HeaderTenantID))
	if len(tenantID) > maxIdentityFieldLength {
		return Identity{}, errInvalidPrincipal("tenant identity header is too long")
	}
	requestID := strings.TrimSpace(credentials.Header(HeaderRequestID))
	if len(requestID) > maxIdentityFieldLength {
		return Identity{}, errInvalidPrincipal("request identity header is too long")
	}

	rawTimestamp := strings.TrimSpace(credentials.Header(HeaderTimestamp))
	if rawTimestamp == "" {
		return Identity{}, errMissingCredential("missing request timestamp header")
	}
	timestamp, err := parseGatewayTimestamp(rawTimestamp)
	if err != nil {
		return Identity{}, errUnauthenticated("invalid request timestamp")
	}

	now := v.now().UTC()
	if now.Sub(timestamp) > v.clockSkew {
		return Identity{}, errUnauthenticated("request timestamp expired")
	}
	if timestamp.Sub(now) > v.clockSkew {
		return Identity{}, errUnauthenticated("request timestamp is in the future")
	}

	rawSignature := strings.TrimSpace(credentials.Header(HeaderSignature))
	if rawSignature == "" {
		return Identity{}, errMissingCredential("missing request signature header")
	}
	submitted, err := hex.DecodeString(rawSignature)
	if err != nil {
		return Identity{}, errUnauthenticated("invalid request signature")
	}

	expected := SignGatewayHMAC(string(v.secret), GatewayClaims{
		ActorID:    actorID,
		TenantID:   tenantID,
		CompanyIDs: companies,
		Timestamp:  timestamp,
		RequestID:  requestID,
	})
	expectedBytes, err := hex.DecodeString(expected)
	if err != nil {
		return Identity{}, errUnauthenticated("invalid request signature")
	}
	if !hmac.Equal(submitted, expectedBytes) {
		return Identity{}, errUnauthenticated("invalid request signature")
	}

	return Identity{
		ActorID:    actorID,
		TenantID:   tenantID,
		CompanyIDs: deduplicateCompanies(companies),
		Source:     SourceGatewayHMAC,
	}, nil
}

func parseGatewayTimestamp(raw string) (time.Time, error) {
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return time.Unix(seconds, 0).UTC(), nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, errors.New("unsupported timestamp format")
}

// parseCompanyIDList parses a comma-separated list of UUIDs and rejects empty
// or invalid entries. Ordering and duplicates are preserved so the signature is
// verified over exactly what the gateway sent.
func parseCompanyIDList(raw string) ([]types.CompanyID, error) {
	parts := strings.Split(raw, ",")
	companies := make([]types.CompanyID, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		parsed, err := uuid.Parse(trimmed)
		if err != nil {
			return nil, errInvalidPrincipal("company identity header contains a non-UUID value")
		}
		companies = append(companies, types.CompanyID(parsed))
	}
	if len(companies) == 0 {
		return nil, errInvalidPrincipal("at least one company identity is required")
	}
	return companies, nil
}

func deduplicateCompanies(companies []types.CompanyID) []types.CompanyID {
	unique := make([]types.CompanyID, 0, len(companies))
	seen := make(map[uuid.UUID]struct{}, len(companies))
	for _, companyID := range companies {
		if _, duplicate := seen[uuid.UUID(companyID)]; duplicate {
			continue
		}
		seen[uuid.UUID(companyID)] = struct{}{}
		unique = append(unique, companyID)
	}
	return unique
}
