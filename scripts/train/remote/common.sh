# Functions the ship's server steps share (scripts/train/ship.mjs).
#
# Never run on its own and never installed: the ship sends this file and ONE
# step file over ssh on stdin, the server saves the pair to a temporary file
# and runs it with the step's arguments:
#
#   cat common.sh deploy.sh | ssh <host> 'f=$(mktemp) && cat > "$f" && bash "$f" <args>; ...'
#
# Saved first, then run, so nothing a step starts (docker, sqlite3) can read
# the rest of the script off stdin. And sent, never written inline into an ssh
# command: a quote or an apostrophe in an inline script ends the string early,
# and the rest runs in the wrong shell (2026-05-10).
#
# An instance is `name:dir:port:db:service` - the compose directory, the host
# port its /healthz answers on, its SQLite file under the directory (or
# `none`), its compose service.

log() { printf '[%s] %s\n' "$(date -u +%H:%M:%S)" "$*"; }
die() {
  printf 'FAILED: %s\n' "$*" >&2
  exit 1
}

# parse_instance SPEC -> I_NAME I_DIR I_PORT I_DB I_SVC I_COMPOSE
parse_instance() {
  local spec="$1" f
  IFS=: read -r I_NAME I_DIR I_PORT I_DB I_SVC <<<"$spec"
  if [ -z "$I_NAME" ] || [ -z "$I_DIR" ] || [ -z "$I_PORT" ] || [ -z "$I_DB" ] || [ -z "$I_SVC" ]; then
    die "instance '$spec' is not name:dir:port:db:service"
  fi
  case "$I_DIR" in
    /*/*) ;;
    *) die "$I_NAME: the compose directory '$I_DIR' must be an absolute path below /" ;;
  esac
  case "$I_PORT" in
    *[!0-9]*) die "$I_NAME: port '$I_PORT' is not a number" ;;
  esac
  I_COMPOSE=""
  for f in docker-compose.yml docker-compose.yaml compose.yml compose.yaml; do
    if [ -f "$I_DIR/$f" ]; then
      I_COMPOSE="$I_DIR/$f"
      break
    fi
  done
  [ -n "$I_COMPOSE" ] || die "$I_NAME: no compose file in $I_DIR"
}

# re_escape TEXT -> TEXT with what an extended regular expression gives a
# meaning to escaped
re_escape() { printf '%s' "$1" | sed -e 's/[][\.*^$+?(){}|]/\\&/g'; }

# image_refs COMPOSE IMAGE -> every IMAGE:<tag> an `image:` line of COMPOSE names
image_refs() {
  grep -E "^[[:space:]]*image:[[:space:]]*[\"']?$(re_escape "$2"):" "$1" |
    sed -E "s/^[[:space:]]*image:[[:space:]]*[\"']?//; s/[\"'[:space:]]*\$//" || true
}

# variant_of TAG -> the image variant a tag carries: slim-, full- or nothing
variant_of() {
  case "$1" in
    slim-*) printf 'slim-' ;;
    full-*) printf 'full-' ;;
    *) printf '' ;;
  esac
}

# set_image COMPOSE IMAGE REF -> the one IMAGE:<tag> line now names REF.
# Exactly one such line, or nothing is touched: never a guess at which.
set_image() {
  local n
  n=$(image_refs "$1" "$2" | grep -c . || true)
  [ "$n" -eq 1 ] || die "$1 names $2 on $n image lines; the ship changes exactly one"
  sed -i -E "s#^([[:space:]]*image:[[:space:]]*[\"']?)$(re_escape "$2"):[^\"'[:space:]]*#\\1$3#" "$1"
}

# container_of DIR SERVICE -> the running container's id, or nothing
container_of() { (cd "$1" && docker compose ps -q "$2" </dev/null 2>/dev/null | head -n 1); }

# env_of CONTAINER VAR -> VAR as the container was started with it
env_of() { docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$1" </dev/null | sed -n "s/^$2=//p" | head -n 1; }

# compose_env DIR SERVICE VAR -> VAR as the compose file gives it to SERVICE
# (after interpolation: what the next container will be started with)
compose_env() {
  (cd "$1" && docker compose config --format json </dev/null) |
    python3 -c 'import json, sys; d = json.load(sys.stdin); e = (d.get("services", {}).get(sys.argv[1]) or {}).get("environment") or {}; v = e.get(sys.argv[2]) if isinstance(e, dict) else None; print("" if v is None else v)' "$2" "$3"
}

# gateways_of CONTAINER -> the gateway of every network the container is on
gateways_of() { docker inspect -f '{{range .NetworkSettings.Networks}}{{.Gateway}} {{end}}' "$1" </dev/null; }

# trusted_covers VALUE IP... -> exit 0 when every IP is in FILEX_TRUSTED_PROXIES'
# VALUE (an address, or inside a range); otherwise prints the ones that are not.
# ⚠ Why: the reverse proxy reaches filex through the published port, so every
# visitor arrives from the network's gateway; a gateway filex does not trust
# makes every visitor one address, and the sign-in limit locks them all out.
trusted_covers() {
  local value="$1"
  shift
  python3 -c 'import ipaddress, re, sys
nets = []
for tok in re.split(r"[\s,]+", sys.argv[1]):
    if tok and tok != "auto":
        try:
            nets.append(ipaddress.ip_network(tok, strict=False))
        except ValueError:
            pass
missing = [ip for ip in sys.argv[2:] if ip and not any(ipaddress.ip_address(ip) in n for n in nets)]
print(" ".join(missing))
sys.exit(1 if missing else 0)' "$value" "$@"
}

# wait_health PORT SECONDS -> exit 0 once /healthz answers 200
wait_health() {
  local port="$1" until
  until=$(($(date +%s) + $2))
  while :; do
    if curl -fsS -m 5 "http://127.0.0.1:$port/healthz" </dev/null >/dev/null 2>&1; then
      return 0
    fi
    [ "$(date +%s)" -lt "$until" ] || return 1
    sleep 3
  done
}

# goose_of SQLITE -> the newest migration applied to it, or nothing
goose_of() {
  [ -f "$1" ] || return 0
  sqlite3 -readonly "$1" 'SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = 1;' </dev/null 2>/dev/null || true
}
