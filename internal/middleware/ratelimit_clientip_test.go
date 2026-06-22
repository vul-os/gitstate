package middleware

import (
	"net/http"
	"testing"
)

// Regression for the rate-limit bypass: a client-supplied X-Forwarded-For must
// NOT be trusted (it's spoofable → fresh bucket per request). Fly-Client-IP (set
// by the trusted edge) wins; otherwise the real TCP peer is used.
func TestClientIP_IgnoresSpoofedXFF(t *testing.T) {
	cases := []struct {
		name   string
		xff    string
		fly    string
		remote string
		want   string
	}{
		{"spoofed XFF ignored, falls back to peer", "1.2.3.4", "", "203.0.113.9:5555", "203.0.113.9"},
		{"fly header trusted over peer", "1.2.3.4", "198.51.100.7", "10.0.0.1:443", "198.51.100.7"},
		{"no headers → peer", "", "", "192.0.2.50:1234", "192.0.2.50"},
		{"rotating spoofed XFF cannot change the key", "9.9.9.9, 8.8.8.8", "", "203.0.113.9:5555", "203.0.113.9"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, _ := http.NewRequest("GET", "/", nil)
			r.RemoteAddr = c.remote
			if c.xff != "" {
				r.Header.Set("X-Forwarded-For", c.xff)
			}
			if c.fly != "" {
				r.Header.Set("Fly-Client-IP", c.fly)
			}
			if got := clientIP(r); got != c.want {
				t.Fatalf("clientIP = %q, want %q (XFF must not be trusted)", got, c.want)
			}
		})
	}
}
