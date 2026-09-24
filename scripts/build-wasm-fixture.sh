#!/usr/bin/env bash
# Builds the wasm fixture plugin the wasmplugin tests run against.
#
#   bash scripts/build-wasm-fixture.sh          # → backend/internal/wasmplugin/testdata/echo/echo.wasm
#
# Stock Go (1.24+), no TinyGo: GOOS=wasip1 GOARCH=wasm with -buildmode=c-shared
# so the //go:wasmexport functions become module exports. The artefact is
# git-ignored; CI builds it before `go test`, and a local `go test` skips the
# runtime tests with a clear message when it is missing.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root/backend"
out="internal/wasmplugin/testdata/echo/echo.wasm"
GOOS=wasip1 GOARCH=wasm go build -trimpath -ldflags="-s -w" -buildmode=c-shared -o "$out" ./internal/wasmplugin/testdata/echo
ls -la "$out"
