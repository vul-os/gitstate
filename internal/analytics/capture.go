// Package analytics — capture.go
// Privacy-first product-analytics capture for the cloud console. The Capture
// middleware observes inbound HTTP traffic, derives the client IP, HASHES it
// with a server-side salt (the raw IP is NEVER stored or logged), resolves
// coarse geo, classifies the request into an analytics "kind", and records it
// — but only for meaningful events (auth + marketing pageviews). High-churn
// /api polling, static assets, health checks and the SSE stream are skipped so
// the table stays useful and small.
//
// The insert runs in a goroutine with its own short-lived context, so capture
// never adds latency to (nor can it fail) the request it observes.
package analytics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// poolDB is the minimal surface Capture needs from *db.DB; declaring it as an
// interface keeps capture testable and decoupled from the concrete DB.
type poolDB interface {
	Pool() *pgxpool.Pool
}

// HashIP returns sha256(ip + salt) hex-encoded. This is the ONLY representation
// of a client IP that is ever persisted — the raw IP is never stored or logged.
// The salt makes the hash non-reversible across instances (a global rainbow
// table of /32s is otherwise trivial). An empty ip yields "" so we never store a
// hash of just the salt.
func HashIP(ip, salt string) string {
	if ip == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(ip + salt))
	return hex.EncodeToString(sum[:])
}

// RecordEvent inserts one analytics event via the given pool. It is safe to call
// with a nil pool (no-op) so callers need not guard. Errors are returned for the
// caller to log/ignore; the capture middleware discards them.
func RecordEvent(ctx context.Context, pool *pgxpool.Pool, e store.AnalyticsEvent) error {
	if pool == nil {
		return nil
	}
	return store.InsertAnalyticsEvent(ctx, pool, e)
}

// Capture builds the analytics-capture middleware. database supplies the pool
// (nil-safe — capture becomes a pass-through when the DB or pool is nil), cfg
// supplies the IP-hash salt, and geo resolves coarse location. The returned
// func is a standard net/http middleware the orchestrator wires in router.go.
//
//	mw := analytics.Capture(database, cfg, geo)   // func(http.Handler) http.Handler
//
// A nil geo is tolerated (geo columns resolve to "").
func Capture(database poolDB, cfg *config.Config, geo *GeoResolver) func(http.Handler) http.Handler {
	var pool *pgxpool.Pool
	if database != nil {
		pool = database.Pool()
	}
	salt := ""
	if cfg != nil {
		salt = cfg.Admin.AnalyticsSalt
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Capture nothing when there is nowhere to write.
			if pool == nil {
				next.ServeHTTP(w, r)
				return
			}

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			kind, ok := classify(r, rec.status)
			if !ok {
				return
			}

			// Derive + hash the client IP. The raw IP lives only as a local
			// variable here and is never stored or logged.
			rawIP := clientIP(r)
			ipHash := HashIP(rawIP, salt)

			var country, region, city string
			if geo != nil {
				if ip := net.ParseIP(rawIP); ip != nil {
					country, region, city = geo.Lookup(ip)
				}
			}

			ev := store.AnalyticsEvent{
				Kind:      kind,
				Path:      r.URL.Path,
				Method:    r.Method,
				Status:    rec.status,
				IPHash:    ipHash,
				Country:   country,
				Region:    region,
				City:      city,
				UserAgent: truncate(r.UserAgent(), 512),
			}

			// Insert off the request path with an independent timeout so a slow
			// or cancelled request can never affect (or be affected by) capture.
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = RecordEvent(ctx, pool, ev)
			}()
		})
	}
}

// ── Classification ────────────────────────────────────────────────────────────

// classify decides whether a request is worth recording and, if so, its kind.
// It keeps volume sane: auth events (signup/login/login_failed/logout) and
// marketing/landing pageviews are recorded; static assets, health, the SSE
// stream, and high-churn /api polling are skipped. ok=false means "skip".
func classify(r *http.Request, status int) (kind string, ok bool) {
	p := r.URL.Path

	// Never record the noise.
	if isSkippable(p) {
		return "", false
	}

	// Auth events — detected by path + method (+ status for login outcome).
	switch {
	case r.Method == http.MethodPost && matchAuth(p, "signup", "register"):
		return "signup", true
	case r.Method == http.MethodPost && matchAuth(p, "login", "signin", "session"):
		if status >= 400 {
			return "login_failed", true
		}
		return "login", true
	case matchAuth(p, "logout", "signout"):
		return "logout", true
	}

	// Marketing / landing pageviews — GET HTML navigations outside the app/api.
	if r.Method == http.MethodGet && isMarketingPageview(p) {
		return "pageview", true
	}

	return "", false
}

// isSkippable matches the high-churn / non-meaningful traffic we never record:
// static assets, health/metrics probes, the admin console + its SSE stream, and
// the org-scoped /api surface (polling). Auth routes are matched BEFORE this in
// classify, so skipping /api here does not drop API-hosted auth endpoints.
func isSkippable(p string) bool {
	switch {
	case p == "/health", p == "/healthz", p == "/readyz", p == "/metrics", p == "/favicon.ico", p == "/robots.txt":
		return true
	case strings.HasPrefix(p, "/static/"),
		strings.HasPrefix(p, "/assets/"),
		strings.HasPrefix(p, "/admin/events"), // SSE stream — never sample
		strings.HasPrefix(p, "/_"):
		return true
	}
	// Static asset extensions.
	if i := strings.LastIndexByte(p, '.'); i >= 0 {
		switch strings.ToLower(p[i:]) {
		case ".css", ".js", ".map", ".png", ".jpg", ".jpeg", ".gif", ".svg",
			".ico", ".woff", ".woff2", ".ttf", ".webp", ".avif":
			return true
		}
	}
	return false
}

// matchAuth reports whether the path looks like one of the named auth endpoints,
// tolerating an /api or /auth prefix (e.g. /login, /api/auth/login, /signup).
func matchAuth(path string, names ...string) bool {
	lp := strings.ToLower(path)
	for _, n := range names {
		if lp == "/"+n || strings.HasSuffix(lp, "/"+n) || strings.Contains(lp, "/"+n+"/") {
			return true
		}
	}
	return false
}

// isMarketingPageview reports whether p is a landing/marketing page we want to
// count as a pageview. It is an ALLOWLIST (root + a few marketing roots) rather
// than "everything that isn't /api", so authenticated app navigation and API
// polling never inflate the pageview count.
func isMarketingPageview(p string) bool {
	if p == "/" {
		return true
	}
	for _, prefix := range []string{
		"/pricing", "/about", "/docs", "/blog", "/features", "/contact", "/changelog", "/legal",
	} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}

// statusRecorder wraps http.ResponseWriter to capture the final status code so
// classify() can distinguish a successful login from a failed one. It defaults
// to 200 (the implicit status when a handler writes a body without WriteHeader).
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.wroteHeader = true
	}
	return s.ResponseWriter.Write(b)
}

// Flush forwards to the underlying writer when it supports flushing, preserving
// SSE / streaming semantics for handlers that wrap through this recorder.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// ── Client-IP derivation ──────────────────────────────────────────────────────

// clientIP extracts the real client IP, preferring the first hop of
// X-Forwarded-For (set by fly.io's proxy) over RemoteAddr. Mirrors the
// middleware package's clientIP so capture and rate-limiting agree.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first := xff
		if i := strings.IndexByte(xff, ','); i >= 0 {
			first = xff[:i]
		}
		if first = strings.TrimSpace(first); first != "" {
			return first
		}
	}
	if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return ip
	}
	return r.RemoteAddr
}

// ── helpers ───────────────────────────────────────────────────────────────────

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// compile-time assertion that *db.DB satisfies poolDB (so Capture accepts it).
var _ poolDB = (*db.DB)(nil)
