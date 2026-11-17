package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func TestLimiterSeparatesReadWriteAndRefillsTokens(t *testing.T) {
	config := Config{
		Enabled: true, ReadLimit: 2, WriteLimit: 1,
		ClientReadLimit: 100, ClientWriteLimit: 100, Window: time.Second, MaxKeys: 10,
	}
	limiter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Unix(100, 0)

	if allowed, _ := limiter.AllowAt("tenant:a", Read, start); !allowed {
		t.Fatal("first read request was rejected")
	}
	if allowed, _ := limiter.AllowAt("tenant:a", Read, start); !allowed {
		t.Fatal("second read request was rejected")
	}
	if allowed, retry := limiter.AllowAt("tenant:a", Read, start); allowed || retry <= 0 {
		t.Fatalf("read limit result = (%v, %s), want rejection and positive retry", allowed, retry)
	}
	if allowed, _ := limiter.AllowAt("tenant:a", Write, start); !allowed {
		t.Fatal("write bucket was not independent from read bucket")
	}
	if allowed, retry := limiter.AllowAt("tenant:a", Write, start); allowed || retry <= 0 {
		t.Fatalf("write limit result = (%v, %s), want rejection and positive retry", allowed, retry)
	}
	if allowed, _ := limiter.AllowAt("tenant:a", Write, start.Add(time.Second)); !allowed {
		t.Fatal("write bucket did not refill")
	}
}

func TestLimiterBoundsKeysAndReclaimsIdleBuckets(t *testing.T) {
	config := Config{
		Enabled: true, ReadLimit: 1, WriteLimit: 1,
		ClientReadLimit: 100, ClientWriteLimit: 100, Window: time.Second, MaxKeys: 1,
	}
	limiter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Unix(200, 0)

	if allowed, _ := limiter.AllowAt("client:a", Read, start); !allowed {
		t.Fatal("first client was rejected")
	}
	if allowed, retry := limiter.AllowAt("client:b", Read, start); allowed || retry <= 0 {
		t.Fatalf("capacity result = (%v, %s), want fail-closed rejection", allowed, retry)
	}
	if allowed, _ := limiter.AllowAt("client:b", Read, start.Add(2*time.Second)); !allowed {
		t.Fatal("idle bucket was not reclaimed")
	}
}

func TestLimiterIsSafeForConcurrentCallers(t *testing.T) {
	config := Config{
		Enabled: true, ReadLimit: 100, WriteLimit: 1,
		ClientReadLimit: 100, ClientWriteLimit: 100, Window: time.Minute, MaxKeys: 10,
	}
	limiter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}

	const callers = 200
	var allowed int
	var mu sync.Mutex
	var wait sync.WaitGroup
	for i := 0; i < callers; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if ok, _ := limiter.Allow("client:a", Read); ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wait.Wait()
	if allowed != config.ReadLimit {
		t.Fatalf("allowed %d concurrent requests, want %d", allowed, config.ReadLimit)
	}
}

func TestClientGuardHasIndependentHigherBudget(t *testing.T) {
	config := Config{
		Enabled: true, ReadLimit: 1, WriteLimit: 1,
		ClientReadLimit: 2, ClientWriteLimit: 2, Window: time.Minute, MaxKeys: 10,
	}
	limiter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < config.ClientReadLimit; i++ {
		if allowed, _ := limiter.AllowClient("192.0.2.1", Read); !allowed {
			t.Fatalf("client guard rejected request %d before reaching its budget", i+1)
		}
	}
	if allowed, retry := limiter.AllowClient("192.0.2.1", Read); allowed || retry <= 0 {
		t.Fatalf("client guard result = (%v, %s), want rejection and positive retry", allowed, retry)
	}
	if allowed, _ := limiter.Allow("tenant:a", Read); !allowed {
		t.Fatal("client guard exhausted the independent tenant budget")
	}
}

func TestLimiterRejectsInvalidConfiguration(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected invalid empty configuration to be rejected")
	}
}
