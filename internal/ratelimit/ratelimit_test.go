package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestAllowRespectsBurstThenBlocks(t *testing.T) {
	l := New(rate.Every(time.Minute), 3, time.Minute)

	for i := 0; i < 3; i++ {
		if !l.Allow("1.2.3.4") {
			t.Fatalf("request %d within the burst was blocked", i)
		}
	}
	if l.Allow("1.2.3.4") {
		t.Fatal("request over the burst was allowed")
	}
}

func TestAllowTracksKeysIndependently(t *testing.T) {
	l := New(rate.Every(time.Minute), 1, time.Minute)

	if !l.Allow("1.1.1.1") {
		t.Fatal("first request for 1.1.1.1 was blocked")
	}
	if !l.Allow("2.2.2.2") {
		t.Fatal("first request for a different IP was blocked by the other IP's bucket")
	}
	if l.Allow("1.1.1.1") {
		t.Fatal("second request for 1.1.1.1 was allowed within the burst of one")
	}
}

func TestSweepForgetsStaleEntries(t *testing.T) {
	l := New(rate.Every(time.Minute), 1, time.Minute)
	now := time.Now()
	l.now = func() time.Time { return now }

	l.Allow("1.2.3.4")
	if len(l.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(l.entries))
	}

	now = now.Add(2 * time.Minute)
	l.Allow("5.6.7.8") // triggers a sweep as a side effect

	if _, ok := l.entries["1.2.3.4"]; ok {
		t.Fatal("stale entry was not swept")
	}
	if _, ok := l.entries["5.6.7.8"]; !ok {
		t.Fatal("fresh entry was swept")
	}
}

func TestMiddlewareReturnsTooManyRequests(t *testing.T) {
	l := New(rate.Every(time.Minute), 1, time.Minute)
	called := 0
	h := l.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "9.9.9.9:1234"

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", rec.Code)
	}
	if called != 1 {
		t.Fatalf("handler called %d times, want 1", called)
	}
}

func TestClientIPPrefersForwardedFor(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		forwarded  string
		want       string
	}{
		{"no proxy header", "203.0.113.5:4321", "", "203.0.113.5"},
		{"single proxy hop", "127.0.0.1:4321", "198.51.100.7", "198.51.100.7"},
		{"multiple proxy hops uses the original client", "127.0.0.1:4321", "198.51.100.7, 10.0.0.1", "198.51.100.7"},
		{"malformed remote addr falls back unchanged", "not-an-addr", "", "not-an-addr"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.forwarded != "" {
				req.Header.Set("X-Forwarded-For", tt.forwarded)
			}
			if got := ClientIP(req); got != tt.want {
				t.Fatalf("ClientIP = %q, want %q", got, tt.want)
			}
		})
	}
}
