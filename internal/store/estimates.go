package store

// estimates.go — queries against the effort_estimates table (decisions P3).
// Estimates are org-scoped (FORCE RLS); all reads/writes run inside db.WithOrg
// (pgx.Tx) so the org GUC is set — a bare-pool read would see current_org()=NULL
// and return zero rows.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// EffortEstimate mirrors a row from effort_estimates.
type EffortEstimate struct {
	ID         string
	OrgID      string
	PRID       *string // nullable — estimate may be for an issue only
	IssueID    *string // nullable — estimate may be for a PR only
	Difficulty float64
	Rationale  string
	Evidence   map[string]interface{} // jsonb column
	Model      string
	CreatedAt  time.Time

	// Calibration fields (migration 017). Populated by EstimateForPR /
	// RecomputeCalibration; nil/zero on legacy rows.
	PredictedSecs *float64 // calibrated estimate at creation
	ActualSecs    *int64   // observed lead time, backfilled at merge
	CohortKey     string   // cohort used for the conversion
	SizeBucket    string   // xs|s|m|l|xl
	ChangeType    string   // feature|fix|refactor|chore|docs|test
}

// SaveEstimateInput carries the fields required to insert an effort estimate.
// Both PRID and IssueID are optional; at least one should be set.
type SaveEstimateInput struct {
	OrgID      string
	PRID       *string
	IssueID    *string
	Difficulty float64
	Rationale  string
	Evidence   map[string]interface{}
	Model      string
}

// SaveEstimate inserts a new row into effort_estimates inside an existing
// org-scoped transaction (tx must be from db.WithOrg).
func SaveEstimate(ctx context.Context, tx pgx.Tx, in SaveEstimateInput) (*EffortEstimate, error) {
	evidenceJSON, err := json.Marshal(in.Evidence)
	if err != nil {
		return nil, fmt.Errorf("store.estimates: marshal evidence: %w", err)
	}

	const q = `
		INSERT INTO effort_estimates
		    (org_id, pr_id, issue_id, difficulty, rationale, evidence, model)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, org_id,
		          pr_id::text,
		          issue_id::text,
		          difficulty::float8,
		          COALESCE(rationale, ''),
		          evidence,
		          COALESCE(model, ''),
		          created_at`

	row := tx.QueryRow(ctx, q,
		in.OrgID,
		in.PRID,
		in.IssueID,
		in.Difficulty,
		in.Rationale,
		evidenceJSON,
		in.Model,
	)

	return scanEstimate(row)
}

// scanEstimate reads a single effort_estimate row from any pgx.Row.
func scanEstimate(row pgx.Row) (*EffortEstimate, error) {
	var e EffortEstimate
	var prID, issueID *string
	var evidenceRaw []byte

	err := row.Scan(
		&e.ID,
		&e.OrgID,
		&prID,
		&issueID,
		&e.Difficulty,
		&e.Rationale,
		&evidenceRaw,
		&e.Model,
		&e.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("store.estimates: scan: %w", err)
	}

	e.PRID = prID
	e.IssueID = issueID

	if len(evidenceRaw) > 0 {
		if err := json.Unmarshal(evidenceRaw, &e.Evidence); err != nil {
			return nil, fmt.Errorf("store.estimates: unmarshal evidence: %w", err)
		}
	}
	if e.Evidence == nil {
		e.Evidence = make(map[string]interface{})
	}

	return &e, nil
}
