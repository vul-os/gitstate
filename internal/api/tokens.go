package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/middleware"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// RegisterTokenRoutes wires the /api/tokens/* endpoints onto mux.
// Called by the orchestrator from router.go — this package does NOT edit router.go.
//
// API tokens are HUMAN-managed credentials: every route here requires a JWT
// session (RequireAuth) + an active org (OrgScope, via X-Org-ID) and is gated to
// org owners/admins. Machines authenticate WITH tokens but cannot manage them.
func RegisterTokenRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	h := &tokenHandlers{db: database, cfg: cfg}
	requireAuth := middleware.RequireAuth(cfg.Auth.JWTSigningKey)
	orgScope := middleware.OrgScope(database.Pool())

	mux.Handle("POST /api/tokens",
		requireAuth(orgScope(http.HandlerFunc(h.createToken))))
	mux.Handle("GET /api/tokens",
		requireAuth(orgScope(http.HandlerFunc(h.listTokens))))
	mux.Handle("DELETE /api/tokens/{id}",
		requireAuth(orgScope(http.HandlerFunc(h.revokeToken))))
}

type tokenHandlers struct {
	db  *db.DB
	cfg *config.Config
}

// knownScopes is the closed set of scopes a token may carry. Requests for unknown
// scopes are rejected so a typo can't silently grant nothing (or everything).
var knownScopes = map[string]bool{
	"read:issues":      true,
	"read:context":     true,
	"read:prs":         true,
	"write:agent_runs": true,
	"write:issues":     true,
}

// ── Response types ───────────────────────────────────────────────────────────

type tokenRow struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
}

// createTokenResponse returns the raw token ONCE (only on creation) plus the row.
type createTokenResponse struct {
	// Token is the raw "gsk_…" secret. It is returned EXACTLY once, here, and is
	// never recoverable afterward. The client must store it immediately.
	Token string   `json:"token"`
	Row   tokenRow `json:"tokenInfo"`
}

func toTokenRow(t *store.APIToken) tokenRow {
	return tokenRow{
		ID:         t.ID,
		Name:       t.Name,
		Prefix:     t.Prefix,
		Scopes:     t.Scopes,
		LastUsedAt: t.LastUsedAt,
		ExpiresAt:  t.ExpiresAt,
		RevokedAt:  t.RevokedAt,
		CreatedAt:  t.CreatedAt,
	}
}

// ── Handlers ─────────────────────────────────────────────────────────────────

// POST /api/tokens  {"name":"my-agent","scopes":["read:context"],"expiresInDays":90}
// Owner/admin only. Returns the raw token ONCE + the persisted row.
func (h *tokenHandlers) createToken(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	user := middleware.UserFromContext(r.Context())

	role, err := store.GetMemberRole(r.Context(), h.db.Pool(), orgID, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !canManageMembers(role) {
		writeError(w, http.StatusForbidden, "only owners and admins can manage API tokens")
		return
	}

	var req struct {
		Name          string   `json:"name"`
		Scopes        []string `json:"scopes"`
		ExpiresInDays *int     `json:"expiresInDays"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(req.Scopes) == 0 {
		writeError(w, http.StatusBadRequest, "at least one scope is required")
		return
	}
	for _, s := range req.Scopes {
		if !knownScopes[s] {
			writeError(w, http.StatusBadRequest, "unknown scope: "+s)
			return
		}
	}

	var expiresAt *time.Time
	if req.ExpiresInDays != nil {
		if *req.ExpiresInDays <= 0 {
			writeError(w, http.StatusBadRequest, "expiresInDays must be positive")
			return
		}
		t := time.Now().UTC().Add(time.Duration(*req.ExpiresInDays) * 24 * time.Hour)
		expiresAt = &t
	}

	var raw string
	var tok *store.APIToken
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		var e error
		raw, tok, e = store.CreateAPIToken(r.Context(), tx, orgID, user.ID, req.Name, req.Scopes, expiresAt)
		return e
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not create token")
		return
	}

	// raw is returned exactly once and never logged.
	writeJSON(w, http.StatusCreated, createTokenResponse{
		Token: raw,
		Row:   toTokenRow(tok),
	})
}

// GET /api/tokens — owner/admin only. Lists all tokens (no secrets).
func (h *tokenHandlers) listTokens(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	user := middleware.UserFromContext(r.Context())

	role, err := store.GetMemberRole(r.Context(), h.db.Pool(), orgID, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !canManageMembers(role) {
		writeError(w, http.StatusForbidden, "only owners and admins can manage API tokens")
		return
	}

	var toks []*store.APIToken
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		var e error
		toks, e = store.ListAPITokens(r.Context(), tx, orgID)
		return e
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not list tokens")
		return
	}

	out := make([]tokenRow, 0, len(toks))
	for _, t := range toks {
		out = append(out, toTokenRow(t))
	}
	writeJSON(w, http.StatusOK, out)
}

// DELETE /api/tokens/{id} — owner/admin only. Revokes a token (idempotent).
func (h *tokenHandlers) revokeToken(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	user := middleware.UserFromContext(r.Context())
	tokenID := r.PathValue("id")

	role, err := store.GetMemberRole(r.Context(), h.db.Pool(), orgID, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !canManageMembers(role) {
		writeError(w, http.StatusForbidden, "only owners and admins can manage API tokens")
		return
	}

	var revokeErr error
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		revokeErr = store.RevokeAPIToken(r.Context(), tx, orgID, tokenID)
		return revokeErr
	}); err != nil {
		if errors.Is(revokeErr, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "token not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not revoke token")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
