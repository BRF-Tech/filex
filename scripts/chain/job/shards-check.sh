#!/bin/bash
# The shard list against the tree: `node scripts/test-shards.mjs check`.
# Red when a test runs in no shard or in two, a shard runs none, a package
# go list knows runs nowhere, or the list names a test file that is gone -
# the -race and Go jobs of this very run are cut from that list, so a red
# here means they may have left something out. Node image, Go mounted at
# /usr/local/go (the check asks `go list` and `go test -list`, which compile
# the test binaries of the two sharded packages and run nothing).
. /w/chain/job/common.sh
cd /w/src || exit 1
go version
node scripts/test-shards.mjs check > "$OUT/check.out" 2>&1
rc=$?
tail -40 "$OUT/check.out"
verdict=$(grep -E '^test shards:' "$OUT/check.out" | tail -1)
summary "shards-check=$rc ${verdict:-no verdict in check.out}"
exit $rc
