package interceptors

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/ratelimit"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type rateLimitTestStream struct {
	ctx context.Context
}

func (s rateLimitTestStream) SetHeader(metadata.MD) error  { return nil }
func (s rateLimitTestStream) SendHeader(metadata.MD) error { return nil }
func (s rateLimitTestStream) SetTrailer(metadata.MD)       {}
func (s rateLimitTestStream) Context() context.Context     { return s.ctx }
func (s rateLimitTestStream) SendMsg(any) error            { return nil }
func (s rateLimitTestStream) RecvMsg(any) error            { return io.EOF }

func TestStreamRateLimitRejectsExcessRequests(t *testing.T) {
	limiter, err := ratelimit.New(ratelimit.Config{
		Enabled: true, ReadLimit: 1, WriteLimit: 1,
		ClientReadLimit: 100, ClientWriteLimit: 100, Window: time.Minute, MaxKeys: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	interceptor := StreamRateLimit(limiter)
	stream := rateLimitTestStream{ctx: context.Background()}
	info := &grpc.StreamServerInfo{FullMethod: "/example.Service/Watch"}
	calls := 0
	handler := func(any, grpc.ServerStream) error {
		calls++
		return nil
	}

	if err := interceptor(nil, stream, info, handler); err != nil {
		t.Fatalf("first stream call: %v", err)
	}
	err = interceptor(nil, stream, info, handler)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second stream status = %s, want ResourceExhausted; err=%v", status.Code(err), err)
	}
	if calls != 1 {
		t.Fatalf("stream handler calls = %d, want 1", calls)
	}
}

func TestClientStreamRateLimitRejectsExcessRequests(t *testing.T) {
	config := ratelimit.DefaultConfig()
	config.ClientReadLimit = 1
	limiter, err := ratelimit.New(config)
	if err != nil {
		t.Fatal(err)
	}

	interceptor := ClientStreamRateLimit(limiter)
	stream := rateLimitTestStream{ctx: context.Background()}
	info := &grpc.StreamServerInfo{FullMethod: "/example.Service/Watch"}
	calls := 0
	handler := func(any, grpc.ServerStream) error {
		calls++
		return nil
	}

	if err := interceptor(nil, stream, info, handler); err != nil {
		t.Fatalf("first client stream call: %v", err)
	}
	err = interceptor(nil, stream, info, handler)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second client stream status = %s, want ResourceExhausted; err=%v", status.Code(err), err)
	}
	if calls != 1 {
		t.Fatalf("client stream handler calls = %d, want 1", calls)
	}
}

func TestUnaryRateLimitIsolatesTenants(t *testing.T) {
	config := ratelimit.DefaultConfig()
	config.ReadLimit = 1
	limiter, err := ratelimit.New(config)
	if err != nil {
		t.Fatal(err)
	}
	interceptor := UnaryRateLimit(limiter)
	info := &grpc.UnaryServerInfo{FullMethod: "/example.Service/Read"}
	handler := func(context.Context, any) (any, error) { return nil, nil }
	call := func(tenant string) error {
		ctx := publicapplication.WithPrincipal(context.Background(), publicapplication.Principal{TenantID: tenant})
		_, err := interceptor(ctx, nil, info, handler)
		return err
	}

	if err := call("tenant-a"); err != nil {
		t.Fatalf("first tenant-a request: %v", err)
	}
	if err := call("tenant-a"); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second tenant-a status = %s, want ResourceExhausted; err=%v", status.Code(err), err)
	}
	if err := call("tenant-b"); err != nil {
		t.Fatalf("tenant-b should have an independent bucket: %v", err)
	}
}
