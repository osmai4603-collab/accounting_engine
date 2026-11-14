package main

import (
	"context"
	"log"
	"net/http"
	"strings"

	"github.com/osmai4603-collab/accounting_engine/internal/serviceauth"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
)

// trustedServiceSource is stamped on every authenticated request. Client
// headers never override it while the authentication boundary is active.
const trustedServiceSource = "standalone-service"

// newServiceVerifier builds the transport-neutral verifier from service
// configuration. A nil verifier means the boundary is disabled (development or
// test only; production validation rejects that mode).
func newServiceVerifier(cfg Config) (serviceauth.Verifier, error) {
	return serviceauth.NewVerifier(cfg.authConfig())
}

func principalFromIdentity(identity serviceauth.Identity) publicapplication.Principal {
	companies := make([]types.CompanyID, len(identity.CompanyIDs))
	copy(companies, identity.CompanyIDs)
	return publicapplication.Principal{
		ActorID:    identity.ActorID,
		TenantID:   identity.TenantID,
		CompanyIDs: companies,
	}
}

// authorizeCompany rejects a requested company that the verified principal was
// not granted. When no principal was injected the boundary is disabled and the
// historical embedded behaviour is preserved.
func authorizeCompany(ctx context.Context, companyID types.CompanyID) error {
	principal, ok := publicapplication.PrincipalFromContext(ctx)
	if !ok {
		return nil
	}
	for _, allowed := range principal.CompanyIDs {
		if allowed == companyID {
			return nil
		}
	}
	return publicapplication.NewError(publicapplication.ErrForbidden, "company access denied", nil)
}

type httpAuthBoundary struct {
	verifier  serviceauth.Verifier
	challenge string
}

func newHTTPAuthBoundary(cfg Config, verifier serviceauth.Verifier) *httpAuthBoundary {
	if verifier == nil {
		return nil
	}
	return &httpAuthBoundary{verifier: verifier, challenge: authChallenge(cfg.AuthMode)}
}

func authChallenge(mode string) string {
	switch serviceauth.NormalizeMode(mode) {
	case serviceauth.ModeGatewayHMAC:
		return "HMAC-SHA256"
	case serviceauth.ModeJWTHS256, serviceauth.ModeJWTRS256:
		return "Bearer"
	default:
		return "Bearer"
	}
}

// middleware protects /api/v1/* only. Health, readiness, and metrics remain
// open so orchestrators and probes do not require credentials.
func (b *httpAuthBoundary) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		identity, err := b.verifier.Verify(r.Context(), httpCredentials(r))
		if err != nil {
			if b.challenge != "" {
				w.Header().Set("WWW-Authenticate", b.challenge)
			}
			writeHTTPError(w, r, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		ctx := publicapplication.WithPrincipal(r.Context(), principalFromIdentity(identity))
		ctx = publicapplication.WithRequestMetadata(ctx, trustedRequestMetadata(r))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func httpCredentials(r *http.Request) serviceauth.Credentials {
	headers := make(map[string]string, 7)
	for _, name := range []string{
		serviceauth.HeaderActorID,
		serviceauth.HeaderCompanyIDs,
		serviceauth.HeaderTenantID,
		serviceauth.HeaderTimestamp,
		serviceauth.HeaderSignature,
		serviceauth.HeaderRequestID,
		serviceauth.HeaderAuthorization,
	} {
		if value := r.Header.Get(name); value != "" {
			headers[strings.ToLower(name)] = value
		}
	}
	return serviceauth.Credentials{
		Authorization: r.Header.Get(serviceauth.HeaderAuthorization),
		Headers:       headers,
	}
}

func trustedRequestMetadata(r *http.Request) publicapplication.RequestMetadata {
	correlationID := strings.TrimSpace(r.Header.Get("X-Correlation-ID"))
	if correlationID == "" {
		correlationID = strings.TrimSpace(r.Header.Get("X-Request-ID"))
	}
	return publicapplication.RequestMetadata{
		CorrelationID:  correlationID,
		IdempotencyKey: strings.TrimSpace(r.Header.Get("Idempotency-Key")),
		SourceSystem:   trustedServiceSource,
	}
}

// withHTTPRequestMetadata merges transport headers with metadata already set
// by the authentication boundary, filling only missing fields. When the
// boundary is disabled the result is identical to the historical behaviour.
func withHTTPRequestMetadata(r *http.Request, fallbackSource string) context.Context {
	ctx := r.Context()
	metadata, _ := publicapplication.RequestMetadataFromContext(ctx)
	incoming := requestMetadataFromHTTP(r, fallbackSource)
	if metadata.CorrelationID == "" {
		metadata.CorrelationID = incoming.CorrelationID
	}
	if metadata.IdempotencyKey == "" {
		metadata.IdempotencyKey = incoming.IdempotencyKey
	}
	if metadata.SourceSystem == "" {
		metadata.SourceSystem = incoming.SourceSystem
	}
	return publicapplication.WithRequestMetadata(ctx, metadata)
}

// configurationErrorHandler makes a misconfigured secure boundary fail closed
// instead of silently serving anonymous traffic.
type configurationErrorHandler struct {
	reason error
}

func (h configurationErrorHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	writeHTTPError(w, r, http.StatusServiceUnavailable, "service_unavailable", "service is misconfigured")
}

func logAuthConfigurationError(err error) {
	log.Printf("Service authentication configuration is invalid; refusing to serve requests: %v", err)
}
