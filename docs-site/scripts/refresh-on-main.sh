#!/usr/bin/env bash
#
# filex-docs-refresh.sh — keep docs.filex.sh following the GitHub releases.
#
# Installed on the `main` server as /root/filex-docs-refresh.sh and run from cron.
# This copy lives in the repo so the script is reviewable and versioned; the two
# are kept identical by hand (the checksum is printed on every run).
#
# What it does, in order:
#   1. regenerate docs/RELEASES.md from the GitHub Releases API
#   2. stop here if nothing changed (a rebuild that publishes identical bytes is
#      cost with no benefit — the site is only stale when a release appears)
#   3. build the VitePress site
#   4. sanity-gate the build (index + releases page present, plausible file count)
#   5. hand the result to the brf-mono StaticSites module, which does the atomic
#      swap into /srv/caddy-static/filex-docs and keeps .prev-filex-docs
#
# It never touches git. The prose in docs/*.md comes from the snapshot in
# SRC_DIR, which is refreshed by a normal deploy from the repository; this script
# only ever changes the Releases page. That is deliberate: a cron that pulled a
# branch could silently republish older documentation.
#
# Exit codes: 0 nothing to do / published · 1 build or deploy failed.

set -euo pipefail

SRC_DIR=/root/filex-docs-src
SITE_DIR="$SRC_DIR/docs-site"
DIST_DIR="$SITE_DIR/.vitepress/dist"
PAGE="$SRC_DIR/docs/RELEASES.md"
SLUG=filex-docs
ZIP=/root/filex-docs-dist.zip
STAMP=/root/.filex-docs-releases.sha256
MIN_FILES=30

log() { echo "[$(date -Is)] $*"; }
fail() { log "FAILED: $*"; exit 1; }

[ -d "$SITE_DIR" ] || fail "source snapshot missing: $SITE_DIR"
[ -d "$SITE_DIR/node_modules/vitepress" ] || fail "vitepress not installed in $SITE_DIR (run npm install there)"

log "refresh start (script sha256: $(sha256sum "$0" | cut -c1-12))"

cd "$SITE_DIR"

before=""
[ -f "$STAMP" ] && before="$(cat "$STAMP")"

if ! node scripts/fetch-releases.mjs; then
  fail "release generator refused to run"
fi

after="$(sha256sum "$PAGE" | awk '{print $1}')"

if [ "$after" = "$before" ]; then
  if [ "${1:-}" != "--force" ]; then
    log "no new release — RELEASES.md unchanged, nothing published"
    exit 0
  fi
  log "--force: RELEASES.md unchanged, rebuilding and republishing anyway"
else
  log "RELEASES.md changed (${before:-none} -> $after) — rebuilding the site"
fi

if ! npx vitepress build > /tmp/filex-docs-build.log 2>&1; then
  tail -30 /tmp/filex-docs-build.log
  fail "vitepress build"
fi

[ -f "$DIST_DIR/index.html" ] || fail "build produced no index.html"
[ -f "$DIST_DIR/RELEASES.html" ] || fail "build produced no RELEASES.html"
count=$(find "$DIST_DIR" -type f | wc -l)
[ "$count" -ge "$MIN_FILES" ] || fail "build produced only $count files (expected >= $MIN_FILES)"
log "build ok — $count files"

rm -f "$ZIP"
( cd "$DIST_DIR" && zip -qr "$ZIP" . )
[ -s "$ZIP" ] || fail "zip is empty"

docker cp "$ZIP" brf-mono:/tmp/filex-docs.zip >/dev/null || fail "docker cp into brf-mono"

# Fed to `artisan tinker` on stdin, which is psysh: no `<?php` opener, and one
# statement per line. Deploying through the module rather than copying files
# ourselves is what gives the atomic swap and the .prev-filex-docs rollback.
cat > /root/filex-docs-deploy.php <<'PHP'
$site = \Modules\StaticSites\Models\StaticSite::where('slug', 'filex-docs')->firstOrFail();
$res = app(\Modules\StaticSites\Services\SiteContentService::class)->deployZip($site, '/tmp/filex-docs.zip');
echo ($res['ok'] ? 'DEPLOY-OK: ' : 'DEPLOY-ERR: ').$res['message'].PHP_EOL;
PHP

out=$(docker exec -i brf-mono php artisan tinker < /root/filex-docs-deploy.php 2>&1 | tail -8) || true
log "staticsites: $(echo "$out" | tr '\n' ' ')"
echo "$out" | grep -q 'DEPLOY-OK:' || fail "StaticSites deploy did not report OK"

docker exec brf-mono rm -f /tmp/filex-docs.zip >/dev/null 2>&1 || true
rm -f "$ZIP"

echo "$after" > "$STAMP"
log "published — docs.filex.sh now serves $count files"
