// Package admin — login.go
// Branded cookie-session login for the browser-reachable super-admin console.
//
//	GET  /admin/login   — render the login form
//	POST /admin/login   — validate credentials, issue token, set gs_admin cookie
//	GET  /admin/logout  — clear the cookie and return to the login form
//
// These three routes are intentionally NOT behind RequireAdminAuth.
package admin

import (
	"errors"
	"net/http"

	"github.com/exo/gitstate/internal/auth"
)

// errAdminLogin is the single error surfaced for every login failure so the
// form cannot be used to enumerate accounts.
var errAdminLogin = errors.New("invalid credentials or not a super-admin")

// loginData is the view-model for the login template.
type loginData struct {
	Error string
}

// loginPage renders the branded login form.
func (h *adminHandlers) loginPage(w http.ResponseWriter, r *http.Request) {
	// Already authenticated? Send straight to the console.
	if tok := adminTokenFromRequest(r); tok != "" {
		if _, err := auth.ParseAccessToken(h.cfg.Auth.JWTSigningKey, tok); err == nil {
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
			return
		}
	}
	h.renderLogin(w, loginData{})
}

// loginSubmit validates the posted credentials, issues an access token, sets the
// gs_admin cookie, and redirects to the console.
func (h *adminHandlers) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderLoginStatus(w, http.StatusBadRequest, loginData{Error: "malformed form submission"})
		return
	}

	email := r.PostForm.Get("email")
	password := r.PostForm.Get("password")
	if email == "" || password == "" {
		h.renderLoginStatus(w, http.StatusBadRequest, loginData{Error: "email and password are required"})
		return
	}

	user, err := authenticateAdmin(r, h.cfg, h.db, email, password)
	if err != nil {
		// Never log the password; never reveal which check failed.
		h.renderLoginStatus(w, http.StatusUnauthorized, loginData{Error: errAdminLogin.Error()})
		return
	}

	token, err := auth.IssueAccessToken(
		h.cfg.Auth.JWTSigningKey,
		user.ID, user.Email, user.Name,
		h.cfg.Auth.AccessTokenTTL,
	)
	if err != nil {
		h.renderLoginStatus(w, http.StatusInternalServerError, loginData{Error: "could not start session, please retry"})
		return
	}

	setAdminCookie(w, token, h.cfg.Auth.AccessTokenTTL, httpsDeployment(h.cfg))
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// logout clears the session cookie and returns to the login form.
func (h *adminHandlers) logout(w http.ResponseWriter, r *http.Request) {
	clearAdminCookie(w, httpsDeployment(h.cfg))
	http.Redirect(w, r, loginPath, http.StatusSeeOther)
}

func (h *adminHandlers) renderLogin(w http.ResponseWriter, data loginData) {
	h.renderLoginStatus(w, http.StatusOK, data)
}

func (h *adminHandlers) renderLoginStatus(w http.ResponseWriter, status int, data loginData) {
	t, err := getTemplates("login")
	if err != nil {
		renderErr(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := t.ExecuteTemplate(w, "login.html", data); err != nil {
		renderErr(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
}
