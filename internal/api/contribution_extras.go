// Package api — contribution_extras.go
// REST handlers for the three CONTRIBUTION extensions (all on the existing
// contributionHandlers, registered by RegisterContributionRoutes):
//
//	GET  /api/contribution/trends?periods=6&interval=month → per-member composite series
//	GET  /api/kudos?user=                                   → peer recognition feed (+ counts)
//	POST /api/kudos                                          → give kudos (giver = caller)
//
// All routes are behind RequireAuth + OrgScope; every read/write runs inside
// db.WithOrg so RLS enforces the org boundary.
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
