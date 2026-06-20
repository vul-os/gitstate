package notifications

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestIsBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.5.5.5", // loopback
		"10.0.0.1", "10.255.255.255", // 10/8
		"172.16.0.1", "172.31.255.255", // 172.16/12
		"192.168.1.1", // 192.168/16
		"169.254.1.1", // link-local
		"::1",         // IPv6 loopback
		"fc00::1",     // fc00::/7
		"fd12:3456::1",
		"fe80::1",  // IPv6 link-local
		"0.0.0.0",  // unspecified
		"224.0.0.1", // multicast
	}
	for _, s := range blocked {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("bad test IP %q", s)
		}
		if !isBlockedIP(ip) {
			t.Errorf("isBlockedIP(%s) = false, want true", s)
		}
	}

	allowed := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:2800:220:1::"}
	for _, s := range allowed {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("bad test IP %q", s)
		}
		if isBlockedIP(ip) {
			t.Errorf("isBlockedIP(%s) = true, want false (public)", s)
		}
	}
}

func TestValidateWebhookURL(t *testing.T) {
	bad := []string{
		"",
		"ftp://example.com",
		"file:///etc/passwd",
		"gopher://x",
		"http://", // no host
		"javascript:alert(1)",
	}
	for _, u := range bad {
		if _, err := validateWebhookURL(u); err == nil {
			t.Errorf("validateWebhookURL(%q) accepted, want rejected", u)
		}
	}
	good := []string{"http://example.com/hook", "https://hooks.slack.com/abc"}
	for _, u := range good {
		if _, err := validateWebhookURL(u); err != nil {
			t.Errorf("validateWebhookURL(%q) rejected: %v", u, err)
		}
	}
}

func TestPostWebhookRejectsBadScheme(t *testing.T) {
	err := postWebhook(context.Background(), "ftp://evil/", map[string]any{"x": 1})
	if !errors.Is(err, ErrBlockedWebhookTarget) {
		t.Fatalf("postWebhook(ftp) err = %v, want ErrBlockedWebhookTarget", err)
	}
}

func TestGuardedDialContextBlocksLoopback(t *testing.T) {
	// localhost resolves to a loopback IP → must be refused before dialing.
	_, err := guardedDialContext(context.Background(), "tcp", "localhost:80")
	if !errors.Is(err, ErrBlockedWebhookTarget) {
		t.Fatalf("guardedDialContext(localhost) err = %v, want ErrBlockedWebhookTarget", err)
	}
	// A literal private IP, too.
	_, err = guardedDialContext(context.Background(), "tcp", "169.254.169.254:80")
	if !errors.Is(err, ErrBlockedWebhookTarget) {
		t.Fatalf("guardedDialContext(169.254.169.254) err = %v, want ErrBlockedWebhookTarget", err)
	}
}

func TestPostWebhookBlocksPrivateTarget(t *testing.T) {
	// End-to-end through the shared client: a request to a loopback URL must fail
	// the SSRF guard (sentinel error), not actually connect.
	err := postWebhook(context.Background(), "http://127.0.0.1:9/hook", map[string]any{"x": 1})
	if !errors.Is(err, ErrBlockedWebhookTarget) {
		t.Fatalf("postWebhook(loopback) err = %v, want ErrBlockedWebhookTarget", err)
	}
}
