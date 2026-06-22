package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/middleware"
	"github.com/exo/gitstate/internal/store"
)

// RegisterProfileRoutes wires the authenticated self-profile endpoints. Used by
// Settings so a user can set a real contact email when their OAuth login only
// yielded a noreply placeholder (e.g. a GitHub account with a hidden email).
// Called by the orchestrator from router.go — this package does NOT edit router.go.
func RegisterProfileRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	requireAuth := middleware.RequireAuth(cfg.Auth.JWTSigningKey)
	h := &profileHandlers{db: database}

	mux.Handle("GET /api/profile", requireAuth(http.HandlerFunc(h.get)))
	mux.Handle("PATCH /api/profile", requireAuth(http.HandlerFunc(h.update)))
}

type profileHandlers struct{ db *db.DB }

type profileResponse struct {
	ID                string `json:"id"`
	Email             string `json:"email"`
	Name              string `json:"name"`
	AvatarURL         string `json:"avatarUrl"`
	EmailIsPlaceholder bool  `json:"emailIsPlaceholder"`
}

// emailIsPlaceholder reports whether an email is a provider "noreply" address —
// the signal Settings uses to prompt for a real contact email.
func emailIsPlaceholder(email string) bool {
	e := strings.ToLower(email)
	return strings.Contains(e, "@users.noreply.github.com") ||
		strings.Contains(e, "@users.noreply.gitlab.com")
}

func toProfileResponse(u *store.User) profileResponse {
	return profileResponse{
		ID:                 u.ID,
		Email:              u.Email,
		Name:               u.Name,
		AvatarURL:          u.AvatarURL,
		EmailIsPlaceholder: emailIsPlaceholder(u.Email),
	}
}

func (h *profileHandlers) get(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFromContext(r.Context())
	if actor == nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	u, err := store.GetUserByID(r.Context(), h.db.Pool(), actor.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load profile")
		return
	}
	writeJSON(w, http.StatusOK, toProfileResponse(u))
}

type updateProfileRequest struct {
	Name  *string `json:"name"`
	Email *string `json:"email"`
}

func (h *profileHandlers) update(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFromContext(r.Context())
	if actor == nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req updateProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	name, email := "", ""
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
	}
	if req.Email != nil {
		email = strings.TrimSpace(*req.Email)
		if email != "" && !looksLikeEmail(email) {
			writeError(w, http.StatusBadRequest, "that does not look like a valid email")
			return
		}
		// Don't let a user "update" to a fresh noreply placeholder.
		if emailIsPlaceholder(email) {
			writeError(w, http.StatusBadRequest, "please use a real email address")
			return
		}
	}
	if name == "" && email == "" {
		writeError(w, http.StatusBadRequest, "nothing to update")
		return
	}

	u, err := store.UpdateUserProfile(r.Context(), h.db.Pool(), actor.ID, name, email)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrEmailTaken):
			writeError(w, http.StatusConflict, "that email is already in use by another account")
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusNotFound, "user not found")
		default:
			writeError(w, http.StatusInternalServerError, "could not update profile")
		}
		return
	}
	writeJSON(w, http.StatusOK, toProfileResponse(u))
}

// looksLikeEmail is a deliberately loose sanity check (real validation is the
// citext column + downstream verification, not a regex).
func looksLikeEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	return at > 0 && at < len(s)-1 && strings.IndexByte(s[at+1:], '.') >= 0 && !strings.ContainsAny(s, " \t\n")
}
