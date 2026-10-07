#!/usr/bin/env bash
# The apps the browser suites install, for ci.yml's Playwright jobs:
#
#   bash .github/workflows/scripts/ci-apps.sh <dir>
#
# e-Signature and Convert from their latest public releases (plugin.wasm and
# filex-app.json, which is all a test install reads), and the three language
# packs that ship (Spanish, German, French) from their repositories - a pack
# is its manifest and has no build. The build host's chain mounts the same
# five from local checkouts (scripts/chain/job/e2e.sh); the specs find them
# through e2e/helpers/app-locations.mjs. The right-to-left specs install the
# repository's own fixture pack (e2e/helpers/langPack.ts).
#
# The variables are appended to $GITHUB_ENV for the steps after this one. An
# app that cannot be fetched fails the step: with FILEX_REQUIRE_WASM_FIXTURE
# set a missing app is a red spec anyway, and a red here says why.
set -euo pipefail
dir="${1:?usage: ci-apps.sh <dir>}"
mkdir -p "$dir"
for app in sign convert; do
  mkdir -p "$dir/$app"
  gh release download -R "BRF-Tech/filex-$app" -p filex-app.json -p plugin.wasm -D "$dir/$app" --clobber
  [ -s "$dir/$app/plugin.wasm" ] && [ -s "$dir/$app/filex-app.json" ] || { echo "::error::filex-$app: no plugin.wasm or filex-app.json in its latest release"; exit 1; }
  echo "filex-$app $(node -p "require('$dir/$app/filex-app.json').version")"
done
for lang in es de fr; do
  rm -rf "$dir/lang-$lang"
  git clone -q --depth 1 "https://github.com/BRF-Tech/filex-lang-$lang.git" "$dir/lang-$lang"
  [ -s "$dir/lang-$lang/filex-app.json" ] || { echo "::error::filex-lang-$lang has no filex-app.json"; exit 1; }
done
{
  echo "FILEX_SIGN_APP_DIR=$dir/sign"
  echo "FILEX_CONVERT_APP_DIR=$dir/convert"
  echo "FILEX_LANG_ES_APP_DIR=$dir/lang-es"
  echo "FILEX_E2E_LANG_PACK_ES=$dir/lang-es/filex-app.json"
  echo "FILEX_LANG_DE_APP_DIR=$dir/lang-de"
  echo "FILEX_LANG_FR_APP_DIR=$dir/lang-fr"
} >> "${GITHUB_ENV:?not in a GitHub job}"
