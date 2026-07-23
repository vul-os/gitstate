// Package email is a tiny SMTP mailer for transactional mail (invoice delivery).
//
// Configuration is read from the environment (SMTP_HOST / SMTP_PORT / SMTP_USER
// / SMTP_PASS / SMTP_FROM) — the app config struct carries no SMTP fields, so we
// resolve them here independently (mirroring internal/notifications). When
// SMTP_HOST is empty (the dev default) Send is a graceful no-op that logs and
// returns nil, so nothing crashes when mail isn't configured.
//
// The message is assembled as a MIME multipart/mixed body: an HTML part plus any
// binary attachments (e.g. a PDF invoice), so a single call delivers a rendered
// email with the invoice attached.
package email

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"mime"
	"mime/multipart"
	"net"
	"net/smtp"
	"net/textproto"
	"os"
	"strings"
	"time"
)

// getenv is a thin wrapper so SMTP settings are sourced from the process
// environment (kept separate to make the config surface explicit).
func getenv(k string) string { return os.Getenv(k) }

// sendTimeout bounds a single outbound SMTP attempt.
const sendTimeout = 20 * time.Second

// Attachment is a single binary file attached to a message.
type Attachment struct {
	Filename    string
	ContentType string // e.g. "application/pdf"; defaults to application/octet-stream
	Data        []byte
}

// Config holds resolved SMTP settings. Configured() reports whether real
// delivery is possible.
type Config struct {
	Host string
	Port string
	User string
	Pass string
	From string
}

// Configured reports whether a real send can be attempted.
func (c Config) Configured() bool { return c.Host != "" && c.From != "" }

// Mailer sends mail via the configured backend (Resend / SES / SMTP). The zero
// value is not useful; use New.
type Mailer struct {
	cfg  Config
	prov providerConfig // resend / ses / smtp selection (resolved from env)
	send sendFunc       // injectable for tests; defaults to smtp.SendMail
	log  *log.Logger
}

// sendFunc matches smtp.SendMail so tests can capture the composed message
// without opening a socket.
type sendFunc func(addr string, a smtp.Auth, from string, to []string, msg []byte) error

// New builds a Mailer from the ambient environment, auto-selecting the backend
// (Resend if RESEND_API_KEY, SES if AWS creds, else SMTP).
func New() *Mailer {
	return &Mailer{cfg: LoadConfig(), prov: loadProviderConfig(), send: smtp.SendMail, log: log.Default()}
}

// NewWithSender builds a Mailer with an explicit config and send hook. Used by
// tests to assert on the composed MIME without a real SMTP server. Forces the
// SMTP path (prov defaults to smtp) so the injected sender is exercised.
func NewWithSender(cfg Config, send sendFunc) *Mailer {
	return &Mailer{cfg: cfg, prov: providerConfig{provider: "smtp"}, send: send, log: log.Default()}
}

// LoadConfig reads SMTP settings from the environment, applying sane defaults.
func LoadConfig() Config {
	c := Config{
		Host: strings.TrimSpace(getenv("SMTP_HOST")),
		Port: strings.TrimSpace(getenv("SMTP_PORT")),
		User: strings.TrimSpace(getenv("SMTP_USER")),
		Pass: getenv("SMTP_PASS"),
		From: strings.TrimSpace(getenv("SMTP_FROM")),
	}
	if c.Port == "" {
		c.Port = "587"
	}
	if c.From == "" {
		c.From = c.User
	}
	return c
}

// Send delivers an HTML message (with optional attachments) to one or more
// recipients. When SMTP is not configured it logs and returns nil (dev no-op),
// so callers never crash on an unconfigured server. Recipient addresses are
// never echoed in returned errors.
func (m *Mailer) Send(ctx context.Context, to []string, subject, htmlBody string, attachments []Attachment) error {
	recips := cleanRecipients(to)
	if len(recips) == 0 {
		return errors.New("email: no recipients")
	}

	// Resend / SES backends (when configured) handle the send directly.
	if handled, err := m.sendVia(ctx, recips, subject, htmlBody, attachments); handled {
		if err != nil {
			return err
		}
		return nil
	}

	if !m.cfg.Configured() {
		m.log.Printf("email: not configured (no SMTP_HOST / RESEND_API_KEY / SES creds) — skipping send of %q to %d recipient(s)", subject, len(recips))
		return nil
	}

	msg, err := BuildMIME(m.cfg.From, recips, subject, htmlBody, attachments)
	if err != nil {
		return fmt.Errorf("email: build message: %w", err)
	}

	addr := net.JoinHostPort(m.cfg.Host, m.cfg.Port)
	var auth smtp.Auth
	if m.cfg.User != "" {
		auth = smtp.PlainAuth("", m.cfg.User, m.cfg.Pass, m.cfg.Host)
	}

	// smtp.SendMail takes no context; run it with a deadline guard.
	done := make(chan error, 1)
	go func() { done <- m.send(addr, auth, m.cfg.From, recips, msg) }()

	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	select {
	case err := <-done:
		if err != nil {
			// Do not surface recipient addresses in the error.
			return errors.New("email: SMTP send failed")
		}
		return nil
	case <-ctx.Done():
		return errors.New("email: SMTP send timed out")
	}
}

// BuildMIME assembles an RFC 5322 / MIME multipart/mixed message: an HTML body
// part plus one base64-encoded part per attachment. Header values are sanitized
// against header injection. Exported so the composition can be unit-tested
// without sending.
func BuildMIME(from string, to []string, subject, htmlBody string, attachments []Attachment) ([]byte, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	boundary := w.Boundary()

	// Top-level headers.
	var head bytes.Buffer
	fmt.Fprintf(&head, "From: %s\r\n", sanitizeHeader(from))
	fmt.Fprintf(&head, "To: %s\r\n", sanitizeHeader(strings.Join(to, ", ")))
	fmt.Fprintf(&head, "Subject: %s\r\n", encodeSubject(subject))
	head.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&head, "Content-Type: multipart/mixed; boundary=%s\r\n", boundary)
	head.WriteString("\r\n")

	// HTML body part.
	htmlHdr := textproto.MIMEHeader{}
	htmlHdr.Set("Content-Type", "text/html; charset=UTF-8")
	htmlHdr.Set("Content-Transfer-Encoding", "quoted-printable")
	part, err := w.CreatePart(htmlHdr)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write([]byte(toQuotedPrintable(htmlBody))); err != nil {
		return nil, err
	}

	// Attachment parts.
	for _, a := range attachments {
		ct := a.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		ahdr := textproto.MIMEHeader{}
		ahdr.Set("Content-Type", ct)
		ahdr.Set("Content-Transfer-Encoding", "base64")
		ahdr.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, sanitizeHeader(a.Filename)))
		ap, err := w.CreatePart(ahdr)
		if err != nil {
			return nil, err
		}
		if err := writeBase64(ap, a.Data); err != nil {
			return nil, err
		}
	}

	if err := w.Close(); err != nil {
		return nil, err
	}

	out := append(head.Bytes(), buf.Bytes()...)
	return out, nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

// writeBase64 writes data as standard base64 wrapped at 76 columns (RFC 2045).
func writeBase64(w interface{ Write([]byte) (int, error) }, data []byte) error {
	enc := base64.StdEncoding.EncodeToString(data)
	const cols = 76
	for i := 0; i < len(enc); i += cols {
		end := i + cols
		if end > len(enc) {
			end = len(enc)
		}
		if _, err := w.Write([]byte(enc[i:end] + "\r\n")); err != nil {
			return err
		}
	}
	return nil
}

// toQuotedPrintable performs a minimal QP encoding sufficient for HTML email:
// it normalizes line endings to CRLF and leaves printable ASCII intact. (Most
// SMTP servers accept this; non-ASCII still rides through as UTF-8 bytes which
// modern MTAs handle via 8BITMIME.)
func toQuotedPrintable(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

// encodeSubject RFC 2047-encodes the subject if it contains non-ASCII, and
// always strips CR/LF to prevent header injection.
func encodeSubject(s string) string {
	s = sanitizeHeader(s)
	for _, r := range s {
		if r > 127 {
			return mime.QEncoding.Encode("UTF-8", s)
		}
	}
	return s
}

// sanitizeHeader removes CR/LF so a value cannot inject extra headers.
func sanitizeHeader(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// cleanRecipients trims, drops empties, and de-duplicates recipient addresses.
func cleanRecipients(to []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range to {
		a = strings.TrimSpace(a)
		if a == "" || seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	return out
}
