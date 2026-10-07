# The ship's trusted-proxy step, run on the server after common.sh (see
# there), before anything is deployed.
#
#   proxies.sh INSTANCE...
#
# For every running instance: the gateway of each network its container is
# on, measured now, must be in FILEX_TRUSTED_PROXIES as the compose file will
# give it to the next container (`docker compose config`, so a value from
# .env counts).
#
# ⚠ Why this is a step (0.50, 2026-10-03): the reverse proxy reaches filex
# through the published port, so every visitor arrives from the Docker
# network's gateway. filex trusts no gateway by default; unlisted, every
# visitor is one address and the sign-in limit (10 per address in 10 minutes)
# locks all of them out. A gateway moves when its network is created again,
# so it is measured at every deploy, not remembered. Nothing is changed here:
# a gap stops the ship before the deploy, with the line to write.

set -euo pipefail

[ "$#" -gt 0 ] || die "no instance given"
command -v python3 >/dev/null || die "python3 is needed to compare addresses"
bad=0
for spec in "$@"; do
  parse_instance "$spec"
  cid=$(container_of "$I_DIR" "$I_SVC")
  if [ -z "$cid" ]; then
    log "$I_NAME: not running, no gateway to measure (a first deploy is checked after it starts)"
    continue
  fi
  gws=$(gateways_of "$cid")
  value=$(compose_env "$I_DIR" "$I_SVC" FILEX_TRUSTED_PROXIES)
  if [ -z "$value" ]; then
    log "$I_NAME: FILEX_TRUSTED_PROXIES is not set in $I_COMPOSE - write: FILEX_TRUSTED_PROXIES: \"auto, ${gws%% *}\""
    bad=1
    continue
  fi
  # shellcheck disable=SC2086
  if missing=$(trusted_covers "$value" $gws); then
    log "$I_NAME: gateway ${gws% } is trusted (FILEX_TRUSTED_PROXIES \"$value\")"
  else
    log "$I_NAME: gateway $missing is NOT in FILEX_TRUSTED_PROXIES \"$value\" - add it in $I_COMPOSE (and in the repository copy of that file)"
    bad=1
  fi
done
[ "$bad" -eq 0 ] || die "a gateway is not trusted; nothing was deployed"
log "every gateway is trusted"
