#!/usr/bin/env bash
# An S3 gateway on 127.0.0.1:7070, for ci.yml's S3 line and for s3-live.yml:
#
#   bash .github/workflows/scripts/ci-s3.sh        the multi-storage spec (E2E_S3_*)
#   bash .github/workflows/scripts/ci-s3.sh live   the live S3 Go tests (FILEX_TEST_S3_*)
#
# `node e2e/run.mjs local --s3` starts a Versity S3 Gateway of its own for
# 26-s3 and its two storages; 70-multi-storage registers one more from the
# E2E_S3_* variables, against this gateway on 127.0.0.1:7070 - the build
# host's chain gives it the same one (scripts/chain/job/e2e.sh, kind s3). The
# image, the posix root on an anonymous volume and the bucket-as-a-directory
# are e2e/lib/s3server.mjs's; region us-east-1 is part of the signature, and
# path-style addressing, since nothing resolves <bucket>.localhost (lessons
# #918, #927).
#
# `live` starts the same gateway with a bucket of its own, filex-live, and
# names it to the Go tests that talk to a real S3 server
# (backend/internal/testutil/lives3) in FILEX_TEST_S3_* (#365). The key is the
# gateway's root key, made up for this container and valid nowhere else.
set -euo pipefail
mode=${1:-e2e}
case "$mode" in
  e2e) bucket=filex-e2e-multi ;;
  live) bucket=filex-live ;;
  *) echo "usage: ci-s3.sh [live]" >&2; exit 2 ;;
esac
image=$(node --input-type=module -e "import('./e2e/lib/s3server.mjs').then((m) => console.log(m.S3_IMAGE_DEFAULT))")
docker run -d --rm --name filex-ci-s3 -p 127.0.0.1:7070:7070 -v /data \
  -e ROOT_ACCESS_KEY=filexe2e -e ROOT_SECRET_KEY=filexe2esecret \
  -e VGW_BACKEND=posix -e VGW_BACKEND_ARG=/data -e VGW_HEALTH=/health "$image" > /dev/null
up=""
for _ in $(seq 1 30); do
  if [ "$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:7070/health || true)" = 200 ]; then up=1; break; fi
  sleep 2
done
[ -n "$up" ] || { docker logs filex-ci-s3 || true; echo "::error::the S3 gateway ($image) did not answer"; exit 1; }
docker exec filex-ci-s3 mkdir -p "/data/$bucket"
echo "S3 gateway up: $image on 127.0.0.1:7070, bucket $bucket"
if [ "$mode" = live ]; then
  {
    echo "FILEX_TEST_S3_BUCKET=filex-live"
    echo "FILEX_TEST_S3_ACCESS_KEY=filexe2e"
    echo "FILEX_TEST_S3_SECRET_KEY=filexe2esecret"
    echo "FILEX_TEST_S3_ENDPOINT=http://127.0.0.1:7070"
    echo "FILEX_TEST_S3_REGION=us-east-1"
    echo "FILEX_TEST_S3_PATH_STYLE=1"
  } >> "${GITHUB_ENV:?not in a GitHub job}"
  exit 0
fi
{
  echo "E2E_S3_BUCKET=filex-e2e-multi"
  echo "E2E_S3_ACCESS_KEY=filexe2e"
  echo "E2E_S3_SECRET_KEY=filexe2esecret"
  echo "E2E_S3_ENDPOINT=http://127.0.0.1:7070"
  echo "E2E_S3_PATH_STYLE=1"
  echo "E2E_S3_REGION=us-east-1"
  echo "E2E_S3_PREFIX=e2e-multi/"
} >> "${GITHUB_ENV:?not in a GitHub job}"
