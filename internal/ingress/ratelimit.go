package ingress

import (
	"sync"
	"time"
)

// tokenBucket implements a per-project token bucket rate limiter.
type tokenBucket struct {
	mu       sync.Mutex
	rate     float64 // tokens per second
	capacity float64 // max burst
	buckets  map[string]*bucket
}

type bucket struct {
	tokens   float64
	lastTime time.Time
}

// NewRateLimiter creates a token bucket rate limiter.
// ratePerMinute is the sustained rate per project (requests/minute).
// Burst allows short spikes up to ratePerMinute.
func NewRateLimiter(ratePerMinute int) RateLimiter {
	rate := float64(ratePerMinute) / 60.0
	return &tokenBucket{
		rate:     rate,
		capacity: float64(ratePerMinute),
		buckets:  make(map[string]*bucket),
	}
}

func (tb *tokenBucket) Allow(projectID string) bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	b, ok := tb.buckets[projectID]
	if !ok {
		b = &bucket{
			tokens:   tb.capacity,
			lastTime: now,
		}
		tb.buckets[projectID] = b
	}

	// Refill tokens based on elapsed time.
	elapsed := now.Sub(b.lastTime).Seconds()
	b.tokens += elapsed * tb.rate
	if b.tokens > tb.capacity {
		b.tokens = tb.capacity
	}
	b.lastTime = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
