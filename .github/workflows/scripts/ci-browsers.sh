#!/usr/bin/env bash
# The browsers a Playwright part runs, with the system libraries they need,
# for ci.yml's browser parts:
#
#   bash .github/workflows/scripts/ci-browsers.sh <package dir> <engine>...
#
# What `playwright install --with-deps <engine>...` does, without waiting on
# one Ubuntu mirror (task #306):
#
#   1. the package files the last run of this part fetched (<package dir>,
#      which ci.yml's "Cache the browsers' system packages" keeps per engine)
#      go into apt's own cache: apt takes a file from there when its name,
#      version and checksum are the index's, and fetches only what is newer;
#   2. apt fetches the rest (`--download-only`) in two tries, each with a
#      limit of its own. The runner's sources read /etc/apt/apt-mirrors.txt,
#      whose first mirror is Azure's: the second try goes to the mirrors
#      after it;
#   3. apt installs from its cache, Playwright fetches the browser itself
#      (`playwright install`, from its own download host), and the package
#      files go back to <package dir> for the cache to keep.
#
# ⚠ Why (v0.58.1, v0.58.2 and v0.59.0; runs 42066697121, 42167295823 and
# 42290540824): apt-get update read the index in a second, and the packages
# came from azure.archive.ubuntu.com at 40-60 kB/s - Firefox's 49 MB and
# WebKit's 114 MB never arrived inside the step's 10 minutes. All six red
# parts ran in westus2, the parts beside them elsewhere took 20-50 seconds,
# and a re-run that landed in westus2 again was red again.
#
# The package list is Playwright's own (`install-deps --dry-run`). A dry run
# this script cannot read falls back to `playwright install --with-deps`,
# with a warning. ciFullMatrix.test.ts runs it with stand-ins for sudo,
# apt-get and pnpm, and points CI_APT_ARCHIVES, CI_APT_MIRRORS and
# CI_APT_LIMITS (the two tries' limits, in seconds) at its own.
set -euo pipefail

debs="${1:-}"
if [ -z "$debs" ] || [ "$#" -lt 2 ]; then
  echo "usage: bash .github/workflows/scripts/ci-browsers.sh <package dir> <engine>..." >&2
  exit 2
fi
shift
archives="${CI_APT_ARCHIVES:-/var/cache/apt/archives}"
mirrors="${CI_APT_MIRRORS:-/etc/apt/apt-mirrors.txt}"
read -r first second <<< "${CI_APT_LIMITS:-150 270}"
azure='azure\.archive\.ubuntu\.com'

playwright() { pnpm --dir e2e exec playwright "$@"; }

plan=$(playwright install-deps --dry-run "$@")
pkgs=$(sed -n 's/.*--no-install-recommends \([^"]*\)".*/\1/p' <<< "$plan")
if [ -z "$pkgs" ]; then
  echo "::warning::no package list in 'playwright install-deps --dry-run': $plan"
  playwright install --with-deps "$@"
  exit 0
fi

mkdir -p "$debs"
kept=$(find "$debs" -maxdepth 1 -name '*.deb' | wc -l)
if [ "$kept" -gt 0 ]; then
  sudo find "$debs" -maxdepth 1 -name '*.deb' -exec mv -t "$archives" {} +
fi
echo "package files from the last run: $kept"

# shellcheck disable=SC2086 # the list is one word per package
fetch() {
  sudo timeout --kill-after=15 "$1" sh -c 'apt-get -q update && apt-get -q install -y --no-install-recommends --download-only "$@"' sh $pkgs
}

if ! fetch "$first"; then
  echo "::warning::apt did not fetch the system libraries in ${first}s; trying the mirrors after Azure's"
  if [ -f "$mirrors" ] && grep -q "$azure" "$mirrors" && grep -v "$azure" "$mirrors" | grep -q .; then
    sudo sed -i "/$azure/d" "$mirrors"
  fi
  if ! fetch "$second"; then
    echo "::error::apt did not fetch the system libraries from any mirror ($first s, then $second s)"
    exit 1
  fi
fi

# shellcheck disable=SC2086
sudo apt-get -q install -y --no-install-recommends $pkgs
playwright install "$@"

sudo apt-get -q autoclean
find "$archives" -maxdepth 1 -name '*.deb' -exec cp -t "$debs" {} +
echo "package files kept for the next run: $(find "$debs" -maxdepth 1 -name '*.deb' | wc -l)"
