// Package analytics — pure unit tests.
// These exercise the DB-free helper functions (averages, bucketing, date-range
// defaulting, filter parsing, day parsing) and always run — no DATABASE_URL or
// Postgres required.
package analytics

import (
	"math"
	"testing"
	"time"
)

func TestDeriveAverages(t *testing.T) {
	tests := []struct {
		name                                      string
		commits, activeDays, contributors         int
		additions, deletions                      int64
		wantPerDay, wantPerContrib, wantPerCommit float64
	}{
		{
			name: "typical", commits: 100, activeDays: 20, contributors: 5,
			additions: 800, deletions: 200,
			wantPerDay: 5, wantPerContrib: 20, wantPerCommit: 10,
		},
		{
			name: "zero active days → no div-by-zero", commits: 10, activeDays: 0, contributors: 2,
			additions: 5, deletions: 5,
			wantPerDay: 0, wantPerContrib: 5, wantPerCommit: 1,
		},
		{
			name: "zero contributors", commits: 10, activeDays: 5, contributors: 0,
			additions: 0, deletions: 0,
			wantPerDay: 2, wantPerContrib: 0, wantPerCommit: 0,
		},
		{
			name: "zero commits → lines/commit is 0", commits: 0, activeDays: 0, contributors: 0,
			additions: 100, deletions: 50,
			wantPerDay: 0, wantPerContrib: 0, wantPerCommit: 0,
		},
		{
			name: "fractional", commits: 7, activeDays: 2, contributors: 3,
			additions: 10, deletions: 4,
			wantPerDay: 3.5, wantPerContrib: 7.0 / 3.0, wantPerCommit: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveAverages(tt.commits, tt.activeDays, tt.contributors, tt.additions, tt.deletions)
			if !almostEqual(got.CommitsPerActiveDay, tt.wantPerDay) {
				t.Errorf("CommitsPerActiveDay = %v, want %v", got.CommitsPerActiveDay, tt.wantPerDay)
			}
			if !almostEqual(got.CommitsPerContributor, tt.wantPerContrib) {
				t.Errorf("CommitsPerContributor = %v, want %v", got.CommitsPerContributor, tt.wantPerContrib)
			}
			if !almostEqual(got.LinesPerCommit, tt.wantPerCommit) {
				t.Errorf("LinesPerCommit = %v, want %v", got.LinesPerCommit, tt.wantPerCommit)
			}
		})
	}
}

func TestNormalizeBucket(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"day", "day"},
		{"week", "week"},
		{"Week", "week"},
		{" WEEK ", "week"},
		{"month", "month"}, // month bucketing for wide ranges
		{"Month", "month"},
		{" MONTH ", "month"},
		{"", "day"}, // empty → day
		{"garbage", "day"},
	}
	for _, tt := range tests {
		if got := NormalizeBucket(tt.in); got != tt.want {
			t.Errorf("NormalizeBucket(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseFilterDefaulting(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		in        FilterInput
		wantFrom  time.Time
		wantTo    time.Time
		wantRepo  string
		wantAuth  string
		wantError bool
	}{
		{
			name:     "no bounds → all time (floor), not the 9-month default",
			in:       FilterInput{},
			wantFrom: allTimeFloor, wantTo: now,
		},
		{
			name:     "only from → to defaults to now",
			in:       FilterInput{From: "2026-01-01"},
			wantFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), wantTo: now,
		},
		{
			name:     "only to → from defaults to 9mo before to",
			in:       FilterInput{To: "2026-06-01"},
			wantFrom: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC).AddDate(0, -DefaultRangeMonths, 0),
			wantTo:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "both bounds preserved",
			in:       FilterInput{From: "2026-01-01", To: "2026-03-01"},
			wantFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			wantTo:   time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "inverted bounds get swapped",
			in:       FilterInput{From: "2026-03-01", To: "2026-01-01"},
			wantFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			wantTo:   time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "RFC3339 timestamps accepted",
			in:       FilterInput{From: "2026-01-01T08:30:00Z", To: "2026-02-01T00:00:00Z"},
			wantFrom: time.Date(2026, 1, 1, 8, 30, 0, 0, time.UTC),
			wantTo:   time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "repo + author trimmed and passed through",
			in:       FilterInput{RepoID: "  repo-123 ", Author: " jane@work "},
			wantFrom: allTimeFloor, wantTo: now,
			wantRepo: "repo-123", wantAuth: "jane@work",
		},
		{
			name:      "bad from date → error",
			in:        FilterInput{From: "not-a-date"},
			wantError: true,
		},
		{
			name:      "bad to date → error",
			in:        FilterInput{To: "2026/01/01"},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseFilter(tt.in, now)
			if tt.wantError {
				if err == nil {
					t.Fatalf("expected error, got nil (filter=%+v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.From.Equal(tt.wantFrom) {
				t.Errorf("From = %v, want %v", got.From, tt.wantFrom)
			}
			if !got.To.Equal(tt.wantTo) {
				t.Errorf("To = %v, want %v", got.To, tt.wantTo)
			}
			if got.RepoID != tt.wantRepo {
				t.Errorf("RepoID = %q, want %q", got.RepoID, tt.wantRepo)
			}
			if got.Author != tt.wantAuth {
				t.Errorf("Author = %q, want %q", got.Author, tt.wantAuth)
			}
			// Invariant: from must never be after to once defaulted.
			if got.From.After(got.To) {
				t.Errorf("invariant violated: From %v after To %v", got.From, got.To)
			}
		})
	}
}

func TestParseDay(t *testing.T) {
	tests := []struct {
		in        string
		want      time.Time
		wantError bool
	}{
		{in: "2026-06-18", want: time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)},
		{in: " 2026-01-01 ", want: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{in: "2026-13-01", wantError: true}, // invalid month
		{in: "garbage", wantError: true},
		{in: "", wantError: true},
		{in: "2026-06-18T00:00:00Z", wantError: true}, // timestamp not accepted here
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseDay(tt.in)
			if tt.wantError {
				if err == nil {
					t.Fatalf("expected error for %q, got %v", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.in, err)
			}
			if !got.Equal(tt.want) {
				t.Errorf("ParseDay(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestApplyDefaultsIsPure(t *testing.T) {
	// applyDefaults must not mutate non-zero bounds.
	now := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	out := applyDefaults(Filter{From: from, To: to}, now)
	if !out.From.Equal(from) || !out.To.Equal(to) {
		t.Errorf("applyDefaults mutated explicit bounds: got from=%v to=%v", out.From, out.To)
	}
}

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestRate(t *testing.T) {
	tests := []struct {
		part, whole int
		want        float64
	}{
		{0, 0, 0},    // div-by-zero guard
		{5, 0, 0},    // div-by-zero guard
		{1, 2, 0.5},  //
		{3, 4, 0.75}, //
		{10, 10, 1},  // 100%
		{0, 7, 0},    // none
	}
	for _, tt := range tests {
		if got := Rate(tt.part, tt.whole); !almostEqual(got, tt.want) {
			t.Errorf("Rate(%d,%d) = %v, want %v", tt.part, tt.whole, got, tt.want)
		}
	}
}

func TestPct(t *testing.T) {
	tests := []struct {
		part, whole int
		want        float64
	}{
		{0, 0, 0},    // div-by-zero guard
		{1, 2, 50},   //
		{1, 3, 33.3}, // rounded to 1dp
		{2, 3, 66.7}, // rounded to 1dp
		{7, 7, 100},  //
		{0, 5, 0},    //
	}
	for _, tt := range tests {
		if got := Pct(tt.part, tt.whole); !almostEqual(got, tt.want) {
			t.Errorf("Pct(%d,%d) = %v, want %v", tt.part, tt.whole, got, tt.want)
		}
	}
}

func TestPercentile(t *testing.T) {
	// Linear-interpolation (NumPy default) percentiles.
	data := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	tests := []struct {
		name    string
		samples []float64
		p       float64
		want    float64
	}{
		{"empty → 0", nil, 50, 0},
		{"single sample", []float64{42}, 90, 42},
		{"p0 → min", data, 0, 1},
		{"p100 → max", data, 100, 10},
		{"p50 (median) of 1..10", data, 50, 5.5},
		{"p90 of 1..10", data, 90, 9.1},
		{"unsorted input", []float64{10, 1, 5, 3}, 50, 4}, // sorted 1,3,5,10 → median = (3+5)/2
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Percentile(tt.samples, tt.p); !almostEqual(got, tt.want) {
				t.Errorf("Percentile(%v, %v) = %v, want %v", tt.samples, tt.p, got, tt.want)
			}
		})
	}
}

func TestPercentileDoesNotMutateInput(t *testing.T) {
	in := []float64{5, 1, 3, 2, 4}
	cp := append([]float64{}, in...)
	_ = Percentile(in, 50)
	for i := range in {
		if in[i] != cp[i] {
			t.Fatalf("Percentile mutated input at %d: got %v, want %v", i, in, cp)
		}
	}
}
