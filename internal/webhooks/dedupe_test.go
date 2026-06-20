package webhooks

import (
	"testing"
	"time"
)

func TestDeliveryDeduperBasic(t *testing.T) {
	d := NewDeliveryDeduper(time.Minute, 100)

	if d.Seen("org1", "abc") {
		t.Fatal("first sight should not be a duplicate")
	}
	if !d.Seen("org1", "abc") {
		t.Fatal("repeat within window should be a duplicate")
	}
	// Different org, same id → not a duplicate (per-org keying).
	if d.Seen("org2", "abc") {
		t.Fatal("same id in a different org should not be a duplicate")
	}
	// Empty id is never deduped.
	if d.Seen("org1", "") || d.Seen("org1", "") {
		t.Fatal("empty delivery id must never dedupe")
	}
}

func TestDeliveryDeduperExpiry(t *testing.T) {
	d := NewDeliveryDeduper(time.Minute, 100)
	now := time.Unix(1_000_000, 0)
	d.now = func() time.Time { return now }

	if d.Seen("org", "x") {
		t.Fatal("first sight not a duplicate")
	}
	if !d.Seen("org", "x") {
		t.Fatal("immediate repeat is a duplicate")
	}
	// Advance past the TTL → the entry should have expired.
	now = now.Add(2 * time.Minute)
	if d.Seen("org", "x") {
		t.Fatal("after TTL the id should be forgotten (not a duplicate)")
	}
}

func TestDeliveryDeduperSizeBound(t *testing.T) {
	const max = 50
	d := NewDeliveryDeduper(time.Hour, max)
	for i := 0; i < max*4; i++ {
		d.Seen("org", string(rune('A'+i%26))+time.Duration(i).String())
	}
	d.mu.Lock()
	n := len(d.seen)
	d.mu.Unlock()
	if n > max {
		t.Fatalf("deduper exceeded size bound: %d > %d", n, max)
	}
}
