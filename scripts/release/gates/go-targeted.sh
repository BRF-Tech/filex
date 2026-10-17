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
par=()
timeout=30m
if [ "${FILEX_GO_RACE:-}" = 1 ]; then
  export CGO_ENABLED=1
  cc="$(go env CC)"
  command -v "${cc%% *}" >/dev/null 2>&1 || { echo "-race needs a C compiler and there is none here (go env CC: ${cc:-unset}): install one (gcc), or run the patch as --profile patch" >&2; exit 1; }
  race=(-race)
  # Under -race a package takes several times the CPU and memory, and the
  # pretag builds both images beside this gate: the run gets an hour, the
  # targeted one as the whole one, and half the cores for its packages. On
  # v0.57.1's first round (load 60) the handlers package rang the 30-minute
  # alarm 17 seconds into a test, and three timing tests missed their bounds.
  timeout=60m
  cores="$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 4)"
  par=(-p "$(( cores / 2 > 2 ? cores / 2 : 2 ))")
fi

whole() {
  echo "the whole module: $1"
  go vet ./...
  exec go test ${race[@]+"${race[@]}"} ${par[@]+"${par[@]}"} -timeout "$timeout" ./...
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

# Without -race, one run. Under -race a package CI splits by -run
# (scripts/test-shards.json, the race profile: the handlers package in eight
# shards) runs as CI runs it, its shards side by side; one process for it
# took over an hour on v0.57.1's second round, its tests one after another
# on an idle machine. The other targets run together as before.
shards_tool="${FILEX_CHECKOUT:-$(cd .. && pwd)}/scripts/test-shards.mjs"
if [ ${#race[@]} -eq 0 ] || [ ! -f "$shards_tool" ] || ! command -v node >/dev/null 2>&1; then
  # shellcheck disable=SC2086
  exec go test ${race[@]+"${race[@]}"} ${par[@]+"${par[@]}"} -timeout "$timeout" $targets
fi

mod="$(go list -m)"
split_names=()
split_pkgs=" "
for name in $(node "$shards_tool" go --profile race --format matrix 2>/dev/null | grep -o '"shard":"[^"]*"' | sed 's/"shard":"//; s/"$//'); do
  mapfile -t a < <(node "$shards_tool" go --profile race --format args --shard "$name" 2>/dev/null)
  [ "${#a[@]}" -eq 3 ] && [ "${a[0]}" = "-run" ] || continue
  case "${a[2]}" in ./*) ;; *) continue ;; esac
  pkg="$mod/${a[2]#./}"
  printf '%s\n' $targets | grep -qxF "$pkg" || continue
  split_names+=("$name")
  case "$split_pkgs" in *" $pkg "*) ;; *) split_pkgs="$split_pkgs$pkg " ;; esac
done

rest=""
for t in $targets; do
  case "$split_pkgs" in *" $t "*) ;; *) rest="$rest $t" ;; esac
done

logs="$(mktemp -d)"
trap 'rm -f "$graph"; rm -rf "$logs"' EXIT
slots=$(( cores / 4 > 2 ? cores / 4 : 2 ))
pids=()
labels=()
run() {
  local label="$1"; shift
  while [ "$(jobs -rp | wc -l)" -ge "$slots" ]; do wait -n || true; done
  ( go test -race -timeout "$timeout" "$@" > "$logs/$label.log" 2>&1; echo $? > "$logs/$label.rc" ) &
  pids+=("$!")
  labels+=("$label")
}

echo "under -race: $(printf '%s ' "${split_names[@]}")side by side, the other $(printf '%s\n' $rest | grep -c .) package(s) together"
# shellcheck disable=SC2086
[ -n "${rest// /}" ] && run rest ${par[@]+"${par[@]}"} $rest
for name in "${split_names[@]}"; do
  mapfile -t a < <(node "$shards_tool" go --profile race --format args --shard "$name" 2>/dev/null)
  run "$name" "${a[@]}"
done
wait "${pids[@]}" || true

failed=0
for label in "${labels[@]}"; do
  rc="$(cat "$logs/$label.rc" 2>/dev/null || echo 1)"
  echo "=== $label: exit $rc"
  cat "$logs/$label.log"
  [ "$rc" = 0 ] || failed=1
done
exit "$failed"
