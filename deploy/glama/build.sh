#!/bin/sh
# Glama's one build step (deploy/glama/README.md): installs the filex release
# that web/package.json names, checked against that release's checksums.txt,
# and the two files Glama's mcp-proxy starts. Runs from the repository root,
# as root, in the Debian + Node.js image Glama generates.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
version=$(node -p 'require(process.argv[1]).version' "$root/web/package.json")
case "$(uname -m)" in
  x86_64 | amd64) arch=x86_64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "deploy/glama/build.sh: filex has no Linux release for $(uname -m)" >&2; exit 1 ;;
esac
name="filex_${version}_linux_${arch}.tar.gz"
base="https://github.com/BRF-Tech/filex/releases/download/v${version}"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
curl -fsSL --retry 3 -o "$work/$name" "$base/$name"
curl -fsSL --retry 3 -o "$work/checksums.txt" "$base/checksums.txt"
awk -v n="$name" '$2 == n' "$work/checksums.txt" > "$work/sum"
if [ ! -s "$work/sum" ]; then
  echo "deploy/glama/build.sh: $name is not in the checksums of v$version" >&2
  exit 1
fi
(cd "$work" && sha256sum -c sum)

tar -xzf "$work/$name" -C /usr/local/bin filex
install -m 0644 "$here/filex-mcp-bridge.cjs" /usr/local/lib/filex-mcp-bridge.cjs
install -m 0755 "$here/filex-mcp-stdio" /usr/local/bin/filex-mcp-stdio
filex --version
