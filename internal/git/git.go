// Package git provides local git reading by shelling out to the system git binary.
// All operations use exec.Command arg slices — never string interpolation of
// untrusted input — and carry context timeouts.
//
// Exported surface for the sync agent:
//
//	Clone / Fetch          — manage a local clone cache
//	WalkCommits            — iterate commits with authorship + stat + is_agent flag
//	Diff / DiffRange       — unified diff string + per-file numstat
//	Blame                  — line→author map for a single file
//	LeadTime               — first-commit-at → merged-at duration helper
package git

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// defaultTimeout for all git sub-commands that do NOT perform network I/O.
const defaultTimeout = 60 * time.Second

// cloneTimeout for clone/fetch which touch the network.
const cloneTimeout = 5 * time.Minute

// ── Clone / Fetch ─────────────────────────────────────────────────────────────

// CacheDir returns the root directory used for local repo clones.
// It respects the GIT_CACHE_DIR environment variable; otherwise it uses
// a "gitstate-repos" sub-directory under os.TempDir().
func CacheDir() string {
	if v := os.Getenv("GIT_CACHE_DIR"); v != "" {
		return v
	}
	return filepath.Join(os.TempDir(), "gitstate-repos")
}

// RepoDir returns the canonical local path for a cloned repo given its clone URL.
// The directory name is derived by URL-slugifying the clone URL so that repeated
// Clone calls are idempotent.
func RepoDir(cloneURL string) string {
	slug := strings.NewReplacer(
		"://", "_",
		"/", "_",
		":", "_",
		"@", "_",
		".git", "",
	).Replace(cloneURL)
	return filepath.Join(CacheDir(), slug)
}

// Clone clones cloneURL into dir (shallow, depth=1 for speed).
// If dir already exists it performs a Fetch instead.
// dir is created (with parents) if it does not exist.
func Clone(ctx context.Context, cloneURL, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("git: mkdir %s: %w", dir, err)
	}

	// If the directory already looks like a git repo, fetch instead.
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err == nil {
		return Fetch(ctx, dir)
	}

	cloneCtx, cancel := context.WithTimeout(ctx, cloneTimeout)
	defer cancel()

	// --depth 1 for initial clone; we deepen on WalkCommits when since is set.
	cmd := exec.CommandContext(cloneCtx, "git", "clone",
		"--depth", "1",
		"--no-single-branch",
		cloneURL, dir,
	)
	return runCmd(cmd, "clone")
}

// Fetch updates an existing local clone.
// Uses --depth=2147483647 (unshallow) so that WalkCommits can see history.
func Fetch(ctx context.Context, dir string) error {
	fetchCtx, cancel := context.WithTimeout(ctx, cloneTimeout)
	defer cancel()

	cmd := exec.CommandContext(fetchCtx, "git", "-C", dir,
		"fetch", "--all", "--unshallow",
	)
	err := runCmd(cmd, "fetch --unshallow")
	// "--unshallow" errors if the repo is already complete; ignore that.
	if err != nil && strings.Contains(err.Error(), "unshallow") {
		cmd2 := exec.CommandContext(ctx, "git", "-C", dir, "fetch", "--all")
		return runCmd(cmd2, "fetch")
	}
	return err
}

// ── WalkCommits ───────────────────────────────────────────────────────────────

// CommitRecord holds the parsed output of a single git commit.
type CommitRecord struct {
	SHA         string
	AuthorName  string
	AuthorEmail string
	Message     string
	Additions   int
	Deletions   int
	CommittedAt time.Time
	IsAgent     bool // detected by is_agent_heuristic
}

// WalkCommits walks commits in dir reachable from HEAD, optionally filtered to
// those whose commit timestamp is after since (zero = all history).
// Commits are returned newest-first (git log default).
func WalkCommits(ctx context.Context, dir string, since time.Time) ([]CommitRecord, error) {
	walkCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	// Format: NUL-delimited fields per commit, separated by a record sentinel.
	// Fields: sha, author name, author email, commit timestamp (unix), subject+body.
	const sentinel = "---GSCOMMIT---"
	const format = "%H%n%aN%n%aE%n%at%n%B%n" + sentinel

	args := []string{"-C", dir, "log",
		"--format=" + format,
		"--numstat",
		"--no-merges",
	}
	if !since.IsZero() {
		args = append(args, "--after="+since.UTC().Format(time.RFC3339))
	}

	cmd := exec.CommandContext(walkCtx, "git", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git: walk commits: %w", err)
	}

	return parseWalkOutput(string(out), sentinel)
}

// parseWalkOutput parses the mixed format+numstat output produced by WalkCommits.
// Each commit block looks like:
//
//	<sha>
//	<author name>
//	<author email>
//	<unix timestamp>
//	<message lines...>
//	---GSCOMMIT---
//	<blank line>
//	<numstat lines: add TAB del TAB file>
//	<blank line>
func parseWalkOutput(raw, sentinel string) ([]CommitRecord, error) {
	var records []CommitRecord

	// Split into per-commit blocks on the sentinel.
	// git outputs: header block → sentinel → blank → numstat → blank → next header block.
	// We parse by scanning line by line with a small state machine.
	type state int
	const (
		stHeader state = iota
		stMessage
		stNumstat
	)

	sc := bufio.NewScanner(strings.NewReader(raw))
	sc.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)

	var (
		cur     CommitRecord
		lineNo  int
		msgBuf  strings.Builder
		inBlock bool
		st      state
	)

	flush := func() {
		if inBlock {
			cur.Message = strings.TrimSpace(msgBuf.String())
			cur.IsAgent = isAgentCommit(cur)
			records = append(records, cur)
			cur = CommitRecord{}
			msgBuf.Reset()
			inBlock = false
		}
	}

	for sc.Scan() {
		line := sc.Text()

		if line == sentinel {
			// End of message section; numstat follows (after a blank line).
			st = stNumstat
			lineNo = 0
			continue
		}

		switch st {
		case stHeader:
			// The header has exactly 4 preamble lines before the message.
			switch lineNo {
			case 0:
				if line == "" {
					// Blank between numstat block and next commit header; skip.
					continue
				}
				flush()
				inBlock = true
				cur.SHA = line
				lineNo++
			case 1:
				cur.AuthorName = line
				lineNo++
			case 2:
				cur.AuthorEmail = line
				lineNo++
			case 3:
				ts, err := strconv.ParseInt(line, 10, 64)
				if err == nil {
					cur.CommittedAt = time.Unix(ts, 0).UTC()
				}
				lineNo++
				st = stMessage
			}

		case stMessage:
			// Accumulate message until sentinel.
			msgBuf.WriteString(line)
			msgBuf.WriteByte('\n')

		case stNumstat:
			if line == "" {
				// End of numstat for this commit; next line starts the next header.
				st = stHeader
				lineNo = 0
				continue
			}
			// Format: <add>\t<del>\t<file>  (or "-" for binary)
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) == 3 {
				if a, err := strconv.Atoi(parts[0]); err == nil {
					cur.Additions += a
				}
				if d, err := strconv.Atoi(parts[1]); err == nil {
					cur.Deletions += d
				}
			}
		}
	}
	flush() // final commit if stream ended without trailing blank

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("git: parse walk output: %w", err)
	}
	return records, nil
}

// ── is_agent heuristic (decisions P5) ────────────────────────────────────────
//
// We classify a commit as agent-authored when any of the following match:
//
//  1. Author email ends in "[bot]@users.noreply.github.com" or the author name
//     contains "[bot]".
//  2. The commit message/trailers contain a "Co-Authored-By:" line whose name
//     matches known bot patterns: ends with "[bot]", or contains one of the
//     canonical agent trailer strings (claude, copilot, cursor, devin, codeium,
//     aider, amp, gitstate-agent).
//  3. The commit message contains the string "🤖 Generated with" (Claude Code
//     trailer convention).
//
// This is intentionally conservative: false-negatives (missed agent commits) are
// safer than false-positives (misclassifying human work). New patterns can be
// appended here as new agents emerge.

var agentNamePatterns = []string{
	"[bot]",
	"claude",
	"copilot",
	"cursor",
	"devin",
	"codeium",
	"aider",
	"amp",
	"gitstate-agent",
	"github-actions",
	"dependabot",
}

func isAgentCommit(c CommitRecord) bool {
	// 1. Author signals.
	nameLower := strings.ToLower(c.AuthorName)
	emailLower := strings.ToLower(c.AuthorEmail)

	if strings.Contains(nameLower, "[bot]") {
		return true
	}
	if strings.HasSuffix(emailLower, "[bot]@users.noreply.github.com") {
		return true
	}

	// 2. Co-Authored-By trailers.
	msgLower := strings.ToLower(c.Message)
	for _, line := range strings.Split(c.Message, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "co-authored-by:") {
			for _, pat := range agentNamePatterns {
				if strings.Contains(lower, pat) {
					return true
				}
			}
		}
	}

	// 3. Known bot trailer strings anywhere in message.
	if strings.Contains(msgLower, "🤖 generated with") {
		return true
	}
	if strings.Contains(msgLower, "generated with claude") ||
		strings.Contains(msgLower, "generated by copilot") ||
		strings.Contains(msgLower, "generated by cursor") {
		return true
	}

	return false
}

// ── Diff / DiffRange ──────────────────────────────────────────────────────────

// FileStat holds per-file addition/deletion counts from numstat.
type FileStat struct {
	Path      string
	Additions int
	Deletions int
}

// DiffResult bundles the unified diff text with per-file stats.
type DiffResult struct {
	Unified   string
	FileStats []FileStat
}

// Diff returns the diff for a single commit identified by sha.
// It is equivalent to `git show <sha>`.
func Diff(ctx context.Context, dir, sha string) (*DiffResult, error) {
	if err := validateRef(sha); err != nil {
		return nil, err
	}

	diffCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	// Unified diff.
	uCmd := exec.CommandContext(diffCtx, "git", "-C", dir, "show",
		"--format=", // suppress the commit header
		"-U3",
		sha,
	)
	uOut, err := uCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git: diff %s: %w", sha, err)
	}

	// Numstat.
	nCmd := exec.CommandContext(diffCtx, "git", "-C", dir, "show",
		"--format=",
		"--numstat",
		sha,
	)
	nOut, err := nCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git: numstat %s: %w", sha, err)
	}

	return &DiffResult{
		Unified:   string(uOut),
		FileStats: parseNumstat(string(nOut)),
	}, nil
}

// DiffRange returns the diff between base and head refs (inclusive of head).
// Equivalent to `git diff base..head`.
func DiffRange(ctx context.Context, dir, base, head string) (*DiffResult, error) {
	if err := validateRef(base); err != nil {
		return nil, fmt.Errorf("git: DiffRange base: %w", err)
	}
	if err := validateRef(head); err != nil {
		return nil, fmt.Errorf("git: DiffRange head: %w", err)
	}

	diffCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	rangeArg := base + ".." + head

	uCmd := exec.CommandContext(diffCtx, "git", "-C", dir, "diff", "-U3", rangeArg)
	uOut, err := uCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git: diff range %s: %w", rangeArg, err)
	}

	nCmd := exec.CommandContext(diffCtx, "git", "-C", dir, "diff", "--numstat", rangeArg)
	nOut, err := nCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git: numstat range %s: %w", rangeArg, err)
	}

	return &DiffResult{
		Unified:   string(uOut),
		FileStats: parseNumstat(string(nOut)),
	}, nil
}

// parseNumstat parses the output of `git diff --numstat` or `git show --numstat`.
func parseNumstat(raw string) []FileStat {
	var stats []FileStat
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		fs := FileStat{Path: parts[2]}
		if a, err := strconv.Atoi(parts[0]); err == nil {
			fs.Additions = a
		}
		if d, err := strconv.Atoi(parts[1]); err == nil {
			fs.Deletions = d
		}
		stats = append(stats, fs)
	}
	return stats
}

// ── Blame ──────────────────────────────────────────────────────────────────────

// BlameEntry holds the author for a single source line.
type BlameEntry struct {
	Line        int
	AuthorName  string
	AuthorEmail string
	SHA         string
}

// Blame returns a line-indexed author map for the given file path at HEAD.
// The returned slice is 1-indexed: result[1] = line 1's blame entry.
// Index 0 is always a zero value.
func Blame(ctx context.Context, dir, path string) ([]BlameEntry, error) {
	if strings.Contains(path, "..") || filepath.IsAbs(path) {
		return nil, fmt.Errorf("git: blame: unsafe path %q", path)
	}

	blameCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	// --porcelain emits machine-readable blame with author metadata per hunk.
	cmd := exec.CommandContext(blameCtx, "git", "-C", dir,
		"blame", "--porcelain", "--", path,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git: blame %s: %w", path, err)
	}

	return parseBlamePorcelain(out), nil
}

// parseBlamePorcelain parses `git blame --porcelain` output.
// Porcelain format per hunk:
//
//	<sha> <orig-line> <result-line> [<num-lines>]
//	author <name>
//	author-mail <email>
//	... (other headers)
//	\t<line content>
func parseBlamePorcelain(raw []byte) []BlameEntry {
	// Pre-allocate with index 0 as zero value sentinel.
	entries := []BlameEntry{{}}

	sc := bufio.NewScanner(bytes.NewReader(raw))
	var (
		curSHA   string
		curName  string
		curEmail string
	)

	for sc.Scan() {
		line := sc.Text()
		switch {
		case len(line) == 40 && !strings.Contains(line, " "):
			// Pure SHA line — unlikely; the header always has trailing fields.
		case len(line) > 40 && line[40] == ' ':
			// "<sha> <orig> <result> [<count>]"
			curSHA = line[:40]
			curName = ""
			curEmail = ""
		case strings.HasPrefix(line, "author "):
			curName = strings.TrimPrefix(line, "author ")
		case strings.HasPrefix(line, "author-mail "):
			curEmail = strings.Trim(strings.TrimPrefix(line, "author-mail "), "<>")
		case len(line) > 0 && line[0] == '\t':
			// Code line — emit the entry.
			entries = append(entries, BlameEntry{
				Line:        len(entries), // 1-indexed
				SHA:         curSHA,
				AuthorName:  curName,
				AuthorEmail: curEmail,
			})
		}
	}
	return entries
}

// ── LeadTime ──────────────────────────────────────────────────────────────────

// LeadTime computes the duration from firstCommitAt to mergedAt.
// Returns 0 if either timestamp is zero.
// This is the DORA "lead time for changes" definition used in cycle_times.
func LeadTime(firstCommitAt, mergedAt time.Time) time.Duration {
	if firstCommitAt.IsZero() || mergedAt.IsZero() {
		return 0
	}
	return mergedAt.Sub(firstCommitAt)
}

// ── Internal helpers ──────────────────────────────────────────────────────────

// runCmd runs cmd and wraps stderr into any returned error.
func runCmd(cmd *exec.Cmd, op string) error {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("git: %s: %w — %s", op, err, msg)
		}
		return fmt.Errorf("git: %s: %w", op, err)
	}
	return nil
}

// validateRef rejects ref strings that could be used to inject shell arguments.
// We only permit hex chars, dots, slashes, dashes, underscores, and tildes —
// everything needed for real git refs and SHAs.
func validateRef(ref string) error {
	if ref == "" {
		return fmt.Errorf("git: empty ref")
	}
	for _, ch := range ref {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') ||
			ch == '.' || ch == '/' || ch == '-' || ch == '_' || ch == '~' || ch == '^' {
			continue
		}
		return fmt.Errorf("git: ref %q contains disallowed character %q", ref, ch)
	}
	return nil
}
