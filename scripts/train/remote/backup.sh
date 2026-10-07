# The ship's backup step, run on the server after common.sh (see there).
#
#   backup.sh BACKUP_DIR INSTANCE...
#
# For every instance, into BACKUP_DIR/<name>/: its compose file, its .env (as
# env.bak, mode 600), the image it runs (image.txt), and - unless its database
# is `none` - a copy of its SQLite file made with SQLite's own online backup
# (never `cp` of a live file), checked with `PRAGMA integrity_check`, and the
# newest migration that copy holds (goose.txt). The deploy step rolls back from
# exactly these files.
#
# ⚠ A backup directory is never reused: one that exists already is refused,
# so a second attempt cannot overwrite the copy a first attempt rolled back to.

set -euo pipefail

bk="${1:?the backup directory}"
shift
[ "$#" -gt 0 ] || die "no instance given"
case "$bk" in
  /*/*) ;;
  *) die "the backup directory '$bk' must be an absolute path below /" ;;
esac
[ ! -e "$bk" ] || die "$bk exists already; a backup is never written over"
command -v sqlite3 >/dev/null || die "sqlite3 is not installed on this server"

need=0
for spec in "$@"; do
  parse_instance "$spec"
  if [ "$I_DB" != none ]; then
    [ -f "$I_DIR/$I_DB" ] || die "$I_NAME: no database at $I_DIR/$I_DB"
    need=$((need + $(stat -c %s "$I_DIR/$I_DB")))
  fi
done
install -d -m 700 "$bk"
avail=$(($(df -Pk "$bk" | awk 'NR == 2 { print $4 }') * 1024))
[ "$avail" -gt $((need * 2 + 104857600)) ] || die "$bk: $avail bytes free, the copies need $need (twice that, plus 100 MB, is the floor)"

for spec in "$@"; do
  parse_instance "$spec"
  out="$bk/$I_NAME"
  install -d -m 700 "$out"
  cp -p "$I_COMPOSE" "$out/"
  if [ -f "$I_DIR/.env" ]; then install -m 600 "$I_DIR/.env" "$out/env.bak"; fi
  cid=$(container_of "$I_DIR" "$I_SVC")
  if [ -n "$cid" ]; then
    docker inspect -f '{{.Config.Image}} {{.Image}}' "$cid" </dev/null > "$out/image.txt"
  else
    echo "not running" > "$out/image.txt"
  fi
  if [ "$I_DB" = none ]; then
    log "$I_NAME: compose file and settings kept (no SQLite database to copy)"
    continue
  fi
  sqlite3 "$I_DIR/$I_DB" ".backup '$out/instance.sqlite'" </dev/null
  check=$(sqlite3 -readonly "$out/instance.sqlite" 'PRAGMA integrity_check;' </dev/null)
  [ "$check" = ok ] || die "$I_NAME: the copy fails its integrity check: $check"
  goose_of "$out/instance.sqlite" > "$out/goose.txt"
  log "$I_NAME: $(stat -c %s "$out/instance.sqlite") bytes, integrity ok, migration $(cat "$out/goose.txt"), running $(cut -d' ' -f1 "$out/image.txt")"
done
log "backup in $bk"
