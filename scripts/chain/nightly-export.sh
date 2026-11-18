#!/usr/bin/env bash
# The night's public commit (task #361): the PUBLIC form of the private main's
# tip - the tree scripts/export-public.sh makes of it, with the same rewrite
# and the same refusals as a release, never the private tree - committed on
# top of the public repository's main as ONE commit, in a clone of its own.
# scripts/chain/nightly.mjs runs it from tonight's tree, then pushes the
# commit to the public repository's `nightly` branch, where GitHub's CI
# tests it.
#
# scripts/chain/nightly.mjs runs it with:
#   NE_SRC            the nightly checkout, at NE_SHA
#   NE_SHA            the private main's commit tonight
#   NE_PUBLIC_DIR     a clone of the public repository, made here the first
#                     time and reset to its main every time
#   NE_PUBLIC_REMOTE  where that clone comes from
#   NE_OUT            where the logs, commit.txt and the bundle go
#   NE_AUTHOR_NAME NE_AUTHOR_EMAIL
#                     who the commit is by (author and committer: the account
#                     the public repository's commits are by)
#   NE_MESSAGE_FILE   the commit message (nightly-lib.mjs nightlyCommitMessage);
#                     @BASE@ in it becomes the public main's commit, known
#                     only once this script fetched it
#
# Leaves in NE_OUT: export.log (scripts/export-public.sh), commit.txt
# ("<commit> <public main>"), and nightly.bundle - the commit, with the public
# main as its prerequisite, as scripts/chain/nightly-push.sh takes it. The
# clone keeps the commit on a branch of its own (filex-nightly); the next
# night resets it.
#
# Exit: 0 committed; 3 nothing to commit (the export is the public main as it
# is; commit.txt holds that commit alone); 1 the export refused the commit;
# 2 anything else.
set -euo pipefail
: "${NE_SRC:?}" "${NE_SHA:?}" "${NE_PUBLIC_DIR:?}" "${NE_PUBLIC_REMOTE:?}" "${NE_OUT:?}"
: "${NE_AUTHOR_NAME:?}" "${NE_AUTHOR_EMAIL:?}" "${NE_MESSAGE_FILE:?}"
say() { echo "$(date -u +%FT%TZ) nightly-export: $*"; }
dir="$NE_PUBLIC_DIR"
mkdir -p "$NE_OUT"
rm -f "$NE_OUT/commit.txt" "$NE_OUT/nightly.bundle"

head=$(git -c safe.directory='*' -C "$NE_SRC" rev-parse HEAD)
if [ "$head" != "$NE_SHA" ]; then
  say "the checkout $NE_SRC is at $head, not at $NE_SHA"
  exit 2
fi

if [ ! -d "$dir/.git" ]; then
  if [ -e "$dir" ]; then
    say "$dir exists and is not a clone: move it away"
    exit 2
  fi
  git clone --quiet "$NE_PUBLIC_REMOTE" "$dir" || exit 2
fi
git -C "$dir" fetch --quiet origin main || exit 2
git -C "$dir" checkout --quiet --force -B main origin/main || exit 2
git -C "$dir" reset --quiet --hard origin/main || exit 2
git -C "$dir" clean -ffdxq || exit 2
base=$(git -C "$dir" rev-parse origin/main)

say "export of ${NE_SHA:0:8} on the public ${base:0:8}"
if ! bash "$NE_SRC/scripts/export-public.sh" "$dir" > "$NE_OUT/export.log" 2>&1; then
  tail -30 "$NE_OUT/export.log"
  say "the export refused ${NE_SHA:0:8} (log $NE_OUT/export.log): nothing is pushed"
  git -C "$dir" reset --quiet --hard origin/main
  git -C "$dir" clean -ffdxq
  exit 1
fi
tail -3 "$NE_OUT/export.log"

if git -C "$dir" diff --cached --quiet; then
  echo "$base" > "$NE_OUT/commit.txt"
  say "the export is the public main as it is (${base:0:8}): nothing to commit"
  exit 3
fi

sed "s/@BASE@/${base:0:8}/g" "$NE_MESSAGE_FILE" > "$NE_OUT/message.final" || exit 2
GIT_AUTHOR_NAME="$NE_AUTHOR_NAME" GIT_AUTHOR_EMAIL="$NE_AUTHOR_EMAIL" \
GIT_COMMITTER_NAME="$NE_AUTHOR_NAME" GIT_COMMITTER_EMAIL="$NE_AUTHOR_EMAIL" \
  git -C "$dir" -c commit.gpgsign=false commit --quiet --no-verify -F "$NE_OUT/message.final" || exit 2
sha=$(git -C "$dir" rev-parse HEAD)
git -C "$dir" branch --quiet -f filex-nightly "$sha"
git -C "$dir" bundle create "$NE_OUT/nightly.bundle" refs/heads/filex-nightly ^origin/main 2> "$NE_OUT/bundle.log" || {
  cat "$NE_OUT/bundle.log"
  say "the bundle of ${sha:0:8} could not be made"
  exit 2
}
echo "$sha $base" > "$NE_OUT/commit.txt"
say "committed ${sha:0:8} on ${base:0:8}: $(git -C "$dir" diff --shortstat "$base" "$sha")"
