// internal/billing/scheduler.go — the billing LIFECYCLE engine.
//
// The Scheduler drives the whole monthly cycle + dunning machine under an
// INJECTABLE Clock (never time.Now directly), so simulated-time tests are
// deterministic. It reuses the existing GenerateInvoice (Service) for line-item
// generation and a pluggable Charger for the actual money movement (the EE
// Paystack service implements Charger; tests inject a fake).
//
// Integrity closures enforced here:
//   #4 builder count = derived truth — recomputed at invoice time inside GenerateInvoice
//      (counts org members with ≥1 commit/PR in the period via the commits/PR tables).
//   #5 FX locked at charge — the Charger calls SetInvoiceCharge which stamps fx_rate+id.
//   #6 proration — Prorate() computes the mid-cycle plan-change delta.
//   #7 dunning — RunDunning escalates retry(1/3/5/7) → suspend(7) → cancel+purge(14).
//   #8 idempotency — RunBillingCycle skips an org whose period already has a non-void
//      invoice; retries charge the SAME outstanding invoice, never a new one.
package billing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/store"
)

// ── Dunning schedule (closure #7) ─────────────────────────────────────────────
//
// Day offsets are measured from the FIRST charge failure (the day the org entered
// past_due). Retries fire on days 1/3/5/7; suspension at day 7; cancel+purge at
// day 14. These are the canonical constants the lifecycle and tests share.
const (
	// DunningRetryDays are the day-offsets (from entry into past_due) at which a
	// charge is retried.
	dunningRetryDay1 = 1
	dunningRetryDay2 = 3
	dunningRetryDay3 = 5
	dunningRetryDay4 = 7

	// DunningSuspendDay is the day-offset at which a still-unpaid org is suspended
	// (writes/sync blocked, data kept).
	DunningSuspendDay = 7

	// DunningCancelDay is the day-offset at which a still-unpaid org is canceled and
	// its work-product data is PURGED.
	DunningCancelDay = 14
)

// dunningRetryOffsets are the retry day-offsets in order. attempt N (1-indexed)
// schedules its retry at dunningRetryOffsets[N-1] days from the past_due entry.
var dunningRetryOffsets = []int{dunningRetryDay1, dunningRetryDay2, dunningRetryDay3, dunningRetryDay4}

// ── Charger (the money-movement seam) ─────────────────────────────────────────

// ChargeResult is what a Charger returns on success.
type ChargeResult struct {
	ZARCents    int
	FXRate      float64
	FXRateID    string
	PaystackRef string
}

// InvoiceEmailHook, when set (by main.go), is called best-effort after a NEW
// invoice is issued so the PDF is emailed to org owners. nil ⇒ no email.
var InvoiceEmailHook func(ctx context.Context, orgID, invoiceID string) error

// Charger performs the actual ZAR charge for an outstanding invoice. The EE
// Paystack service implements this; tests inject a fake. It MUST be idempotent
// per invoice — a retried/replayed charge for an already-paid invoice should
// return success without moving money twice (closure #8). Implementations stamp
// the FX rate onto the invoice (closure #5) via store.SetInvoiceCharge.
//
// A non-nil error means the charge FAILED (card declined, provider down) and the
// org should be put into / kept in dunning. A nil error means the invoice is paid.
type Charger interface {
	Charge(ctx context.Context, orgID, invoiceID string, usdCents int) (*ChargeResult, error)
}

// ── Scheduler ─────────────────────────────────────────────────────────────────

// Scheduler runs the monthly billing cycle and the dunning machine.
type Scheduler struct {
	svc     *Service
	db      *db.DB
	cfg     *config.Config
	charger Charger
	clock   Clock

	// xpool is the cross-org pool used for the "due for billing / due for retry"
	// scans (subscriptions is FORCE RLS'd, so these scans need BYPASSRLS). It is the
	// admin pool when ADMIN_DATABASE_URL is set, else the app pool (single-tenant).
	xpool      *pgxpool.Pool
	xpoolOwned bool

	log *slog.Logger
}

// NewScheduler builds a Scheduler. charger is the money seam (EE Paystack in prod,
// a fake in tests). clock is the injectable time source (SystemClock in prod).
//
// It opens the BYPASSRLS admin pool for cross-org scans when ADMIN_DATABASE_URL is
// configured, falling back to the app pool otherwise (mirrors the jobs queue).
// Call Close() to release a self-owned admin pool.
func NewScheduler(database *db.DB, cfg *config.Config, charger Charger, clock Clock) (*Scheduler, error) {
	if database == nil {
		return nil, fmt.Errorf("billing: NewScheduler requires a non-nil db")
	}
	if clock == nil {
		clock = SystemClock{}
	}
	s := &Scheduler{
		svc:     New(database, cfg),
		db:      database,
		cfg:     cfg,
		charger: charger,
		clock:   clock,
		log:     slog.Default().With("component", "billing-scheduler"),
	}

	adminURL := ""
	if cfg != nil {
		adminURL = cfg.Admin.DatabaseURL
	}
	if adminURL != "" {
		pool, err := db.NewPool(context.Background(), adminURL, 0)
		if err != nil {
			return nil, fmt.Errorf("billing: scheduler open admin pool: %w", err)
		}
		s.xpool = pool
		s.xpoolOwned = true
	} else {
		s.xpool = database.Pool()
		s.xpoolOwned = false
	}
	return s, nil
}

// Close releases the admin pool if the Scheduler owns it.
func (s *Scheduler) Close() {
	if s.xpoolOwned && s.xpool != nil {
		s.xpool.Close()
	}
}

// periodLength is one calendar month (charged monthly).
func nextPeriodEnd(start time.Time) time.Time { return start.AddDate(0, 1, 0) }

// ── RunBillingCycle (closure #4/#5/#8) ────────────────────────────────────────

// RunBillingCycle processes every active org whose current period has ended at or
// before `now`:
//
//  1. idempotency guard (#8): if a non-void invoice already exists for the period,
//     skip (a prior run already billed it).
//  2. recompute builders + generate the USD invoice (#4) via GenerateInvoice.
//  3. charge via the Charger (FX stamped at charge time, #5).
//     success ⇒ subscription active + period advanced.
//     failure ⇒ past_due + first retry scheduled (#7 handoff to RunDunning).
//
// `now` comes from the caller's Clock. Returns the count of orgs processed and the
// first error encountered (per-org errors are logged and do not abort the sweep).
func (s *Scheduler) RunBillingCycle(ctx context.Context, now time.Time) (processed int, firstErr error) {
	due, err := store.ListSubscriptionsDueForBilling(ctx, s.xpool, now)
	if err != nil {
		return 0, fmt.Errorf("billing: list due subscriptions: %w", err)
	}

	for _, sub := range due {
		if err := s.billOrg(ctx, sub, now); err != nil {
			s.log.ErrorContext(ctx, "billing cycle: org failed", "org_id", sub.OrgID, "error", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		processed++
	}
	return processed, firstErr
}

// billOrg bills a single org for its just-ended period.
func (s *Scheduler) billOrg(ctx context.Context, sub store.SubscriptionLifecycle, now time.Time) error {
	if sub.CurrentPeriodEnd == nil {
		return nil
	}
	periodEnd := *sub.CurrentPeriodEnd
	periodStart := periodEnd.AddDate(0, -1, 0)
	if sub.CurrentPeriodStart != nil {
		periodStart = *sub.CurrentPeriodStart
	}

	// (#8) idempotency: did a prior run already produce an invoice for this period?
	var existing bool
	if err := s.db.WithOrg(ctx, sub.OrgID, func(tx pgx.Tx) error {
		var e error
		existing, e = store.InvoiceExistsForPeriod(ctx, tx, sub.OrgID, periodStart, periodEnd)
		return e
	}); err != nil {
		return fmt.Errorf("idempotency check: %w", err)
	}

	var invoiceID string
	var usdCents int
	if existing {
		// Reuse the outstanding invoice (don't mint a second).
		var inv *store.Invoice
		if err := s.db.WithOrg(ctx, sub.OrgID, func(tx pgx.Tx) error {
			var e error
			inv, e = store.GetOpenInvoiceForPeriod(ctx, tx, sub.OrgID, periodStart, periodEnd)
			return e
		}); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				// A non-void invoice exists but it's already paid → nothing to do but
				// advance the period so we don't re-scan it forever.
				return s.advancePeriodOnly(ctx, sub.OrgID, periodEnd, now)
			}
			return fmt.Errorf("load outstanding invoice: %w", err)
		}
		invoiceID, usdCents = inv.ID, inv.USDCents
	} else {
		// (#4) recompute builders + generate the USD invoice for the period.
		inv, err := s.svc.GenerateInvoice(ctx, sub.OrgID, periodStart, periodEnd)
		if err != nil {
			return fmt.Errorf("generate invoice: %w", err)
		}
		invoiceID, usdCents = inv.ID, inv.USDCents

		// Email the freshly-issued invoice (PDF) to the org owners. Best-effort +
		// async so delivery never blocks or fails the billing cycle. The hook is
		// set from main.go (billing stays decoupled from invoicedelivery/email).
		if InvoiceEmailHook != nil {
			oid, iid := sub.OrgID, invoiceID
			go func() {
				if e := InvoiceEmailHook(context.WithoutCancel(ctx), oid, iid); e != nil {
					slog.Warn("billing: email invoice to owners failed", "org_id", oid, "invoice_id", iid, "err", e)
				}
			}()
		}
	}

	// $0 invoices (e.g. free tier, zero builders) need no charge: mark paid-equivalent
	// by advancing the period immediately.
	if usdCents == 0 {
		return s.advancePeriodOnly(ctx, sub.OrgID, periodEnd, now)
	}

	// (#5) charge via the Charger; it stamps FX at charge time.
	if _, err := s.charger.Charge(ctx, sub.OrgID, invoiceID, usdCents); err != nil {
		// Charge failed → enter dunning (#7).
		return s.enterDunning(ctx, sub.OrgID, now)
	}

	// Success → active + advance period.
	newStart := periodEnd
	newEnd := nextPeriodEnd(newStart)
	return s.db.WithOrg(ctx, sub.OrgID, func(tx pgx.Tx) error {
		if err := store.MarkInvoicePaid(ctx, tx, invoiceID, now); err != nil && !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("mark invoice paid: %w", err)
		}
		return store.MarkBillingSuccess(ctx, tx, sub.OrgID, newStart, newEnd)
	})
}

// advancePeriodOnly advances the period window and keeps the subscription active
// without charging (used for $0 invoices / already-paid periods).
func (s *Scheduler) advancePeriodOnly(ctx context.Context, orgID string, periodEnd, now time.Time) error {
	newStart := periodEnd
	newEnd := nextPeriodEnd(newStart)
	return s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		return store.MarkBillingSuccess(ctx, tx, orgID, newStart, newEnd)
	})
}

// enterDunning puts an org into past_due and schedules the first retry (day 1).
func (s *Scheduler) enterDunning(ctx context.Context, orgID string, now time.Time) error {
	firstRetry := now.AddDate(0, 0, dunningRetryOffsets[0])
	return s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		return store.EnterPastDue(ctx, tx, orgID, 1, firstRetry)
	})
}

// ── RunDunning (closure #7) ───────────────────────────────────────────────────

// RunDunning processes every past_due/suspended subscription whose next_retry_at is
// due at or before `now`:
//
//   - retry the outstanding charge; on success → active + period advanced (recovery).
//   - on failure escalate by elapsed days since first failure:
//       < day 7  → schedule the next retry (1/3/5/7).
//       ≥ day 7  → SUSPEND (writes/sync blocked, data kept), keep retrying.
//       ≥ day 14 → CANCEL + PURGE org data.
//
// The elapsed-day clock is derived from the past_due entry, reconstructed from
// dunning_attempts (attempt 1 was scheduled at day-1, etc.) — we anchor on the
// scheduled retry offsets so simulated time lines up exactly.
func (s *Scheduler) RunDunning(ctx context.Context, now time.Time) (processed int, firstErr error) {
	due, err := store.ListSubscriptionsDueForRetry(ctx, s.xpool, now)
	if err != nil {
		return 0, fmt.Errorf("billing: list retry subscriptions: %w", err)
	}

	for _, sub := range due {
		if err := s.dunOrg(ctx, sub, now); err != nil {
			s.log.ErrorContext(ctx, "dunning: org failed", "org_id", sub.OrgID, "error", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		processed++
	}
	return processed, firstErr
}

// dunOrg runs one dunning step for a past_due/suspended org.
func (s *Scheduler) dunOrg(ctx context.Context, sub store.SubscriptionLifecycle, now time.Time) error {
	if sub.CurrentPeriodEnd == nil {
		return nil
	}
	periodEnd := *sub.CurrentPeriodEnd
	periodStart := periodEnd.AddDate(0, -1, 0)
	if sub.CurrentPeriodStart != nil {
		periodStart = *sub.CurrentPeriodStart
	}

	// elapsedDays since the first failure = the day-offset of the retry that JUST
	// came due. dunning_attempts counts retries scheduled so far; the attempt that
	// is now due corresponds to dunningRetryOffsets[attempts-1].
	elapsedDays := currentDunningDay(sub.DunningAttempts)

	// (#14) hard stop: at/after the cancel day, cancel + PURGE regardless of charge.
	if elapsedDays >= DunningCancelDay {
		return s.cancelAndPurge(ctx, sub.OrgID, now)
	}

	// Attempt the charge against the SAME outstanding invoice (#8).
	var inv *store.Invoice
	if err := s.db.WithOrg(ctx, sub.OrgID, func(tx pgx.Tx) error {
		var e error
		inv, e = store.GetOpenInvoiceForPeriod(ctx, tx, sub.OrgID, periodStart, periodEnd)
		return e
	}); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// No outstanding invoice (already paid out-of-band) → recover.
			return s.recover(ctx, sub.OrgID, periodEnd, now)
		}
		return fmt.Errorf("dunning load invoice: %w", err)
	}

	if _, chargeErr := s.charger.Charge(ctx, sub.OrgID, inv.ID, inv.USDCents); chargeErr == nil {
		// Recovered mid-dunning → back to active + advance period.
		return s.db.WithOrg(ctx, sub.OrgID, func(tx pgx.Tx) error {
			if err := store.MarkInvoicePaid(ctx, tx, inv.ID, now); err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			return store.MarkBillingSuccess(ctx, tx, sub.OrgID, periodEnd, nextPeriodEnd(periodEnd))
		})
	}

	// Charge failed again → escalate.
	nextAttempts := sub.DunningAttempts + 1
	nextDay := currentDunningDay(nextAttempts)
	// Anchor "first failure" so the next retry lands at the right simulated day.
	firstFailure := now.AddDate(0, 0, -elapsedDays)
	nextRetry := firstFailure.AddDate(0, 0, nextDay)
	if nextDay <= elapsedDays {
		// No further scheduled retry before cancel — push to the cancel day.
		nextRetry = firstFailure.AddDate(0, 0, DunningCancelDay)
	}

	if elapsedDays >= DunningSuspendDay && sub.BillingStatus != store.BillingSuspended {
		// Day-7: suspend (block writes/sync, keep data), keep dunning toward cancel.
		suspendRetry := firstFailure.AddDate(0, 0, DunningCancelDay)
		return s.db.WithOrg(ctx, sub.OrgID, func(tx pgx.Tx) error {
			return store.Suspend(ctx, tx, sub.OrgID, now, nextAttempts, suspendRetry)
		})
	}

	if sub.BillingStatus == store.BillingSuspended {
		// Already suspended and not yet at cancel day → keep waiting for cancel day.
		suspendRetry := firstFailure.AddDate(0, 0, DunningCancelDay)
		return s.db.WithOrg(ctx, sub.OrgID, func(tx pgx.Tx) error {
			return store.ScheduleNextRetry(ctx, tx, sub.OrgID, nextAttempts, suspendRetry)
		})
	}

	// Still in the retry window (< day 7) → schedule the next retry.
	return s.db.WithOrg(ctx, sub.OrgID, func(tx pgx.Tx) error {
		return store.ScheduleNextRetry(ctx, tx, sub.OrgID, nextAttempts, nextRetry)
	})
}

// currentDunningDay maps a dunning attempt count to the day-offset (from first
// failure) of that attempt. attempts==1 → day 1, 2→3, 3→5, 4→7, ≥5 → cancel day.
func currentDunningDay(attempts int) int {
	if attempts <= 0 {
		return 0
	}
	if attempts <= len(dunningRetryOffsets) {
		return dunningRetryOffsets[attempts-1]
	}
	return DunningCancelDay
}

// recover returns a (no-longer-outstanding) org to active and advances its period.
func (s *Scheduler) recover(ctx context.Context, orgID string, periodEnd, now time.Time) error {
	return s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		return store.MarkBillingSuccess(ctx, tx, orgID, periodEnd, nextPeriodEnd(periodEnd))
	})
}

// cancelAndPurge cancels the subscription and PURGES the org's work-product data
// (closure #7 day-14). Both happen in one org-scoped tx so a partial purge can't
// leave a "canceled but data retained" state.
func (s *Scheduler) cancelAndPurge(ctx context.Context, orgID string, now time.Time) error {
	return s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		if err := store.Cancel(ctx, tx, orgID, now); err != nil {
			return fmt.Errorf("cancel: %w", err)
		}
		purged, err := store.PurgeOrgData(ctx, tx, orgID)
		if err != nil {
			return fmt.Errorf("purge: %w", err)
		}
		s.log.InfoContext(ctx, "dunning: canceled + purged org data", "org_id", orgID, "rows_purged", purged)
		return nil
	})
}

// ── Proration (closure #6) ────────────────────────────────────────────────────

// ProrationResult is the outcome of a mid-cycle plan change.
type ProrationResult struct {
	// DeltaUSDCents is the prorated amount to charge (positive, an upgrade) or
	// credit (negative, a downgrade) for the remainder of the current period.
	DeltaUSDCents int
	// RemainingFraction is the fraction of the period left at the change instant.
	RemainingFraction float64
}

// Prorate computes the prorated delta when an org changes from oldPerBuilderCents
// to newPerBuilderCents at `now`, for `builders` billable builders, within the
// period [periodStart, periodEnd] (closure #6).
//
// delta = (newPrice − oldPrice) × builders × (remaining period / full period).
// Upgrades yield a positive charge; downgrades a negative credit. The result is
// rounded to the nearest cent.
func Prorate(oldPerBuilderCents, newPerBuilderCents, builders int, periodStart, periodEnd, now time.Time) ProrationResult {
	full := periodEnd.Sub(periodStart)
	if full <= 0 {
		return ProrationResult{}
	}
	remaining := periodEnd.Sub(now)
	if remaining < 0 {
		remaining = 0
	}
	if remaining > full {
		remaining = full
	}
	frac := float64(remaining) / float64(full)

	perBuilderDelta := newPerBuilderCents - oldPerBuilderCents
	deltaFloat := float64(perBuilderDelta*builders) * frac
	// Round half away from zero so a +12.5 → 13 and −12.5 → −13.
	delta := int(deltaFloat + signHalf(deltaFloat))

	return ProrationResult{DeltaUSDCents: delta, RemainingFraction: frac}
}

// signHalf returns +0.5 for non-negative x and −0.5 for negative x (for rounding
// half away from zero).
func signHalf(x float64) float64 {
	if x < 0 {
		return -0.5
	}
	return 0.5
}

// ── Ticker loop ───────────────────────────────────────────────────────────────

// Run runs one full sweep (billing cycle + dunning) at the clock's current time.
// Exposed so a ticker (or a test) can invoke a single tick.
func (s *Scheduler) Run(ctx context.Context) {
	now := s.clock.Now()
	if _, err := s.RunBillingCycle(ctx, now); err != nil {
		s.log.ErrorContext(ctx, "billing cycle sweep error", "error", err)
	}
	if _, err := s.RunDunning(ctx, now); err != nil {
		s.log.ErrorContext(ctx, "dunning sweep error", "error", err)
	}
}

// StartLoop runs the hourly ticker loop until ctx is cancelled. Production uses
// SystemClock; the loop calls Run each tick. This is the goroutine the package's
// StartBillingScheduler wires up.
func (s *Scheduler) StartLoop(ctx context.Context) {
	const tick = time.Hour
	go func() {
		s.log.InfoContext(ctx, "billing scheduler loop started", "tick", tick)
		// Prime once on boot so a restart catches up immediately.
		s.Run(ctx)
		t := time.NewTicker(tick)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				s.log.InfoContext(ctx, "billing scheduler loop stopped")
				s.Close()
				return
			case <-t.C:
				s.Run(ctx)
			}
		}
	}()
}
