// Package middleware — token-bucket rate limiter per client IP (decisions S1/F3).
//
// RateLimit returns middleware that enforces a per-IP request budget using an
// in-memory token bucket. Buckets are lazily created and periodically cleaned up
// to prevent unbounded memory growth from one-off IP addresses.
//
// This is an in-process implementation (pure stdlib, no Redis) appropriate for a
// single-process deployment on fly.io. Multi-region setups should replace with a
// shared store (Redis) but this is the correct starting boundary per decisions.
package middleware

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// bucket holds the token state for one IP address.
type bucket struct {
	mu       sync.Mutex
	tokens   float64
	lastSeen time.Time
	rate     float64 // tokens per second (= perMin / 60.0)
	capacity float64 // maximum tokens
}

// allow removes one token and returns true if the request is permitted.
// It refills tokens proportional to elapsed time since the last call.
func (b *bucket) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(b.lastSeen).Seconds()
	b.lastSeen = now

	// Refill.
	b.tokens += elapsed * b.rate
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// limiter holds all per-IP buckets and handles periodic cleanup.
type limiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	perMin   int
	stopOnce sync.Once
	stop     chan struct{}
}

// newLimiter creates a limiter and starts its cleanup goroutine.
func newLimiter(perMin int) *limiter {
	l := &limiter{
		buckets: make(map[string]*bucket),
		perMin:  perMin,
		stop:    make(chan struct{}),
	}
	go l.cleanup()
	return l
}

// cleanup removes buckets that have been idle for more than 5 minutes.
func (l *limiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			l.mu.Lock()
			cutoff := time.Now().Add(-5 * time.Minute)
			for ip, b := range l.buckets {
				b.mu.Lock()
				if b.lastSeen.Before(cutoff) {
					delete(l.buckets, ip)
				}
				b.mu.Unlock()
			}
			l.mu.Unlock()
		case <-l.stop:
			return
		}
	}
}

// get returns (creating if necessary) the bucket for ip.
func (l *limiter) get(ip string) *bucket {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[ip]
	if !ok {
		rate := float64(l.perMin) / 60.0
		b = &bucket{
			tokens:   float64(l.perMin), // start full
			lastSeen: time.Now(),
			rate:     rate,
			capacity: float64(l.perMin),
		}
		l.buckets[ip] = b
	}
	return b
}

// clientIP returns the rate-limit key for the request.
//
// SECURITY: it never trusts the client-supplied X-Forwarded-For — its leftmost
// value is fully attacker-controlled, so keying the limiter on it lets an
// attacker rotate a spoofed XFF to mint a fresh bucket per request (defeating
// the global + auth brute-force limits). On Fly the edge sets Fly-Client-IP,
// which a client cannot forge past the proxy; otherwise we key on the real TCP
// peer (RemoteAddr). Behind an untrusted proxy this over-limits (everyone shares
// the proxy IP) rather than under-limits — fail-safe, not bypassable.
func clientIP(r *http.Request) string {
	if fly := trimSpace(r.Header.Get("Fly-Client-IP")); fly != "" {
		return fly
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

// RateLimit returns middleware that allows at most perMin requests per minute
// per client IP. Requests over the limit receive 429 Too Many Requests.
//
// perMin is the sustained budget. The bucket starts full so a burst of perMin
// requests is allowed immediately, then refills at perMin/60 per second.
func RateLimit(perMin int) func(http.Handler) http.Handler {
	l := newLimiter(perMin)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			if !l.get(ip).allow() {
				http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// AuthRateLimit returns a stricter rate limiter suited for authentication
// endpoints (/auth/login, /auth/signup, /auth/refresh). Default: 10 req/min.
// This is intentionally tighter than the general API limit to slow brute-force
// credential attacks.
func AuthRateLimit() func(http.Handler) http.Handler {
	return RateLimit(10)
}
