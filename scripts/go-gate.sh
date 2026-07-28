#!/usr/bin/env bash
# gitstate Go gate — build, vet, gofmt and test the Go tree under cmd/ + internal/.
#
# WHY THIS EXISTS
# ---------------
# gitstate's product code is the Rust workspace under crates/. The Go tree
# (cmd/ + internal/) is the older server-side implementation kept in-tree for a
# staged port — the Makefile says so explicitly and does not build it. That is
# fine as a build policy, but it meant 243 Go files across 37 packages (29 of
# them with tests) had NO CI at all: nothing compiled them, nothing ran their
# tests, and a breaking edit would land green. This script is that missing gate.
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
PKGS=(./cmd/... ./internal/...)

# Coverage floors — measured against the tree at the time this gate was added.
MIN_PKGS="${MIN_PKGS:-37}"          # packages under cmd/ + internal/
MIN_TESTED_PKGS="${MIN_TESTED_PKGS:-29}"  # of those, ones carrying _test.go
MIN_GO_FILES="${MIN_GO_FILES:-243}" # .go files handed to gofmt

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
while IFS= read -r f; do go_files+=("$f"); done < <(find cmd internal -type f -name '*.go' | sort)
echo "  ${#go_files[@]} .go files"
[ "${#go_files[@]}" -ge "$MIN_GO_FILES" ] ||
  fail "expected >= $MIN_GO_FILES .go files to format-check, found ${#go_files[@]} (gofmt would have passed by checking nothing)"

unformatted="$(gofmt -l "${go_files[@]}")"
if [ -n "$unformatted" ]; then
  fail "gofmt reports unformatted files (run: gofmt -w cmd internal):
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
go test "${race[@]}" "${PKGS[@]}" 2>&1 | tee "$test_log"
test_status="${PIPESTATUS[0]}"
set -e
[ "$test_status" -eq 0 ] || fail "go test exited $test_status"

ok_count="$(grep -c '^ok  ' "$test_log" || true)"
echo "  $ok_count packages reported ok"
[ "$ok_count" -ge "$MIN_TESTED_PKGS" ] ||
  fail "only $ok_count packages reported ok, expected >= $MIN_TESTED_PKGS — go test exited 0 without running the suite"

printf '\n\033[32mGO GATE PASSED\033[0m — %s packages built + vetted, %s files gofmt-clean, %s packages tested%s\n' \
  "$pkg_count" "${#go_files[@]}" "$ok_count" "$([ -n "${NO_RACE:-}" ] && echo ' (no race detector)' || echo ' with -race')"
