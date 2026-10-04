#!/usr/bin/env bash
# Builds the wasm fixture plugin the wasmplugin tests and the e2e app specs run
# against.
#
#   bash scripts/build-wasm-fixture.sh          # → backend/internal/wasmplugin/testdata/echo/echo.wasm
#   FILEX_BACKEND_DIR=<a copy of backend/> bash <checkout>/scripts/build-wasm-fixture.sh
#
# Stock Go (1.24+), no TinyGo: GOOS=wasip1 GOARCH=wasm with -buildmode=c-shared
# so the //go:wasmexport functions become module exports. The artefact is
# git-ignored; CI builds it before `go test`, and a local `go test` skips the
# runtime tests with a clear message when it is missing. Both the Go tests
# (internal/testutil/wasmfixture) and the e2e specs (e2e/helpers/echoFixture.ts)
# refuse a module older than the sources beside it.
#
# FILEX_BACKEND_DIR is the Go module to build in (default: this checkout's
# backend/). The release's Go gates on a Windows workstation run in a mirror of
# the module on WSL's own disk (scripts/lib/go-build.mjs, wslMirrorCd) and call
# this script from the checkout: Go runs in the mirror, never on /mnt/<drive>.
#
# ⚠ A module built anywhere but this checkout's backend/ is ALSO copied into
# this checkout: the Playwright suite (on Windows) installs the checkout's
# copy. On the v0.50.0 pretag only the mirror's copy was refreshed, and spec
# 192 ran a module built before the change it tested: an hour of the release
# chain run again (lesson #960, #139).
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
backend="$(cd "${FILEX_BACKEND_DIR:-$root/backend}" && pwd)"
out="internal/wasmplugin/testdata/echo/echo.wasm"
cd "$backend"
GOOS=wasip1 GOARCH=wasm go build -trimpath -ldflags="-s -w" -buildmode=c-shared -o "$out" ./internal/wasmplugin/testdata/echo
ls -la "$out"
if [ ! "$backend/$out" -ef "$root/backend/$out" ]; then
  cp "$out" "$root/backend/$out"
  echo "copied into $root/backend/$out"
fi
