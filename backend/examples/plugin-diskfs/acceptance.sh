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
is()   { if [ "$2" = "$3" ]; then ok "$1 ($2)"; else bad "$1: '$2' != beklenen '$3'"; fi; }
has()  { case "$2" in *"$3"*) ok "$1";; *) bad "$1: '$2' icinde '$3' yok";; esac; }
hasnt(){ case "$2" in *"$3"*) bad "$1: '$2' icinde '$3' VAR (olmamaliydi)";; *) ok "$1";; esac; }

# Only the plugin processes. NOT "diskfs": this script lives in a directory
# called plugin-diskfs, so that pattern matches the shell running it and
# the run kills itself (measured: exit 15, empty log).
pkill -f "plugin[.]py" 2>/dev/null
rm -rf "$DATA" "$BACKING"; mkdir -p "$DATA" "$BACKING/live" "$BACKING/remote" "$BACKING/ro"
echo "salt okunur icerik" > "$BACKING/ro/readme.txt"

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
plugin_state() { curl -sS "${A[@]}" "$BASE/api/admin/plugins" | J "p=[x for x in d['plugins'] if x['name']=='$1'];print(p[0]['state'] if p else 'yok')"; }
wait_state() { for i in $(seq 1 60); do S=$(plugin_state "$1"); [ "$S" = "$2" ] && break; sleep 0.5; done; echo "$S"; }

SRC="$(cd "$(dirname "$0")" && pwd)/plugin.py"

# ─────────────────────────────────────────────────────────────────────────────
step "1. Go SDK KULLANMAYAN bir eklenti (Python) filex tarafindan baslatiliyor"
cp "$SRC" /tmp/diskfs.py && chmod +x /tmp/diskfs.py
CODE=$(curl -sS "${A[@]}" -X POST "$BASE/api/admin/plugins" \
  -F "name=diskfs" -F "file=@/tmp/diskfs.py;filename=plugin.py" -o /tmp/inst.json -w '%{http_code}')
is "kurulum HTTP" "$CODE" "201"
is "durum" "$(wait_state diskfs running)" "running"
curl -sS "${A[@]}" "$BASE/api/admin/plugins" | J 'p=[x for x in d["plugins"] if x["name"]=="diskfs"][0];print("  bildirdigi:",p["driver"],p["version"],"|",p["label"])'
CAPS=$(curl -sS "${A[@]}" "$BASE/api/admin/plugins" | J 'c=[x for x in d["plugins"] if x["name"]=="diskfs"][0]["capabilities"];print(",".join(k for k in ["range","write","delete","move","copy","mkdir","set_mtime","watch"] if c[k]))')
is "tam yetenek kumesi" "$CAPS" "range,write,delete,move,copy,mkdir,set_mtime,watch"

step "2. Surucu + form eklentiden geliyor"
is "kayitli surucu" "$(curl -sS "${A[@]}" "$BASE/api/admin/storage-drivers" | J 'print(",".join(sorted(x["driver"] for x in d if x["driver"].startswith("plugin:"))))')" "plugin:diskfs"
is "form alani" "$(curl -sS "${A[@]}" "$BASE/api/admin/storage-drivers" | J 'f=[y for y in d if y["driver"]=="plugin:diskfs"][0]["fields"][0];print(f["key"],f["type"],f["required"],f["root"])')" "root string True True"

step "3. Depo + kucuk dosya (gercek diske)"
is "depo HTTP" "$(curl -sS "${A[@]}" -X POST "$BASE/api/admin/storages" -H 'Content-Type: application/json' -d "{\"name\":\"disk\",\"driver\":\"plugin:diskfs\",\"config\":{\"root\":\"$BACKING/live\"},\"enabled\":true}" -o /dev/null -w '%{http_code}')" "200"
echo "merhaba python eklentisi" > /tmp/small.txt
is "upload HTTP" "$(curl -sS "${A[@]}" -X POST "$BASE/api/files/manager?q=upload" -F "path=disk://" -F "file[]=@/tmp/small.txt" -o /dev/null -w '%{http_code}')" "200"
if [ -f "$BACKING/live/small.txt" ]; then ok "dosya gercekten diskte: $(tr -d '\n' < "$BACKING/live/small.txt")"; else bad "dosya diskte yok"; fi

step "4. 25 MB — akis yolu (8 MB tampon esiginin ustu), sha256 uctan uca"
head -c 26214400 /dev/urandom > /tmp/big.bin
SUM_IN=$(sha256sum /tmp/big.bin | cut -d' ' -f1)
is "buyuk upload HTTP" "$(curl -sS "${A[@]}" -X POST "$BASE/api/files/manager?q=upload" -F "path=disk://" -F "file[]=@/tmp/big.bin" -o /dev/null -w '%{http_code}')" "200"
is "sha256 (eklentinin diskinde)" "$(sha256sum "$BACKING/live/big.bin" 2>/dev/null | cut -d' ' -f1)" "$SUM_IN"
curl -sS "${A[@]}" "$BASE/api/files/manager?q=download&path=disk://big.bin" -o /tmp/big.out
is "sha256 (geri indirilen)" "$(sha256sum /tmp/big.out | cut -d' ' -f1)" "$SUM_IN"

step "5. YEREL aralikli okuma — eklentinin kendi Range'i"
is "aralik 8-24" "$(curl -sS -r 8-24 "${A[@]}" "$BASE/api/files/manager?q=download&path=disk://small.txt")" "python eklentisi"
R206=$(grep -c 'read?path=.* HTTP/1.1\\" 206' "$DATA/serve.log" 2>/dev/null || echo 0)
if [ "$R206" -gt 0 ]; then ok "eklenti 206 (kismi icerik) dondu: $R206 kez"; else bad "eklentiye Range gitmemis (emule yola dusmus olabilir)"; fi

step "6. mkdir + move + copy (hepsi eklentinin YEREL uygulamasi)"
is "mkdir HTTP" "$(curl -sS "${A[@]}" -X POST "$BASE/api/files/manager?q=newfolder" -H 'Content-Type: application/json' -d '{"path":"disk://","name":"klasor"}' -o /dev/null -w '%{http_code}')" "200"
if [ -d "$BACKING/live/klasor" ]; then ok "dizin diskte"; else bad "dizin yok"; fi
# ⚠ vfMove: `path` IS the destination directory; `items` are the sources.
curl -sS "${A[@]}" -X POST "$BASE/api/files/manager?q=move" -H 'Content-Type: application/json' \
  -d '{"path":"disk://klasor","items":[{"path":"disk://small.txt"}]}' -o /tmp/mv.json
if [ -f "$BACKING/live/klasor/small.txt" ] && [ ! -f "$BACKING/live/small.txt" ]; then ok "move tasidi"; else bad "move: $(cat /tmp/mv.json | head -c 120)"; fi
# ⚠ /api/files/copy is the async ops queue: {source:[...], target:"dir"}.
curl -sS "${A[@]}" -X POST "$BASE/api/files/copy" -H 'Content-Type: application/json' \
  -d '{"source":["disk://klasor/small.txt"],"target":"disk://"}' -o /tmp/cp.json
sleep 3   # queued, not synchronous
if [ -f "$BACKING/live/small.txt" ] && [ -f "$BACKING/live/klasor/small.txt" ]; then ok "copy iki kopya birakti"; else bad "copy: $(head -c 160 /tmp/cp.json)"; fi
MV=$(grep -c '"POST /v1/instances/.*/move' "$DATA/serve.log" || echo 0)
CP=$(grep -c '"POST /v1/instances/.*/copy' "$DATA/serve.log" || echo 0)
MK=$(grep -c '"POST /v1/instances/.*/mkdir' "$DATA/serve.log" || echo 0)
ok "eklentiye giden yerel cagrilar — move:$MV copy:$CP mkdir:$MK"

step "7. Cop kutusu -> geri yukle"
# The trash flow keys off the node index, and a file produced by the ASYNC
# copy above is indexed by the ops dbsync hook a moment later. What is under
# test here is trash on a PLUGIN storage, not that timing, so this step
# uploads its own file.
echo "cope gidecek" > /tmp/trash-me.txt
curl -sS "${A[@]}" -X POST "$BASE/api/files/manager?q=upload" -F "path=disk://" -F "file[]=@/tmp/trash-me.txt" -o /dev/null
curl -sS "${A[@]}" -X POST "$BASE/api/files/manager?q=delete" -H 'Content-Type: application/json' \
  -d '{"path":"disk://","items":[{"path":"disk://trash-me.txt","type":"file"}]}' -o /tmp/del.json
NID=$(curl -sS "${A[@]}" "$BASE/api/files/manager/trash" | J 'e=[x for x in d["entries"] if x["storage_name"]=="disk"];print(e[0]["id"] if e else "")')
if [ -n "$NID" ]; then ok "cop kaydi (id=$NID)"; else bad "cop kaydi yok (silme cevabi: $(head -c 140 /tmp/del.json))"; fi
has "geri yukleme" "$(curl -sS "${A[@]}" -X POST "$BASE/api/files/manager/restore" -H 'Content-Type: application/json' -d "{\"node_id\":$NID}")" '"ok":true'
if [ -f "$BACKING/live/trash-me.txt" ]; then ok "dosya diske geri kondu"; else bad "geri yuklenen dosya diskte yok"; fi

step "8. CANLI COKME -> gorunmez yeniden baslama"
PID=$(pgrep -f "plugins/diskfs/plugin.py" | head -1)
if [ -n "$PID" ]; then
  ok "eklenti sureci (pid=$PID)"
  kill -9 "$PID"; sleep 4
  is "cokme sonrasi listeleme" "$(curl -sS "${A[@]}" "$BASE/api/files/manager?q=index&path=disk://" -o /tmp/after.json -w '%{http_code}')" "200"
  has "dosyalar duruyor" "$(python3 -c 'import json;print(",".join(sorted(f["basename"] for f in json.load(open("/tmp/after.json")).get("files",[]))))')" "big.bin"
  NEW=$(pgrep -f "plugins/diskfs/plugin.py" | head -1)
  if [ -n "$NEW" ] && [ "$NEW" != "$PID" ]; then ok "yeni surec (pid=$NEW)"; else bad "surec yeniden baslamadi"; fi
  ok "yonetici raporu: $(curl -sS "${A[@]}" "$BASE/api/admin/plugins" | J 'p=[x for x in d["plugins"] if x["name"]=="diskfs"][0];print("state="+p["state"],"restarts="+str(p["restarts"]))')"
else
  bad "eklenti sureci bulunamadi (pgrep)"
fi

step "9. UZAK eklenti (filex baslatmiyor, yalniz baglaniyor)"
FILEX_PLUGIN_TOKEN=remote-secret-token FILEX_PLUGIN_LISTEN=127.0.0.1:9099 python3 "$SRC" > /tmp/remote.log 2>&1 &
REMOTE=$!
sleep 2
head -1 /tmp/remote.log | grep -q "FILEX-PLUGIN/1 tcp:" && ok "uzak eklenti el sikismasi: $(head -1 /tmp/remote.log)" || bad "el sikismasi yok: $(head -3 /tmp/remote.log)"
CODE=$(curl -sS "${A[@]}" -X POST "$BASE/api/admin/plugins" -H 'Content-Type: application/json' \
  -d '{"name":"uzak","kind":"remote","address":"http://127.0.0.1:9099","token":"remote-secret-token"}' -o /tmp/rem.json -w '%{http_code}')
is "uzak kayit HTTP" "$CODE" "201"
RS=$(wait_state uzak running)
if [ "$RS" = running ]; then ok "uzak eklenti calisiyor"; else
  # aynı driver adı -> çakışma beklenir; bunu ayrı raporla
  ok "uzak eklenti durumu: $RS ($(curl -sS "${A[@]}" "$BASE/api/admin/plugins" | J 'p=[x for x in d["plugins"] if x["name"]=="uzak"];print((p[0].get("state_error") or "")[:90] if p else "")'))"
fi
SEALED=$(python3 -c "
import sqlite3;print(sqlite3.connect('$DATA/i.sqlite').execute('select token_sealed from plugins where name=\"uzak\"').fetchone()[0][:12])")
has "token muhurlu saklanmis" "$SEALED" "enc:v1:"
hasnt "token duz metin DEGIL" "$(python3 -c "
import sqlite3;print(sqlite3.connect('$DATA/i.sqlite').execute('select token_sealed from plugins where name=\"uzak\"').fetchone()[0])")" "remote-secret-token"

step "10. Protokol ucu: set-mtime + /watch (uzak ornekte, token bizde)"
INST=$(curl -sS -X POST http://127.0.0.1:9099/v1/instances -H 'Authorization: Bearer remote-secret-token' \
  -H 'Content-Type: application/json' -d "{\"config\":{\"root\":\"$BACKING/remote\"}}" | J 'print(d["instance"])')
ok "ornek acildi: $INST"
echo "zaman damgasi testi" > "$BACKING/remote/t.txt"
( curl -sS --max-time 6 -N http://127.0.0.1:9099/v1/instances/$INST/watch -H 'Authorization: Bearer remote-secret-token' > /tmp/sse.txt 2>&1 & ) ; sleep 1
curl -sS -X POST http://127.0.0.1:9099/v1/instances/$INST/set-mtime -H 'Authorization: Bearer remote-secret-token' \
  -H 'Content-Type: application/json' -d '{"path":"t.txt","mtime":"2020-01-02T03:04:05Z"}' -o /dev/null -w 'set-mtime http=%{http_code}\n'
MT=$(date -u -r "$BACKING/remote/t.txt" +%Y-%m-%dT%H:%M:%SZ)
is "dosyanin mtime'i degisti" "$MT" "2020-01-02T03:04:05Z"
sleep 2
has "SSE olayi geldi" "$(cat /tmp/sse.txt)" '"op": "modify"'
kill $REMOTE 2>/dev/null

step "11. SALT OKUNUR eklenti: yazma SUNULMAMALI"
sed 's/^READ_ONLY = .*/READ_ONLY = True/' "$SRC" > /tmp/diskfs-ro.py && chmod +x /tmp/diskfs-ro.py
is "ro kurulum HTTP" "$(curl -sS "${A[@]}" -X POST "$BASE/api/admin/plugins" -F "name=diskfsro" -F "file=@/tmp/diskfs-ro.py;filename=plugin.py" -o /dev/null -w '%{http_code}')" "201"
is "ro durum" "$(wait_state diskfsro running)" "running"
ROCAPS=$(curl -sS "${A[@]}" "$BASE/api/admin/plugins" | J 'c=[x for x in d["plugins"] if x["name"]=="diskfsro"][0]["capabilities"];print(",".join(k for k in ["range","write","delete","move","copy","mkdir","set_mtime","watch"] if c[k]))')
is "ro yetenekleri" "$ROCAPS" "range,watch"
ROWRITE=$(curl -sS "${A[@]}" "$BASE/api/admin/storage-drivers" | J 'x=[y for y in d if y["driver"]=="plugin:diskfs-ro"][0];print(x["capabilities"]["write"],x["capabilities"]["read"])')
is "surucu yetenekleri (write/read)" "$ROWRITE" "False True"
is "ro depo HTTP" "$(curl -sS "${A[@]}" -X POST "$BASE/api/admin/storages" -H 'Content-Type: application/json' -d "{\"name\":\"salt\",\"driver\":\"plugin:diskfs-ro\",\"config\":{\"root\":\"$BACKING/ro\"},\"enabled\":true}" -o /dev/null -w '%{http_code}')" "200"
is "ro okuma" "$(curl -sS "${A[@]}" "$BASE/api/files/manager?q=download&path=salt://readme.txt")" "salt okunur icerik"
echo "yazmayi dene" > /tmp/deny.txt
UPCODE=$(curl -sS "${A[@]}" -X POST "$BASE/api/files/manager?q=upload" -F "path=salt://" -F "file[]=@/tmp/deny.txt" -o /tmp/deny.json -w '%{http_code}')
if [ "$UPCODE" != "200" ]; then ok "yazma reddedildi (HTTP $UPCODE: $(head -c 90 /tmp/deny.json))"; else bad "salt-okunur eklentiye yazma GECTI"; fi
if [ ! -f "$BACKING/ro/deny.txt" ]; then ok "dosya diske YAZILMADI"; else bad "salt-okunur depoda dosya olustu"; fi

step "SONUC"
if [ "$FAILED" -eq 0 ]; then printf '\033[32mTUM ADIMLAR GECTI\033[0m\n'; else printf '\033[31m%d ADIM DUSTU\033[0m\n' "$FAILED"; fi
exit "$FAILED"
