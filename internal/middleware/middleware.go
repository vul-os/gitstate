// Package middleware provides HTTP middleware for gitstate:
// Logger, Recoverer, CORS, and a stub AuthContext.
package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// contextKey is an unexported type for context keys in this package.
type contextKey string

const (
	// authTokenKey is the context key for the raw bearer token (if present).
	authTokenKey contextKey = "auth_token"
)

// BearerToken returns the raw bearer token attached to ctx by AuthContext,
// or empty string if absent.
func BearerToken(ctx context.Context) string {
	v, _ := ctx.Value(authTokenKey).(string)
	return v
}

// Logger logs method, path, status code, and duration for every request.
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		slog.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(status int) {
	rw.status = status
	rw.ResponseWriter.WriteHeader(status)
}

// Recoverer catches panics, logs them, and returns 500.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic recovered", "error", rec, "path", r.URL.Path)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// CORS adds Cross-Origin headers allowing requests from allowedOrigin.
// For preflight OPTIONS requests it returns 204 immediately.
func CORS(allowedOrigin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Allow the configured origin, or any localhost/127 origin in dev.
			if origin != "" && (origin == allowedOrigin ||
				strings.HasPrefix(origin, "http://localhost:") ||
				strings.HasPrefix(origin, "http://127.0.0.1:")) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			} else if allowedOrigin != "" {
				w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
			}

			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// AuthContext parses an Authorization: Bearer <token> header if present and
// attaches the raw token to the request context. It is a no-op if no header
// is present. Real JWT verification is wired in a later wave (A2 auth).
func AuthContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if header := r.Header.Get("Authorization"); header != "" {
			if after, ok := strings.CutPrefix(header, "Bearer "); ok && after != "" {
				r = r.WithContext(context.WithValue(r.Context(), authTokenKey, after))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Chain applies a sequence of middleware in order (outermost first).
func Chain(h http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	// Apply in reverse so that the first middleware is outermost.
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}
