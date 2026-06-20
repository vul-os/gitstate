package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/exo/gitstate/internal/auth"
	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/middleware"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RegisterAuthRoutes wires the /auth/* endpoints onto mux.
// Called by the orchestrator from router.go — this package does NOT edit router.go.
func RegisterAuthRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	h := &authHandlers{db: database, cfg: cfg}
	// Stricter per-IP limit on credential endpoints to blunt brute-forcing —
	// the global 300/min budget is far too generous for login/signup/refresh.
	authLimit := middleware.AuthRateLimit()
	mux.Handle("POST /auth/signup", authLimit(http.HandlerFunc(h.signup)))
	mux.Handle("POST /auth/login", authLimit(http.HandlerFunc(h.login)))
	mux.Handle("POST /auth/refresh", authLimit(http.HandlerFunc(h.refresh)))
	mux.HandleFunc("POST /auth/logout", h.logout)
}

// authHandlers holds the shared dependencies for auth HTTP handlers.
type authHandlers struct {
	db  *db.DB
	cfg *config.Config
}

// ── Request / response types ────────────────────────────────────────────────

type signupRequest struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}

type logoutRequest struct {
	RefreshToken string `json:"refreshToken"`
}

type userResponse struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

type authResponse struct {
	AccessToken  string       `json:"accessToken"`
	RefreshToken string       `json:"refreshToken"`
	User         userResponse `json:"user"`
}

type tokenResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
}

// ── Helpers ──────────────────────────────────────────────────────────────────

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// issueTokenPair creates a fresh access + refresh token pair for a user and
// inserts the refresh token into the database (new family).
func (h *authHandlers) issueTokenPair(r *http.Request, u *store.User) (string, string, error) {
	cfg := h.cfg
	accessToken, err := auth.IssueAccessToken(cfg.Auth.JWTSigningKey, u.ID, u.Email, u.Name, cfg.Auth.AccessTokenTTL)
	if err != nil {
		return "", "", fmt.Errorf("issue access token: %w", err)
	}

	raw, hash, err := auth.GenerateRefreshToken()
	if err != nil {
		return "", "", fmt.Errorf("generate refresh token: %w", err)
	}

	familyID, err := newUUID(r.Context(), h.db.Pool())
	if err != nil {
		return "", "", fmt.Errorf("generate family id: %w", err)
	}

	expiresAt := time.Now().UTC().Add(cfg.Auth.RefreshTokenTTL)
	if _, err = store.InsertRefresh(r.Context(), h.db.Pool(), u.ID, familyID, hash, expiresAt); err != nil {
		return "", "", fmt.Errorf("store refresh token: %w", err)
	}

	return accessToken, raw, nil
}

// newUUID generates a UUID via the database (reuses the pgcrypto extension).
func newUUID(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	var id string
	if err := pool.QueryRow(ctx, "SELECT gen_random_uuid()::text").Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}

// ── Handlers ─────────────────────────────────────────────────────────────────

// POST /auth/signup
func (h *authHandlers) signup(w http.ResponseWriter, r *http.Request) {
	var req signupRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	req.Name = strings.TrimSpace(req.Name)

	if !emailRe.MatchString(req.Email) {
		writeError(w, http.StatusBadRequest, "invalid email address")
		return
	}
	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	u, err := store.CreateUser(r.Context(), h.db.Pool(), req.Email, req.Name, hash)
	if err != nil {
		if isDuplicateEmail(err) {
			writeError(w, http.StatusConflict, "email already registered")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create user")
		return
	}

	// Create a personal organization for the new user.
	if orgErr := h.createPersonalOrg(r, u); orgErr != nil {
		// Non-fatal: user exists; log but don't fail signup.
		_ = orgErr
	}

	accessToken, refreshToken, err := h.issueTokenPair(r, u)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not issue tokens")
		return
	}

	writeJSON(w, http.StatusCreated, authResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		User:         userResponse{ID: u.ID, Email: u.Email, Name: u.Name},
	})
}

// POST /auth/login
func (h *authHandlers) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	u, err := store.GetUserByEmail(r.Context(), h.db.Pool(), req.Email)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if u.PasswordHash == "" {
		writeError(w, http.StatusUnauthorized, "account uses OAuth; password login not available")
		return
	}

	if err = auth.VerifyPassword(req.Password, u.PasswordHash); errors.Is(err, auth.ErrPasswordMismatch) {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	accessToken, refreshToken, err := h.issueTokenPair(r, u)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not issue tokens")
		return
	}

	writeJSON(w, http.StatusOK, authResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		User:         userResponse{ID: u.ID, Email: u.Email, Name: u.Name},
	})
}

// POST /auth/refresh
func (h *authHandlers) refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := decodeJSON(r, &req); err != nil || req.RefreshToken == "" {
		writeError(w, http.StatusBadRequest, "refreshToken is required")
		return
	}

	tokenHash := auth.HashToken(req.RefreshToken)
	rt, err := store.GetRefreshByHash(r.Context(), h.db.Pool(), tokenHash)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusUnauthorized, "invalid refresh token")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Reuse detection: if this token was already replaced or revoked, revoke the
	// entire family (decisions A5).
	if rt.RevokedAt != nil || rt.ReplacedBy != "" {
		_ = store.RevokeFamily(r.Context(), h.db.Pool(), rt.FamilyID)
		writeError(w, http.StatusUnauthorized, "refresh token already used; please log in again")
		return
	}

	// Check expiry.
	if time.Now().UTC().After(rt.ExpiresAt) {
		writeError(w, http.StatusUnauthorized, "refresh token expired")
		return
	}

	u, err := store.GetUserByID(r.Context(), h.db.Pool(), rt.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Rotate: new token in same family.
	newRaw, newHash, err := auth.GenerateRefreshToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	expiresAt := time.Now().UTC().Add(h.cfg.Auth.RefreshTokenTTL)
	if _, err = store.RotateRefresh(r.Context(), h.db.Pool(), rt.ID, u.ID, rt.FamilyID, newHash, expiresAt); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	accessToken, err := auth.IssueAccessToken(h.cfg.Auth.JWTSigningKey, u.ID, u.Email, u.Name, h.cfg.Auth.AccessTokenTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, tokenResponse{
		AccessToken:  accessToken,
		RefreshToken: newRaw,
	})
}

// POST /auth/logout
func (h *authHandlers) logout(w http.ResponseWriter, r *http.Request) {
	var req logoutRequest
	if err := decodeJSON(r, &req); err != nil || req.RefreshToken == "" {
		// Treat missing token as success (idempotent).
		w.WriteHeader(http.StatusNoContent)
		return
	}

	tokenHash := auth.HashToken(req.RefreshToken)
	rt, err := store.GetRefreshByHash(r.Context(), h.db.Pool(), tokenHash)
	if err == nil {
		// Revoke the entire family to invalidate all devices sharing this chain.
		_ = store.RevokeFamily(r.Context(), h.db.Pool(), rt.FamilyID)
	}

	w.WriteHeader(http.StatusNoContent)
}

// createPersonalOrg inserts a personal organization and makes the user its owner.
// Uses raw pool queries (no RLS needed for inserts that supply org_id explicitly).
func (h *authHandlers) createPersonalOrg(r *http.Request, u *store.User) error {
	ctx := r.Context()
	pool := h.db.Pool()

	// Derive a slug from the email local part, sanitized and de-duped via suffix.
	baseSlug := slugify(strings.SplitN(u.Email, "@", 2)[0])
	orgName := u.Name + "'s workspace"

	// Try inserting with the base slug; append user-id suffix on conflict.
	const insertOrg = `
		INSERT INTO organizations (slug, name)
		VALUES ($1, $2)
		ON CONFLICT (slug) DO UPDATE SET slug = EXCLUDED.slug || '-' || LEFT($3::text, 8)
		RETURNING id`

	var orgID string
	if err := pool.QueryRow(ctx, insertOrg, baseSlug, orgName, u.ID).Scan(&orgID); err != nil {
		return fmt.Errorf("create personal org: %w", err)
	}

	const insertMember = `
		INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'owner')
		ON CONFLICT DO NOTHING`
	if _, err := pool.Exec(ctx, insertMember, orgID, u.ID); err != nil {
		return fmt.Errorf("add org owner: %w", err)
	}

	return nil
}

// slugify converts a string to a lowercase alphanumeric-and-dash slug.
var nonAlphanumRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(s)
	s = nonAlphanumRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "workspace"
	}
	return s
}

// isDuplicateEmail returns true when the error is a Postgres unique-constraint
// violation on the users.email column.
func isDuplicateEmail(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "users_email_key") || strings.Contains(msg, "23505")
}
