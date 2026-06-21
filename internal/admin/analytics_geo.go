// Package admin — analytics_geo.go
// Server-rendered geo-IP + realtime analytics page for the super-admin console.
// Reads the GLOBAL analytics_events table (migration 022) through the audited
// BYPASSRLS admin pool (h.adminPool via h.aggPool), exactly like the other
// cross-org aggregate views. PRIVACY: this page only ever surfaces coarse geo
// and salted-hash-derived counts — never a raw IP (the table stores none).
//
// These handlers are methods on *adminHandlers but live in their OWN file and
// register their OWN template set (the shared getTemplates in routes.go is not
// editable here). The orchestrator wires the routes in router.go behind
// RequireSuperAdmin, mirroring the other /admin routes.
package admin

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/exo/gitstate/internal/store"
)

// ── Geo-analytics template set (own singleton) ────────────────────────────────

var (
	geoTmplOnce sync.Once
	geoTmpl     *template.Template
	geoTmplErr  error
)

// geoFuncMap extends the shared funcMap with the few helpers this page needs.
// It is built lazily (the shared funcMap is package-level and already defined).
func geoFuncMap() template.FuncMap {
	fm := template.FuncMap{}
	for k, v := range funcMap {
		fm[k] = v
	}

	// flag renders the regional-indicator emoji for an ISO-3166 alpha-2 code,
	// or a neutral globe when unknown.
	fm["flag"] = func(cc string) string {
		cc = strings.ToUpper(strings.TrimSpace(cc))
		if len(cc) != 2 || cc[0] < 'A' || cc[0] > 'Z' || cc[1] < 'A' || cc[1] > 'Z' {
			return "\U0001F310" // 🌐
		}
		r := []rune{0x1F1E6 + rune(cc[0]-'A'), 0x1F1E6 + rune(cc[1]-'A')}
		return string(r)
	}

	// geoLabel composes "City, Region" (falling back to country / "Unknown").
	fm["geoLabel"] = func(country, region, city string) string {
		parts := make([]string, 0, 2)
		if city != "" {
			parts = append(parts, city)
		}
		if region != "" && region != city {
			parts = append(parts, region)
		}
		if len(parts) == 0 {
			if country != "" {
				return country
			}
			return "Unknown"
		}
		return strings.Join(parts, ", ")
	}

	// clock formats an event timestamp compactly for the live feed.
	fm["clock"] = func(t time.Time) string { return t.Local().Format("15:04:05") }

	// kindBadge renders a coloured badge for an event kind.
	fm["kindBadge"] = func(kind string) template.HTML {
		class := "badge-muted"
		switch kind {
		case "signup":
			class = "badge-teal"
		case "login":
			class = "badge-indigo"
		case "login_failed":
			class = "badge-danger"
		case "pageview":
			class = "badge-muted"
		case "logout":
			class = "badge-warn"
		}
		return template.HTML(fmt.Sprintf(`<span class="badge %s">%s</span>`,
			class, template.HTMLEscapeString(kind)))
	}

	// eventSparkline draws a polyline (no fill except the first series) for a
	// daily event series in the shared 300×60 viewBox.
	fm["eventSparkline"] = func(days []store.EventDay, stroke string) template.HTML {
		if len(days) == 0 {
			return ""
		}
		maxC := 1
		for _, d := range days {
			if d.Count > maxC {
				maxC = d.Count
			}
		}
		const w, h = 300.0, 60.0
		n := len(days)
		pts := make([]string, n)
		for i, d := range days {
			denom := n - 1
			if denom < 1 {
				denom = 1
			}
			x := w * float64(i) / float64(denom)
			y := h - (h * float64(d.Count) / float64(maxC))
			pts[i] = fmt.Sprintf("%.1f,%.1f", x, y)
		}
		joined := strings.Join(pts, " ")
		return template.HTML(fmt.Sprintf(
			`<polyline points="%s" fill="none" stroke="%s" stroke-width="2" stroke-linejoin="round" stroke-linecap="round"/>`,
			joined, stroke,
		))
	}

	return fm
}

func geoTemplates() (*template.Template, error) {
	geoTmplOnce.Do(func() {
		geoTmpl, geoTmplErr = template.New("").Funcs(geoFuncMap()).
			ParseFS(templateFS, "templates/layout.html", "templates/geo-analytics.html")
	})
	return geoTmpl, geoTmplErr
}

// ── Page data ─────────────────────────────────────────────────────────────────

const geoAnalyticsDays = 30

type geoAnalyticsData struct {
	baseData
	OnlineNow  int
	Countries  []store.CountryCount
	MaxCountry int
	Kinds      []store.KindCount
	MaxKind    int
	Signups    []store.EventDay
	Logins     []store.EventDay
	Recent     []store.RecentEvent
}

// geoAnalytics renders GET /admin/analytics — the full server-rendered geo +
// realtime page. Cross-org/global read → audited service pool (S2).
func (h *adminHandlers) geoAnalytics(w http.ResponseWriter, r *http.Request) {
	t, err := geoTemplates()
	if err != nil {
		renderErr(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	data := geoAnalyticsData{baseData: h.base(r, "geo")}

	if agg := h.aggPool(); agg != nil {
		ctx := r.Context()
		data.OnlineNow, _ = store.AnalyticsOnlineNow(ctx, agg)
		data.Countries, _ = store.AnalyticsEventsByCountry(ctx, agg, geoAnalyticsDays, 10)
		for _, c := range data.Countries {
			if c.Count > data.MaxCountry {
				data.MaxCountry = c.Count
			}
		}
		data.Kinds, _ = store.AnalyticsCountsByKind(ctx, agg, geoAnalyticsDays)
		for _, k := range data.Kinds {
			if k.Count > data.MaxKind {
				data.MaxKind = k.Count
			}
		}
		data.Signups, _ = store.AnalyticsKindByDay(ctx, agg, "signup", geoAnalyticsDays)
		data.Logins, _ = store.AnalyticsKindByDay(ctx, agg, "login", geoAnalyticsDays)
		data.Recent, _ = store.AnalyticsRecentEvents(ctx, agg, 25)

		// S2: global aggregate access is audited on the (non-htmx) page load.
		h.auditCrossOrgView(ctx, r, "admin.analytics.geo.view")
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout.html", data); err != nil {
		fmt.Fprintf(w, "\n<!-- template error: %s -->", err)
	}
}

// geoAnalyticsFeed renders GET /admin/analytics/feed — the realtime activity
// table fragment, polled by htmx (hx-trigger="every 5s"). Returns only the
// <table> partial, not a full document.
func (h *adminHandlers) geoAnalyticsFeed(w http.ResponseWriter, r *http.Request) {
	t, err := geoTemplates()
	if err != nil {
		renderErr(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	data := geoAnalyticsData{baseData: h.base(r, "geo")}
	if agg := h.aggPool(); agg != nil {
		data.Recent, _ = store.AnalyticsRecentEvents(r.Context(), agg, 25)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "activity-feed", data); err != nil {
		fmt.Fprintf(w, "<!-- template error: %s -->", err)
	}
}

// geoAnalyticsOnline renders GET /admin/analytics/online — the small "N online
// now" pill, polled by htmx. Counts distinct salted ip_hashes in the last 5 min.
func (h *adminHandlers) geoAnalyticsOnline(w http.ResponseWriter, r *http.Request) {
	n := 0
	if agg := h.aggPool(); agg != nil {
		n, _ = store.AnalyticsOnlineNow(r.Context(), agg)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<span class="realtime-dot"></span> %d online now`, n)
}
