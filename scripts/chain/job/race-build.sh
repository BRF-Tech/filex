#!/bin/bash
# The -race test binary of one package (PKG), built once for every shard of
# that package's -race list lines (they run it with -test.run). Go image, as
# an unprivileged user; the binary goes to /w/run/bin/$BIN.
. /w/chain/job/common.sh
cd /w/src/backend || exit 1
mkdir -p /w/run/bin
nice -n 10 go test -race -c -o "/w/run/bin/$BIN" "$PKG" > "$OUT/build.out" 2>&1
rc=$?
tail -20 "$OUT/build.out"
summary "race-build=$rc $PKG"
exit $rc
