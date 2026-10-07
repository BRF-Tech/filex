#!/bin/bash
# The Go tests that talk to a REAL S3 provider (nightly profile, task #175):
# the provider conformance suite, the B2 report reader over S3, issue #21's
# rename and the driver's Init, against the bucket the nightly settings name
# (FILEX_TEST_S3_*, read by backend/internal/testutil/lives3). Go image, root.
#
#   /w/run/etc/s3-live.env   the bucket and its key, one KEY=VALUE per line,
#                            0600, written by run.mjs from the settings file and
#                            removed when the run ends (nightly-lib s3LiveFile)
#
# ⚠ The values never reach a command line, the log or the job's output: they
# are read from that file, and the test output is written through `redact`,
# which replaces the key and the secret wherever a provider's error echoes
# them. Only names are ever printed.
#
# ⚠ Everywhere else each test skips itself, and a skip reads as a pass: a run
# in which one of them did not pass is red, and so is a run without the
# settings (FILEX_TEST_S3_BUCKET, _ACCESS_KEY, _SECRET_KEY and _REGION; lesson
# #927: the region is part of the signature, and a server with a region of its
# own refuses "auto").
. /w/chain/job/common.sh
cd /w/src/backend || exit 1
go version
CREDS=/w/run/etc/s3-live.env
TESTS='TestLiveProviderConformance TestInit_Integration TestB2_OverRealS3 TestRenameOnS3_TheReportersSequence'
PKGS='./internal/storage/drivers/s3 ./internal/usage ./internal/api/handlers'
REQUIRED='FILEX_TEST_S3_BUCKET FILEX_TEST_S3_ACCESS_KEY FILEX_TEST_S3_SECRET_KEY FILEX_TEST_S3_REGION'

if [ ! -s "$CREDS" ]; then
  summary "s3-live=1 no settings: set $REQUIRED (and FILEX_TEST_S3_ENDPOINT) in the nightly settings file"
  exit 1
fi
while IFS= read -r line || [ -n "$line" ]; do
  case "$line" in
    FILEX_TEST_S3_*=*) export "$line" ;;
  esac
done < "$CREDS"
missing=""
for name in $REQUIRED; do
  [ -n "${!name:-}" ] || missing="$missing $name"
done
if [ -n "$missing" ]; then
  summary "s3-live=1 missing in the nightly settings:$missing"
  exit 1
fi
echo "settings: $(sed 's/=.*//' "$CREDS" | tr '\n' ' ')"

redact() {
  awk 'BEGIN { a = ENVIRON["FILEX_TEST_S3_ACCESS_KEY"]; s = ENVIRON["FILEX_TEST_S3_SECRET_KEY"] }
    function cut(line, v,    i, out) {
      if (v == "") return line
      out = ""
      while ((i = index(line, v)) > 0) {
        out = out substr(line, 1, i - 1) "[redacted]"
        line = substr(line, i + length(v))
      }
      return out line
    }
    { print cut(cut($0, s), a) }'
}

export INTEGRATION=1
RUN="^($(echo $TESTS | tr ' ' '|'))\$"
nice -n 10 go test -count=1 -v -timeout "${GO_TIMEOUT:-20m}" -run "$RUN" $PKGS 2>&1 | redact > "$OUT/go.out"
rc=${PIPESTATUS[0]}
grep -E '^(=== RUN|--- (PASS|FAIL|SKIP)|\s+--- (PASS|FAIL|SKIP)|panic:|FAIL|ok )' "$OUT/go.out" | head -60
passed=0
notpassed=""
for t in $TESTS; do
  if grep -qE "^--- PASS: $t " "$OUT/go.out"; then
    passed=$((passed + 1))
  else
    notpassed="$notpassed $t"
  fi
done
skipped=$(grep -cE '^--- SKIP: ' "$OUT/go.out")
if [ "$rc" -eq 0 ] && [ -n "$notpassed" ]; then
  echo "did not pass:$notpassed - a skip is not a pass here"
  rc=1
fi
summary "s3-live=$rc passed=$passed/$(echo $TESTS | wc -w) skipped=$skipped${notpassed:+ not passed:$notpassed}"
exit $rc
