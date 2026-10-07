#!/usr/bin/env bash
# e2e/realenv/run.sh - filex against REAL third-party servers in Docker.
#
#   e2e/realenv/run.sh                 every stage: tls, sso, office
#   e2e/realenv/run.sh tls sso         only those
#
# Stages (each starts its own containers on its own networks, runs its specs
# in a Playwright container on those networks, and removes everything again):
#
#   tls     Pebble (Let's Encrypt's ACME test server) + pebble-challtestsrv
#           (its DNS): filex issuing its own certificates (FILEX_TLS_MODE=acme:
#           TLS-ALPN-01, HTTP-01, a tenant's CNAME'd own domain, renewal), and a
#           real Caddy in front (FILEX_TLS_MODE=proxy: on-demand `ask`,
#           `get_certificate http`, the proxy-only guard).
#   sso     Keycloak (dev mode, realms imported) and OpenLDAP (ldaps with the
#           run's own CA): SSO buttons in a browser, realm-less and realm
#           sign-ins, a tenant's own OIDC and LDAP through the guarded client,
#           the first sign-in rule, group links.
#   office  ONLYOFFICE Document Server: issue #80's S2 (JWT off on the
#           document server) and S5 (a secret that does not match).
#
# A stage whose images are not on this machine is SKIPPED and says so (set
# REALENV_PULL=1 to pull them). Linux and Docker only. The specs skip
# themselves, with the reason, when they are run outside this script.
#
# Environment (all optional):
#   REALENV_BIN          filex linux binary            (default: bin/filex)
#   REALENV_WORK         certificates, logs, results  (default: e2e/realenv/.work)
#   REALENV_PREFIX       container/network name prefix (default: fxre)
#   REALENV_PRIV_NET     first three octets of the private /24 (default 172.30.50)
#   REALENV_PUB_NET      first three octets of the "public" /24 the guarded
#                        client may reach (default 203.0.113, TEST-NET-3)
#   REALENV_LOCK         a lock file to hold while a stage runs (a shared host)
#   REALENV_KEEP=1       leave the last stage's containers running
#   REALENV_PULL=1       pull missing images instead of skipping
#   REALENV_*_IMAGE      PEBBLE, CHALLTESTSRV, CADDY, KEYCLOAK, LDAP, OO,
#                        BASE (runs filex), RUNNER (Playwright)
#   REALENV_OO_S5_URL    office S5: a running document server with JWT on (any
#                        secret but filex's) instead of starting a second one;
#                        REALENV_OO_S5_NETWORK is the network it is reached on
set -uo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/../.." && pwd)
WORK=${REALENV_WORK:-$HERE/.work}
BIN=${REALENV_BIN:-$REPO/bin/filex}
P=${REALENV_PREFIX:-fxre}
PRIV=${REALENV_PRIV_NET:-172.30.50}
PUB=${REALENV_PUB_NET:-203.0.113}

PEBBLE_IMAGE=${REALENV_PEBBLE_IMAGE:-ghcr.io/letsencrypt/pebble:latest}
CHALLTESTSRV_IMAGE=${REALENV_CHALLTESTSRV_IMAGE:-ghcr.io/letsencrypt/pebble-challtestsrv:latest}
CADDY_IMAGE=${REALENV_CADDY_IMAGE:-caddy:2-alpine}
KEYCLOAK_IMAGE=${REALENV_KEYCLOAK_IMAGE:-quay.io/keycloak/keycloak:26.8.0}
LDAP_IMAGE=${REALENV_LDAP_IMAGE:-osixia/openldap:1.5.0}
OO_IMAGE=${REALENV_OO_IMAGE:-onlyoffice/documentserver:latest}
BASE_IMAGE=${REALENV_BASE_IMAGE:-alpine:3.24}

PW_PKG=$REPO/e2e/node_modules/@playwright/test/package.json
PW_VERSION=$(sed -n 's/^ *"version": *"\([^"]*\)".*/\1/p' "$PW_PKG" 2>/dev/null | head -1)
RUNNER_IMAGE=${REALENV_RUNNER_IMAGE:-mcr.microsoft.com/playwright:v${PW_VERSION:-0}-noble}

SECRET_KEY=realenv-secret-key-not-for-production
ADMIN_EMAIL=admin@local
ADMIN_PASSWORD=admin

STAGES=("$@")
[ ${#STAGES[@]} -eq 0 ] && STAGES=(tls sso office)

SKIPPED=()
FAILED=()
PASSED=()
LOGPIDS=()

log() { printf '%s realenv: %s\n' "$(date +%H:%M:%S)" "$*"; }
skip() { log "SKIPPED $1: $2"; SKIPPED+=("$1: $2"); }

have_image() {
  docker image inspect "$1" >/dev/null 2>&1 && return 0
  if [ "${REALENV_PULL:-0}" = 1 ]; then
    log "pulling $1"
    docker pull -q "$1" >/dev/null 2>&1 && return 0
  fi
  return 1
}

# The images a stage needs; skips the stage (and says which image) when one
# is missing.
need_images() {
  local stage=$1 img
  shift
  for img in "$@"; do
    if ! have_image "$img"; then
      skip "$stage" "image $img is not on this machine (REALENV_PULL=1 pulls it)"
      return 1
    fi
  done
  return 0
}

c() { echo "$P-$1"; }

net_up() {
  docker network inspect "$P-priv" >/dev/null 2>&1 ||
    docker network create --subnet "$PRIV.0/24" "$P-priv" >/dev/null || return 1
  docker network inspect "$P-pub" >/dev/null 2>&1 ||
    docker network create --subnet "$PUB.0/24" "$P-pub" >/dev/null || return 1
}

follow_log() {
  docker logs -f "$1" > "$WORK/logs/$1.log" 2>&1 &
  LOGPIDS+=($!)
}

down_all() {
  local ids
  for pid in "${LOGPIDS[@]:-}"; do [ -n "$pid" ] && kill "$pid" 2>/dev/null; done
  LOGPIDS=()
  ids=$(docker ps -aq --filter "name=^$P-")
  [ -n "$ids" ] && docker rm -f -v $ids >/dev/null 2>&1
  docker network rm "$P-priv" "$P-pub" >/dev/null 2>&1
  return 0
}

# filex in a container: the binary under test, its log in the work dir.
#   start_filex <name> <ip> [docker args...] -- [env KEY=VALUE...]
start_filex() {
  local name=$1 ip=$2
  shift 2
  local dargs=() envs=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do dargs+=("$1"); shift; done
  [ "${1:-}" = "--" ] && shift
  for kv in "$@"; do envs+=(-e "$kv"); done
  docker run -d --name "$(c "$name")" --network "$P-priv" --ip "$ip" "${dargs[@]}" \
    -v "$BIN:/usr/local/bin/filex:ro" -v "$WORK:/work" \
    -e FILEX_DATA_DIR=/data -e FILEX_LISTEN=:5212 \
    -e FILEX_ADMIN_EMAIL=$ADMIN_EMAIL -e FILEX_ADMIN_PASSWORD=$ADMIN_PASSWORD \
    -e FILEX_SECRET_KEY=$SECRET_KEY -e FILEX_LOG_LEVEL=debug -e FILEX_UPDATE_CHECK=0 \
    "${envs[@]}" "$BASE_IMAGE" \
    sh -c "mkdir -p /data /srv/files && exec /usr/local/bin/filex serve >> /work/logs/$(c "$name").log 2>&1" >/dev/null
}

runner_up() {
  docker run -d --name "$(c runner)" --network "$P-priv" --ip "$PRIV.5" \
    -v "$REPO:/repo" -v "$WORK:/work" -w /repo/e2e -e HOME=/root \
    "$RUNNER_IMAGE" sleep infinity >/dev/null || return 1
  docker network connect --ip "$PUB.5" "$P-pub" "$(c runner)" || return 1
}

rexec() { docker exec "$(c runner)" "$@"; }

# The networks, the Playwright container every stage drives its specs from,
# and the run's own certificates.
common_up() {
  net_up || { FAILED+=("$1: networks"); return 1; }
  runner_up || { FAILED+=("$1: runner"); return 1; }
  if [ -z "${CERTS_MADE:-}" ]; then
    rm -rf "$WORK/certs"
    rexec bash /repo/e2e/realenv/stack/certs.sh /work/certs >/dev/null || { FAILED+=("$1: certificates"); return 1; }
    CERTS_MADE=1
  fi
}

# wait_url <seconds> <curl args...>: until the runner's curl succeeds.
wait_url() {
  local secs=$1 i
  shift
  for ((i = 0; i < secs; i += 2)); do
    rexec curl -sf -o /dev/null --max-time 2 "$@" && return 0
    sleep 2
  done
  log "not ready after ${secs}s: $*"
  return 1
}

# run_specs <stage> <label> [env KEY=VALUE...] -- <spec files...>
run_specs() {
  local stage=$1 label=$2
  shift 2
  local envs=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do envs+=(-e "$1"); shift; done
  [ "${1:-}" = "--" ] && shift
  log "$stage: running $label"
  docker exec "${envs[@]}" -e REALENV=1 -e REALENV_WORK=/work -e "REALENV_RESULTS=/work/results/$label" \
    -e REALENV_PREFIX="$P" -e REALENV_PRIV_NET="$PRIV" -e REALENV_PUB_NET="$PUB" \
    -e E2E_ADMIN_EMAIL=$ADMIN_EMAIL -e E2E_ADMIN_PASSWORD=$ADMIN_PASSWORD \
    "$(c runner)" npx playwright test -c realenv/playwright.config.ts "$@" \
    > "$WORK/logs/specs-$label.log" 2>&1
  local rc=$?
  tail -n 40 "$WORK/logs/specs-$label.log"
  if [ $rc -eq 0 ]; then PASSED+=("$label"); else FAILED+=("$label (exit $rc, $WORK/logs/specs-$label.log)"); fi
}

stage_tls() {
  need_images tls "$PEBBLE_IMAGE" "$CHALLTESTSRV_IMAGE" "$CADDY_IMAGE" "$BASE_IMAGE" "$RUNNER_IMAGE" || return 0
  common_up tls || return 0

  local cid
  cid=$(docker create "$PEBBLE_IMAGE")
  docker cp "$cid:/test/certs/pebble.minica.pem" "$WORK/pebble.minica.pem" >/dev/null
  docker rm "$cid" >/dev/null

  docker run -d --name "$(c dns)" --network "$P-priv" --ip "$PRIV.10" "$CHALLTESTSRV_IMAGE" \
    -dnsserver :53 -management :8055 -http01 "" -https01 "" -tlsalpn01 "" -doh "" \
    -defaultIPv4 "" -defaultIPv6 "" >/dev/null
  local peb=(-e PEBBLE_VA_NOSLEEP=1 -e PEBBLE_WFE_NONCEREJECT=0 -e PEBBLE_AUTHZREUSE=0)
  docker run -d --name "$(c pebble)" --network "$P-priv" --ip "$PRIV.11" --network-alias pebble \
    "${peb[@]}" "$PEBBLE_IMAGE" -dnsserver "$PRIV.10:53" >/dev/null
  docker run -d --name "$(c pebble-short)" --network "$P-priv" --ip "$PRIV.12" \
    -v "$HERE/stack/pebble-short.json:/realenv/pebble-short.json:ro" \
    "${peb[@]}" "$PEBBLE_IMAGE" -config /realenv/pebble-short.json -dnsserver "$PRIV.10:53" >/dev/null
  for n in dns pebble pebble-short; do follow_log "$(c $n)"; done

  local acme=(FILEX_MULTI_TENANT=1 FILEX_TLS_MODE=acme FILEX_TLS_ACME_DIRECTORY=https://pebble:14000/dir
    FILEX_TLS_ACME_EMAIL=ops@filex.test SSL_CERT_FILE=/work/pebble.minica.pem)
  # TLS-ALPN-01 only: no HTTP listener, so nothing else could have proved it.
  start_filex acme "$PRIV.20" --dns "$PRIV.10" -- "${acme[@]}" \
    FILEX_PUBLIC_URL=https://files.filex.test:5001 FILEX_TENANT_DOMAIN=tenants.filex.test \
    FILEX_TLS_LISTEN=:5001 FILEX_TLS_HTTP_LISTEN=off
  # HTTP-01 only: TLS on a port Pebble never dials.
  start_filex h1 "$PRIV.21" --dns "$PRIV.10" -- "${acme[@]}" \
    FILEX_PUBLIC_URL=https://h1.filex.test:8443 FILEX_TLS_LISTEN=:8443 FILEX_TLS_HTTP_LISTEN=:5002
  # Renewal: the three-minute Pebble answers as "pebble" here.
  start_filex renew "$PRIV.22" --dns "$PRIV.10" --add-host "pebble:$PRIV.12" -- "${acme[@]}" \
    FILEX_PUBLIC_URL=https://renew.filex.test:5001 FILEX_TLS_LISTEN=:5001 FILEX_TLS_HTTP_LISTEN=off
  # No trust in Pebble's root: what an operator sees when the CA is not trusted.
  start_filex notrust "$PRIV.23" --dns "$PRIV.10" -- FILEX_TLS_MODE=acme \
    FILEX_TLS_ACME_DIRECTORY=https://pebble:14000/dir FILEX_PUBLIC_URL=https://notrust.filex.test:5001 \
    FILEX_TLS_LISTEN=:5001 FILEX_TLS_HTTP_LISTEN=off
  # Behind Caddy: only Caddy is the proxy.
  start_filex fxproxy "$PRIV.31" --dns "$PRIV.10" -- FILEX_MULTI_TENANT=1 \
    FILEX_PUBLIC_URL=https://files.proxy.test:5001 FILEX_TENANT_DOMAIN=tenants.proxy.test \
    FILEX_TRUSTED_PROXIES=$PRIV.30
  docker run -d --name "$(c caddy)" --network "$P-priv" --ip "$PRIV.30" \
    -v "$HERE/stack/Caddyfile:/etc/caddy/Caddyfile:ro" -v "$WORK/pebble.minica.pem:/realenv/pebble.minica.pem:ro" \
    -e REALENV_ACME_DIR=https://pebble:14000/dir -e "REALENV_FILEX=$(c fxproxy):5212" "$CADDY_IMAGE" >/dev/null
  follow_log "$(c caddy)"

  for n in acme h1 renew notrust fxproxy; do
    wait_url 60 "http://$(c $n):5212/healthz" || { FAILED+=("tls: filex $n did not start"); return 0; }
  done
  wait_url 60 --cacert /work/pebble.minica.pem https://pebble:14000/dir || { FAILED+=("tls: pebble"); return 0; }

  run_specs tls tls \
    REALENV_TLS=1 \
    "REALENV_DNS_MGMT=http://$(c dns):8055" \
    "REALENV_PEBBLE_MGMT=https://$PRIV.11:15000" \
    "REALENV_PEBBLE_SHORT_MGMT=https://$PRIV.12:15000" \
    "REALENV_ACME_API=http://$(c acme):5212" "REALENV_ACME_IP=$PRIV.20" \
    "REALENV_H1_API=http://$(c h1):5212" "REALENV_H1_IP=$PRIV.21" \
    "REALENV_RENEW_API=http://$(c renew):5212" "REALENV_RENEW_IP=$PRIV.22" \
    "REALENV_NOTRUST_IP=$PRIV.23" "REALENV_DEAD_IP=$PRIV.5" \
    "REALENV_PROXY_API=http://$(c fxproxy):5212" "REALENV_CADDY_IP=$PRIV.30" \
    "REALENV_PEBBLE_LOG=/work/logs/$(c pebble).log" "REALENV_PEBBLE_SHORT_LOG=/work/logs/$(c pebble-short).log" \
    "REALENV_CADDY_LOG=/work/logs/$(c caddy).log" \
    -- realenv/tests/acme.spec.ts realenv/tests/proxy.spec.ts
}

stage_sso() {
  need_images sso "$KEYCLOAK_IMAGE" "$LDAP_IMAGE" "$BASE_IMAGE" "$RUNNER_IMAGE" || return 0
  common_up sso || return 0
  mkdir -p "$WORK/ldap-certs"
  cp "$WORK/certs/ca.pem" "$WORK/certs/ldap.crt" "$WORK/certs/ldap.key" "$WORK/certs/dhparam.pem" "$WORK/ldap-certs/"

  docker run -d --name "$(c kc)" --network "$P-pub" --ip "$PUB.10" --network-alias idp.example.test \
    --memory 1500m -e KC_BOOTSTRAP_ADMIN_USERNAME=admin -e KC_BOOTSTRAP_ADMIN_PASSWORD=admin \
    -e KC_HEALTH_ENABLED=true -e KC_HOSTNAME=http://idp.example.test:8080 \
    -v "$HERE/stack/keycloak:/opt/keycloak/data/import:ro" \
    "$KEYCLOAK_IMAGE" start-dev --import-realm >/dev/null
  docker run -d --name "$(c ldap)" --network "$P-pub" --ip "$PUB.11" --network-alias ldap.example.test \
    -e LDAP_ORGANISATION=Example -e LDAP_DOMAIN=example.test -e LDAP_ADMIN_PASSWORD=admin-pass \
    -e LDAP_TLS=true -e LDAP_TLS_CRT_FILENAME=ldap.crt -e LDAP_TLS_KEY_FILENAME=ldap.key \
    -e LDAP_TLS_CA_CRT_FILENAME=ca.pem -e LDAP_TLS_DH_PARAM_FILENAME=dhparam.pem \
    -e LDAP_TLS_VERIFY_CLIENT=never \
    -v "$WORK/ldap-certs:/container/service/slapd/assets/certs" \
    -v "$HERE/stack/ldap:/container/service/slapd/assets/config/bootstrap/ldif/custom:ro" \
    "$LDAP_IMAGE" --copy-service >/dev/null
  docker network connect --ip "$PRIV.41" --alias ldap-private.test "$P-priv" "$(c ldap)"
  for n in kc ldap; do follow_log "$(c $n)"; done

  start_filex sso "$PRIV.50" --network-alias files.sso.test -- FILEX_MULTI_TENANT=1 \
    FILEX_PUBLIC_URL=http://files.sso.test:5212 FILEX_TENANT_DOMAIN=t.sso.test
  docker network connect --ip "$PUB.50" "$P-pub" "$(c sso)"

  wait_url 60 http://files.sso.test:5212/healthz || { FAILED+=("sso: filex did not start"); return 0; }
  wait_url 240 http://idp.example.test:8080/realms/beta/.well-known/openid-configuration ||
    { FAILED+=("sso: keycloak did not start"); return 0; }
  local i
  local ldap_ok=
  for ((i = 0; i < 90; i += 3)); do
    rexec bash -c "echo | openssl s_client -connect ldap.example.test:636 -CAfile /work/certs/ca.pem 2>/dev/null | grep -q 'Verify return code: 0'" && { ldap_ok=1; break; }
    sleep 3
  done
  [ -n "$ldap_ok" ] || log "sso: OpenLDAP's ldaps:// did not answer with the run's certificate within 90s (logs/$(c ldap).log); the LDAP cases will fail"

  run_specs sso sso \
    REALENV_SSO=1 \
    REALENV_SSO_URL=http://files.sso.test:5212 \
    REALENV_IDP=http://idp.example.test:8080 \
    REALENV_LDAP_URL=ldaps://ldap.example.test:636 \
    REALENV_LDAP_PRIVATE_URL=ldaps://ldap-private.test:636 \
    REALENV_LDAP_CA=/work/certs/ca.pem \
    -- realenv/tests/sso.spec.ts
}

# start_ds <scenario> [docker args...]: the document server, its own logs
# (converter, docservice) in the work dir.
start_ds() {
  local scen=$1
  shift
  docker rm -f -v "$(c ds)" >/dev/null 2>&1
  rm -rf "$WORK/ds-$scen"
  mkdir -p "$WORK/ds-$scen"
  docker run -d --name "$(c ds)" --network "$P-priv" --ip "$PRIV.60" --network-alias office.test \
    -v "$WORK/ds-$scen:/var/log/onlyoffice" "$@" "$OO_IMAGE" >/dev/null || return 1
  follow_log "$(c ds)"
  wait_url 300 http://office.test/healthcheck
}

stage_office() {
  need_images office "$BASE_IMAGE" "$RUNNER_IMAGE" || return 0
  if [ -z "${REALENV_OO_S5_URL:-}" ]; then
    need_images office "$OO_IMAGE" || return 0
  fi
  common_up office || return 0
  start_filex oo "$PRIV.61" --network-alias files.office.test -- FILEX_PUBLIC_URL=http://files.office.test:5212
  wait_url 60 http://files.office.test:5212/healthz || { FAILED+=("office: filex did not start"); return 0; }
  local filex_secret=realenv-filex-oo-secret-0123456789 s5url=http://office.test s5logs=/work/ds-s5/documentserver

  # S5 first: a document server that enforces JWT with another secret than
  # filex's. REALENV_OO_S5_URL names one that is already running (its own
  # secret, whatever it is, is not filex's), reached on REALENV_OO_S5_NETWORK;
  # otherwise one is started here.
  local s5=1
  if [ -n "${REALENV_OO_S5_URL:-}" ]; then
    if [ -n "${REALENV_OO_S5_NETWORK:-}" ]; then
      docker network connect "$REALENV_OO_S5_NETWORK" "$(c oo)" &&
        docker network connect "$REALENV_OO_S5_NETWORK" "$(c runner)" ||
        { FAILED+=("office-s5: network $REALENV_OO_S5_NETWORK"); s5=; }
    fi
    s5url=$REALENV_OO_S5_URL
    s5logs=
    [ -n "$s5" ] && { wait_url 60 "$s5url/healthcheck" || { FAILED+=("office-s5: $s5url does not answer"); s5=; }; }
  else
    start_ds s5 -e JWT_ENABLED=true -e JWT_SECRET=realenv-a-different-secret-9876543210 ||
      { FAILED+=("office-s5: document server (JWT on) did not start"); s5=; }
  fi
  if [ -n "$s5" ]; then
    run_specs office office-s5 REALENV_OFFICE=s5 "REALENV_OO_URL=$s5url" \
      "REALENV_OO_SECRET=$filex_secret" REALENV_OO_CALLBACK=http://files.office.test:5212 \
      "REALENV_OO_DS_LOGS=$s5logs" REALENV_OO_FILEX_LOG="/work/logs/$(c oo).log" \
      -- realenv/tests/office.spec.ts
  fi
  if [ -n "${REALENV_OO_S5_NETWORK:-}" ]; then
    docker network disconnect "$REALENV_OO_S5_NETWORK" "$(c oo)" >/dev/null 2>&1
    docker network disconnect "$REALENV_OO_S5_NETWORK" "$(c runner)" >/dev/null 2>&1
  fi
  docker rm -f -v "$(c ds)" >/dev/null 2>&1

  # S2: a document server with JWT off, removed as soon as S2 is measured.
  need_images office "$OO_IMAGE" || return 0
  start_ds s2 -e JWT_ENABLED=false || { FAILED+=("office: document server (JWT off) did not start"); return 0; }
  run_specs office office-s2 REALENV_OFFICE=s2 REALENV_OO_URL=http://office.test \
    "REALENV_OO_SECRET=$filex_secret" REALENV_OO_CALLBACK=http://files.office.test:5212 \
    REALENV_OO_DS_LOGS=/work/ds-s2/documentserver REALENV_OO_FILEX_LOG="/work/logs/$(c oo).log" \
    -- realenv/tests/office.spec.ts
  docker rm -f -v "$(c ds)" >/dev/null 2>&1
}

if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
  for s in "${STAGES[@]}"; do skip "$s" "no Docker here"; done
  exit 0
fi
if [ ! -x "$BIN" ]; then
  for s in "${STAGES[@]}"; do skip "$s" "no filex binary at $BIN (REALENV_BIN, or pnpm run build:all)"; done
  exit 0
fi
if [ -z "$PW_VERSION" ]; then
  for s in "${STAGES[@]}"; do skip "$s" "e2e dependencies are not installed (pnpm install)"; done
  exit 0
fi

mkdir -p "$WORK/logs" "$WORK/results"
# A signal ends the run (and removes what it started): the trap must exit,
# or a run killed while it waits for REALENV_LOCK goes on without it.
trap 'down_all' EXIT
trap 'exit 130' INT TERM

for s in "${STAGES[@]}"; do
  case $s in
  tls | sso | office) ;;
  *) log "unknown stage $s (tls, sso, office)"; exit 2 ;;
  esac
  down_all
  if [ -n "${REALENV_LOCK:-}" ]; then
    exec 9>"$REALENV_LOCK"
    log "$s: waiting for $REALENV_LOCK"
    flock 9 || { log "$s: could not take $REALENV_LOCK"; exit 1; }
  fi
  log "$s: start"
  "stage_$s"
  if [ "${REALENV_KEEP:-0}" = 1 ] && [ "$s" = "${STAGES[-1]}" ]; then
    trap - EXIT
    log "$s: containers kept (REALENV_KEEP=1); remove them with: docker rm -f -v \$(docker ps -aq --filter name=^$P-)"
  else
    down_all
  fi
  [ -n "${REALENV_LOCK:-}" ] && flock -u 9
done

echo
log "passed:  ${PASSED[*]:-none}"
[ ${#SKIPPED[@]} -gt 0 ] && for x in "${SKIPPED[@]}"; do log "skipped: $x"; done
[ ${#FAILED[@]} -gt 0 ] && { for x in "${FAILED[@]}"; do log "FAILED:  $x"; done; exit 1; }
exit 0
