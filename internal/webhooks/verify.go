// Package webhooks implements inbound webhook receiving (real-time sync) and
// CI/CD deployment ingestion for GitHub and GitLab.
//
// Signature verification (security):
//   - GitHub sends `X-Hub-Signature-256: sha256=<hex>` — an HMAC-SHA256 of the
//     RAW request body keyed by the org's stored secret. We recompute it with
//     crypto/hmac + crypto/sha256 and compare in constant time (hmac.Equal).
//     The org is identified by a `?org=<id>` hint baked into the payload URL the
//     user copies from Settings; we read that one org's secret under RLS and
//     verify against it. A wrong/absent signature → 401.
//   - GitLab sends `X-Gitlab-Token: <secret>` — a plain shared token. The org is
//     identified by the same `?org=<id>` hint as GitHub; we read that org's stored
//     token under RLS and compare it to the header in CONSTANT TIME
//     (crypto/subtle.ConstantTimeCompare via ConstantTimeEqual) — never via SQL
//     equality on the raw secret. A wrong/absent token → 401.
//
// Secrets and raw bodies are NEVER logged.
package webhooks

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
)

// GenerateSecret returns a URL-safe random secret suitable for a webhook HMAC
// key / shared token (32 bytes → 64 hex chars).
func GenerateSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// VerifyGitHubSignature reports whether the `X-Hub-Signature-256` header is a
// valid HMAC-SHA256 of body keyed by secret. header form: "sha256=<hex>".
// Constant-time; tolerant of an absent "sha256=" prefix.
func VerifyGitHubSignature(secret string, body []byte, header string) bool {
	if secret == "" || header == "" {
		return false
	}
	want := strings.TrimSpace(header)
	want = strings.TrimPrefix(want, "sha256=")

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	got := hex.EncodeToString(mac.Sum(nil))

	// hmac.Equal is constant-time. Compare the hex strings as bytes.
	return hmac.Equal([]byte(got), []byte(strings.ToLower(want)))
}

// ConstantTimeEqual reports whether a and b are equal, comparing without early
// exit so the duration does not leak how many leading bytes matched. Used by the
// GitLab receiver to compare the X-Gitlab-Token header to the org's stored token
// (the token IS the shared secret) instead of a timing-leaky SQL `=` equality.
// subtle.ConstantTimeCompare returns 0 immediately on a length mismatch, which is
// acceptable here: the secret length is fixed and not itself sensitive.
func ConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
