# The site host's half of scripts/chain/nightly-site.sh (task #266): sent on
# stdin after SITE, REF, ORAS, PAGE_B64 and JSON_B64. Never run by hand.
#
#   1. the artifact pulled into <SITE>/.nightly/.incoming (Docker, ORAS: the
#      packages are public on ghcr, no credential), every file checked
#      against its SHA256SUMS;
#   2. the page and the manifest put beside them, the directory renamed to
#      <SITE>/.nightly/<date>, and <SITE>/nightly - a link - pointed at it in
#      one rename (a reader sees the old night or the new one, never half);
#   3. every other night under <SITE>/.nightly removed (ghcr keeps the last
#      ones; here only the newest is served).
set -euo pipefail
: "${SITE:?}" "${REF:?}" "${ORAS:?}" "${PAGE_B64:?}" "${JSON_B64:?}"
case "$SITE" in /?*) ;; *) echo "SITE must be an absolute directory, not /" >&2; exit 2 ;; esac
[ -d "$SITE" ] || { echo "$SITE is not a directory" >&2; exit 2; }
if [ -e "$SITE/nightly" ] && [ ! -L "$SITE/nightly" ]; then
  echo "$SITE/nightly exists and is not a link: move it away first" >&2
  exit 2
fi

store="$SITE/.nightly"
incoming="$store/.incoming"
mkdir -p "$store"
rm -rf -- "$incoming"
mkdir "$incoming"

printf '%s' "$JSON_B64" | base64 -d > "$incoming/.manifest.json"
day=$(sed -n 's/^ *"date": *"\([0-9]\{4\}\)-\([0-9]\{2\}\)-\([0-9]\{2\}\)".*/\1\2\3/p' "$incoming/.manifest.json" | head -n 1)
case "$day" in
  [0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]) ;;
  *) echo "the manifest names no date" >&2; exit 2 ;;
esac

docker run --rm --user 0:0 -v "$incoming:/w" -w /w "$ORAS" pull "$REF" >&2
[ -s "$incoming/SHA256SUMS" ] || { echo "$REF carries no SHA256SUMS: nothing published" >&2; exit 1; }
(cd "$incoming" && sha256sum --check --quiet SHA256SUMS) || { echo "a package does not match its SHA256SUMS: nothing published" >&2; exit 1; }

mv "$incoming/.manifest.json" "$incoming/nightly.json"
printf '%s' "$PAGE_B64" | base64 -d > "$incoming/index.html"
chmod -R a+rX "$incoming"

rm -rf -- "${store:?}/$day"
mv "$incoming" "$store/$day"
ln -sfn ".nightly/$day" "$SITE/.nightly-link"
mv -Tf "$SITE/.nightly-link" "$SITE/nightly"

for d in "$store"/[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]; do
  [ -d "$d" ] || continue
  [ "$(basename "$d")" = "$day" ] && continue
  rm -rf -- "$d"
done
version=$(sed -n 's/^ *"version": *"\([^"]*\)".*/\1/p' "$store/$day/nightly.json" | head -n 1)
echo "filex.sh/nightly: $version ($day), $(find "$store/$day" -maxdepth 1 -name 'filex-desktop-*' | wc -l) package(s)"
