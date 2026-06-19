// Package docs embeds the product documentation as plain markdown files and
// exposes them for rendering in the frontend. Docs are just .md files in
// internal/docs/content — edit them like any markdown; they render in-app via
// the /api/docs endpoints and mirror the project README for simplicity.
package docs

import (
	"embed"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed content/*.md
var contentFS embed.FS

// Doc is a single documentation page.
type Doc struct {
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	Order    int    `json:"order"`
	Category string `json:"category"`          // for grouping on the docs home
	Summary  string `json:"summary,omitempty"` // one-line description for cards
	Content  string `json:"content,omitempty"`
}

// metaLine parses an optional leading metadata comment:
//
//	<!-- title: Getting Started | order: 2 -->
//
// Falling back to the first markdown H1 for the title and 99 for order.
func parseMeta(slug, body string) Doc {
	d := Doc{Slug: slug, Order: 99, Category: "General"}

	// Strip a leading metadata comment (so it never renders) and parse its fields.
	trimmed := strings.TrimLeft(body, " \t\r\n")
	if strings.HasPrefix(trimmed, "<!--") {
		if end := strings.Index(trimmed, "-->"); end >= 0 {
			inner := strings.TrimSpace(trimmed[len("<!--"):end])
			for _, part := range strings.Split(inner, "|") {
				k, v, ok := strings.Cut(strings.TrimSpace(part), ":")
				if !ok {
					continue
				}
				k, v = strings.TrimSpace(k), strings.TrimSpace(v)
				switch k {
				case "title":
					d.Title = v
				case "order":
					if n, err := strconv.Atoi(v); err == nil {
						d.Order = n
					}
				case "category":
					d.Category = v
				case "summary":
					d.Summary = v
				}
			}
			// Remove the comment from the content that gets rendered.
			body = strings.TrimLeft(trimmed[end+len("-->"):], "\r\n")
		}
	}
	d.Content = body

	// Fall back to the first H1 for the title.
	if d.Title == "" {
		for _, ln := range strings.SplitN(body, "\n", 6) {
			if s := strings.TrimSpace(ln); strings.HasPrefix(s, "# ") {
				d.Title = strings.TrimSpace(strings.TrimPrefix(s, "# "))
				break
			}
		}
	}
	if d.Title == "" {
		d.Title = slug
	}
	return d
}

func load() ([]Doc, error) {
	entries, err := fs.ReadDir(contentFS, "content")
	if err != nil {
		return nil, err
	}
	var out []Doc
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, err := contentFS.ReadFile("content/" + e.Name())
		if err != nil {
			return nil, err
		}
		slug := strings.TrimSuffix(e.Name(), ".md")
		out = append(out, parseMeta(slug, string(b)))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].Slug < out[j].Slug
	})
	return out, nil
}

// List returns all docs WITHOUT content (for the nav/index).
func List() ([]Doc, error) {
	all, err := load()
	if err != nil {
		return nil, err
	}
	for i := range all {
		all[i].Content = ""
	}
	return all, nil
}

// Get returns one doc by slug, content included.
func Get(slug string) (Doc, bool, error) {
	all, err := load()
	if err != nil {
		return Doc{}, false, err
	}
	for _, d := range all {
		if d.Slug == slug {
			return d, true, nil
		}
	}
	return Doc{}, false, nil
}
