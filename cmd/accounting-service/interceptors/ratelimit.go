package interceptors

import (
	"context"
	"math"
	"net"
	"strconv"
	"strings"

	"github.com/osmai4603-collab/accounting_engine/internal/ratelimit"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func UnaryRateLimit(limiter *ratelimit.Limiter) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := allow(ctx, limiter, info.FullMethod); err != nil {
			return nil, err
		}
		return handler(ctx, request)
	}
}

func ClientUnaryRateLimit(limiter *ratelimit.Limiter) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := allowClient(ctx, limiter, grpcMethodClass(info.FullMethod)); err != nil {
			return nil, err
		}
		return handler(ctx, request)
	}
}

func StreamRateLimit(limiter *ratelimit.Limiter) grpc.StreamServerInterceptor {
	return func(service any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := allow(stream.Context(), limiter, info.FullMethod); err != nil {
			return err
		}
		return handler(service, stream)
	}
}

func ClientStreamRateLimit(limiter *ratelimit.Limiter) grpc.StreamServerInterceptor {
	return func(service any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := allowClient(stream.Context(), limiter, grpcMethodClass(info.FullMethod)); err != nil {
			return err
		}
		return handler(service, stream)
	}
}

func allow(ctx context.Context, limiter *ratelimit.Limiter, method string) error {
	if !limiter.Enabled() {
		return nil
	}
	allowed, retryAfter := limiter.Allow(grpcRateLimitKey(ctx), grpcMethodClass(method))
	if allowed {
		return nil
	}
	seconds := int(math.Ceil(retryAfter.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	grpc.SetTrailer(ctx, metadata.Pairs("retry-after", strconv.Itoa(seconds)))
	return status.Errorf(codes.ResourceExhausted, "request rate limit exceeded; retry after %d seconds", seconds)
}

func allowClient(ctx context.Context, limiter *ratelimit.Limiter, class ratelimit.Class) error {
	if !limiter.Enabled() {
		return nil
	}
	allowed, retryAfter := limiter.AllowClient(grpcClientIP(ctx), class)
	if allowed {
		return nil
	}
	seconds := int(math.Ceil(retryAfter.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	grpc.SetTrailer(ctx, metadata.Pairs("retry-after", strconv.Itoa(seconds)))
	return status.Errorf(codes.ResourceExhausted, "client request rate limit exceeded; retry after %d seconds", seconds)
}

func grpcRateLimitKey(ctx context.Context) string {
	if principal, ok := publicapplication.PrincipalFromContext(ctx); ok && principal.TenantID != "" {
		return "tenant:" + principal.TenantID
	}
	return "client:" + grpcClientIP(ctx)
}

func grpcClientIP(ctx context.Context) string {
	if remote, ok := peer.FromContext(ctx); ok && remote.Addr != nil {
		host, _, err := net.SplitHostPort(remote.Addr.String())
		if err == nil {
			return host
		}
		if ip := net.ParseIP(strings.Trim(remote.Addr.String(), "[]")); ip != nil {
			return ip.String()
		}
	}
	return "unknown"
}

func grpcMethodClass(method string) ratelimit.Class {
	switch {
	case strings.HasSuffix(method, "/PostEntry"),
		strings.HasSuffix(method, "/ReverseEntry"),
		strings.HasSuffix(method, "/ProcessEvent"):
		return ratelimit.Write
	default:
		return ratelimit.Read
	}
}
