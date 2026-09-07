#!/bin/sh
# filex container entrypoint — decides which user the server runs as.
#
# ⚠⚠ Until v0.36.0 there was no entrypoint at all: the image ran `filex` as
# root, and it did so while ALSO installing `su-exec` and creating a `filex`
# account it never used — the pieces for dropping privilege were in the image
# and nothing called them. What a reader saw was that `/data` came back
# root-owned on the host, so deleting one's own data directory needed `sudo`,
# and none of the eleven deploy manifests said a word about it.
#
# The rule here, and the reason for its shape:
#
#   1. Already unprivileged (`docker run --user`, compose `user:`, Kubernetes
#      `runAsUser`) — run as we are. We cannot chown anything anyway, and a
#      failed chown must not stop the server.
#   2. Root, and no PUID/PGID — run as root, exactly as every earlier image
#      did. ⚠ This is the important one: an existing install whose `/data` is
#      full of root-owned files keeps working, untouched, on upgrade. Dropping
#      privilege by default would leave every one of them unable to open its
#      own database.
#   3. Root, with PUID/PGID — take ownership of the data directory (once) and
#      drop to that uid/gid. Opting in is the operator's decision.
#
# ⚠ Only the DATA directory is chowned. The folders holding the operator's
# files are not ours to re-own: a storage may be a network mount, may be shared
# with other software, and may be enormous. If filex cannot write there after
# you set PUID, fix the permissions on that folder — that is a deliberate line.
set -eu

BIN=/usr/local/bin/filex
DATA="${FILEX_DATA_DIR:-/data}"

if [ "$(id -u)" != "0" ]; then
  # Case 1. PUID here would be a lie — we have no way to become anyone else —
  # so say so rather than appearing to honour it.
  if [ -n "${PUID:-}${PGID:-}" ]; then
    echo "filex: PUID/PGID ignored — the container is already running as uid $(id -u)." >&2
  fi
  exec "$BIN" "$@"
fi

if [ -z "${PUID:-}" ] && [ -z "${PGID:-}" ]; then
  exec "$BIN" "$@"                       # Case 2: unchanged, root.
fi

# Case 3.
uid="${PUID:-1000}"
gid="${PGID:-$uid}"

case "$uid$gid" in
  *[!0-9]*)
    echo "filex: PUID/PGID must be numeric (got PUID='${PUID:-}' PGID='${PGID:-}')." >&2
    exit 1
    ;;
esac
if [ "$uid" = "0" ]; then
  exec "$BIN" "$@"                       # PUID=0 is "stay root", not an error.
fi

mkdir -p "$DATA"

# ⚠ Guarded, not unconditional: `chown -R` over a data directory carrying a
# large thumbnail cache costs real time on every single boot, and after the
# first one the answer never changes.
#
# ⚠⚠ The guard is a MARKER FILE and not the directory's own owner, and that is
# not fussiness — the obvious `stat -c %u "$DATA"` check is wrong for the most
# common layout there is. `-v ./data:/data` after the operator ran `mkdir data`
# gives a directory owned by the host user with root-owned files inside it: the
# owner check passes, the chown is skipped, and filex cannot open its database.
# Measured on the published image before this file existed. The marker records
# what the tree was actually last chowned TO, which is the question being asked.
MARK="$DATA/.filex-uid"
if [ "$(cat "$MARK" 2>/dev/null || echo none)" != "${uid}:${gid}" ]; then
  echo "filex: taking ownership of $DATA for ${uid}:${gid} (once)…" >&2
  chown -R "${uid}:${gid}" "$DATA"
  printf '%s' "${uid}:${gid}" > "$MARK"
  chown "${uid}:${gid}" "$MARK"
fi

exec su-exec "${uid}:${gid}" "$BIN" "$@"
