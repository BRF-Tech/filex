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

set -euo pipefail

src="${1:?the docs snapshot directory}"
stamp="${2:?a stamp for the copy}"
case "$src" in
  /*/*) ;;
  *) die "the docs snapshot '$src' must be an absolute path below /" ;;
esac
[ -d "$src/docs-site" ] || die "$src is not the docs site's snapshot (it has no docs-site/)"
[ ! -e "$src.bak-$stamp" ] || die "$src.bak-$stamp exists already"
cp -a "$src" "$src.bak-$stamp"
rm -rf "$src/docs"
log "snapshot copied to $src.bak-$stamp; docs/ cleared for the release"
