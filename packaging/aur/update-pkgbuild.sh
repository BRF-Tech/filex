#!/usr/bin/env bash
# Points the AUR package at a released version: pkgver, pkgrel and sha256sums
# in filex-app-bin/PKGBUILD, then .SRCINFO regenerated beside it.
#
#   packaging/aur/update-pkgbuild.sh 0.44.0
#       downloads that release's .deb and LICENSE from GitHub and hashes them
#   packaging/aur/update-pkgbuild.sh 0.44.0 --deb desktop/release/filex-desktop-amd64.deb
#       hashes a local copy of the .deb instead (CI: the file it just uploaded)
#   ... --pkgrel 2     a packaging-only fix of the same version
#   packaging/aur/update-pkgbuild.sh --srcinfo-only
#       rewrites .SRCINFO from PKGBUILD as it stands (after a hand edit of a
#       field other than pkgver/pkgrel/sha256sums)
#
# ⚠ It pushes NOTHING. The AUR repository (ssh://aur@aur.archlinux.org/
# filex-app-bin.git) and the SSH key registered with the AUR account
# belong to the maintainer; copy PKGBUILD and .SRCINFO there and commit both —
# the AUR refuses a push whose .SRCINFO does not match.
#
# .SRCINFO is what `makepkg --printsrcinfo` prints, written here without
# makepkg so this runs on any Linux (the release runner is Ubuntu). The key
# order follows makepkg's srcinfo.sh; the output was compared byte for byte
# with makepkg's own in an Arch container.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
pkgdir="$here/filex-app-bin"
pkgbuild="$pkgdir/PKGBUILD"

ver="" deb="" rel=1 only=0
while [ $# -gt 0 ]; do
  case "$1" in
    --srcinfo-only) only=1; shift ;;
    --deb) deb="$2"; shift 2 ;;
    --pkgrel) rel="$2"; shift 2 ;;
    -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
    *) [ -z "$ver" ] || { echo "unexpected argument: $1" >&2; exit 2; }; ver="${1#v}"; shift ;;
  esac
done
[ "$only" = 1 ] || [[ "$ver" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "usage: $0 <version> [--deb file] [--pkgrel n] | --srcinfo-only" >&2; exit 2; }
[[ "$rel" =~ ^[1-9][0-9]*$ ]] || { echo "--pkgrel must be a positive integer" >&2; exit 2; }
if [ -n "$deb" ] && [ ! -f "$deb" ]; then echo "no such file: $deb" >&2; exit 2; fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

if [ "$only" = 0 ]; then
# The sources, as PKGBUILD spells them for this version: PKGBUILD owns the URLs.
mapfile -t sources < <(bash -c 'set -e; source "$1"; pkgver="$2"; eval "$(sed -n "/^source=(/,/)/p" "$1")"; printf "%s\n" "${source[@]}"' _ "$pkgbuild" "$ver")
[ ${#sources[@]} -gt 0 ] || { echo "could not read source=() from $pkgbuild" >&2; exit 1; }

sums=()
for s in "${sources[@]}"; do
  name="${s%%::*}" url="${s#*::}"
  if [ -n "$deb" ] && [[ "$name" == *.deb ]]; then
    f="$deb"
  else
    f="$work/$name"
    curl -fsSL --retry 3 -o "$f" "$url" || { echo "download failed: $url" >&2; exit 1; }
  fi
  sums+=("$(sha256sum "$f" | cut -d' ' -f1)")
  echo "  $name  ${sums[-1]}"
done

# PKGBUILD: three fields, nothing else touched.
SUMS="$(printf "'%s'\n" "${sums[@]}")" VER="$ver" REL="$rel" perl -0pi -e '
  my @s = split /\n/, $ENV{SUMS};
  my $block = "sha256sums=(" . join("\n            ", @s) . ")";
  s/^pkgver=.*$/pkgver=$ENV{VER}/m or die "no pkgver\n";
  s/^pkgrel=.*$/pkgrel=$ENV{REL}/m or die "no pkgrel\n";
  s/^sha256sums=\([^)]*\)/$block/m or die "no sha256sums\n";
' "$pkgbuild"
fi

# .SRCINFO from the PKGBUILD itself (bash evaluates it exactly as makepkg does).
bash -c '
  set -e
  source "$1"
  out() { local k="$1"; shift; local v; for v in "$@"; do [ -n "$v" ] && printf "\t%s = %s\n" "$k" "$v"; done; return 0; }
  printf "pkgbase = %s\n" "$pkgname"
  out pkgdesc "$pkgdesc"; out pkgver "$pkgver"; out pkgrel "$pkgrel"; out epoch "${epoch:-}"
  out url "$url"; out install "${install:-}"; out changelog "${changelog:-}"
  out arch "${arch[@]}"; out groups "${groups[@]}"; out license "${license[@]}"
  out checkdepends "${checkdepends[@]}"; out makedepends "${makedepends[@]}"
  out depends "${depends[@]}"; out optdepends "${optdepends[@]}"; out provides "${provides[@]}"
  out conflicts "${conflicts[@]}"; out replaces "${replaces[@]}"; out noextract "${noextract[@]}"
  out options "${options[@]}"; out backup "${backup[@]}"; out source "${source[@]}"
  out validpgpkeys "${validpgpkeys[@]}"
  for a in ck md5 sha1 sha224 sha256 sha384 sha512 b2; do
    eval "out ${a}sums \"\${${a}sums[@]}\""
  done
  printf "\npkgname = %s\n" "$pkgname"
' _ "$pkgbuild" > "$pkgdir/.SRCINFO"

if [ "$only" = 1 ]; then echo ".SRCINFO rewritten from PKGBUILD"; else echo "PKGBUILD + .SRCINFO -> $ver-$rel"; fi
