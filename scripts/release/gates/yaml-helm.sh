#!/usr/bin/env bash
# The YAML gate of `pnpm release`, honest in both directions.
#
#   bash scripts/release/gates/yaml-helm.sh [repo]
#
#   1. every tracked YAML file parses — EXCEPT the Helm chart's templates,
#      which are Go templates (`{{- if … }}`) and are not YAML until Helm
#      renders them. Parsing them raw failed 9 of 27 files on the v0.43.0
#      release run: a red gate with a clean tree (lesson #430).
#   2. the chart itself is checked the way it is USED: `helm lint`, then
#      `helm template` twice — with the defaults, and with EVERY optional part
#      switched on — and each render must parse and produce documents.
#
# ⚠ The "every part on" switches are READ from values.yaml, never typed here.
# The first hand-typed version said `postgres.enabled`; the key is
# `postgresql.enabled`, so PostgreSQL was never rendered and the gate said
# nothing about it (lesson #432).
#
# ⚠ A missing helm or python is a FAILURE, not a skipped step: a gate that
# announces its own absence and passes is not a gate (lesson #75).
set -uo pipefail
REPO="${1:-.}"
CHART=deploy/helm/filex
cd "$REPO" || exit 2
command -v helm >/dev/null 2>&1 || { echo "helm is not installed: the chart cannot be checked, and unchecked is not green"; exit 1; }
PY=python3
command -v "$PY" >/dev/null 2>&1 || PY=python
"$PY" -c "import yaml" 2>/dev/null || { echo "$PY has no PyYAML (pip install pyyaml)"; exit 1; }
OUT="$(mktemp -d)"
fail=0

echo "== 1. tracked YAML (Helm templates excluded)"
git ls-files '*.yml' '*.yaml' | grep -v "^$CHART/templates/" | "$PY" -c "
import sys, yaml
n = bad = 0
for p in sys.stdin.read().split():
    n += 1
    try:
        list(yaml.safe_load_all(open(p, encoding='utf-8')))
    except Exception as e:
        bad += 1
        print('  YAML FAIL', p, str(e).splitlines()[0])
print(f'  {n} files, {bad} failed')
sys.exit(1 if bad or n == 0 else 0)
" || fail=1

echo "== 2. helm lint"
if helm lint "$CHART" > "$OUT/lint.txt" 2>&1; then
  tail -1 "$OUT/lint.txt" | sed 's/^/  /'
else
  cat "$OUT/lint.txt"; fail=1
fi

SWITCHES="$("$PY" -c "
import yaml
v = yaml.safe_load(open('$CHART/values.yaml', encoding='utf-8'))
out = []
def walk(o, p=''):
    if isinstance(o, dict):
        for k, x in o.items():
            q = f'{p}.{k}' if p else k
            if k == 'enabled' and isinstance(x, bool):
                out.append(q)
            walk(x, q)
walk(v)
print(' '.join(f'--set {q}=true' for q in out))
")"
echo "   every part on: $SWITCHES"
[ -n "$SWITCHES" ] || { echo "  values.yaml has no enabled switches to turn on"; fail=1; }

render() {
  local name="$1"; shift
  if ! helm template t "$CHART" "$@" > "$OUT/$name.yaml" 2> "$OUT/$name.err"; then
    echo "  [$name] helm template FAILED"; cat "$OUT/$name.err"; fail=1; return
  fi
  "$PY" - "$OUT/$name.yaml" "$name" <<'PY' || fail=1
import sys, yaml
path, name = sys.argv[1], sys.argv[2]
docs = [d for d in yaml.safe_load_all(open(path, encoding='utf-8')) if d]
kinds = sorted({d.get('kind', '?') for d in docs})
print(f'  [{name}] {len(docs)} documents: {", ".join(kinds)}')
sys.exit(0 if docs else 1)
PY
}
echo "== 3. helm template"
render defaults
# shellcheck disable=SC2086
render all-on $SWITCHES
# grep -c prints 0 AND exits 1 on no match, so `|| echo 0` made it "0<newline>0"
# and the -le below died with "integer expected" (the v0.48.0 run).
DEFAULTS_N="$(grep -c '^kind:' "$OUT/defaults.yaml" 2>/dev/null || true)"
ALL_N="$(grep -c '^kind:' "$OUT/all-on.yaml" 2>/dev/null || true)"
DEFAULTS_N="${DEFAULTS_N:-0}"
ALL_N="${ALL_N:-0}"
if [ "$ALL_N" -le "$DEFAULTS_N" ]; then
  echo "  every part on rendered $ALL_N documents, the defaults $DEFAULTS_N — the switches changed nothing"
  fail=1
fi

# 0.50: the bundled MinIO became versitygw. Its data must never be deleted by
# an upgrade: the new PVC carries resource-policy keep, and a values file that
# still turns MinIO on stops the render (an upgrade that merely dropped the
# old PVC from the manifest would make Helm delete it).
echo "== 4. the bundled S3 server keeps its data; the retired MinIO is refused"
if helm template t "$CHART" --set versitygw.enabled=true --set storage.type=s3 > "$OUT/s3.yaml" 2> "$OUT/s3.err"; then
  "$PY" - "$OUT/s3.yaml" <<'PY' || fail=1
import sys, yaml
docs = [d for d in yaml.safe_load_all(open(sys.argv[1], encoding='utf-8')) if d]
pvc = [d for d in docs if d.get('kind') == 'PersistentVolumeClaim' and d['metadata']['name'].endswith('-versitygw-data')]
keep = [d for d in pvc if (d['metadata'].get('annotations') or {}).get('helm.sh/resource-policy') == 'keep']
env = {}
for d in docs:
    if d.get('kind') == 'Secret':
        env.update(d.get('stringData') or {})
problems = []
if not pvc:
    problems.append('no versitygw PVC rendered')
elif not keep:
    problems.append('the versitygw PVC has no helm.sh/resource-policy: keep')
if not str(env.get('FILEX_DEFAULT_STORAGE_S3_ENDPOINT', '')).endswith('-versitygw:7070'):
    problems.append(f"the seeded endpoint is {env.get('FILEX_DEFAULT_STORAGE_S3_ENDPOINT')!r}, not the bundled server")
if env.get('FILEX_DEFAULT_STORAGE_S3_REGION') != 'us-east-1':
    problems.append(f"the seeded region is {env.get('FILEX_DEFAULT_STORAGE_S3_REGION')!r}; the gateway signs us-east-1 only")
for p in problems:
    print('  ' + p)
print('  versitygw: PVC kept, storage seeded at the bundled server' if not problems else '  versitygw: RED')
sys.exit(1 if problems else 0)
PY
else
  echo "  helm template with versitygw.enabled FAILED"; cat "$OUT/s3.err"; fail=1
fi
if helm template t "$CHART" --set minio.enabled=true > /dev/null 2> "$OUT/minio.err"; then
  echo "  minio.enabled=true still renders: an upgrade would drop the old MinIO PVC without a word"; fail=1
elif grep -q 'helm.sh/resource-policy=keep' "$OUT/minio.err" && grep -q 'versitygw.enabled=true' "$OUT/minio.err"; then
  echo "  minio.enabled=true is refused, with the steps that keep its data"
else
  echo "  minio.enabled=true failed, but not with the steps that keep its data:"; cat "$OUT/minio.err"; fail=1
fi
rm -f "$OUT/s3.yaml" "$OUT/s3.err" "$OUT/minio.err"

rm -f "$OUT/lint.txt" "$OUT/defaults.yaml" "$OUT/defaults.err" "$OUT/all-on.yaml" "$OUT/all-on.err"
rmdir "$OUT" 2>/dev/null
if [ "$fail" = 0 ]; then echo "YAML GATE GREEN"; exit 0; else echo "YAML GATE RED"; exit 1; fi
