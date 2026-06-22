// Package api — accounting_oauth_authz_test.go
// Regression test for the cross-org OAuth-binding fix on the accounting connect
// flow. GET /api/accounting/{provider}/start self-authenticates from ?token=&org=
// (outside OrgScope), so it MUST verify the JWT user is an owner/admin of the
// requested org before sealing it into the state cookie. Without this, any
// authenticated user could start a flow against another org and have the callback
// overwrite that org's accounting connection (binding the victim's invoice pushes
// to the attacker's Xero/QuickBooks tenant).
//
// DB-backed; skips cleanly when DATABASE_URL is unset.
package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/exo/gitstate/internal/accounting"
	"github.com/exo/gitstate/internal/config"
)

// stubAccountingProvider is a minimal Provider so /start reaches the auth check
// without real OAuth credentials.
type stubAccountingProvider struct{}

func (stubAccountingProvider) Name() string   { return "xero" }
func (stubAccountingProvider) Scopes() string { return "accounting.transactions" }
func (stubAccountingProvider) AuthCodeURL(state string) string {
	return "https://accounting.example/authorize?state=" + state
}
func (stubAccountingProvider) Exchange(context.Context, string, map[string]string) (accounting.Tokens, string, string, error) {
	return accounting.Tokens{}, "", "", nil
}
func (stubAccountingProvider) Refresh(context.Context, string) (accounting.Tokens, error) {
	return accounting.Tokens{}, nil
}
func (stubAccountingProvider) CreateInvoice(context.Context, accounting.Tokens, string, accounting.Invoice) (string, string, error) {
	return "", "", nil
}

func TestAccountingStart_CrossOrgBindingRejected(t *testing.T) {
	database := apiTestDB(t)
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const signingKey = "test-signing-key-for-acct-oauth-authz"
	cfg := &config.Config{}
	cfg.Auth.JWTSigningKey = signingKey

	ns := time.Now().UnixNano()

	// Victim org (has data worth hijacking).
	var victimOrg string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("acct-victim-%d", ns), "Victim Org").Scan(&victimOrg); err != nil {
		t.Fatalf("create victim org: %v", err)
	}
	// Attacker's own org (they are owner here).
	var attackerOrg string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("acct-attacker-%d", ns), "Attacker Org").Scan(&attackerOrg); err != nil {
		t.Fatalf("create attacker org: %v", err)
	}
	defer func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM organizations WHERE id = ANY($1)`, []string{victimOrg, attackerOrg})
	}()

	// Attacker: owner of their OWN org, NOT a member of the victim org.
	_, attackerTok := seedMember(t, ctx, database, signingKey, attackerOrg, "owner")
	// A legitimate owner of the victim org (control: must be allowed).
	_, victimOwnerTok := seedMember(t, ctx, database, signingKey, victimOrg, "owner")

	// Build handlers with a stub provider so /start reaches the membership check.
	h := &accountingHandlers{
		db:        database,
		cfg:       cfg,
		providers: map[string]accounting.Provider{"xero": stubAccountingProvider{}},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/accounting/{provider}/start", h.start)

	get := func(token, org string) *httptest.ResponseRecorder {
		url := fmt.Sprintf("/api/accounting/xero/start?token=%s&org=%s", token, org)
		req := httptest.NewRequest(http.MethodGet, url, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	hasStateCookie := func(rec *httptest.ResponseRecorder) bool {
		for _, c := range rec.Result().Cookies() {
			if c.Name == accountingStateCookie && c.Value != "" {
				return true
			}
		}
		return false
	}

	// 1) Attacker binds their JWT to the VICTIM org → 403, no state cookie set.
	rec := get(attackerTok, victimOrg)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-org start: status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}
	if hasStateCookie(rec) {
		t.Errorf("cross-org start must NOT set the accounting state cookie")
	}

	// 2) Victim org owner starting their OWN org's flow → 302 with state cookie.
	rec = get(victimOwnerTok, victimOrg)
	if rec.Code != http.StatusFound {
		t.Errorf("own-org start: status = %d, want 302 (body=%s)", rec.Code, rec.Body.String())
	}
	if !hasStateCookie(rec) {
		t.Errorf("own-org start must set the accounting state cookie")
	}

	// 3) A non-manager member of the victim org is also rejected (connect is a
	// privileged billing action gated to owner/admin).
	_, victimMemberTok := seedMember(t, ctx, database, signingKey, victimOrg, "member")
	if rec := get(victimMemberTok, victimOrg); rec.Code != http.StatusForbidden {
		t.Errorf("member start: status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}
}
