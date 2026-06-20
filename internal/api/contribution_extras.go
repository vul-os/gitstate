// Package api — contribution_extras.go
// REST handlers for the three CONTRIBUTION extensions (all on the existing
// contributionHandlers, registered by RegisterContributionRoutes):
//
//	GET  /api/contribution/trends?periods=6&interval=month → per-member composite series
//	GET  /api/equity?period=YYYY-MM-DD                      → advisory ledger (suggested vs actual)
//	PUT  /api/equity   (owner/admin)                        → record actual_pct / pool_label / note
//	GET  /api/kudos?user=                                   → peer recognition feed (+ counts)
//	POST /api/kudos                                          → give kudos (giver = caller)
//
// All routes are behind RequireAuth + OrgScope; every read/write runs inside
// db.WithOrg so RLS enforces the org boundary. The equity ledger is ADVISORY —
// suggestedPct is the contribution-weighted share; it informs, never decides.
package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/exo/gitstate/internal/contribution"
	"github.com/exo/gitstate/internal/middleware"
	"github.com/exo/gitstate/internal/store"
)

// ── Trends ──────────────────────────────────────────────────────────────────

// GET /api/contribution/trends?periods=6&interval=month
func (h *contributionHandlers) trends(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "X-Org-ID header required")
		return
	}
	q := r.URL.Query()
	periods := 6
	if s := q.Get("periods"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			periods = n
		}
	}
	interval := contribution.IntervalMonth
	if q.Get("interval") == "week" {
		interval = contribution.IntervalWeek
	}

	series, err := h.svc.ComputeTrends(r.Context(), orgID, periods, interval, time.Now().UTC())
	if err != nil {
		slog.Error("contribution trends", "err", err)
		writeError(w, http.StatusInternalServerError, "could not compute contribution trends")
		return
	}
	if series == nil {
		series = []contribution.TrendSeries{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"interval": string(interval),
		"periods":  periods,
		"series":   series,
	})
}

// ── Equity ledger (advisory) ────────────────────────────────────────────────

// equityPeriod resolves the ledger window: ?period=YYYY-MM-DD anchors the calendar
// month containing that date; omitted ⇒ the current calendar month. The month is
// the natural equity-grant cadence and matches the snapshot windows.
func equityPeriod(r *http.Request) contribution.Period {
	now := time.Now().UTC()
	anchor := now
	if s := r.URL.Query().Get("period"); s != "" {
		if t, err := parseContribDate(s); err == nil {
			anchor = t
		}
	}
	start := time.Date(anchor.Year(), anchor.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	return contribution.Period{From: start, To: end}
}

// GET /api/equity?period=
func (h *contributionHandlers) equity(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "X-Org-ID header required")
		return
	}
	p := equityPeriod(r)
	ledger, err := h.svc.ComputeEquity(r.Context(), orgID, p)
	if err != nil {
		slog.Error("equity ledger", "err", err)
		writeError(w, http.StatusInternalServerError, "could not compute equity ledger")
		return
	}
	if ledger.Rows == nil {
		ledger.Rows = []contribution.EquityRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"period":   ledger.Period,
		"advisory": true,
		"note":     "Advisory only: suggestedPct is each member's contribution-weighted share of the pool. It informs allocation conversations — it does not decide them.",
		"rows":     ledger.Rows,
	})
}

type putEquityJSON struct {
	UserID    string   `json:"userId"`
	Period    string   `json:"period"`    // YYYY-MM-DD inside the target month (optional)
	ActualPct *float64 `json:"actualPct"` // null clears a previously-entered grant
	PoolLabel string   `json:"poolLabel"`
	Note      string   `json:"note"`
}

// PUT /api/equity — owner/admin only.
func (h *contributionHandlers) putEquity(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "X-Org-ID header required")
		return
	}
	user := middleware.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	role, err := store.GetMemberRole(r.Context(), h.db.Pool(), orgID, user.ID)
	if err != nil || !canManageMembers(role) {
		writeError(w, http.StatusForbidden, "only owners and admins can record equity allocations")
		return
	}

	var body putEquityJSON
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(body.UserID) == "" {
		writeError(w, http.StatusBadRequest, "userId required")
		return
	}
	// The grant target must be a member of THIS org — otherwise we'd persist an
	// equity row for a stranger's id.
	if _, err := store.GetMemberRole(r.Context(), h.db.Pool(), orgID, body.UserID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "userId is not a member of this org")
			return
		}
		slog.Error("equity put member lookup", "err", err)
		writeError(w, http.StatusInternalServerError, "could not validate member")
		return
	}
	if body.ActualPct != nil && (*body.ActualPct < 0 || *body.ActualPct > 100) {
		writeError(w, http.StatusBadRequest, "actualPct must be between 0 and 100")
		return
	}

	// Resolve the same calendar-month window the GET uses.
	pr := r
	if body.Period != "" {
		q := r.URL.Query()
		q.Set("period", body.Period)
		clone := *r
		u := *r.URL
		u.RawQuery = q.Encode()
		clone.URL = &u
		pr = &clone
	}
	p := equityPeriod(pr)

	ledger, err := h.svc.SetEquityActual(r.Context(), orgID, body.UserID, p, body.ActualPct, body.PoolLabel, body.Note)
	if err != nil {
		slog.Error("equity put", "err", err)
		writeError(w, http.StatusInternalServerError, "could not save equity allocation")
		return
	}
	if ledger.Rows == nil {
		ledger.Rows = []contribution.EquityRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"period":   ledger.Period,
		"advisory": true,
		"rows":     ledger.Rows,
	})
}

// ── Kudos ───────────────────────────────────────────────────────────────────

// GET /api/kudos?user=
func (h *contributionHandlers) listKudos(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "X-Org-ID header required")
		return
	}
	toUser := r.URL.Query().Get("user")

	kudos, err := h.svc.ListKudos(r.Context(), orgID, toUser)
	if err != nil {
		slog.Error("kudos list", "err", err)
		writeError(w, http.StatusInternalServerError, "could not load kudos")
		return
	}
	counts, err := h.svc.KudosCounts(r.Context(), orgID)
	if err != nil {
		slog.Error("kudos counts", "err", err)
		writeError(w, http.StatusInternalServerError, "could not load kudos counts")
		return
	}
	if kudos == nil {
		kudos = []store.Kudo{}
	}
	if counts == nil {
		counts = map[string]int{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"kudos":  kudos,
		"counts": counts,
	})
}

type postKudosJSON struct {
	ToUser    string `json:"toUser"`
	Dimension string `json:"dimension"`
	Message   string `json:"message"`
}

// POST /api/kudos — giver = caller; can't kudos yourself.
func (h *contributionHandlers) postKudos(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "X-Org-ID header required")
		return
	}
	user := middleware.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var body postKudosJSON
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	body.ToUser = strings.TrimSpace(body.ToUser)
	body.Message = strings.TrimSpace(body.Message)
	if body.ToUser == "" {
		writeError(w, http.StatusBadRequest, "toUser required")
		return
	}
	if body.ToUser == user.ID {
		writeError(w, http.StatusBadRequest, "you can't give kudos to yourself")
		return
	}
	// The recipient must be a member of THIS org — don't persist kudos to a
	// stranger's id.
	if _, err := store.GetMemberRole(r.Context(), h.db.Pool(), orgID, body.ToUser); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "toUser is not a member of this org")
			return
		}
		slog.Error("kudos post member lookup", "err", err)
		writeError(w, http.StatusInternalServerError, "could not validate member")
		return
	}
	if body.Message == "" {
		writeError(w, http.StatusBadRequest, "message required")
		return
	}
	if len(body.Message) > 500 {
		body.Message = body.Message[:500]
	}
	// dimension, when supplied, must be one of the canonical axes.
	if body.Dimension != "" && !validDimension(body.Dimension) {
		writeError(w, http.StatusBadRequest, "invalid dimension")
		return
	}

	k, err := h.svc.GiveKudos(r.Context(), orgID, user.ID, body.ToUser, body.Dimension, body.Message)
	if err != nil {
		slog.Error("kudos post", "err", err)
		writeError(w, http.StatusInternalServerError, "could not record kudos")
		return
	}
	writeJSON(w, http.StatusCreated, k)
}

// validDimension reports whether key is one of the six contribution axes.
func validDimension(key string) bool {
	switch key {
	case contribution.DimShipped, contribution.DimReview, contribution.DimEffort,
		contribution.DimQuality, contribution.DimOwnership, contribution.DimDurability:
		return true
	}
	return false
}
