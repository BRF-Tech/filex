#!/bin/sh
# Rebuilds engine.wasm.gz, the SVG engine filex embeds (see engine.go and
# docs/thumbnails.md, Design notes: SVG).
#
#   sh backend/internal/thumb/svgwasm/build.sh
#
# Needs Docker and nothing else. The toolchain image is pinned by digest and the
# crates by rust/Cargo.lock, and every path that could end up in the module is
# remapped, so the same commit builds the same bytes: the script prints the
# SHA-256 of what it wrote; compare it with the committed file's.
set -eu

HERE=$(cd "$(dirname "$0")" && pwd)
IMAGE=rust:1.90-slim@sha256:7fa728f3678acf5980d5db70960cf8491aff9411976789086676bdf0c19db39e

docker run --rm \
  -v "$HERE/rust:/src:ro" \
  -v "$HERE:/out" \
  -e CARGO_HOME=/cargo \
  -e CARGO_TARGET_DIR=/tmp/target \
  -e RUSTFLAGS="--remap-path-prefix=/src=/filex-svgthumb --remap-path-prefix=/cargo=/cargo" \
  -w /src \
  "$IMAGE" sh -eu -c '
    rustup target add wasm32-unknown-unknown >/dev/null
    cargo build --locked --release --target wasm32-unknown-unknown
    gzip -9 -n -c /tmp/target/wasm32-unknown-unknown/release/filex_svgthumb.wasm > /out/engine.wasm.gz
    chown '"$(id -u):$(id -g)"' /out/engine.wasm.gz
  '

if command -v sha256sum >/dev/null 2>&1; then
  sha256sum "$HERE/engine.wasm.gz"
else
  shasum -a 256 "$HERE/engine.wasm.gz"
fi
