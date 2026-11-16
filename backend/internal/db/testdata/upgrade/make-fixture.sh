#!/usr/bin/env bash
# Writes sqlite-v<version>.sql beside this script: the schema a first start of
# that filex release leaves in a new SQLite database, and its goose version
# rows. TestAnUpgradedSQLiteInstallHasTheSchemaOfAFreshOne
# (backend/internal/db/upgrade_schema_test.go) loads every such file, takes it
# to this tree's schema and compares the result with a fresh install.
#
#   bash backend/internal/db/testdata/upgrade/make-fixture.sh 0.10.0
#
# It runs the release's own published image (ghcr.io/brf-tech/filex:v<version>),
# not this tree's migrations at that tag: the point is what the release really
# did on its first start, which before 0.38 included DDL that no migration file
# held (internal/ops created pending_ops by hand). Needs docker and python3.
# The database is a throwaway one, and only its schema and the goose rows are
# written: no setting, user or key leaves the container.
set -euo pipefail

version="${1:-}"
case "$version" in
  "" | *[!0-9.]*) echo "usage: $0 <version, e.g. 0.10.0>" >&2; exit 2 ;;
esac

here="$(cd "$(dirname "$0")" && pwd)"
out="$here/sqlite-v$version.sql"
image="ghcr.io/brf-tech/filex:v$version"
name="filex-upgrade-fixture-$version-$$"
work="$(mktemp -d)"
[ -n "$work" ] && [ -d "$work" ] || { echo "no scratch directory" >&2; exit 1; }

cleanup() {
  docker rm -f -v "$name" >/dev/null 2>&1 || true
  rm -rf -- "$work"
}
trap cleanup EXIT

docker pull -q "$image" >/dev/null
docker run -d --name "$name" -p 127.0.0.1::5212 "$image" serve >/dev/null
port="$(docker port "$name" 5212/tcp | head -1 | sed 's/.*://')"

up=""
for _ in $(seq 1 90); do
  if curl -fsS -o /dev/null "http://127.0.0.1:$port/healthz" 2>/dev/null; then up=1; break; fi
  sleep 1
done
if [ -z "$up" ]; then
  docker logs "$name" 2>&1 | tail -40 >&2
  echo "v$version did not answer /healthz in 90 s" >&2
  exit 1
fi
# What the server does at start is done before it listens; a moment more
# for anything started beside the listener, then a clean stop, so SQLite
# folds its write-ahead log back into the file.
sleep 3
docker stop -t 30 "$name" >/dev/null
docker cp "$name:/data/." "$work/data"

python3 - "$work/data" "$out" "$version" <<'PY'
import os
import sqlite3
import sys

data, out, version = sys.argv[1], sys.argv[2], sys.argv[3]

db = None
for root, _, files in os.walk(data):
    for f in sorted(files):
        if not f.endswith((".sqlite", ".db")):
            continue
        p = os.path.join(root, f)
        c = sqlite3.connect(p)
        try:
            has = c.execute(
                "SELECT 1 FROM sqlite_master WHERE type='table' AND name='goose_db_version'"
            ).fetchone()
        finally:
            c.close()
        if has:
            db = p
if db is None:
    sys.exit("no database with a goose_db_version table under " + data)

c = sqlite3.connect(db)
objects = c.execute(
    "SELECT type, name, sql FROM sqlite_master"
    " WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%' ORDER BY rowid"
).fetchall()
goose = c.execute(
    "SELECT version_id, is_applied FROM goose_db_version ORDER BY id"
).fetchall()
c.close()

order = {"table": 0, "index": 1, "view": 2, "trigger": 3}
objects.sort(key=lambda o: order.get(o[0], 9))

with open(out, "w", encoding="utf-8", newline="\n") as f:
    f.write("-- The schema filex v%s leaves in a new SQLite database on its first start,\n" % version)
    f.write("-- and its goose version rows. Written by make-fixture.sh from the release's\n")
    f.write("-- published image; do not edit by hand.\n")
    f.write("--\n-- filex-fixture-version: %s\n" % version)
    f.write("-- filex-fixture-goose: %d\n\n" % max(v for v, applied in goose if applied))
    for _, _, sql in objects:
        f.write(sql.strip() + ";\n")
    f.write("\n")
    for v, applied in goose:
        f.write("INSERT INTO goose_db_version (version_id, is_applied) VALUES (%d, %d);\n" % (v, 1 if applied else 0))
print("wrote %s (%d objects, goose %d)" % (out, len(objects), max(v for v, a in goose if a)))
PY
