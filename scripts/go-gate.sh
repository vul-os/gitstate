#!/usr/bin/env bash
# gitstate Go gate — build, vet, gofmt and test the Go tree under cmd/ + internal/.
#
# WHY THIS EXISTS
# ---------------
# gitstate's product code is the Rust workspace under crates/. The Go tree
# (cmd/ + internal/) is the older server-side implementation kept in-tree for a
# staged port — the Makefile says so explicitly and does not build it. That is
# fine as a build policy, but it meant Go files across many packages had NO CI
# at all: nothing compiled them, nothing ran their tests, and a breaking edit
# would land green. This script is that missing gate.
#
# 2026-08-04: the fully-ported domains (contribution, contributors, metrics,
# importer, local git reading) and everything that only ever served the
# abandoned multi-tenant SaaS server (cmd/gitstate, internal/api, admin, auth,
# calendar, capacity, chat, middleware, notifications, oauth, web, webhooks,
# githubapp, docs, jobs, sync, analytics, email, cmd/seed, cmd/seedgit) were
# removed — see decisions.md T11's resolution note. What remains under cmd/ +
# internal/ is genuinely NOT YET PORTED (report/NL→report, calibration,
# semantic search/embed, agent_runs+context_bundle inside internal/store) plus
# the scaffolding (config/db/crypto/llm/gitanalysis) and standalone CLIs
# (gittrack, gitstate-mcp) it needs to keep building. The floors below were
# re-measured against that smaller tree; they are not the original 243/37/29.
#
# FAIL-CLOSED CONTRACT
# --------------------
# Every step below either passes on real work or exits non-zero. There is no
# skip path. On top of the tool exit codes, the gate asserts COVERAGE COUNTS —
# how many packages were enumerated, how many .go files were format-checked,
# and how many packages actually reported `ok` from `go test`. A gate that
# silently stops seeing code (a bad path, a moved directory, a `go list` that
# matches nothing) fails here instead of passing by doing nothing.
#
# Raise the MIN_* floors when the tree genuinely grows; never lower them to get
# green.
#
# DB-GATED TESTS ARE NOT FREE COVERAGE
# -------------------------------------
# ~46 tests across this tree (internal/store by far the largest, plus
# calibration and gitanalysis) call `t.Skip(...)` when DATABASE_URL is unset.
# `go test` still prints `ok` for a package where every test skipped — indistinguishable
# from a package that actually ran and passed, in the `ok_count` check above.
# This gate does NOT let that pass as coverage: it re-runs with `-v`, counts
# `--- SKIP` lines explicitly, and asserts that count (see step 6 below) rather
# than trusting the package-level `ok`. With DATABASE_URL set, zero skips are
# tolerated — a skip there means the DB is misconfigured, not merely absent.
# Without it, the skip count must match EXPECTED_DB_SKIPPED_TESTS exactly, so a
# newly-introduced (or newly-vanished) skip is never silent.
#
# Usage:
#   scripts/go-gate.sh              # build, vet, gofmt, test -race
#   NO_RACE=1 scripts/go-gate.sh    # same without the race detector
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# Explicit package scope. NOT `./...`: the repo root holds go.mod, so `./...`
# also matches any stray Go file vendored inside web/node_modules (the `flatted`
# package ships one). Scoping to the real trees keeps the gate deterministic
# whether or not the frontend has been installed.
#
# `internal/` is gone as of the T11 final cleanup wave (2026-08-05) — every
# domain and every piece of scaffolding under it has been ported or deleted.
# Only cmd/migrate is left, and it goes away in the same commit as this
# script, go.mod, go.sum and migrations/. PKGS is `./cmd/...` only for this
# one intermediate commit.
PKGS=(./cmd/...)

# Coverage floors — re-measured 2026-08-04 after the SaaS-only and
# fully-ported packages were removed (decisions.md T11 resolution note). The
# tree used to be 243 files / 37 packages / 29 tested; it is now the
# not-yet-ported reference (report, calibration, embed/search, and
# internal/store's still-unported files) plus the scaffolding it needs
# (config/db/crypto/llm/gitanalysis) and two standalone CLIs (gittrack,
# gitstate-mcp).
MIN_PKGS="${MIN_PKGS:-1}"          # packages under cmd/ (internal/ is gone)
MIN_TESTED_PKGS="${MIN_TESTED_PKGS:-0}"  # of those, ones carrying _test.go
MIN_GO_FILES="${MIN_GO_FILES:-1}" # .go files handed to gofmt

# Exact, not a floor: how many top-level Go tests are expected to `t.Skip` when
# no DATABASE_URL is set. Measured directly with `go test -v` across
# ./cmd/... ./internal/... on a clean tree (2026-08-04, post Go-removal) — see
# the "DB-GATED TESTS" note above. Update this deliberately (with a comment
# saying why) if a DB-gated test is genuinely added or removed; never bump it
# to silence a failure without knowing which test moved.
EXPECTED_DB_SKIPPED_TESTS="${EXPECTED_DB_SKIPPED_TESTS:-0}"

fail() { echo "GO GATE FAILED: $*" >&2; exit 1; }
step() { CURRENT_STEP="$*"; printf '\n\033[1m── %s\033[0m\n' "$*"; }

# Any command dying under `set -e` must still say which step it was, so a raw
# toolchain error (a moved directory, an unresolvable module) never reads as an
# unexplained exit.
CURRENT_STEP="startup"
trap 'rc=$?; [ "$rc" -eq 0 ] || echo "GO GATE FAILED during: ${CURRENT_STEP} (exit ${rc})" >&2' ERR

# ── 0. Preflight: local `replace` targets must resolve ───────────────────────
# go.mod carries `replace github.com/llmux/llmux => ../../vulos/llmux`, a
# filesystem path outside this repo. If it is absent every later step fails with
# a module-resolution error that reads like a network problem; name it here
# instead so CI says what is actually missing.
step "preflight: go.mod filesystem replace targets"
replaces="$(awk '
  /^replace[[:space:]]*\(/ { blk = 1; next }
  blk && /^\)/             { blk = 0; next }
  (blk || /^replace[[:space:]]/) && /=>/ {
    i = index($0, "=>")
    rhs = substr($0, i + 2)
    gsub(/^[[:space:]]+|[[:space:]]+$/, "", rhs)
    split(rhs, parts, /[[:space:]]+/)
    print parts[1]
  }
' go.mod | grep -E '^(\.|/)' || true)"

if [ -z "$replaces" ]; then
  echo "  (none)"
else
  while IFS= read -r target; do
    [ -n "$target" ] || continue
    if [ -d "$target" ]; then
      echo "  ok   $target -> $(cd "$target" && pwd)"
    else
      fail "go.mod replace target does not exist: $target
    Resolved from: $ROOT
    In CI this repo and its sibling must be checked out side by side; see the
    'go' job in .github/workflows/ci.yml."
    fi
  done <<<"$replaces"
fi

# ── 1. Enumerate packages (and assert we can still see them) ─────────────────
step "go list ${PKGS[*]}"
pkg_list="$(go list "${PKGS[@]}")"
pkg_count="$(printf '%s\n' "$pkg_list" | grep -c .)"
echo "  $pkg_count packages"
[ "$pkg_count" -ge "$MIN_PKGS" ] ||
  fail "expected >= $MIN_PKGS Go packages, found $pkg_count (did a directory move, or did the pattern stop matching?)"

if printf '%s\n' "$pkg_list" | grep -q 'node_modules'; then
  fail "package list leaked into node_modules — the scope pattern is wrong:
$(printf '%s\n' "$pkg_list" | grep 'node_modules')"
fi

tested_list="$(go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' "${PKGS[@]}" | grep -c . || true)"
echo "  $tested_list packages carry tests"
[ "$tested_list" -ge "$MIN_TESTED_PKGS" ] ||
  fail "expected >= $MIN_TESTED_PKGS packages with tests, found $tested_list (were test files deleted?)"

# ── 2. Build ─────────────────────────────────────────────────────────────────
step "go build"
go build "${PKGS[@]}"
echo "  ok"

# ── 3. Vet ───────────────────────────────────────────────────────────────────
step "go vet"
go vet "${PKGS[@]}"
echo "  ok"

# ── 4. gofmt ─────────────────────────────────────────────────────────────────
# gofmt -l prints nothing both when everything is formatted AND when it was
# handed no files at all, so count the files first and assert the count.
step "gofmt"
go_files=()
while IFS= read -r f; do go_files+=("$f"); done < <(find cmd -type f -name '*.go' | sort)
echo "  ${#go_files[@]} .go files"
[ "${#go_files[@]}" -ge "$MIN_GO_FILES" ] ||
  fail "expected >= $MIN_GO_FILES .go files to format-check, found ${#go_files[@]} (gofmt would have passed by checking nothing)"

unformatted="$(gofmt -l "${go_files[@]}")"
if [ -n "$unformatted" ]; then
  fail "gofmt reports unformatted files (run: gofmt -w cmd):
$unformatted"
fi
echo "  ok"

# ── 5. Test ──────────────────────────────────────────────────────────────────
# -race is sound across this tree (verified clean); NO_RACE=1 exists only for
# platforms without race-detector support, and prints what it gave up.
step "go test"
race=(-race)
if [ -n "${NO_RACE:-}" ]; then
  race=()
  echo "  NOTE: NO_RACE=1 — data races are NOT being checked in this run."
fi

test_log="$(mktemp)"
trap 'rm -f "$test_log"' EXIT
set +e
# -v is required, not cosmetic: without it a package where every test skipped
# and a package that genuinely ran both print only `ok  <pkg>  <time>`, so the
# skip-coverage step below would have nothing to count. See "DB-GATED TESTS"
# above.
go test "${race[@]}" -v "${PKGS[@]}" 2>&1 | tee "$test_log"
test_status="${PIPESTATUS[0]}"
set -e
[ "$test_status" -eq 0 ] || fail "go test exited $test_status"

ok_count="$(grep -c '^ok  ' "$test_log" || true)"
echo "  $ok_count packages reported ok"
[ "$ok_count" -ge "$MIN_TESTED_PKGS" ] ||
  fail "only $ok_count packages reported ok, expected >= $MIN_TESTED_PKGS — go test exited 0 without running the suite"

# ── 6. Skip-coverage assertion ──────────────────────────────────────────────
# A package reporting `ok` proves nothing about what ran INSIDE it: `go test`
# prints `ok` whether every test executed or every test called t.Skip. Count
# top-level test outcomes directly instead of trusting the package verdict.
# (Anchored on column 0 so indented `--- SKIP`/`--- PASS` lines from t.Run
# subtests are not double-counted against their parent.)
step "skip-coverage assertion (DB-gated tests must not silently count as tested)"
skip_count="$(grep -c '^--- SKIP' "$test_log" || true)"
passed_count="$(grep -c '^--- PASS' "$test_log" || true)"
failed_count="$(grep -c '^--- FAIL' "$test_log" || true)"
echo "  $passed_count top-level tests passed, $skip_count skipped, $failed_count failed"

if [ -n "${DATABASE_URL:-}" ]; then
  # A database is configured — every DB-gated test is expected to actually
  # run. Any skip here means the connection/migrations/role grants are wrong,
  # not merely "no DB available", and that must fail loudly rather than ride
  # the package's `ok` to a green gate.
  [ "$skip_count" -eq 0 ] ||
    fail "$skip_count tests SKIPPED even though DATABASE_URL is set — the DB-backed
    suite is not actually exercising the database (bad connection string,
    unmigrated schema, or a role/grant mismatch against provision-db.sh).
    'go test' still exited 0 and every affected package still printed 'ok'.
    Skipped:
$(grep -B1 '^--- SKIP' "$test_log" | grep -E '_test\.go:[0-9]+:' | sed 's/^/      /')"
  echo "  ok — DATABASE_URL set and zero DB-gated tests skipped"
else
  echo "  NOTE: DATABASE_URL not set — $skip_count DB-backed integration tests"
  echo "        SKIPPED, not run (internal/store, mostly, plus calibration; see"
  echo "        the DB-GATED TESTS note at the top of this file). This gate does"
  echo "        NOT count that as coverage — it asserts the exact skip count below"
  echo "        instead of trusting the packages' 'ok'. CI sets DATABASE_URL (+"
  echo "        ADMIN_DATABASE_URL) against a real Postgres service so these run"
  echo "        for real; see the 'go' job in .github/workflows/ci.yml."
  [ "$skip_count" -eq "$EXPECTED_DB_SKIPPED_TESTS" ] ||
    fail "expected exactly $EXPECTED_DB_SKIPPED_TESTS DB-gated tests to skip with no
    DATABASE_URL, found $skip_count. A number that moved — up OR down — means a
    t.Skip appeared or vanished somewhere in this tree since EXPECTED_DB_SKIPPED_TESTS
    was set, and that is worth a human look before trusting this run:
$(grep -B1 '^--- SKIP' "$test_log" | grep -E '_test\.go:[0-9]+:' | sed 's/^/      /')"
fi

if [ "$skip_count" -gt 0 ]; then
  coverage_note=" — ${skip_count} DB-gated tests SKIPPED (NOT full coverage; set DATABASE_URL)"
else
  coverage_note=" — 0 tests skipped"
fi
printf '\n\033[32mGO GATE PASSED\033[0m — %s packages built + vetted, %s files gofmt-clean, %s packages tested%s, %s tests passed%s\n' \
  "$pkg_count" "${#go_files[@]}" "$ok_count" "$([ -n "${NO_RACE:-}" ] && echo ' (no race detector)' || echo ' with -race')" \
  "$passed_count" "$coverage_note"
