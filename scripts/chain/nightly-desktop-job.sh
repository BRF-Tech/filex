#!/usr/bin/env bash
# The desktop app of a nightly build, made from the public tree of the
# night's commit (task #266) - one script for both places that run it:
#   - electron-builder's own container on the build host
#     (scripts/chain/nightly-desktop.sh: the tree at /w/src, out at /w/out,
#     Go at /usr/local/go), for the app the amd64 snap is packed from;
#   - GitHub's runners (.github/workflows/nightly-desktop.yml: ND_JOB_SRC and
#     ND_JOB_OUT name the tree and the out directory), for every desktop
#     package and the arm64 snap's app.
#
#   ND_VERSION   the version every package carries (nightly-lib.mjs desktopVersion)
#   ND_TARGETS   what to make, comma-separated:
#                  snap         the x64 app unpacked (release/linux-unpacked), with
#                               the amd64 CLI in it: the build host packs the snap
#                               from it (snapcraft needs LXD)
#                  snap-arm64   the same for arm64 (release/linux-arm64-unpacked),
#                               on an arm64 machine: the workflow packs the snap
#                  linux-x64    AppImage, .deb and .rpm, x64
#                  linux-arm64  AppImage, .deb and .rpm, arm64 (on an arm64 machine)
#                  windows      the NSIS installer and the portable .exe, x64 and arm64
#                  mac          the .dmg and the .zip, for this Mac's CPU
#   ND_JOB_SRC   the public tree (default /w/src)
#   ND_JOB_OUT   where the logs, targets.txt and files/ go (default /w/out)
#
# It writes one line per target to <out>/targets.txt ("<target> built" or
# "<target> failed") and copies the packages it made to <out>/files. A target
# that fails does not stop the others; the exit code is 1 when one failed.
#
# The CLI inside each package is built for that package's platform and CPU
# (desktop/scripts/fetch-cli.mjs), as release.yml's rows build it; a package
# is never signed (a nightly is not a release).
set -uo pipefail
: "${ND_VERSION:?}" "${ND_TARGETS:?}"
SRC="${ND_JOB_SRC:-/w/src}"
OUT="${ND_JOB_OUT:-/w/out}"
mkdir -p "$OUT/files"
: > "$OUT/targets.txt"
say() { echo "$(date -u +%FT%TZ) nightly-desktop-job: $*"; }
cd "$SRC" || exit 1

command -v pnpm >/dev/null 2>&1 || corepack enable pnpm >/dev/null 2>&1 || npm install -g pnpm@9 >/dev/null 2>&1
node --version
pnpm --version
go version

say "install, packages, web, embed"
if ! { pnpm install --frozen-lockfile &&
  pnpm -r --filter './packages/*' build &&
  pnpm --filter ./web build &&
  node scripts/sync-embed.mjs; } > "$OUT/build.log" 2>&1; then
  tail -30 "$OUT/build.log"
  for t in ${ND_TARGETS//,/ }; do echo "$t failed" >> "$OUT/targets.txt"; done
  say "the build the packages need failed (log build.log)"
  exit 1
fi

say "the desktop app as $ND_VERSION"
if ! { (cd desktop && npm version "$ND_VERSION" --no-git-tag-version --allow-same-version) &&
  (cd desktop && pnpm run build); } > "$OUT/desktop-build.log" 2>&1; then
  tail -30 "$OUT/desktop-build.log"
  for t in ${ND_TARGETS//,/ }; do echo "$t failed" >> "$OUT/targets.txt"; done
  say "the desktop app did not build (log desktop-build.log)"
  exit 1
fi

# No keychain is searched for a signing identity: a stray certificate on a
# runner would make a "signed" build Gatekeeper refuses (release.yml says the
# same). A nightly is never signed.
export CSC_IDENTITY_AUTO_DISCOVERY=false

failed=0
package() {
  local target="$1" log="$OUT/$1.log"
  shift
  say "$target"
  if (cd desktop && "$@") >> "$log" 2>&1; then
    return 0
  fi
  tail -20 "$log"
  return 1
}

for t in ${ND_TARGETS//,/ }; do
  ok=1
  case "$t" in
    linux-x64)
      package "$t" node scripts/fetch-cli.mjs --platform linux --arch x64 &&
        package "$t" pnpm exec electron-builder --linux AppImage deb rpm --x64 --publish never || ok=0
      ;;
    linux-arm64)
      package "$t" node scripts/fetch-cli.mjs --platform linux --arch arm64 &&
        package "$t" pnpm exec electron-builder --linux AppImage deb rpm --arm64 --publish never || ok=0
      ;;
    windows)
      package "$t" node scripts/fetch-cli.mjs --platform win32 --arch x64 &&
        package "$t" pnpm exec electron-builder --win nsis portable --x64 --publish never &&
        package "$t" node scripts/fetch-cli.mjs --platform win32 --arch arm64 &&
        package "$t" pnpm exec electron-builder --win nsis portable --arm64 --publish never || ok=0
      ;;
    mac)
      package "$t" node scripts/fetch-cli.mjs --platform darwin &&
        package "$t" pnpm exec electron-builder --mac dmg zip --publish never || ok=0
      ;;
    snap)
      package "$t" node scripts/fetch-cli.mjs --platform linux --arch x64 &&
        package "$t" pnpm exec electron-builder --linux dir --x64 --publish never || ok=0
      ;;
    snap-arm64)
      package "$t" node scripts/fetch-cli.mjs --platform linux --arch arm64 &&
        package "$t" pnpm exec electron-builder --linux dir --arm64 --publish never || ok=0
      ;;
    *)
      say "unknown target $t"
      ok=0
      ;;
  esac
  if [ "$ok" = 1 ]; then
    echo "$t built" >> "$OUT/targets.txt"
  else
    echo "$t failed" >> "$OUT/targets.txt"
    failed=1
  fi
done

shopt -s nullglob
for f in desktop/release/filex-desktop-*; do
  case "$f" in
    *.blockmap|*.yml|*.yaml) ;;
    *) [ -f "$f" ] && cp "$f" "$OUT/files/" ;;
  esac
done
ls -la "$OUT/files"
exit "$failed"
