package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	accountingv1 "github.com/osmai4603-collab/accounting_engine/proto/accounting/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	reflectionv1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
)

func main() {
	httpPort := envPort("HTTP_PORT", "18080")
	grpcPort := envPort("GRPC_PORT", "15051")
	httpAddress := "http://127.0.0.1:" + httpPort

	client := &http.Client{Timeout: 2 * time.Second}
	if err := waitForReadiness(client, httpAddress+"/readyz", 45*time.Second); err != nil {
		fail(err)
	}
	if err := checkHTTP(client, httpAddress); err != nil {
		fail(err)
	}
	if err := checkGRPC(grpcPort); err != nil {
		fail(err)
	}
	fmt.Println("accounting-service PostgreSQL readiness, HTTP, and gRPC checks passed")
}

func envPort(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func waitForReadiness(client *http.Client, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		response, err := client.Get(url)
		if err == nil {
			body, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if readErr == nil && response.StatusCode == http.StatusOK && string(body) == "READY" {
				return nil
			}
			lastErr = fmt.Errorf("GET %s returned %d %q", url, response.StatusCode, body)
		} else {
			lastErr = err
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("service did not become ready within %s: %w", timeout, lastErr)
}

func checkHTTP(client *http.Client, baseURL string) error {
	response, err := client.Get(baseURL + "/healthz")
	if err != nil {
		return fmt.Errorf("GET /healthz: %w", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GET /healthz returned %d, want 200", response.StatusCode)
	}

	response, err = client.Get(baseURL + "/api/v1/trial-balance?company_id=not-a-uuid")
	if err != nil {
		return fmt.Errorf("GET /api/v1/trial-balance: %w", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		return fmt.Errorf("GET /api/v1/trial-balance returned %d, want 400", response.StatusCode)
	}
	return nil
}

func checkGRPC(port string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, "127.0.0.1:"+port,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return fmt.Errorf("connect to gRPC server: %w", err)
	}
	defer conn.Close()

	reflection, err := reflectionv1.NewServerReflectionClient(conn).ServerReflectionInfo(ctx)
	if err != nil {
		return fmt.Errorf("open gRPC reflection stream: %w", err)
	}
	if err := reflection.Send(&reflectionv1.ServerReflectionRequest{
		MessageRequest: &reflectionv1.ServerReflectionRequest_ListServices{ListServices: ""},
	}); err != nil {
		return fmt.Errorf("request gRPC service list: %w", err)
	}
	reflected, err := reflection.Recv()
	if err != nil {
		return fmt.Errorf("receive gRPC service list: %w", err)
	}
	services := make(map[string]bool)
	for _, service := range reflected.GetListServicesResponse().GetService() {
		services[service.GetName()] = true
	}
	if !services["accounting.v1.AccountingService"] {
		return fmt.Errorf("gRPC reflection did not advertise AccountingService")
	}

	_, err = accountingv1.NewAccountingServiceClient(conn).GetEntry(ctx, &accountingv1.GetEntryRequest{})
	if status.Code(err) != codes.InvalidArgument {
		return fmt.Errorf("GetEntry with invalid IDs returned %s, want InvalidArgument", status.Code(err))
	}
	return nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
