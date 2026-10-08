#!/bin/bash
# `pnpm shots --all` (nightly profile): every shot script in e2e/shots/,
# whatever its digest says - the app scenes and the ONLYOFFICE scene included
# (task #187). --all because the night is where a scene whose INPUTS missed
# something it shows is caught: its digest stands still, and only taking it
# again finds its pixels moved (lesson #1150). Every picture is compared with
# the published one; the contact sheet lists the ones that moved.
#
# ⚠ THIS is where the published set is taken (scripts/lib/shots-site.mjs
# PUBLISH_PLATFORM and PUBLISH_ENVIRONMENT, decided 2026-10-06): Linux, the
# chain's Playwright image, and run.mjs's FONTS_CONF - sans-serif and
# system-ui set in Liberation Sans. The build host itself sets them in DejaVu
# Sans: a picture taken there beside these is a README in two typefaces. Until
# #187 the app and Document Server scenes were left out here (--without-apps)
# and taken on the host, and the README carried both faces; now every scene
# is taken here, in one run. The run says so in its review
# (SHOTS_ENVIRONMENT=chain), and accept warns about a run from anywhere else.
# A release takes them here too: a targeted run with CHAIN_EXTRAS=shots and
# --src the release checkout (CHAIN_SHOTS_ONLY narrows it to some scripts).
#
# What the scenes beyond the product need, and where it comes from:
#   - the app builds and the language packs (apps.mjs, apppermissions.mjs,
#     langpack.mjs, pluginrequests.mjs, signing.mjs): the directories
#     CHAIN_SIGN_APP_DIR, CHAIN_CONVERT_APP_DIR and CHAIN_LANG_{ES,DE,FR}_DIR
#     name, which run.mjs mounts at /w/apps/<name> - the names of
#     e2e/helpers/app-locations.mjs. Copied into the job before anything runs
#     (only what locateApp reads: filex-app.json, and plugin.wasm for an
#     app), so a directory another job rewrites mid-run - the nightly
#     translation's pack checkouts, say - cannot change under a scene (lesson
#     #993). The Arabic pack (lang-ar) is the suite's right-to-left fixture
#     and is never handed to a picture;
#   - a Document Server (csvoffice.mjs): the chain's own, which run.mjs starts
#     for this job (plan.mjs DS_EXTRAS). The scene's filex listens on every
#     address, and the server calls it back at http://filex:<port> - `filex`
#     is the alias run.mjs (dsStart) gives the network every job shares, on
#     the server's network, so no forwarder is needed as e2e.sh's is;
#   - the converter's engines (apps.mjs): installed below with the thumbnail
#     engines, and SHOTS_ENGINES=host - this container has no Docker to run
#     the full image instead, and a missing engine must say so, not try.
# Without one of them the run is refused before it builds anything (shots.mjs
# names what is missing), unless CHAIN_REQUIRE_APPS=0, which leaves the app
# and Document Server scenes out as before (--without-apps).
#
# --keep-going: a scene that fails does not stop the others. A language pack
# that is behind this tree fails langpack.mjs (it refuses to photograph a pack
# under 100%), and without it every scene after it would go untaken that
# night. The run is still a failure, and its review names every failed scene.
#
# SHOTS_PACKS_BEHIND=warn (plan.mjs sets it in the nightly profile only): when
# the run's ONLY failures are scenes whose language packs are behind this tree
# (langpack.mjs exits PACKS_BEHIND_EXIT; shots-site.mjs onlyPacksBehind reads
# the run's review), the job is green with a warning (common.sh warn) instead
# of red - the strings the day added are translated the night after (the maintainer,
# 2026-10-08, task #187). Anywhere else - a release's targeted
# CHAIN_EXTRAS=shots run - it stays red, and accept refuses the run either way.
#
# The build job made the packages, the web build and the embed;
# shots builds only its own stamped binary from them (--skip
# packages,web,embed), and verifies it serves this web build before it shoots.
# A shot script that no longer fits the product turns the night red, not the
# release day. Playwright image, root, Go mounted at /usr/local/go (the
# binary, and the example plugin the plugins scene installs).
#
# The pictures land where this release's go (e2e/shots/release.mjs), in the
# checkout under test - which the nightly run resets before the next night.
# This job keeps them, with the contact sheet, in /w/run/out/shots.
. /w/chain/job/common.sh
cd /w/src || exit 1
REL=$(node -e "import('/w/src/e2e/shots/release.mjs').then((m) => console.log(m.SHOTS_ROOT_REL))") || REL=""
[ -n "$REL" ] || { summary "shots=1 cannot read SHOTS_ROOT_REL from e2e/shots/release.mjs"; exit 1; }
go version

APPS=/tmp/shots-apps
rm -rf "$APPS"
missing=""
for name in convert sign lang-es lang-de lang-fr; do
  case "$name" in
    sign) var=FILEX_SIGN_APP_DIR need="filex-app.json plugin.wasm" ;;
    convert) var=FILEX_CONVERT_APP_DIR need="filex-app.json plugin.wasm" ;;
    lang-es) var=FILEX_LANG_ES_APP_DIR need="filex-app.json" ;;
    lang-de) var=FILEX_LANG_DE_APP_DIR need="filex-app.json" ;;
    lang-fr) var=FILEX_LANG_FR_APP_DIR need="filex-app.json" ;;
  esac
  ok=1
  for f in $need; do
    [ -f "/w/apps/$name/$f" ] || ok=0
  done
  if [ "$ok" = 1 ]; then
    mkdir -p "$APPS/$name"
    for f in $need; do
      cp "/w/apps/$name/$f" "$APPS/$name/$f" || { summary "shots=1 cannot copy /w/apps/$name/$f"; exit 1; }
    done
    export "$var=$APPS/$name"
  else
    missing="$missing $name"
  fi
done
echo "apps: sign=${FILEX_SIGN_APP_DIR:-none} convert=${FILEX_CONVERT_APP_DIR:-none} es=${FILEX_LANG_ES_APP_DIR:-none} de=${FILEX_LANG_DE_APP_DIR:-none} fr=${FILEX_LANG_FR_APP_DIR:-none}"

if [ "${DS:-0}" = 1 ]; then
  SHOTS_ONLYOFFICE_JWT="$(cat "$DS_SECRET_FILE")" || { summary "shots=1 cannot read $DS_SECRET_FILE"; exit 1; }
  export SHOTS_ONLYOFFICE_JWT
  export SHOTS_ONLYOFFICE_URL="$DS_URL" SHOTS_ONLYOFFICE_CALLBACK_HOST=filex
  echo "document server: $(curl -s -m 5 "$DS_URL/healthcheck")"
else
  missing="$missing document-server"
fi

if [ -z "$missing" ] || [ "${REQUIRE_APPS:-1}" = 1 ]; then
  SCOPE=--with-apps
  scope="every scene"
  [ -z "$missing" ] || scope="every scene, but missing:$missing"
else
  SCOPE=--without-apps
  scope="without the app and Document Server scenes (CHAIN_REQUIRE_APPS=0, missing:$missing)"
fi
if [ -n "${SHOTS_ONLY:-}" ]; then
  MODE=(--only "$SHOTS_ONLY")
  scope="only $SHOTS_ONLY, $scope"
else
  MODE=(--all)
fi
echo "scope: $scope"

# The thumbnails a picture shows are the server's engines' work, as on the
# full image: the Playwright image has no ffmpeg, Ghostscript, poppler,
# ImageMagick or rsvg, and without them thumbnails.mjs would show the HEIC
# photo as not drawn (found preparing 0.53.0, 2026-10-07). The same five are
# the converter's engines (apps.mjs; its sixth, office, is the Document
# Server). The store scene serves its
# store as https://store.example.com (e2e/shots/store.mjs), so its pictures
# name a store and not 127.0.0.1: the name resolves here, the job runs as root,
# port 443 is free and openssl is in the image.
export DEBIAN_FRONTEND=noninteractive
(apt-get update -qq && apt-get install -y -qq --no-install-recommends ffmpeg imagemagick libheif-plugin-libde265 ghostscript poppler-utils librsvg2-bin) > "$OUT/apt.out" 2>&1 \
  || { summary "shots=1 the thumbnail engines did not install (out/shots/apt.out)"; exit 1; }
grep -q ' store.example.com$' /etc/hosts || echo '127.0.0.1 store.example.com' >> /etc/hosts
export SHOTS_STORE_HOST=store.example.com
# Code text (keys, paths, commands, the Ctrl+K hint) is set in `monospace`,
# which this image resolves to a CJK face, and Ghostscript's fonts above
# turn it into a Courier clone that joins "fi" and "ff" in a key: pinned to
# Liberation Mono here, in this container alone, as sans-serif is in
# run.mjs's FONTS_CONF.
cat > /etc/fonts/local.conf <<'XML'
<?xml version="1.0"?>
<!DOCTYPE fontconfig SYSTEM "fonts.dtd">
<fontconfig>
  <alias binding="strong"><family>monospace</family><prefer><family>Liberation Mono</family></prefer></alias>
</fontconfig>
XML
STAMP="$OUT/started"
: > "$STAMP"
SHOTS_ENVIRONMENT=chain SHOTS_ENGINES=host node scripts/shots.mjs "${MODE[@]}" --keep-going "$SCOPE" --skip packages,web,embed > "$OUT/shots.out" 2>&1
rc=$?
grep -E '^\[shots\] ' "$OUT/shots.out" | grep -iE 'left out|refus|fail|timed out|cleanup' | tail -40
tail -25 "$OUT/shots.out"
taken=$(find "$REL" -name '*.png' -newer "$STAMP" 2>/dev/null | wc -l)
total=$(find "$REL" -name '*.png' 2>/dev/null | wc -l)
mkdir -p "$OUT/screenshots"
[ -d "$REL" ] && cp -a "$REL/." "$OUT/screenshots/"
[ -d e2e/.artifacts/shots ] && cp -a e2e/.artifacts/shots "$OUT/artifacts"
behind=""
if [ "$rc" -ne 0 ] && [ "${SHOTS_PACKS_BEHIND:-}" = warn ]; then
  behind=$(node -e "import('/w/src/scripts/lib/shots-site.mjs').then((m) => { const fs = require('fs'); const f = '/w/src/' + m.REVIEW_REL; if (!fs.existsSync(f) || fs.statSync(f).mtimeMs < fs.statSync(process.argv[1]).mtimeMs) return; const s = m.onlyPacksBehind(JSON.parse(fs.readFileSync(f, 'utf8'))); if (s) console.log(s.join(' ')); })" "$STAMP") || behind=""
fi
if [ -n "$behind" ]; then
  warn "shots: the language packs are behind this tree, so $behind was not taken; every other scene was ($taken of $total pictures). node scripts/langpacks.mjs status says what is missing; the nightly translation adds it after this run."
  summary "shots=0 (warning: language packs behind this tree, $behind not taken) taken=$taken of $total in $REL, $scope (contact sheet: out/shots/artifacts/contact-sheet.html)"
  exit 0
fi
summary "shots=$rc taken=$taken of $total in $REL, $scope (contact sheet: out/shots/artifacts/contact-sheet.html)"
exit $rc
