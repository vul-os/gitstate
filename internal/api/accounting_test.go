// Package api — accounting_test.go
// HTTP tests for the accounting routes:
//   - the invoice-push manager gate (a plain member is 403'd);
//   - the graceful "provider not configured" path (config-gated, no creds set);
//   - the status endpoint shape when no provider is configured.
//
// These drive the real middleware chain (RequireAuth + OrgScope) and skip cleanly
// when DATABASE_URL is unset, mirroring role_gate_test.go.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/exo/gitstate/internal/config"
)

func TestAccountingPushGateAndNotConfigured(t *testing.T) {
	database := apiTestDB(t)
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const signingKey = "test-signing-key-for-accounting"
	cfg := &config.Config{}
	cfg.Auth.JWTSigningKey = signingKey
	// No Accounting creds set → both providers "not configured".

	ns := time.Now().UnixNano()
	var orgID string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("acct-gate-%d", ns), "Acct Gate Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	defer func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM organizations WHERE id = $1`, orgID)
	}()

	// Seed an invoice in the org so the push path reaches the provider check.
	var invID string
	if _, err := database.Pool().Exec(ctx, `SELECT set_config('app.current_org', $1, false)`, orgID); err != nil {
		t.Fatalf("set current_org: %v", err)
	}
	if err := database.Pool().QueryRow(ctx, `
		INSERT INTO client_invoices (org_id, number, status, period_start, period_end, currency, subtotal_cents, total_cents)
		VALUES ($1, 'INV-GATE-1', 'draft', now(), now(), 'USD', 0, 0) RETURNING id`, orgID).Scan(&invID); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}

	_, memberTok := seedMember(t, ctx, database, signingKey, orgID, "member")
	_, ownerTok := seedMember(t, ctx, database, signingKey, orgID, "owner")

	mux := http.NewServeMux()
	RegisterAccountingRoutes(mux, database, cfg)

	do := func(method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Org-ID", orgID)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	// 1) A plain member is 403'd on push (manager gate).
	if rec := do(http.MethodPost, "/api/invoices/"+invID+"/push", memberTok, `{"provider":"xero"}`); rec.Code != http.StatusForbidden {
		t.Errorf("push as member: status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}

	// 2) An owner gets past the gate, but the provider isn't configured → 400.
	rec := do(http.MethodPost, "/api/invoices/"+invID+"/push", ownerTok, `{"provider":"xero"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("push as owner (not configured): status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not configured") {
		t.Errorf("push not-configured body = %s", rec.Body.String())
	}

	// 3) Disconnect on an unconfigured provider is still manager-gated + graceful.
	if rec := do(http.MethodDelete, "/api/accounting/xero", memberTok, ``); rec.Code != http.StatusForbidden {
		t.Errorf("disconnect as member: status = %d, want 403", rec.Code)
	}

	// 4) Status reports both providers as not-configured / not-connected.
	statusRec := do(http.MethodGet, "/api/accounting/status", ownerTok, ``)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("status: %d (%s)", statusRec.Code, statusRec.Body.String())
	}
	var got []accountingStatus
	if err := json.Unmarshal(statusRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("status len = %d, want 2", len(got))
	}
	for _, s := range got {
		if s.Configured || s.Connected {
			t.Errorf("provider %s should be unconfigured/unconnected: %+v", s.Provider, s)
		}
	}
	t.Logf("accounting gates OK: member 403 on push/disconnect; owner sees graceful not-configured 400")
}
