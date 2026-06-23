package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/contributors"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/middleware"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// RegisterContributorRoutes wires all /api/contributors* endpoints onto mux.
//
// ORCHESTRATOR: add the single wiring line
//
//	RegisterContributorRoutes(mux, database, cfg)
//
// to NewRouter in internal/api/router.go (in the `if database != nil { … }`
// block, e.g. next to RegisterContributionRoutes). This package does NOT edit
// router.go itself.
//
// Reads (GET) are open to any org member; mutations are owner/admin-gated.
// X-Org-ID is required (OrgScope middleware verifies membership + sets RLS org).
func RegisterContributorRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	h := &contributorHandlers{db: database, cfg: cfg}
	requireAuth := middleware.RequireAuth(cfg.Auth.JWTSigningKey)
	orgScope := middleware.OrgScope(database.Pool())
	wrap := func(fn http.HandlerFunc) http.Handler {
		return requireAuth(orgScope(http.HandlerFunc(fn)))
	}

	mux.Handle("GET /api/contributors", wrap(h.list))
	mux.Handle("POST /api/contributors/detect", wrap(h.detect))
	mux.Handle("PATCH /api/contributors/{id}", wrap(h.patch))
	mux.Handle("POST /api/contributors/{id}/merge", wrap(h.merge))
	mux.Handle("POST /api/contributors/{id}/split", wrap(h.split))
	mux.Handle("POST /api/contributors/{id}/link", wrap(h.link))
	mux.Handle("POST /api/contributors/{id}/invite", wrap(h.invite))
	mux.Handle("DELETE /api/contributors/{id}", wrap(h.del))
}

type contributorHandlers struct {
	db  *db.DB
	cfg *config.Config
}

// ── Response types (exact frontend contract) ─────────────────────────────────

type contributorIdentityResp struct {
	Kind     string `json:"kind"`
	Value    string `json:"value"`
	NameSeen string `json:"nameSeen"`
}

type contributorStatsResp struct {
	Commits int `json:"commits"`
	PRs     int `json:"prs"`
	Reviews int `json:"reviews"`
}

type contributorResp struct {
	ID           string                    `json:"id"`
	DisplayName  string                    `json:"displayName"`
	PrimaryEmail string                    `json:"primaryEmail"`
	Excluded     bool                      `json:"excluded"`
	IsBot        bool                      `json:"isBot"`
	UserID       string                    `json:"userId"`
	MemberName   string                    `json:"memberName"`
	MemberEmail  string                    `json:"memberEmail"`
	InvitedAt    *time.Time                `json:"invitedAt"`
	Status       string                    `json:"status"` // linked | invited | uninvited
	Identities   []contributorIdentityResp `json:"identities"`
	Stats        contributorStatsResp      `json:"stats"`
}

type detectResp struct {
	Contributors int `json:"contributors"`
	Identities   int `json:"identities"`
	Merged       int `json:"merged"`
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// requireManage returns the caller's role and whether they may mutate (owner/admin).
func (h *contributorHandlers) callerCanManage(r *http.Request, orgID string) (bool, error) {
	user := middleware.UserFromContext(r.Context())
	if user == nil {
		return false, store.ErrNotFound
	}
	role, err := store.GetMemberRole(r.Context(), h.db.Pool(), orgID, user.ID)
	if err != nil {
		return false, err
	}
	return canManageMembers(role), nil
}

func contributorStatus(c store.ContributorRecord) string {
	if c.UserID != "" {
		return "linked"
	}
	if c.InvitedAt != nil {
		return "invited"
	}
	return "uninvited"
}

func toContributorResp(c store.ContributorRecord, stats store.ContributorStats) contributorResp {
	ids := make([]contributorIdentityResp, 0, len(c.Identities))
	for _, i := range c.Identities {
		ids = append(ids, contributorIdentityResp{Kind: i.Kind, Value: i.Value, NameSeen: i.NameSeen})
	}
	return contributorResp{
		ID:           c.ID,
		DisplayName:  c.DisplayName,
		PrimaryEmail: c.PrimaryEmail,
		Excluded:     c.Excluded,
		IsBot:        c.IsBot,
		UserID:       c.UserID,
		MemberName:   c.MemberName,
		MemberEmail:  c.MemberEmail,
		InvitedAt:    c.InvitedAt,
		Status:       contributorStatus(c),
		Identities:   ids,
		Stats:        contributorStatsResp{Commits: stats.Commits, PRs: stats.PRs, Reviews: stats.Reviews},
	}
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// GET /api/contributors
func (h *contributorHandlers) list(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())

	var list []store.ContributorRecord
	var stats map[string]store.ContributorStats
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		var err error
		list, err = store.ListContributors(r.Context(), tx, orgID)
		if err != nil {
			return err
		}
		stats, err = store.ContributorStatsByID(r.Context(), tx, orgID)
		return err
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not list contributors")
		return
	}

	resp := make([]contributorResp, 0, len(list))
	for _, c := range list {
		resp = append(resp, toContributorResp(c, stats[c.ID]))
	}
	writeJSON(w, http.StatusOK, resp)
}

// POST /api/contributors/detect
func (h *contributorHandlers) detect(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	ok, err := h.callerCanManage(r, orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !ok {
		writeError(w, http.StatusForbidden, "only owners and admins can run detection")
		return
	}

	var res contributors.DetectResult
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		var e error
		res, e = contributors.DetectAndUpsert(r.Context(), tx, orgID)
		return e
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not run detection")
		return
	}
	writeJSON(w, http.StatusOK, detectResp{
		Contributors: res.Contributors,
		Identities:   res.Identities,
		Merged:       res.Merged,
	})
}

// PATCH /api/contributors/{id}
func (h *contributorHandlers) patch(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	id := r.PathValue("id")
	ok, err := h.callerCanManage(r, orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !ok {
		writeError(w, http.StatusForbidden, "only owners and admins can edit contributors")
		return
	}

	var req struct {
		DisplayName  *string `json:"displayName"`
		PrimaryEmail *string `json:"primaryEmail"`
		Excluded     *bool   `json:"excluded"`
		IsBot        *bool   `json:"isBot"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	up := store.ContributorUpdate{
		DisplayName:  req.DisplayName,
		PrimaryEmail: req.PrimaryEmail,
		Excluded:     req.Excluded,
		IsBot:        req.IsBot,
	}

	if err := h.mutate(r, orgID, func(tx pgx.Tx) error {
		return store.UpdateContributor(r.Context(), tx, orgID, id, up)
	}); err != nil {
		h.writeMutateErr(w, err, "could not update contributor")
		return
	}
	h.returnOne(w, r, orgID, id)
}

// POST /api/contributors/{id}/merge {intoId}
func (h *contributorHandlers) merge(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	id := r.PathValue("id")
	ok, err := h.callerCanManage(r, orgID)
	if err != nil || !ok {
		h.gate(w, err, ok)
		return
	}
	var req struct {
		IntoID string `json:"intoId"`
	}
	if err := decodeJSON(r, &req); err != nil || strings.TrimSpace(req.IntoID) == "" {
		writeError(w, http.StatusBadRequest, "intoId is required")
		return
	}
	if err := h.mutate(r, orgID, func(tx pgx.Tx) error {
		return store.MergeContributors(r.Context(), tx, orgID, id, req.IntoID)
	}); err != nil {
		h.writeMutateErr(w, err, "could not merge contributors")
		return
	}
	h.returnOne(w, r, orgID, req.IntoID)
}

// POST /api/contributors/{id}/split {value}
func (h *contributorHandlers) split(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	ok, err := h.callerCanManage(r, orgID)
	if err != nil || !ok {
		h.gate(w, err, ok)
		return
	}
	var req struct {
		Value string `json:"value"`
	}
	if err := decodeJSON(r, &req); err != nil || strings.TrimSpace(req.Value) == "" {
		writeError(w, http.StatusBadRequest, "value is required")
		return
	}
	var newID string
	if err := h.mutate(r, orgID, func(tx pgx.Tx) error {
		var e error
		newID, e = store.SplitIdentity(r.Context(), tx, orgID, req.Value)
		return e
	}); err != nil {
		h.writeMutateErr(w, err, "could not split identity")
		return
	}
	h.returnOne(w, r, orgID, newID)
}

// POST /api/contributors/{id}/link {userId}
func (h *contributorHandlers) link(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	id := r.PathValue("id")
	ok, err := h.callerCanManage(r, orgID)
	if err != nil || !ok {
		h.gate(w, err, ok)
		return
	}
	var req struct {
		UserID string `json:"userId"`
	}
	if err := decodeJSON(r, &req); err != nil || strings.TrimSpace(req.UserID) == "" {
		writeError(w, http.StatusBadRequest, "userId is required")
		return
	}
	// Verify the user is a member of this org before linking.
	if _, err := store.GetMemberRole(r.Context(), h.db.Pool(), orgID, req.UserID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusBadRequest, "user is not a member of this org")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := h.mutate(r, orgID, func(tx pgx.Tx) error {
		return store.LinkContributorToUser(r.Context(), tx, orgID, id, req.UserID)
	}); err != nil {
		h.writeMutateErr(w, err, "could not link contributor")
		return
	}
	h.returnOne(w, r, orgID, id)
}

// POST /api/contributors/{id}/invite {email?}
//
// Creates an org_invite for the contributor's email (default primary_email) and
// records invited_at. Because org_invites has NO contributor_id column (and we
// do not add a migration), the link at accept time is recovered by matching the
// accepted user's email to a contributor identity / primary_email — see
// linkContributorOnAccept (called from the profile/invite accept path).
func (h *contributorHandlers) invite(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	id := r.PathValue("id")
	ok, err := h.callerCanManage(r, orgID)
	if err != nil || !ok {
		h.gate(w, err, ok)
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	_ = decodeJSON(r, &req)
	email := strings.ToLower(strings.TrimSpace(req.Email))

	// Default to the contributor's primary email.
	var contrib *store.ContributorRecord
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		var e error
		contrib, e = store.GetContributor(r.Context(), tx, orgID, id)
		return e
	}); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "contributor not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if email == "" {
		email = strings.ToLower(strings.TrimSpace(contrib.PrimaryEmail))
	}
	if !emailRe.MatchString(email) {
		writeError(w, http.StatusBadRequest, "a valid email is required to invite this contributor")
		return
	}

	rawToken, tokenHash, err := generateInviteToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not generate invite token")
		return
	}
	expiresAt := time.Now().UTC().Add(7 * 24 * time.Hour)
	inv, err := store.CreateInvite(r.Context(), h.db.Pool(), orgID, email, "member", tokenHash, expiresAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create invite")
		return
	}

	// Record invited_at on the contributor.
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		return store.SetInvited(r.Context(), tx, orgID, id, time.Now().UTC())
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not mark contributor invited")
		return
	}

	acceptURL := buildInviteAcceptURL(h.cfg.App.PublicURL, rawToken)
	writeJSON(w, http.StatusCreated, inviteResponse{
		ID:        inv.ID,
		Email:     inv.Email,
		Role:      inv.Role,
		Token:     rawToken,
		AcceptURL: acceptURL,
	})
}

// DELETE /api/contributors/{id}
// Only a manually-created/empty contributor (no identities) is hard-deleted;
// otherwise we soft-exclude it (set excluded=true) rather than orphan its data.
func (h *contributorHandlers) del(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	id := r.PathValue("id")
	ok, err := h.callerCanManage(r, orgID)
	if err != nil || !ok {
		h.gate(w, err, ok)
		return
	}

	var excludedInstead bool
	if err := h.mutate(r, orgID, func(tx pgx.Tx) error {
		n, e := store.CountContributorIdentities(r.Context(), tx, orgID, id)
		if e != nil {
			return e
		}
		if n == 0 {
			return store.DeleteContributor(r.Context(), tx, orgID, id)
		}
		// Has identities → treat delete as exclude (keep the data attributable).
		excludedInstead = true
		excl := true
		return store.UpdateContributor(r.Context(), tx, orgID, id, store.ContributorUpdate{Excluded: &excl})
	}); err != nil {
		h.writeMutateErr(w, err, "could not delete contributor")
		return
	}
	if excludedInstead {
		writeJSON(w, http.StatusOK, map[string]any{"excluded": true})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── shared mutation plumbing ────────────────────────────────────────────────

func (h *contributorHandlers) mutate(r *http.Request, orgID string, fn func(pgx.Tx) error) error {
	return h.db.WithOrg(r.Context(), orgID, fn)
}

func (h *contributorHandlers) gate(w http.ResponseWriter, err error, ok bool) {
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !ok {
		writeError(w, http.StatusForbidden, "only owners and admins can manage contributors")
	}
}

func (h *contributorHandlers) writeMutateErr(w http.ResponseWriter, err error, msg string) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "contributor not found")
		return
	}
	writeError(w, http.StatusInternalServerError, msg)
}

// returnOne re-reads and returns a single contributor (with stats) after a mutation.
func (h *contributorHandlers) returnOne(w http.ResponseWriter, r *http.Request, orgID, id string) {
	var c *store.ContributorRecord
	var stats map[string]store.ContributorStats
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		var e error
		c, e = store.GetContributor(r.Context(), tx, orgID, id)
		if e != nil {
			return e
		}
		stats, e = store.ContributorStatsByID(r.Context(), tx, orgID)
		return e
	}); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// e.g. merge survivor — fall back to a minimal OK.
			writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
			return
		}
		writeError(w, http.StatusInternalServerError, "could not load contributor")
		return
	}
	writeJSON(w, http.StatusOK, toContributorResp(*c, stats[c.ID]))
}

// ── invite-accept link recovery ────────────────────────────────────────────

// linkContributorOnAccept links the accepted user to a contributor by matching
// the user's email to a contributor identity / primary_email. Best-effort — a
// no-match is not an error. Call this from the invite-accept path AFTER the user
// is a member. (org_invites carries no contributor_id, so we match on email.)
//
// Exported helper so the orgs invite-accept handler can call it; safe to call
// even when the contributor system isn't in use (returns nil quickly).
func linkContributorOnAccept(r *http.Request, database *db.DB, orgID, userID, email string) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || orgID == "" || userID == "" {
		return
	}
	_ = database.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		cid, err := store.FindContributorByIdentityOrEmail(r.Context(), tx, orgID, email)
		if err != nil || cid == "" {
			return nil
		}
		_ = store.LinkContributorToUser(r.Context(), tx, orgID, cid, userID)
		return nil
	})
}
