#!/bin/bash
# The documentation gates: every relative link resolves, the docs site
# builds, every in-page anchor lands, and the deploy YAML parses. Node image.
. /w/chain/job/common.sh
cd /w/src || exit 1
use_pnpm
node scripts/check-links.mjs > "$OUT/links.out" 2>&1; links=$?
tail -3 "$OUT/links.out"
(cd docs-site && npm run build > "$OUT/docs-build.out" 2>&1); site=$?
grep -E 'error|Error|dead link|build complete' "$OUT/docs-build.out" | head -10
node scripts/check-doc-anchors.mjs > "$OUT/anchors.out" 2>&1; anchors=$?
tail -5 "$OUT/anchors.out"
JY=$(find node_modules/.pnpm -maxdepth 1 -type d -name 'js-yaml@4*' | head -1)
[ -n "$JY" ] && JY="$JY/node_modules/js-yaml"
yaml=0
files=$(git -c safe.directory='*' ls-files -- '.goreleaser.yml' 'desktop/electron-builder.yml' 'deploy/*.yml' 'deploy/*.yaml' | grep -v '/templates/')
for f in $files; do
  if JY="/w/src/$JY" F="$f" node -e "require(process.env.JY).loadAll(require('fs').readFileSync(process.env.F, 'utf8'))" 2> "$OUT/yaml.err"; then
    echo "yaml ok  $f"
  else
    echo "yaml BAD $f: $(head -1 "$OUT/yaml.err")"
    yaml=1
  fi
done
[ -n "$JY" ] || { echo "js-yaml 4 not found under node_modules/.pnpm"; yaml=1; }
summary "check-links=$links docs-build=$site check-doc-anchors=$anchors yaml=$yaml ($(echo $files | wc -w) files)"
[ "$links$site$anchors$yaml" = 0000 ]
