#!/usr/bin/env bash
# Installs an app plugin into a running filex and runs one action on a
# sample file — the "does it actually work in a server" check for a plugin
# repository, before it is published.
#
#   bash scripts/smoke-app-plugin.sh <base-url> <admin-email> <admin-pass> <plugin.wasm> <filex-app.json> <action> <sample-file> [params-json]
#
# Steps: log in → dry-run install (prints the permission review) → install
# with the manifest's exact grant → seed a local storage + upload the sample →
# run the action → poll the op → print the outputs and the plugin's log tail →
# remove the plugin and the storage. Exit code follows the op's status.
set -euo pipefail
base="${1:?base url}"; email="${2:?admin email}"; pass="${3:?admin password}"
wasm="${4:?plugin.wasm}"; manifest="${5:?filex-app.json}"; action="${6:?action id}"; sample="${7:?sample file}"
params="${8:-{\}}"
jar="$(mktemp)"; trap 'rm -f "$jar"' EXIT
curl -sS -c "$jar" -H 'Content-Type: application/json' -d "{\"email\":\"$email\",\"password\":\"$pass\"}" "$base/api/auth/login" >/dev/null
grant="$(python3 -c "import json,sys; print(json.dumps({'permissions': json.load(open(sys.argv[1]))['permissions']}))" "$manifest")"
name="$(python3 -c "import json,sys; print(json.load(open(sys.argv[1]))['name'])" "$manifest")"
echo "== dry run"
curl -sS -b "$jar" -F "wasm=@$wasm" -F "manifest=@$manifest" "$base/api/admin/app-plugins?dry_run=1" | python3 -m json.tool | head -40
echo "== install"
inst="$(curl -sS -b "$jar" -F "wasm=@$wasm" -F "manifest=@$manifest" -F "grant=$grant" "$base/api/admin/app-plugins")"
echo "$inst" | python3 -m json.tool | head -20
pid="$(echo "$inst" | python3 -c "import json,sys; print(json.load(sys.stdin).get('id',''))")"
[ -n "$pid" ] || { echo "install failed"; exit 1; }
stname="smoke-$name-$$"
mkdir -p "/tmp/filex-$stname"
st="$(curl -sS -b "$jar" -H 'Content-Type: application/json' -d "{\"name\":\"$stname\",\"driver\":\"local\",\"mount_path\":\"/$stname\",\"config\":{\"root\":\"/tmp/filex-$stname\"},\"enabled\":true}" "$base/api/admin/storages")"
echo "$st" | python3 -m json.tool | head -5
fname="$(basename "$sample")"
curl -sS -b "$jar" -F "path=$stname://" -F "file[]=@$sample" "$base/api/files/manager?action=upload" >/dev/null
echo "== run $action on $stname://$fname"
run="$(curl -sS -b "$jar" -H 'Content-Type: application/json' -d "{\"paths\":[\"$stname://$fname\"],\"params\":$params}" "$base/api/files/plugins/actions/$name/$action/run")"
echo "$run" | python3 -m json.tool | head -30
opid="$(echo "$run" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('op',{}).get('id',''))")"
status="surface"
if [ -n "$opid" ]; then
  for _ in $(seq 1 120); do
    op="$(curl -sS -b "$jar" "$base/api/files/ops/$opid")"
    status="$(echo "$op" | python3 -c "import json,sys; print(json.load(sys.stdin)['status'])")"
    case "$status" in ok|failed|partial|cancelled) break;; esac
    sleep 1
  done
  echo "== op $status"
  echo "$op" | python3 -m json.tool
fi
echo "== plugin log tail"
curl -sS -b "$jar" "$base/api/admin/app-plugins/$pid/logs?after=0" | python3 -c "import json,sys; [print(l['level'], l['msg']) for l in json.load(sys.stdin)['lines'][-15:]]"
echo "== listing"
curl -sS -b "$jar" "$base/api/files/manager?action=index&path=$stname://" | python3 -c "import json,sys; [print(f['basename'], f.get('size')) for f in json.load(sys.stdin)['files']]"
echo "== cleanup"
curl -sS -b "$jar" -X DELETE "$base/api/admin/app-plugins/$pid" -o /dev/null -w '%{http_code}\n'
sid="$(echo "$st" | python3 -c "import json,sys; print(json.load(sys.stdin).get('id',''))")"
[ -n "$sid" ] && curl -sS -b "$jar" -X DELETE "$base/api/admin/storages/$sid" -o /dev/null -w '%{http_code}\n'
[ "$status" = ok ] || [ "$status" = surface ]
