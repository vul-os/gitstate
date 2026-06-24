// Package store — contributors.go
// CRUD for the contributor identity system: the canonical "contributor" rows
// and the git identities (emails + logins) that map to them. Plus the resolver
// the contribution/analytics aggregation uses to GROUP BY the canonical person
// instead of raw git identities, and the merge/split/link/exclude/invite ops the
// management UI drives.
//
// Every function MUST run inside db.WithOrg(ctx, orgID, …) so the org_isolation
// RLS policy is active (contributors + contributor_identities both FORCE RLS).
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ── Types ────────────────────────────────────────────────────────────────────

// ContributorIdentity is one git identity (email or login) mapped to a contributor.
type ContributorIdentity struct {
	Kind     string `json:"kind"`  // 'email' | 'login'
	Value    string `json:"value"` // lowercased
	NameSeen string `json:"nameSeen"`
}

// ContributorRecord is the canonical person plus their identities and (optional)
// linked gitstate member. MemberName/MemberEmail come from the linked users row.
type ContributorRecord struct {
	ID           string
	OrgID        string
	DisplayName  string
	PrimaryEmail string
	UserID       string // linked gitstate user (empty when none)
	Excluded     bool
	IsBot        bool
	InvitedAt    *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time

	MemberName  string // from users.name when linked
	MemberEmail string // from users.email when linked

	Identities []ContributorIdentity
}

// ContributorUpdate carries the optionally-set fields for UpdateContributor.
// Nil pointers are left unchanged.
type ContributorUpdate struct {
	DisplayName  *string
	PrimaryEmail *string
	Excluded     *bool
	IsBot        *bool
	UserID       *string // "" clears the link (sets NULL); non-empty links a user
}

// ── List / Get ────────────────────────────────────────────────────────────────

// ListContributors returns every contributor in the org with their identities and
// the linked member's name/email (when linked). Ordered by display name.
func ListContributors(ctx context.Context, tx pgx.Tx, orgID string) ([]ContributorRecord, error) {
	const q = `
		SELECT c.id::text, c.org_id::text, c.display_name, COALESCE(c.primary_email,''),
		       COALESCE(c.user_id::text,''), c.excluded, c.is_bot, c.invited_at,
		       c.created_at, c.updated_at,
		       COALESCE(u.name,''), COALESCE(u.email::text,'')
		FROM contributors c
		LEFT JOIN users u ON u.id = c.user_id
		WHERE c.org_id = $1
		ORDER BY lower(c.display_name), c.id`
	rows, err := tx.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("store: list contributors: %w", err)
	}
	defer rows.Close()

	byID := map[string]*ContributorRecord{}
	order := make([]string, 0)
	for rows.Next() {
		var c ContributorRecord
		if err := rows.Scan(
			&c.ID, &c.OrgID, &c.DisplayName, &c.PrimaryEmail, &c.UserID,
			&c.Excluded, &c.IsBot, &c.InvitedAt, &c.CreatedAt, &c.UpdatedAt,
			&c.MemberName, &c.MemberEmail,
		); err != nil {
			return nil, fmt.Errorf("store: scan contributor: %w", err)
		}
		cp := c
		byID[c.ID] = &cp
		order = append(order, c.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list contributors rows: %w", err)
	}

	// Attach identities in a second pass.
	const iq = `
		SELECT contributor_id::text, kind, value, COALESCE(name_seen,'')
		FROM contributor_identities
		WHERE org_id = $1
		ORDER BY kind, value`
	irows, err := tx.Query(ctx, iq, orgID)
	if err != nil {
		return nil, fmt.Errorf("store: list contributor identities: %w", err)
	}
	defer irows.Close()
	for irows.Next() {
		var cid string
		var id ContributorIdentity
		if err := irows.Scan(&cid, &id.Kind, &id.Value, &id.NameSeen); err != nil {
			return nil, fmt.Errorf("store: scan contributor identity: %w", err)
		}
		if c := byID[cid]; c != nil {
			c.Identities = append(c.Identities, id)
		}
	}
	if err := irows.Err(); err != nil {
		return nil, fmt.Errorf("store: list contributor identities rows: %w", err)
	}

	out := make([]ContributorRecord, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out, nil
}

// GetContributor returns one contributor with identities + linked member. Returns
// ErrNotFound when no such contributor exists in the org.
func GetContributor(ctx context.Context, tx pgx.Tx, orgID, id string) (*ContributorRecord, error) {
	const q = `
		SELECT c.id::text, c.org_id::text, c.display_name, COALESCE(c.primary_email,''),
		       COALESCE(c.user_id::text,''), c.excluded, c.is_bot, c.invited_at,
		       c.created_at, c.updated_at,
		       COALESCE(u.name,''), COALESCE(u.email::text,'')
		FROM contributors c
		LEFT JOIN users u ON u.id = c.user_id
		WHERE c.org_id = $1 AND c.id = $2`
	var c ContributorRecord
	err := tx.QueryRow(ctx, q, orgID, id).Scan(
		&c.ID, &c.OrgID, &c.DisplayName, &c.PrimaryEmail, &c.UserID,
		&c.Excluded, &c.IsBot, &c.InvitedAt, &c.CreatedAt, &c.UpdatedAt,
		&c.MemberName, &c.MemberEmail,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get contributor: %w", err)
	}

	const iq = `
		SELECT kind, value, COALESCE(name_seen,'')
		FROM contributor_identities
		WHERE org_id = $1 AND contributor_id = $2
		ORDER BY kind, value`
	rows, err := tx.Query(ctx, iq, orgID, id)
	if err != nil {
		return nil, fmt.Errorf("store: get contributor identities: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var di ContributorIdentity
		if err := rows.Scan(&di.Kind, &di.Value, &di.NameSeen); err != nil {
			return nil, fmt.Errorf("store: scan contributor identity: %w", err)
		}
		c.Identities = append(c.Identities, di)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: get contributor identities rows: %w", err)
	}
	return &c, nil
}

// ── Create / Upsert identity ──────────────────────────────────────────────────

// CreateContributor inserts a new (manually-created) contributor and returns its id.
func CreateContributor(ctx context.Context, tx pgx.Tx, orgID, displayName, primaryEmail string, isBot bool) (string, error) {
	const q = `
		INSERT INTO contributors (org_id, display_name, primary_email, is_bot)
		VALUES ($1, $2, NULLIF($3,''), $4)
		RETURNING id::text`
	var id string
	if err := tx.QueryRow(ctx, q, orgID, displayName, strings.ToLower(strings.TrimSpace(primaryEmail)), isBot).Scan(&id); err != nil {
		return "", fmt.Errorf("store: create contributor: %w", err)
	}
	return id, nil
}

// UpsertIdentity maps an identity (kind,value) to a contributor. If the identity
// already exists it is MOVED to the given contributor (used by split/merge). The
// value is lowercased.
func UpsertIdentity(ctx context.Context, tx pgx.Tx, orgID, contributorID, kind, value, nameSeen string) error {
	const q = `
		INSERT INTO contributor_identities (org_id, contributor_id, kind, value, name_seen)
		VALUES ($1, $2, $3, $4, NULLIF($5,''))
		ON CONFLICT (org_id, kind, value)
		DO UPDATE SET contributor_id = EXCLUDED.contributor_id,
		              name_seen = COALESCE(EXCLUDED.name_seen, contributor_identities.name_seen)`
	if _, err := tx.Exec(ctx, q, orgID, contributorID, kind, strings.ToLower(strings.TrimSpace(value)), nameSeen); err != nil {
		return fmt.Errorf("store: upsert identity: %w", err)
	}
	return nil
}

// ── Update ────────────────────────────────────────────────────────────────────

// UpdateContributor applies the set fields. Returns ErrNotFound when the row is
// missing. Always bumps updated_at.
func UpdateContributor(ctx context.Context, tx pgx.Tx, orgID, id string, up ContributorUpdate) error {
	sets := []string{"updated_at = now()"}
	args := []any{orgID, id}
	add := func(frag string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", frag, len(args)))
	}
	if up.DisplayName != nil {
		add("display_name", *up.DisplayName)
	}
	if up.PrimaryEmail != nil {
		v := strings.ToLower(strings.TrimSpace(*up.PrimaryEmail))
		// store NULL for empty so the column stays clean
		args = append(args, nullString(v))
		sets = append(sets, fmt.Sprintf("primary_email = $%d", len(args)))
	}
	if up.Excluded != nil {
		add("excluded", *up.Excluded)
	}
	if up.IsBot != nil {
		add("is_bot", *up.IsBot)
	}
	if up.UserID != nil {
		args = append(args, nullString(strings.TrimSpace(*up.UserID)))
		sets = append(sets, fmt.Sprintf("user_id = $%d", len(args)))
	}

	q := fmt.Sprintf(`UPDATE contributors SET %s WHERE org_id = $1 AND id = $2`, strings.Join(sets, ", "))
	tag, err := tx.Exec(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("store: update contributor: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetInvited records that an invite was sent to link this contributor to a user.
func SetInvited(ctx context.Context, tx pgx.Tx, orgID, id string, at time.Time) error {
	const q = `UPDATE contributors SET invited_at = $3, updated_at = now() WHERE org_id = $1 AND id = $2`
	tag, err := tx.Exec(ctx, q, orgID, id, at)
	if err != nil {
		return fmt.Errorf("store: set invited: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ── Merge / Split ─────────────────────────────────────────────────────────────

// MergeContributors moves all identities from fromID into intoID, then deletes
// the emptied fromID contributor. The survivor (intoID) keeps its manual fields
// (display name, primary email, exclude, link). Returns ErrNotFound if either
// contributor is missing in the org.
func MergeContributors(ctx context.Context, tx pgx.Tx, orgID, fromID, intoID string) error {
	if fromID == intoID {
		return fmt.Errorf("store: merge contributors: cannot merge a contributor into itself")
	}
	// Verify both exist in the org (RLS-scoped).
	var exists int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM contributors WHERE org_id=$1 AND id IN ($2,$3)`, orgID, fromID, intoID).Scan(&exists); err != nil {
		return fmt.Errorf("store: merge contributors: verify: %w", err)
	}
	if exists != 2 {
		return ErrNotFound
	}
	// Move identities. The unique key is (org_id,kind,value) so a clash can only
	// happen if the same value somehow exists under both — defensively delete the
	// loser's dup first.
	if _, err := tx.Exec(ctx, `
		DELETE FROM contributor_identities f
		WHERE f.org_id = $1 AND f.contributor_id = $2
		  AND EXISTS (SELECT 1 FROM contributor_identities t
		              WHERE t.org_id = $1 AND t.contributor_id = $3
		                AND t.kind = f.kind AND t.value = f.value)`, orgID, fromID, intoID); err != nil {
		return fmt.Errorf("store: merge contributors: dedup: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE contributor_identities SET contributor_id = $3
		WHERE org_id = $1 AND contributor_id = $2`, orgID, fromID, intoID); err != nil {
		return fmt.Errorf("store: merge contributors: move identities: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM contributors WHERE org_id = $1 AND id = $2`, orgID, fromID); err != nil {
		return fmt.Errorf("store: merge contributors: delete emptied: %w", err)
	}
	return nil
}

// SplitIdentity detaches one identity (by value) from its current contributor
// into a brand-new contributor, and returns the new contributor's id. The new
// contributor's display name/primary email seed from the identity. Returns
// ErrNotFound when the identity does not exist in the org.
func SplitIdentity(ctx context.Context, tx pgx.Tx, orgID, value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	var kind, nameSeen string
	err := tx.QueryRow(ctx, `
		SELECT kind, COALESCE(name_seen,'') FROM contributor_identities
		WHERE org_id = $1 AND value = $2`, orgID, value).Scan(&kind, &nameSeen)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("store: split identity: lookup: %w", err)
	}

	display := nameSeen
	if display == "" {
		display = value
	}
	primaryEmail := ""
	if kind == "email" {
		primaryEmail = value
	}
	newID, err := CreateContributor(ctx, tx, orgID, display, primaryEmail, false)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE contributor_identities SET contributor_id = $3
		WHERE org_id = $1 AND value = $2`, orgID, value, newID); err != nil {
		return "", fmt.Errorf("store: split identity: move: %w", err)
	}
	// If the old contributor is now empty AND was auto-created (no user link, not
	// excluded, default-ish), leave it — callers may still want it. We only delete
	// it when it has zero remaining identities to avoid orphans.
	if _, err := tx.Exec(ctx, `
		DELETE FROM contributors c
		WHERE c.org_id = $1 AND c.id <> $2
		  AND NOT EXISTS (SELECT 1 FROM contributor_identities i WHERE i.org_id=$1 AND i.contributor_id=c.id)
		  AND c.user_id IS NULL AND c.invited_at IS NULL`, orgID, newID); err != nil {
		return "", fmt.Errorf("store: split identity: gc empty: %w", err)
	}
	return newID, nil
}

// DeleteContributor removes a contributor and its identities (cascade). Intended
// for manually-created/empty contributors; the API layer decides policy. Returns
// ErrNotFound when missing.
func DeleteContributor(ctx context.Context, tx pgx.Tx, orgID, id string) error {
	tag, err := tx.Exec(ctx, `DELETE FROM contributors WHERE org_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return fmt.Errorf("store: delete contributor: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountContributorIdentities returns how many identities map to a contributor.
func CountContributorIdentities(ctx context.Context, tx pgx.Tx, orgID, id string) (int, error) {
	var n int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM contributor_identities WHERE org_id=$1 AND contributor_id=$2`, orgID, id).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count contributor identities: %w", err)
	}
	return n, nil
}

// ── Resolver ──────────────────────────────────────────────────────────────────

// IdentityToContributor returns a map of lowercased identity value -> contributor_id
// for the whole org. Used by the aggregation layer to remap a raw git ident
// (lower(email) | login) onto its canonical contributor so a person's multiple
// emails/logins COUNT AS ONE. Because emails and logins share the value space and
// an email local-part can equal a login, callers should look up by value and fall
// back to themselves when absent.
//
// NOTE: emails and logins are merged into the same value-keyed map. In the
// (extremely rare) case an email value string equals a login value string that
// maps to a different contributor, the login wins last-write — acceptable because
// the aggregation key is the lowercased value either way.
func IdentityToContributor(ctx context.Context, tx pgx.Tx, orgID string) (map[string]string, error) {
	const q = `SELECT value, contributor_id::text FROM contributor_identities WHERE org_id = $1`
	rows, err := tx.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("store: identity->contributor: %w", err)
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var value, cid string
		if err := rows.Scan(&value, &cid); err != nil {
			return nil, fmt.Errorf("store: scan identity->contributor: %w", err)
		}
		m[strings.ToLower(value)] = cid
	}
	return m, rows.Err()
}

// ContributorIdentityValues returns every lowercased identity `value` (emails +
// logins) that maps to the given contributor in the org. Used by the analytics /
// metrics service layer to expand a chosen contributor into the full set of git
// identities to filter by, so picking a grouped person filters ALL their
// identities (not just one email). Returns an empty slice when the contributor
// has no identities (or does not exist). Org-scoped via RLS + the explicit org_id.
func ContributorIdentityValues(ctx context.Context, tx pgx.Tx, orgID, contributorID string) ([]string, error) {
	const q = `
		SELECT lower(value) FROM contributor_identities
		WHERE org_id = $1 AND contributor_id = $2
		ORDER BY value`
	rows, err := tx.Query(ctx, q, orgID, contributorID)
	if err != nil {
		return nil, fmt.Errorf("store: contributor identity values: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("store: scan contributor identity value: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ContributorIDForIdentity resolves a single lowercased identity value (email or
// login) to its canonical contributor_id, or ("", nil) when the identity is not
// mapped to any contributor. Org-scoped.
func ContributorIDForIdentity(ctx context.Context, tx pgx.Tx, orgID, value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", nil
	}
	const q = `SELECT contributor_id::text FROM contributor_identities WHERE org_id = $1 AND value = $2 LIMIT 1`
	var id string
	err := tx.QueryRow(ctx, q, orgID, value).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: contributor id for identity: %w", err)
	}
	return id, nil
}

// ExcludedContributors returns the set of contributor_ids flagged excluded, and
// (separately) those flagged is_bot, so the aggregation layer can drop them.
func ExcludedContributors(ctx context.Context, tx pgx.Tx, orgID string) (excluded map[string]bool, bots map[string]bool, err error) {
	const q = `SELECT id::text, excluded, is_bot FROM contributors WHERE org_id = $1`
	rows, e := tx.Query(ctx, q, orgID)
	if e != nil {
		return nil, nil, fmt.Errorf("store: excluded contributors: %w", e)
	}
	defer rows.Close()
	excluded = map[string]bool{}
	bots = map[string]bool{}
	for rows.Next() {
		var id string
		var ex, bot bool
		if err := rows.Scan(&id, &ex, &bot); err != nil {
			return nil, nil, fmt.Errorf("store: scan excluded contributor: %w", err)
		}
		if ex {
			excluded[id] = true
		}
		if bot {
			bots[id] = true
		}
	}
	return excluded, bots, rows.Err()
}

// FindContributorByIdentityOrEmail resolves a contributor for an accepted invite
// (or any email): it matches the email first against contributor_identities, then
// against contributors.primary_email. Returns ("", nil) when no match. Used by the
// invite-accept fallback when org_invites carries no contributor_id column.
func FindContributorByIdentityOrEmail(ctx context.Context, tx pgx.Tx, orgID, email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return "", nil
	}
	const q = `
		SELECT contributor_id::text FROM contributor_identities
		WHERE org_id = $1 AND kind = 'email' AND value = $2
		UNION
		SELECT id::text FROM contributors
		WHERE org_id = $1 AND lower(primary_email) = $2
		LIMIT 1`
	var id string
	err := tx.QueryRow(ctx, q, orgID, email).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: find contributor by email: %w", err)
	}
	return id, nil
}

// LinkContributorToUser sets contributor.user_id (the link survives email changes
// because it keys on user_id, not email). orgID-scoped.
func LinkContributorToUser(ctx context.Context, tx pgx.Tx, orgID, contributorID, userID string) error {
	const q = `UPDATE contributors SET user_id = $3, updated_at = now() WHERE org_id = $1 AND id = $2`
	tag, err := tx.Exec(ctx, q, orgID, contributorID, userID)
	if err != nil {
		return fmt.Errorf("store: link contributor to user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ── Stats ─────────────────────────────────────────────────────────────────────

// ContributorStats are best-effort lifetime activity counts for one contributor,
// aggregated over ALL of its identities (commits by email|login, PRs by login,
// reviews by login).
type ContributorStats struct {
	Commits int `json:"commits"`
	PRs     int `json:"prs"`
	Reviews int `json:"reviews"`
}

// ContributorStatsByID returns per-contributor lifetime stats keyed by
// contributor_id, computed by joining the source tables to contributor_identities.
// Identities not yet mapped to any contributor are not represented here. Org-scoped.
func ContributorStatsByID(ctx context.Context, tx pgx.Tx, orgID string) (map[string]ContributorStats, error) {
	out := map[string]ContributorStats{}

	// commits: match by email OR login identity.
	const qc = `
		SELECT ci.contributor_id::text, COUNT(*) FROM commits c
		JOIN contributor_identities ci ON ci.org_id = c.org_id
		  AND ( (ci.kind='email' AND ci.value = lower(c.author_email::text))
		     OR (ci.kind='login' AND ci.value = lower(c.author_login)) )
		WHERE c.org_id = $1
		GROUP BY 1`
	if err := scanContribCount(ctx, tx, qc, orgID, out, func(s *ContributorStats, n int) { s.Commits += n }); err != nil {
		return nil, fmt.Errorf("store: contributor stats commits: %w", err)
	}

	// PRs: by author_login.
	const qp = `
		SELECT ci.contributor_id::text, COUNT(*) FROM pull_requests p
		JOIN contributor_identities ci ON ci.org_id = p.org_id
		  AND ci.kind='login' AND ci.value = lower(p.author_login)
		WHERE p.org_id = $1 AND p.author_login IS NOT NULL AND p.author_login <> ''
		GROUP BY 1`
	if err := scanContribCount(ctx, tx, qp, orgID, out, func(s *ContributorStats, n int) { s.PRs += n }); err != nil {
		return nil, fmt.Errorf("store: contributor stats prs: %w", err)
	}

	// reviews: by reviewer_login (distinct PRs).
	const qr = `
		SELECT ci.contributor_id::text, COUNT(DISTINCT pr.pr_id) FROM pr_reviews pr
		JOIN contributor_identities ci ON ci.org_id = pr.org_id
		  AND ci.kind='login' AND ci.value = lower(pr.reviewer_login)
		WHERE pr.org_id = $1 AND pr.reviewer_login <> ''
		GROUP BY 1`
	if err := scanContribCount(ctx, tx, qr, orgID, out, func(s *ContributorStats, n int) { s.Reviews += n }); err != nil {
		return nil, fmt.Errorf("store: contributor stats reviews: %w", err)
	}

	return out, nil
}

func scanContribCount(ctx context.Context, tx pgx.Tx, q, orgID string, out map[string]ContributorStats, apply func(*ContributorStats, int)) error {
	rows, err := tx.Query(ctx, q, orgID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return err
		}
		s := out[id]
		apply(&s, n)
		out[id] = s
	}
	return rows.Err()
}

// nullString returns nil for empty strings so a NULL is stored, else the string.
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// PruneOrphanContributors removes identities whose git value no longer appears in
// ANY remaining commit/PR/review (e.g. after a repo is disconnected + its data
// deleted), then removes contributors left with no identities — UNLESS they are
// linked to a user (real members stay). Run inside db.WithOrg (RLS scopes it).
func PruneOrphanContributors(ctx context.Context, tx pgx.Tx, orgID string) (int64, error) {
	const delIdents = `
		WITH live AS (
			SELECT lower(author_email::text) v FROM commits WHERE author_email IS NOT NULL
			UNION SELECT lower(author_login) FROM commits WHERE author_login IS NOT NULL
			UNION SELECT lower(author_login) FROM pull_requests WHERE author_login IS NOT NULL
			UNION SELECT lower(reviewer_login) FROM pr_reviews WHERE reviewer_login IS NOT NULL
		)
		DELETE FROM contributor_identities ci
		WHERE ci.org_id = $1 AND ci.value NOT IN (SELECT v FROM live WHERE v IS NOT NULL AND v <> '')`
	if _, err := tx.Exec(ctx, delIdents, orgID); err != nil {
		return 0, fmt.Errorf("store: prune orphan identities: %w", err)
	}
	const delContribs = `
		DELETE FROM contributors c
		WHERE c.org_id = $1 AND c.user_id IS NULL
		  AND NOT EXISTS (SELECT 1 FROM contributor_identities ci WHERE ci.contributor_id = c.id)`
	tag, err := tx.Exec(ctx, delContribs, orgID)
	if err != nil {
		return 0, fmt.Errorf("store: prune orphan contributors: %w", err)
	}
	return tag.RowsAffected(), nil
}
