#!/usr/bin/env bash
# Whether the release leaves a winget package alone because its first
# submission waits for a moderator. It reads, and changes nothing on winget.
#
#   GH_TOKEN=<brkfun's WINGET_TOKEN> winget-pending.sh <PackageIdentifier> <version>
#
# ⚠ Why (task #68, the owner's decision of 2026-10-16): a package's first
# submission ("New-Package") waits in microsoft/winget-pkgs' moderator queue,
# and every release opened a new pull request and closed the one before
# (winget-supersede.sh): fourteen pull requests of BRFTech.filex-app passed
# validation, and none was ever approved - the queue started again with each.
# Until winget has the package, the release does not touch it at all: no new
# pull request, no update of the open one, no closing, no comment.
#
# What it says:
#   - the package is on winget-pkgs' master (manifests/<p>/<Publisher>/<Name>):
#     nothing - the release submits <version> as a new version, as the CLI's;
#   - it is not, and one of our pull requests for it is open: the line
#     `skipped: <id> waits for a moderator on #<number>` (also a notice and a
#     line of the run's summary), `pending=<number>` to $GITHUB_OUTPUT and
#     WINGET_DESKTOP_SKIP to $GITHUB_ENV - the steps that submit, sign the CLA
#     and close are skipped by it;
#   - it is not, and none is open: nothing - the release opens the first;
#   - it cannot tell (GitHub not answering): the line `skipped: could not ask
#     whether <id> is on winget`, and WINGET_DESKTOP_SKIP too - not touching
#     a first submission that may be waiting is the safe side.
#
# Never red: every answer is a line, and the run goes on.
set -uo pipefail

id="${1:?the winget PackageIdentifier}"
ver="${2:?the version the release submits}"
repo=microsoft/winget-pkgs

publisher="${id%%.*}"
name="${id#*.}"
first=$(printf '%s' "${id:0:1}" | tr '[:upper:]' '[:lower:]')
path="manifests/$first/$publisher/$name"

say() { echo "winget-pending: $*"; }
skip() {
  local line
  line=$(printf '%s' "$1" | tr '\r\n' '  ')
  echo "$line"
  echo "::notice title=winget::$line"
  if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then echo "### winget: $line" >> "$GITHUB_STEP_SUMMARY"; fi
  if [ -n "${GITHUB_ENV:-}" ]; then echo "WINGET_DESKTOP_SKIP=$line" >> "$GITHUB_ENV"; fi
  if [ -n "${2:-}" ] && [ -n "${GITHUB_OUTPUT:-}" ]; then echo "pending=$2" >> "$GITHUB_OUTPUT"; fi
  exit 0
}

if answer=$(gh api "repos/$repo/contents/$path" --jq 'length' 2>&1); then
  say "$id is on winget ($path, $answer version(s)): the release submits $ver as a new version"
  exit 0
fi
if ! grep -q 'HTTP 404' <<< "$answer"; then
  skip "skipped: could not ask whether $id is on winget ($answer) - its first submission may be waiting, so nothing is submitted, signed or closed"
fi

# Our open pull requests for exactly this package, oldest first.
open=$(gh pr list --repo "$repo" --state open --author "@me" --limit 100 --search "$id in:title" \
  --json number,title,createdAt --jq 'sort_by(.createdAt) | .[] | "\(.number)\t\(.title)"' 2>/dev/null)
while IFS=$'\t' read -r num t; do
  [ -n "$num" ] || continue
  case "$t" in
    "New version: $id "*|"New package: $id version "*|"New package: $id "*)
      skip "skipped: $id waits for a moderator on #$num" "$num"
      ;;
  esac
done <<< "$open"
say "$id is not on winget yet and no pull request of ours is open for it: the release opens its first"
exit 0
