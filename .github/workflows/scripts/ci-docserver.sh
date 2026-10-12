#!/usr/bin/env bash
# An ONLYOFFICE Document Server for ci.yml's Document Server jobs:
#
#   bash .github/workflows/scripts/ci-docserver.sh <filex port> <go test port>
#
# The server runs in Docker on 127.0.0.1:8090 with a JWT secret made here,
# and reaches back to the runner as `filex` (the Docker bridge's gateway).
# filex itself listens on 127.0.0.1 only (e2e/run.mjs), so a forwarder on the
# gateway's address hands the server's calls on to it - the build host's
# chain does the same (scripts/chain/run.mjs, scripts/chain/job/forward.mjs).
# The Go test listens on every address itself, on the second port.
#
# Appends to $GITHUB_ENV what e2e/run.mjs passes to filex and the specs read
# (FILEX_ONLYOFFICE_URL, _JWT, _CALLBACK_URL) and what the office engine's Go
# test reads (FILEX_TEST_OO_*, FILEX_TEST_CONVERT: the Convert app that
# `node scripts/ci/browser-prep.mjs apps` fetched, which must run first). The
# secret is masked in the log.
set -euo pipefail
port="${1:?usage: ci-docserver.sh <filex port> <go test port>}"
goport="${2:?usage: ci-docserver.sh <filex port> <go test port>}"
# The image is pinned to the last 9.x (Docs 9.4.0.1, what `latest` was until
# 2026-10-12). Docs 10.0 keeps a CSV file's own delimiter when it saves, and
# filex's CSV save (internal/onlyoffice/csv.go) is written for 9.x, which
# always wrote commas: on 10.0 the 199-csv-onlyoffice spec fails. Docs 10 is
# not supported yet, so the gate tests the server filex was written for.
image="${DS_IMAGE:-onlyoffice/documentserver:9.4.0.1}"
secret=$(openssl rand -hex 24)
echo "::add-mask::$secret"
gw=$(docker network inspect bridge --format '{{(index .IPAM.Config 0).Gateway}}')
[ -n "$gw" ] || { echo "::error::the Docker bridge has no gateway address"; exit 1; }
docker run -d --name filex-ci-ds -p 127.0.0.1:8090:80 --add-host filex:host-gateway \
  -e JWT_ENABLED=true -e "JWT_SECRET=$secret" -e JWT_HEADER=Authorization \
  -e ALLOW_PRIVATE_IP_ADDRESS=true -e ALLOW_META_IP_ADDRESS=true "$image" > /dev/null
nohup node scripts/chain/job/forward.mjs "$gw:$port" "127.0.0.1:$port" > /dev/null 2>&1 &
healthy=""
for _ in $(seq 1 120); do
  if [ "$(curl -s -m 3 http://127.0.0.1:8090/healthcheck || true)" = true ]; then healthy=1; break; fi
  sleep 3
done
if [ -z "$healthy" ]; then
  docker logs --tail 60 filex-ci-ds || true
  echo "::error::the Document Server ($image) did not answer its healthcheck in 6 minutes"
  exit 1
fi
echo "Document Server up: $image, filex is $gw:$port to it"
{
  echo "FILEX_ONLYOFFICE_URL=http://127.0.0.1:8090"
  echo "FILEX_ONLYOFFICE_JWT=$secret"
  echo "FILEX_ONLYOFFICE_CALLBACK_URL=http://filex:$port"
  echo "FILEX_TEST_OO_URL=http://127.0.0.1:8090"
  echo "FILEX_TEST_OO_JWT=$secret"
  echo "FILEX_TEST_OO_LISTEN=:$goport"
  echo "FILEX_TEST_OO_CALLBACK=http://filex:$goport"
  echo "FILEX_TEST_OO_FIXTURES=$PWD/e2e/fixtures/file-types"
  echo "FILEX_TEST_CONVERT=${FILEX_CONVERT_APP_DIR:?run node scripts/ci/browser-prep.mjs apps first}"
} >> "${GITHUB_ENV:?not in a GitHub job}"
