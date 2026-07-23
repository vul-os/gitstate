package gitanalysis

import (
	"reflect"
	"testing"
)

// ── IsBugFixMessage ─────────────────────────────────────────────────────────

func TestIsBugFixMessage(t *testing.T) {
	cases := map[string]bool{
		"fix: correct off-by-one":     true,
		"fixed the crash":             true,
		"fixes #123":                  true,
		"hotfix for prod":             true,
		"bug in parser":               true,
		"patch the leak":              true,
		"patched memory issue":        true,
		"revert bad change":           true,
		"reverted the merge":          true,
		"address regression in cache": true,
		"feat: add new endpoint":      false,
		"refactor module":             false,
		"docs: update readme":         false,
		"prefix matters not":          false,
		"affixed a label":             false, // must not match "fix" inside a word
	}
	for msg, want := range cases {
		if got := IsBugFixMessage(msg); got != want {
			t.Errorf("IsBugFixMessage(%q) = %v, want %v", msg, got, want)
		}
	}
}

// ── parseStatNum ────────────────────────────────────────────────────────────

func TestParseStatNum(t *testing.T) {
	cases := map[string]int{
		"5":     5,
		"  12 ": 12,
		"-":     0, // binary file marker
		"":      0,
		"abc":   0,
		"0":     0,
	}
	for in, want := range cases {
		if got := parseStatNum(in); got != want {
			t.Errorf("parseStatNum(%q) = %d, want %d", in, got, want)
		}
	}
}

// ── cleanRenamePath ─────────────────────────────────────────────────────────

func TestCleanRenamePath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"src/app.go", "src/app.go"},
		{"  src/app.go  ", "src/app.go"},
		{"old.go => new.go", "new.go"},
		{"dir/{old => new}/file.go", "dir/new/file.go"},
		{"a/{b => c}/d.go", "a/c/d.go"},
		{"pkg/{ => sub}/x.go", "pkg/sub/x.go"}, // empty old side
		{"", ""},
	}
	for _, c := range cases {
		if got := cleanRenamePath(c.in); got != c.want {
			t.Errorf("cleanRenamePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCleanRenamePath_Quoted(t *testing.T) {
	// Git C-quotes non-ASCII paths; we best-effort unquote.
	got := cleanRenamePath(`"caf\303\251.txt"`)
	if got != "café.txt" {
		t.Errorf("quoted path = %q, want café.txt", got)
	}
}

// ── normEmail ───────────────────────────────────────────────────────────────

func TestNormEmail(t *testing.T) {
	cases := map[string]string{
		"  Alice@ACME.dev  ": "alice@acme.dev",
		"BOB@X.COM":          "bob@x.com",
		"":                   "",
	}
	for in, want := range cases {
		if got := normEmail(in); got != want {
			t.Errorf("normEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

// ── isHex40 / isZeroSHA / short ─────────────────────────────────────────────

func TestIsHex40(t *testing.T) {
	valid := "0123456789abcdef0123456789abcdef01234567" // 40 hex
	if !isHex40(valid) {
		t.Error("valid 40-hex should pass")
	}
	if isHex40("tooShort") {
		t.Error("short string should fail")
	}
	if isHex40("0123456789ABCDEF0123456789abcdef01234567") {
		t.Error("uppercase hex should fail (git porcelain is lowercase)")
	}
	if isHex40("0123456789abcdef0123456789abcdef0123456g") {
		t.Error("non-hex char should fail")
	}
}

func TestIsZeroSHA(t *testing.T) {
	if !isZeroSHA("0000000000000000000000000000000000000000") {
		t.Error("all-zero sha should be detected")
	}
	if isZeroSHA("0000000000000000000000000000000000000001") {
		t.Error("non-zero sha should not match")
	}
	if isZeroSHA("") {
		t.Error("empty string is not a zero sha")
	}
}

func TestShort(t *testing.T) {
	if got := short("0123456789abcdef"); got != "01234567" {
		t.Errorf("short = %q, want 01234567", got)
	}
	if got := short("abc"); got != "abc" {
		t.Errorf("short of <8 = %q, want abc", got)
	}
}

// ── injectToken ─────────────────────────────────────────────────────────────

func TestInjectToken(t *testing.T) {
	cases := []struct {
		name, url, token, want string
	}{
		{"no token", "https://github.com/a/b.git", "", "https://github.com/a/b.git"},
		{"https injects userinfo", "https://github.com/a/b.git", "tok", "https://x-access-token:tok@github.com/a/b.git"},
		{"non-https unchanged", "git@github.com:a/b.git", "tok", "git@github.com:a/b.git"},
		{"already has userinfo", "https://user@github.com/a/b.git", "tok", "https://user@github.com/a/b.git"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := injectToken(c.url, c.token); got != c.want {
				t.Errorf("injectToken(%q,%q) = %q, want %q", c.url, c.token, got, c.want)
			}
		})
	}
}

// ── scrubURL (token never leaks into logs) ──────────────────────────────────

func TestScrubURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://x-access-token:secret@github.com/a/b.git", "https://github.com/…"},
		{"https://github.com/a/b.git", "https://github.com/…"},
		{"https://github.com", "https://github.com"}, // no path
		{"git://host/repo", "git://host/…"},
		{"not-a-url", "<repo>"},
	}
	for _, c := range cases {
		got := scrubURL(c.in)
		if got != c.want {
			t.Errorf("scrubURL(%q) = %q, want %q", c.in, got, c.want)
		}
		// The token must never appear in the scrubbed form.
		if got == c.in && c.in == "https://x-access-token:secret@github.com/a/b.git" {
			t.Error("token leaked")
		}
	}
}

// ── parseDeletedRanges (SZZ diff parsing) ───────────────────────────────────

func TestParseDeletedRanges(t *testing.T) {
	diff := "diff --git a/app.go b/app.go\n" +
		"--- a/app.go\n" +
		"+++ b/app.go\n" +
		"@@ -10,2 +10,1 @@\n" +
		"-old line A\n" +
		"-old line B\n" +
		"+new line\n" +
		"@@ -50 +49,0 @@\n" + // single-line, no count → count 1
		"-removed\n"
	got := parseDeletedRanges(diff)
	want := map[string][]lineRange{
		"app.go": {{start: 10, count: 2}, {start: 50, count: 1}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseDeletedRanges = %+v, want %+v", got, want)
	}
}

func TestParseDeletedRanges_PureAdditionSkipped(t *testing.T) {
	// A hunk that only adds lines (oldCount 0) has no pre-image line to blame.
	diff := "diff --git a/new.go b/new.go\n" +
		"--- /dev/null\n" +
		"+++ b/new.go\n" +
		"@@ -0,0 +1,3 @@\n" +
		"+a\n+b\n+c\n"
	got := parseDeletedRanges(diff)
	if len(got) != 0 {
		t.Errorf("pure-addition diff should yield no ranges, got %+v", got)
	}
}

func TestParseDeletedRanges_FileDeletion(t *testing.T) {
	// New side /dev/null (file deleted): old path is recovered from the --- header.
	diff := "diff --git a/gone.go b/gone.go\n" +
		"--- a/gone.go\n" +
		"+++ /dev/null\n" +
		"@@ -1,2 +0,0 @@\n" +
		"-x\n-y\n"
	got := parseDeletedRanges(diff)
	if rs, ok := got["gone.go"]; !ok || len(rs) != 1 || rs[0].count != 2 {
		t.Errorf("file-deletion ranges = %+v, want gone.go [{1,2}]", got)
	}
}

// ── blamePorcelainAuthors ───────────────────────────────────────────────────

func TestBlamePorcelainAuthors(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	// Two content lines attributed to one introducing commit.
	raw := []byte(sha + " 1 1 2\n" +
		"author-mail <alice@acme.dev>\n" +
		"\tcontent line 1\n" +
		sha + " 2 2\n" +
		"\tcontent line 2\n")
	got := blamePorcelainAuthors(raw)
	ia, ok := got[sha]
	if !ok {
		t.Fatalf("expected attribution for %s; got %+v", sha, got)
	}
	if ia.lines != 2 {
		t.Errorf("lines = %d, want 2", ia.lines)
	}
	if ia.email != "alice@acme.dev" {
		t.Errorf("email = %q, want alice@acme.dev", ia.email)
	}
}

func TestBlamePorcelainAuthors_IgnoresZeroSHA(t *testing.T) {
	zero := "0000000000000000000000000000000000000000"
	raw := []byte(zero + " 1 1 1\n" +
		"author-mail <not-committed@local>\n" +
		"\tuncommitted line\n")
	if got := blamePorcelainAuthors(raw); len(got) != 0 {
		t.Errorf("zero-sha (uncommitted) should not be attributed, got %+v", got)
	}
}
