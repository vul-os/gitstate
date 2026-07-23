// Package calibration turns a model difficulty score into a calibrated
// effort-in-seconds estimate that learns from each org's own merged history.
//
// The design (Wave 1 of the AI/agent flywheel):
//
//   - cohort.go    — pure, deterministic derivation of cohort_key / size_bucket /
//     change_type from a PR + its diff metadata.
//   - curve.go     — the math: a fixed cold-start difficulty→secs prior, empirical-
//     Bayes shrinkage toward the global cohort, recency-weighted
//     quantiles, and CalibratedSecs (the read path).
//   - recompute.go — RecomputeCalibration: backfill actuals from cycle_times, build
//     the per-cohort curves, and the per-cohort accuracy summary.
//
// All DB work runs inside db.WithOrg so the non-superuser FORCE-RLS role sees
// app.current_org; a bare-pool read would return zero rows.
package calibration

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// DiffStats are the size signals used to bucket a change. They are taken from
// the pull_requests row (additions/deletions/changed_files) and never require
// re-reading the raw diff.
type DiffStats struct {
	Additions    int
	Deletions    int
	ChangedFiles int
	// TopDirs are the distinct top-level directories touched by the change
	// (e.g. "internal", "web"). Optional — when empty, area cohorts are skipped.
	TopDirs []string
}

// PRMeta is the minimal PR context needed to derive cohorts. RepoID is the
// repo UUID; Title / Branch / Paths feed the change_type heuristic.
type PRMeta struct {
	RepoID string
	Title  string
	Branch string
	Paths  []string
}

// ── size_bucket ───────────────────────────────────────────────────────────────

// SizeBucket classifies the magnitude of a change into xs|s|m|l|xl using both
// churn (additions+deletions) and the number of files touched — whichever is
// larger wins, so a one-file 2000-line generated change and a 40-file refactor
// both land in xl. Deterministic and monotonic.
func SizeBucket(s DiffStats) string {
	churn := s.Additions + s.Deletions
	files := s.ChangedFiles
	if files < len(s.TopDirs) {
		files = len(s.TopDirs)
	}

	byChurn := bucketByThresholds(churn, 10, 50, 250, 1000)
	byFiles := bucketByThresholds(files, 1, 3, 10, 25)

	// The coarser (larger) of the two classifications wins.
	if bucketRank(byFiles) > bucketRank(byChurn) {
		return byFiles
	}
	return byChurn
}

var sizeOrder = []string{"xs", "s", "m", "l", "xl"}

func bucketRank(b string) int {
	for i, v := range sizeOrder {
		if v == b {
			return i
		}
	}
	return 0
}

// bucketByThresholds maps v into xs|s|m|l|xl. t1..t4 are the upper bounds of
// xs, s, m, l respectively (v > t4 ⇒ xl).
func bucketByThresholds(v, t1, t2, t3, t4 int) string {
	switch {
	case v <= t1:
		return "xs"
	case v <= t2:
		return "s"
	case v <= t3:
		return "m"
	case v <= t4:
		return "l"
	default:
		return "xl"
	}
}

// ── change_type ───────────────────────────────────────────────────────────────

// changeTypeRules is evaluated in order; the first matching family wins. Keeping
// the order explicit makes the heuristic deterministic and easy to test: fixes
// beat features (a "fix the feature flag" PR is a fix), docs/test beat code.
var changeTypeKeywords = []struct {
	typ   string
	words []string
}{
	{"fix", []string{"fix", "bug", "hotfix", "patch", "regression", "revert", "broken"}},
	{"docs", []string{"docs", "doc", "documentation", "readme", "changelog", "comment"}},
	{"test", []string{"test", "tests", "spec", "coverage", "e2e", "fixture"}},
	{"refactor", []string{"refactor", "cleanup", "clean-up", "rename", "restructure", "simplify", "dedupe"}},
	{"chore", []string{"chore", "bump", "deps", "dependency", "dependencies", "ci", "build", "release", "lint", "format", "config"}},
	{"feature", []string{"feat", "feature", "add", "implement", "introduce", "support", "new"}},
}

// ChangeType classifies a PR into feature|fix|refactor|chore|docs|test using the
// title, branch name, and touched paths. Path-only signals (all files under
// docs/ or *_test.go) are decisive when the title is ambiguous. Default is
// "feature" — the most common bucket for net-new work. Deterministic.
func ChangeType(m PRMeta) string {
	hay := strings.ToLower(m.Title + " " + m.Branch)

	// Conventional-commit / branch prefixes are the strongest signal.
	if t := conventionalPrefix(hay); t != "" {
		return t
	}

	// Path-derived signals: a change touching only docs or only tests is that
	// type regardless of the prose.
	if onlyPathsMatch(m.Paths, isDocPath) {
		return "docs"
	}
	if onlyPathsMatch(m.Paths, isTestPath) {
		return "test"
	}

	for _, rule := range changeTypeKeywords {
		for _, w := range rule.words {
			if containsWord(hay, w) {
				return rule.typ
			}
		}
	}
	return "feature"
}

// conventionalPrefix recognises "feat:", "fix(scope):", "feat/foo", "chore-…"
// style prefixes at the very start of the title/branch.
func conventionalPrefix(hay string) string {
	hay = strings.TrimSpace(hay)
	prefixes := map[string]string{
		"feat":     "feature",
		"feature":  "feature",
		"fix":      "fix",
		"bugfix":   "fix",
		"hotfix":   "fix",
		"docs":     "docs",
		"doc":      "docs",
		"test":     "test",
		"tests":    "test",
		"refactor": "refactor",
		"chore":    "chore",
		"build":    "chore",
		"ci":       "chore",
		"perf":     "refactor",
	}
	// token is everything up to the first :, (, /, -, or space.
	cut := strings.IndexFunc(hay, func(r rune) bool {
		return r == ':' || r == '(' || r == '/' || r == '-' || r == ' '
	})
	if cut <= 0 {
		return ""
	}
	tok := hay[:cut]
	if t, ok := prefixes[tok]; ok {
		return t
	}
	return ""
}

func isDocPath(p string) bool {
	p = strings.ToLower(p)
	return strings.HasPrefix(p, "docs/") || strings.HasPrefix(p, "doc/") ||
		strings.HasSuffix(p, ".md") || strings.HasSuffix(p, ".rst") ||
		strings.Contains(p, "readme") || strings.Contains(p, "changelog")
}

func isTestPath(p string) bool {
	p = strings.ToLower(p)
	base := path.Base(p)
	return strings.HasSuffix(p, "_test.go") ||
		strings.HasSuffix(p, ".test.ts") || strings.HasSuffix(p, ".test.js") ||
		strings.HasSuffix(p, ".spec.ts") || strings.HasSuffix(p, ".spec.js") ||
		strings.HasPrefix(p, "test/") || strings.HasPrefix(p, "tests/") ||
		strings.HasPrefix(base, "test_") || strings.Contains(p, "/__tests__/")
}

// onlyPathsMatch reports true when paths is non-empty and every path satisfies f.
func onlyPathsMatch(paths []string, f func(string) bool) bool {
	if len(paths) == 0 {
		return false
	}
	for _, p := range paths {
		if !f(p) {
			return false
		}
	}
	return true
}

// containsWord reports whether word appears in hay as a whole token (bounded by
// non-alphanumeric characters), so "add" does not match "address".
func containsWord(hay, word string) bool {
	idx := 0
	for {
		i := strings.Index(hay[idx:], word)
		if i < 0 {
			return false
		}
		start := idx + i
		end := start + len(word)
		leftOK := start == 0 || !isAlnum(rune(hay[start-1]))
		rightOK := end == len(hay) || !isAlnum(rune(hay[end]))
		if leftOK && rightOK {
			return true
		}
		idx = start + 1
		if idx >= len(hay) {
			return false
		}
	}
}

func isAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// ── cohort_key ────────────────────────────────────────────────────────────────

// GlobalCohort is the org-wide fallback cohort that always exists once there is
// any merged history; it is the empirical-Bayes prior for sparser cohorts.
const GlobalCohort = "global"

// CohortCandidates returns the cohort keys for a change ordered RICHEST-FIRST:
//
//	repo:<id>|area:<topdir>   (most specific — same repo, same area)
//	repo:<id>                 (same repo)
//	type:<change_type>        (same kind of change, org-wide)
//	global                    (org-wide prior, always last)
//
// The area segment is the top-level directory shared by the change; when the
// diff touches multiple top dirs the lexicographically-smallest is used so the
// key is stable. Deterministic.
func CohortCandidates(m PRMeta, stats DiffStats, changeType string) []string {
	var out []string

	if m.RepoID != "" {
		if area := primaryArea(stats.TopDirs); area != "" {
			out = append(out, fmt.Sprintf("repo:%s|area:%s", m.RepoID, area))
		}
		out = append(out, fmt.Sprintf("repo:%s", m.RepoID))
	}
	if changeType != "" {
		out = append(out, fmt.Sprintf("type:%s", changeType))
	}
	out = append(out, GlobalCohort)
	return out
}

// primaryArea picks the stable representative top-level directory.
func primaryArea(topDirs []string) string {
	cleaned := make([]string, 0, len(topDirs))
	for _, d := range topDirs {
		d = strings.Trim(strings.TrimSpace(d), "/")
		if d == "" || d == "." {
			continue
		}
		cleaned = append(cleaned, d)
	}
	if len(cleaned) == 0 {
		return ""
	}
	sort.Strings(cleaned)
	return cleaned[0]
}

// TopDirsFromPaths extracts the distinct top-level directories from a set of
// changed file paths (e.g. "internal/api/x.go" → "internal"). Files at the repo
// root contribute the sentinel "root". Deterministic, sorted, de-duplicated.
func TopDirsFromPaths(paths []string) []string {
	seen := map[string]struct{}{}
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		p = strings.TrimPrefix(p, "./")
		var top string
		if i := strings.IndexByte(p, '/'); i >= 0 {
			top = p[:i]
		} else {
			top = "root"
		}
		if top == "" {
			top = "root"
		}
		seen[top] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}
