#!/usr/bin/env bash
# seed-example-fixtures.sh
#
# Seeds the demo storage on `main` (Hetzner) with a representative fixture
# of every file type the FileManager PreviewModal can dispatch to.
#
# Re-running is safe — every step is overwrite-on-existing. The script first
# materialises the fixtures into a fixed scratch dir, then mirrors them to:
#   1. /root/filex/storages/demo/example/                  (local FS storage)
#   2. s3://brf/filex-test/example/                        (Hetzner Object Storage)
#
# Designed to run on `main` (Linux). Required tooling: python3, rsync, zip,
# aws (AWS CLI v2). All are already present on the box.
#
# Usage (on main):
#   bash /path/to/repo/scripts/seed-example-fixtures.sh
#
# Override the source repo location with REPO_DIR if running from a non-default
# clone, e.g.  REPO_DIR=/srv/filemanager bash seed-example-fixtures.sh
#
# This script is purely additive; nothing under example/ is deleted, so files
# you've staged manually will keep working unless they share a name with one
# of the fixtures below.

set -euo pipefail

# ---- Config ----------------------------------------------------------------

REPO_DIR="${REPO_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
SCRATCH_DIR="${SCRATCH_DIR:-/tmp/filex-fixtures}"

LOCAL_TARGET="/root/filex/storages/demo/example"
S3_BUCKET="brf"
S3_PREFIX="filex-test/example"
S3_ENDPOINT="https://nbg1.your-objectstorage.com"

# Hetzner Object Storage credentials. Hard-coded to keep the script
# self-contained on `main`; rotate via OBS console if leaked.
export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:-1QRDCWIOTQP9Q0J3KF1L}"
export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:-ILCuGlALpZGRccfy6yX6o6bMGwTz98wZ5XzdcwBu}"
export AWS_DEFAULT_REGION="${AWS_DEFAULT_REGION:-nbg1}"

# All fixtures the FileManager PreviewModal dispatches to a non-trivial viewer.
# Keep this list in sync with scripts/_gen_fixtures.py — that's the source of
# truth for content; this list drives the rsync + S3 upload loops.
FIXTURES=(
  # Office (OnlyOffice + libreoffice viewer/thumb)
  report.xlsx
  letter.docx
  slides.pptx
  notes.odt
  budget.ods
  # Diagram / notebook
  diagram.drawio
  flow.mmd
  notebook.ipynb
  # 3D / model
  cube.stl
  cube.obj
  cube.glb
  # Documents / e-book
  book.epub
  # Image (binary placeholders, exercise PSD/TIFF fallback paths)
  layered.psd
  scan.tiff
)

# ---- Pre-flight ------------------------------------------------------------

for cmd in python3 rsync aws zip printf; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "[seed-example] missing required tool: $cmd" >&2
    exit 1
  fi
done

GEN_SCRIPT="$REPO_DIR/scripts/_gen_fixtures.py"
if [[ ! -f "$GEN_SCRIPT" ]]; then
  echo "[seed-example] cannot find generator at $GEN_SCRIPT" >&2
  echo "[seed-example] is REPO_DIR set correctly? (current: $REPO_DIR)" >&2
  exit 1
fi

# ---- 1. Materialise fixtures into the scratch dir --------------------------

mkdir -p "$SCRATCH_DIR"
echo "[seed-example] generating fixtures in $SCRATCH_DIR"
FIXTURE_OUT="$SCRATCH_DIR" python3 "$GEN_SCRIPT"

# Sanity: every name in FIXTURES has to exist on disk.
for name in "${FIXTURES[@]}"; do
  if [[ ! -s "$SCRATCH_DIR/$name" ]]; then
    echo "[seed-example] generator failed to produce $name" >&2
    exit 1
  fi
done

# ---- 2. Mirror to local demo storage --------------------------------------

mkdir -p "$LOCAL_TARGET"
echo "[seed-example] rsync -> $LOCAL_TARGET"
# Build the rsync source list from $FIXTURES so we never touch unrelated files.
RSYNC_SRC=()
for name in "${FIXTURES[@]}"; do
  RSYNC_SRC+=("$SCRATCH_DIR/$name")
done
rsync -av --chmod=F0644 "${RSYNC_SRC[@]}" "$LOCAL_TARGET/"

# ---- 3. Mirror to S3 ------------------------------------------------------

echo "[seed-example] aws s3 cp -> s3://$S3_BUCKET/$S3_PREFIX/"
for name in "${FIXTURES[@]}"; do
  aws --endpoint-url="$S3_ENDPOINT" s3 cp \
    "$SCRATCH_DIR/$name" \
    "s3://$S3_BUCKET/$S3_PREFIX/$name" \
    --only-show-errors
done

# ---- 4. Summary -----------------------------------------------------------

echo
echo "[seed-example] done — ${#FIXTURES[@]} fixtures uploaded:"
printf '  %-22s  %8s bytes  ->  %s + s3://%s/%s/\n' "FILE" "SIZE" "$LOCAL_TARGET" "$S3_BUCKET" "$S3_PREFIX"
for name in "${FIXTURES[@]}"; do
  size=$(stat -c '%s' "$SCRATCH_DIR/$name" 2>/dev/null || stat -f '%z' "$SCRATCH_DIR/$name")
  printf '  %-22s  %8s\n' "$name" "$size"
done
