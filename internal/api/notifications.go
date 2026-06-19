// Package api — notifications.go
//
// Notification channels + evidence-based digests, delivered to Slack, a generic
// webhook, or email. Backed by notification_channels / notification_log
// (migration 20260619_013, both RLS) and the internal/notifications builders +
// delivery.
//
// Routes (RequireAuth + OrgScope; X-Org-ID required; reads run in db.WithOrg):
//
//	GET    /api/notifications/channels            → list channels + emailConfigured
//	POST   /api/notifications/channels            → create a channel
//	PATCH  /api/notifications/channels/{id}       → partial update
//	DELETE /api/notifications/channels/{id}       → delete
//	GET    /api/notifications/preview?kind=…      → rendered digest (no send)
//	POST   /api/notifications/channels/{id}/test  → send the digest(s) now
//	GET    /api/notifications/log                 → recent send/preview log
//
// SECURITY: a channel's target (webhook URL / email) is never logged. Every
// send/preview writes a notification_log row. Only owners/admins may mutate
// channels or trigger sends.
package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/middleware"
	"github.com/exo/gitstate/internal/notifications"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// RegisterNotificationRoutes wires the /api/notifications/* endpoints onto mux.
// Called by the orchestrator from router.go — this package does NOT edit router.go.
func RegisterNotificationRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	h := &notificationHandlers{db: database, cfg: cfg, builder: notifications.NewBuilder(database)}
	requireAuth := middleware.RequireAuth(cfg.Auth.JWTSigningKey)
	orgScope := middleware.OrgScope(database.Pool())
	auth := func(handler http.Handler) http.Handler {
		return requireAuth(orgScope(handler))
	}

	mux.Handle("GET /api/notifications/channels", auth(http.HandlerFunc(h.listChannels)))
	mux.Handle("POST /api/notifications/channels", auth(http.HandlerFunc(h.createChannel)))
	mux.Handle("PATCH /api/notifications/channels/{id}", auth(http.HandlerFunc(h.patchChannel)))
	mux.Handle("DELETE /api/notifications/channels/{id}", auth(http.HandlerFunc(h.deleteChannel)))
	mux.Handle("GET /api/notifications/preview", auth(http.HandlerFunc(h.preview)))
	mux.Handle("POST /api/notifications/channels/{id}/test", auth(http.HandlerFunc(h.testSend)))
	mux.Handle("GET /api/notifications/log", auth(http.HandlerFunc(h.log)))
}

type notificationHandlers struct {
	db      *db.DB
	cfg     *config.Config
	builder *notifications.Builder
}

// validKind reports whether a channel kind is supported.
func validChannelKind(k string) bool {
	return k == "slack" || k == "webhook" || k == "email"
}

func validSchedule(s string) bool { return s == "weekly" || s == "daily" }

// requireManager checks the caller is an owner/admin; writes an error and
// returns false otherwise.
func (h *notificationHandlers) requireManager(w http.ResponseWriter, r *http.Request, orgID string) bool {
	user := middleware.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return false
	}
	role, err := store.GetMemberRole(r.Context(), h.db.Pool(), orgID, user.ID)
	if err != nil || !canManageMembers(role) {
		writeError(w, http.StatusForbidden, "only owners and admins can manage notifications")
		return false
	}
	return true
}

// ── Channel DTOs ───────────────────────────────────────────────────────────────

type channelsResponse struct {
	Channels        []*store.NotificationChannel `json:"channels"`
	EmailConfigured bool                         `json:"emailConfigured"`
}

type createChannelRequest struct {
	Kind     string             `json:"kind"`
	Target   string             `json:"target"`
	Label    string             `json:"label"`
	Enabled  *bool              `json:"enabled"`
	Digests  *store.DigestPrefs `json:"digests"`
	Schedule string             `json:"schedule"`
}

type patchChannelRequest struct {
	Target   *string            `json:"target"`
	Label    *string            `json:"label"`
	Enabled  *bool              `json:"enabled"`
	Digests  *store.DigestPrefs `json:"digests"`
	Schedule *string            `json:"schedule"`
}

// ── GET /api/notifications/channels ─────────────────────────────────────────────

func (h *notificationHandlers) listChannels(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "org context required")
		return
	}

	var channels []*store.NotificationChannel
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		var e error
		channels, e = store.ListNotificationChannels(r.Context(), tx, orgID)
		return e
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channels")
		return
	}
	if channels == nil {
		channels = []*store.NotificationChannel{}
	}

	writeJSON(w, http.StatusOK, channelsResponse{
		Channels:        channels,
		EmailConfigured: notifications.EmailConfigured(),
	})
}

// ── POST /api/notifications/channels ────────────────────────────────────────────

func (h *notificationHandlers) createChannel(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "org context required")
		return
	}
	if !h.requireManager(w, r, orgID) {
		return
	}

	var req createChannelRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Kind = strings.TrimSpace(req.Kind)
	req.Target = strings.TrimSpace(req.Target)
	req.Schedule = strings.TrimSpace(req.Schedule)

	if !validChannelKind(req.Kind) {
		writeError(w, http.StatusBadRequest, "kind must be slack, webhook, or email")
		return
	}
	if req.Target == "" {
		writeError(w, http.StatusBadRequest, "target is required")
		return
	}
	if (req.Kind == "slack" || req.Kind == "webhook") && !strings.HasPrefix(req.Target, "https://") {
		writeError(w, http.StatusBadRequest, "webhook target must be an https URL")
		return
	}
	if req.Kind == "email" && !strings.Contains(req.Target, "@") {
		writeError(w, http.StatusBadRequest, "email target must be an email address")
		return
	}
	if req.Schedule == "" {
		req.Schedule = "weekly"
	}
	if !validSchedule(req.Schedule) {
		writeError(w, http.StatusBadRequest, "schedule must be weekly or daily")
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	digests := store.DigestPrefs{WeeklyStatus: true, StalePRs: true, OOO: true}
	if req.Digests != nil {
		digests = *req.Digests
	}

	var created *store.NotificationChannel
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		var e error
		created, e = store.CreateNotificationChannel(r.Context(), tx, orgID, req.Kind, req.Target, req.Label, enabled, digests, req.Schedule)
		return e
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create channel")
		return
	}

	writeJSON(w, http.StatusCreated, created)
}

// ── PATCH /api/notifications/channels/{id} ──────────────────────────────────────

func (h *notificationHandlers) patchChannel(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "org context required")
		return
	}
	if !h.requireManager(w, r, orgID) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "channel id required")
		return
	}

	var req patchChannelRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Schedule != nil {
		s := strings.TrimSpace(*req.Schedule)
		if !validSchedule(s) {
			writeError(w, http.StatusBadRequest, "schedule must be weekly or daily")
			return
		}
		req.Schedule = &s
	}
	if req.Target != nil {
		t := strings.TrimSpace(*req.Target)
		if t == "" {
			writeError(w, http.StatusBadRequest, "target cannot be empty")
			return
		}
		req.Target = &t
	}

	patch := store.NotificationChannelPatch{
		Target:   req.Target,
		Label:    req.Label,
		Enabled:  req.Enabled,
		Digests:  req.Digests,
		Schedule: req.Schedule,
	}

	var updated *store.NotificationChannel
	err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		var e error
		updated, e = store.UpdateNotificationChannel(r.Context(), tx, orgID, id, patch)
		return e
	})
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update channel")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// ── DELETE /api/notifications/channels/{id} ─────────────────────────────────────

func (h *notificationHandlers) deleteChannel(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "org context required")
		return
	}
	if !h.requireManager(w, r, orgID) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "channel id required")
		return
	}

	err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		return store.DeleteNotificationChannel(r.Context(), tx, orgID, id)
	})
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete channel")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── GET /api/notifications/preview?kind=… ───────────────────────────────────────

// previewResponse returns the structured digest plus its rendered forms so the
// frontend can show a live preview without sending.
type previewResponse struct {
	Digest *notifications.Digest `json:"digest"`
	Text   string                `json:"text"`
	Slack  map[string]any        `json:"slack"`
}

func (h *notificationHandlers) preview(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "org context required")
		return
	}
	kind := r.URL.Query().Get("kind")
	if !notifications.ValidKind(kind) {
		writeError(w, http.StatusBadRequest, "kind must be weeklyStatus, stalePRs, or ooo")
		return
	}

	digest, err := h.builder.Build(r.Context(), orgID, kind)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build digest: "+err.Error())
		return
	}
	rendered := notifications.Render(digest)

	// Record the preview in the log (best-effort; failure does not block).
	_ = h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		return store.WriteNotificationLog(r.Context(), tx, orgID, "", kind, "preview", rendered.Summary)
	})

	writeJSON(w, http.StatusOK, previewResponse{
		Digest: digest,
		Text:   rendered.Text,
		Slack:  rendered.SlackPayload,
	})
}

// ── POST /api/notifications/channels/{id}/test ──────────────────────────────────

// testSendResponse reports the result of each digest delivery attempt.
type testSendResult struct {
	Kind   string `json:"kind"`
	Status string `json:"status"` // sent | failed | skipped
	Detail string `json:"detail,omitempty"`
}

type testSendResponse struct {
	Results []testSendResult `json:"results"`
}

func (h *notificationHandlers) testSend(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "org context required")
		return
	}
	if !h.requireManager(w, r, orgID) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "channel id required")
		return
	}

	// Load the channel (RLS-scoped).
	var ch *store.NotificationChannel
	err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		var e error
		ch, e = store.GetNotificationChannel(r.Context(), tx, orgID, id)
		return e
	})
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load channel")
		return
	}

	// Which digests does this channel subscribe to?
	kinds := enabledKinds(ch.Digests)
	if len(kinds) == 0 {
		writeError(w, http.StatusBadRequest, "channel has no digests enabled")
		return
	}

	// Email channel on a server with no SMTP → report clearly, do not attempt.
	emailUnavailable := ch.Kind == "email" && !notifications.EmailConfigured()

	resp := testSendResponse{Results: make([]testSendResult, 0, len(kinds))}
	for _, kind := range kinds {
		res := testSendResult{Kind: kind}

		digest, berr := h.builder.Build(r.Context(), orgID, kind)
		if berr != nil {
			res.Status = "failed"
			res.Detail = "could not build digest"
			h.logSend(r, orgID, ch.ID, kind, "failed", "build failed")
			resp.Results = append(resp.Results, res)
			continue
		}
		rendered := notifications.Render(digest)

		if emailUnavailable {
			res.Status = "skipped"
			res.Detail = "email not configured on this server"
			h.logSend(r, orgID, ch.ID, kind, "failed", "email not configured")
			resp.Results = append(resp.Results, res)
			continue
		}

		derr := notifications.Deliver(r.Context(), ch.Kind, ch.Target, rendered)
		if derr != nil {
			res.Status = "failed"
			res.Detail = derr.Error() // never contains the target (see deliver.go)
			h.logSend(r, orgID, ch.ID, kind, "failed", rendered.Summary)
		} else {
			res.Status = "sent"
			h.logSend(r, orgID, ch.ID, kind, "sent", rendered.Summary)
		}
		resp.Results = append(resp.Results, res)
	}

	writeJSON(w, http.StatusOK, resp)
}

// logSend writes a notification_log row (best-effort).
func (h *notificationHandlers) logSend(r *http.Request, orgID, channelID, kind, status, summary string) {
	_ = h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		return store.WriteNotificationLog(r.Context(), tx, orgID, channelID, kind, status, summary)
	})
}

// enabledKinds maps a channel's digest prefs to the ordered list of digest kinds.
func enabledKinds(p store.DigestPrefs) []string {
	var out []string
	if p.WeeklyStatus {
		out = append(out, notifications.KindWeeklyStatus)
	}
	if p.StalePRs {
		out = append(out, notifications.KindStalePRs)
	}
	if p.OOO {
		out = append(out, notifications.KindOOO)
	}
	return out
}

// ── GET /api/notifications/log ──────────────────────────────────────────────────

func (h *notificationHandlers) log(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "org context required")
		return
	}

	var entries []*store.NotificationLogEntry
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		var e error
		entries, e = store.ListNotificationLog(r.Context(), tx, orgID, 50)
		return e
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list log")
		return
	}
	if entries == nil {
		entries = []*store.NotificationLogEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}
