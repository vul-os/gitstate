// Package web embeds the compiled React/Vite frontend and exposes an http.Handler
// that serves it as a single-page application (SPA).
//
// Embed layout:
//
//	internal/web/embed/          ← .gitignore reserves this; only .gitkeep is committed
//	  index.html                 ← populated by `make web` or the Docker build
//	  assets/                    ← hashed JS/CSS bundles
//
// The Go build always succeeds because .gitkeep is present (all:embed includes it).
// If the embed dir contains no index.html the handler returns a graceful placeholder
// telling the operator to run `npm run build` or use the Vite dev server.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:embed
var embedFS embed.FS

// Handler returns an http.Handler that serves the embedded SPA.
//
// Routing rules:
//   - Paths that begin with /api/, /auth/, /admin/, or /healthz are NOT handled here;
//     the caller must register those routes before installing this handler as a catch-all.
//   - Requests for files that exist verbatim in the embed (e.g. /assets/main-abc.js,
//     /favicon.ico) are served directly with correct Content-Type.
//   - All other requests receive index.html so the React router can handle client-side
//     navigation (deep links, refreshes on /projects/42, etc.).
//   - If no index.html is present in the embed (dev mode / no build copied in) every
//     request receives a small HTML placeholder page.
//
// Wire as the catch-all AFTER all API/auth/admin routes:
//
//	mux.Handle("/", web.Handler())
func Handler() http.Handler {
	// Strip the "embed/" prefix so the file server sees paths relative to the embed root.
	sub, err := fs.Sub(embedFS, "embed")
	if err != nil {
		// Should never happen — "embed" dir is always present (at minimum .gitkeep).
		panic("web: failed to sub embed FS: " + err.Error())
	}

	// Check whether a real build is present.
	if _, err := sub.Open("index.html"); err != nil {
		// No build present — return the dev-mode placeholder handler.
		return devPlaceholderHandler()
	}

	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Do not serve the .gitkeep file.
		if r.URL.Path == "/.gitkeep" {
			http.NotFound(w, r)
			return
		}

		// Let the file server try to serve the exact path first.
		// If the file exists, serve it and return.
		if _, err := sub.Open(strings.TrimPrefix(r.URL.Path, "/")); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}

		// Fall back to index.html for unknown paths (SPA client-side routing).
		// Set the path to "/" so http.FileServer serves index.html from the root.
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, r2)
	})
}

// devPlaceholderHandler returns a simple HTML page when no frontend build is present.
// This lets the Go server start cleanly in development before a `npm run build` has run.
func devPlaceholderHandler() http.Handler {
	const page = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>gitstate — no frontend build</title>
  <style>
    body { font-family: system-ui, sans-serif; display: flex; align-items: center;
           justify-content: center; min-height: 100vh; margin: 0; background: #0f172a; color: #e2e8f0; }
    .card { max-width: 480px; padding: 2rem; background: #1e293b; border-radius: 12px;
            box-shadow: 0 4px 24px rgba(0,0,0,.4); }
    h1 { margin: 0 0 .5rem; font-size: 1.4rem; color: #38bdf8; }
    p  { margin: .75rem 0; line-height: 1.6; }
    code { background: #0f172a; border-radius: 4px; padding: .15em .4em; font-size: .9em; }
    a { color: #818cf8; }
  </style>
</head>
<body>
  <div class="card">
    <h1>gitstate</h1>
    <p>The Go server is running, but <strong>no frontend build</strong> was found.</p>
    <p>To serve the React UI, either:</p>
    <p>
      • Run <code>make web</code> (or <code>npm run build</code> inside <code>web/</code>)
        and restart the server.<br />
      • Or point your browser to the Vite dev server at
        <a href="http://localhost:5173">localhost:5173</a> while this process handles the API.
    </p>
    <p>API and admin routes are fully operational at <code>/api/*</code>, <code>/admin/*</code>,
       and <code>/healthz</code>.</p>
  </div>
</body>
</html>`

	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(page))
	})
}
