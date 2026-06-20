package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"strings"
	"time"
)

// ErrEmailNotConfigured is returned when an email delivery is attempted but no
// SMTP server is configured on this server.
var ErrEmailNotConfigured = errors.New("notifications: email delivery is not configured (set SMTP_HOST)")

// deliveryTimeout bounds every outbound webhook / SMTP attempt.
const deliveryTimeout = 10 * time.Second

// ErrBlockedWebhookTarget is returned when a webhook URL is rejected by the SSRF
// guard (bad scheme or an address that resolves to a private/loopback IP).
var ErrBlockedWebhookTarget = errors.New("notifications: webhook target is not allowed")

// httpClient is a shared client with a hard timeout so a slow/blocked receiver
// can never hang a request goroutine. Its transport uses a guarded DialContext so
// every connection — including any the receiver redirects us to — is checked
// against the private-IP blocklist before the socket is opened.
var httpClient = &http.Client{
	Timeout:   deliveryTimeout,
	Transport: &http.Transport{DialContext: guardedDialContext},
}

// guardedDialContext resolves the address and refuses to dial if any resolved IP
// is private, loopback, link-local or otherwise non-public. It defends against
// SSRF (and DNS-rebinding via redirects) by validating at connect time.
func guardedDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, ErrBlockedWebhookTarget
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		// Do not surface the resolver error (it may echo the host).
		return nil, ErrBlockedWebhookTarget
	}
	if len(ips) == 0 {
		return nil, ErrBlockedWebhookTarget
	}
	for _, ip := range ips {
		if isBlockedIP(ip.IP) {
			return nil, ErrBlockedWebhookTarget
		}
	}
	// Dial the first allowed IP directly so we connect to the exact address we
	// validated (avoids a re-resolve to a different, possibly private, IP).
	d := &net.Dialer{Timeout: deliveryTimeout}
	var dialErr error
	for _, ip := range ips {
		conn, e := d.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if e == nil {
			return conn, nil
		}
		dialErr = e
	}
	if dialErr != nil {
		return nil, errors.New("notifications: webhook request failed (network error)")
	}
	return nil, ErrBlockedWebhookTarget
}

// isBlockedIP reports whether ip is one we must never dial for a user-supplied
// webhook: loopback, link-local (incl. 169.254/16 and IPv6 fe80::/10), private
// ranges (10/8, 172.16/12, 192.168/16, fc00::/7), unspecified, or multicast.
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsPrivate() {
		return true
	}
	// IsPrivate covers 10/8, 172.16/12, 192.168/16 and fc00::/7; the checks above
	// cover 127/8, 169.254/16 and ::1. Anything else is treated as public.
	return false
}

// validateWebhookURL parses target and enforces an http(s) scheme with a host.
// IP-level filtering happens at dial time (guardedDialContext), which also covers
// redirects. Returns the parsed URL so the caller can build the request.
func validateWebhookURL(target string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(target))
	if err != nil {
		return nil, ErrBlockedWebhookTarget
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, ErrBlockedWebhookTarget
	}
	if u.Hostname() == "" {
		return nil, ErrBlockedWebhookTarget
	}
	return u, nil
}

// SMTPConfig is read from the environment (the app config struct carries no SMTP
// fields, so notifications resolves SMTP independently). Email delivery is only
// attempted when Host is non-empty.
type SMTPConfig struct {
	Host string // SMTP_HOST
	Port string // SMTP_PORT (default 587)
	User string // SMTP_USER
	Pass string // SMTP_PASS
	From string // SMTP_FROM (default User)
}

// LoadSMTPConfig reads SMTP settings from the environment.
func LoadSMTPConfig() SMTPConfig {
	c := SMTPConfig{
		Host: strings.TrimSpace(os.Getenv("SMTP_HOST")),
		Port: strings.TrimSpace(os.Getenv("SMTP_PORT")),
		User: strings.TrimSpace(os.Getenv("SMTP_USER")),
		Pass: os.Getenv("SMTP_PASS"),
		From: strings.TrimSpace(os.Getenv("SMTP_FROM")),
	}
	if c.Port == "" {
		c.Port = "587"
	}
	if c.From == "" {
		c.From = c.User
	}
	return c
}

// Configured reports whether email delivery is possible.
func (c SMTPConfig) Configured() bool { return c.Host != "" && c.From != "" }

// EmailConfigured is a convenience that checks the ambient environment.
func EmailConfigured() bool { return LoadSMTPConfig().Configured() }

// Deliver sends a rendered digest to a channel of the given kind/target.
//
//	slack | webhook → HTTP POST the Slack/webhook JSON payload.
//	email           → SMTP send the plain-text body (only if configured).
//
// The target (a webhook URL or email address) is NEVER logged or returned in an
// error. Errors describe the failure without echoing the destination.
func Deliver(ctx context.Context, kind, target string, r Rendered) error {
	switch kind {
	case "slack", "webhook":
		return postWebhook(ctx, target, r.SlackPayload)
	case "email":
		return sendEmail(ctx, target, r)
	default:
		return fmt.Errorf("notifications: unknown channel kind %q", kind)
	}
}

// postWebhook POSTs the JSON payload to the target URL. On a non-2xx response it
// returns an error that includes the status code but NOT the URL.
func postWebhook(ctx context.Context, target string, payload map[string]any) error {
	if strings.TrimSpace(target) == "" {
		return errors.New("notifications: webhook target is empty")
	}
	// SSRF guard: reject non-http(s) schemes up front. IP-level filtering (private/
	// loopback/link-local) happens in guardedDialContext, which also vets any
	// redirect the receiver returns.
	if _, err := validateWebhookURL(target); err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("notifications: marshal payload: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, deliveryTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		// Do not wrap err here: a url.Error would embed the target URL.
		return errors.New("notifications: invalid webhook target")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		// http.Client errors are *url.Error and embed the URL — never surface it.
		// A blocked target (SSRF guard, incl. a redirect to a private IP) surfaces
		// as the sentinel so callers can distinguish it from a transient failure.
		if errors.Is(err, ErrBlockedWebhookTarget) {
			return ErrBlockedWebhookTarget
		}
		return errors.New("notifications: webhook request failed (network error)")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("notifications: webhook returned status %d", resp.StatusCode)
	}
	return nil
}

// sendEmail sends the plain-text digest body via SMTP, if configured.
func sendEmail(ctx context.Context, to string, r Rendered) error {
	cfg := LoadSMTPConfig()
	if !cfg.Configured() {
		return ErrEmailNotConfigured
	}
	if strings.TrimSpace(to) == "" {
		return errors.New("notifications: email target is empty")
	}

	subject := r.Summary
	if subject == "" {
		subject = "gitstate digest"
	}
	// Build a minimal RFC 5322 message.
	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", cfg.From)
	fmt.Fprintf(&msg, "To: %s\r\n", to)
	fmt.Fprintf(&msg, "Subject: %s\r\n", sanitizeHeader(subject))
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(strings.ReplaceAll(r.Text, "\n", "\r\n"))

	addr := net.JoinHostPort(cfg.Host, cfg.Port)
	var auth smtp.Auth
	if cfg.User != "" {
		auth = smtp.PlainAuth("", cfg.User, cfg.Pass, cfg.Host)
	}

	// smtp.SendMail does not take a context; run it with a deadline guard.
	done := make(chan error, 1)
	go func() {
		done <- smtp.SendMail(addr, auth, cfg.From, []string{to}, msg.Bytes())
	}()

	ctx, cancel := context.WithTimeout(ctx, deliveryTimeout)
	defer cancel()
	select {
	case err := <-done:
		if err != nil {
			// Do not include the recipient address in the surfaced error.
			return errors.New("notifications: SMTP send failed")
		}
		return nil
	case <-ctx.Done():
		return errors.New("notifications: SMTP send timed out")
	}
}

// sanitizeHeader strips CR/LF from a header value to prevent header injection.
func sanitizeHeader(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}
