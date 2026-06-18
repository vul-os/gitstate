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
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Order   int    `json:"order"`
	Content string `json:"content,omitempty"`
}

// metaLine parses an optional leading metadata comment:
//
//	<!-- title: Getting Started | order: 2 -->
//
// Falling back to the first markdown H1 for the title and 99 for order.
func parseMeta(slug, body string) Doc {
	d := Doc{Slug: slug, Order: 99, Content: body}
	lines := strings.SplitN(body, "\n", 4)
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "<!--") && strings.Contains(ln, "-->") {
			inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(ln, "<!--"), "-->"))
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
				}
			}
		}
		if d.Title == "" && strings.HasPrefix(ln, "# ") {
			d.Title = strings.TrimSpace(strings.TrimPrefix(ln, "# "))
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
