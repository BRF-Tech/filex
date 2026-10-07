#!/usr/bin/env bash
# The nightly build (task #175): the image of a commit the nightly run found
# green, built from that commit's PUBLIC form - the tree
# scripts/export-public.sh makes of it, with the same rewrite and the same
# refusals as a release - and never from the private tree: only what the open
# repository would carry goes into it. Local only: it tags NB_IMAGE:NB_TAG on
# this host and publishes nothing (`node scripts/chain/nightly.mjs publish`
# does, when asked). One image, the default recipe (docker/Dockerfile), for
# this host's architecture; no desktop packages.
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
#
# The public clone is left reset: the export stages its tree there, and this
# script throws that away once the image is built.
set -euo pipefail
: "${NB_SRC:?}" "${NB_SHA:?}" "${NB_PUBLIC_DIR:?}" "${NB_PUBLIC_REMOTE:?}" "${NB_OUT:?}"
: "${NB_IMAGE:?}" "${NB_TAG:?}" "${NB_VERSION:?}" "${NB_DATE:?}"
DOCKERFILE="${NB_DOCKERFILE:-docker/Dockerfile}"
mkdir -p "$NB_OUT"
say() { echo "$(date -u +%FT%TZ) nightly-build: $*"; }

head=$(git -c safe.directory='*' -C "$NB_SRC" rev-parse HEAD)
if [ "$head" != "$NB_SHA" ]; then
  say "the checkout is at $head, not at $NB_SHA"
  exit 1
fi

reset_public() {
  git -C "$NB_PUBLIC_DIR" reset --quiet --hard "origin/main"
  git -C "$NB_PUBLIC_DIR" clean -ffdxq
}

if [ ! -d "$NB_PUBLIC_DIR/.git" ]; then
  if [ -e "$NB_PUBLIC_DIR" ]; then
    say "$NB_PUBLIC_DIR exists and is not a clone: move it away"
    exit 1
  fi
  say "cloning $NB_PUBLIC_REMOTE into $NB_PUBLIC_DIR"
  git clone --quiet "$NB_PUBLIC_REMOTE" "$NB_PUBLIC_DIR"
fi
git -C "$NB_PUBLIC_DIR" fetch --quiet origin main
git -C "$NB_PUBLIC_DIR" checkout --quiet --force -B main origin/main
reset_public
trap 'reset_public >/dev/null 2>&1 || true' EXIT

say "export of ${NB_SHA:0:8} into $NB_PUBLIC_DIR"
if ! bash "$NB_SRC/scripts/export-public.sh" "$NB_PUBLIC_DIR" > "$NB_OUT/export.log" 2>&1; then
  tail -30 "$NB_OUT/export.log"
  say "the export refused this commit (log $NB_OUT/export.log): nothing is built from it"
  exit 1
fi
tail -3 "$NB_OUT/export.log"

say "docker build $NB_IMAGE:$NB_TAG ($NB_VERSION) from $DOCKERFILE"
if ! docker build --pull -f "$NB_PUBLIC_DIR/$DOCKERFILE" \
  --build-arg "VERSION=$NB_VERSION" \
  --build-arg "COMMIT=${NB_SHA:0:8}" \
  --build-arg "DATE=$NB_DATE" \
  --label "org.opencontainers.image.title=filex nightly" \
  --label "org.opencontainers.image.version=$NB_VERSION" \
  --label "org.opencontainers.image.revision=$NB_SHA" \
  --label "org.opencontainers.image.created=$NB_DATE" \
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
