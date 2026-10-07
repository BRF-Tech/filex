#!/usr/bin/env bash
# An S3 gateway for the multi-storage spec, for ci.yml's S3 line:
#
#   bash .github/workflows/scripts/ci-s3.sh
#
# `node e2e/run.mjs local --s3` starts a Versity S3 Gateway of its own for
# 26-s3 and its two storages; 70-multi-storage registers one more from the
# E2E_S3_* variables, against this gateway on 127.0.0.1:7070 - the build
# host's chain gives it the same one (scripts/chain/job/e2e.sh, kind s3). The
# image, the posix root on an anonymous volume and the bucket-as-a-directory
# are e2e/lib/s3server.mjs's; region us-east-1 is part of the signature, and
# path-style addressing, since nothing resolves <bucket>.localhost (lessons
# #918, #927).
set -euo pipefail
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
docker exec filex-ci-s3 mkdir -p /data/filex-e2e-multi
echo "S3 gateway up: $image on 127.0.0.1:7070, bucket filex-e2e-multi"
{
  echo "E2E_S3_BUCKET=filex-e2e-multi"
  echo "E2E_S3_ACCESS_KEY=filexe2e"
  echo "E2E_S3_SECRET_KEY=filexe2esecret"
  echo "E2E_S3_ENDPOINT=http://127.0.0.1:7070"
  echo "E2E_S3_PATH_STYLE=1"
  echo "E2E_S3_REGION=us-east-1"
  echo "E2E_S3_PREFIX=e2e-multi/"
} >> "${GITHUB_ENV:?not in a GitHub job}"
