#!/bin/bash
# `pnpm shots --all` (nightly profile): every shot script in e2e/shots/,
# whatever its digest says, without the scenes that need an app build or a
# Document Server (--without-apps, as CI takes them). --all because the night
# is where a scene whose INPUTS missed something it shows is caught: its
# digest stands still, and only taking it again finds its pixels moved
# (lesson #1150). Every picture is compared with the published one; the
# contact sheet lists the ones that moved.
#
# ⚠ THIS is where the published set is taken (scripts/lib/shots-site.mjs
# PUBLISH_PLATFORM and PUBLISH_ENVIRONMENT, decided 2026-10-06): Linux, the
# chain's Playwright image, and run.mjs's FONTS_CONF - sans-serif and
# system-ui set in Liberation Sans. The build host itself sets them in DejaVu
# Sans: a picture taken there beside these is a README in two typefaces. The
# run says so in its review (SHOTS_ENVIRONMENT=chain), and accept warns about
# a run from anywhere else. A release takes them here too: a targeted run with
# CHAIN_EXTRAS=shots and --src the release checkout.
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
# The thumbnails a picture shows are the server's engines' work, as on the
# full image: the Playwright image has no ffmpeg, Ghostscript, poppler,
# ImageMagick or rsvg, and without them thumbnails.mjs would show the HEIC
# photo as not drawn (found preparing 0.53.0, 2026-10-07). The store scene serves its
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
SHOTS_ENVIRONMENT=chain node scripts/shots.mjs --all --skip packages,web,embed --without-apps > "$OUT/shots.out" 2>&1
rc=$?
grep -E '^\[shots\] ' "$OUT/shots.out" | grep -iE 'left out|refus|fail|timed out|cleanup' | tail -40
tail -25 "$OUT/shots.out"
taken=$(find "$REL" -name '*.png' -newer "$STAMP" 2>/dev/null | wc -l)
total=$(find "$REL" -name '*.png' 2>/dev/null | wc -l)
mkdir -p "$OUT/screenshots"
[ -d "$REL" ] && cp -a "$REL/." "$OUT/screenshots/"
[ -d e2e/.artifacts/shots ] && cp -a e2e/.artifacts/shots "$OUT/artifacts"
summary "shots=$rc taken=$taken of $total in $REL (contact sheet: out/shots/artifacts/contact-sheet.html)"
exit $rc
