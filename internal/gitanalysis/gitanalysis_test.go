package gitanalysis

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// gitAvailable reports whether the git binary is present; tests skip gracefully
// when it is not (e.g. minimal CI images).
func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// gitRun runs a git command in dir with a fixed author/committer identity + date,
// failing the test on error. Empty name/email leaves the configured identity.
func gitRun(t *testing.T, dir string, when time.Time, name, email string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", full...)
	d := when.Format(time.RFC3339)
	env := append(os.Environ(),
		"GIT_AUTHOR_DATE="+d, "GIT_COMMITTER_DATE="+d,
		"GIT_TERMINAL_PROMPT=0", "LC_ALL=C",
	)
	if name != "" {
		env = append(env, "GIT_AUTHOR_NAME="+name, "GIT_COMMITTER_NAME="+name)
	}
	if email != "" {
		env = append(env, "GIT_AUTHOR_EMAIL="+email, "GIT_COMMITTER_EMAIL="+email)
	}
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", full, err, out)
	}
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestAnalyzeRepo builds a tiny two-author repo with a test file and a bug-fix
// commit, then asserts: survival is computed, the test file is detected, and SZZ
// produces at least one bug-introduction attribution.
func TestAnalyzeRepo(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git binary not available; skipping")
	}

	dir := t.TempDir()
	t0 := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)

	gitRun(t, dir, t0, "", "", "init", "-q", "-b", "main")
	gitRun(t, dir, t0, "", "", "config", "user.name", "seed")
	gitRun(t, dir, t0, "", "", "config", "user.email", "seed@local")

	const (
		alice = "alice@acme.dev"
		bob   = "bob@acme.dev"
	)

	// Commit 1 (Alice): introduces app.go with a "buggy" line that survives.
	write(t, dir, "app.go", "package app\n\nfunc Add(a, b int) int {\n\treturn a - b // BUG: should be +\n}\n")
	gitRun(t, dir, t0, "", "", "add", "-A")
	gitRun(t, dir, t0, "Alice", alice, "commit", "-q", "-m", "feat(app): add Add function")

	// Commit 2 (Bob): adds a test file (test-coupling) + a durable helper.
	t1 := t0.Add(24 * time.Hour)
	write(t, dir, "app_test.go", "package app\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T){ if Add(1,1)!=2 { t.Fail() } }\n")
	write(t, dir, "helper.go", "package app\n\nfunc Helper() string { return \"ok\" }\n")
	gitRun(t, dir, t1, "", "", "add", "-A")
	gitRun(t, dir, t1, "Bob", bob, "commit", "-q", "-m", "test(app): add Add test and helper")

	// Commit 3 (Bob): a BUG-FIX commit that repairs Alice's buggy line. SZZ should
	// blame Alice (the introducing author) for the fixed line.
	t2 := t1.Add(24 * time.Hour)
	write(t, dir, "app.go", "package app\n\nfunc Add(a, b int) int {\n\treturn a + b // fixed\n}\n")
	gitRun(t, dir, t2, "", "", "add", "-A")
	gitRun(t, dir, t2, "Bob", bob, "commit", "-q", "-m", "fix(app): correct Add to use addition")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	res, err := AnalyzeRepo(ctx, dir)
	if err != nil {
		t.Fatalf("AnalyzeRepo: %v", err)
	}
	if res.HeadSHA == "" {
		t.Fatal("expected a resolved HEAD sha")
	}

	// ── Test-file detection in commit_files. ────────────────────────────────
	var sawTest, sawProd bool
	for _, cf := range res.CommitFiles {
		if cf.Path == "app_test.go" {
			if !cf.IsTest {
				t.Errorf("app_test.go should be flagged is_test")
			}
			sawTest = true
		}
		if cf.Path == "app.go" {
			if cf.IsTest {
				t.Errorf("app.go should NOT be flagged is_test")
			}
			sawProd = true
		}
	}
	if !sawTest {
		t.Error("expected a commit_files row for app_test.go")
	}
	if !sawProd {
		t.Error("expected a commit_files row for app.go")
	}

	// ── Survival: both authors should appear; surviving lines tracked. ──────
	survByEmail := map[string]AuthorSurvival{}
	for _, s := range res.Survival {
		survByEmail[s.AuthorEmail] = s
	}
	if len(survByEmail) == 0 {
		t.Fatal("expected per-author survival rows")
	}
	if _, ok := survByEmail[alice]; !ok {
		t.Errorf("expected Alice in survival rows; got %v", keys(survByEmail))
	}
	bobSurv, ok := survByEmail[bob]
	if !ok {
		t.Fatalf("expected Bob in survival rows; got %v", keys(survByEmail))
	}
	// Bob's helper.go + the fixed line should still survive at HEAD.
	if bobSurv.SurvivingLines == 0 {
		t.Errorf("expected Bob to have surviving lines at HEAD, got 0")
	}
	if bobSurv.AuthoredLines == 0 {
		t.Errorf("expected Bob to have authored lines, got 0")
	}

	// ── SZZ: at least one attribution, and the bug should trace to Alice. ───
	if len(res.BugIntros) == 0 {
		t.Fatal("expected at least one SZZ bug-introduction attribution")
	}
	var blamedAlice bool
	for _, bi := range res.BugIntros {
		if bi.Lines < 1 {
			t.Errorf("bug introduction should blame ≥1 line, got %d", bi.Lines)
		}
		if bi.FixSHA == "" || bi.IntroducedSHA == "" {
			t.Errorf("bug introduction missing sha(s): %+v", bi)
		}
		if bi.AuthorEmail == alice {
			blamedAlice = true
		}
	}
	if !blamedAlice {
		t.Errorf("expected SZZ to blame Alice for the fixed bug; attributions: %+v", res.BugIntros)
	}
}

// TestEmptyRepo: an empty repo must not panic and returns an empty result.
func TestEmptyRepo(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git binary not available; skipping")
	}
	dir := t.TempDir()
	gitRun(t, dir, time.Now(), "", "", "init", "-q", "-b", "main")

	res, err := AnalyzeRepo(context.Background(), dir)
	if err != nil {
		t.Fatalf("AnalyzeRepo on empty repo should not error, got: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if len(res.CommitFiles) != 0 || len(res.Survival) != 0 || len(res.BugIntros) != 0 {
		t.Errorf("expected empty result for empty repo, got %+v", res)
	}
}

// TestIsTestPath spot-checks the cross-ecosystem test-path heuristic.
func TestIsTestPath(t *testing.T) {
	cases := map[string]bool{
		"internal/foo_test.go":       true,
		"src/app.test.js":            true,
		"web/Button.spec.tsx":        true,
		"tests/test_thing.py":        true,
		"pkg/thing_test.py":          true,
		"src/__tests__/x.js":         true,
		"java/src/FooTest.java":      true,
		"java/src/TestFoo.java":      true,
		"spec/models/user_spec.rb":   true,
		"testdata/fixture.json":      true,
		"src/app.go":                 false,
		"src/contest.go":             false, // "test" substring must not match
		"README.md":                  false,
		"internal/latest/version.go": false,
	}
	for path, want := range cases {
		if got := IsTestPath(path); got != want {
			t.Errorf("IsTestPath(%q) = %v, want %v", path, got, want)
		}
	}
}

func keys(m map[string]AuthorSurvival) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
