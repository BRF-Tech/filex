#!/usr/bin/env python3
"""Build the release manifest that installs poll (docs/UPDATES.md).

The manifest is filex's own document rather than a read of the git tags,
because one of its fields cannot be derived from a tag:

  auto_ok     — kill switch: pull a bad release out of AUTOMATIC distribution
                without deleting it. Set to false and every install stops
                taking it by itself; the release stays downloadable.

  migrations  — marks releases that change the schema; the policy engine will
                not apply one without a confirmation (so a backup is taken).
                ⚠⚠ DERIVED, not typed: a release carries migrations when its
                tag holds a migration file that no earlier tag held. It used to
                be a hand-kept list in the publishing wrapper, and the list went
                stale — measured 2026-09-14: the live manifest did not mark
                v0.31.0 (00030_api_token_kind) at all, and a run of the wrapper
                would also have dropped v0.34.0 … v0.41.0. `--migrations` still
                adds a version by hand; nothing can take a derived one away.

Everything else (versions, dates, asset URLs, SHA-256 digests) comes from the
GitHub releases, so the digests are the ones goreleaser published. Every
release is listed (paged), not the newest N: an install on an old version still
needs the entries between it and the latest to decide a safe path.

Usage:
    python3 scripts/gen-update-manifest.py > stable.json
    python3 scripts/gen-update-manifest.py --limit 20 --out stable.json

    # mark a release as unsafe for automatic upgrades
    python3 scripts/gen-update-manifest.py --no-auto v0.7.3 --no-auto v0.7.4

    # which releases change the schema, as read from the tags (no network)
    python3 scripts/gen-update-manifest.py --print-migrations

Requires only the standard library. A GITHUB_TOKEN in the environment raises
the API rate limit but is not needed for a public repository.
"""

import argparse
import json
import os
import re
import subprocess
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


REPO_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SEMVER_TAG = re.compile(r"^v(\d+)\.(\d+)\.(\d+)$")
# Every layout the migrations have lived in: backend/db/migrations/<dialect>/
# today, a flat migrations/ directory in the earliest releases.
MIGRATION_FILE = re.compile(r"(?:^|/)migrations/(?:[a-z]+/)?(\d{5}_[^/]+\.sql)$")


def git(repo_dir: str, *args: str) -> str:
    r = subprocess.run(["git", "-C", repo_dir, *args], capture_output=True, text=True)
    if r.returncode != 0:
        raise RuntimeError(f"git {' '.join(args)}: {r.stderr.strip()}")
    return r.stdout


def migration_releases(repo_dir: str) -> tuple[set, set]:
    """(versions that ADD a migration file, every semver tag present locally).

    Walks the tags in version order and marks a tag whose tree holds a
    migration file name no earlier tag held. Names, not paths: a migration
    that moved directories is not a new one.
    """
    tags = [t for t in git(repo_dir, "tag").split() if SEMVER_TAG.match(t)]
    tags.sort(key=lambda t: tuple(int(x) for x in SEMVER_TAG.match(t).groups()))
    seen: set = set()
    marked: set = set()
    for tag in tags:
        names = set()
        for path in git(repo_dir, "ls-tree", "-r", "--name-only", tag).splitlines():
            m = MIGRATION_FILE.search(path)
            if m:
                names.add(m.group(1))
        if seen and names - seen:
            marked.add(tag.lstrip("v"))
        seen |= names
    return marked, {t.lstrip("v") for t in tags}


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
    ap.add_argument("--limit", type=int, default=0, help="how many releases to include (0 = all)")
    ap.add_argument("--repo-dir", default=REPO_DIR,
                    help="git checkout whose tags decide `migrations` (default: this repository)")
    ap.add_argument("--print-migrations", action="store_true",
                    help="print the versions that carry migrations, read from the tags, and exit")
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
        help="also mark a version as carrying schema migrations (on top of the derived ones)",
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

    derived, local_tags = migration_releases(args.repo_dir)
    if args.print_migrations:
        key = lambda v: tuple(int(x) for x in v.split("."))
        for v in sorted(derived, key=key):
            print(f"v{v}")
        return 0

    no_auto = {v.lstrip("v") for v in args.no_auto}
    migrations = derived | {v.lstrip("v") for v in args.migrations}
    security = {v.lstrip("v") for v in args.security}
    min_versions = {}
    for pair in args.min_version:
        if "=" in pair:
            k, v = pair.split("=", 1)
            min_versions[k.lstrip("v")] = v

    releases = []
    page = 1
    while True:
        batch = http_json(f"{API}?per_page=100&page={page}")
        releases.extend(batch)
        if len(batch) < 100 or (args.limit and len(releases) >= args.limit):
            break
        page += 1
    out = []
    for rel in releases:
        if rel.get("draft") or rel.get("prerelease"):
            continue
        if args.limit and len(out) >= args.limit:
            break
        tag = rel["tag_name"]
        bare = tag.lstrip("v")
        # A release this checkout has no tag for cannot be judged, and a
        # silent `migrations: false` is exactly the wrong answer to publish.
        if SEMVER_TAG.match(tag) and bare not in local_tags:
            print(f"error: {tag} is published but not tagged in {args.repo_dir} — "
                  "`git fetch --tags` first", file=sys.stderr)
            return 2
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
