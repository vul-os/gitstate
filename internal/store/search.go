// Package store — search.go
// Wave 4 of the AI/agent flywheel: full-text + fuzzy search over issues, PRs and
// commits so an agent can find work by MEANING, not exact keywords.
//
// Two layers:
//   1. Primary FTS — websearch_to_tsquery against the generated search_tsv
//      columns (GIN-indexed), ranked by ts_rank, with a ts_headline snippet.
//   2. Fuzzy fallback — when FTS matches nothing (typo / partial word, e.g.
//      "athentication"), fall back to pg_trgm similarity on issue titles plus a
//      lenient ILIKE scan on PR titles / commit messages, ranked by similarity.
//
// Everything runs on the caller's pgx.Tx (wrapped in db.WithOrg) so FORCE-RLS
// scopes every read to the org. Only the entity type list is templated into SQL;
// the user's query string is ALWAYS passed as a bound parameter.
package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Search result entity types. These are the only accepted values in the `types`
// filter; anything else is ignored (and an empty/unknown filter means "all").
const (
	SearchTypeIssue  = "issue"
	SearchTypePR     = "pr"
	SearchTypeCommit = "commit"
)

// searchTypeAliases maps the API-facing plural names to the canonical singular
// result type. Callers may pass either ("issues"|"issue", "prs"|"pr", …).
var searchTypeAliases = map[string]string{
	"issue":   SearchTypeIssue,
	"issues":  SearchTypeIssue,
	"pr":      SearchTypePR,
	"prs":     SearchTypePR,
	"commit":  SearchTypeCommit,
	"commits": SearchTypeCommit,
}

// Default / cap for the number of returned rows.
const (
	searchDefaultLimit = 20
	searchMaxLimit     = 100
)

// SearchResult is a single compact, LLM-friendly hit.
type SearchResult struct {
	Type    string  `json:"type"`           // issue | pr | commit
	ID      string  `json:"id"`             // internal UUID
	Number  int     `json:"number"`         // issue/PR number (0 for commits)
	Title   string  `json:"title"`          // title / commit subject
	Snippet string  `json:"snippet"`        // highlighted excerpt of the match
	Rank    float64 `json:"rank"`           // ts_rank (FTS) or similarity (fuzzy)
	RepoID  string  `json:"repoId,omitempty"`
	State   string  `json:"state,omitempty"` // issue/PR state; "" for commits
}

// normalizeSearchTypes resolves the requested type filter to a deterministic,
// de-duplicated set of canonical types. An empty or all-unknown input yields all
// three types in a stable order.
func normalizeSearchTypes(types []string) []string {
	want := map[string]bool{}
	for _, t := range types {
		if canon, ok := searchTypeAliases[strings.ToLower(strings.TrimSpace(t))]; ok {
			want[canon] = true
		}
	}
	if len(want) == 0 {
		return []string{SearchTypeIssue, SearchTypePR, SearchTypeCommit}
	}
	// Stable canonical order.
	out := make([]string, 0, 3)
	for _, t := range []string{SearchTypeIssue, SearchTypePR, SearchTypeCommit} {
		if want[t] {
			out = append(out, t)
		}
	}
	return out
}

func clampLimit(limit int) int {
	if limit <= 0 {
		return searchDefaultLimit
	}
	if limit > searchMaxLimit {
		return searchMaxLimit
	}
	return limit
}

// Search runs full-text search across the requested entity types, falling back to
// fuzzy (pg_trgm / ILIKE) matching when FTS finds nothing. The returned bool is
// true when the fuzzy fallback produced the results, so callers can tell the user
// the hit was approximate.
//
// tx MUST come from db.WithOrg so RLS scopes the query to orgID. The query string
// is only ever bound as a parameter; never interpolated.
func Search(ctx context.Context, tx pgx.Tx, orgID, query string, types []string, limit int) ([]SearchResult, bool, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, false, nil
	}
	wanted := normalizeSearchTypes(types)
	limit = clampLimit(limit)

	results, err := searchFTS(ctx, tx, wanted, query, limit)
	if err != nil {
		return nil, false, err
	}
	if len(results) > 0 {
		return results, false, nil
	}

	// Nothing matched the structured query — try typo-tolerant fuzzy search.
	results, err = searchFuzzy(ctx, tx, wanted, query, limit)
	if err != nil {
		return nil, false, err
	}
	return results, len(results) > 0, nil
}

// searchFTS UNIONs one websearch_to_tsquery branch per requested entity type and
// returns the top `limit` rows by ts_rank. $1 = query, $2 = limit.
func searchFTS(ctx context.Context, tx pgx.Tx, wanted []string, query string, limit int) ([]SearchResult, error) {
	branches := make([]string, 0, len(wanted))
	for _, t := range wanted {
		switch t {
		case SearchTypeIssue:
			branches = append(branches, `
				SELECT 'issue'::text AS type, id::text, COALESCE(number,0) AS number,
				       title,
				       ts_headline('english', coalesce(title,'') || ' ' || coalesce(body,''),
				                   websearch_to_tsquery('english', $1),
				                   'MaxFragments=1,MaxWords=18,MinWords=5,StartSel=<b>,StopSel=</b>') AS snippet,
				       ts_rank(search_tsv, websearch_to_tsquery('english', $1)) AS rank,
				       COALESCE(repo_id::text,'') AS repo_id,
				       COALESCE(state,'') AS state
				FROM issues
				WHERE search_tsv @@ websearch_to_tsquery('english', $1)`)
		case SearchTypePR:
			branches = append(branches, `
				SELECT 'pr'::text AS type, id::text, COALESCE(number,0) AS number,
				       title,
				       ts_headline('english', coalesce(title,''),
				                   websearch_to_tsquery('english', $1),
				                   'MaxFragments=1,MaxWords=18,MinWords=5,StartSel=<b>,StopSel=</b>') AS snippet,
				       ts_rank(search_tsv, websearch_to_tsquery('english', $1)) AS rank,
				       COALESCE(repo_id::text,'') AS repo_id,
				       COALESCE(state,'') AS state
				FROM pull_requests
				WHERE search_tsv @@ websearch_to_tsquery('english', $1)`)
		case SearchTypeCommit:
			branches = append(branches, `
				SELECT 'commit'::text AS type, id::text, 0 AS number,
				       split_part(coalesce(message,''), E'\n', 1) AS title,
				       ts_headline('english', coalesce(message,''),
				                   websearch_to_tsquery('english', $1),
				                   'MaxFragments=1,MaxWords=18,MinWords=5,StartSel=<b>,StopSel=</b>') AS snippet,
				       ts_rank(search_tsv, websearch_to_tsquery('english', $1)) AS rank,
				       COALESCE(repo_id::text,'') AS repo_id,
				       ''::text AS state
				FROM commits
				WHERE search_tsv @@ websearch_to_tsquery('english', $1)`)
		}
	}
	if len(branches) == 0 {
		return nil, nil
	}

	q := fmt.Sprintf(`
		SELECT type, id, number, title, snippet, rank, repo_id, state
		FROM ( %s ) AS hits
		ORDER BY rank DESC, number DESC
		LIMIT $2`, strings.Join(branches, "\nUNION ALL\n"))

	return querySearchRows(ctx, tx, q, query, limit)
}

// searchFuzzy is the typo-tolerant fallback. Issues use pg_trgm similarity on the
// (trigram-indexed) title; PRs and commits use a lenient ILIKE substring match
// (no trgm index on those, so similarity ranking is best-effort via word_similarity).
// $1 = query, $2 = limit. A small floor keeps the noise down.
func searchFuzzy(ctx context.Context, tx pgx.Tx, wanted []string, query string, limit int) ([]SearchResult, error) {
	const simFloor = 0.2 // similarity threshold for issue titles
	branches := make([]string, 0, len(wanted))
	for _, t := range wanted {
		switch t {
		case SearchTypeIssue:
			branches = append(branches, fmt.Sprintf(`
				SELECT 'issue'::text AS type, id::text, COALESCE(number,0) AS number,
				       title,
				       left(coalesce(title,''), 160) AS snippet,
				       similarity(coalesce(title,''), $1) AS rank,
				       COALESCE(repo_id::text,'') AS repo_id,
				       COALESCE(state,'') AS state
				FROM issues
				WHERE similarity(coalesce(title,''), $1) > %g`, simFloor))
		case SearchTypePR:
			branches = append(branches, `
				SELECT 'pr'::text AS type, id::text, COALESCE(number,0) AS number,
				       title,
				       left(coalesce(title,''), 160) AS snippet,
				       word_similarity($1, coalesce(title,'')) AS rank,
				       COALESCE(repo_id::text,'') AS repo_id,
				       COALESCE(state,'') AS state
				FROM pull_requests
				WHERE coalesce(title,'') ILIKE '%' || $1 || '%'
				   OR word_similarity($1, coalesce(title,'')) > 0.4`)
		case SearchTypeCommit:
			branches = append(branches, `
				SELECT 'commit'::text AS type, id::text, 0 AS number,
				       split_part(coalesce(message,''), E'\n', 1) AS title,
				       left(coalesce(message,''), 160) AS snippet,
				       word_similarity($1, coalesce(message,'')) AS rank,
				       COALESCE(repo_id::text,'') AS repo_id,
				       ''::text AS state
				FROM commits
				WHERE coalesce(message,'') ILIKE '%' || $1 || '%'
				   OR word_similarity($1, coalesce(message,'')) > 0.4`)
		}
	}
	if len(branches) == 0 {
		return nil, nil
	}

	q := fmt.Sprintf(`
		SELECT type, id, number, title, snippet, rank, repo_id, state
		FROM ( %s ) AS hits
		ORDER BY rank DESC, number DESC
		LIMIT $2`, strings.Join(branches, "\nUNION ALL\n"))

	return querySearchRows(ctx, tx, q, query, limit)
}

// querySearchRows runs a search SQL statement (with $1=query, $2=limit) and scans
// the uniform 8-column projection into SearchResults. Snippets are trimmed.
func querySearchRows(ctx context.Context, tx pgx.Tx, q, query string, limit int) ([]SearchResult, error) {
	rows, err := tx.Query(ctx, q, query, limit)
	if err != nil {
		return nil, fmt.Errorf("store: search query: %w", err)
	}
	defer rows.Close()

	var out []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(
			&r.Type, &r.ID, &r.Number, &r.Title, &r.Snippet, &r.Rank, &r.RepoID, &r.State,
		); err != nil {
			return nil, fmt.Errorf("store: scan search row: %w", err)
		}
		r.Title = strings.TrimSpace(r.Title)
		r.Snippet = strings.TrimSpace(r.Snippet)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: search rows: %w", err)
	}
	return out, nil
}
