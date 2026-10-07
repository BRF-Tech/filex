#!/bin/bash
# The build every other job runs on: install, the echo app fixture, and
# build:all (packages, web, embed assets, bin/filex). Node image, Go mounted
# at /usr/local/go. A binary or admin bundle older than this job is a red,
# not a pass: the rest of the chain would test the previous build.
# `go mod download` fetches the test-only modules too: the Go jobs run as
# another user and could not write them into a module cache root made.
. /w/chain/job/common.sh
cd /w/src || exit 1
use_pnpm
STAMP="$OUT/started"
: > "$STAMP"
node --version; pnpm --version; go version
pnpm install --frozen-lockfile > "$OUT/pnpm-install.out" 2>&1
install=$?
tail -5 "$OUT/pnpm-install.out"
bash scripts/build-wasm-fixture.sh > "$OUT/wasm.out" 2>&1
wasm=$?
pnpm run build:all > "$OUT/build-all.out" 2>&1
build=$?
tail -15 "$OUT/build-all.out"
(cd backend && go mod download) > "$OUT/go-mod-download.out" 2>&1
mods=$?
fresh=yes
for f in bin/filex backend/embed/admin/index.html; do
  [ "$f" -nt "$STAMP" ] || { fresh=no; echo "not rebuilt by this job: $f"; }
done
summary "pnpm-install=$install wasm-fixture=$wasm build-all=$build go-mod-download=$mods fresh=$fresh"
[ "$install$wasm$build$mods" = 0000 ] && [ "$fresh" = yes ]
