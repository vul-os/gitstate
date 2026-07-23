// Package metrics — DB-backed test proving ComputeInvolvement now sources
// reviews_done from the pr_reviews table (gap B): a reviewer who reviewed N
// DISTINCT PRs in the period gets reviews_done = N, self-reviews are excluded,
// and two reviews on the SAME PR count once.
package metrics

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/jackc/pgx/v5"
)

func TestComputeInvolvementReviewsDone(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping involvement reviews test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	database, err := db.New(ctx, &config.Config{Database: config.DatabaseConfig{URL: dbURL}})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	ns := time.Now().UnixNano()
	var orgID string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO organizations (slug,name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("inv-rev-%d", ns), "Inv Rev").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, orgID)
	})

	reviewerEmail := fmt.Sprintf("rev-%d@example.com", ns)
	authorEmail := fmt.Sprintf("auth-%d@example.com", ns)
	var reviewerUserID, authorUserID string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO users (email,name) VALUES ($1,'Rev') RETURNING id`, reviewerEmail).Scan(&reviewerUserID); err != nil {
		t.Fatalf("seed reviewer: %v", err)
	}
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO users (email,name) VALUES ($1,'Auth') RETURNING id`, authorEmail).Scan(&authorUserID); err != nil {
		t.Fatalf("seed author: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`,
			[]string{reviewerUserID, authorUserID})
	})

	period := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	submitted := time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)

	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var repoID string
		if err := tx.QueryRow(ctx,
			`INSERT INTO repos (org_id,platform,external_id,full_name) VALUES ($1,'github',$2,'acme/svc') RETURNING id`,
			orgID, fmt.Sprintf("inv-repo-%d", ns)).Scan(&repoID); err != nil {
			return err
		}
		// Commit identity bridge: login→email→users for both reviewer + author.
		for _, c := range []struct{ login, email string }{
			{"reviewer", reviewerEmail}, {"author", authorEmail},
		} {
			if _, err := tx.Exec(ctx,
				`INSERT INTO commits (org_id,repo_id,sha,author_login,author_email,committed_at)
				 VALUES ($1,$2,$3,$4,$5,$6)`,
				orgID, repoID, fmt.Sprintf("sha-%s-%d", c.login, ns), c.login, c.email, submitted); err != nil {
				return err
			}
		}
		// Two PRs authored by "author".
		var pr1, pr2 string
		for i, dst := range []*string{&pr1, &pr2} {
			if err := tx.QueryRow(ctx,
				`INSERT INTO pull_requests (org_id,repo_id,platform,external_id,number,title,author_login,state,merged_at,created_at)
				 VALUES ($1,$2,'github',$3,$4,'feat','author','merged',$5,$5) RETURNING id`,
				orgID, repoID, fmt.Sprintf("inv-pr-%d-%d", ns, i), 100+i, submitted).Scan(dst); err != nil {
				return err
			}
		}
		// reviewer reviewed BOTH PRs (pr2 twice → counts once via DISTINCT pr_id).
		// author "reviewed" pr1 (self-review) → must be excluded.
		insert := func(prID, login, state string, at time.Time) error {
			_, err := tx.Exec(ctx,
				`INSERT INTO pr_reviews (org_id,repo_id,pr_id,reviewer_login,state,submitted_at)
				 VALUES ($1,$2,$3,$4,$5,$6)`, orgID, repoID, prID, login, state, at)
			return err
		}
		if err := insert(pr1, "reviewer", "approved", submitted); err != nil {
			return err
		}
		if err := insert(pr2, "reviewer", "commented", submitted.Add(time.Hour)); err != nil {
			return err
		}
		if err := insert(pr2, "reviewer", "approved", submitted.Add(2*time.Hour)); err != nil {
			return err
		}
		if err := insert(pr1, "author", "commented", submitted.Add(3*time.Hour)); err != nil { // self-review
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	svc := New(database, nil)
	if err := svc.ComputeInvolvement(ctx, orgID, period); err != nil {
		t.Fatalf("ComputeInvolvement: %v", err)
	}

	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var reviews int
		if err := tx.QueryRow(ctx,
			`SELECT reviews_done FROM involvement WHERE org_id=$1 AND user_id=$2 AND period_start=$3`,
			orgID, reviewerUserID, period).Scan(&reviews); err != nil {
			return fmt.Errorf("reviewer row: %w", err)
		}
		// 2 DISTINCT PRs reviewed (pr2's duplicate collapses).
		if reviews != 2 {
			t.Errorf("reviewer reviews_done = %d, want 2 (DISTINCT PRs reviewed)", reviews)
		}
		// author self-review must NOT credit author with a review.
		var authorReviews int
		err := tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(reviews_done),0) FROM involvement WHERE org_id=$1 AND user_id=$2 AND period_start=$3`,
			orgID, authorUserID, period).Scan(&authorReviews)
		if err != nil {
			return fmt.Errorf("author row: %w", err)
		}
		if authorReviews != 0 {
			t.Errorf("author reviews_done = %d, want 0 (self-review excluded)", authorReviews)
		}
		return nil
	}); err != nil {
		t.Fatalf("verify: %v", err)
	}
}
