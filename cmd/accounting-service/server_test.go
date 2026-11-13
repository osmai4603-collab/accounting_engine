package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPHandlersReturnBadRequestForInvalidInput(t *testing.T) {
	handler := newHTTPServer(Config{}, nil).Handler
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			name:   "invalid company UUID",
			method: http.MethodGet,
			path:   "/api/v1/trial-balance?company_id=not-a-uuid",
		},
		{
			name:   "invalid event UUID",
			method: http.MethodPost,
			path:   "/api/v1/events",
			body:   `{"CompanyID":"not-a-uuid"}`,
		},
		{
			name:   "malformed JSON",
			method: http.MethodPost,
			path:   "/api/v1/entries",
			body:   `{"Description":`,
		},
		{
			name:   "non-object JSON",
			method: http.MethodPost,
			path:   "/api/v1/entries",
			body:   `null`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body)
			}
		})
	}
}

func TestHTTPHandlersRejectOversizedRequestBody(t *testing.T) {
	handler := newHTTPServer(Config{}, nil).Handler
	body := `{"Description":"` + strings.Repeat("x", maxRequestBodyBytes) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/entries", strings.NewReader(body))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusRequestEntityTooLarge, response.Body)
	}
}

func TestHTTPServerTimeoutConfiguration(t *testing.T) {
	server := newHTTPServer(Config{}, nil)
	if server.ReadHeaderTimeout != 5*time.Second {
		t.Errorf("ReadHeaderTimeout = %s, want 5s", server.ReadHeaderTimeout)
	}
	if server.ReadTimeout != 15*time.Second {
		t.Errorf("ReadTimeout = %s, want 15s", server.ReadTimeout)
	}
	if server.WriteTimeout != 30*time.Second {
		t.Errorf("WriteTimeout = %s, want 30s", server.WriteTimeout)
	}
	if server.IdleTimeout != 120*time.Second {
		t.Errorf("IdleTimeout = %s, want 120s", server.IdleTimeout)
	}
}

func TestHTTPHeaderReadTimeoutClosesSlowConnection(t *testing.T) {
	server := &http.Server{
		Handler:           http.NotFoundHandler(),
		ReadHeaderTimeout: 25 * time.Millisecond,
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- serveHTTP(ctx, server, listener)
	}()

	connection, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if _, err := io.WriteString(connection, "GET / HTTP/1.1\r\nHost: test\r\n"); err != nil {
		cancel()
		_ = connection.Close()
		t.Fatal(err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(time.Second))
	_, readErr := io.ReadAll(connection)
	_ = connection.Close()
	cancel()
	if readErr != nil {
		t.Fatalf("slow connection did not close after header timeout: %v", readErr)
	}
	if err := <-serverErrors; err != nil {
		t.Fatalf("serveHTTP returned error: %v", err)
	}
}

func TestHTTPServerGracefulShutdownDrainsActiveRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = fmt.Fprint(w, "finished")
	})
	server := newHTTPServer(Config{}, nil)
	server.Handler = handler
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- serveHTTP(ctx, server, listener)
	}()

	responseErrors := make(chan error, 1)
	go func() {
		response, requestErr := http.Get("http://" + listener.Addr().String())
		if requestErr == nil {
			_ = response.Body.Close()
		}
		responseErrors <- requestErr
	}()

	<-started
	cancel()
	select {
	case err := <-serverErrors:
		t.Fatalf("shutdown returned before active request completed: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	close(release)
	if err := <-serverErrors; err != nil {
		t.Fatalf("serveHTTP returned error: %v", err)
	}
	if err := <-responseErrors; err != nil {
		t.Fatalf("active request failed during shutdown: %v", err)
	}
}
