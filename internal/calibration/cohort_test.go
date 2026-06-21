package calibration

import (
	"reflect"
	"testing"
)

func TestSizeBucket(t *testing.T) {
	tests := []struct {
		name string
		in   DiffStats
		want string
	}{
		{"tiny one-liner", DiffStats{Additions: 3, Deletions: 1, ChangedFiles: 1}, "xs"},
		{"small", DiffStats{Additions: 30, Deletions: 10, ChangedFiles: 2}, "s"},
		{"medium churn", DiffStats{Additions: 150, Deletions: 60, ChangedFiles: 4}, "m"},
		{"large churn", DiffStats{Additions: 600, Deletions: 200, ChangedFiles: 8}, "l"},
		{"xl by churn", DiffStats{Additions: 2000, Deletions: 500, ChangedFiles: 3}, "xl"},
		// files-dominated: small churn but huge fan-out ⇒ coarser wins.
		{"xl by files", DiffStats{Additions: 40, Deletions: 10, ChangedFiles: 30}, "xl"},
		{"l by files beats xs churn", DiffStats{Additions: 5, Deletions: 2, ChangedFiles: 12}, "l"},
		// TopDirs can stand in for file count when ChangedFiles is 0.
		{"files from topdirs", DiffStats{Additions: 5, Deletions: 0, ChangedFiles: 0, TopDirs: []string{"a", "b", "c", "d"}}, "m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SizeBucket(tt.in); got != tt.want {
				t.Errorf("SizeBucket(%+v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestChangeType(t *testing.T) {
	tests := []struct {
		name string
		in   PRMeta
		want string
	}{
		{"conventional feat", PRMeta{Title: "feat: add billing webhook"}, "feature"},
		{"conventional fix scope", PRMeta{Title: "fix(api): null deref on empty body"}, "fix"},
		{"branch prefix", PRMeta{Branch: "fix/login-redirect"}, "fix"},
		{"bug keyword", PRMeta{Title: "Resolve crash when token expires"}, "feature"}, // "resolve" not a keyword; default
		{"explicit bug word", PRMeta{Title: "Bug: race in scheduler"}, "fix"},
		{"docs by keyword", PRMeta{Title: "Update README and docs"}, "docs"},
		{"refactor", PRMeta{Title: "Refactor the sync pipeline"}, "refactor"},
		{"chore deps", PRMeta{Title: "chore: bump dependencies"}, "chore"},
		{"test keyword", PRMeta{Title: "Add tests for cohort derivation"}, "test"},
		{"docs by path only", PRMeta{Title: "misc", Paths: []string{"docs/intro.md", "README.md"}}, "docs"},
		{"test by path only", PRMeta{Title: "misc", Paths: []string{"internal/x_test.go", "internal/y_test.go"}}, "test"},
		{"default feature", PRMeta{Title: "Wire the dashboard charts"}, "feature"},
		{"add is feature not address", PRMeta{Title: "Update address validation"}, "feature"},
		{"feature word boundary", PRMeta{Title: "implement retry"}, "feature"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ChangeType(tt.in); got != tt.want {
				t.Errorf("ChangeType(%+v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestTopDirsFromPaths(t *testing.T) {
	got := TopDirsFromPaths([]string{
		"internal/api/x.go", "internal/store/y.go", "web/app.tsx", "README.md", "./go.mod",
	})
	want := []string{"internal", "root", "web"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TopDirsFromPaths = %v, want %v", got, want)
	}
}

func TestCohortCandidates(t *testing.T) {
	tests := []struct {
		name       string
		meta       PRMeta
		stats      DiffStats
		changeType string
		want       []string
	}{
		{
			"full richest-first",
			PRMeta{RepoID: "R1"},
			DiffStats{TopDirs: []string{"web", "internal"}}, // primary area = "internal" (sorted)
			"feature",
			[]string{"repo:R1|area:internal", "repo:R1", "type:feature", "global"},
		},
		{
			"no area",
			PRMeta{RepoID: "R1"},
			DiffStats{},
			"fix",
			[]string{"repo:R1", "type:fix", "global"},
		},
		{
			"no repo",
			PRMeta{},
			DiffStats{TopDirs: []string{"internal"}},
			"refactor",
			[]string{"type:refactor", "global"},
		},
		{
			"global only",
			PRMeta{},
			DiffStats{},
			"",
			[]string{"global"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CohortCandidates(tt.meta, tt.stats, tt.changeType)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CohortCandidates = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExpandCohorts(t *testing.T) {
	tests := []struct {
		stored string
		want   []string
	}{
		{"repo:R1|area:internal", []string{"global", "repo:R1|area:internal", "repo:R1"}},
		{"repo:R1", []string{"global", "repo:R1"}},
		{"type:fix", []string{"global", "type:fix"}},
		{"global", []string{"global"}},
		{"", []string{"global"}},
	}
	for _, tt := range tests {
		t.Run(tt.stored, func(t *testing.T) {
			got := expandCohorts(tt.stored)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("expandCohorts(%q) = %v, want %v", tt.stored, got, tt.want)
			}
		})
	}
}
