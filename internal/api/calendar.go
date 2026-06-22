// Package api — Google/Microsoft calendar connection + two-way leave/availability
// sync routes.
//
// A member authorizes once; their access + refresh tokens are stored AES-256-GCM
// encrypted in calendar_connections (per user, per provider). From there:
//   - approved leave is pushed as an all-day OOO event (PushApprovedLeave, called
//     by the leave-approval handler — wired by the orchestrator);
//   - busy/OOO windows are pulled back into availability (POST /api/calendar/sync).
//
// The OAuth client id/secret are reused from the login providers
// (cfg.Auth.Providers.{Google,Microsoft}); only the calendar scopes differ
// (decisions A6 config-gating, S3 secrets, S1 RLS).
package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/exo/gitstate/internal/auth"
	"github.com/exo/gitstate/internal/calendar"
	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/crypto"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/middleware"
	oauthpkg "github.com/exo/gitstate/internal/oauth"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"
)

// RegisterCalendarRoutes wires the calendar connection + sync endpoints behind
// RequireAuth + OrgScope (except the provider callback, which recovers the
// org/user from a signed-ish state cookie). Called by the orchestrator from
// router.go — this package does NOT edit router.go.
//
//	GET    /api/calendar/{provider}/start     → 302 to provider authorize (404 if not configured)
//	GET    /api/calendar/{provider}/callback  → exchange, encrypt+store, redirect to /capacity
//	GET    /api/calendar/status               → [{provider, connected, email, pushLeave, pullBusy, configured}]
//	DELETE /api/calendar/{provider}           → disconnect (delete stored tokens)
//	PATCH  /api/calendar/{provider}           → toggle pushLeave/pullBusy
//	POST   /api/calendar/sync                 → pull busy → adjust caller's availability
func RegisterCalendarRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	providers := oauthpkg.LoadCalendars(cfg, cfg.App.PublicURL)
	h := &calendarHandlers{db: database, cfg: cfg, providers: providers}

	requireAuth := middleware.RequireAuth(cfg.Auth.JWTSigningKey)
	orgScope := middleware.OrgScope(database.Pool())
	authed := func(handler http.Handler) http.Handler {
		return requireAuth(orgScope(handler))
	}

	mux.Handle("GET /api/calendar/status", authed(http.HandlerFunc(h.status)))
	mux.Handle("DELETE /api/calendar/{provider}", authed(http.HandlerFunc(h.disconnect)))
	mux.Handle("PATCH /api/calendar/{provider}", authed(http.HandlerFunc(h.toggle)))
	mux.Handle("POST /api/calendar/sync", authed(http.HandlerFunc(h.sync)))

	// start is a top-level browser navigation (provider redirect must land on a
	// real page), so it can't carry the Bearer/X-Org-ID headers the middleware
	// needs — it self-authenticates from ?token= and ?org= query params.
	mux.HandleFunc("GET /api/calendar/{provider}/start", h.start)

	// callback is a top-level provider redirect (no Authorization header) — the
	// org/user are recovered from the state cookie set at /start.
	mux.HandleFunc("GET /api/calendar/{provider}/callback", h.callback)
}

type calendarHandlers struct {
	db        *db.DB
	cfg       *config.Config
	providers oauthpkg.CalProviders
}

// calendarStateCookie carries CSRF state + the org/user that initiated the flow
// across the provider redirect (the callback has no auth context).
const calendarStateCookie = "gs_calendar_state"

type calendarState struct {
	State    string `json:"s"`
	OrgID    string `json:"o"`
	UserID   string `json:"u"`
	Provider string `json:"p"`
}

// ── GET /api/calendar/{provider}/start ─────────────────────────────────────────

func (h *calendarHandlers) start(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(r.PathValue("provider"))
	p, ok := h.providers[provider]
	if !ok {
		writeError(w, http.StatusNotFound, "calendar provider not configured")
		return
	}

	tokenStr := r.URL.Query().Get("token")
	orgID := r.URL.Query().Get("org")
	if tokenStr == "" {
		if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			tokenStr = bearer
		}
	}
	claims, err := auth.ParseAccessToken(h.cfg.Auth.JWTSigningKey, tokenStr)
	if err != nil || orgID == "" {
		writeError(w, http.StatusUnauthorized, "missing or invalid auth")
		return
	}

	// The org is supplied via an attacker-controllable query param and this flow
	// self-authenticates via ?token= (outside OrgScope), so verify the JWT user is
	// actually a member of this org before binding a calendar connection to it.
	// Otherwise any authenticated user could attach their calendar to (or overwrite
	// a member's connection in) an org they don't belong to.
	if _, err := store.GetMemberRole(r.Context(), h.db.Pool(), orgID, claims.UserID()); err != nil {
		writeError(w, http.StatusForbidden, "not a member of this organization")
		return
	}

	stateVal, err := oauthpkg.GenerateState()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generate state")
		return
	}

	cs := calendarState{State: stateVal, OrgID: orgID, UserID: claims.UserID(), Provider: provider}
	raw, _ := json.Marshal(cs)
	http.SetCookie(w, &http.Cookie{
		Name:     calendarStateCookie,
		Value:    base64.RawURLEncoding.EncodeToString(raw),
		Path:     "/",
		MaxAge:   600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(r),
	})

	http.Redirect(w, r, p.AuthCodeURL(stateVal), http.StatusFound)
}

// ── GET /api/calendar/{provider}/callback ──────────────────────────────────────

func (h *calendarHandlers) callback(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(r.PathValue("provider"))
	p, ok := h.providers[provider]
	if !ok {
		writeError(w, http.StatusNotFound, "calendar provider not configured")
		return
	}

	cookie, err := r.Cookie(calendarStateCookie)
	if err != nil || cookie.Value == "" {
		writeError(w, http.StatusBadRequest, "missing state cookie")
		return
	}
	rawState, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid state cookie")
		return
	}
	var cs calendarState
	if err := json.Unmarshal(rawState, &cs); err != nil {
		writeError(w, http.StatusBadRequest, "invalid state cookie")
		return
	}
	// Clear the state cookie.
	http.SetCookie(w, &http.Cookie{
		Name: calendarStateCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})

	if r.URL.Query().Get("state") != cs.State || cs.Provider != provider || cs.OrgID == "" || cs.UserID == "" {
		writeError(w, http.StatusBadRequest, "state mismatch")
		return
	}

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		h.redirectCapacity(w, r, "error="+errParam)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, "missing code")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	token, acct, err := p.Exchange(ctx, code)
	if err != nil {
		slog.Error("calendar: exchange", "provider", provider, "err", err)
		h.redirectCapacity(w, r, "error=exchange_failed")
		return
	}

	key, err := crypto.KeyFromEnv()
	if err != nil {
		slog.Error("calendar: encryption key", "err", err)
		h.redirectCapacity(w, r, "error=server_misconfigured")
		return
	}
	encToken, err := crypto.Encrypt([]byte(token.AccessToken), key)
	if err != nil {
		slog.Error("calendar: encrypt token", "err", err)
		h.redirectCapacity(w, r, "error=encrypt_failed")
		return
	}
	var encRefresh []byte
	if token.RefreshToken != "" {
		if encRefresh, err = crypto.Encrypt([]byte(token.RefreshToken), key); err != nil {
			slog.Error("calendar: encrypt refresh", "err", err)
		}
	}

	in := store.UpsertCalendarConnectionInput{
		OrgID:            cs.OrgID,
		UserID:           cs.UserID,
		Provider:         provider,
		ExternalEmail:    acct.Email,
		TokenEncrypted:   encToken,
		RefreshEncrypted: encRefresh,
		Scopes:           strings.Join(p.Scopes(), " "),
	}
	if !token.Expiry.IsZero() {
		exp := token.Expiry.UTC()
		in.ExpiresAt = &exp
	}

	if err := h.db.WithOrg(r.Context(), cs.OrgID, func(tx pgx.Tx) error {
		_, e := store.UpsertCalendarConnection(r.Context(), tx, in)
		return e
	}); err != nil {
		slog.Error("calendar: store connection", "provider", provider, "err", err)
		h.redirectCapacity(w, r, "error=store_failed")
		return
	}

	h.redirectCapacity(w, r, "calendar="+provider)
}

func (h *calendarHandlers) redirectCapacity(w http.ResponseWriter, r *http.Request, query string) {
	url := fmt.Sprintf("%s/capacity?%s", h.cfg.App.PublicURL, query)
	http.Redirect(w, r, url, http.StatusFound)
}

// ── GET /api/calendar/status ───────────────────────────────────────────────────

type calendarStatus struct {
	Provider   string `json:"provider"`
	Connected  bool   `json:"connected"`
	Configured bool   `json:"configured"`
	Email      string `json:"email,omitempty"`
	PushLeave  bool   `json:"pushLeave"`
	PullBusy   bool   `json:"pullBusy"`
}

func (h *calendarHandlers) status(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	user := middleware.UserFromContext(r.Context())

	byProvider := map[string]*calendarStatus{}
	for _, prov := range []string{"google", "microsoft"} {
		_, configured := h.providers[prov]
		byProvider[prov] = &calendarStatus{Provider: prov, Configured: configured}
	}

	var conns []*store.CalendarConnection
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		c, e := store.ListCalendarConnectionsForUser(r.Context(), tx, orgID, user.ID)
		conns = c
		return e
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "list calendar connections")
		return
	}
	for _, c := range conns {
		if s, ok := byProvider[c.Provider]; ok {
			s.Connected = true
			s.Email = c.ExternalEmail
			s.PushLeave = c.PushLeave
			s.PullBusy = c.PullBusy
		}
	}

	out := []calendarStatus{*byProvider["google"], *byProvider["microsoft"]}
	writeJSON(w, http.StatusOK, out)
}

// ── DELETE /api/calendar/{provider} ────────────────────────────────────────────

func (h *calendarHandlers) disconnect(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(r.PathValue("provider"))
	orgID := middleware.OrgFromContext(r.Context())
	user := middleware.UserFromContext(r.Context())

	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		return store.DeleteCalendarConnection(r.Context(), tx, orgID, user.ID, provider)
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "disconnect calendar")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── PATCH /api/calendar/{provider} ─────────────────────────────────────────────

func (h *calendarHandlers) toggle(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(r.PathValue("provider"))
	orgID := middleware.OrgFromContext(r.Context())
	user := middleware.UserFromContext(r.Context())

	var body struct {
		PushLeave *bool `json:"pushLeave"`
		PullBusy  *bool `json:"pullBusy"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	var resp calendarStatus
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		cur, err := store.GetCalendarConnection(r.Context(), tx, orgID, user.ID, provider)
		if err != nil {
			return err
		}
		push, pull := cur.PushLeave, cur.PullBusy
		if body.PushLeave != nil {
			push = *body.PushLeave
		}
		if body.PullBusy != nil {
			pull = *body.PullBusy
		}
		updated, err := store.UpdateCalendarToggles(r.Context(), tx, orgID, user.ID, provider, push, pull)
		if err != nil {
			return err
		}
		resp = calendarStatus{
			Provider: updated.Provider, Connected: true, Configured: true,
			Email: updated.ExternalEmail, PushLeave: updated.PushLeave, PullBusy: updated.PullBusy,
		}
		return nil
	}); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no calendar connection for provider")
			return
		}
		writeError(w, http.StatusInternalServerError, "update calendar toggles")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ── POST /api/calendar/sync ────────────────────────────────────────────────────

type syncRequest struct {
	From string `json:"from"` // YYYY-MM-DD (default: today)
	To   string `json:"to"`   // YYYY-MM-DD (default: from + 30d)
}

type syncResponse struct {
	Synced              int          `json:"synced"`     // connections pulled
	BusyDays            int          `json:"busyDays"`   // distinct busy days found
	BusyBlocks          []busyDayDTO `json:"busyBlocks"` // the busy windows (for display)
	AvailabilityWritten bool         `json:"availabilityWritten"`
}

type busyDayDTO struct {
	Provider string `json:"provider"`
	Start    string `json:"start"`
	End      string `json:"end"`
	Status   string `json:"status"`
}

func (h *calendarHandlers) sync(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	user := middleware.UserFromContext(r.Context())

	var body syncRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
	}
	from := parseDayOrDefault(body.From, time.Now().UTC().Truncate(24*time.Hour))
	to := parseDayOrDefault(body.To, from.AddDate(0, 0, 30))
	if !to.After(from) {
		writeError(w, http.StatusBadRequest, "'to' must be after 'from'")
		return
	}

	key, err := crypto.KeyFromEnv()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server misconfigured")
		return
	}

	// Load the caller's pull-enabled connections.
	var conns []*store.CalendarConnection
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		c, e := store.ListCalendarConnectionsForUser(r.Context(), tx, orgID, user.ID)
		conns = c
		return e
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "list calendar connections")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	resp := syncResponse{BusyBlocks: []busyDayDTO{}}
	busyDays := map[string]struct{}{}

	for _, conn := range conns {
		if !conn.PullBusy {
			continue
		}
		client, derr := h.calendarClient(key, conn)
		if derr != nil {
			slog.Warn("calendar: build client", "provider", conn.Provider, "err", derr)
			continue
		}
		blocks, perr := client.PullBusy(ctx, from, to)
		if perr != nil {
			slog.Warn("calendar: pull busy", "provider", conn.Provider, "err", perr)
			continue
		}
		resp.Synced++
		for _, b := range blocks {
			resp.BusyBlocks = append(resp.BusyBlocks, busyDayDTO{
				Provider: conn.Provider,
				Start:    b.Start.UTC().Format(time.RFC3339),
				End:      b.End.UTC().Format(time.RFC3339),
				Status:   b.Status,
			})
			for d := b.Start.UTC().Truncate(24 * time.Hour); d.Before(b.End.UTC()); d = d.AddDate(0, 0, 1) {
				busyDays[d.Format("2006-01-02")] = struct{}{}
			}
		}
		// Persist any refreshed token so we don't re-refresh next time.
		h.persistRefreshedToken(r.Context(), key, orgID, user.ID, conn, client.RefreshedToken())
		_ = h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
			return store.MarkCalendarSynced(r.Context(), tx, orgID, user.ID, conn.Provider)
		})
	}

	resp.BusyDays = len(busyDays)

	// Reflect calendar reality in availability: if the calendar shows busy days
	// in the window, reduce the member's effective weekly hours proportionally to
	// the share of working days that are blocked. This keeps capacity honest
	// (derived-not-entered) without inventing leave entries.
	if resp.BusyDays > 0 {
		if wrote := h.applyBusyToAvailability(r.Context(), orgID, user.ID, from, busyDays); wrote {
			resp.AvailabilityWritten = true
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// applyBusyToAvailability writes a new availability snapshot whose weekly_hours
// are scaled down by the fraction of working days (in the synced window) that the
// calendar reports as busy. Returns true if a row was written.
func (h *calendarHandlers) applyBusyToAvailability(ctx context.Context, orgID, userID string, from time.Time, busyDays map[string]struct{}) bool {
	var wrote bool
	_ = h.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		cur, err := store.GetAvailability(ctx, tx, orgID, userID, from)
		var weekly float64 = 40
		workingDays := []int32{1, 2, 3, 4, 5}
		if err == nil {
			weekly = cur.WeeklyHours
			workingDays = cur.WorkingDays
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}

		workingSet := map[int32]struct{}{}
		for _, d := range workingDays {
			workingSet[d] = struct{}{}
		}
		busyWorking := 0
		for day := range busyDays {
			d, perr := time.Parse("2006-01-02", day)
			if perr != nil {
				continue
			}
			iso := int32(d.Weekday())
			if iso == 0 {
				iso = 7 // Go Sunday=0 → ISO 7
			}
			if _, ok := workingSet[iso]; ok {
				busyWorking++
			}
		}
		if busyWorking == 0 {
			return nil
		}

		// Scale weekly hours by the share of remaining working days in a 4-week
		// horizon (≈20 working days). Clamp to keep it positive (constraint > 0).
		const horizonWorkingDays = 20.0
		factor := 1 - float64(busyWorking)/horizonWorkingDays
		if factor < 0.05 {
			factor = 0.05
		}
		scaled := weekly * factor
		if scaled <= 0 {
			scaled = 0.5
		}

		if _, err := store.UpsertAvailability(ctx, tx, orgID, userID, scaled, workingDays, from); err != nil {
			return err
		}
		wrote = true
		return nil
	})
	return wrote
}

// ── Shared helpers ─────────────────────────────────────────────────────────────

// calendarClient decrypts a connection's tokens and builds a calendar.Client,
// wiring the provider's oauth2.Config so the client can refresh on expiry.
func (h *calendarHandlers) calendarClient(key [32]byte, conn *store.CalendarConnection) (*calendar.Client, error) {
	return buildCalendarClient(h.cfg, key, conn)
}

// persistRefreshedToken stores a refreshed access token if it changed.
func (h *calendarHandlers) persistRefreshedToken(ctx context.Context, key [32]byte, orgID, userID string, conn *store.CalendarConnection, tok *oauth2.Token) {
	persistRefreshedToken(ctx, h.db, key, orgID, userID, conn, tok)
}

// buildCalendarClient is shared by the handlers and PushApprovedLeave.
func buildCalendarClient(cfg *config.Config, key [32]byte, conn *store.CalendarConnection) (*calendar.Client, error) {
	if len(conn.TokenEncrypted) == 0 {
		return nil, store.ErrNotFound
	}
	accessBytes, err := crypto.Decrypt(conn.TokenEncrypted, key)
	if err != nil {
		return nil, fmt.Errorf("decrypt access token: %w", err)
	}
	var refresh string
	if len(conn.RefreshEncrypted) > 0 {
		rb, derr := crypto.Decrypt(conn.RefreshEncrypted, key)
		if derr == nil {
			refresh = string(rb)
		}
	}

	providers := oauthpkg.LoadCalendars(cfg, cfg.App.PublicURL)
	var oc *oauth2.Config
	if p, ok := providers[conn.Provider]; ok {
		oc = p.Config()
	}

	cc := calendar.Conn{
		Provider:     conn.Provider,
		AccessToken:  string(accessBytes),
		RefreshToken: refresh,
		CalendarID:   conn.CalendarID,
		OAuth:        oc,
	}
	if conn.ExpiresAt != nil {
		cc.Expiry = *conn.ExpiresAt
	}
	return calendar.New(cc), nil
}

// persistRefreshedToken stores a refreshed access token (+ expiry) for a
// connection if it differs from what's already stored. Best-effort; errors are
// logged, not returned.
func persistRefreshedToken(ctx context.Context, database *db.DB, key [32]byte, orgID, userID string, conn *store.CalendarConnection, tok *oauth2.Token) {
	if tok == nil || tok.AccessToken == "" {
		return
	}
	// Re-encrypt the current access token and compare expiry to detect a refresh.
	plain, derr := crypto.Decrypt(conn.TokenEncrypted, key)
	if derr == nil && string(plain) == tok.AccessToken {
		return // unchanged
	}

	encToken, err := crypto.Encrypt([]byte(tok.AccessToken), key)
	if err != nil {
		slog.Warn("calendar: re-encrypt refreshed token", "err", err)
		return
	}
	var encRefresh []byte
	if tok.RefreshToken != "" {
		if e, eerr := crypto.Encrypt([]byte(tok.RefreshToken), key); eerr == nil {
			encRefresh = e
		}
	}
	var exp *time.Time
	if !tok.Expiry.IsZero() {
		e := tok.Expiry.UTC()
		exp = &e
	}
	_ = database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		return store.UpdateCalendarTokens(ctx, tx, orgID, userID, conn.Provider, encToken, encRefresh, exp)
	})
}

func parseDayOrDefault(s string, def time.Time) time.Time {
	if s == "" {
		return def
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC()
	}
	return def
}

// ── Exported: push approved leave to a member's calendars ──────────────────────

// PushApprovedLeave pushes an approved leave entry to every push-enabled calendar
// connection the leave's member has, creating or updating an all-day OOO event
// and recording the event id/provider on the leave row so later edits/cancels
// sync. It is safe to call multiple times (idempotent on the stored event id).
//
// The orchestrator wires this into the leave-approval path (it does NOT live in
// capacity.go). Best-effort: a calendar failure does not fail the approval — the
// error is returned for logging but the leave stays approved.
func PushApprovedLeave(ctx context.Context, database *db.DB, cfg *config.Config, orgID, leaveID string) error {
	key, err := crypto.KeyFromEnv()
	if err != nil {
		return fmt.Errorf("calendar push: encryption key: %w", err)
	}

	// Load the leave entry, its member's display name, existing event link, and
	// the member's push-enabled connections — all org-scoped.
	var (
		leave      *store.LeaveEntry
		conns      []*store.CalendarConnection
		prevEvID   string
		prevProv   string
		memberName string
	)
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		l, e := store.GetLeaveEntry(ctx, tx, orgID, leaveID)
		if e != nil {
			return e
		}
		leave = l
		eid, prov, e := store.GetLeaveCalendarLink(ctx, tx, orgID, leaveID)
		if e != nil && !errors.Is(e, store.ErrNotFound) {
			return e
		}
		prevEvID, prevProv = eid, prov
		cs, e := store.ListCalendarConnectionsForUser(ctx, tx, orgID, l.UserID)
		if e != nil {
			return e
		}
		conns = cs
		for _, m := range listMembersBestEffort(ctx, tx, orgID) {
			if m.UserID == l.UserID {
				memberName = m.Name
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("calendar push: load leave: %w", err)
	}

	if leave.Status != "approved" {
		return nil // only approved leave is pushed
	}

	cl := calendar.Leave{
		ID:        leave.ID,
		Kind:      leave.Kind,
		StartDate: leave.StartDate,
		EndDate:   leave.EndDate,
		Note:      leave.Note,
		Name:      memberName,
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var firstErr error
	pushed := false
	for _, conn := range conns {
		if !conn.PushLeave {
			continue
		}
		client, berr := buildCalendarClient(cfg, key, conn)
		if berr != nil {
			if firstErr == nil {
				firstErr = berr
			}
			continue
		}
		existing := ""
		if prevProv == conn.Provider {
			existing = prevEvID
		}
		evID, perr := client.PushLeave(ctx, cl, existing)
		if perr != nil {
			slog.Warn("calendar: push leave", "provider", conn.Provider, "leave", leaveID, "err", perr)
			if firstErr == nil {
				firstErr = perr
			}
			continue
		}
		persistRefreshedToken(ctx, database, key, orgID, leave.UserID, conn, client.RefreshedToken())

		// Record the event link (we store the last-pushed provider's id).
		if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
			return store.SetLeaveCalendarEvent(ctx, tx, orgID, leaveID, evID, conn.Provider)
		}); err != nil && firstErr == nil {
			firstErr = err
		}
		pushed = true
	}

	if !pushed {
		return firstErr // may be nil when the member simply has no push-enabled calendar
	}
	return firstErr
}

// listMembersBestEffort returns org members, swallowing errors (name is cosmetic
// for the event title).
func listMembersBestEffort(ctx context.Context, tx pgx.Tx, orgID string) []store.OrgMember {
	members, err := store.ListMembers(ctx, tx, orgID)
	if err != nil {
		return nil
	}
	return members
}
