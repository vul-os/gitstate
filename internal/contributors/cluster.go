// Package contributors clusters the many raw git identities (emails + logins)
// that actually belong to ONE person into a canonical "contributor", and
// persists them into the contributors / contributor_identities tables.
//
// The clustering is heuristic and LLM-FREE: it runs union-find (disjoint set)
// over every distinct identity collected from the org's data and unions two
// identities when any of a small set of high-precision rules fire (shared
// display name, email local-part == a login, same email, same login, or the
// same reasonably-unique local-part across domains). Each resulting cluster
// becomes one contributor.
//
// DetectAndUpsert is IDEMPOTENT and PRESERVES MANUAL EDITS: identities already
// mapped to a contributor are never re-clustered or moved, so manual merges,
// splits, links and excludes survive a re-run. Only NEW (unmapped) identities
// are clustered and attached — to an existing contributor when they union with
// one of its identities, otherwise to a fresh contributor.
package contributors

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Kind values for contributor_identities.kind.
const (
	KindEmail = "email"
	KindLogin = "login"
)

// Identity is one raw git identity collected from the org's data. Value is the
// lowercased email or login; Name is the best display name seen for it (the
// author display name — in this schema that is the git login, since commits
// carry no separate display-name column).
type Identity struct {
	Kind  string // KindEmail | KindLogin
	Value string // lowercased
	Name  string // display name seen (lowercased for matching; original kept in NameSeen)
	Count int    // how many rows referenced it (used to pick the most-frequent fields)

	NameSeen string // original-case display name to persist
}

// Cluster is one resolved person: the set of identities the union-find grouped
// together, plus the derived display fields.
type Cluster struct {
	Identities   []Identity
	DisplayName  string
	PrimaryEmail string
	IsBot        bool
}

// ── Generic / bot name guards ────────────────────────────────────────────────

// genericNames are display names too generic to imply two identities are the
// same person. We never union on these.
var genericNames = map[string]bool{
	"":               true,
	"github":         true,
	"root":           true,
	"unknown":        true,
	"admin":          true,
	"user":           true,
	"runner":         true,
	"ubuntu":         true,
	"ci":             true,
	"build":          true,
	"none":           true,
	"n/a":            true,
	"your name":      true,
	"github action":  true,
	"github-actions": true,
}

// weakLocalParts are email local-parts too generic to imply same-person across
// different domains (the cross-domain local-part rule skips these).
var weakLocalParts = map[string]bool{
	"admin":    true,
	"dev":      true,
	"info":     true,
	"git":      true,
	"hello":    true,
	"contact":  true,
	"team":     true,
	"support":  true,
	"noreply":  true,
	"no-reply": true,
	"mail":     true,
	"me":       true,
	"hi":       true,
	"root":     true,
	"user":     true,
	"test":     true,
	"build":    true,
	"ci":       true,
	"action":   true,
}

// botNamePatterns mark an identity as a bot when contained in its login/name.
var botNamePatterns = []string{
	"[bot]",
	"dependabot",
	"renovate",
	"github-actions",
	"copilot",
	"semantic-release",
	"greenkeeper",
	"snyk-bot",
	"mergify",
	"netlify",
	"vercel[bot]",
	"codecov",
}

// botEmailNames mark an identity as a bot when its email (or local part) is a
// known automation/agent address.
var botEmailLocalParts = map[string]bool{
	"noreply":             false, // not on its own — handled via domain below
	"actions":             true,
	"github-actions[bot]": true,
	"bot":                 true,
}

// IsBotIdentity reports whether a single identity looks bot/agent-authored.
func IsBotIdentity(id Identity) bool {
	v := strings.ToLower(id.Value)
	n := strings.ToLower(id.Name)
	for _, p := range botNamePatterns {
		if strings.Contains(v, p) || strings.Contains(n, p) {
			return true
		}
	}
	if id.Kind == KindEmail {
		// `…[bot]@users.noreply.github.com` and agent vendor addresses.
		if strings.HasSuffix(v, "[bot]@users.noreply.github.com") {
			return true
		}
		local, domain := splitEmail(v)
		if domain == "anthropic.com" && local == "noreply" {
			return true // claude / noreply@anthropic.com
		}
		if botEmailLocalParts[local] {
			return true
		}
	}
	return false
}

// ── union-find ───────────────────────────────────────────────────────────────

type unionFind struct {
	parent map[string]string
	rank   map[string]int
}

func newUnionFind() *unionFind {
	return &unionFind{parent: map[string]string{}, rank: map[string]int{}}
}

func (u *unionFind) add(x string) {
	if _, ok := u.parent[x]; !ok {
		u.parent[x] = x
		u.rank[x] = 0
	}
}

func (u *unionFind) find(x string) string {
	for u.parent[x] != x {
		u.parent[x] = u.parent[u.parent[x]] // path compression
		x = u.parent[x]
	}
	return x
}

func (u *unionFind) union(a, b string) {
	ra, rb := u.find(a), u.find(b)
	if ra == rb {
		return
	}
	if u.rank[ra] < u.rank[rb] {
		ra, rb = rb, ra
	}
	u.parent[rb] = ra
	if u.rank[ra] == u.rank[rb] {
		u.rank[ra]++
	}
}

// ── clustering ───────────────────────────────────────────────────────────────

// identityKey is the union-find node key for an identity (kind:value).
func identityKey(id Identity) string { return id.Kind + ":" + id.Value }

// Cluster runs the union-find over the supplied identities and returns one
// Cluster per resolved person. Pure (no DB) so the rules are unit-testable.
// ClusterIdentities groups identities into people. `pairs` are (email, login)
// values that CO-OCCURRED on the same commit author — the strongest same-person
// signal (the commit literally has both), unioned before any heuristic rule.
func ClusterIdentities(ids []Identity, pairs ...[2]string) []Cluster {
	// De-duplicate by (kind,value), summing counts and keeping the most-frequent
	// non-empty name as NameSeen.
	merged := map[string]*Identity{}
	for _, raw := range ids {
		raw.Value = strings.ToLower(strings.TrimSpace(raw.Value))
		if raw.Value == "" {
			continue
		}
		k := identityKey(raw)
		m := merged[k]
		if m == nil {
			cp := raw
			merged[k] = &cp
			continue
		}
		m.Count += raw.Count
		if m.NameSeen == "" && raw.NameSeen != "" {
			m.NameSeen = raw.NameSeen
		}
		if m.Name == "" && raw.Name != "" {
			m.Name = raw.Name
		}
	}

	uf := newUnionFind()
	list := make([]*Identity, 0, len(merged))
	for _, m := range merged {
		uf.add(identityKey(*m))
		list = append(list, m)
	}
	// Deterministic order so unions (and tie-breaks below) are stable.
	sort.Slice(list, func(i, j int) bool {
		if list[i].Kind != list[j].Kind {
			return list[i].Kind < list[j].Kind
		}
		return list[i].Value < list[j].Value
	})

	// Build lookup indexes for the rule passes.
	logins := map[string]*Identity{}    // login value -> identity
	byLocal := map[string][]*Identity{} // email local-part -> email identities
	byName := map[string][]*Identity{}  // lowercased non-generic display name -> identities
	for _, id := range list {
		switch id.Kind {
		case KindLogin:
			logins[id.Value] = id
		case KindEmail:
			local, _ := splitEmail(id.Value)
			if local != "" {
				byLocal[local] = append(byLocal[local], id)
			}
		}
		// Union on identical (non-generic) display names. Bot names ARE allowed
		// here: two identities both named "dependabot[bot]" are the same bot, and
		// we union on the FULL name string so distinct bots never collide.
		nm := strings.ToLower(strings.TrimSpace(id.Name))
		if nm != "" && !genericNames[nm] {
			byName[nm] = append(byName[nm], id)
		}
	}

	// Rule 0 (STRONGEST): an email + a login that appear together on the same
	// commit are the same person — the commit literally carries both. This beats
	// every heuristic below (a person's email and username are linked, not separate).
	for _, p := range pairs {
		ek := identityKey(Identity{Kind: KindEmail, Value: strings.ToLower(strings.TrimSpace(p[0]))})
		lk := identityKey(Identity{Kind: KindLogin, Value: strings.ToLower(strings.TrimSpace(p[1]))})
		if _, ok := merged[ek]; !ok {
			continue
		}
		if _, ok := merged[lk]; !ok {
			continue
		}
		uf.union(ek, lk)
	}

	// Rule 1: identities sharing the same (non-generic, non-bot) display name.
	for _, group := range byName {
		for i := 1; i < len(group); i++ {
			uf.union(identityKey(*group[0]), identityKey(*group[i]))
		}
	}

	// Rule 2: email local-part == an existing login.
	for local, emails := range byLocal {
		if lg, ok := logins[local]; ok {
			for _, e := range emails {
				uf.union(identityKey(*e), identityKey(*lg))
			}
		}
	}

	// Rule 3: same reasonably-unique email local-part across different domains.
	for local, emails := range byLocal {
		if len(emails) < 2 {
			continue
		}
		if len(local) < 4 || weakLocalParts[local] || isNumeric(local) {
			continue
		}
		for i := 1; i < len(emails); i++ {
			uf.union(identityKey(*emails[0]), identityKey(*emails[i]))
		}
	}

	// (Rules "same email" / "same login" are implicit: de-dup already collapses
	// identical (kind,value) into one node, and the login/email cross rules above
	// bridge the two kinds.)

	// Collect clusters by representative root.
	groups := map[string][]*Identity{}
	for _, id := range list {
		root := uf.find(identityKey(*id))
		groups[root] = append(groups[root], id)
	}

	out := make([]Cluster, 0, len(groups))
	for _, members := range groups {
		out = append(out, buildCluster(members))
	}
	// Deterministic output order: by display name then primary email.
	sort.Slice(out, func(i, j int) bool {
		if out[i].DisplayName != out[j].DisplayName {
			return out[i].DisplayName < out[j].DisplayName
		}
		return out[i].PrimaryEmail < out[j].PrimaryEmail
	})
	return out
}

// buildCluster derives the display fields for one resolved person.
func buildCluster(members []*Identity) Cluster {
	c := Cluster{}
	// most-frequent non-empty display name (NameSeen); fall back to a login.
	nameVotes := map[string]int{}
	nameOrig := map[string]string{}
	bestName, bestNameCount := "", -1
	var bestLogin string
	var bestLoginCount = -1
	for _, m := range members {
		c.Identities = append(c.Identities, *m)
		if IsBotIdentity(*m) {
			c.IsBot = true
		}
		ns := strings.TrimSpace(m.NameSeen)
		if ns == "" {
			ns = strings.TrimSpace(m.Name)
		}
		key := strings.ToLower(ns)
		if key != "" && !genericNames[key] {
			nameVotes[key] += m.Count + 1
			if _, ok := nameOrig[key]; !ok {
				nameOrig[key] = ns
			}
			if v := nameVotes[key]; v > bestNameCount {
				bestNameCount, bestName = v, nameOrig[key]
			}
		}
		if m.Kind == KindLogin {
			if m.Count > bestLoginCount {
				bestLoginCount, bestLogin = m.Count, m.Value
			}
		}
	}

	// most-frequent REAL (non-noreply) email as primary.
	bestEmail, bestEmailCount := "", -1
	for _, m := range members {
		if m.Kind != KindEmail {
			continue
		}
		if isNoreplyEmail(m.Value) {
			continue
		}
		if m.Count > bestEmailCount {
			bestEmailCount, bestEmail = m.Count, m.Value
		}
	}
	// fall back to any email (even noreply) if no real one exists.
	if bestEmail == "" {
		for _, m := range members {
			if m.Kind == KindEmail && m.Count > bestEmailCount {
				bestEmailCount, bestEmail = m.Count, m.Value
			}
		}
	}

	c.DisplayName = bestName
	if c.DisplayName == "" {
		c.DisplayName = bestLogin
	}
	if c.DisplayName == "" {
		c.DisplayName = bestEmail
	}
	c.PrimaryEmail = bestEmail
	return c
}

// ── small helpers ─────────────────────────────────────────────────────────────

func splitEmail(email string) (local, domain string) {
	at := strings.LastIndexByte(email, '@')
	if at < 0 {
		return email, ""
	}
	return email[:at], email[at+1:]
}

// isNoreplyEmail reports whether an email is a GitHub/GitLab no-reply address
// (or otherwise an unusable contact address).
func isNoreplyEmail(email string) bool {
	_, domain := splitEmail(email)
	if strings.Contains(domain, "noreply") || strings.Contains(domain, "no-reply") {
		return true
	}
	if strings.HasSuffix(domain, ".local") || strings.HasSuffix(domain, ".localdomain") {
		return true
	}
	if domain == "example.com" {
		return true
	}
	return false
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ── persistence ────────────────────────────────────────────────────────────────

// DetectResult reports what a DetectAndUpsert run did.
type DetectResult struct {
	Contributors int // contributors that now exist (touched this run)
	Identities   int // identities newly attached this run
	Merged       int // new identities attached to a PRE-EXISTING contributor
}

// DetectAndUpsert collects every distinct git identity in the org, clusters the
// UNMAPPED ones, and persists the result. It is IDEMPOTENT and PRESERVES MANUAL
// EDITS:
//
//   - Identities already in contributor_identities are left untouched (their
//     contributor, manual merges/links/excludes all survive).
//   - A new identity that union-clusters with an already-mapped identity is
//     attached to that EXISTING contributor (so re-running after import grows the
//     right person rather than spawning a duplicate).
//   - Otherwise new clusters create new contributors.
//
// Must run inside db.WithOrg(ctx, orgID, …).
func DetectAndUpsert(ctx context.Context, tx pgx.Tx, orgID string) (DetectResult, error) {
	var res DetectResult

	// 1) Collect every distinct identity from the org's data.
	pairs, err := collectCoOccurrencePairs(ctx, tx, orgID)
	if err != nil {
		return res, err
	}
	all, err := collectIdentities(ctx, tx, orgID)
	if err != nil {
		return res, err
	}
	if len(all) == 0 {
		return res, nil
	}

	// 2) Load the existing mapping (identity value+kind -> contributor_id) so we
	//    can skip already-mapped identities and attach new ones to the right person.
	existing, err := loadExistingMap(ctx, tx, orgID)
	if err != nil {
		return res, err
	}

	// 3) Cluster ALL identities (existing + new) so new identities can union onto
	//    existing contributors, but only PERSIST changes for new identities.
	clusters := ClusterIdentities(all, pairs...)

	for _, cl := range clusters {
		// Does this cluster already overlap an existing contributor? If several
		// existing contributors overlap (e.g. a manual split), we DO NOT merge
		// them — we respect the manual layout and only place brand-new identities.
		existingContribForNew := pickExistingContributor(cl, existing)

		// Partition: which identities are new (need persisting)?
		var newIdents []Identity
		for _, id := range cl.Identities {
			if _, mapped := existing[identityKey(id)]; !mapped {
				newIdents = append(newIdents, id)
			}
		}
		if len(newIdents) == 0 {
			continue // wholly manual/known cluster — leave it alone.
		}

		contribID := existingContribForNew
		if contribID == "" {
			// Create a fresh contributor for this cluster.
			contribID, err = insertContributor(ctx, tx, orgID, cl)
			if err != nil {
				return res, err
			}
			res.Contributors++
		} else {
			res.Merged++ // attaching new identities onto a pre-existing contributor
		}

		for _, id := range newIdents {
			if err := insertIdentity(ctx, tx, orgID, contribID, id); err != nil {
				return res, err
			}
			res.Identities++
		}
	}

	return res, nil
}

// pickExistingContributor returns the contributor_id an existing identity in the
// cluster already maps to (the most common one when several appear), or "" when
// the cluster is wholly new.
func pickExistingContributor(cl Cluster, existing map[string]string) string {
	votes := map[string]int{}
	for _, id := range cl.Identities {
		if cid, ok := existing[identityKey(id)]; ok {
			votes[cid]++
		}
	}
	best, bestN := "", 0
	// Deterministic: iterate sorted.
	keys := make([]string, 0, len(votes))
	for k := range votes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if votes[k] > bestN {
			bestN, best = votes[k], k
		}
	}
	return best
}

// collectIdentities gathers every distinct git identity (email + login) from
// commits, pull_requests, pr_reviews and author_survival, with the best display
// name (the git login, since commits carry no separate name column) and a usage
// count used to weight the most-frequent display/primary-email picks.
// collectCoOccurrencePairs returns (email, login) values that appeared TOGETHER on
// the same commit author — the strongest same-person signal for ClusterIdentities.
func collectCoOccurrencePairs(ctx context.Context, tx pgx.Tx, orgID string) ([][2]string, error) {
	const q = `
		SELECT lower(author_email::text), lower(author_login)
		FROM commits
		WHERE org_id = $1 AND author_email IS NOT NULL AND author_email::text <> ''
		  AND author_login IS NOT NULL AND author_login <> ''
		GROUP BY 1,2`
	rows, err := tx.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("contributors: collect co-occurrence: %w", err)
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var email, login string
		if err := rows.Scan(&email, &login); err != nil {
			return nil, fmt.Errorf("contributors: scan co-occurrence: %w", err)
		}
		if email != "" && login != "" {
			out = append(out, [2]string{email, login})
		}
	}
	return out, rows.Err()
}

func collectIdentities(ctx context.Context, tx pgx.Tx, orgID string) ([]Identity, error) {
	out := make([]Identity, 0, 256)

	add := func(kind, value, name string, count int) {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			return
		}
		out = append(out, Identity{Kind: kind, Value: value, Name: strings.ToLower(strings.TrimSpace(name)), NameSeen: strings.TrimSpace(name), Count: count})
	}

	// commits: emails + logins. The display name proxy is author_login.
	{
		const q = `
			SELECT lower(author_email::text) AS email, lower(COALESCE(author_login,'')) AS login,
			       COALESCE(MAX(author_login),'') AS name, COUNT(*) AS n
			FROM commits
			WHERE org_id = $1
			GROUP BY 1,2`
		rows, err := tx.Query(ctx, q, orgID)
		if err != nil {
			return nil, fmt.Errorf("contributors: collect commits: %w", err)
		}
		for rows.Next() {
			var email, login, name string
			var n int
			if err := rows.Scan(&email, &login, &name, &n); err != nil {
				rows.Close()
				return nil, fmt.Errorf("contributors: scan commit ident: %w", err)
			}
			if email != "" {
				add(KindEmail, email, name, n)
			}
			if login != "" {
				add(KindLogin, login, name, n)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("contributors: commit ident rows: %w", err)
		}
		rows.Close()
	}

	// pull_requests: author_login.
	if err := collectLogins(ctx, tx, orgID, &out,
		`SELECT lower(author_login), COUNT(*) FROM pull_requests
		 WHERE org_id = $1 AND author_login IS NOT NULL AND author_login <> '' GROUP BY 1`,
		"pull_requests"); err != nil {
		return nil, err
	}

	// pr_reviews: reviewer_login.
	if err := collectLogins(ctx, tx, orgID, &out,
		`SELECT lower(reviewer_login), COUNT(*) FROM pr_reviews
		 WHERE org_id = $1 AND reviewer_login <> '' GROUP BY 1`,
		"pr_reviews"); err != nil {
		return nil, err
	}

	// author_survival: author_email (durability attribution).
	{
		const q = `SELECT lower(author_email::text), COUNT(*) FROM author_survival
			WHERE org_id = $1 AND author_email IS NOT NULL AND author_email::text <> '' GROUP BY 1`
		rows, err := tx.Query(ctx, q, orgID)
		if err != nil {
			return nil, fmt.Errorf("contributors: collect author_survival: %w", err)
		}
		for rows.Next() {
			var email string
			var n int
			if err := rows.Scan(&email, &n); err != nil {
				rows.Close()
				return nil, fmt.Errorf("contributors: scan survival ident: %w", err)
			}
			add(KindEmail, email, "", n)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("contributors: survival ident rows: %w", err)
		}
		rows.Close()
	}

	return out, nil
}

func collectLogins(ctx context.Context, tx pgx.Tx, orgID string, out *[]Identity, q, label string) error {
	rows, err := tx.Query(ctx, q, orgID)
	if err != nil {
		return fmt.Errorf("contributors: collect %s: %w", label, err)
	}
	defer rows.Close()
	for rows.Next() {
		var login string
		var n int
		if err := rows.Scan(&login, &n); err != nil {
			return fmt.Errorf("contributors: scan %s ident: %w", label, err)
		}
		login = strings.ToLower(strings.TrimSpace(login))
		if login == "" {
			continue
		}
		*out = append(*out, Identity{Kind: KindLogin, Value: login, Name: login, NameSeen: login, Count: n})
	}
	return rows.Err()
}

// loadExistingMap returns identityKey -> contributor_id for everything already
// mapped (so DetectAndUpsert is idempotent and manual edits survive).
func loadExistingMap(ctx context.Context, tx pgx.Tx, orgID string) (map[string]string, error) {
	const q = `SELECT kind, value, contributor_id::text FROM contributor_identities WHERE org_id = $1`
	rows, err := tx.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("contributors: load existing map: %w", err)
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var kind, value, cid string
		if err := rows.Scan(&kind, &value, &cid); err != nil {
			return nil, fmt.Errorf("contributors: scan existing map: %w", err)
		}
		m[kind+":"+strings.ToLower(value)] = cid
	}
	return m, rows.Err()
}

func insertContributor(ctx context.Context, tx pgx.Tx, orgID string, cl Cluster) (string, error) {
	const q = `
		INSERT INTO contributors (org_id, display_name, primary_email, is_bot)
		VALUES ($1, $2, NULLIF($3,''), $4)
		RETURNING id::text`
	var id string
	if err := tx.QueryRow(ctx, q, orgID, cl.DisplayName, cl.PrimaryEmail, cl.IsBot).Scan(&id); err != nil {
		return "", fmt.Errorf("contributors: insert contributor: %w", err)
	}
	return id, nil
}

func insertIdentity(ctx context.Context, tx pgx.Tx, orgID, contributorID string, id Identity) error {
	// ON CONFLICT DO NOTHING guards against a race / double-detect; the unique key
	// is (org_id, kind, value).
	const q = `
		INSERT INTO contributor_identities (org_id, contributor_id, kind, value, name_seen)
		VALUES ($1, $2, $3, $4, NULLIF($5,''))
		ON CONFLICT (org_id, kind, value) DO NOTHING`
	if _, err := tx.Exec(ctx, q, orgID, contributorID, id.Kind, id.Value, id.NameSeen); err != nil {
		return fmt.Errorf("contributors: insert identity: %w", err)
	}
	return nil
}
