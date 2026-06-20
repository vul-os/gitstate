// Package api — webhooks.go
// HTTP surface for inbound webhooks (real-time sync) + CI/CD deployments/incidents.
//
// Two registrars (orchestrator wires both; this file does NOT edit router.go):
//
//   - RegisterWebhookReceiver(mux, database)        — PUBLIC, UNAUTHENTICATED.
//     POST /api/webhooks/{provider}   (github | gitlab)
//     Verifies the signature, resolves the org with NO prior org context, then
//     processes the event inside db.WithOrg. Mounted on the public mux like the
//     public invoice-share route.
//
//   - RegisterWebhookRoutes(mux, database, cfg)      — RequireAuth + OrgScope.
//     GET   /api/webhooks/config           — payload URLs + whether a secret is set
//     POST  /api/webhooks/config           — generate/rotate a per-provider secret (reveal once)
//     GET   /api/deployments               — list deployments
//     POST  /api/deployments               — record a manual deployment
//     GET   /api/incidents                 — list incidents
//     POST  /api/incidents                 — open an incident
//     PATCH /api/incidents/{id}            — resolve an incident
//
// SECURITY: GitHub uses HMAC-SHA256 over the raw body (X-Hub-Signature-256); the
// org is identified by a ?org= hint in the payload URL, and that org's secret is
// read under RLS before verifying. GitLab uses X-Gitlab-Token equality, resolved
// via the SECURITY DEFINER webhook_org_by_secret() lookup. Bad signature → 401.
// Secrets and raw bodies are NEVER logged.
package api

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/middleware"
	"github.com/exo/gitstate/internal/store"
	"github.com/exo/gitstate/internal/webhooks"
)

// maxWebhookBody caps the raw payload we'll buffer for HMAC + parse (GitHub's
// documented max is 25MB; we accept up to that).
const maxWebhookBody = 25 << 20

// ── Public receiver (unauthenticated) ───────────────────────────────────────────

// RegisterWebhookReceiver wires the PUBLIC inbound webhook endpoint. The
// orchestrator mounts this on the public mux (no auth middleware).
func RegisterWebhookReceiver(mux *http.ServeMux, database *db.DB) {
	h := &webhookHandlers{db: database, dedupe: webhooks.NewDeliveryDeduper(0, 0)}
	mux.HandleFunc("POST /api/webhooks/{provider}", h.receive)
}

type webhookHandlers struct {
	db     *db.DB
	cfg    *config.Config
	dedupe *webhooks.DeliveryDeduper
}

// deliveryID returns the provider's unique delivery identifier, used for replay
// dedupe. GitHub stamps X-GitHub-Delivery; GitLab stamps X-Gitlab-Event-UUID.
func deliveryID(r *http.Request, provider string) string {
	switch provider {
	case "github":
		return r.Header.Get("X-GitHub-Delivery")
	case "gitlab":
		return r.Header.Get("X-Gitlab-Event-UUID")
	}
	return ""
}

func (h *webhookHandlers) receive(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(r.PathValue("provider"))
	if provider != "github" && provider != "gitlab" {
		writeError(w, http.StatusNotFound, "unknown provider")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read body")
		return
	}

	// Resolve the org + verify the signature. On failure → 401 (never reveal why).
	orgID, event, ok := h.authenticateDelivery(r, provider, body)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid signature")
		return
	}

	// Replay protection: a previously-seen (org, delivery-id) within the window is
	// acknowledged 200 but skipped, so a captured signed delivery can't be re-applied.
	// Only check after the signature passes (so unauthenticated traffic can't probe
	// or pollute the cache).
	if h.dedupe != nil && h.dedupe.Seen(orgID, deliveryID(r, provider)) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "duplicate": true, "event": event})
		return
	}

	res, perr := webhooks.Process(r.Context(), h.db, orgID, provider, event, body)
	if perr != nil {
		// Defensive: log without body/secret. Still 200 if it was a transient
		// processing miss? No — surface 500 so the platform retries.
		slog.Error("webhook process", "provider", provider, "event", event, "org_id", orgID, "err", perr)
		writeError(w, http.StatusInternalServerError, "processing error")
		return
	}

	// Unknown/ignored events are accepted (200) — platforms expect 2xx so they
	// don't disable the hook.
	if res.Ignored {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": true, "event": event})
		return
	}
	slog.Info("webhook processed",
		"provider", provider, "event", event,
		"commits", res.Commits, "prs", res.PRs, "issues", res.Issues,
		"deployments", res.Deployments, "incidents_opened", res.Incidents, "incidents_closed", res.Closed)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "event": event,
		"commits": res.Commits, "prs": res.PRs, "issues": res.Issues,
		"deployments": res.Deployments,
	})
}

// authenticateDelivery resolves the org and verifies the signature for a raw
// delivery. Returns (orgID, eventName, ok). It NEVER logs secrets/bodies.
func (h *webhookHandlers) authenticateDelivery(r *http.Request, provider string, body []byte) (string, string, bool) {
	switch provider {
	case "gitlab":
		// X-Gitlab-Token IS the shared secret → resolve org directly.
		token := r.Header.Get("X-Gitlab-Token")
		if token == "" {
			return "", "", false
		}
		orgID, err := store.WebhookOrgBySecret(r.Context(), h.db.Pool(), "gitlab", token)
		if err != nil {
			return "", "", false
		}
		event := r.Header.Get("X-Gitlab-Event")
		return orgID, event, true

	case "github":
		// The org hint is baked into the payload URL the user copies from Settings.
		orgHint := r.URL.Query().Get("org")
		sig := r.Header.Get("X-Hub-Signature-256")
		if orgHint == "" || sig == "" {
			return "", "", false
		}
		// Read the org's secret under RLS, then HMAC-verify against it.
		var secret string
		err := h.db.WithOrg(r.Context(), orgHint, func(tx pgx.Tx) error {
			s, e := store.GetEnabledWebhookSecret(r.Context(), tx, orgHint, "github")
			secret = s
			return e
		})
		if err != nil || secret == "" {
			return "", "", false
		}
		if !webhooks.VerifyGitHubSignature(secret, body, sig) {
			return "", "", false
		}
		event := r.Header.Get("X-GitHub-Event")
		return orgHint, event, true
	}
	return "", "", false
}

// ── Authed config + data (RequireAuth + OrgScope) ───────────────────────────────

// RegisterWebhookRoutes wires the authed webhook config + deployment/incident
// endpoints. The orchestrator wires this on the main mux.
func RegisterWebhookRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	h := &webhookHandlers{db: database, cfg: cfg}

	requireAuth := middleware.RequireAuth(cfg.Auth.JWTSigningKey)
	orgScope := middleware.OrgScope(database.Pool())
	auth := func(handler http.Handler) http.Handler {
		return requireAuth(orgScope(handler))
	}

	mux.Handle("GET /api/webhooks/config", auth(http.HandlerFunc(h.getConfig)))
	mux.Handle("POST /api/webhooks/config", auth(http.HandlerFunc(h.rotateSecret)))

	mux.Handle("GET /api/deployments", auth(http.HandlerFunc(h.listDeployments)))
	mux.Handle("POST /api/deployments", auth(http.HandlerFunc(h.createDeployment)))

	mux.Handle("GET /api/incidents", auth(http.HandlerFunc(h.listIncidents)))
	mux.Handle("POST /api/incidents", auth(http.HandlerFunc(h.createIncident)))
	mux.Handle("PATCH /api/incidents/{id}", auth(http.HandlerFunc(h.resolveIncident)))
}

type webhookProviderConfig struct {
	Provider    string  `json:"provider"`
	PayloadURL  string  `json:"payloadUrl"`
	SecretSet   bool    `json:"secretSet"`
	Enabled     bool    `json:"enabled"`
	LastEventAt *string `json:"lastEventAt"`
}

type webhookConfigResponse struct {
	PublicURL string                  `json:"publicUrl"`
	Providers []webhookProviderConfig `json:"providers"`
}

func (h *webhookHandlers) getConfig(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())

	var configs []store.WebhookConfig
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		c, e := store.ListWebhookConfigs(r.Context(), tx, orgID)
		configs = c
		return e
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not load webhook config")
		return
	}

	byProvider := map[string]store.WebhookConfig{}
	for _, c := range configs {
		byProvider[c.Provider] = c
	}

	out := webhookConfigResponse{PublicURL: h.publicURL(), Providers: []webhookProviderConfig{}}
	for _, p := range []string{"github", "gitlab"} {
		pc := webhookProviderConfig{Provider: p, PayloadURL: h.payloadURL(p, orgID)}
		if c, ok := byProvider[p]; ok {
			pc.SecretSet = c.Secret != ""
			pc.Enabled = c.Enabled
			if c.LastEventAt != nil {
				s := c.LastEventAt.UTC().Format(time.RFC3339)
				pc.LastEventAt = &s
			}
		}
		out.Providers = append(out.Providers, pc)
	}
	writeJSON(w, http.StatusOK, out)
}

type rotateSecretRequest struct {
	Provider string `json:"provider"`
}

type rotateSecretResponse struct {
	Provider   string `json:"provider"`
	Secret     string `json:"secret"` // revealed ONCE
	PayloadURL string `json:"payloadUrl"`
}

func (h *webhookHandlers) rotateSecret(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())

	// Only owners/admins may rotate the org's webhook HMAC secret — rotating it
	// breaks every configured delivery until the provider is re-pasted, so a bare
	// member must not be able to DoS the org's real-time sync.
	user := middleware.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	role, err := store.GetMemberRole(r.Context(), h.db.Pool(), orgID, user.ID)
	if err != nil || !canManageMembers(role) {
		writeError(w, http.StatusForbidden, "only owners and admins can rotate the webhook secret")
		return
	}

	var req rotateSecretRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	if provider != "github" && provider != "gitlab" {
		writeError(w, http.StatusBadRequest, "provider must be github or gitlab")
		return
	}

	secret, err := webhooks.GenerateSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not generate secret")
		return
	}

	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		_, e := store.UpsertWebhookSecret(r.Context(), tx, orgID, provider, secret)
		return e
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save secret")
		return
	}

	writeJSON(w, http.StatusOK, rotateSecretResponse{
		Provider:   provider,
		Secret:     secret,
		PayloadURL: h.payloadURL(provider, orgID),
	})
}

// ── deployments ─────────────────────────────────────────────────────────────────

func (h *webhookHandlers) listDeployments(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	f := store.DeploymentFilter{Limit: 100, Environment: r.URL.Query().Get("environment")}
	if s := r.URL.Query().Get("from"); s != "" {
		if t, ok := parseEngDate(s); ok {
			f.From = t
		}
	}
	if s := r.URL.Query().Get("to"); s != "" {
		if t, ok := parseEngDate(s); ok {
			f.To = t
		}
	}

	var deps []store.Deployment
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		d, e := store.ListDeployments(r.Context(), tx, orgID, f)
		deps = d
		return e
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not list deployments")
		return
	}
	if deps == nil {
		deps = []store.Deployment{}
	}
	writeJSON(w, http.StatusOK, deps)
}

type createDeploymentRequest struct {
	RepoID      string `json:"repoId"`
	Environment string `json:"environment"`
	Status      string `json:"status"`
	SHA         string `json:"sha"`
	DeployedAt  string `json:"deployedAt"`
}

func (h *webhookHandlers) createDeployment(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	var req createDeploymentRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	in := store.DeploymentInput{
		OrgID:       orgID,
		RepoID:      req.RepoID,
		Environment: req.Environment,
		Status:      req.Status,
		SHA:         req.SHA,
		Source:      "manual",
	}
	if t, ok := parseEngDate(req.DeployedAt); ok {
		in.DeployedAt = t
	}

	var dep *store.Deployment
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		d, e := store.InsertDeployment(r.Context(), tx, in)
		dep = d
		return e
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not record deployment")
		return
	}
	writeJSON(w, http.StatusCreated, dep)
}

// ── incidents ───────────────────────────────────────────────────────────────────

func (h *webhookHandlers) listIncidents(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	f := store.IncidentFilter{Limit: 100, OpenOnly: r.URL.Query().Get("open") == "true"}

	var incs []store.Incident
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		i, e := store.ListIncidents(r.Context(), tx, orgID, f)
		incs = i
		return e
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not list incidents")
		return
	}
	if incs == nil {
		incs = []store.Incident{}
	}
	writeJSON(w, http.StatusOK, incs)
}

type createIncidentRequest struct {
	RepoID   string `json:"repoId"`
	Title    string `json:"title"`
	Severity string `json:"severity"`
	OpenedAt string `json:"openedAt"`
}

func (h *webhookHandlers) createIncident(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	var req createIncidentRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	in := store.IncidentInput{
		OrgID:    orgID,
		RepoID:   req.RepoID,
		Title:    strings.TrimSpace(req.Title),
		Severity: req.Severity,
	}
	if in.Title == "" {
		in.Title = "Incident"
	}
	if t, ok := parseEngDate(req.OpenedAt); ok {
		in.OpenedAt = t
	}

	var inc *store.Incident
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		i, e := store.InsertIncident(r.Context(), tx, in)
		inc = i
		return e
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not open incident")
		return
	}
	writeJSON(w, http.StatusCreated, inc)
}

type resolveIncidentRequest struct {
	ResolvedAt string `json:"resolvedAt"`
}

func (h *webhookHandlers) resolveIncident(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing id")
		return
	}
	var req resolveIncidentRequest
	_ = decodeJSON(r, &req)
	at := time.Now().UTC()
	if t, ok := parseEngDate(req.ResolvedAt); ok {
		at = t
	}

	var inc *store.Incident
	err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		i, e := store.ResolveIncident(r.Context(), tx, orgID, id, at)
		inc = i
		return e
	})
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "incident not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not resolve incident")
		return
	}
	writeJSON(w, http.StatusOK, inc)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (h *webhookHandlers) publicURL() string {
	if h.cfg != nil {
		return strings.TrimRight(h.cfg.App.PublicURL, "/")
	}
	return ""
}

// payloadURL builds the URL the user pastes into the provider's webhook settings.
// GitHub carries the org hint as a query param (needed to find the HMAC secret
// pre-auth); GitLab identifies the org by its X-Gitlab-Token secret, so no hint.
func (h *webhookHandlers) payloadURL(provider, orgID string) string {
	base := h.publicURL() + "/api/webhooks/" + provider
	if provider == "github" {
		return base + "?org=" + orgID
	}
	return base
}
