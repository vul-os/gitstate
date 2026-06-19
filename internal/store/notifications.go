// Package store — notifications.go
// Org-scoped CRUD for notification_channels and notification_log (migration
// 20260619_013). Every function runs inside a db.WithOrg transaction so the
// org_isolation RLS policy enforces the tenancy boundary. The org_id is never
// interpolated for isolation — RLS handles it — and all user-supplied values are
// passed as bind parameters.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// DigestPrefs records which digests a channel should receive. Mirrors the
// notification_channels.digests jsonb column.
type DigestPrefs struct {
	WeeklyStatus bool `json:"weeklyStatus"`
	StalePRs     bool `json:"stalePRs"`
	OOO          bool `json:"ooo"`
}

// NotificationChannel mirrors a notification_channels row.
type NotificationChannel struct {
	ID        string      `json:"id"`
	OrgID     string      `json:"orgId"`
	Kind      string      `json:"kind"`   // slack | webhook | email
	Target    string      `json:"target"` // webhook URL or email address
	Label     string      `json:"label"`
	Enabled   bool        `json:"enabled"`
	Digests   DigestPrefs `json:"digests"`
	Schedule  string      `json:"schedule"` // weekly | daily
	CreatedAt time.Time   `json:"createdAt"`
}

const notificationChannelCols = `id, org_id, kind, target, COALESCE(label,''),
	enabled, digests, schedule, created_at`

func scanNotificationChannel(row pgx.Row) (*NotificationChannel, error) {
	var c NotificationChannel
	var digestsRaw []byte
	if err := row.Scan(
		&c.ID, &c.OrgID, &c.Kind, &c.Target, &c.Label,
		&c.Enabled, &digestsRaw, &c.Schedule, &c.CreatedAt,
	); err != nil {
		return nil, err
	}
	if len(digestsRaw) > 0 {
		if err := json.Unmarshal(digestsRaw, &c.Digests); err != nil {
			return nil, fmt.Errorf("store: unmarshal digests: %w", err)
		}
	}
	return &c, nil
}

// ListNotificationChannels returns all channels for the org, newest first.
// Must run inside a db.WithOrg transaction.
func ListNotificationChannels(ctx context.Context, tx pgx.Tx, orgID string) ([]*NotificationChannel, error) {
	rows, err := tx.Query(ctx, `SELECT `+notificationChannelCols+`
		FROM notification_channels
		WHERE org_id = $1
		ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("store: list notification channels: %w", err)
	}
	defer rows.Close()

	var out []*NotificationChannel
	for rows.Next() {
		c, err := scanNotificationChannel(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan notification channel: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetNotificationChannel returns a single channel by id (org-scoped via RLS).
func GetNotificationChannel(ctx context.Context, tx pgx.Tx, orgID, id string) (*NotificationChannel, error) {
	c, err := scanNotificationChannel(tx.QueryRow(ctx, `SELECT `+notificationChannelCols+`
		FROM notification_channels
		WHERE id = $1 AND org_id = $2`, id, orgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get notification channel: %w", err)
	}
	return c, nil
}

// CreateNotificationChannel inserts a channel.
func CreateNotificationChannel(ctx context.Context, tx pgx.Tx, orgID, kind, target, label string, enabled bool, digests DigestPrefs, schedule string) (*NotificationChannel, error) {
	digestsJSON, err := json.Marshal(digests)
	if err != nil {
		return nil, fmt.Errorf("store: marshal digests: %w", err)
	}
	c, err := scanNotificationChannel(tx.QueryRow(ctx, `
		INSERT INTO notification_channels (org_id, kind, target, label, enabled, digests, schedule)
		VALUES ($1, $2, $3, NULLIF($4,''), $5, $6, $7)
		RETURNING `+notificationChannelCols,
		orgID, kind, target, label, enabled, digestsJSON, schedule))
	if err != nil {
		return nil, fmt.Errorf("store: create notification channel: %w", err)
	}
	return c, nil
}

// NotificationChannelPatch carries optional updates for a channel. Nil fields
// are left unchanged.
type NotificationChannelPatch struct {
	Target   *string
	Label    *string
	Enabled  *bool
	Digests  *DigestPrefs
	Schedule *string
}

// UpdateNotificationChannel applies a partial update and returns the new row.
func UpdateNotificationChannel(ctx context.Context, tx pgx.Tx, orgID, id string, p NotificationChannelPatch) (*NotificationChannel, error) {
	// Build a dynamic SET list; org_id is enforced by RLS + the WHERE clause.
	set := ""
	args := []any{}
	idx := 1
	add := func(frag string, val any) {
		if set != "" {
			set += ", "
		}
		set += fmt.Sprintf("%s = $%d", frag, idx)
		args = append(args, val)
		idx++
	}
	if p.Target != nil {
		add("target", *p.Target)
	}
	if p.Label != nil {
		add("label", *p.Label)
	}
	if p.Enabled != nil {
		add("enabled", *p.Enabled)
	}
	if p.Digests != nil {
		digestsJSON, err := json.Marshal(*p.Digests)
		if err != nil {
			return nil, fmt.Errorf("store: marshal digests: %w", err)
		}
		add("digests", digestsJSON)
	}
	if p.Schedule != nil {
		add("schedule", *p.Schedule)
	}
	if set == "" {
		// Nothing to update — return the current row.
		return GetNotificationChannel(ctx, tx, orgID, id)
	}

	q := fmt.Sprintf(`UPDATE notification_channels SET %s
		WHERE id = $%d AND org_id = $%d
		RETURNING %s`, set, idx, idx+1, notificationChannelCols)
	args = append(args, id, orgID)

	c, err := scanNotificationChannel(tx.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: update notification channel: %w", err)
	}
	return c, nil
}

// DeleteNotificationChannel removes a channel by id (org-scoped via RLS).
func DeleteNotificationChannel(ctx context.Context, tx pgx.Tx, orgID, id string) error {
	ct, err := tx.Exec(ctx, `DELETE FROM notification_channels WHERE id = $1 AND org_id = $2`, id, orgID)
	if err != nil {
		return fmt.Errorf("store: delete notification channel: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ── Notification log ──────────────────────────────────────────────────────────

// NotificationLogEntry mirrors a notification_log row.
type NotificationLogEntry struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"orgId"`
	ChannelID string    `json:"channelId,omitempty"`
	Kind      string    `json:"kind"`
	Status    string    `json:"status"` // sent | failed | preview
	Summary   string    `json:"summary"`
	SentAt    time.Time `json:"sentAt"`
}

// WriteNotificationLog records a send/preview/failure. channelID may be empty
// (e.g. for a preview not tied to a channel). Must run inside a db.WithOrg tx.
func WriteNotificationLog(ctx context.Context, tx pgx.Tx, orgID, channelID, kind, status, summary string) error {
	var chArg any
	if channelID != "" {
		chArg = channelID
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO notification_log (org_id, channel_id, kind, status, summary)
		VALUES ($1, $2, $3, $4, NULLIF($5,''))`,
		orgID, chArg, kind, status, summary)
	if err != nil {
		return fmt.Errorf("store: write notification log: %w", err)
	}
	return nil
}

// ListNotificationLog returns the most recent log entries for the org.
func ListNotificationLog(ctx context.Context, tx pgx.Tx, orgID string, limit int) ([]*NotificationLogEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := tx.Query(ctx, `
		SELECT id, org_id, COALESCE(channel_id::text,''), kind, status, COALESCE(summary,''), sent_at
		FROM notification_log
		WHERE org_id = $1
		ORDER BY sent_at DESC
		LIMIT $2`, orgID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list notification log: %w", err)
	}
	defer rows.Close()

	var out []*NotificationLogEntry
	for rows.Next() {
		var e NotificationLogEntry
		if err := rows.Scan(&e.ID, &e.OrgID, &e.ChannelID, &e.Kind, &e.Status, &e.Summary, &e.SentAt); err != nil {
			return nil, fmt.Errorf("store: scan notification log: %w", err)
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// ── Digest source queries ─────────────────────────────────────────────────────

// StalePR is an open pull request that has been open longer than the threshold.
type StalePR struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	AuthorLogin string    `json:"authorLogin"`
	RepoName    string    `json:"repoName"`
	Number      int       `json:"number"`
	CreatedAt   time.Time `json:"createdAt"`
	AgeDays     int       `json:"ageDays"`
}

// ListStalePRs returns open pull requests created more than olderThanDays ago,
// oldest first. Must run inside a db.WithOrg transaction.
func ListStalePRs(ctx context.Context, tx pgx.Tx, orgID string, olderThanDays int) ([]*StalePR, error) {
	if olderThanDays <= 0 {
		olderThanDays = 7
	}
	const q = `
		SELECT
			p.id::text,
			COALESCE(p.title,''),
			COALESCE(p.author_login,''),
			COALESCE(r.full_name,''),
			COALESCE(p.number,0),
			p.created_at,
			GREATEST(0, EXTRACT(DAY FROM (now() - p.created_at))::int) AS age_days
		FROM pull_requests p
		LEFT JOIN repos r ON r.id = p.repo_id
		WHERE p.org_id = $1
		  AND p.state = 'open'
		  AND p.merged_at IS NULL
		  AND p.created_at < now() - make_interval(days => $2)
		ORDER BY p.created_at ASC`

	rows, err := tx.Query(ctx, q, orgID, olderThanDays)
	if err != nil {
		return nil, fmt.Errorf("store: list stale PRs: %w", err)
	}
	defer rows.Close()

	var out []*StalePR
	for rows.Next() {
		var s StalePR
		if err := rows.Scan(&s.ID, &s.Title, &s.AuthorLogin, &s.RepoName, &s.Number, &s.CreatedAt, &s.AgeDays); err != nil {
			return nil, fmt.Errorf("store: scan stale PR: %w", err)
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

// OOOEntry is one member's approved leave overlapping a window.
type OOOEntry struct {
	UserID    string    `json:"userId"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Kind      string    `json:"kind"`
	StartDate time.Time `json:"startDate"`
	EndDate   time.Time `json:"endDate"`
	HalfDay   bool      `json:"halfDay"`
	Portion   string    `json:"portion"`
}

// ListOOOInPeriod returns every member's approved leave overlapping [from, to),
// joined to the member's display name/email. Must run inside a db.WithOrg tx.
func ListOOOInPeriod(ctx context.Context, tx pgx.Tx, orgID string, from, to time.Time) ([]*OOOEntry, error) {
	const q = `
		SELECT
			l.user_id::text,
			COALESCE(u.name,''),
			COALESCE(u.email::text,''),
			l.kind,
			l.start_date,
			l.end_date,
			l.half_day,
			COALESCE(l.portion,'full')
		FROM leave_entries l
		JOIN users u ON u.id = l.user_id
		WHERE l.org_id = $1
		  AND l.status = 'approved'
		  AND l.start_date < $3
		  AND l.end_date  >= $2
		ORDER BY l.start_date ASC, u.email ASC`

	rows, err := tx.Query(ctx, q, orgID, from, to)
	if err != nil {
		return nil, fmt.Errorf("store: list OOO in period: %w", err)
	}
	defer rows.Close()

	var out []*OOOEntry
	for rows.Next() {
		var e OOOEntry
		if err := rows.Scan(&e.UserID, &e.Name, &e.Email, &e.Kind, &e.StartDate, &e.EndDate, &e.HalfDay, &e.Portion); err != nil {
			return nil, fmt.Errorf("store: scan OOO entry: %w", err)
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// WeeklyShipped holds the counts of issues/PRs that reached a shipped state in a
// window, plus median-ish cycle time. It is a lightweight summary used by the
// weekly-status digest.
type WeeklyShipped struct {
	IssuesDone   int `json:"issuesDone"`
	PRsMerged    int `json:"prsMerged"`
	CommitsTotal int `json:"commitsTotal"`
}

// WeeklyShippedCounts returns shipped counts over [from, to). Must run inside a
// db.WithOrg transaction.
func WeeklyShippedCounts(ctx context.Context, tx pgx.Tx, orgID string, from, to time.Time) (WeeklyShipped, error) {
	var s WeeklyShipped
	err := tx.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM issues i
			   WHERE i.org_id = $1
			     AND COALESCE(i.derived_state, i.state) IN ('done','closed')
			     AND i.updated_at >= $2 AND i.updated_at < $3),
			(SELECT COUNT(*) FROM pull_requests p
			   WHERE p.org_id = $1
			     AND p.merged_at IS NOT NULL
			     AND p.merged_at >= $2 AND p.merged_at < $3),
			(SELECT COUNT(*) FROM commits c
			   WHERE c.org_id = $1
			     AND c.committed_at >= $2 AND c.committed_at < $3)`,
		orgID, from, to).Scan(&s.IssuesDone, &s.PRsMerged, &s.CommitsTotal)
	if err != nil {
		return WeeklyShipped{}, fmt.Errorf("store: weekly shipped counts: %w", err)
	}
	return s, nil
}
