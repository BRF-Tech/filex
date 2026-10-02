#!/usr/bin/env bash
# Close our own older, still-open winget pull requests for a package once the
# release has opened the new one.
#
#   GH_TOKEN=<brkfun's WINGET_TOKEN> winget-supersede.sh <PackageIdentifier> <version>
#
# ⚠ Why: microsoft/winget-pkgs reviews slowly, and every release opens a pull
# request of its own, so they pile up — on 2026-09-28 BRFTech.filex-app had
# 0.44.2, 0.46.1 and 0.47.0 open at once (#441070, #441715, #441927), each a
# reviewer's time spent on a version nobody should install any more. Only the
# newest one is worth reviewing (the maintainer, 2026-09-28).
#
# Rules:
#   - the NEW pull request ("New version: <id> <version>") must exist and be
#     open, or nothing is closed: a release that failed to submit must not
#     leave the package with no pull request at all;
#   - only pull requests opened by this account (--author @me), whose title
#     names exactly <id> — BRFTech.filex never matches BRFTech.filex-app;
#   - only versions strictly LOWER than <version>, compared as semver (a
#     pre-release sorts below its release, 0.10.0 above 0.9.0);
#   - each is closed with "Superseded by #<new>".
# Nothing here fails the release: a pull request left open is a warning.
set -uo pipefail

# semver_lt A B: true when A < B. MAJOR.MINOR.PATCH numerically, then a
# version without a pre-release above the same one with it, then the
# pre-release identifiers dot by dot (numeric ones numerically).
semver_lt() {
  local a="${1#v}" b="${2#v}"
  local ac="${a%%-*}" bc="${b%%-*}" ap="" bp=""
  [ "$ac" != "$a" ] && ap="${a#*-}"
  [ "$bc" != "$b" ] && bp="${b#*-}"
  local IFS=.
  local -a x=($ac) y=($bc)
  local i
  for i in 0 1 2; do
    local p="${x[$i]:-0}" q="${y[$i]:-0}"
    [[ "$p" =~ ^[0-9]+$ && "$q" =~ ^[0-9]+$ ]] || return 1
    if ((10#$p < 10#$q)); then return 0; fi
    if ((10#$p > 10#$q)); then return 1; fi
  done
  [ -z "$ap" ] && return 1
  [ -z "$bp" ] && return 0
  local -a s=($ap) t=($bp)
  for ((i = 0; i < ${#s[@]} || i < ${#t[@]}; i++)); do
    [ "$i" -ge "${#s[@]}" ] && return 0
    [ "$i" -ge "${#t[@]}" ] && return 1
    local u="${s[$i]}" w="${t[$i]}"
    if [[ "$u" =~ ^[0-9]+$ && "$w" =~ ^[0-9]+$ ]]; then
      if ((10#$u < 10#$w)); then return 0; fi
      if ((10#$u > 10#$w)); then return 1; fi
    elif [[ "$u" =~ ^[0-9]+$ ]]; then return 0
    elif [[ "$w" =~ ^[0-9]+$ ]]; then return 1
    elif [[ "$u" < "$w" ]]; then return 0
    elif [[ "$u" > "$w" ]]; then return 1
    fi
  done
  return 1
}

# `--semver-lt A B` exposes the comparison alone (the guard test drives it).
[ "${1:-}" = "--semver-lt" ] && { semver_lt "${2:?}" "${3:?}"; exit $?; }

id="${1:?the winget PackageIdentifier}"
ver="${2:?the version the release just submitted}"
repo=microsoft/winget-pkgs
wait="${WINGET_SUPERSEDE_WAIT:-15}"
tries="${WINGET_SUPERSEDE_TRIES:-20}"

title="New version: $id $ver"
new=""
for _ in $(seq 1 "$tries"); do
  new=$(gh pr list --repo "$repo" --state open --author "@me" --search "\"$title\" in:title" \
    --json number,title --jq ".[] | select(.title == \"$title\") | .number" 2>/dev/null | head -1)
  [ -n "$new" ] && break
  sleep "$wait"
done
if [ -z "$new" ]; then
  echo "::warning title=winget::no open pull request titled \"$title\"; the older ones are left open."
  exit 0
fi

# Every open pull request of ours that names this package, as "number<TAB>title".
open=$(gh pr list --repo "$repo" --state open --author "@me" --limit 100 --search "$id in:title" \
  --json number,title --jq '.[] | "\(.number)\t\(.title)"' 2>/dev/null)
if [ -z "$open" ]; then
  echo "winget: no other open pull request for $id"
  exit 0
fi

closed=0
while IFS=$'\t' read -r num t; do
  [ -n "$num" ] || continue
  [ "$num" = "$new" ] && continue
  old=""
  case "$t" in
    "New version: $id "*) old="${t#"New version: $id "}" ;;
    "New package: $id version "*) old="${t#"New package: $id version "}" ;;
    *) continue ;;
  esac
  [[ "$old" =~ ^v?[0-9]+(\.[0-9]+){0,2}(-[0-9A-Za-z.-]+)?$ ]] || continue
  semver_lt "$old" "$ver" || continue
  if gh pr close "$num" --repo "$repo" --comment "Superseded by #$new" >/dev/null 2>&1; then
    echo "winget #$num ($id $old): closed, superseded by #$new"
    closed=$((closed + 1))
  else
    echo "::warning title=winget::could not close #$num ($id $old); close it by hand (superseded by #$new)."
  fi
done <<< "$open"
echo "winget: $closed older pull request(s) for $id closed in favour of #$new"
exit 0
