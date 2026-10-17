#!/usr/bin/env bash
# The desktop side of a nightly build (task #266): from the PUBLIC form of the
# commit the night found green (scripts/chain/nightly-export.sh, as the image),
# the amd64 snap for the Snap Store's edge channel, and that public form as a
# source archive for GitHub's runners, which make every desktop package and
# the arm64 snap from it (.github/workflows/nightly-desktop.yml). Local only:
# it builds and keeps; scripts/chain/nightly.mjs uploads the snap and pushes
# the archive.
#
# scripts/chain/nightly.mjs runs it under the build lock, with:
#   ND_SRC             the nightly checkout, at the commit that ran green
#   ND_SHA             that commit
#   ND_PUBLIC_DIR      a clone of the public repository of its own (the image's
#                      is NB_PUBLIC_DIR: the two never share a staged tree)
#   ND_PUBLIC_REMOTE   where that clone comes from
#   ND_OUT             where the logs and the files go (files/, targets.txt,
#                      SHA256SUMS)
#   ND_VERSION         the version every package carries (desktopVersion)
#   ND_TARGETS         snap, source - comma-separated:
#                        snap    the amd64 snap (files/filex-desktop-amd64.snap)
#                        source  the public tree as files/filex-nightly-src.tar.gz,
#                                taken before anything is built in it
#   ND_IMAGE           the container the packages are made in
#                      (electron-builder's own: Node, wine, rpm)
#   ND_GOROOT          the Go toolchain mounted into it; copied from the
#                      golang image of backend/go.mod when it is not there yet
#   ND_GOMODCACHE ND_GOCACHE ND_PNPM_STORE ND_ELECTRON_CACHE   the caches
#   ND_SNAPCRAFT       the snapcraft the host packs the snap with (in LXD)
#
# Why the snap is packed here and not in the container: since 0.55 the snap
# is core24, made by snapcraft in LXD (desktop/electron-builder.yml,
# `snapcraft:`), and a container has no LXD. The container leaves the x64 app
# unpacked with its amd64 CLI, and electron-builder on this host packs the snap
# from it (--prepackaged). The arm64 snap is not packed here: snapcraft
# cannot cross-build it, and this host is x64 - GitHub's arm64 runner makes it
# from the source archive.
#
# Exit: 0 every target built; 1 one or more did not (targets.txt says which);
# 2 nothing could start (the export refused the commit, no Go).
set -uo pipefail
: "${ND_SRC:?}" "${ND_SHA:?}" "${ND_PUBLIC_DIR:?}" "${ND_PUBLIC_REMOTE:?}" "${ND_OUT:?}"
: "${ND_VERSION:?}" "${ND_TARGETS:?}" "${ND_IMAGE:?}" "${ND_GOROOT:?}"
: "${ND_GOMODCACHE:?}" "${ND_GOCACHE:?}" "${ND_PNPM_STORE:?}" "${ND_ELECTRON_CACHE:?}"
SNAPCRAFT="${ND_SNAPCRAFT:-snapcraft}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
say() { echo "$(date -u +%FT%TZ) nightly-desktop: $*"; }
. "$HERE/nightly-export.sh"

mkdir -p "$ND_OUT/files" "$ND_GOMODCACHE" "$ND_GOCACHE" "$ND_PNPM_STORE" "$ND_ELECTRON_CACHE"
: > "$ND_OUT/targets.txt"
trap 'nightly_public_reset "$ND_PUBLIC_DIR" >/dev/null 2>&1 || true' EXIT

say "export of ${ND_SHA:0:8} into $ND_PUBLIC_DIR"
if ! nightly_public_export "$ND_SRC" "$ND_SHA" "$ND_PUBLIC_DIR" "$ND_PUBLIC_REMOTE" "$ND_OUT/export.log"; then
  exit 2
fi

# The source archive first: the public tree as the export left it, before a
# build writes node_modules and the packages into it.
case ",$ND_TARGETS," in
  *,source,*)
    if tar --exclude=./.git -C "$ND_PUBLIC_DIR" -czf "$ND_OUT/files/filex-nightly-src.tar.gz" . 2> "$ND_OUT/source.log"; then
      echo "source built" >> "$ND_OUT/targets.txt"
      say "the source archive: $(du -h "$ND_OUT/files/filex-nightly-src.tar.gz" | cut -f1)"
    else
      cat "$ND_OUT/source.log"
      echo "source failed" >> "$ND_OUT/targets.txt"
    fi
    ;;
esac
container_targets=$(printf '%s' ",$ND_TARGETS," | sed 's/,source,/,/g; s/^,//; s/,$//')
if [ -z "$container_targets" ]; then
  say "made: $(tr '\n' ' ' < "$ND_OUT/targets.txt")"
  grep -q ' failed$' "$ND_OUT/targets.txt" && exit 1
  exit 0
fi

if [ ! -x "$ND_GOROOT/bin/go" ]; then
  gover=$(awk '$1 == "go" { print $2; exit }' "$ND_PUBLIC_DIR/backend/go.mod")
  [ -n "$gover" ] || { say "backend/go.mod names no Go version"; exit 2; }
  say "copying the Go toolchain of golang:$gover to $ND_GOROOT"
  name="fxnightly-desktop-goroot-$$"
  docker rm -f "$name" >/dev/null 2>&1
  if ! docker create --name "$name" "golang:$gover" >/dev/null; then
    say "docker create golang:$gover failed"
    exit 2
  fi
  mkdir -p "$(dirname "$ND_GOROOT")"
  docker cp "$name:/usr/local/go" "$ND_GOROOT"
  rc=$?
  docker rm -f "$name" >/dev/null 2>&1
  [ "$rc" = 0 ] || { say "copying the Go toolchain failed"; exit 2; }
fi

say "the app in $ND_IMAGE: $container_targets ($ND_VERSION)"
docker run --rm --name "fxnightly-desktop-$$" \
  -v "$ND_PUBLIC_DIR:/w/src" -v "$ND_OUT:/w/out" -v "$HERE:/w/chain:ro" \
  -v "$ND_GOROOT:/usr/local/go:ro" -v "$ND_GOMODCACHE:/w/cache/gomod" -v "$ND_GOCACHE:/w/cache/gocache" \
  -v "$ND_PNPM_STORE:/w/cache/pnpm-store" -v "$ND_ELECTRON_CACHE:/w/cache/electron" \
  -e "ND_VERSION=$ND_VERSION" -e "ND_TARGETS=$container_targets" \
  -e "PATH=/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin" \
  -e GOMODCACHE=/w/cache/gomod -e GOCACHE=/w/cache/gocache -e GOFLAGS=-buildvcs=false -e GOTOOLCHAIN=local \
  -e npm_config_store_dir=/w/cache/pnpm-store -e ELECTRON_CACHE=/w/cache/electron -e electron_config_cache=/w/cache/electron \
  -e COREPACK_ENABLE_DOWNLOAD_PROMPT=0 -e NODE_OPTIONS=--max-old-space-size=6144 \
  "$ND_IMAGE" bash /w/chain/nightly-desktop-job.sh > "$ND_OUT/container.log" 2>&1
crc=$?
tail -15 "$ND_OUT/container.log"
[ "$crc" = 0 ] || say "the container ended $crc (log container.log)"

# The snap, from the app the container left unpacked: electron-builder on
# this host, which calls snapcraft (LXD). Its own line in targets.txt
# replaces the container's.
case ",$ND_TARGETS," in
  *,snap,*)
    if grep -qx 'snap built' "$ND_OUT/targets.txt"; then
      grep -vx 'snap built' "$ND_OUT/targets.txt" > "$ND_OUT/targets.tmp"
      mv "$ND_OUT/targets.tmp" "$ND_OUT/targets.txt"
      shims="$ND_OUT/shims"
      mkdir -p "$shims"
      corepack enable --install-directory "$shims" pnpm >/dev/null 2>&1 || true
      snapdir=$(dirname "$(command -v "$SNAPCRAFT" 2>/dev/null || echo /snap/bin/snapcraft)")
      say "the snap: electron-builder --linux snap on this host ($SNAPCRAFT)"
      if (cd "$ND_PUBLIC_DIR/desktop" &&
        PATH="$shims:$snapdir:$PATH" ELECTRON_CACHE="$ND_ELECTRON_CACHE" electron_config_cache="$ND_ELECTRON_CACHE" \
          ./node_modules/.bin/electron-builder --linux snap --x64 --publish never --prepackaged release/linux-unpacked) > "$ND_OUT/snap.log" 2>&1 &&
        [ -f "$ND_PUBLIC_DIR/desktop/release/filex-desktop-amd64.snap" ]; then
        cp "$ND_PUBLIC_DIR/desktop/release/filex-desktop-amd64.snap" "$ND_OUT/files/"
        echo "snap built" >> "$ND_OUT/targets.txt"
      else
        tail -20 "$ND_OUT/snap.log"
        echo "snap failed" >> "$ND_OUT/targets.txt"
        say "the snap was not packed (log snap.log)"
      fi
    fi
    ;;
esac

(cd "$ND_OUT/files" && sha256sum filex-desktop-* > ../SHA256SUMS 2>/dev/null)
say "made: $(tr '\n' ' ' < "$ND_OUT/targets.txt")"
if grep -q ' failed$' "$ND_OUT/targets.txt"; then
  exit 1
fi
exit 0
