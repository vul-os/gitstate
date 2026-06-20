// Package webhooks — dedupe.go
// Replay protection for inbound deliveries. A captured, validly-signed delivery
// could otherwise be replayed (re-POSTed) and re-applied. We keep a bounded,
// TTL'd, concurrency-safe set of recently-seen delivery IDs keyed PER ORG; a
// repeat within the window is acknowledged (200) but skipped.
//
// In-memory is sufficient for the single-binary deploy: the worst case after a
// restart is that a replay landing in the same short window as the original is
// re-applied, and every ingest path is idempotent (UpsertCommit / UpsertPR /
// UpsertIssue, and deployments keyed on a stable ExternalID) so even that is
// harmless to data — this guard exists to stop spurious re-counting/log noise.
package webhooks

import (
	"sync"
	"time"
)

// DefaultDedupeTTL bounds how long a delivery ID is remembered.
const DefaultDedupeTTL = 10 * time.Minute

// defaultDedupeMax caps the number of remembered delivery IDs (LRU-ish: the
// oldest are evicted when the cap is exceeded). Sized for a busy single binary.
const defaultDedupeMax = 8192

// DeliveryDeduper remembers recently-seen (org, deliveryID) pairs and reports
// repeats. Safe for concurrent use.
type DeliveryDeduper struct {
	mu   sync.Mutex
	ttl  time.Duration
	max  int
	seen map[string]time.Time // key → expiry
	now  func() time.Time     // injectable clock (tests)
}

// NewDeliveryDeduper builds a deduper with the given TTL and size bound. A
// non-positive ttl/max falls back to the package defaults.
func NewDeliveryDeduper(ttl time.Duration, max int) *DeliveryDeduper {
	if ttl <= 0 {
		ttl = DefaultDedupeTTL
	}
	if max <= 0 {
		max = defaultDedupeMax
	}
	return &DeliveryDeduper{
		ttl:  ttl,
		max:  max,
		seen: make(map[string]time.Time),
		now:  time.Now,
	}
}

// Seen records (orgID, deliveryID) and reports whether it was already present
// (and not yet expired). An empty deliveryID is never deduped — Seen returns
// false and stores nothing, so deliveries without an ID always process.
func (d *DeliveryDeduper) Seen(orgID, deliveryID string) bool {
	if deliveryID == "" {
		return false
	}
	key := orgID + "\x00" + deliveryID

	d.mu.Lock()
	defer d.mu.Unlock()

	now := d.now()
	if exp, ok := d.seen[key]; ok && exp.After(now) {
		return true // fresh duplicate
	}

	// Not present (or expired): (re)record it, evicting if we're at the cap.
	if len(d.seen) >= d.max {
		d.evictLocked(now)
	}
	d.seen[key] = now.Add(d.ttl)
	return false
}

// evictLocked drops expired entries; if that didn't free space, it drops the
// soonest-to-expire entries until we're under the cap. Caller holds d.mu.
func (d *DeliveryDeduper) evictLocked(now time.Time) {
	for k, exp := range d.seen {
		if !exp.After(now) {
			delete(d.seen, k)
		}
	}
	if len(d.seen) < d.max {
		return
	}
	// Still full of live entries: evict ~10% with the earliest expiry.
	target := d.max - d.max/10
	for len(d.seen) > target {
		var oldestKey string
		var oldestExp time.Time
		first := true
		for k, exp := range d.seen {
			if first || exp.Before(oldestExp) {
				oldestKey, oldestExp, first = k, exp, false
			}
		}
		if first { // map emptied concurrently — nothing to evict
			return
		}
		delete(d.seen, oldestKey)
	}
}
