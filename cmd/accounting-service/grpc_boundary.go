package main

import (
	"context"
	"strings"

	"github.com/osmai4603-collab/accounting_engine/internal/serviceauth"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type grpcAuthBoundary struct {
	verifier serviceauth.Verifier
}

func (b *grpcAuthBoundary) unaryInterceptor(
	ctx context.Context,
	request any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	authenticated, err := b.authenticate(ctx)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	return handler(authenticated, request)
}

func (b *grpcAuthBoundary) streamInterceptor(
	service any,
	stream grpc.ServerStream,
	info *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) error {
	authenticated, err := b.authenticate(stream.Context())
	if err != nil {
		return status.Error(codes.Unauthenticated, "authentication required")
	}
	return handler(service, authenticatedServerStream{ServerStream: stream, ctx: authenticated})
}

type authenticatedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s authenticatedServerStream) Context() context.Context { return s.ctx }

func (b *grpcAuthBoundary) authenticate(ctx context.Context) (context.Context, error) {
	identity, err := b.verifier.Verify(ctx, grpcCredentials(ctx))
	if err != nil {
		return nil, err
	}
	ctx = publicapplication.WithPrincipal(ctx, principalFromIdentity(identity))
	ctx = publicapplication.WithRequestMetadata(ctx, trustedGRPCMetadata(ctx))
	return ctx, nil
}

func grpcCredentials(ctx context.Context) serviceauth.Credentials {
	headers := grpcIncomingValues(ctx)
	credentials := serviceauth.Credentials{Headers: headers}
	if authorization := headers[strings.ToLower(serviceauth.HeaderAuthorization)]; authorization != "" {
		credentials.Authorization = authorization
	}
	return credentials
}

// trustedGRPCMetadata derives non-identity request metadata from incoming
// metadata while preserving anything already injected and always stamping the
// trusted service source.
func trustedGRPCMetadata(ctx context.Context) publicapplication.RequestMetadata {
	incoming := grpcIncomingValues(ctx)
	metadataValue, _ := publicapplication.RequestMetadataFromContext(ctx)
	if metadataValue.CorrelationID == "" {
		metadataValue.CorrelationID = firstNonEmpty(incoming["x-correlation-id"], incoming["x-request-id"])
	}
	if metadataValue.IdempotencyKey == "" {
		metadataValue.IdempotencyKey = incoming["idempotency-key"]
	}
	if metadataValue.SourceSystem == "" {
		metadataValue.SourceSystem = trustedServiceSource
	}
	return metadataValue
}

func grpcIncomingValues(ctx context.Context) map[string]string {
	incoming, _ := metadata.FromIncomingContext(ctx)
	values := make(map[string]string, len(incoming))
	for key, list := range incoming {
		lower := strings.ToLower(key)
		if strings.HasSuffix(lower, "-bin") || len(list) == 0 {
			continue
		}
		values[lower] = list[0]
	}
	return values
}
