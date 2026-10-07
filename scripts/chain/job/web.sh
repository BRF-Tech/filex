#!/bin/bash
# The web gates: every vitest file, the core package build, vue-tsc on core
# and web, the desktop typecheck and its unit tests. Playwright image.
. /w/chain/job/common.sh
cd /w/src || exit 1
use_pnpm
(cd web && pnpm exec vitest run > "$OUT/vitest.out" 2>&1); vitest=$?
grep -E '^\s*(Test Files|Tests|Errors?)\s' "$OUT/vitest.out" | tail -4
grep -E '^\s*(FAIL|×)' "$OUT/vitest.out" | head -40
(pnpm --filter "./packages/core" build > "$OUT/core-build.out" 2>&1); core=$?
(cd packages/core && pnpm exec vue-tsc --noEmit > "$OUT/core-tsc.out" 2>&1); coretsc=$?
(cd web && pnpm exec vue-tsc --noEmit > "$OUT/web-tsc.out" 2>&1); webtsc=$?
(cd desktop && pnpm run typecheck > "$OUT/desktop-tsc.out" 2>&1); desktsc=$?
(cd desktop && pnpm test > "$OUT/desktop-test.out" 2>&1); desktest=$?
grep -E '^# (tests|pass|fail)' "$OUT/desktop-test.out"
grep -E '^not ok' "$OUT/desktop-test.out" | head -20
tests=$(grep -E '^\s*Tests\s' "$OUT/vitest.out" | tail -1 | tr -s ' ')
summary "vitest=$vitest (${tests# }) core-build=$core core-vue-tsc=$coretsc web-vue-tsc=$webtsc desktop-tsc=$desktsc desktop-test=$desktest"
[ "$vitest$core$coretsc$webtsc$desktsc$desktest" = 000000 ]
