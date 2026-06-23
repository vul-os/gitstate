package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/exo/gitstate/internal/auth"
	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/middleware"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// RegisterOrgRoutes wires all /api/orgs/* and /api/invites/* endpoints onto mux.
// Called by the orchestrator from router.go — this package does NOT edit router.go.
//
// Active-org convention: requests that are scoped to a single org must include the
// X-Org-ID header, which OrgScope middleware reads to verify membership and attach
// the org ID to the context via OrgFromContext.
func RegisterOrgRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	h := &orgHandlers{db: database, cfg: cfg}
	requireAuth := middleware.RequireAuth(cfg.Auth.JWTSigningKey)
	orgScope := middleware.OrgScope(database.Pool())

	// Caller-level org listing + creation (no org context required yet).
	mux.Handle("GET /api/orgs", requireAuth(http.HandlerFunc(h.listOrgs)))
	mux.Handle("POST /api/orgs", requireAuth(http.HandlerFunc(h.createOrg)))

	// Org-scoped member management: X-Org-ID required.
	mux.Handle("GET /api/orgs/{id}/members",
		requireAuth(orgScope(http.HandlerFunc(h.listMembers))))
	mux.Handle("POST /api/orgs/{id}/members",
		requireAuth(orgScope(http.HandlerFunc(h.inviteMember))))
	mux.Handle("PATCH /api/orgs/{id}/members/{userId}",
		requireAuth(orgScope(http.HandlerFunc(h.updateMember))))
	mux.Handle("DELETE /api/orgs/{id}/members/{userId}",
		requireAuth(orgScope(http.HandlerFunc(h.removeMember))))

	// Invite acceptance (auth required; org scope not required — user may not be a member yet).
	mux.Handle("POST /api/invites/accept", requireAuth(http.HandlerFunc(h.acceptInvite)))
}

type orgHandlers struct {
	db  *db.DB
	cfg *config.Config
}

// ── Response types ───────────────────────────────────────────────────────────

type orgResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Slug    string `json:"slug"`
	Role    string `json:"role"`
	PlanKey string `json:"planKey"`
}

type memberResponse struct {
	UserID string `json:"userId"`
	Email  string `json:"email"`
	Name   string `json:"name"`
	Role   string `json:"role"`
}

type inviteResponse struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Role  string `json:"role"`
	// Token is the raw invite token. It is returned so a self-host/dev instance
	// with no SMTP configured can surface a copyable invite link in the UI.
	Token string `json:"token,omitempty"`
	// AcceptURL is the full invite-accept URL the inviter can share directly.
	// Built from cfg.App.PublicURL + the SPA route /invite/accept?token=...
	AcceptURL string `json:"acceptUrl,omitempty"`
}

type acceptInviteResponse struct {
	OrgID string `json:"orgId"`
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// validRole returns true for the four supported member roles.
func validRole(r string) bool {
	switch r {
	case "owner", "admin", "member", "stakeholder":
		return true
	}
	return false
}

// canManageMembers returns true when the role allows inviting or removing others.
func canManageMembers(role string) bool {
	return role == "owner" || role == "admin"
}

// generateInviteToken returns (rawToken, tokenHash, error).
// The raw token is sent to the invitee; only the hash is stored.
func generateInviteToken() (string, string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	raw := hex.EncodeToString(buf)
	hash := auth.HashToken(raw)
	return raw, hash, nil
}

// buildInviteAcceptURL composes the shareable invite-accept link from the
// instance's public URL and the SPA route the InviteAccept page is mounted on
// (web/src/App.jsx: /invite/accept?token=...). publicURL may have a trailing
// slash; we normalize it. The token is URL-query-escaped.
func buildInviteAcceptURL(publicURL, rawToken string) string {
	base := strings.TrimRight(strings.TrimSpace(publicURL), "/")
	return base + "/invite/accept?token=" + url.QueryEscape(rawToken)
}

// isSlugConflict checks for a Postgres unique constraint violation on slug.
func isSlugConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "organizations_slug_key") ||
		(strings.Contains(msg, "unique") && strings.Contains(msg, "slug"))
}

// ── Handlers ─────────────────────────────────────────────────────────────────

// GET /api/orgs
// Returns the list of organizations the authenticated user belongs to.
func (h *orgHandlers) listOrgs(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	orgs, err := store.ListOrgsForUser(r.Context(), h.db.Pool(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list organizations")
		return
	}

	resp := make([]orgResponse, 0, len(orgs))
	for _, o := range orgs {
		resp = append(resp, orgResponse{
			ID:      o.ID,
			Name:    o.Name,
			Slug:    o.Slug,
			Role:    o.Role,
			PlanKey: o.PlanKey,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// POST /api/orgs  {"name":"Acme"}
// Creates a new organization; the caller becomes the owner.
func (h *orgHandlers) createOrg(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req struct {
		Name string `json:"name"`
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

	slug := slugify(req.Name)

	o, err := store.CreateOrg(r.Context(), h.db.Pool(), req.Name, slug, user.ID)
	if err != nil {
		if isSlugConflict(err) {
			writeError(w, http.StatusConflict, "an organization with that name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create organization")
		return
	}

	writeJSON(w, http.StatusCreated, orgResponse{
		ID:      o.ID,
		Name:    o.Name,
		Slug:    o.Slug,
		Role:    o.Role,
		PlanKey: o.PlanKey,
	})
}

// GET /api/orgs/{id}/members
// Lists all members of the org identified by X-Org-ID.
func (h *orgHandlers) listMembers(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())

	var members []store.OrgMember
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		var err error
		members, err = store.ListMembers(r.Context(), tx, orgID)
		return err
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not list members")
		return
	}

	resp := make([]memberResponse, 0, len(members))
	for _, m := range members {
		resp = append(resp, memberResponse{
			UserID: m.UserID,
			Email:  m.Email,
			Name:   m.Name,
			Role:   m.Role,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// POST /api/orgs/{id}/members  {"email":"alice@example.com","role":"member"}
// Creates an invite for the given email address.
// Stakeholders are always allowed (free seat — decisions P6).
func (h *orgHandlers) inviteMember(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	user := middleware.UserFromContext(r.Context())

	callerRole, err := store.GetMemberRole(r.Context(), h.db.Pool(), orgID, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !canManageMembers(callerRole) {
		writeError(w, http.StatusForbidden, "only owners and admins can invite members")
		return
	}

	var req struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if !emailRe.MatchString(req.Email) {
		writeError(w, http.StatusBadRequest, "invalid email address")
		return
	}
	if req.Role == "" {
		req.Role = "member"
	}
	if !validRole(req.Role) {
		writeError(w, http.StatusBadRequest, "role must be owner, admin, member, or stakeholder")
		return
	}
	// Stakeholders are FREE — billing enforcement is a later wave (decisions P6).

	rawToken, tokenHash, err := generateInviteToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not generate invite token")
		return
	}

	expiresAt := time.Now().UTC().Add(7 * 24 * time.Hour)
	inv, err := store.CreateInvite(r.Context(), h.db.Pool(), orgID, req.Email, req.Role, tokenHash, expiresAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create invite")
		return
	}

	acceptURL := buildInviteAcceptURL(h.cfg.App.PublicURL, rawToken)

	// When SMTP/email is configured the raw token would be emailed to req.Email
	// here. Email delivery is not wired in dev/self-host, so we instead return the
	// token + acceptUrl in the response so the inviter can copy a shareable link.
	// The token is NEVER logged.
	writeJSON(w, http.StatusCreated, inviteResponse{
		ID:        inv.ID,
		Email:     inv.Email,
		Role:      inv.Role,
		Token:     rawToken,
		AcceptURL: acceptURL,
	})
}

// PATCH /api/orgs/{id}/members/{userId}  {"role":"admin"}
// Updates the role of an existing member.
func (h *orgHandlers) updateMember(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	user := middleware.UserFromContext(r.Context())
	targetUserID := r.PathValue("userId")

	callerRole, err := store.GetMemberRole(r.Context(), h.db.Pool(), orgID, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !canManageMembers(callerRole) {
		writeError(w, http.StatusForbidden, "only owners and admins can change roles")
		return
	}

	var req struct {
		Role string `json:"role"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !validRole(req.Role) {
		writeError(w, http.StatusBadRequest, "role must be owner, admin, member, or stakeholder")
		return
	}

	var updateErr error
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		updateErr = store.UpdateMemberRole(r.Context(), tx, orgID, targetUserID, req.Role)
		return updateErr
	}); err != nil {
		if errors.Is(updateErr, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "member not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not update member role")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"role": req.Role})
}

// DELETE /api/orgs/{id}/members/{userId}
// Removes a member from the org.
func (h *orgHandlers) removeMember(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	user := middleware.UserFromContext(r.Context())
	targetUserID := r.PathValue("userId")

	callerRole, err := store.GetMemberRole(r.Context(), h.db.Pool(), orgID, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !canManageMembers(callerRole) {
		writeError(w, http.StatusForbidden, "only owners and admins can remove members")
		return
	}

	var removeErr error
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		removeErr = store.RemoveMember(r.Context(), tx, orgID, targetUserID)
		return removeErr
	}); err != nil {
		if errors.Is(removeErr, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "member not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not remove member")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// POST /api/invites/accept  {"token":"<raw>"}
// Accepts an org invite and adds the authenticated user as a member.
func (h *orgHandlers) acceptInvite(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Token == "" {
		writeError(w, http.StatusBadRequest, "token is required")
		return
	}

	tokenHash := auth.HashToken(req.Token)
	inv, err := store.GetInviteByTokenHash(r.Context(), h.db.Pool(), tokenHash)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "invite not found or expired")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := store.AcceptInvite(r.Context(), h.db.Pool(), inv.ID, inv.OrgID, user.ID, inv.Role); err != nil {
		writeError(w, http.StatusInternalServerError, "could not accept invite")
		return
	}

	// If this invite was sent to link a contributor (contributors system), recover
	// the link by matching the accepted user's email to a contributor identity /
	// primary_email. org_invites has no contributor_id column, so we match on the
	// invite email (which IS the contributor's email when invited via the
	// contributors UI). Best-effort: a no-match is silently ignored.
	linkContributorOnAccept(r, h.db, inv.OrgID, user.ID, inv.Email)

	writeJSON(w, http.StatusOK, acceptInviteResponse{OrgID: inv.OrgID})
}
