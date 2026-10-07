#!/bin/bash
# One line of the -race list. With BIN set it is a shard: the package's
# prebuilt -race binary (race-build) runs the tests RUN matches, from the
# package's directory. Without, `go test -race` on PKGS minus EXCLUDE. No
# database DSN: the engines are the Go list's job. Go image, unprivileged.
#
# A shard whose regex matches no test is red: a list that runs nothing would
# otherwise read as a package that passed.
. /w/chain/job/common.sh
cd /w/src/backend || exit 1
unset FILEX_TEST_PG_DSN FILEX_TEST_MYSQL_DSN
if [ -n "${BIN:-}" ]; then
  cd "${PKGS#./}" || exit 1
  nice -n 10 "/w/run/bin/$BIN" -test.run "$RUN" -test.timeout 90m -test.count 1 -test.v > "$OUT/race.out" 2>&1
  rc=$?
  tests=$(grep -cE '^--- (PASS|FAIL|SKIP): ' "$OUT/race.out")
  failed=$(grep -cE '^--- FAIL: ' "$OUT/race.out")
  races=$(grep -c 'WARNING: DATA RACE' "$OUT/race.out")
  grep -E '^(--- FAIL|panic:)' "$OUT/race.out" | head -20
  tail -2 "$OUT/race.out"
  [ "$tests" -gt 0 ] || { echo "-test.run '$RUN' matched no test in ${PKGS}"; rc=1; }
  summary "race=$rc tests=$tests failed=$failed races=$races"
  exit $rc
fi
pkgs=$(go_packages) || { summary "go list failed"; exit 1; }
nice -n 10 go test -race -count=1 -p "${RACE_P:-3}" -timeout 60m ${RUN:+-run "$RUN"} $pkgs > "$OUT/race.out" 2>&1
rc=$?
grep -E '^(FAIL|--- FAIL|panic)' "$OUT/race.out" | head -20
summary "race=$rc ok=$(grep -c '^ok' "$OUT/race.out") fail=$(grep -c '^FAIL' "$OUT/race.out") races=$(grep -c 'WARNING: DATA RACE' "$OUT/race.out") packages=$(echo $pkgs | wc -w)"
exit $rc
