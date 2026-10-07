#!/bin/bash
# The e2e suite's own typecheck (e2e/tsconfig.json). Playwright image.
. /w/chain/job/common.sh
cd /w/src/e2e || exit 1
./node_modules/.bin/tsc -p tsconfig.json > "$OUT/tsc.out" 2>&1
rc=$?
head -40 "$OUT/tsc.out"
summary "e2e-tsc=$rc"
exit $rc
