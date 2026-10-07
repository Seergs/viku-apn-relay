// Package ratelimit throttles HTTP requests per client IP.
package ratelimit

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// entry is one client's token bucket.
type entry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// Limiter enforces a per-IP request rate. The zero value is not usable; call
// New. It is safe for concurrent use.
type Limiter struct {
	mu    sync.Mutex
	rate  rate.Limit
	burst int
	ttl   time.Duration

	entries   map[string]*entry
	lastSweep time.Time
	now       func() time.Time
}

// New returns a Limiter that allows r requests per second per IP, with a
// burst of burst requests. An IP with no requests for longer than ttl is
// forgotten, so memory does not grow without bound.
func New(r rate.Limit, burst int, ttl time.Duration) *Limiter {
	return &Limiter{
		rate:    r,
		burst:   burst,
		ttl:     ttl,
		entries: make(map[string]*entry),
		now:     time.Now,
	}
}

// Allow reports whether a request from key may proceed. It also sweeps
// entries older than the TTL, so no background goroutine is needed.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.sweep(now)

	e, ok := l.entries[key]
	if !ok {
		e = &entry{limiter: rate.NewLimiter(l.rate, l.burst)}
		l.entries[key] = e
	}
	e.lastSeen = now
	return e.limiter.AllowN(now, 1)
}

// sweep removes entries not seen within the TTL. Callers hold l.mu.
func (l *Limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < l.ttl {
		return
	}
	l.lastSweep = now
	for key, e := range l.entries {
		if now.Sub(e.lastSeen) > l.ttl {
			delete(l.entries, key)
		}
	}
}

// Middleware answers 429 for a request whose client IP is over the limit,
// and calls next otherwise.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.Allow(ClientIP(r)) {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ClientIP returns the originating client address. The relay sits behind a
// reverse proxy, so every connection otherwise looks like it comes from the
// proxy: it trusts CF-Connecting-IP (set by Cloudflare, overwriting any value
// a client sends) over X-Forwarded-For (set by Caddy, but only appended to,
// so a client reaching Caddy directly could prepend a fake one) over the TCP
// peer address.
//
// This is only safe to trust as long as the origin firewall accepts
// connections on 80/443 from Cloudflare's ranges alone; otherwise a request
// straight to the VPS skips Cloudflare and forges both headers.
func ClientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); ip != "" {
		return ip
	}
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		first, _, _ := strings.Cut(fwd, ",")
		if ip := strings.TrimSpace(first); ip != "" {
			return ip
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
