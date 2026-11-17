package middleware

import (
	"encoding/json"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/ratelimit"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
)

type rateLimitError struct {
	Error         rateLimitErrorDetail `json:"error"`
	CorrelationID string               `json:"correlation_id"`
}

type rateLimitErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func RateLimit(limiter *ratelimit.Limiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !limiter.Enabled() || !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			next.ServeHTTP(w, r)
			return
		}

		allowed, retryAfter := limiter.Allow(httpRateLimitKey(r), httpRequestClass(r))
		if !allowed {
			writeRateLimitError(w, r, retryAfter)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func ClientIPRateLimit(limiter *ratelimit.Limiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !limiter.Enabled() || !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		allowed, retryAfter := limiter.AllowClient(httpClientIP(r), httpRequestClass(r))
		if !allowed {
			writeRateLimitError(w, r, retryAfter)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeRateLimitError(w http.ResponseWriter, r *http.Request, retryAfter time.Duration) {
	seconds := int(math.Ceil(retryAfter.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(rateLimitError{
		Error:         rateLimitErrorDetail{Code: "rate_limit_exceeded", Message: "request rate limit exceeded"},
		CorrelationID: r.Header.Get("X-Correlation-ID"),
	})
}

func httpRateLimitKey(r *http.Request) string {
	if principal, ok := publicapplication.PrincipalFromContext(r.Context()); ok && principal.TenantID != "" {
		return "tenant:" + principal.TenantID
	}
	return "client:" + httpClientIP(r)
}

func httpClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	if ip := net.ParseIP(strings.Trim(r.RemoteAddr, "[]")); ip != nil {
		return ip.String()
	}
	return "unknown"
}

func httpRequestClass(r *http.Request) ratelimit.Class {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return ratelimit.Read
	}
	return ratelimit.Write
}
