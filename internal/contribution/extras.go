// Package contribution — extras.go
// The three CONTRIBUTION extensions layered on top of the 6-dimension composite
// engine (contribution.go), all reusing the SAME pure Profiles core per window:
//
//   - Trends  — the composite (+ 6 dimension scores) per member across the last
//     ~N periods, persisted to contribution_snapshots (idempotent) and returned
//     as a per-member series for the over-time chart / sparklines.
//   - Kudos   — peer recognition (a human signal that does NOT feed the score).
//
// Every DB touch runs org-scoped via db.WithOrg so RLS enforces the boundary.
package contribution

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// ── Trends over time ────────────────────────────────────────────────────────

// Interval selects the granularity of a trend window.
type Interval string

const (
	IntervalMonth Interval = "month"
	IntervalWeek  Interval = "week"
)

// TrendPoint is one period's score for a member.
type TrendPoint struct {
	PeriodStart time.Time          `json:"periodStart"`
	PeriodEnd   time.Time          `json:"periodEnd"`
	Composite   float64            `json:"composite"`
	Dimensions  map[string]float64 `json:"dimensions"`
}

// TrendSeries is one PERSON's composite over the last N periods (oldest first),
// keyed by the canonical contributor so a grouped person (usually with no userId)
// still gets a real series. UserID is carried for linked members.
type TrendSeries struct {
	ContributorID string       `json:"contributorId"`
	UserID        string       `json:"userId"`
	Name          string       `json:"name"`
	Email         string       `json:"email"`
	IsAgentBot    bool         `json:"isAgentBot"`
	Points        []TrendPoint `json:"points"`
}

// windowBounds returns the [start,end) for the i-th window back from `now`
// (i=0 is the most recent complete period). Month windows are calendar months;
// week windows are 7-day blocks ending at the start of the current period.
func windowBounds(now time.Time, interval Interval, i int) (time.Time, time.Time) {
	switch interval {
	case IntervalWeek:
		// Anchor to the start of "this week" (Monday 00:00 UTC), then step back.
		anchor := startOfWeek(now)
		end := anchor.AddDate(0, 0, -7*i)
		start := end.AddDate(0, 0, -7)
		return start, end
	default: // month
		anchor := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		end := anchor.AddDate(0, -i, 0)
		start := end.AddDate(0, -1, 0)
		return start, end
	}
}

func startOfWeek(t time.Time) time.Time {
	t = t.UTC()
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	// Monday=0 … Sunday=6
	wd := (int(d.Weekday()) + 6) % 7
	return d.AddDate(0, 0, -wd)
}

func (m DimensionScores) asMap() map[string]float64 {
	return map[string]float64{
		DimShipped:    m.Shipped,
		DimReview:     m.Review,
		DimEffort:     m.Effort,
		DimQuality:    m.Quality,
		DimOwnership:  m.Ownership,
		DimDurability: m.Durability,
	}
}

// ComputeTrends computes the composite per member for each of the last `periods`
// windows at the given interval, PERSISTS each member/period to
// contribution_snapshots (idempotent upsert), and returns a per-member series.
// `now` is injected for deterministic tests. Bounded to a sane window count.
func (s *Service) ComputeTrends(ctx context.Context, orgID string, periods int, interval Interval, now time.Time) ([]TrendSeries, error) {
	if periods <= 0 {
		periods = 6
	}
	if periods > 24 {
		periods = 24
	}
	if interval != IntervalWeek {
		interval = IntervalMonth
	}

	// person-key → series (kept insertion-stable, sorted at the end). The person key
	// is the canonical contributor id when known, else the linked user id, else a
	// raw-identity fallback — so EVERY person gets one series even before detection
	// links them to a user (the bug we're fixing: snapshots existed only for the 1
	// linked user, so every grouped person had an empty/fake trend).
	bySeries := map[string]*TrendSeries{}
	personKey := func(m Member) string {
		if m.ContributorID != "" {
			return "c:" + m.ContributorID
		}
		if m.UserID != "" {
			return "u:" + m.UserID
		}
		// Raw identity (no contributor, no user): key by email|name so it stays stable
		// across periods and never collapses distinct people together.
		if m.Email != "" {
			return "e:" + strings.ToLower(m.Email)
		}
		return "n:" + strings.ToLower(m.Name)
	}

	// Walk oldest → newest so each series reads left→right.
	for i := periods - 1; i >= 0; i-- {
		start, end := windowBounds(now.UTC(), interval, i)
		rep, err := s.Compute(ctx, orgID, Period{From: start, To: end})
		if err != nil {
			return nil, err
		}

		// Persist this window's snapshots per PERSON: keyed by contributor_id when
		// known, else the linked user_id. A row with neither (a raw unmapped identity)
		// can't be persisted (no stable FK id) so it is skipped — it still renders in
		// the live series, it just isn't cached. Best-effort upsert inside one tx.
		err = s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
			for _, m := range rep.Members {
				if m.ContributorID == "" && m.UserID == "" {
					continue
				}
				if err := store.UpsertPersonSnapshot(ctx, tx, orgID, m.ContributorID, m.UserID, start, end, m.Composite, m.Dimensions.asMap()); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}

		for _, m := range rep.Members {
			key := personKey(m)
			ser := bySeries[key]
			if ser == nil {
				ser = &TrendSeries{ContributorID: m.ContributorID, UserID: m.UserID, Name: m.Name, Email: m.Email, IsAgentBot: m.IsAgentBot}
				bySeries[key] = ser
			}
			// Backfill identity fields a later period may resolve (e.g. a user link).
			if ser.ContributorID == "" {
				ser.ContributorID = m.ContributorID
			}
			if ser.UserID == "" {
				ser.UserID = m.UserID
			}
			if ser.Name == "" {
				ser.Name = m.Name
			}
			ser.Points = append(ser.Points, TrendPoint{
				PeriodStart: start, PeriodEnd: end,
				Composite: m.Composite, Dimensions: m.Dimensions.asMap(),
			})
		}
	}

	out := make([]TrendSeries, 0, len(bySeries))
	for _, ser := range bySeries {
		out = append(out, *ser)
	}
	// Sort by latest composite desc (last point), tie-break by name.
	sort.SliceStable(out, func(a, b int) bool {
		la, lb := lastComposite(out[a]), lastComposite(out[b])
		if la != lb {
			return la > lb
		}
		return out[a].Name < out[b].Name
	})
	return out, nil
}

func lastComposite(s TrendSeries) float64 {
	if len(s.Points) == 0 {
		return 0
	}
	return s.Points[len(s.Points)-1].Composite
}

// ── Kudos ───────────────────────────────────────────────────────────────────

// GiveKudos records one peer recognition. The giver is the caller; giving kudos
// to yourself is rejected by the handler. dimension is optional (ties to an axis).
func (s *Service) GiveKudos(ctx context.Context, orgID, fromUser, toUser, dimension, message string) (store.Kudo, error) {
	var k store.Kudo
	err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var e error
		k, e = store.InsertKudo(ctx, tx, orgID, fromUser, toUser, dimension, message)
		return e
	})
	return k, err
}

// ListKudos returns recognition messages for the org (optionally filtered to one
// recipient), newest first.
func (s *Service) ListKudos(ctx context.Context, orgID, toUser string) ([]store.Kudo, error) {
	var out []store.Kudo
	err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var e error
		out, e = store.ListKudos(ctx, tx, orgID, toUser, 100)
		return e
	})
	return out, err
}

// KudosCounts returns kudos-received counts per recipient user_id (a human signal
// surfaced beside each member on the roster).
func (s *Service) KudosCounts(ctx context.Context, orgID string) (map[string]int, error) {
	var out map[string]int
	err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var e error
		out, e = store.KudosCounts(ctx, tx, orgID)
		return e
	})
	return out, err
}
