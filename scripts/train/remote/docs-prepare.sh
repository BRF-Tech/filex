# The first half of the ship's docs step, run on the server after common.sh
# (see there): a copy of the docs site's source snapshot, then its docs/ out
# of the way for the release's pages.
#
#   docs-prepare.sh SNAPSHOT_DIR STAMP
#
# The ship then copies docs/, README.md, CHANGELOG.md and docs-site/ from the
# PUBLIC checkout into SNAPSHOT_DIR, and the hand-written release summaries
# from this repository, and runs the site's refresh. docs-site/ is not
# emptied: the refresh builds there with the node_modules the server keeps.
#
# ⚠ Why the copy comes first (CONTRIBUTING, Release process, step 11): the
# snapshot is not a git checkout, so the copy is the only way back to what
# the site was built from yesterday.
#
# ⚠ Only the two newest copies stay, this one included. Each copy carries
# docs-site/node_modules, and nothing used to remove them: measured
# 2026-10-29, 34 copies (7.8 GB) had piled up on the server since
# 2026-09-05. Older ones go one at a time, each by its full name, and only
# when that name is exactly SNAPSHOT_DIR.bak-YYYYMMDD-HHMMSS (a .tgz or a
# copy named by hand is left alone). Newest is decided by the stamp in the
# name, not by mtime: `cp -a` keeps the snapshot's own mtime, so `ls -t`
# would order the copies by when the site last changed.

set -euo pipefail

KEEP_COPIES=2

src="${1:?the docs snapshot directory}"
stamp="${2:?a stamp for the copy}"
case "$src" in
  /*/*) ;;
  *) die "the docs snapshot '$src' must be an absolute path below /" ;;
esac
case "$stamp" in
  [0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]-[0-9][0-9][0-9][0-9][0-9][0-9]) ;;
  *) die "the stamp '$stamp' is not YYYYMMDD-HHMMSS" ;;
esac
[ -d "$src/docs-site" ] || die "$src is not the docs site's snapshot (it has no docs-site/)"
[ ! -e "$src.bak-$stamp" ] || die "$src.bak-$stamp exists already"
cp -a "$src" "$src.bak-$stamp"
log "snapshot copied to $src.bak-$stamp"

parent=$(dirname "$src")
base=$(basename "$src")
n=0
while IFS= read -r name; do
  [[ "$name" =~ ^(.+)\.bak-[0-9]{8}-[0-9]{6}$ ]] || continue
  [ "${BASH_REMATCH[1]}" = "$base" ] || continue
  n=$((n + 1))
  [ "$n" -gt "$KEEP_COPIES" ] || continue
  if rm -rf -- "${parent:?}/$name"; then
    log "older copy removed: $parent/$name"
  else
    log "WARNING: could not remove the older copy $parent/$name; the release goes on"
  fi
done < <(find "$parent" -mindepth 1 -maxdepth 1 -type d -name "$base.bak-*" -printf '%f\n' | LC_ALL=C sort -r)

rm -rf "$src/docs"
log "docs/ cleared for the release; $KEEP_COPIES newest copies kept"
