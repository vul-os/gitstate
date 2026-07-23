// Package store — search.go
// Wave 4 of the AI/agent flywheel: full-text + fuzzy search over issues, PRs and
// commits so an agent can find work by MEANING, not exact keywords.
//
// Two layers:
//  1. Primary FTS — websearch_to_tsquery against the generated search_tsv
//     columns (GIN-indexed), ranked by ts_rank, with a ts_headline snippet.
//  2. Fuzzy fallback — when FTS matches nothing (typo / partial word, e.g.
//     "athentication"), fall back to pg_trgm similarity on issue titles plus a
//     lenient ILIKE scan on PR titles / commit messages, ranked by similarity.
//
// Everything runs on the caller's pgx.Tx (wrapped in db.WithOrg) so FORCE-RLS
// scopes every read to the org. Only the entity type list is templated into SQL;
// the user's query string is ALWAYS passed as a bound parameter.
package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/exo/gitstate/internal/embed"
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
	Type    string  `json:"type"`    // issue | pr | commit
	ID      string  `json:"id"`      // internal UUID
	Number  int     `json:"number"`  // issue/PR number (0 for commits)
	Title   string  `json:"title"`   // title / commit subject
	Snippet string  `json:"snippet"` // highlighted excerpt of the match
	Rank    float64 `json:"rank"`    // ts_rank (FTS) or similarity (fuzzy)
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

// rrfK is the Reciprocal Rank Fusion constant. The standard value (60) damps the
// contribution of low-ranked items so a result must place well in at least one
// ranker to surface; a hit appearing in BOTH the FTS and vector rankings is
// rewarded by the sum of its reciprocal ranks.
const rrfK = 60.0

// Search runs HYBRID search across the requested entity types.
//
// Issues are searched two ways and fused with Reciprocal Rank Fusion (RRF):
//  1. Full-text (websearch_to_tsquery / ts_rank) — exact lexical matches.
//  2. Vector KNN — the query is embedded with the same local embedder used at
//     index time and the HNSW cosine index returns the semantically nearest
//     issues, so "login is broken" can surface an "authentication redirect" issue
//     that shares no keyword.
//
// The two rankings are de-duplicated and re-ranked by RRF (Σ 1/(rrfK+rank)). PRs
// and commits remain FTS-only and are appended after the fused issue list. When the
// FTS pass matches nothing at all (and vectors add nothing), the typo-tolerant
// fuzzy fallback fires exactly as before.
//
// Returns (results, fuzzy, semantic). `fuzzy` is true when the fuzzy fallback
// produced the hits; `semantic` is true when the vector ranker actually
// contributed (i.e. some issues were embedded and matched). Before any issue is
// embedded, semantic is false and behaviour is identical to the pre-vector path.
//
// tx MUST come from db.WithOrg so RLS scopes every read to orgID. The query string
// is only ever bound as a parameter; never interpolated.
func Search(ctx context.Context, tx pgx.Tx, orgID, query string, types []string, limit int) ([]SearchResult, bool, bool, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, false, false, nil
	}
	wanted := normalizeSearchTypes(types)
	limit = clampLimit(limit)

	ftsResults, err := searchFTS(ctx, tx, wanted, query, limit)
	if err != nil {
		return nil, false, false, err
	}

	// Vector KNN over issues (only when issues are in scope). Returns nothing
	// before any issue is embedded, in which case the fusion is a no-op and we
	// behave exactly like FTS-only.
	var vecHits []VectorHit
	if wantsIssues(wanted) {
		qVec := embed.Embed(query)
		vecHits, err = SearchIssuesByVector(ctx, tx, embed.ToPGVector(qVec), limit)
		if err != nil {
			return nil, false, false, err
		}
	}

	fused, semantic := fuseHybrid(ftsResults, vecHits, limit)
	if len(fused) > 0 {
		// Vector-only issues carry just an ID — hydrate their row data.
		if semantic {
			if err := HydrateMissingIssues(ctx, tx, fused); err != nil {
				return nil, false, false, err
			}
		}
		return fused, false, semantic, nil
	}

	// Nothing matched FTS or vectors — try typo-tolerant fuzzy search.
	fuzzy, err := searchFuzzy(ctx, tx, wanted, query, limit)
	if err != nil {
		return nil, false, false, err
	}
	return fuzzy, len(fuzzy) > 0, false, nil
}

// wantsIssues reports whether the issue type is in scope.
func wantsIssues(wanted []string) bool {
	for _, t := range wanted {
		if t == SearchTypeIssue {
			return true
		}
	}
	return false
}

// fuseHybrid merges the FTS results with the vector KNN issue hits using
// Reciprocal Rank Fusion and returns the top `limit` rows by fused score. It also
// reports whether the vector ranker actually contributed any issue that the FTS
// pass did not already surface (or improved an issue's standing), i.e. whether the
// result is "semantic".
//
// FTS already carries full result rows (issue/pr/commit). Vector hits are issue IDs
// only; an issue that appears ONLY in the vector ranking is hydrated lazily — but
// because the hot path wants a single round trip, we instead keep FTS rows as the
// source of truth for row data and let vectors re-rank / boost them. Issues found
// only by the vector ranker are fetched in a follow-up batch by the caller-free
// helper below.
func fuseHybrid(fts []SearchResult, vecHits []VectorHit, limit int) ([]SearchResult, bool) {
	// RRF score per result key. Key = type+"\x00"+id so PRs/commits never collide
	// with issues.
	type fusedRow struct {
		res   SearchResult
		score float64
	}
	order := []string{} // preserves first-seen order for stable output
	rows := map[string]*fusedRow{}

	keyOf := func(typ, id string) string { return typ + "\x00" + id }

	// FTS contributes by its rank position (0-based → rank 1-based).
	for i, r := range fts {
		k := keyOf(r.Type, r.ID)
		if _, ok := rows[k]; !ok {
			rows[k] = &fusedRow{res: r}
			order = append(order, k)
		}
		rows[k].score += 1.0 / (rrfK + float64(i+1))
	}

	// Vector hits contribute to issue rows. A vector-only issue (not in the FTS
	// set) is recorded with a placeholder row carrying its similarity as Rank; the
	// caller hydrates these via HydrateMissingIssues. semantic is set when a vector
	// hit either introduces a new issue or boosts an FTS issue's score.
	semantic := false
	for i, vh := range vecHits {
		k := keyOf(SearchTypeIssue, vh.IssueID)
		if _, ok := rows[k]; !ok {
			rows[k] = &fusedRow{res: SearchResult{
				Type: SearchTypeIssue,
				ID:   vh.IssueID,
				Rank: vh.Similarity, // cosine similarity until hydrated
			}}
			order = append(order, k)
			semantic = true
		} else {
			// The vector ranker reinforced an existing FTS issue.
			semantic = true
		}
		rows[k].score += 1.0 / (rrfK + float64(i+1))
	}

	// Stable sort by fused score (desc), tie-broken by first-seen order.
	out := make([]*fusedRow, 0, len(rows))
	for _, k := range order {
		out = append(out, rows[k])
	}
	// Insertion sort keeps it stable and is fine for the small (≤limit) set.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].score > out[j-1].score; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}

	results := make([]SearchResult, 0, len(out))
	for _, fr := range out {
		results = append(results, fr.res)
		if len(results) >= limit {
			break
		}
	}
	return results, semantic
}

// HydrateMissingIssues fills in the title/number/state/snippet for any issue-typed
// result whose row was introduced by the vector ranker (FTS never saw it, so only
// the ID is set). It runs one batched SELECT for all such IDs. Results whose Title
// is already populated (came from FTS) are left untouched. tx MUST come from
// db.WithOrg.
func HydrateMissingIssues(ctx context.Context, tx pgx.Tx, results []SearchResult) error {
	var ids []string
	idx := map[string]int{}
	for i, r := range results {
		if r.Type == SearchTypeIssue && r.Title == "" {
			ids = append(ids, r.ID)
			idx[r.ID] = i
		}
	}
	if len(ids) == 0 {
		return nil
	}

	const q = `
		SELECT id::text, COALESCE(number,0), COALESCE(title,''),
		       left(COALESCE(title,'') || ' ' || COALESCE(body,''), 160) AS snippet,
		       COALESCE(repo_id::text,''), COALESCE(state,'')
		FROM issues
		WHERE id = ANY($1)`
	rows, err := tx.Query(ctx, q, ids)
	if err != nil {
		return fmt.Errorf("store: hydrate vector issues: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, title, snippet, repoID, state string
		var number int
		if err := rows.Scan(&id, &number, &title, &snippet, &repoID, &state); err != nil {
			return fmt.Errorf("store: scan hydrate row: %w", err)
		}
		if i, ok := idx[id]; ok {
			r := &results[i]
			r.Number = number
			r.Title = strings.TrimSpace(title)
			r.Snippet = strings.TrimSpace(snippet)
			r.RepoID = repoID
			r.State = state
		}
	}
	return rows.Err()
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
