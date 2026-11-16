package storage

import (
	"context"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
)

type contextKey string

const (
	companyContextKey contextKey = "storage.company_id"
	actorContextKey   contextKey = "storage.actor_id"
)

// WithCompanyContext attaches the active company ID to the context.
// Storage adapters must extract this value and enforce tenant boundaries
// (e.g. through PostgreSQL RLS or in-memory tenant isolation). Adapters must
// ensure tenant state is cleaned up prior to returning connections to any pool.
func WithCompanyContext(ctx context.Context, companyID types.CompanyID) context.Context {
	return context.WithValue(ctx, companyContextKey, companyID)
}

// CompanyFromContext extracts the active company ID from the context if present.
func CompanyFromContext(ctx context.Context) (types.CompanyID, bool) {
	if ctx == nil {
		return types.CompanyID{}, false
	}
	val, ok := ctx.Value(companyContextKey).(types.CompanyID)
	return val, ok
}

// WithActorContext attaches the authenticated actor ID to the context.
// Adapters may persist this actor ID in audit trails or transaction metadata.
func WithActorContext(ctx context.Context, actorID string) context.Context {
	return context.WithValue(ctx, actorContextKey, actorID)
}

// ActorFromContext extracts the active actor ID from the context if present.
func ActorFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	val, ok := ctx.Value(actorContextKey).(string)
	return val, ok
}
