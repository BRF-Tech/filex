# Databases

filex keeps its file tree, users, shares, settings and job queue in one
database. Three engines are supported, and - since v0.38.0 - all three are
actually exercised: every change runs the migrations, compares the schema each
dialect builds against the SQLite one, and performs the writes an install makes
in its first five minutes, against a real PostgreSQL and a real MySQL server.

| Engine | Minimum | Use it when |
|---|---|---|
| **SQLite** (default) | bundled, pure Go, no CGO | one node, one process. The default and a perfectly good production answer for a team on one machine |
| **PostgreSQL** | 13+ (CI runs 17) | several filex processes, HA, an existing Postgres you back up already. The recommended choice for teams |
| **MySQL / MariaDB** | **MySQL 8.0.17+** (CI runs 8.4) or **MariaDB 11.4+** | you already run MySQL and would rather not add a second engine |

```bash
FILEX_DB_DRIVER=sqlite      # default; DSN optional, <data-dir>/instance.sqlite
FILEX_DB_DRIVER=postgres
FILEX_DB_DSN='postgres://filex:secret@db:5432/filex?sslmode=require'
FILEX_DB_DRIVER=mysql
FILEX_DB_DSN='filex:secret@tcp(db:3306)/filex'
```

Migrations run at startup. `filex migrate up | down | status` does the same by
hand.

> **MySQL DSN.** filex fills in `parseTime=true`, `loc=UTC` and
> `time_zone='+00:00'` when your DSN does not set them. The third one is not
> cosmetic: without it the server's `NOW()` follows its local zone while filex
> writes UTC, and a queued job becomes runnable hours early or invisible hours
> late. Anything you set yourself is left alone.

> **Why MySQL 8.0.17.** Migration `00041` compares file names byte for byte
> with the `utf8mb4_0900_bin` collation, which MySQL added in 8.0.17. MariaDB
> 10.x does not know the `utf8mb4_0900_*` collations and stops at migration
> `00001`; MariaDB 11.4 accepts them.

> **PostgreSQL's locale.** Without the search index, a file-name search asks
> PostgreSQL to compose and lower-case the stored name (`normalize`, PostgreSQL
> 13+, and `lower`) the way filex folds the query - see
> [SEARCH.md](SEARCH.md#capital-letters-and-the-turkish-i). `lower` follows the
> database's locale: create the database with a UTF-8 locale that is not `C`
> (the official `postgres` image's default, `en_US.utf8`, is fine), or only
> `A`-`Z` change case and `şubat` misses `ŞUBAT.pdf`.

## Supported versions

| Engine | filex | Checked on every change (CI) | Also measured |
|---|---|---|---|
| SQLite | every release | bundled driver | - |
| PostgreSQL | v0.38.0+ | PostgreSQL 17 | - |
| MySQL | **v0.41.0+** | MySQL 8.4 (`mysql:8.4`) | 8.0.46 by hand, 2026-09-14 |
| MariaDB | **v0.41.0+** | not in CI | 11.4.13 by hand, 2026-09-14 |

v0.38.0 was the first release that installed and ran on MySQL. From v0.38.0
through v0.40.0 a MySQL install never moved quota usage, never pruned version
history, answered 500 on the sync history, and could not catalogue two names
that differ only by case or accent. v0.41.0 fixes all four, and the gates below
now cover each of them.

## The queue follows the database

The persistent job queue - content extraction, antivirus scans, replica
retries, thumbnails - lives in your database unless you point it elsewhere.

| `FILEX_QUEUE_DRIVER` | What it does |
|---|---|
| *(unset)* | follows `FILEX_DB_DRIVER`: `sqlite` → sqlite, `postgres` → postgres, `mysql` → mysql. Shares the application connection; nothing else to run |
| `redis` | a separate Redis/Valkey. Set `FILEX_QUEUE_DSN=redis://…`. For several filex processes, or to keep queue load off the database |
| `postgres` | an explicit Postgres, with its own `FILEX_QUEUE_DSN` when it is not the application database |

⚠ Until v0.38.0 an unset driver meant *sqlite*, whatever the database was. On a
PostgreSQL install that sent SQLite-flavoured SQL down the Postgres connection:
every worker logged a syntax error on every poll and no background job ever
ran, on a server that otherwise looked healthy.

## What "supported" is checked to mean

`backend/internal/db` holds the gates, and CI runs them against service
containers (`test:go:engines`). They are worth knowing about because they
describe exactly what is guaranteed:

- **The migrations apply** from an empty database, and applying them a second
  time - what every restart does - changes nothing.
- **An install upgraded in place has the schema of a fresh one** (SQLite).
  The schema six releases left on their first start - v0.1.57, the first
  public one, v0.10.0, v0.26.1, v0.37.0, v0.38.0 and v0.61.0, kept in
  `backend/internal/db/testdata/upgrade` and written from each release's own
  image - is taken to this release the way a start does it, and compared with
  a fresh install table by table, column by column (type, `NOT NULL`, default,
  key, collation) and index by index. A difference is a query that works on a
  new install and fails on an upgraded one
  ([below](#upgrading-an-old-sqlite-install)).
- **The schemas match.** Each dialect's tables and columns are compared against
  SQLite's, name by name. Types are not compared (`TEXT`, `VARCHAR(190)` and
  `JSONB` are legitimate per-engine answers); names are, because a name that
  differs is a query that fails. The comparison also catches a column that is
  `NOT NULL` with no default where SQLite supplies one - an `INSERT` that works
  on SQLite and fails there.
- **The writes work.** Registering a storage, creating the admin, recording a
  file, saving a setting, configuring an external service, tagging, starring,
  thumbnailing and sharing - on every engine.
- **The queue contract holds** on every queue driver, including coalescing,
  priority order and scheduled delivery.
- **Every SQL statement in the shared store prepares on a real MySQL server**,
  exactly as MySQL receives it after the upsert rewrite. SQLite accepts
  spellings MySQL rejects, so a statement that only ever ran on SQLite is the
  likeliest MySQL break.
- **The paths that broke on MySQL stay fixed** on every engine: quota
  accounting, version pruning, the sync history window, and file names that
  differ only by case, accent or a trailing space.
- **What is below a folder is one answer on every engine**, and an index
  answers it (migration `00083`): the rows whose path starts with the folder's,
  byte for byte, in both stored spellings - never a folder of another case or
  accent, a sibling that only starts with its name, or a `%` or `_` read as a
  wildcard. PostgreSQL compares these paths in the `"C"` collation whatever the
  database's locale, and the check runs in an ICU database as well; the plan
  of each statement is read on every engine.
- **A file-name search without the index gives one answer on every engine**:
  every word required, ranked before the limit, and the stored name compared
  through the same normaliser as the query - decomposed names, Turkish
  capitals, the four i's as one letter.

Run them yourself against throwaway servers:

```bash
docker run -d --name pg -e POSTGRES_USER=filex -e POSTGRES_PASSWORD=filex \
  -e POSTGRES_DB=filex -p 5432:5432 postgres:17
docker run -d --name my -e MYSQL_ROOT_PASSWORD=filex -p 3306:3306 mysql:8.4

cd backend
FILEX_TEST_PG_DSN='postgres://filex:filex@127.0.0.1:5432/filex?sslmode=disable' \
FILEX_TEST_MYSQL_DSN='root:filex@tcp(127.0.0.1:3306)/mysql' \
  go test ./internal/db/ ./internal/queue/
```

Without the two variables the suites skip themselves and only SQLite runs, so
nobody is forced to keep two database servers to work on filex.

## Upgrading an old SQLite install

An install can go from any release to the current one in place: at start the
migrations it has not run yet run, and after them the **schema repairs** - the
columns the migrations take for granted that an earlier release left out.

There is one so far. Before v0.38.0 filex made its operations queue
(`pending_ops`) at start, outside the migrations, and from v0.27.0 on it added
the column `dest_storage_id` the same way. Migration `00036` (v0.38.0) took the
table over on the belief that every install had that column already. A SQLite
install whose last start was v0.26.1 or older, upgraded straight to v0.38.0 or
later, kept the table without it: the operations list
(`GET /api/files/ops`) answered 500 with `no such column: dest_storage_id`
while `/healthz` said the server was fine. PostgreSQL and MySQL installs are
not affected: before v0.38.0 neither could finish a first start, and the table
they have is the migration's.

Since v0.61.1 such an install is repaired at start, and the log says so once,
on that start:

```text
INFO db: schema repaired: added the column(s) an earlier release left out of this database columns=pending_ops.dest_storage_id engine=sqlite3
```

A repair adds only a column a fresh install has, made the way the migrations
make it, so the rows are kept and nothing else changes; on every other install
it reads the table's columns and does nothing. If you added the column by hand
(`ALTER TABLE pending_ops ADD COLUMN dest_storage_id INTEGER NOT NULL DEFAULT 0`),
the repair finds it there and leaves it. A repair is not a migration: the
release that carries one is still a patch, and the newest migration an install
is at does not move.

## Moving between engines

There is no built-in migration between engines. The supported path is a fresh
install pointed at the new database plus a re-sync of your storages: the file
tree is a cache of what is on the backends, and the sync worker rebuilds it.

⚠ Only the *tree* comes back. Everything filex knows **about** a file lives in
the database and not on the backend, so none of it survives: users, shares,
grants and settings, but also tags, stars, comments, version history (its bytes
stay on the backend under `.versions/`, keyed by node ids that no longer exist), recents,
each person's remembered folder views - and who owns each file. A re-synced
file is found by the scanner, and a file the scanner found is recorded as
belonging to **System**, so the Owner column and the Owner filter start empty.
Plan it as a migration, not a switch of a variable.

## Backups

Three things hold state filex cannot rebuild: the **database**, the **storage
backends**, and **`FILEX_SECRET_KEY`** - a restored database without the key
that sealed its S3 access keys is a database whose access keys no longer
verify. The search index and the thumbnail cache under the data directory are
rebuildable, and `<data-dir>/cache` should be actively excluded.

For SQLite the database is one file - stop filex or use
`sqlite3 instance.sqlite ".backup out.sqlite"`, never a plain copy of a live
WAL database. For PostgreSQL and MySQL use their own tooling. See
[DEPLOYMENT.md](DEPLOYMENT.md#backup--restore) for the whole picture.
