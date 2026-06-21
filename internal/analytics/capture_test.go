// Package analytics — capture_test.go
// DB-free unit tests for the privacy-critical capture helpers: IP hashing
// (stable, salt-dependent, never the raw IP), request classification, and
// client-IP derivation. These always run (no DATABASE_URL / Postgres needed).
package analytics

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestHashIPStable(t *testing.T) {
	// Same input → same hash (so repeat visitors can be counted).
	a := HashIP("203.0.113.7", "pepper")
	b := HashIP("203.0.113.7", "pepper")
	if a != b {
		t.Fatalf("HashIP not stable: %q != %q", a, b)
	}
	if a == "" {
		t.Fatal("HashIP returned empty for a real IP")
	}
	// sha256 hex is 64 chars.
	if len(a) != 64 {
		t.Fatalf("HashIP length = %d, want 64", len(a))
	}
}

func TestHashIPSaltDependent(t *testing.T) {
	withSalt := HashIP("203.0.113.7", "salt-A")
	otherSalt := HashIP("203.0.113.7", "salt-B")
	if withSalt == otherSalt {
		t.Fatal("HashIP must depend on the salt — different salts produced the same hash")
	}
}

func TestHashIPNeverEqualsRawIP(t *testing.T) {
	ip := "198.51.100.42"
	if h := HashIP(ip, "pepper"); h == ip {
		t.Fatal("HashIP returned the raw IP — raw IP must never be stored")
	}
	// And it must not merely contain the raw IP either.
	if h := HashIP(ip, "pepper"); contains(h, ip) {
		t.Fatalf("hash %q contains the raw IP %q", h, ip)
	}
}

func TestHashIPEmptyIP(t *testing.T) {
	if h := HashIP("", "pepper"); h != "" {
		t.Fatalf("HashIP(\"\") = %q, want \"\" (never hash the salt alone)", h)
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		path     string
		status   int
		wantKind string
		wantOK   bool
	}{
		{"signup post", "POST", "/api/auth/signup", 200, "signup", true},
		{"login ok", "POST", "/api/auth/login", 200, "login", true},
		{"login failed", "POST", "/api/auth/login", 401, "login_failed", true},
		{"logout", "GET", "/admin/logout", 200, "logout", true},
		{"landing pageview", "GET", "/", 200, "pageview", true},
		{"pricing pageview", "GET", "/pricing", 200, "pageview", true},
		{"docs pageview", "GET", "/docs/getting-started", 200, "pageview", true},
		{"api polling skipped", "GET", "/api/orgs/x/commits", 200, "", false},
		{"static skipped", "GET", "/static/app.js", 200, "", false},
		{"css ext skipped", "GET", "/build/main.css", 200, "", false},
		{"health skipped", "GET", "/health", 200, "", false},
		{"sse skipped", "GET", "/admin/events", 200, "", false},
		{"app nav not a pageview", "GET", "/app/dashboard", 200, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, ok := classify(&http.Request{Method: tt.method, URL: mustURL(tt.path)}, tt.status)
			if ok != tt.wantOK {
				t.Fatalf("classify ok = %v, want %v (kind=%q)", ok, tt.wantOK, kind)
			}
			if ok && kind != tt.wantKind {
				t.Fatalf("classify kind = %q, want %q", kind, tt.wantKind)
			}
		})
	}
}

func TestClientIPPrefersXFF(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:55555"
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 70.41.3.18, 150.172.238.178")
	if got := clientIP(r); got != "203.0.113.7" {
		t.Fatalf("clientIP = %q, want first XFF hop 203.0.113.7", got)
	}
}

func TestClientIPFallsBackToRemoteAddr(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.0.2.55:44444"
	if got := clientIP(r); got != "192.0.2.55" {
		t.Fatalf("clientIP = %q, want 192.0.2.55 from RemoteAddr", got)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func mustURL(path string) *url.URL {
	return &url.URL{Path: path}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
