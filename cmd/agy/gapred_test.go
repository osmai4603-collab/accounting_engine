//go:build gapred

// Red test for GAP-OPS-01: the `agy serve` process exposes only /healthz and
// /metrics. A service must expose a readiness probe (and authentication/TLS/
// request limits) before production. Tracked by P4-T1.
package main

import "testing"

func TestGAP_OPS_01_ServiceExposesReadinessProbe(t *testing.T) {
	t.Skip("GAP-OPS-01: serve.go has no /readyz, auth, TLS, or request limits; tracked by P4-T1")
}
