package observability

import (
	"context"

	"github.com/google/uuid"
)

type companyIDContextKey struct{}
type entryIDContextKey struct{}

// WithCompanyID attaches the tenant/company identifier to the context. A nil
// UUID clears the value.
func WithCompanyID(ctx context.Context, companyID uuid.UUID) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, companyIDContextKey{}, companyID)
}

// CompanyIDFromContext returns the company identifier carried by the context.
func CompanyIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	if ctx == nil {
		return uuid.Nil, false
	}
	companyID, ok := ctx.Value(companyIDContextKey{}).(uuid.UUID)
	if !ok || companyID == uuid.Nil {
		return uuid.Nil, false
	}
	return companyID, true
}

// WithEntryID attaches the journal entry identifier to the context.
func WithEntryID(ctx context.Context, entryID uuid.UUID) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, entryIDContextKey{}, entryID)
}

// EntryIDFromContext returns the journal entry identifier carried by the
// context.
func EntryIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	if ctx == nil {
		return uuid.Nil, false
	}
	entryID, ok := ctx.Value(entryIDContextKey{}).(uuid.UUID)
	if !ok || entryID == uuid.Nil {
		return uuid.Nil, false
	}
	return entryID, true
}

// WithFinancialScope is a convenience helper that attaches the company, entry,
// and trace identifiers used by every financial log record.
func WithFinancialScope(ctx context.Context, companyID, entryID uuid.UUID, traceparent string) context.Context {
	ctx = WithCompanyID(ctx, companyID)
	ctx = WithEntryID(ctx, entryID)
	ctx = ContextWithTraceparent(ctx, traceparent)
	return ctx
}
