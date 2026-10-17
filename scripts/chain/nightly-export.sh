# The public form of a nightly commit, for the scripts that build from it
# (scripts/chain/nightly-build.sh: the image; scripts/chain/nightly-desktop.sh:
# the desktop packages and the snap). Sourced, never run: it defines two
# functions and changes nothing by itself.
#
#   nightly_public_export <src> <sha> <public dir> <public remote> <log>
#       <public dir> becomes a clone of <public remote> (made the first time)
#       reset to its main, and scripts/export-public.sh stages the public tree
#       of <src> - which must be checked out at <sha> - in it: the same rewrite
#       and the same refusals as a release. Returns 1, with the end of <log>
#       printed, when the export refuses the commit: nothing may be built
#       from it then.
#   nightly_public_reset <public dir>
#       throws the staged tree away again (the clone back at its main).
#
# Only what the open repository would carry goes into a nightly build, never
# the private tree (task #175).

nightly_public_reset() {
  git -C "$1" reset --quiet --hard "origin/main"
  git -C "$1" clean -ffdxq
}

nightly_public_export() {
  local src="$1" sha="$2" dir="$3" remote="$4" log="$5" head
  head=$(git -c safe.directory='*' -C "$src" rev-parse HEAD)
  if [ "$head" != "$sha" ]; then
    echo "the checkout $src is at $head, not at $sha" >&2
    return 1
  fi
  if [ ! -d "$dir/.git" ]; then
    if [ -e "$dir" ]; then
      echo "$dir exists and is not a clone: move it away" >&2
      return 1
    fi
    git clone --quiet "$remote" "$dir" || return 1
  fi
  git -C "$dir" fetch --quiet origin main || return 1
  git -C "$dir" checkout --quiet --force -B main origin/main || return 1
  nightly_public_reset "$dir" || return 1
  if ! bash "$src/scripts/export-public.sh" "$dir" > "$log" 2>&1; then
    tail -30 "$log" >&2
    echo "the export refused ${sha:0:8} (log $log): nothing is built from it" >&2
    return 1
  fi
  tail -3 "$log"
  return 0
}
