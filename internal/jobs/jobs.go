// Package jobs is a durable, Postgres-backed background job queue for gitstate.
//
// WHY: repo syncs used to run in detached goroutines that died on a server
// restart, stranding a bulk import partway (e.g. 6/105). This queue makes work
// DURABLE — every job is a row in the `jobs` table. In-process workers dequeue
// with SELECT … FOR UPDATE SKIP LOCKED, run the registered handler, and mark the
// row done/failed. A restart never strands work: stale 'running' rows (a worker
// that died holding the lock) are flipped back to 'pending' by RequeueStale, which
// runs on Start and on a periodic ticker. No Redis, no LISTEN/NOTIFY — polling is
// plenty at this scale.
//
// TENANCY: the worker processes jobs across ALL orgs, but the jobs table is
// org-RLS'd (FORCE RLS, org_isolation). So queue ops (dequeue + status updates)
// run on the BYPASSRLS admin pool (cfg.Admin.DatabaseURL / ADMIN_DATABASE_URL),
// which skips the policy and sees every org's rows. Each job's HANDLER then runs
// in that job's own org context via the normal app db.WithOrg(orgID, …), so the
// org-scoped work it does is RLS-correct. When ADMIN_DATABASE_URL is empty we fall
// back to the app pool — single-tenant dev still works because the dequeue tx SETs
// app.current_org per claim (so the org_isolation policy admits that org's rows).
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
)

// Handler runs one job. It receives the app DB (for org-scoped work via
// database.WithOrg), the job's org id, and its raw JSON payload. Returning a
// non-nil error causes the job to be retried (with exponential backoff) until
// max_attempts, after which it is marked failed.
type Handler func(ctx context.Context, database *db.DB, orgID string, payload json.RawMessage) error

// Queue is a durable Postgres-backed job queue with in-process workers.
type Queue struct {
	// admin is the pool used for ALL queue operations (dequeue + status updates).
	// It is the BYPASSRLS admin pool when ADMIN_DATABASE_URL is set (cross-org), or
	// the app pool as a single-tenant fallback. The dequeue tx SETs app.current_org
	// from each claimed row, so the fallback still satisfies org_isolation.
	admin *pgxpool.Pool
	// adminOwned is true when we opened `admin` ourselves (admin pool) and must
	// Close it; false when it aliases the app pool (which main.go owns).
	adminOwned bool
	// usingAdmin is true when admin is the BYPASSRLS pool (cross-org dequeue). When
	// false we are on the app-pool fallback and must SET app.current_org per claim.
	usingAdmin bool

	// db is the app DB used by handlers (and by Enqueue under RLS).
	db *db.DB

	registry map[string]Handler
	workers  int
	workerID string

	log *slog.Logger
}

// EnqueueOpts are optional knobs for a single Enqueue call. The zero value is a
// sensible default: priority 0, run immediately, no dedupe, max_attempts from the
// table default (5).
type EnqueueOpts struct {
	Priority    int           // higher runs first (default 0)
	RunAfter    time.Time     // earliest eligible time (zero = now)
	Delay       time.Duration // convenience: RunAfter = now()+Delay when RunAfter is zero
	DedupeKey   string        // at most one LIVE (pending|running) job per (org, key)
	MaxAttempts int           // 0 = use the table default (5)
}

// New builds a Queue: it opens the admin (BYPASSRLS) pool from
// cfg.Admin.DatabaseURL, falling back to the app pool when that is empty, and
// initializes an empty handler registry. Register handlers, then Start.
func New(database *db.DB, cfg *config.Config) (*Queue, error) {
	if database == nil {
		return nil, fmt.Errorf("jobs: New requires a non-nil db")
	}

	q := &Queue{
		db:       database,
		registry: map[string]Handler{},
		workers:  jobWorkers(cfg),
		workerID: workerID(),
		log:      slog.Default().With("component", "jobs"),
	}

	// Prefer the audited BYPASSRLS admin pool so the worker can dequeue across ALL
	// orgs (the jobs table is FORCE RLS'd). Fall back to the app pool for single-
	// tenant dev (the dequeue tx SETs app.current_org per claim so RLS still admits).
	adminURL := ""
	if cfg != nil {
		adminURL = cfg.Admin.DatabaseURL
	}
	if adminURL != "" {
		pool, err := db.NewPool(context.Background(), adminURL, 0)
		if err != nil {
			return nil, fmt.Errorf("jobs: open admin pool: %w", err)
		}
		q.admin = pool
		q.adminOwned = true
		q.usingAdmin = true
		q.log.Info("jobs queue using admin (BYPASSRLS) pool for cross-org dequeue", "workers", q.workers, "worker_id", q.workerID)
	} else {
		q.admin = database.Pool()
		q.adminOwned = false
		q.usingAdmin = false
		q.log.Warn("jobs queue: ADMIN_DATABASE_URL not set — falling back to app pool (single-tenant; dequeue SETs app.current_org per claim)",
			"workers", q.workers, "worker_id", q.workerID)
	}

	return q, nil
}

// Register binds a handler to a job kind. Call before Start. Re-registering a kind
// replaces the previous handler.
func (q *Queue) Register(kind string, h Handler) {
	q.registry[kind] = h
}

// Close releases the admin pool if the Queue owns it. Safe to call on shutdown.
func (q *Queue) Close() {
	if q != nil && q.adminOwned && q.admin != nil {
		q.admin.Close()
	}
}

// Enqueue inserts a pending job for orgID. payload is JSON-marshalled. When a
// DedupeKey is set, the partial-unique index (org_id, dedupe_key) WHERE live
// coalesces a duplicate live job via ON CONFLICT DO NOTHING — re-enqueuing the
// same work while a copy is still pending/running is a no-op.
//
// The INSERT runs on the admin pool. When the admin pool is BYPASSRLS the explicit
// org_id column is honored directly; on the app-pool fallback we wrap the insert in
// db.WithOrg so the org_isolation WITH CHECK passes. Either way the row's org_id =
// orgID.
func (q *Queue) Enqueue(ctx context.Context, orgID, kind string, payload any, opts EnqueueOpts) error {
	if orgID == "" {
		return fmt.Errorf("jobs: Enqueue requires an org id")
	}
	if kind == "" {
		return fmt.Errorf("jobs: Enqueue requires a kind")
	}

	raw, err := marshalPayload(payload)
	if err != nil {
		return fmt.Errorf("jobs: marshal payload: %w", err)
	}

	runAfter := opts.RunAfter
	if runAfter.IsZero() {
		runAfter = time.Now().Add(opts.Delay)
	}

	var dedupe *string
	if opts.DedupeKey != "" {
		dedupe = &opts.DedupeKey
	}
	maxAttempts := 0 // 0 → COALESCE to the table default
	if opts.MaxAttempts > 0 {
		maxAttempts = opts.MaxAttempts
	}

	const insertSQL = `
		INSERT INTO jobs (org_id, kind, payload, priority, run_after, dedupe_key, max_attempts)
		VALUES ($1, $2, $3, $4, $5, $6, COALESCE(NULLIF($7, 0), 5))
		ON CONFLICT (org_id, dedupe_key) WHERE dedupe_key IS NOT NULL AND status IN ('pending','running')
		DO NOTHING`

	args := []any{orgID, kind, raw, opts.Priority, runAfter, dedupe, maxAttempts}

	if q.usingAdmin {
		_, err = q.admin.Exec(ctx, insertSQL, args...)
		if err != nil {
			return fmt.Errorf("jobs: enqueue %s: %w", kind, err)
		}
		return nil
	}
	// App-pool fallback: insert under the org's RLS context so WITH CHECK passes.
	return q.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, insertSQL, args...)
		if e != nil {
			return fmt.Errorf("jobs: enqueue %s: %w", kind, e)
		}
		return nil
	})
}

// claimedJob is a single dequeued row.
type claimedJob struct {
	ID          string
	OrgID       string
	Kind        string
	Payload     json.RawMessage
	Attempts    int
	MaxAttempts int
}

// Start launches the worker goroutines and the stale-requeue ticker, then returns
// immediately. The caller controls lifetime via ctx: cancelling ctx stops every
// worker cleanly (after the in-flight job finishes). RequeueStale runs once up
// front (so a restart immediately reclaims jobs orphaned by the previous process)
// and then on a ticker.
func (q *Queue) Start(ctx context.Context) {
	// Restart-recovery: reclaim jobs orphaned by a previous (now-dead) process.
	if err := q.RequeueStale(ctx); err != nil {
		q.log.Error("jobs: initial RequeueStale failed", "err", err)
	}

	for i := 0; i < q.workers; i++ {
		go q.runWorker(ctx, i)
	}

	go q.runStaleTicker(ctx)

	q.log.Info("jobs queue started", "workers", q.workers)
}

// runStaleTicker periodically requeues stale running jobs (defense in depth beyond
// the one-shot on Start: covers a worker that died mid-run while the process kept
// going).
func (q *Queue) runStaleTicker(ctx context.Context) {
	t := time.NewTicker(staleSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := q.RequeueStale(ctx); err != nil {
				q.log.Error("jobs: periodic RequeueStale failed", "err", err)
			}
		}
	}
}

// runWorker is one worker loop: claim a job, run it, repeat; sleep when idle.
func (q *Queue) runWorker(ctx context.Context, n int) {
	log := q.log.With("worker", n)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		job, err := q.dequeue(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Error("jobs: dequeue failed", "err", err)
			if sleepCtx(ctx, pollInterval) {
				return
			}
			continue
		}
		if job == nil {
			// Nothing eligible — poll again shortly.
			if sleepCtx(ctx, pollInterval) {
				return
			}
			continue
		}

		q.process(ctx, log, job)
	}
}

// dequeueSelectSQL claims the single highest-priority eligible pending job. The
// FOR UPDATE SKIP LOCKED makes concurrent workers (and processes) never double-
// process: a row another worker has locked is skipped, not blocked on.
const dequeueSelectSQL = `
	SELECT id, org_id, kind, payload, attempts, max_attempts
	FROM jobs
	WHERE status = 'pending' AND run_after <= now()
	ORDER BY priority DESC, created_at
	LIMIT 1
	FOR UPDATE SKIP LOCKED`

const dequeueClaimSQL = `
	UPDATE jobs
	SET status = 'running', locked_at = now(), locked_by = $2, attempts = attempts + 1, updated_at = now()
	WHERE id = $1`

// dequeue atomically claims one job and marks it running with this worker's id
// (bumping attempts). Returns (nil, nil) when nothing is eligible.
//
// Admin (BYPASSRLS) pool: a single cross-org SELECT … FOR UPDATE SKIP LOCKED sees
// every org's rows.
//
// App-pool fallback: the jobs table is FORCE RLS'd, so a SELECT with no
// app.current_org set sees ZERO rows (org_isolation: org_id = current_org(), and
// current_org() is NULL). To keep single-tenant (and small multi-tenant) dev
// working without the admin role, we enumerate org ids from the non-RLS
// organizations table and try to claim a job within each org's RLS context until
// one yields. PREFER the admin pool in production — this fallback does one extra
// cheap query per idle poll.
func (q *Queue) dequeue(ctx context.Context) (*claimedJob, error) {
	if q.usingAdmin {
		return q.claimInOrg(ctx, "")
	}
	return q.dequeueFallback(ctx)
}

// claimInOrg runs the select+claim in one tx. When orgID is non-empty it first SETs
// app.current_org so the org_isolation policy admits that org's rows (used by the
// app-pool fallback); when empty it relies on the BYPASSRLS admin pool.
func (q *Queue) claimInOrg(ctx context.Context, orgID string) (*claimedJob, error) {
	tx, err := q.admin.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin dequeue tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if orgID != "" {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org', $1, true)", orgID); err != nil {
			return nil, fmt.Errorf("set app.current_org: %w", err)
		}
	}

	var job claimedJob
	row := tx.QueryRow(ctx, dequeueSelectSQL)
	if err := row.Scan(&job.ID, &job.OrgID, &job.Kind, &job.Payload, &job.Attempts, &job.MaxAttempts); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("select job: %w", err)
	}

	if _, err := tx.Exec(ctx, dequeueClaimSQL, job.ID, q.workerID); err != nil {
		return nil, fmt.Errorf("claim job: %w", err)
	}
	job.Attempts++ // reflect the bump we just committed

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit dequeue tx: %w", err)
	}
	return &job, nil
}

// dequeueFallback claims a job without the BYPASSRLS pool by trying each org's RLS
// context in turn (organizations is not RLS'd, so it is enumerable on the app role).
func (q *Queue) dequeueFallback(ctx context.Context) (*claimedJob, error) {
	rows, err := q.admin.Query(ctx, `SELECT id FROM organizations`)
	if err != nil {
		return nil, fmt.Errorf("list orgs for fallback dequeue: %w", err)
	}
	var orgIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan org id: %w", err)
		}
		orgIDs = append(orgIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate orgs: %w", err)
	}

	for _, orgID := range orgIDs {
		job, err := q.claimInOrg(ctx, orgID)
		if err != nil {
			return nil, err
		}
		if job != nil {
			return job, nil
		}
	}
	return nil, nil
}

// process runs the handler for a claimed job and records the outcome.
func (q *Queue) process(ctx context.Context, log *slog.Logger, job *claimedJob) {
	log = log.With("job_id", job.ID, "kind", job.Kind, "org_id", job.OrgID, "attempt", job.Attempts)

	h, ok := q.registry[job.Kind]
	if !ok {
		log.Error("jobs: no handler registered for kind")
		q.fail(ctx, job, fmt.Sprintf("no handler registered for kind %q", job.Kind))
		return
	}

	start := time.Now()
	err := q.runHandler(ctx, h, job)
	if err == nil {
		log.Info("jobs: done", "dur", time.Since(start).Round(time.Millisecond))
		q.markDone(ctx, job)
		return
	}

	log.Error("jobs: handler error", "err", err, "dur", time.Since(start).Round(time.Millisecond))
	if job.Attempts < job.MaxAttempts {
		q.retry(ctx, job, err)
	} else {
		q.fail(ctx, job, err.Error())
	}
}

// runHandler invokes the handler, converting a panic into an error so a single bad
// job cannot crash the worker.
func (q *Queue) runHandler(ctx context.Context, h Handler, job *claimedJob) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("handler panic: %v", r)
		}
	}()
	return h(ctx, q.db, job.OrgID, job.Payload)
}

// markDone flags the job complete. Uses context.Background so a shutdown mid-flight
// still records the terminal state (the handler already finished).
func (q *Queue) markDone(_ context.Context, job *claimedJob) {
	ctx, cancel := context.WithTimeout(context.Background(), statusUpdateTimeout)
	defer cancel()
	if err := q.execJob(ctx, job.OrgID,
		`UPDATE jobs SET status='done', last_error=NULL, locked_at=NULL, locked_by=NULL, updated_at=now() WHERE id=$1`,
		job.ID); err != nil {
		q.log.Error("jobs: mark done failed", "job_id", job.ID, "err", err)
	}
}

// retry re-queues a failed-but-retryable job with exponential backoff.
func (q *Queue) retry(_ context.Context, job *claimedJob, cause error) {
	ctx, cancel := context.WithTimeout(context.Background(), statusUpdateTimeout)
	defer cancel()
	delay := backoff(job.Attempts)
	if err := q.execJob(ctx, job.OrgID,
		`UPDATE jobs SET status='pending', run_after = now() + $2::interval, last_error=$3,
		     locked_at=NULL, locked_by=NULL, updated_at=now()
		 WHERE id=$1`,
		job.ID, intervalText(delay), truncErr(cause.Error())); err != nil {
		q.log.Error("jobs: retry update failed", "job_id", job.ID, "err", err)
	}
}

// fail marks the job permanently failed (attempts exhausted, or unrecoverable).
func (q *Queue) fail(_ context.Context, job *claimedJob, msg string) {
	ctx, cancel := context.WithTimeout(context.Background(), statusUpdateTimeout)
	defer cancel()
	if err := q.execJob(ctx, job.OrgID,
		`UPDATE jobs SET status='failed', last_error=$2, locked_at=NULL, locked_by=NULL, updated_at=now() WHERE id=$1`,
		job.ID, truncErr(msg)); err != nil {
		q.log.Error("jobs: mark failed update failed", "job_id", job.ID, "err", err)
	}
}

// execJob runs a terminal status UPDATE against a single job row. On the BYPASSRLS
// admin pool it runs directly; on the app-pool fallback it wraps the UPDATE in a tx
// that SETs app.current_org=orgID so the org_isolation USING clause matches the row
// (an unscoped UPDATE on the app role would match zero rows and silently no-op).
func (q *Queue) execJob(ctx context.Context, orgID, sql string, args ...any) error {
	if q.usingAdmin {
		_, err := q.admin.Exec(ctx, sql, args...)
		return err
	}
	tx, err := q.admin.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org', $1, true)", orgID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RequeueStale flips 'running' jobs whose lock is older than staleThreshold back to
// 'pending', so a worker (or whole process) that died holding the lock does not
// strand the job forever. THIS IS WHAT MAKES THE QUEUE RESTART-PROOF: on boot the
// previous process's in-flight jobs (still 'running', lock now stale) are reclaimed
// and re-run. It runs on Start and on a periodic ticker.
//
// Note: attempts is NOT decremented — a reclaimed job already consumed an attempt
// when it was first claimed, which is the correct accounting (a crash-looping job
// still eventually hits max_attempts and fails rather than retrying forever).
func (q *Queue) RequeueStale(ctx context.Context) error {
	const requeueSQL = `
		UPDATE jobs
		SET status='pending', locked_at=NULL, locked_by=NULL, updated_at=now()
		WHERE status='running' AND locked_at < now() - $1::interval`

	if q.usingAdmin {
		tag, err := q.admin.Exec(ctx, requeueSQL, intervalText(staleThreshold))
		if err != nil {
			return fmt.Errorf("jobs: requeue stale: %w", err)
		}
		if n := tag.RowsAffected(); n > 0 {
			q.log.Info("jobs: requeued stale running jobs (restart-recovery)", "count", n)
		}
		return nil
	}

	// App-pool fallback: the cross-org UPDATE is RLS-filtered, so sweep per org.
	rows, err := q.admin.Query(ctx, `SELECT id FROM organizations`)
	if err != nil {
		return fmt.Errorf("jobs: requeue stale: list orgs: %w", err)
	}
	var orgIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("jobs: requeue stale: scan org id: %w", err)
		}
		orgIDs = append(orgIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("jobs: requeue stale: iterate orgs: %w", err)
	}

	var total int64
	for _, orgID := range orgIDs {
		tx, err := q.admin.Begin(ctx)
		if err != nil {
			return fmt.Errorf("jobs: requeue stale: begin: %w", err)
		}
		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org', $1, true)", orgID); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("jobs: requeue stale: set org: %w", err)
		}
		tag, err := tx.Exec(ctx, requeueSQL, intervalText(staleThreshold))
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("jobs: requeue stale: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("jobs: requeue stale: commit: %w", err)
		}
		total += tag.RowsAffected()
	}
	if total > 0 {
		q.log.Info("jobs: requeued stale running jobs (restart-recovery)", "count", total)
	}
	return nil
}

// ── tuning constants ───────────────────────────────────────────────────────────

const (
	// pollInterval is how long an idle worker sleeps before polling again.
	pollInterval = 3 * time.Second
	// staleThreshold is how long a 'running' lock may be held before it is
	// considered orphaned (a dead worker/process) and the job is requeued.
	staleThreshold = 15 * time.Minute
	// staleSweepInterval is how often the periodic stale-requeue ticker fires.
	staleSweepInterval = 5 * time.Minute
	// statusUpdateTimeout bounds the terminal status writes (done/failed/retry),
	// which run on context.Background so a shutdown still records the outcome.
	statusUpdateTimeout = 10 * time.Second
	// defaultWorkers is the worker count when none is configured.
	defaultWorkers = 4
	// backoffBase / backoffCap shape the exponential retry backoff.
	backoffBase = 5 * time.Second
	backoffCap  = 30 * time.Minute
)

// backoff returns the retry delay for the given attempt number (1-based): roughly
// base * 2^(attempt-1), capped at backoffCap.
func backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := backoffBase
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= backoffCap {
			return backoffCap
		}
	}
	if d > backoffCap {
		return backoffCap
	}
	return d
}

// ── helpers ────────────────────────────────────────────────────────────────────

// marshalPayload normalizes any payload to json.RawMessage. A nil payload becomes
// the empty object so the NOT NULL jsonb column is satisfied.
func marshalPayload(payload any) (json.RawMessage, error) {
	if payload == nil {
		return json.RawMessage(`{}`), nil
	}
	if raw, ok := payload.(json.RawMessage); ok {
		if len(raw) == 0 {
			return json.RawMessage(`{}`), nil
		}
		return raw, nil
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// intervalText renders a Go duration as a Postgres interval literal (seconds).
func intervalText(d time.Duration) string {
	return fmt.Sprintf("%d seconds", int64(d.Seconds()))
}

// truncErr caps an error message so a pathological error can't bloat the row.
func truncErr(s string) string {
	const max = 4000
	if len(s) > max {
		return s[:max]
	}
	return s
}

// jobWorkers resolves the worker count (currently a fixed default; kept a function
// so a future config field is a one-line change).
func jobWorkers(_ *config.Config) int {
	return defaultWorkers
}

// workerID returns a stable-ish id for this process's workers, used for locked_by
// (diagnostics + stale-lock attribution). hostname+pid is enough to tell processes
// apart.
func workerID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "host"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}

// sleepCtx sleeps for d or until ctx is cancelled. Returns true if ctx was
// cancelled (the caller should stop).
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return true
	case <-t.C:
		return false
	}
}
