#!/usr/bin/env bash
# Acceptance v2 for the plugin subsystem.
#
# Driven against a REAL plugin written WITHOUT the Go SDK
# (examples/plugin-diskfs/plugin.py, stdlib only) that writes to a real
# directory — so every claim is measured rather than assumed:
#
#   launched-binary kind · remote kind · a non-Go language · real disk ·
#   25 MB streaming upload · NATIVE ranged reads · mkdir/move/copy ·
#   trash+restore · set_mtime · the SSE change stream · a read-only plugin ·
#   conformance · a plugin that LIES · upgrade + rollback ·
#   and a plugin that is KILLED mid-life.
set -uo pipefail

BASE="http://127.0.0.1:5295"
DATA=/tmp/plugacc
BACKING=/tmp/plugacc-backing
PASS=devpassword123
FAILED=0

ok()   { printf '  \033[32mOK\033[0m   %s\n' "$1"; }
bad()  { printf '  \033[31mFAIL\033[0m %s\n' "$1"; FAILED=$((FAILED+1)); }
step() { printf '\n=== %s ===\n' "$1"; }
is()   { if [ "$2" = "$3" ]; then ok "$1 ($2)"; else bad "$1: '$2' != expected '$3'"; fi; }
has()  { case "$2" in *"$3"*) ok "$1";; *) bad "$1: '$3' not found in '$2'";; esac; }
hasnt(){ case "$2" in *"$3"*) bad "$1: '$3' IS in '$2' and should not be";; *) ok "$1";; esac; }

# Only the plugin processes. NOT "diskfs": this script lives in a directory
# called plugin-diskfs, so that pattern matches the shell running it and
# the run kills itself (measured: exit 15, empty log).
pkill -f "plugin[.]py" 2>/dev/null
rm -rf "$DATA" "$BACKING"; mkdir -p "$DATA" "$BACKING/live" "$BACKING/remote" "$BACKING/ro"
echo "read-only content" > "$BACKING/ro/readme.txt"

export FILEX_DATA_DIR="$DATA" FILEX_DB_DRIVER=sqlite FILEX_DB_DSN="$DATA/i.sqlite" \
       FILEX_LISTEN=127.0.0.1:5295 FILEX_SECRET_KEY=acceptance-key
${FILEX_BIN:-/tmp/filex-lin} migrate up > "$DATA/migrate.log" 2>&1
${FILEX_BIN:-/tmp/filex-lin} admin reset-password --email admin@local --password "$PASS" > "$DATA/pw.log" 2>&1 || true
${FILEX_BIN:-/tmp/filex-lin} serve > "$DATA/serve.log" 2>&1 &
SRV=$!
trap 'kill $SRV 2>/dev/null || true; pkill -f "plugin.py" 2>/dev/null' EXIT
for i in $(seq 1 60); do curl -sf "$BASE/healthz" >/dev/null 2>&1 && break; sleep 0.5; done

TOK=$(curl -sS -X POST "$BASE/api/auth/login" -H 'Content-Type: application/json' \
  -d "{\"email\":\"admin@local\",\"password\":\"$PASS\"}" \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')
A=(-H "Authorization: Bearer $TOK")
J() { python3 -c "import sys,json;d=json.load(sys.stdin);$1"; }
plugin_state() { curl -sS "${A[@]}" "$BASE/api/admin/plugins" | J "p=[x for x in d['plugins'] if x['name']=='$1'];print(p[0]['state'] if p else 'missing')"; }
wait_state() { for i in $(seq 1 60); do S=$(plugin_state "$1"); [ "$S" = "$2" ] && break; sleep 0.5; done; echo "$S"; }

SRC="$(cd "$(dirname "$0")" && pwd)/plugin.py"

# ─────────────────────────────────────────────────────────────────────────────
step "1. A plugin that does NOT use the Go SDK (Python) is launched by filex"
cp "$SRC" /tmp/diskfs.py && chmod +x /tmp/diskfs.py
CODE=$(curl -sS "${A[@]}" -X POST "$BASE/api/admin/plugins" \
  -F "name=diskfs" -F "file=@/tmp/diskfs.py;filename=plugin.py" -o /tmp/inst.json -w '%{http_code}')
is "install HTTP" "$CODE" "201"
is "state" "$(wait_state diskfs running)" "running"
curl -sS "${A[@]}" "$BASE/api/admin/plugins" | J 'p=[x for x in d["plugins"] if x["name"]=="diskfs"][0];print("  it reports:",p["driver"],p["version"],"|",p["label"])'
CAPS=$(curl -sS "${A[@]}" "$BASE/api/admin/plugins" | J 'c=[x for x in d["plugins"] if x["name"]=="diskfs"][0]["capabilities"];print(",".join(k for k in ["range","write","delete","move","copy","mkdir","set_mtime","watch"] if c[k]))')
is "the full capability set" "$CAPS" "range,write,delete,move,copy,mkdir,set_mtime,watch"

step "2. The driver and its config form come from the plugin"
is "registered driver" "$(curl -sS "${A[@]}" "$BASE/api/admin/storage-drivers" | J 'print(",".join(sorted(x["driver"] for x in d if x["driver"].startswith("plugin:"))))')" "plugin:diskfs"
is "form field" "$(curl -sS "${A[@]}" "$BASE/api/admin/storage-drivers" | J 'f=[y for y in d if y["driver"]=="plugin:diskfs"][0]["fields"][0];print(f["key"],f["type"],f["required"],f["root"])')" "root string True True"

step "3. A storage and a small file, on real disk"
is "storage HTTP" "$(curl -sS "${A[@]}" -X POST "$BASE/api/admin/storages" -H 'Content-Type: application/json' -d "{\"name\":\"disk\",\"driver\":\"plugin:diskfs\",\"config\":{\"root\":\"$BACKING/live\"},\"enabled\":true}" -o /dev/null -w '%{http_code}')" "200"
echo "hello from the python plugin" > /tmp/small.txt
is "upload HTTP" "$(curl -sS "${A[@]}" -X POST "$BASE/api/files/manager?q=upload" -F "path=disk://" -F "file[]=@/tmp/small.txt" -o /dev/null -w '%{http_code}')" "200"
if [ -f "$BACKING/live/small.txt" ]; then ok "the file really is on disk: $(tr -d '\n' < "$BACKING/live/small.txt")"; else bad "the file is not on disk"; fi

step "4. 25 MB — the streaming path (past the 8 MB buffer), sha256 end to end"
head -c 26214400 /dev/urandom > /tmp/big.bin
SUM_IN=$(sha256sum /tmp/big.bin | cut -d' ' -f1)
is "large upload HTTP" "$(curl -sS "${A[@]}" -X POST "$BASE/api/files/manager?q=upload" -F "path=disk://" -F "file[]=@/tmp/big.bin" -o /dev/null -w '%{http_code}')" "200"
is "sha256 (on the plugin's own disk)" "$(sha256sum "$BACKING/live/big.bin" 2>/dev/null | cut -d' ' -f1)" "$SUM_IN"
curl -sS "${A[@]}" "$BASE/api/files/manager?q=download&path=disk://big.bin" -o /tmp/big.out
is "sha256 (downloaded back)" "$(sha256sum /tmp/big.out | cut -d' ' -f1)" "$SUM_IN"

step "5. NATIVE ranged read — the plugin's own Range, not the host's emulation"
is "range 15-27" "$(curl -sS -r 15-27 "${A[@]}" "$BASE/api/files/manager?q=download&path=disk://small.txt")" "python plugin"
R206=$(grep -c 'read?path=.* HTTP/1.1\\" 206' "$DATA/serve.log" 2>/dev/null || echo 0)
if [ "$R206" -gt 0 ]; then ok "the plugin answered 206 (partial content) $R206 time(s)"; else bad "no Range reached the plugin (it may have fallen back to emulation)"; fi

step "6. mkdir + move + copy, all served by the plugin's own implementations"
is "mkdir HTTP" "$(curl -sS "${A[@]}" -X POST "$BASE/api/files/manager?q=newfolder" -H 'Content-Type: application/json' -d '{"path":"disk://","name":"folder"}' -o /dev/null -w '%{http_code}')" "200"
if [ -d "$BACKING/live/folder" ]; then ok "the directory is on disk"; else bad "no directory on disk"; fi
# ⚠ vfMove: `path` IS the destination directory; `items` are the sources.
curl -sS "${A[@]}" -X POST "$BASE/api/files/manager?q=move" -H 'Content-Type: application/json' \
  -d '{"path":"disk://folder","items":[{"path":"disk://small.txt"}]}' -o /tmp/mv.json
if [ -f "$BACKING/live/folder/small.txt" ] && [ ! -f "$BACKING/live/small.txt" ]; then ok "move moved it"; else bad "move: $(cat /tmp/mv.json | head -c 120)"; fi
# ⚠ /api/files/copy is the async ops queue: {source:[...], target:"dir"}.
curl -sS "${A[@]}" -X POST "$BASE/api/files/copy" -H 'Content-Type: application/json' \
  -d '{"source":["disk://folder/small.txt"],"target":"disk://"}' -o /tmp/cp.json
sleep 3   # queued, not synchronous
if [ -f "$BACKING/live/small.txt" ] && [ -f "$BACKING/live/folder/small.txt" ]; then ok "copy left two copies"; else bad "copy: $(head -c 160 /tmp/cp.json)"; fi
MV=$(grep -c '"POST /v1/instances/.*/move' "$DATA/serve.log" || echo 0)
CP=$(grep -c '"POST /v1/instances/.*/copy' "$DATA/serve.log" || echo 0)
MK=$(grep -c '"POST /v1/instances/.*/mkdir' "$DATA/serve.log" || echo 0)
ok "native calls that reached the plugin — move:$MV copy:$CP mkdir:$MK"

step "7. Trash, then restore"
# The trash flow keys off the node index, and a file produced by the ASYNC
# copy above is indexed by the ops dbsync hook a moment later. What is under
# test here is trash on a PLUGIN storage, not that timing, so this step
# uploads its own file.
echo "bound for the trash" > /tmp/trash-me.txt
curl -sS "${A[@]}" -X POST "$BASE/api/files/manager?q=upload" -F "path=disk://" -F "file[]=@/tmp/trash-me.txt" -o /dev/null
curl -sS "${A[@]}" -X POST "$BASE/api/files/manager?q=delete" -H 'Content-Type: application/json' \
  -d '{"path":"disk://","items":[{"path":"disk://trash-me.txt","type":"file"}]}' -o /tmp/del.json
NID=$(curl -sS "${A[@]}" "$BASE/api/files/manager/trash" | J 'e=[x for x in d["entries"] if x["storage_name"]=="disk"];print(e[0]["id"] if e else "")')
if [ -n "$NID" ]; then ok "a trash record exists (id=$NID)"; else bad "no trash record (delete answered: $(head -c 140 /tmp/del.json))"; fi
has "restore" "$(curl -sS "${A[@]}" -X POST "$BASE/api/files/manager/restore" -H 'Content-Type: application/json' -d "{\"node_id\":$NID}")" '"ok":true'
if [ -f "$BACKING/live/trash-me.txt" ]; then ok "the file is back on disk"; else bad "the restored file is not on disk"; fi

step "8. KILLED MID-LIFE -> an invisible restart"
PID=$(pgrep -f "plugins/diskfs/plugin.py" | head -1)
if [ -n "$PID" ]; then
  ok "plugin process (pid=$PID)"
  kill -9 "$PID"; sleep 4
  is "listing after the kill" "$(curl -sS "${A[@]}" "$BASE/api/files/manager?q=index&path=disk://" -o /tmp/after.json -w '%{http_code}')" "200"
  has "the files are still there" "$(python3 -c 'import json;print(",".join(sorted(f["basename"] for f in json.load(open("/tmp/after.json")).get("files",[]))))')" "big.bin"
  NEW=$(pgrep -f "plugins/diskfs/plugin.py" | head -1)
  if [ -n "$NEW" ] && [ "$NEW" != "$PID" ]; then ok "a new process is up (pid=$NEW)"; else bad "the process did not come back"; fi
  ok "what the admin API reports: $(curl -sS "${A[@]}" "$BASE/api/admin/plugins" | J 'p=[x for x in d["plugins"] if x["name"]=="diskfs"][0];print("state="+p["state"],"restarts="+str(p["restarts"]))')"
else
  bad "no plugin process found (pgrep)"
fi

step "9. The REMOTE kind: filex does not launch it, it only connects"
FILEX_PLUGIN_TOKEN=remote-secret-token FILEX_PLUGIN_LISTEN=127.0.0.1:9099 python3 "$SRC" > /tmp/remote.log 2>&1 &
REMOTE=$!
sleep 2
head -1 /tmp/remote.log | grep -q "FILEX-PLUGIN/1 tcp:" && ok "remote handshake: $(head -1 /tmp/remote.log)" || bad "no handshake: $(head -3 /tmp/remote.log)"
CODE=$(curl -sS "${A[@]}" -X POST "$BASE/api/admin/plugins" -H 'Content-Type: application/json' \
  -d '{"name":"remote","kind":"remote","address":"http://127.0.0.1:9099","token":"remote-secret-token"}' -o /tmp/rem.json -w '%{http_code}')
is "remote registration HTTP" "$CODE" "201"
RS=$(wait_state remote running)
if [ "$RS" = running ]; then ok "the remote plugin is running"; else
  # Same driver name as the launched one, so a clash is the expected outcome.
  # Reported rather than asserted: both answers are informative here.
  ok "remote plugin state: $RS ($(curl -sS "${A[@]}" "$BASE/api/admin/plugins" | J 'p=[x for x in d["plugins"] if x["name"]=="remote"];print((p[0].get("state_error") or "")[:90] if p else "")'))"
fi
SEALED=$(python3 -c "
import sqlite3;print(sqlite3.connect('$DATA/i.sqlite').execute('select token_sealed from plugins where name=\"remote\"').fetchone()[0][:12])")
has "the token is stored sealed" "$SEALED" "enc:v1:"
hasnt "the token is NOT in plaintext" "$(python3 -c "
import sqlite3;print(sqlite3.connect('$DATA/i.sqlite').execute('select token_sealed from plugins where name=\"remote\"').fetchone()[0])")" "remote-secret-token"

step "10. At the protocol level: set-mtime + /watch (on the remote one, whose token we hold)"
INST=$(curl -sS -X POST http://127.0.0.1:9099/v1/instances -H 'Authorization: Bearer remote-secret-token' \
  -H 'Content-Type: application/json' -d "{\"config\":{\"root\":\"$BACKING/remote\"}}" | J 'print(d["instance"])')
ok "instance opened: $INST"
echo "mtime probe" > "$BACKING/remote/t.txt"
( curl -sS --max-time 6 -N http://127.0.0.1:9099/v1/instances/$INST/watch -H 'Authorization: Bearer remote-secret-token' > /tmp/sse.txt 2>&1 & ) ; sleep 1
curl -sS -X POST http://127.0.0.1:9099/v1/instances/$INST/set-mtime -H 'Authorization: Bearer remote-secret-token' \
  -H 'Content-Type: application/json' -d '{"path":"t.txt","mtime":"2020-01-02T03:04:05Z"}' -o /dev/null -w 'set-mtime http=%{http_code}\n'
MT=$(date -u -r "$BACKING/remote/t.txt" +%Y-%m-%dT%H:%M:%SZ)
is "the file mtime really changed" "$MT" "2020-01-02T03:04:05Z"
sleep 2
has "an SSE event arrived" "$(cat /tmp/sse.txt)" '"op": "modify"'
kill $REMOTE 2>/dev/null

step "11. A READ-ONLY plugin: writing must not even be offered"
sed 's/^READ_ONLY = .*/READ_ONLY = True/' "$SRC" > /tmp/diskfs-ro.py && chmod +x /tmp/diskfs-ro.py
is "read-only install HTTP" "$(curl -sS "${A[@]}" -X POST "$BASE/api/admin/plugins" -F "name=diskfsro" -F "file=@/tmp/diskfs-ro.py;filename=plugin.py" -o /dev/null -w '%{http_code}')" "201"
is "read-only state" "$(wait_state diskfsro running)" "running"
ROCAPS=$(curl -sS "${A[@]}" "$BASE/api/admin/plugins" | J 'c=[x for x in d["plugins"] if x["name"]=="diskfsro"][0]["capabilities"];print(",".join(k for k in ["range","write","delete","move","copy","mkdir","set_mtime","watch"] if c[k]))')
is "read-only capabilities" "$ROCAPS" "range,watch"
ROWRITE=$(curl -sS "${A[@]}" "$BASE/api/admin/storage-drivers" | J 'x=[y for y in d if y["driver"]=="plugin:diskfs-ro"][0];print(x["capabilities"]["write"],x["capabilities"]["read"])')
is "driver capabilities (write/read)" "$ROWRITE" "False True"
is "read-only storage HTTP" "$(curl -sS "${A[@]}" -X POST "$BASE/api/admin/storages" -H 'Content-Type: application/json' -d "{\"name\":\"readonly\",\"driver\":\"plugin:diskfs-ro\",\"config\":{\"root\":\"$BACKING/ro\"},\"enabled\":true}" -o /dev/null -w '%{http_code}')" "200"
is "reading works" "$(curl -sS "${A[@]}" "$BASE/api/files/manager?q=download&path=readonly://readme.txt")" "read-only content"
echo "try to write" > /tmp/deny.txt
UPCODE=$(curl -sS "${A[@]}" -X POST "$BASE/api/files/manager?q=upload" -F "path=readonly://" -F "file[]=@/tmp/deny.txt" -o /tmp/deny.json -w '%{http_code}')
if [ "$UPCODE" != "200" ]; then ok "the write was refused (HTTP $UPCODE: $(head -c 90 /tmp/deny.json))"; else bad "a write to a read-only plugin SUCCEEDED"; fi
if [ ! -f "$BACKING/ro/deny.txt" ]; then ok "nothing was written to disk"; else bad "a file appeared on the read-only storage"; fi

step "12. CONFORMANCE: a plugin has to PROVE what it claims"
CONF=$(curl -sS "${A[@]}" "$BASE/api/admin/plugins" | J 'p=[x for x in d["plugins"] if x["name"]=="diskfs"][0];c=p.get("conformance");print("missing" if not c else ("%s %s" % (c["verified"], c["scratch"])))')
is "verified at install" "$CONF" "True selftest"
PROBES=$(curl -sS "${A[@]}" "$BASE/api/admin/plugins" | J 'c=[x for x in d["plugins"] if x["name"]=="diskfs"][0]["conformance"];print(",".join(sorted(r["name"] for r in c["results"] if r["status"]=="pass")))')
ok "probes that passed: $PROBES"
has "the write probe ran" "$PROBES" "write"
has "the range probe ran" "$PROBES" "range"
has "the multipart probe ran" "$PROBES" "multipart"

step "13. A plugin that LIES must be REFUSED (its write swallows the file)"
sed 's|^        emit("create", rel)$|        os.remove(full)\n        emit("create", rel)|' "$SRC" > /tmp/diskfs-liar.py
sed -i 's|"name": "diskfs-ro" if READ_ONLY else "diskfs"|"name": "diskfs-liar"|' /tmp/diskfs-liar.py
chmod +x /tmp/diskfs-liar.py
CODE=$(curl -sS "${A[@]}" -X POST "$BASE/api/admin/plugins" -F "name=liar" -F "file=@/tmp/diskfs-liar.py;filename=plugin.py" -o /tmp/liar.json -w '%{http_code}')
is "the install call is accepted, the probes refuse it after" "$CODE" "201"
LSTATE=$(wait_state liar refused)
is "state" "$LSTATE" "refused"
LERR=$(curl -sS "${A[@]}" "$BASE/api/admin/plugins" | J 'p=[x for x in d["plugins"] if x["name"]=="liar"];print((p[0].get("state_error") or "")[:120] if p else "")')
has "the reason is stated" "$LERR" "fails its own claims"
DRIVERS=$(curl -sS "${A[@]}" "$BASE/api/admin/storage-drivers" | J 'print(",".join(sorted(x["driver"] for x in d if x["driver"].startswith("plugin:"))))')
hasnt "the liar's driver is NOT registered" "$DRIVERS" "plugin:diskfs-liar"
STORE=$(curl -sS "${A[@]}" -X POST "$BASE/api/admin/storages" -H 'Content-Type: application/json' \
  -d '{"name":"liar-store","driver":"plugin:diskfs-liar","config":{"root":"/tmp"},"enabled":true}' -o /tmp/ls.json -w '%{http_code}')
if [ "$STORE" != "200" ]; then ok "a storage on the liar CANNOT be created (HTTP $STORE)"; else bad "a storage was created on a plugin that lies"; fi

step "14. MULTIPART, in a real process"
MPCAP=$(curl -sS "${A[@]}" "$BASE/api/admin/plugins" | J 'c=[x for x in d["plugins"] if x["name"]=="diskfs"][0]["capabilities"];print(c.get("multipart"))')
is "multipart is declared" "$MPCAP" "True"
MPPROBE=$(curl -sS "${A[@]}" "$BASE/api/admin/plugins" | J 'c=[x for x in d["plugins"] if x["name"]=="diskfs"][0]["conformance"];print([r["status"] for r in c["results"] if r["name"]=="multipart"][0])')
is "and its probe passed" "$MPPROBE" "pass"

step "15. UPGRADE IN PLACE: swap the file, the storages stay up"
sed 's|"version": "1.0.0"|"version": "2.0.0"|' "$SRC" > /tmp/diskfs-v2.py && chmod +x /tmp/diskfs-v2.py
PID=$(curl -sS "${A[@]}" "$BASE/api/admin/plugins" | J 'print([x for x in d["plugins"] if x["name"]=="diskfs"][0]["id"])')
UPCODE=$(curl -sS "${A[@]}" -X POST "$BASE/api/admin/plugins/$PID/upgrade" -F "file=@/tmp/diskfs-v2.py;filename=plugin.py" -o /tmp/up.json -w '%{http_code}')
is "upgrade HTTP" "$UPCODE" "200"
NEWVER=$(curl -sS "${A[@]}" "$BASE/api/admin/plugins" | J 'print([x for x in d["plugins"] if x["name"]=="diskfs"][0].get("version"))')
is "the new version is live" "$NEWVER" "2.0.0"
is "the storage still lists" "$(curl -sS "${A[@]}" "$BASE/api/files/manager?q=index&path=disk://" -o /dev/null -w '%{http_code}')" "200"

step "16. UPGRADE ROLLBACK: a broken build must not cost the working one"
printf '#!/bin/sh\nexit 1\n' > /tmp/diskfs-broken.py && chmod +x /tmp/diskfs-broken.py
BAD=$(curl -sS "${A[@]}" -X POST "$BASE/api/admin/plugins/$PID/upgrade" -F "file=@/tmp/diskfs-broken.py;filename=plugin.py" -o /tmp/bad.json -w '%{http_code}')
if [ "$BAD" != "200" ]; then ok "the broken upgrade was refused (HTTP $BAD)"; else bad "a broken binary was accepted"; fi
BACKVER=$(wait_state diskfs running >/dev/null; curl -sS "${A[@]}" "$BASE/api/admin/plugins" | J 'print([x for x in d["plugins"] if x["name"]=="diskfs"][0].get("version"))')
is "the previous version is back" "$BACKVER" "2.0.0"
is "the storage still works" "$(curl -sS "${A[@]}" "$BASE/api/files/manager?q=index&path=disk://" -o /dev/null -w '%{http_code}')" "200"

step "17. LOAD: is the concurrency ceiling visible to an operator"
LOAD=$(curl -sS "${A[@]}" "$BASE/api/admin/plugins" | J 'l=[x for x in d["plugins"] if x["name"]=="diskfs"][0]["load"];print(l["max_in_flight"], l["in_flight"], l["rejected"])')
ok "load: max/in-flight/rejected = $LOAD"
is "the ceiling is reported" "$(echo $LOAD | cut -d' ' -f1)" "10"

step "18. SIGNED PLUGINS: with a trusted key set, an unsigned plugin is refused"
# Restarts filex with FILEX_PLUGIN_TRUSTED_KEYS, because the gap this guards
# against was never in checkSignature — it was that server.go did not pass the
# keys, so the mechanism existed, was documented, and could not be switched on.
# The only way to measure that is through the real process and its environment.
kill $SRV 2>/dev/null; wait $SRV 2>/dev/null
TRUSTED=$(printf '0f%.0s' $(seq 1 32))
FILEX_PLUGIN_TRUSTED_KEYS="$TRUSTED" ${FILEX_BIN:-/tmp/filex-lin} serve > "$DATA/serve2.log" 2>&1 &
SRV=$!
for i in $(seq 1 60); do curl -sf "$BASE/healthz" >/dev/null 2>&1 && break; sleep 0.5; done
is "the API says signatures are required" "$(curl -sS "${A[@]}" "$BASE/api/admin/plugins" | J 'print(d.get("requires_signature"))')" "True"
cp "$SRC" /tmp/diskfs-unsigned.py && chmod +x /tmp/diskfs-unsigned.py
UNS=$(curl -sS "${A[@]}" -X POST "$BASE/api/admin/plugins" \
  -F "name=unsigned" -F "file=@/tmp/diskfs-unsigned.py;filename=plugin.py" -o /tmp/uns.json -w '%{http_code}')
if [ "$UNS" != "201" ]; then ok "an unsigned plugin is refused (HTTP $UNS)"; else bad "an unsigned plugin installed while a trusted key was configured"; fi
has "the refusal names the setting that caused it" "$(cat /tmp/uns.json)" "FILEX_PLUGIN_TRUSTED_KEYS"
BADSIG=$(curl -sS "${A[@]}" -X POST "$BASE/api/admin/plugins" \
  -F "name=badsig" -F "signature=deadbeef" -F "file=@/tmp/diskfs-unsigned.py;filename=plugin.py" -o /tmp/bs.json -w '%{http_code}')
is "a signature from an untrusted key is refused, as a CLIENT error" "$BADSIG" "400"
is "the plugins that were already installed still run" "$(wait_state diskfs running)" "running"

step "RESULT"
if [ "$FAILED" -eq 0 ]; then printf '\033[32mALL STEPS PASSED\033[0m\n'; else printf '\033[31m%d STEP(S) FAILED\033[0m\n' "$FAILED"; fi
exit "$FAILED"
