#!/bin/bash
# One line of the browser list, through e2e/run.mjs. Playwright image, root.
#
#   KIND     cypress | local | nopub | s3
#   ENGINES  E2E_BROWSERS (comma-separated; empty for Cypress)
#   DS       1: connect the Document Server at DS_URL (JWT in DS_SECRET_FILE).
#            filex listens on PORT and the server calls it back at
#            http://filex:PORT, forwarded from DS_FILEX_IP:PORT here
#   ARGS     the rest of the list line, one word per argument
#   PORT     this line's own port (run.mjs: CHAIN_E2E_PORT + its place in the
#            list), used where the Document Server must call filex back
#
# The apps the suite installs come from /w/apps/<name> when run.mjs mounted
# them (sign, convert, lang-es, lang-de, lang-fr; lang-ar is the
# right-to-left fixture and is passed by path). Test output is copied to
# /w/run/out/$JOB.
#
# ⚠ A part of a split run (`--shard i/N` in ARGS) clears and copies only its
# own e2e/test-results/shard-i-of-N and e2e/.artifacts/shard-i-of-N, which
# e2e/run.mjs gives it: clearing the shared directories would delete another
# part's traces mid-run. A whole run leaves the shard-* directories alone.
. /w/chain/job/common.sh
cd /w/src || exit 1
read -r -a EXTRA <<< "${ARGS:-}"
LABEL=""
for i in "${!EXTRA[@]}"; do
  a="${EXTRA[$i]}"
  case "$a" in
    --shard) v="${EXTRA[$((i + 1))]:-}" ;;
    --shard=*) v="${a#--shard=}" ;;
    *) continue ;;
  esac
  LABEL="shard-${v%/*}-of-${v#*/}"
done

# Every entry of e2e/test-results and e2e/.artifacts but the shard-* parts.
whole_entries() {
  local d
  for d in e2e/test-results e2e/.artifacts; do
    [ -d "$d" ] && find "$d" -mindepth 1 -maxdepth 1 ! -name 'shard-*'
  done
}

if [ -n "$LABEL" ]; then
  rm -rf "e2e/test-results/$LABEL" "e2e/.artifacts/$LABEL"
else
  whole_entries | while IFS= read -r e; do rm -rf "$e"; done
  rm -rf e2e/playwright-report web/cypress/screenshots web/cypress/videos
fi

[ -d /w/apps/sign ] && export FILEX_SIGN_APP_DIR=/w/apps/sign
[ -d /w/apps/convert ] && export FILEX_CONVERT_APP_DIR=/w/apps/convert
[ -d /w/apps/lang-es ] && export FILEX_LANG_ES_APP_DIR=/w/apps/lang-es FILEX_E2E_LANG_PACK_ES=/w/apps/lang-es/filex-app.json
[ -d /w/apps/lang-de ] && export FILEX_LANG_DE_APP_DIR=/w/apps/lang-de
[ -d /w/apps/lang-fr ] && export FILEX_LANG_FR_APP_DIR=/w/apps/lang-fr
[ -d /w/apps/lang-ar ] && export FILEX_E2E_LANG_PACK_AR=/w/apps/lang-ar/filex-app.json
if [ "${REQUIRE_APPS:-1}" = 1 ]; then
  export FILEX_REQUIRE_WASM_FIXTURE=1
else
  unset FILEX_REQUIRE_WASM_FIXTURE
fi
export E2E_BROWSERS="${ENGINES:-chromium}"
unset FILEX_ONLYOFFICE_URL FILEX_ONLYOFFICE_JWT FILEX_ONLYOFFICE_CALLBACK_URL
BIN=/w/src/bin/filex
echo "kind=$KIND engines=${ENGINES:--} ds=${DS:-0} args=${ARGS:-}"
echo "apps: sign=${FILEX_SIGN_APP_DIR:-none} convert=${FILEX_CONVERT_APP_DIR:-none} es=${FILEX_LANG_ES_APP_DIR:-none} ar=${FILEX_E2E_LANG_PACK_AR:-none}"

case "$KIND" in
cypress)
  xvfb-run -a node e2e/run.mjs cypress --binary "$BIN" "${EXTRA[@]}" > "$OUT/run.out" 2>&1
  rc=$?
  ;;
local)
  PORTARG=()
  if [ "${DS:-0}" = 1 ]; then
    node /w/chain/job/forward.mjs "$DS_FILEX_IP:$PORT" "127.0.0.1:$PORT" &
    export FILEX_ONLYOFFICE_URL="$DS_URL" FILEX_ONLYOFFICE_JWT="$(cat "$DS_SECRET_FILE")" FILEX_ONLYOFFICE_CALLBACK_URL="http://filex:$PORT"
    echo "document server: $(curl -s -m 5 "$DS_URL/healthcheck")"
    PORTARG=(--port "$PORT")
  fi
  node e2e/run.mjs local --binary "$BIN" "${PORTARG[@]}" "${EXTRA[@]}" > "$OUT/run.out" 2>&1
  rc=$?
  ;;
nopub)
  node e2e/run.mjs local --binary "$BIN" --no-public-url "${EXTRA[@]}" > "$OUT/run.out" 2>&1
  rc=$?
  ;;
s3)
  mkdir -p /tmp/chain-shim
  cp /w/chain/shim/docker /tmp/chain-shim/docker
  chmod 755 /tmp/chain-shim/docker
  export PATH="/tmp/chain-shim:$PATH"
  export E2E_S3_BUCKET=filex-e2e-multi E2E_S3_ACCESS_KEY=filexe2e E2E_S3_SECRET_KEY=filexe2esecret
  export E2E_S3_ENDPOINT=http://127.0.0.1:7070 E2E_S3_PATH_STYLE=1 E2E_S3_REGION=us-east-1 E2E_S3_PREFIX=e2e-multi/
  unset E2E_MINIO_IMAGE
  mkdir -p /w/run/vgw-data/filex-e2e-multi
  echo "gateway /health: $(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:7070/health)"
  node e2e/run.mjs local --binary "$BIN" --s3 "${EXTRA[@]}" > "$OUT/run.out" 2>&1
  rc=$?
  ;;
*)
  summary "unknown kind $KIND"
  exit 2
  ;;
esac

if [ -n "$LABEL" ]; then
  cp -a "e2e/test-results/$LABEL" "$OUT/test-results" 2>/dev/null
  cp -a "e2e/.artifacts/$LABEL" "$OUT/artifacts" 2>/dev/null
else
  mkdir -p "$OUT/test-results" "$OUT/artifacts"
  whole_entries | while IFS= read -r e; do
    case "$e" in
      e2e/test-results/*) cp -a "$e" "$OUT/test-results/" ;;
      *) cp -a "$e" "$OUT/artifacts/" ;;
    esac
  done
  cp -a web/cypress/screenshots "$OUT/cy-screenshots" 2>/dev/null
fi
grep -E '^\s+[0-9]+\) \[' "$OUT/run.out" | head -40
tail -15 "$OUT/run.out"
if [ "$KIND" = cypress ]; then
  counts=$(grep -E 'All specs passed|of [0-9]+ failed' "$OUT/run.out" | tail -1 | tr -s ' ')
else
  counts=$(grep -E '^\s+[0-9]+ (passed|failed|flaky|skipped|did not run)' "$OUT/run.out" | tr -s ' ' | tr '\n' ',')
fi
summary "e2e=$rc ${counts:-no counts in run.out}"
exit $rc
