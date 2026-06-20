// Package api — capacity/PTO/availability/time-tracking routes.
// RegisterCapacityRoutes is wired by the orchestrator (router.go).
// This file does NOT edit router.go (route-wiring rule from PROGRESS.md).
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/exo/gitstate/internal/capacity"
	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/middleware"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// RegisterCapacityRoutes wires capacity, leave, availability, and time-entry
// endpoints onto mux. All routes are guarded by RequireAuth + OrgScope.
//
//	GET  /api/leave                — list leave entries (org or user-scoped)
//	POST /api/leave                — submit a leave request (type, half-day, portion)
//	PATCH /api/leave/{id}          — approve or reject a leave request
//	GET  /api/leave-types          — list configurable leave types
//	POST /api/leave-types          — create a leave type (owner/admin)
//	PATCH /api/leave-types/{id}    — update / archive a leave type (owner/admin)
//	GET  /api/leave-balances       — per-user balances (entitled/carried/used/remaining)
//	GET  /api/availability         — get current member availability
//	PUT  /api/availability         — set member availability
//	GET  /api/time-entries         — list time entries
//	POST /api/time-entries         — log a time entry (manual)
//	GET  /api/capacity?period=     — effective capacity per member (avail − leave)
func RegisterCapacityRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	h := &capacityHandlers{db: database, cfg: cfg}
	requireAuth := middleware.RequireAuth(cfg.Auth.JWTSigningKey)
	orgScope := middleware.OrgScope(database.Pool())
	auth := func(next http.Handler) http.Handler { return requireAuth(orgScope(next)) }

	mux.Handle("GET /api/leave", auth(http.HandlerFunc(h.listLeave)))
	mux.Handle("POST /api/leave", auth(http.HandlerFunc(h.createLeave)))
	mux.Handle("PATCH /api/leave/{id}", auth(http.HandlerFunc(h.approveLeave)))
	mux.Handle("GET /api/leave-types", auth(http.HandlerFunc(h.listLeaveTypes)))
	mux.Handle("POST /api/leave-types", auth(http.HandlerFunc(h.createLeaveType)))
	mux.Handle("PATCH /api/leave-types/{id}", auth(http.HandlerFunc(h.updateLeaveType)))
	mux.Handle("GET /api/leave-balances", auth(http.HandlerFunc(h.listLeaveBalances)))
	mux.Handle("GET /api/availability", auth(http.HandlerFunc(h.getAvailability)))
	mux.Handle("PUT /api/availability", auth(http.HandlerFunc(h.putAvailability)))
	mux.Handle("GET /api/time-entries", auth(http.HandlerFunc(h.listTimeEntries)))
	mux.Handle("POST /api/time-entries", auth(http.HandlerFunc(h.createTimeEntry)))
	mux.Handle("GET /api/capacity", auth(http.HandlerFunc(h.getCapacity)))
}

type capacityHandlers struct {
	db  *db.DB
	cfg *config.Config
}

// errSelfApprove is returned when an owner/admin tries to approve or reject their
// OWN leave request (separation-of-duties guard).
var errSelfApprove = errors.New("cannot approve or reject your own leave request")

// ── Leave ─────────────────────────────────────────────────────────────────

type leaveResponse struct {
	ID          string `json:"id"`
	UserID      string `json:"userId"`
	Kind        string `json:"kind"`
	LeaveTypeID string `json:"leaveTypeId,omitempty"`
	StartDate   string `json:"startDate"` // YYYY-MM-DD
	EndDate     string `json:"endDate"`
	HalfDay     bool   `json:"halfDay"`
	Portion     string `json:"portion"` // full | am | pm
	Status      string `json:"status"`
	Note        string `json:"note,omitempty"`
}

func leaveToResponse(e *store.LeaveEntry) leaveResponse {
	return leaveResponse{
		ID:          e.ID,
		UserID:      e.UserID,
		Kind:        e.Kind,
		LeaveTypeID: e.LeaveTypeID,
		StartDate:   e.StartDate.Format("2006-01-02"),
		EndDate:     e.EndDate.Format("2006-01-02"),
		HalfDay:     e.HalfDay,
		Portion:     e.Portion,
		Status:      e.Status,
		Note:        e.Note,
	}
}

func (h *capacityHandlers) listLeave(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "X-Org-ID header required")
		return
	}
	userID := r.URL.Query().Get("user_id") // optional filter

	var out []leaveResponse
	err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		entries, err := store.ListLeaveEntries(r.Context(), tx, orgID, userID)
		if err != nil {
			return err
		}
		for _, e := range entries {
			out = append(out, leaveToResponse(e))
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list leave entries")
		return
	}
	if out == nil {
		out = []leaveResponse{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *capacityHandlers) createLeave(w http.ResponseWriter, r *http.Request) {
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

	var body struct {
		UserID      string `json:"userId"`      // defaults to authenticated user
		Kind        string `json:"kind"`        // pto | sick | holiday (legacy classifier)
		LeaveTypeID string `json:"leaveTypeId"` // configurable leave type (optional)
		StartDate   string `json:"startDate"`   // YYYY-MM-DD
		EndDate     string `json:"endDate"`
		HalfDay     bool   `json:"halfDay"` // single half-day off
		Portion     string `json:"portion"` // full | am | pm
		Note        string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Default to the requesting user.
	if body.UserID == "" {
		body.UserID = user.ID
	}
	if body.Kind == "" {
		body.Kind = "pto"
	}
	if body.Portion == "" {
		body.Portion = "full"
	}
	if body.Portion != "full" && body.Portion != "am" && body.Portion != "pm" {
		writeError(w, http.StatusBadRequest, "portion must be 'full', 'am', or 'pm'")
		return
	}
	if body.StartDate == "" || body.EndDate == "" {
		writeError(w, http.StatusBadRequest, "startDate and endDate are required")
		return
	}
	start, err := time.Parse("2006-01-02", body.StartDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "startDate must be YYYY-MM-DD")
		return
	}
	end, err := time.Parse("2006-01-02", body.EndDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "endDate must be YYYY-MM-DD")
		return
	}
	if end.Before(start) {
		writeError(w, http.StatusBadRequest, "endDate must be >= startDate")
		return
	}
	// A half-day request is, by definition, a single day.
	if body.HalfDay && !start.Equal(end) {
		writeError(w, http.StatusBadRequest, "halfDay leave must have equal start and end dates")
		return
	}
	if body.Portion != "full" {
		body.HalfDay = true
	}

	var resp leaveResponse
	err = h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		e, err := store.CreateLeaveEntry(r.Context(), tx, orgID, body.UserID, body.Kind,
			body.LeaveTypeID, body.Note, start, end, body.HalfDay, body.Portion)
		if err != nil {
			return err
		}
		resp = leaveToResponse(e)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create leave entry")
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *capacityHandlers) approveLeave(w http.ResponseWriter, r *http.Request) {
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
	// Only owners/admins may approve or reject leave (and never their own request).
	role, err := store.GetMemberRole(r.Context(), h.db.Pool(), orgID, user.ID)
	if err != nil || !canManageMembers(role) {
		writeError(w, http.StatusForbidden, "only owners and admins can approve or reject leave")
		return
	}

	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "leave id is required")
		return
	}

	var body struct {
		Status string `json:"status"` // approved | rejected
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Status != "approved" && body.Status != "rejected" {
		writeError(w, http.StatusBadRequest, "status must be 'approved' or 'rejected'")
		return
	}

	var resp leaveResponse
	err = h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		// Reject self-approval: an owner/admin cannot decide on their OWN request.
		existing, gerr := store.GetLeaveEntry(r.Context(), tx, orgID, id)
		if gerr != nil {
			return gerr
		}
		if existing.UserID == user.ID {
			return errSelfApprove
		}

		e, err := store.ApproveLeaveEntry(r.Context(), tx, orgID, id, body.Status)
		if err == store.ErrNotFound {
			return store.ErrNotFound
		}
		if err != nil {
			return err
		}
		// A status change (approve OR reject) shifts how many days count as
		// "used". If the entry carries a configurable leave type, recompute the
		// affected balance so remaining-days stays accurate.
		if e.LeaveTypeID != "" {
			year := e.StartDate.Year()
			if _, rerr := store.RecomputeUsedDays(r.Context(), tx, orgID, e.UserID, e.LeaveTypeID, year); rerr != nil {
				return rerr
			}
		}
		resp = leaveToResponse(e)
		return nil
	})
	if err == store.ErrNotFound {
		writeError(w, http.StatusNotFound, "leave entry not found")
		return
	}
	if errors.Is(err, errSelfApprove) {
		writeError(w, http.StatusForbidden, errSelfApprove.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not update leave entry")
		return
	}

	// Best-effort two-way sync: mirror an approved leave onto the member's connected
	// Google/Microsoft calendars (OOO event). A calendar failure must NOT fail the
	// approval — it's logged and the response still succeeds.
	if body.Status == "approved" {
		if perr := PushApprovedLeave(r.Context(), h.db, h.cfg, orgID, id); perr != nil {
			slog.Warn("calendar: push approved leave failed (non-fatal)",
				"org_id", orgID, "leave_id", id, "err", perr)
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// ── Leave types ─────────────────────────────────────────────────────────────

type leaveTypeResponse struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	Color            string  `json:"color"`
	DefaultDays      float64 `json:"defaultDays"`
	RequiresApproval bool    `json:"requiresApproval"`
	Accrues          bool    `json:"accrues"`
	CarryoverMax     float64 `json:"carryoverMax"`
	Paid             bool    `json:"paid"`
	Archived         bool    `json:"archived"`
}

func leaveTypeToResponse(t *store.LeaveType) leaveTypeResponse {
	return leaveTypeResponse{
		ID:               t.ID,
		Name:             t.Name,
		Color:            t.Color,
		DefaultDays:      t.DefaultDays,
		RequiresApproval: t.RequiresApproval,
		Accrues:          t.Accrues,
		CarryoverMax:     t.CarryoverMax,
		Paid:             t.Paid,
		Archived:         t.Archived,
	}
}

func (h *capacityHandlers) listLeaveTypes(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "X-Org-ID header required")
		return
	}
	includeArchived := r.URL.Query().Get("archived") == "true"

	var out []leaveTypeResponse
	err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		types, err := store.ListLeaveTypes(r.Context(), tx, orgID, includeArchived)
		if err != nil {
			return err
		}
		for _, t := range types {
			out = append(out, leaveTypeToResponse(t))
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list leave types")
		return
	}
	if out == nil {
		out = []leaveTypeResponse{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *capacityHandlers) createLeaveType(w http.ResponseWriter, r *http.Request) {
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
	// Only owners/admins may configure leave types.
	role, err := store.GetMemberRole(r.Context(), h.db.Pool(), orgID, user.ID)
	if err != nil || !canManageMembers(role) {
		writeError(w, http.StatusForbidden, "only owners and admins can manage leave types")
		return
	}

	var body struct {
		Name             string  `json:"name"`
		Color            string  `json:"color"`
		DefaultDays      float64 `json:"defaultDays"`
		RequiresApproval *bool   `json:"requiresApproval"`
		Accrues          bool    `json:"accrues"`
		CarryoverMax     float64 `json:"carryoverMax"`
		Paid             *bool   `json:"paid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	requiresApproval := true
	if body.RequiresApproval != nil {
		requiresApproval = *body.RequiresApproval
	}
	paid := true
	if body.Paid != nil {
		paid = *body.Paid
	}

	var resp leaveTypeResponse
	err = h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		t, err := store.CreateLeaveType(r.Context(), tx, &store.LeaveType{
			OrgID:            orgID,
			Name:             body.Name,
			Color:            body.Color,
			DefaultDays:      body.DefaultDays,
			RequiresApproval: requiresApproval,
			Accrues:          body.Accrues,
			CarryoverMax:     body.CarryoverMax,
			Paid:             paid,
		})
		if err != nil {
			return err
		}
		resp = leaveTypeToResponse(t)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create leave type")
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *capacityHandlers) updateLeaveType(w http.ResponseWriter, r *http.Request) {
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
		writeError(w, http.StatusForbidden, "only owners and admins can manage leave types")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "leave type id is required")
		return
	}

	var body struct {
		Name             *string  `json:"name"`
		Color            *string  `json:"color"`
		DefaultDays      *float64 `json:"defaultDays"`
		RequiresApproval *bool    `json:"requiresApproval"`
		Accrues          *bool    `json:"accrues"`
		CarryoverMax     *float64 `json:"carryoverMax"`
		Paid             *bool    `json:"paid"`
		Archived         *bool    `json:"archived"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var resp leaveTypeResponse
	err = h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		t, err := store.UpdateLeaveType(r.Context(), tx, orgID, id, store.LeaveTypePatch{
			Name:             body.Name,
			Color:            body.Color,
			DefaultDays:      body.DefaultDays,
			RequiresApproval: body.RequiresApproval,
			Accrues:          body.Accrues,
			CarryoverMax:     body.CarryoverMax,
			Paid:             body.Paid,
			Archived:         body.Archived,
		})
		if err != nil {
			return err
		}
		resp = leaveTypeToResponse(t)
		return nil
	})
	if err == store.ErrNotFound {
		writeError(w, http.StatusNotFound, "leave type not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not update leave type")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ── Leave balances ──────────────────────────────────────────────────────────

type leaveBalanceResponse struct {
	UserID       string  `json:"userId"`
	LeaveTypeID  string  `json:"leaveTypeId"`
	Year         int     `json:"year"`
	EntitledDays float64 `json:"entitledDays"`
	CarriedDays  float64 `json:"carriedDays"`
	UsedDays     float64 `json:"usedDays"`
	Remaining    float64 `json:"remainingDays"`
}

func leaveBalanceToResponse(b *store.LeaveBalance) leaveBalanceResponse {
	return leaveBalanceResponse{
		UserID:       b.UserID,
		LeaveTypeID:  b.LeaveTypeID,
		Year:         b.Year,
		EntitledDays: b.EntitledDays,
		CarriedDays:  b.CarriedDays,
		UsedDays:     b.UsedDays,
		Remaining:    b.Remaining(),
	}
}

// listLeaveBalances handles GET /api/leave-balances?user=&year=
// When user is omitted it returns balances for the whole org; year defaults to
// the current year.
func (h *capacityHandlers) listLeaveBalances(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "X-Org-ID header required")
		return
	}
	userID := r.URL.Query().Get("user")
	year := time.Now().UTC().Year()
	if y := r.URL.Query().Get("year"); y != "" {
		if parsed, err := time.Parse("2006", y); err == nil {
			year = parsed.Year()
		}
	}

	var out []leaveBalanceResponse
	err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		balances, err := store.ListLeaveBalances(r.Context(), tx, orgID, userID, year)
		if err != nil {
			return err
		}
		for _, b := range balances {
			out = append(out, leaveBalanceToResponse(b))
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list leave balances")
		return
	}
	if out == nil {
		out = []leaveBalanceResponse{}
	}
	writeJSON(w, http.StatusOK, out)
}

// ── Availability ──────────────────────────────────────────────────────────

type availabilityResponse struct {
	ID            string  `json:"id"`
	UserID        string  `json:"userId"`
	WeeklyHours   float64 `json:"weeklyHours"`
	WorkingDays   []int32 `json:"workingDays"`
	EffectiveFrom string  `json:"effectiveFrom"` // YYYY-MM-DD
}

func availToResponse(a *store.Availability) availabilityResponse {
	return availabilityResponse{
		ID:            a.ID,
		UserID:        a.UserID,
		WeeklyHours:   a.WeeklyHours,
		WorkingDays:   a.WorkingDays,
		EffectiveFrom: a.EffectiveFrom.Format("2006-01-02"),
	}
}

func (h *capacityHandlers) getAvailability(w http.ResponseWriter, r *http.Request) {
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
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		userID = user.ID
	}

	var out []availabilityResponse
	err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		rows, err := store.ListAvailability(r.Context(), tx, orgID, userID)
		if err != nil {
			return err
		}
		for _, a := range rows {
			out = append(out, availToResponse(a))
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not get availability")
		return
	}
	if out == nil {
		out = []availabilityResponse{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *capacityHandlers) putAvailability(w http.ResponseWriter, r *http.Request) {
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

	var body struct {
		UserID        string  `json:"userId"`        // defaults to authenticated user
		WeeklyHours   float64 `json:"weeklyHours"`   // e.g. 40
		WorkingDays   []int32 `json:"workingDays"`   // ISO weekdays e.g. [1,2,3,4,5]
		EffectiveFrom string  `json:"effectiveFrom"` // YYYY-MM-DD; defaults to today
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.UserID == "" {
		body.UserID = user.ID
	}
	if body.WeeklyHours <= 0 {
		body.WeeklyHours = 40
	}
	if len(body.WorkingDays) == 0 {
		body.WorkingDays = []int32{1, 2, 3, 4, 5}
	}
	effectiveFrom := time.Now().UTC().Truncate(24 * time.Hour)
	if body.EffectiveFrom != "" {
		t, err := time.Parse("2006-01-02", body.EffectiveFrom)
		if err != nil {
			writeError(w, http.StatusBadRequest, "effectiveFrom must be YYYY-MM-DD")
			return
		}
		effectiveFrom = t
	}

	var resp availabilityResponse
	err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		a, err := store.UpsertAvailability(r.Context(), tx, orgID, body.UserID, body.WeeklyHours, body.WorkingDays, effectiveFrom)
		if err != nil {
			return err
		}
		resp = availToResponse(a)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not set availability")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ── Time entries ──────────────────────────────────────────────────────────

type timeEntryResponse struct {
	ID         string `json:"id"`
	UserID     string `json:"userId"`
	IssueID    string `json:"issueId,omitempty"`
	Source     string `json:"source"`
	Minutes    int    `json:"minutes"`
	OccurredOn string `json:"occurredOn"` // YYYY-MM-DD
	Note       string `json:"note,omitempty"`
}

func timeEntryToResponse(e *store.TimeEntry) timeEntryResponse {
	return timeEntryResponse{
		ID:         e.ID,
		UserID:     e.UserID,
		IssueID:    e.IssueID,
		Source:     e.Source,
		Minutes:    e.Minutes,
		OccurredOn: e.OccurredOn.Format("2006-01-02"),
		Note:       e.Note,
	}
}

func (h *capacityHandlers) listTimeEntries(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "X-Org-ID header required")
		return
	}
	userID := r.URL.Query().Get("user_id")

	var out []timeEntryResponse
	err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		entries, err := store.ListTimeEntries(r.Context(), tx, orgID, userID)
		if err != nil {
			return err
		}
		for _, e := range entries {
			out = append(out, timeEntryToResponse(e))
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list time entries")
		return
	}
	if out == nil {
		out = []timeEntryResponse{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *capacityHandlers) createTimeEntry(w http.ResponseWriter, r *http.Request) {
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

	var body struct {
		UserID     string `json:"userId"`     // defaults to authenticated user
		IssueID    string `json:"issueId"`    // optional
		Source     string `json:"source"`     // "manual" (default) | "git"
		Minutes    int    `json:"minutes"`    // required, > 0
		OccurredOn string `json:"occurredOn"` // YYYY-MM-DD; defaults to today
		Note       string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.UserID == "" {
		body.UserID = user.ID
	}
	if body.Source == "" {
		body.Source = "manual"
	}
	if body.Minutes <= 0 {
		writeError(w, http.StatusBadRequest, "minutes must be > 0")
		return
	}
	occurredOn := time.Now().UTC().Truncate(24 * time.Hour)
	if body.OccurredOn != "" {
		t, err := time.Parse("2006-01-02", body.OccurredOn)
		if err != nil {
			writeError(w, http.StatusBadRequest, "occurredOn must be YYYY-MM-DD")
			return
		}
		occurredOn = t
	}

	var resp timeEntryResponse
	err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		e, err := store.CreateTimeEntry(r.Context(), tx, orgID, body.UserID, body.IssueID, body.Source, body.Note, body.Minutes, occurredOn)
		if err != nil {
			return err
		}
		resp = timeEntryToResponse(e)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create time entry")
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

// ── Capacity summary ──────────────────────────────────────────────────────

type memberCapacityResponse struct {
	UserID             string  `json:"userId"`
	AvailableHours     float64 `json:"availableHours"`
	ApprovedLeaveHours float64 `json:"approvedLeaveHours"`
	EffectiveHours     float64 `json:"effectiveHours"`
	LoggedMinutes      int     `json:"loggedMinutes"`
}

// getCapacity handles GET /api/capacity?period=YYYY-MM-DD/YYYY-MM-DD
// and returns effective capacity per member for the org in the period.
// Period format: "YYYY-MM-DD/YYYY-MM-DD" (ISO 8601 interval, start inclusive, end exclusive).
// Members can be filtered with ?user_id= (repeatable).
func (h *capacityHandlers) getCapacity(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "X-Org-ID header required")
		return
	}

	periodStr := r.URL.Query().Get("period")
	if periodStr == "" {
		// Default: current month.
		now := time.Now().UTC()
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 1, 0)
		periodStr = start.Format("2006-01-02") + "/" + end.Format("2006-01-02")
	}

	period, err := parsePeriod(periodStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "period must be YYYY-MM-DD/YYYY-MM-DD")
		return
	}

	memberIDs := r.URL.Query()["user_id"]
	if len(memberIDs) == 0 {
		// Fetch all org member IDs.
		err = h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
			rows, qerr := tx.Query(r.Context(),
				`SELECT user_id FROM org_members WHERE org_id = $1`, orgID)
			if qerr != nil {
				return qerr
			}
			defer rows.Close()
			for rows.Next() {
				var uid string
				if scanErr := rows.Scan(&uid); scanErr != nil {
					return scanErr
				}
				memberIDs = append(memberIDs, uid)
			}
			return rows.Err()
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not list org members")
			return
		}
	}

	caps, err := capacity.EffectiveCapacity(r.Context(), h.db, orgID, period, memberIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not compute capacity")
		return
	}

	out := make([]memberCapacityResponse, 0, len(caps))
	for _, c := range caps {
		out = append(out, memberCapacityResponse{
			UserID:             c.UserID,
			AvailableHours:     c.AvailableHours,
			ApprovedLeaveHours: c.ApprovedLeaveHours,
			EffectiveHours:     c.EffectiveHours,
			LoggedMinutes:      c.LoggedMinutes,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// parsePeriod parses a "YYYY-MM-DD/YYYY-MM-DD" string into a capacity.Period.
func parsePeriod(s string) (capacity.Period, error) {
	const sep = "/"
	idx := len(s) / 2
	// find slash
	for i, c := range s {
		if c == '/' {
			idx = i
			break
		}
	}
	startStr := s[:idx]
	endStr := ""
	if idx+1 < len(s) {
		endStr = s[idx+1:]
	}
	start, err := time.Parse("2006-01-02", startStr)
	if err != nil {
		return capacity.Period{}, err
	}
	end, err := time.Parse("2006-01-02", endStr)
	if err != nil {
		return capacity.Period{}, err
	}
	_ = sep
	return capacity.Period{Start: start, End: end}, nil
}
