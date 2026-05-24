package httpapi

import (
	"testing"
	"time"
)

func TestBrowserRateLimiterLimitsByKeyOriginAndIP(t *testing.T) {
	limiter := newBrowserRateLimiter(2, time.Minute)
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)

	if !limiter.Allow("key-1", "http://localhost:3000", "127.0.0.1", now) {
		t.Fatal("expected first event allowed")
	}
	if !limiter.Allow("key-1", "http://localhost:3000", "127.0.0.1", now.Add(time.Second)) {
		t.Fatal("expected second event allowed")
	}
	if limiter.Allow("key-1", "http://localhost:3000", "127.0.0.1", now.Add(2*time.Second)) {
		t.Fatal("expected third event blocked inside the same bucket")
	}
	if !limiter.Allow("key-1", "http://localhost:3000", "127.0.0.2", now.Add(3*time.Second)) {
		t.Fatal("expected different IP to have a separate bucket")
	}
	if !limiter.Allow("key-1", "http://localhost:3000", "127.0.0.1", now.Add(time.Minute+time.Second)) {
		t.Fatal("expected bucket reset after the rate window")
	}
}
