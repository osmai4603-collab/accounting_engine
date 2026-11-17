package ratelimit

import (
	"errors"
	"sync"
	"time"
)

type Class uint8

const (
	Read Class = iota + 1
	Write
)

type Config struct {
	Enabled          bool
	ReadLimit        int
	WriteLimit       int
	ClientReadLimit  int
	ClientWriteLimit int
	Window           time.Duration
	MaxKeys          int
}

func DefaultConfig() Config {
	return Config{
		Enabled:          true,
		ReadLimit:        60,
		WriteLimit:       20,
		ClientReadLimit:  600,
		ClientWriteLimit: 200,
		Window:           time.Minute,
		MaxKeys:          10_000,
	}
}

func (c Config) Validate() error {
	if c.ReadLimit <= 0 {
		return errors.New("read request limit must be positive")
	}
	if c.WriteLimit <= 0 {
		return errors.New("write request limit must be positive")
	}
	if c.ClientReadLimit <= 0 {
		return errors.New("client read request limit must be positive")
	}
	if c.ClientWriteLimit <= 0 {
		return errors.New("client write request limit must be positive")
	}
	if c.Window <= 0 {
		return errors.New("rate limit window must be positive")
	}
	if c.MaxKeys <= 0 {
		return errors.New("rate limit maximum keys must be positive")
	}
	return nil
}

type bucket struct {
	tokens    float64
	updatedAt time.Time
	lastSeen  time.Time
}

type Limiter struct {
	config      Config
	mu          sync.Mutex
	buckets     map[string]bucket
	lastCleanup time.Time
}

func New(config Config) (*Limiter, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Limiter{
		config:  config,
		buckets: make(map[string]bucket),
	}, nil
}

func (l *Limiter) Enabled() bool {
	return l != nil && l.config.Enabled
}

func (l *Limiter) Allow(key string, class Class) (bool, time.Duration) {
	limit := l.config.ReadLimit
	if class == Write {
		limit = l.config.WriteLimit
	}
	return l.allowAt("subject:"+key, class, limit, time.Now())
}

func (l *Limiter) AllowAt(key string, class Class, now time.Time) (bool, time.Duration) {
	limit := l.config.ReadLimit
	if class == Write {
		limit = l.config.WriteLimit
	}
	return l.allowAt("subject:"+key, class, limit, now)
}

func (l *Limiter) AllowClient(key string, class Class) (bool, time.Duration) {
	limit := l.config.ClientReadLimit
	if class == Write {
		limit = l.config.ClientWriteLimit
	}
	return l.allowAt("client:"+key, class, limit, time.Now())
}

func (l *Limiter) allowAt(key string, class Class, limit int, now time.Time) (bool, time.Duration) {
	if !l.Enabled() {
		return true, 0
	}
	if key == "" || (class != Read && class != Write) {
		return false, l.config.Window
	}
	rate := float64(limit) / float64(l.config.Window)
	mapKey := "read:" + key
	if class == Write {
		mapKey = "write:" + key
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.lastCleanup) >= l.config.Window {
		expiry := now.Add(-l.config.Window)
		for existingKey, existing := range l.buckets {
			if existing.lastSeen.Before(expiry) {
				delete(l.buckets, existingKey)
			}
		}
		l.lastCleanup = now
	}

	current, exists := l.buckets[mapKey]
	if !exists {
		if len(l.buckets) >= l.config.MaxKeys {
			return false, l.config.Window
		}
		current = bucket{tokens: float64(limit), updatedAt: now, lastSeen: now}
	} else {
		elapsed := now.Sub(current.updatedAt)
		if elapsed > 0 {
			current.tokens += float64(elapsed) * rate
			if current.tokens > float64(limit) {
				current.tokens = float64(limit)
			}
		}
		current.updatedAt = now
		current.lastSeen = now
	}

	if current.tokens >= 1 {
		current.tokens--
		l.buckets[mapKey] = current
		return true, 0
	}

	current.lastSeen = now
	l.buckets[mapKey] = current
	retryAfter := time.Duration((1-current.tokens)/rate) + time.Nanosecond
	return false, retryAfter
}
