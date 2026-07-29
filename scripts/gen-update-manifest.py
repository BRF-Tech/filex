#!/usr/bin/env python3
"""Build the release manifest that installs poll (docs/UPDATES.md).

The manifest is filex's own document rather than a read of the git tags,
because two of its fields cannot be derived from a tag:

  auto_ok     — kill switch: pull a bad release out of AUTOMATIC distribution
                without deleting it. Set to false and every install stops
                taking it by itself; the release stays downloadable.
  migrations  — marks releases that change the schema. Patches must not carry
                one; the policy engine refuses to auto-apply a patch that does.

Everything else (versions, dates, asset URLs, SHA-256 digests) comes from the
GitHub releases, so the digests are the ones goreleaser published.

Usage:
    python3 scripts/gen-update-manifest.py > stable.json
    python3 scripts/gen-update-manifest.py --limit 20 --out stable.json

    # mark a release as unsafe for automatic upgrades
    python3 scripts/gen-update-manifest.py --no-auto v0.7.3 --no-auto v0.7.4

    # declare which releases changed the schema
    python3 scripts/gen-update-manifest.py --migrations v0.6.0 --migrations v0.4.2

Requires only the standard library. A GITHUB_TOKEN in the environment raises
the API rate limit but is not needed for a public repository.
"""

import argparse
import json
import os
import re
import sys
import urllib.error
import urllib.request

REPO = os.environ.get("FILEX_REPO", "BRF-Tech/filex")
API = f"https://api.github.com/repos/{REPO}/releases"
IMAGE = os.environ.get("FILEX_IMAGE", "ghcr.io/brf-tech/filex")

# goreleaser writes x86_64/i386 into archive names; the updater asks in Go's
# vocabulary (runtime.GOARCH), so translate on the way in.
ARCH_ALIASES = {"x86_64": "amd64", "i386": "386", "aarch64": "arm64"}

ARCHIVE_RE = re.compile(
    r"^filex_(?P<version>[0-9][^_]*)_(?P<os>[a-z]+)_(?P<arch>[a-zA-Z0-9_]+)\.(?:tar\.gz|zip)$"
)


def http_json(url: str):
    req = urllib.request.Request(url, headers=gh_headers())
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.load(r)


def http_text(url: str) -> str:
    req = urllib.request.Request(url, headers=gh_headers())
    with urllib.request.urlopen(req, timeout=30) as r:
        return r.read().decode("utf-8", "replace")


def gh_headers():
    h = {"Accept": "application/vnd.github+json", "User-Agent": "filex-manifest-builder"}
    tok = os.environ.get("GITHUB_TOKEN") or os.environ.get("GH_TOKEN")
    if tok:
        h["Authorization"] = f"Bearer {tok}"
    return h


def parse_checksums(text: str) -> dict:
    """checksums.txt is '<sha256>  <filename>' per line."""
    out = {}
    for line in text.splitlines():
        parts = line.split()
        if len(parts) >= 2:
            out[parts[-1]] = parts[0]
    return out


def assets_for(release: dict) -> list:
    by_name = {a["name"]: a for a in release.get("assets", [])}
    sums = {}
    if "checksums.txt" in by_name:
        try:
            sums = parse_checksums(http_text(by_name["checksums.txt"]["browser_download_url"]))
        except urllib.error.URLError as e:
            print(f"warn: cannot read checksums for {release['tag_name']}: {e}", file=sys.stderr)

    assets = []
    for name, a in sorted(by_name.items()):
        m = ARCHIVE_RE.match(name)
        if not m:
            continue
        digest = sums.get(name, "")
        if not digest:
            # An asset without a digest is unusable: the updater refuses to
            # install unverified bytes, so publishing it would only produce a
            # confusing failure later.
            print(f"warn: no sha256 for {name}, skipping", file=sys.stderr)
            continue
        arch = m.group("arch")
        assets.append(
            {
                "os": m.group("os"),
                "arch": ARCH_ALIASES.get(arch, arch),
                "url": a["browser_download_url"],
                "sha256": digest,
            }
        )
    return assets


def first_line(body: str) -> str:
    for line in (body or "").splitlines():
        line = line.strip().lstrip("#").strip()
        if line and not line.startswith("<!--"):
            return line[:200]
    return ""


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--limit", type=int, default=30, help="how many releases to include")
    ap.add_argument("--channel", default="stable")
    ap.add_argument("--out", help="write here instead of stdout")
    ap.add_argument(
        "--no-auto",
        action="append",
        default=[],
        metavar="VERSION",
        help="mark a version as NOT eligible for automatic upgrade (kill switch)",
    )
    ap.add_argument(
        "--migrations",
        action="append",
        default=[],
        metavar="VERSION",
        help="mark a version as carrying schema migrations",
    )
    ap.add_argument(
        "--security",
        action="append",
        default=[],
        metavar="VERSION",
        help="mark a version as a security release",
    )
    ap.add_argument("--min-version", action="append", default=[], metavar="VERSION=MIN",
                    help="a version that must not be jumped to directly, e.g. v1.0.0=v0.9.0")
    args = ap.parse_args()

    no_auto = {v.lstrip("v") for v in args.no_auto}
    migrations = {v.lstrip("v") for v in args.migrations}
    security = {v.lstrip("v") for v in args.security}
    min_versions = {}
    for pair in args.min_version:
        if "=" in pair:
            k, v = pair.split("=", 1)
            min_versions[k.lstrip("v")] = v

    releases = http_json(f"{API}?per_page={max(1, min(args.limit, 100))}")
    out = []
    for rel in releases:
        if rel.get("draft") or rel.get("prerelease"):
            continue
        tag = rel["tag_name"]
        bare = tag.lstrip("v")
        entry = {
            "version": tag,
            "date": (rel.get("published_at") or "")[:10],
            "auto_ok": bare not in no_auto,
            "migrations": bare in migrations,
            "notes": first_line(rel.get("body", "")),
            "notes_url": rel.get("html_url", ""),
            "image": f"{IMAGE}:{tag}",
            "assets": assets_for(rel),
        }
        if bare in security:
            entry["severity"] = "security"
        if bare in min_versions:
            entry["min_version"] = min_versions[bare]
        out.append(entry)

    doc = {"channel": args.channel, "releases": out}
    text = json.dumps(doc, indent=2, ensure_ascii=False) + "\n"
    if args.out:
        with open(args.out, "w", encoding="utf-8") as f:
            f.write(text)
        print(f"wrote {args.out} ({len(out)} releases)", file=sys.stderr)
    else:
        sys.stdout.write(text)
    return 0


if __name__ == "__main__":
    sys.exit(main())
