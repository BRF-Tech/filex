#!/bin/sh
# Rebuilds engine.wasm.gz, the template engine filex runs edited templates in
# (see sandbox.go and docs/TEMPLATES.md), and engine.wasm.sources, the digest
# of what it was built from.
#
#   sh backend/internal/templates/sandbox/build.sh
#
# Needs Docker and nothing else. The toolchain image is pinned by digest and
# the modules by backend/go.sum; -trimpath, an empty build id and gzip -n keep
# paths and times out of the bytes, so the same commit builds the same module:
# the script prints the SHA-256 of what it wrote; compare it with the committed
# file's. Run it after any change to internal/templates/engine, to guest/, to
# this file or to the Liquid library's version - TestEngineIsBuiltFromTheseSources
# is red until you do.
set -eu

HERE=$(cd "$(dirname "$0")" && pwd)
BACKEND=$(cd "$HERE/../../.." && pwd)
IMAGE=golang:1.25-alpine@sha256:1ae0735f00daffa3aaf1363a5184c0d2dc55c78e3db4ec70241cdac97bf84b59

docker run --rm \
  -v "$BACKEND:/src" \
  -e CGO_ENABLED=0 \
  -e GOFLAGS=-mod=readonly \
  -w /src \
  "$IMAGE" sh -eu -c '
    GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -trimpath -buildvcs=false \
      -ldflags="-s -w -buildid=" -o /tmp/engine.wasm ./internal/templates/sandbox/guest
    gzip -9 -n -c /tmp/engine.wasm > internal/templates/sandbox/engine.wasm.gz
    go run ./internal/templates/sandbox/gen_sources.go
    chown '"$(id -u):$(id -g)"' internal/templates/sandbox/engine.wasm.gz internal/templates/sandbox/engine.wasm.sources
  '

if command -v sha256sum >/dev/null 2>&1; then
  sha256sum "$HERE/engine.wasm.gz"
else
  shasum -a 256 "$HERE/engine.wasm.gz"
fi
