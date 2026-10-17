#!/usr/bin/env bash
# Publish the filex.sh/nightly page and the night's desktop packages on the
# site host (task #266). scripts/chain/nightly.mjs (followDesktop) runs it as
# NIGHTLY_SITE_CMD with the directory the page was made in:
#
#   bash nightly-site.sh --ssh <destination> --path <the site directory there> <dir>
#
#   <dir>               index.html (the page) and nightly.json (the night's manifest)
#   NIGHTLY_SITE_REF    the artifact the packages are in (<repo>@sha256:...),
#                       from nightly.mjs: the page and the files it lists are
#                       one artifact
#   NIGHTLY_ORAS_IMAGE  the ORAS client the site host pulls them with (Docker)
#
# The site host does the rest (scripts/chain/nightly-site-remote.sh, sent on
# stdin after its settings - never a script written inline into the ssh
# command): it pulls the packages from ghcr itself (nothing large crosses
# from here), checks them against the SHA256SUMS of the artifact, puts the
# page beside them in <path>/.nightly/<date>/ and points <path>/nightly at
# that directory in one rename; the nights before are removed (ghcr keeps
# the last ones). scripts/sync-site.sh never deletes either path.
#
# Exit: 0 published; 1 the site host refused or failed (what it said is the
# last line); 2 a setting is missing.
set -euo pipefail

SSH_DEST=""
SITE=""
DIR=""
while [ $# -gt 0 ]; do
  case "$1" in
    --ssh) SSH_DEST="${2:?--ssh needs a destination}"; shift 2 ;;
    --path) SITE="${2:?--path needs a directory}"; shift 2 ;;
    -h|--help) awk 'NR > 1 && /^#/ { sub(/^# ?/, ""); print; next } NR > 1 { exit }' "${BASH_SOURCE[0]}"; exit 0 ;;
    -*) echo "unknown option $1" >&2; exit 2 ;;
    *) DIR="$1"; shift ;;
  esac
done
[ -n "$SSH_DEST" ] && [ -n "$SITE" ] && [ -n "$DIR" ] || { echo "usage: nightly-site.sh --ssh <destination> --path <site directory> <dir>" >&2; exit 2; }
case "$SITE" in /?*) ;; *) echo "--path must be an absolute directory, not /" >&2; exit 2 ;; esac
: "${NIGHTLY_SITE_REF:?NIGHTLY_SITE_REF is not set}" "${NIGHTLY_ORAS_IMAGE:?NIGHTLY_ORAS_IMAGE is not set}"
for f in index.html nightly.json; do
  [ -s "$DIR/$f" ] || { echo "$DIR/$f is missing or empty" >&2; exit 2; }
done
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REMOTE="$HERE/nightly-site-remote.sh"
[ -f "$REMOTE" ] || { echo "$REMOTE is missing (install-nightly.sh copies it beside this one)" >&2; exit 2; }

{
  printf 'SITE=%q\n' "$SITE"
  printf 'REF=%q\n' "$NIGHTLY_SITE_REF"
  printf 'ORAS=%q\n' "$NIGHTLY_ORAS_IMAGE"
  printf 'PAGE_B64=%q\n' "$(base64 -w0 < "$DIR/index.html")"
  printf 'JSON_B64=%q\n' "$(base64 -w0 < "$DIR/nightly.json")"
  cat "$REMOTE"
} | ssh -o BatchMode=yes "$SSH_DEST" bash -s
