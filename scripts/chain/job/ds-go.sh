#!/bin/bash
# The Go test that needs a real ONLYOFFICE Document Server (nightly profile):
# backend/internal/server TestOfficeEngine_TheConvertAppOnARealDocumentServer
# against the chain's Document Server, with the Convert app run.mjs mounted
# at /w/apps/convert (CHAIN_CONVERT_APP_DIR). It runs right after the last
# browser line with the Document Server, which is still up for it.
#
#   DS_URL          the Document Server, as this job reaches it
#   DS_SECRET_FILE  its JWT secret (root's, 0600: this job runs as root)
#   PORT            where the test serves filex's fetch door; the Document
#                   Server reaches it at http://filex:PORT
#
# ⚠ Everywhere else the test skips itself, and a skip reads as a pass: a run
# in which it did not pass is red. Without a Convert build the job is red too,
# unless CHAIN_REQUIRE_APPS=0 (REQUIRE_APPS here) lets it skip. Go image.
. /w/chain/job/common.sh
cd /w/src/backend || exit 1
go version
TEST='^TestOfficeEngine_TheConvertAppOnARealDocumentServer$'
APP=/w/apps/convert
if [ ! -f "$APP/filex-app.json" ] || [ ! -f "$APP/plugin.wasm" ]; then
  if [ "${REQUIRE_APPS:-1}" = 1 ]; then
    summary "ds-go=1 no Convert build at $APP (filex-app.json + plugin.wasm; CHAIN_CONVERT_APP_DIR, or CHAIN_REQUIRE_APPS=0 to skip)"
    exit 1
  fi
  summary "ds-go=skipped (no Convert build, CHAIN_REQUIRE_APPS=0)"
  exit 0
fi
FILEX_TEST_OO_JWT="$(cat "$DS_SECRET_FILE")" || { summary "ds-go=1 cannot read $DS_SECRET_FILE"; exit 1; }
export FILEX_TEST_OO_JWT
export FILEX_TEST_OO_URL="$DS_URL"
export FILEX_TEST_OO_LISTEN=":$PORT"
export FILEX_TEST_OO_CALLBACK="http://filex:$PORT"
export FILEX_TEST_OO_FIXTURES=/w/src/e2e/fixtures/file-types
export FILEX_TEST_CONVERT="$APP"
echo "document server: $(curl -s -m 5 "$DS_URL/healthcheck")"
nice -n 10 go test -count=1 -v -timeout "${GO_TIMEOUT:-30m}" -run "$TEST" ./internal/server > "$OUT/go.out" 2>&1
rc=$?
grep -E '^(=== RUN|--- (PASS|FAIL|SKIP)|\s+--- (PASS|FAIL|SKIP)|panic:|FAIL|ok )' "$OUT/go.out" | head -40
grep -E 'MEASURE' "$OUT/go.out" | head -5
passed=$(grep -cE '^--- PASS: TestOfficeEngine_TheConvertAppOnARealDocumentServer' "$OUT/go.out")
skipped=$(grep -cE '^--- SKIP: TestOfficeEngine_TheConvertAppOnARealDocumentServer' "$OUT/go.out")
if [ "$rc" -eq 0 ] && [ "$passed" -eq 0 ]; then
  echo "the test did not pass (skipped: $skipped): a skip is not a pass here"
  rc=1
fi
summary "ds-go=$rc passed=$passed skipped=$skipped subtests=$(grep -cE '^\s+--- PASS: ' "$OUT/go.out")"
exit $rc
