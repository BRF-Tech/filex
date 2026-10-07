#!/bin/bash
# e2e/realenv/run.sh (nightly profile): filex against the real ACME, SSO and
# office servers. A HOST job, the one job of the chain that is not a
# container: realenv starts containers of its own with this host's Docker and
# mounts the tree by its path here (run.mjs KIND.realenv, hostJobEnv).
#
# run.mjs gives it:
#   JOB, OUT          this job's name and output directory
#   SRC               the tree under test, which the build job built
#                     (bin/filex, e2e/node_modules)
#   REALENV_*         its prefix (<CHAIN_PREFIX>-re), REALENV_PULL=1, its work
#                     directory under OUT, the chain's Document Server and
#                     Playwright images, and REALENV_STAGES (empty: every stage)
#
# ⚠ No REALENV_LOCK: the chain holds the build lock for the whole run, and
# realenv waiting for the same lock would wait forever (hostJobEnv drops it).
#
# ⚠ realenv SKIPS a stage whose images it cannot get, and exits 0. Here a
# skipped stage is red, unless CHAIN_REALENV_ALLOW_SKIP=1: a night that ran no
# stage must not read as a night they passed.
set -u
set -f
: "${OUT:?OUT is not set}" "${SRC:?SRC is not set}"
mkdir -p "$OUT" "${REALENV_WORK:?REALENV_WORK is not set}"
summary() { echo "SUMMARY $*"; }
unset REALENV_LOCK
cd "$SRC" || { summary "realenv=1 no tree at $SRC"; exit 1; }
read -r -a STAGES <<< "${REALENV_STAGES:-}"
echo "realenv: prefix=$REALENV_PREFIX stages=${REALENV_STAGES:-all} work=$REALENV_WORK"
bash e2e/realenv/run.sh "${STAGES[@]}" > "$OUT/realenv.out" 2>&1
rc=$?
tail -40 "$OUT/realenv.out"
passed=$(grep -E 'realenv: passed:' "$OUT/realenv.out" | tail -1 | sed 's/.*passed: *//')
skipped=$(grep -c 'realenv: skipped:' "$OUT/realenv.out")
failed=$(grep -c 'realenv: FAILED:' "$OUT/realenv.out")
if [ "$rc" -eq 0 ] && [ "$skipped" -gt 0 ] && [ "${REALENV_ALLOW_SKIP:-0}" != 1 ]; then
  echo "a stage was skipped: not a pass here (CHAIN_REALENV_ALLOW_SKIP=1 allows it)"
  rc=1
fi
summary "realenv=$rc passed=${passed:-none} skipped=$skipped failed=$failed"
exit $rc
