#!/bin/bash
# One line of the Go list: vet + test (no -race) on PKGS minus EXCLUDE,
# -run RUN when set, with the PostgreSQL / MySQL / Redis / SMB sidecars
# (their addresses come in FILEX_TEST_*). Go image, as an unprivileged user.
. /w/chain/job/common.sh
cd /w/src/backend || exit 1
go version
pkgs=$(go_packages) || { summary "go list failed"; exit 1; }
nice -n 10 go test -count=1 -p "${GO_P:-6}" -timeout "${GO_TIMEOUT:-45m}" ${RUN:+-run "$RUN"} $pkgs > "$OUT/go.out" 2>&1
rc=$?
grep -E '^(FAIL|panic|--- FAIL)' "$OUT/go.out" | head -60
summary "go=$rc ok=$(grep -c '^ok' "$OUT/go.out") fail=$(grep -c '^FAIL' "$OUT/go.out") packages=$(echo $pkgs | wc -w)"
exit $rc
