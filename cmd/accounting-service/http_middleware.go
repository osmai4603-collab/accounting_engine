package main

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	httpRequestTimeout = 25 * time.Second
)

var correlationIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

type correlationIDKey struct{}

type httpAPIError struct {
	Error         httpAPIErrorDetail `json:"error"`
	CorrelationID string             `json:"correlation_id"`
}

type httpAPIErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func newHTTPRequestContextMiddleware(requireTLS bool, timeout time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		correlationID := strings.TrimSpace(r.Header.Get("X-Correlation-ID"))
		if correlationID == "" {
			correlationID = strings.TrimSpace(r.Header.Get("X-Request-ID"))
		}
		if !correlationIDPattern.MatchString(correlationID) {
			correlationID = uuid.NewString()
		}

		w.Header().Set("X-Correlation-ID", correlationID)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("Cache-Control", "no-store")
		if requireTLS || r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		r.Header.Set("X-Correlation-ID", correlationID)
		ctx := context.WithValue(r.Context(), correlationIDKey{}, correlationID)
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func correlationIDFromRequest(r *http.Request) string {
	if id, ok := r.Context().Value(correlationIDKey{}).(string); ok && id != "" {
		return id
	}
	if id := strings.TrimSpace(r.Header.Get("X-Correlation-ID")); correlationIDPattern.MatchString(id) {
		return id
	}
	return uuid.NewString()
}

func writeHTTPError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	correlationID := correlationIDFromRequest(r)
	w.Header().Set("X-Correlation-ID", correlationID)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(httpAPIError{
		Error:         httpAPIErrorDetail{Code: code, Message: message},
		CorrelationID: correlationID,
	})
}
