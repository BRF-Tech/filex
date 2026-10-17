#!/usr/bin/env bash
#
# The Go gate of a patch release (`pnpm release X.Y.Z --profile patch`,
# scripts/release/plan.mjs): go vet and go test on the packages the patch
# changed and on every package that imports one of them - directly, through
# another package, or from its tests - instead of the whole module.
#
#   FILEX_GO_DIRS   the changed package directories, relative to the module and
#                   space-separated ("./internal/foo ./cmd/filex"), or "./..."
#                   for the whole module. scripts/release/checks.mjs
#                   (goPackageDirs) says which; empty means the whole module.
#   FILEX_GO_RACE   1: the tests run under -race (#252: a server patch's gate,
#                   which does not wait for GitHub's -race parts). The race
#                   detector needs cgo and a C compiler: without one this says
#                   so and fails, it never tests without -race instead.
#
# Runs in the Go module: the release's goGate changes into it (under WSL, into
# its mirror on WSL's own disk). GitHub still runs the whole suite on the
# export commit (#165, decision 5); this is the patch's early answer, never
# the only one.
#
# ⚠ In doubt it tests more, never less: a directory `go list` cannot place,
# or a list that comes out empty, means the whole module.
set -euo pipefail

dirs="${FILEX_GO_DIRS:-./...}"
race=()
whole_timeout=30m
if [ "${FILEX_GO_RACE:-}" = 1 ]; then
  export CGO_ENABLED=1
  cc="$(go env CC)"
  command -v "${cc%% *}" >/dev/null 2>&1 || { echo "-race needs a C compiler and there is none here (go env CC: ${cc:-unset}): install one (gcc), or run the patch as --profile patch" >&2; exit 1; }
  race=(-race)
  whole_timeout=60m
fi

whole() {
  echo "the whole module: $1"
  go vet ./...
  exec go test ${race[@]+"${race[@]}"} -timeout "$whole_timeout" ./...
}

[ "$dirs" = "./..." ] && whole "nothing narrower was named"

graph="$(mktemp)"
trap 'rm -f "$graph"' EXIT

# shellcheck disable=SC2086
if ! changed="$(go list -e $dirs | tr '\n' ' ')"; then
  whole "go list could not place $dirs"
fi
[ -n "${changed// /}" ] || whole "go list placed nothing in $dirs"

# One line per package: its import path, every package it depends on
# (transitively, tests aside), and what its tests import.
go list -e -f '{{.ImportPath}}|{{join .Deps " "}}|{{join .TestImports " "}} {{join .XTestImports " "}}' ./... > "$graph"

# First pass: the packages that are, or depend on, a changed one. Second pass:
# those, and every package whose tests import one of them.
targets="$(awk -F'|' -v want="$changed" '
  BEGIN { n = split(want, w, " "); for (i = 1; i <= n; i++) c[w[i]] = 1 }
  NR == FNR {
    hit = ($1 in c)
    m = split($2, d, " ")
    for (i = 1; i <= m && !hit; i++) if (d[i] in c) hit = 1
    if (hit) s[$1] = 1
    next
  }
  ($1 in s) { print $1; next }
  {
    m = split($3, d, " ")
    for (i = 1; i <= m; i++) if (d[i] in s) { print $1; next }
  }
' "$graph" "$graph" | sort -u)"

[ -n "$targets" ] || whole "no package in ./... is or imports $changed"

echo "changed: $changed"
echo "testing $(printf '%s\n' "$targets" | wc -l | tr -d ' ') package(s), the changed ones and their importers:"
printf '  %s\n' $targets

# shellcheck disable=SC2086
go vet $targets
# shellcheck disable=SC2086
go test ${race[@]+"${race[@]}"} -timeout 30m $targets
