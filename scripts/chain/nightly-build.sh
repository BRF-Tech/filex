#!/usr/bin/env bash
# The nightly build (task #175): the image of a commit the nightly run found
# green, built from that commit's PUBLIC form - the tree
# scripts/export-public.sh makes of it, with the same rewrite and the same
# refusals as a release - and never from the private tree: only what the open
# repository would carry goes into it (scripts/chain/nightly-export.sh). Local
# only: it tags NB_IMAGE:NB_TAG on this host and publishes nothing (`node
# scripts/chain/nightly.mjs publish` does, when asked). One image, the default
# recipe (docker/Dockerfile), for this host's architecture; the desktop side
# is scripts/chain/nightly-desktop.sh.
#
# scripts/chain/nightly.mjs runs it under the build lock, with:
#   NB_SRC            the nightly checkout, at the commit that ran green
#   NB_SHA            that commit (NB_SRC's HEAD must be it)
#   NB_PUBLIC_DIR     a clone of the public repository, made here the first
#                     time and reset to its main every time
#   NB_PUBLIC_REMOTE  where that clone comes from
#   NB_OUT            where the logs go
#   NB_IMAGE NB_TAG   the local image and its tag
#   NB_VERSION        the version the binary reports (nightly-lib.mjs nightlyVersion)
#   NB_DATE           the build time, ISO 8601
#   NB_DOCKERFILE     the recipe, relative to the tree (default docker/Dockerfile)
#   NB_LABELS_FILE    (optional) more labels, one key=value a line
#                     (nightly-lib.mjs nightlyLabels: the source, what it is,
#                     the night's test result)
#
# #266: what a nightly is can be read off it from outside. Its version names
# the commit (0.57.0-nightly.20261014+2ca25999, `filex --version` and the
# admin's About say it), and so do its labels: the commit
# (org.opencontainers.image.revision), the public commit its tree was exported
# on (sh.filex.nightly.public-base) and the night's test result
# (sh.filex.nightly.tests) -
#   docker inspect --format '{{json .Config.Labels}}' ghcr.io/brf-tech/filex:nightly
#
# The public clone is left reset: the export stages its tree there, and this
# script throws that away once the image is built.
set -euo pipefail
: "${NB_SRC:?}" "${NB_SHA:?}" "${NB_PUBLIC_DIR:?}" "${NB_PUBLIC_REMOTE:?}" "${NB_OUT:?}"
: "${NB_IMAGE:?}" "${NB_TAG:?}" "${NB_VERSION:?}" "${NB_DATE:?}"
DOCKERFILE="${NB_DOCKERFILE:-docker/Dockerfile}"
mkdir -p "$NB_OUT"
say() { echo "$(date -u +%FT%TZ) nightly-build: $*"; }
. "$(dirname "${BASH_SOURCE[0]}")/nightly-export.sh"

trap 'nightly_public_reset "$NB_PUBLIC_DIR" >/dev/null 2>&1 || true' EXIT
say "export of ${NB_SHA:0:8} into $NB_PUBLIC_DIR"
if ! nightly_public_export "$NB_SRC" "$NB_SHA" "$NB_PUBLIC_DIR" "$NB_PUBLIC_REMOTE" "$NB_OUT/export.log"; then
  exit 1
fi
base=$(git -C "$NB_PUBLIC_DIR" rev-parse origin/main)

labels=(
  --label "org.opencontainers.image.title=filex nightly"
  --label "org.opencontainers.image.version=$NB_VERSION"
  --label "org.opencontainers.image.revision=$NB_SHA"
  --label "org.opencontainers.image.created=$NB_DATE"
  --label "sh.filex.nightly.public-base=$base"
)
if [ -n "${NB_LABELS_FILE:-}" ] && [ -f "$NB_LABELS_FILE" ]; then
  while IFS= read -r line; do
    [ -n "$line" ] && labels+=(--label "$line")
  done < "$NB_LABELS_FILE"
fi

say "docker build $NB_IMAGE:$NB_TAG ($NB_VERSION) from $DOCKERFILE"
if ! docker build --pull -f "$NB_PUBLIC_DIR/$DOCKERFILE" \
  --build-arg "VERSION=$NB_VERSION" \
  --build-arg "COMMIT=${NB_SHA:0:8}" \
  --build-arg "DATE=$NB_DATE" \
  "${labels[@]}" \
  -t "$NB_IMAGE:$NB_TAG" "$NB_PUBLIC_DIR" > "$NB_OUT/docker-build.log" 2>&1; then
  tail -30 "$NB_OUT/docker-build.log"
  say "docker build failed (log $NB_OUT/docker-build.log)"
  exit 1
fi

reported=$(docker run --rm --entrypoint /usr/local/bin/filex "$NB_IMAGE:$NB_TAG" --version 2>&1 || true)
echo "$reported" > "$NB_OUT/version.out"
case "$reported" in
  *"$NB_VERSION"*) ;;
  *)
    say "the image's filex --version does not report $NB_VERSION: $reported"
    exit 1
    ;;
esac
say "built $NB_IMAGE:$NB_TAG: $(docker image inspect --format '{{.Id}}' "$NB_IMAGE:$NB_TAG")"
