// Package store — accounting_test.go
// Tests for accounting_connections CRUD + invoice external-ref linkage.
//
//   - The token round-trip (AES-256-GCM via internal/crypto → store bytea →
//     decrypt) is verified WITHOUT a database, since the store deals only in
//     ciphertext bytes.
//   - The full DB-backed CRUD path runs in one always-rolled-back transaction
//     under the app role (RLS enforced) and skips gracefully without DATABASE_URL.
package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/exo/gitstate/internal/crypto"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestAccountingTokenRoundTrip verifies the encrypt → (stored bytes) → decrypt
// cycle the store relies on: callers encrypt with internal/crypto, the store
// persists the opaque ciphertext, and a later read decrypts it back. No DB needed.
func TestAccountingTokenRoundTrip(t *testing.T) {
	t.Setenv("TOKEN_ENC_KEY", "test-accounting-key-0123456789abcdef")
	key, err := crypto.KeyFromEnv()
	if err != nil {
		t.Fatalf("key: %v", err)
	}

	access := "xero-access-token-secret"
	refresh := "xero-refresh-token-secret"

	encA, err := crypto.Encrypt([]byte(access), key)
	if err != nil {
		t.Fatalf("encrypt access: %v", err)
	}
	encR, err := crypto.Encrypt([]byte(refresh), key)
	if err != nil {
		t.Fatalf("encrypt refresh: %v", err)
	}

	// The ciphertext must not contain the plaintext (sanity: encryption happened).
	if containsBytes(encA, []byte(access)) {
		t.Error("ciphertext leaks plaintext access token")
	}

	// Build the input the API layer would hand the store; ensure the bytes
	// survive verbatim and decrypt back to the originals.
	in := UpsertAccountingConnectionInput{
		Provider: "xero", TokenEncrypted: encA, RefreshEncrypted: encR,
	}
	gotA, err := crypto.Decrypt(in.TokenEncrypted, key)
	if err != nil {
		t.Fatalf("decrypt access: %v", err)
	}
	gotR, err := crypto.Decrypt(in.RefreshEncrypted, key)
	if err != nil {
		t.Fatalf("decrypt refresh: %v", err)
	}
	if string(gotA) != access || string(gotR) != refresh {
		t.Errorf("round-trip mismatch: %q / %q", gotA, gotR)
	}
}

func containsBytes(haystack, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == string(needle) {
			return true
		}
	}
	return false
}

// TestAccountingConnectionCRUD exercises Upsert/Get/List/UpdateTokens/Delete +
// SetInvoiceExternalRef end-to-end against a real database (RLS enforced).
func TestAccountingConnectionCRUD(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping accounting CRUD integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer conn.Release()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Seed an org + user (matching the pattern in invoicing_test.go).
	ns := time.Now().UnixNano()
	var orgID, userID string
	if err := tx.QueryRow(ctx, `INSERT INTO organizations (name, slug) VALUES ($1,$2) RETURNING id`,
		"AcctOrg", "acct-org-"+itoa(ns)).Scan(&orgID); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO users (email, name) VALUES ($1,$2) RETURNING id`,
		"acct-"+itoa(ns)+"@example.com", "Acct User").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	// Enter the org's RLS context for app-role-equivalent inserts.
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_org', $1, true)`, orgID); err != nil {
		t.Fatalf("set current_org: %v", err)
	}

	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	in := UpsertAccountingConnectionInput{
		OrgID: orgID, UserID: userID, Provider: "xero",
		ExternalOrgID: "tenant-1", ExternalName: "Acme Books",
		TokenEncrypted: []byte("ct-access"), RefreshEncrypted: []byte("ct-refresh"),
		Scopes: "openid accounting.transactions", ExpiresAt: &exp,
	}
	c, err := UpsertAccountingConnection(ctx, tx, in)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if c.ExternalOrgID != "tenant-1" || c.ExternalName != "Acme Books" {
		t.Errorf("upsert returned %+v", c)
	}

	got, err := GetAccountingConnection(ctx, tx, orgID, "xero")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got.TokenEncrypted) != "ct-access" || string(got.RefreshEncrypted) != "ct-refresh" {
		t.Errorf("token bytes not round-tripped: %+v", got)
	}

	// Upsert again (conflict path) with a new external name + token.
	in.ExternalName = "Acme Books 2"
	in.TokenEncrypted = []byte("ct-access-2")
	in.RefreshEncrypted = nil // COALESCE should preserve the old refresh
	if _, err := UpsertAccountingConnection(ctx, tx, in); err != nil {
		t.Fatalf("upsert conflict: %v", err)
	}
	got, _ = GetAccountingConnection(ctx, tx, orgID, "xero")
	if got.ExternalName != "Acme Books 2" || string(got.TokenEncrypted) != "ct-access-2" {
		t.Errorf("conflict upsert: %+v", got)
	}
	if string(got.RefreshEncrypted) != "ct-refresh" {
		t.Errorf("refresh should be preserved on nil: %q", got.RefreshEncrypted)
	}

	list, err := ListAccountingConnections(ctx, tx, orgID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v len=%d", err, len(list))
	}

	newExp := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	if err := UpdateAccountingTokens(ctx, tx, orgID, "xero", []byte("ct-access-3"), []byte("ct-refresh-3"), &newExp); err != nil {
		t.Fatalf("update tokens: %v", err)
	}
	got, _ = GetAccountingConnection(ctx, tx, orgID, "xero")
	if string(got.TokenEncrypted) != "ct-access-3" || string(got.RefreshEncrypted) != "ct-refresh-3" {
		t.Errorf("update tokens: %+v", got)
	}

	// SetInvoiceExternalRef against a freshly-created invoice.
	var invID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO client_invoices (org_id, number, status, period_start, period_end, currency, subtotal_cents, total_cents)
		VALUES ($1, 'INV-1', 'draft', now(), now(), 'USD', 0, 0) RETURNING id`, orgID).Scan(&invID); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	if err := SetInvoiceExternalRef(ctx, tx, orgID, invID, "xero", "EXT-9", "https://go.xero.com/x/EXT-9"); err != nil {
		t.Fatalf("set external ref: %v", err)
	}
	inv, err := GetClientInvoice(ctx, tx, orgID, invID)
	if err != nil {
		t.Fatalf("get invoice: %v", err)
	}
	if inv.ExternalProvider != "xero" || inv.ExternalID != "EXT-9" || inv.ExternalURL != "https://go.xero.com/x/EXT-9" {
		t.Errorf("external ref not stored: %+v", inv)
	}

	if err := DeleteAccountingConnection(ctx, tx, orgID, "xero"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := GetAccountingConnection(ctx, tx, orgID, "xero"); err == nil {
		t.Error("expected ErrNotFound after delete")
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
