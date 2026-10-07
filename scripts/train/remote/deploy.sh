# The ship's deploy step, run on the server after common.sh (see there).
#
#   deploy.sh IMAGE TAG WANT_MIGRATION WAIT_SECONDS ON_FAIL BACKUP_DIR INSTANCE...
#
# One instance at a time, in the order given (put the demo first: it is the
# canary), each:
#   1. the one `image:` line naming IMAGE moves to IMAGE:<variant>TAG (the
#      variant - slim-, full- or none - is the one it ran);
#   2. `docker compose up -d <service>`;
#   3. /healthz answers 200 within WAIT_SECONDS;
#   4. the container runs exactly that image;
#   5. its database is at WANT_MIGRATION, the newest migration the release
#      carries (0: not checked);
#   6. the new container trusts its network's gateway (proxies.sh says why).
# A step that fails rolls that instance back (ON_FAIL=rollback): the compose
# file from the backup, and - when migrations ran - the database copy from the
# backup, the failed one kept beside it; then the old image is started and
# checked. The instances after it are not touched. ON_FAIL=hold leaves the
# instance as it failed, for a person.
#
# ⚠ Why rollback is the default: the maintainer's rule for every update
# (2026-09-28) - backup, update, test the consumers, and roll back on its own
# when a test fails, then report. A deploy that stops half-way and waits for
# someone to notice is the outage.

set -euo pipefail

image="${1:?image}"
tag="${2:?tag}"
want="${3:?newest migration (0: do not check)}"
wait_s="${4:?seconds to wait for /healthz}"
on_fail="${5:?rollback or hold}"
bk="${6:?backup directory}"
shift 6
[ "$#" -gt 0 ] || die "no instance given"
case "$on_fail" in rollback | hold) ;; *) die "on-fail is rollback or hold, not '$on_fail'" ;; esac
[ -d "$bk" ] || die "no backup at $bk - the backup step runs before this one"
command -v python3 >/dev/null || die "python3 is needed to compare addresses"

rollback_instance() {
  local before after stamp f
  before=$(cat "$bk/$I_NAME/goose.txt" 2>/dev/null || true)
  cp -p "$bk/$I_NAME/$(basename "$I_COMPOSE")" "$I_COMPOSE"
  log "$I_NAME: compose file back as it was"
  if [ "$I_DB" != none ]; then
    after=$(goose_of "$I_DIR/$I_DB")
    if [ -n "$after" ] && [ "$after" != "$before" ]; then
      if (cd "$I_DIR" && docker compose stop "$I_SVC" </dev/null); then
        stamp=$(date -u +%Y%m%d-%H%M%S)
        for f in "$I_DIR/$I_DB" "$I_DIR/$I_DB-wal" "$I_DIR/$I_DB-shm"; do
          if [ -f "$f" ]; then cp -p "$f" "$f.failed-$stamp"; fi
        done
        rm -f "$I_DIR/$I_DB-wal" "$I_DIR/$I_DB-shm"
        cat "$bk/$I_NAME/instance.sqlite" > "$I_DIR/$I_DB"
        log "$I_NAME: database back at migration $before (it had reached $after; that copy is $I_DB.failed-$stamp)"
      else
        log "$I_NAME: could not stop the container, so the database was NOT put back (it is at migration $after, the backup at $before)"
      fi
    fi
  fi
  (cd "$I_DIR" && docker compose up -d "$I_SVC" </dev/null) || true
  if wait_health "$I_PORT" "$wait_s"; then
    log "$I_NAME: rolled back and healthy, on $(cut -d' ' -f1 "$bk/$I_NAME/image.txt")"
  else
    log "$I_NAME: rolled back, and /healthz does NOT answer - look at it now"
  fi
}

fail_instance() {
  local cid
  log "$I_NAME: $1"
  cid=$(container_of "$I_DIR" "$I_SVC")
  if [ -n "$cid" ]; then
    log "$I_NAME: the last lines it logged:"
    docker logs --tail 30 "$cid" </dev/null 2>&1 | sed 's/^/    /' || true
  fi
  if [ "$on_fail" = rollback ]; then
    rollback_instance
    die "$I_NAME: $1 - rolled back; the instances after it were not touched"
  fi
  die "$I_NAME: $1 - left as it is (on-fail hold); the backup is $bk/$I_NAME"
}

for spec in "$@"; do
  parse_instance "$spec"
  [ -f "$bk/$I_NAME/$(basename "$I_COMPOSE")" ] || die "$I_NAME: $bk holds no copy of its compose file"
  cur=$(image_refs "$I_COMPOSE" "$image")
  [ "$(printf '%s\n' "$cur" | grep -c .)" -eq 1 ] || die "$I_NAME: $I_COMPOSE names $image on $(printf '%s\n' "$cur" | grep -c .) image lines; the ship changes exactly one"
  ref="$image:$(variant_of "${cur##*:}")$tag"
  if [ "$cur" = "$ref" ]; then
    log "$I_NAME: $I_COMPOSE names $ref already; started again and checked"
  else
    log "$I_NAME: $cur -> $ref"
  fi
  docker pull -q "$ref" </dev/null >/dev/null || die "$I_NAME: cannot pull $ref"
  set_image "$I_COMPOSE" "$image" "$ref"
  t0=$(date +%s)
  if ! (cd "$I_DIR" && docker compose up -d "$I_SVC" </dev/null); then fail_instance "docker compose up failed"; fi
  if ! wait_health "$I_PORT" "$wait_s"; then fail_instance "no /healthz 200 within ${wait_s}s"; fi
  cid=$(container_of "$I_DIR" "$I_SVC")
  [ -n "$cid" ] || fail_instance "no container for service $I_SVC after the start"
  running=$(docker inspect -f '{{.Config.Image}}' "$cid" </dev/null)
  [ "$running" = "$ref" ] || fail_instance "it runs $running, not $ref"
  got="-"
  if [ "$I_DB" != none ]; then
    got=$(goose_of "$I_DIR/$I_DB")
    if [ "$want" -gt 0 ] && [ "$got" != "$want" ]; then fail_instance "its database is at migration ${got:-?}, the release carries $want"; fi
  fi
  gws=$(gateways_of "$cid")
  value=$(env_of "$cid" FILEX_TRUSTED_PROXIES)
  # shellcheck disable=SC2086
  if ! missing=$(trusted_covers "$value" $gws); then fail_instance "gateway $missing is not in FILEX_TRUSTED_PROXIES \"$value\""; fi
  log "$I_NAME: $ref healthy in $(($(date +%s) - t0))s, migration $got, gateway ${gws% } trusted"
done
log "deployed $tag"
