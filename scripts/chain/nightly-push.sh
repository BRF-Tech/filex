#!/usr/bin/env bash
# The night's push from the release workspace (task #361). scripts/chain/nightly.mjs
# copies the night's bundle (scripts/chain/nightly-export.sh) into the
# container NIGHTLY_PUSH_CONTAINER names and sends this script on stdin -
# never as an inline script in the command:
#
#   docker exec -i [-u USER] <container> bash -l -s -- \
#     <bundle> <commit> <branch> <wrap> <clone> <gate source> <public remote>
#
#   <bundle>         the bundle inside the container: the commit, with the
#                    public main as its prerequisite
#   <commit>         the commit the bundle must carry
#   <branch>         the public branch it replaces (nightly)
#   <wrap>           the command the push runs under, which hands git the
#                    workspace's GitHub token for that one command
#                    (with-gh-token; empty: none) - the token never leaves the
#                    workspace, and nothing here reads or prints it
#   <clone>          the container's own clone of the public repository
#                    (empty: ~/.cache/filex-nightly/public), made the first
#                    time, never checked out: it holds the public history and
#                    the night's commit, nothing else
#   <gate source>    the private repository's main checkout the export gate
#                    reads its lists from (empty: ~/filex)
#   <public remote>  the public repository
#
# The push goes through the export's pre-push gate
# (scripts/hooks/export-pre-push.sh), installed into the clone from the gate
# source every night: gitleaks with the export's own lists, no root commit,
# nothing outside refs/heads. It touches no other checkout of the workspace.
#
# Exit: 0 pushed; 1 refused or failed; 2 a setting or the bundle is wrong.
set -euo pipefail
bundle="${1:-}"
sha="${2:-}"
branch="${3:-}"
wrap="${4:-}"
dir="${5:-}"
gate_src="${6:-}"
remote="${7:-}"
say() { echo "$(date -u +%FT%TZ) nightly-push: $*"; }
[ -n "$dir" ] || dir="$HOME/.cache/filex-nightly/public"
[ -n "$gate_src" ] || gate_src="$HOME/filex"
[ -n "$bundle" ] && [ -f "$bundle" ] || { say "no bundle at '$bundle'"; exit 2; }
[[ $sha =~ ^[0-9a-f]{40}$ ]] || { say "'$sha' is not a commit"; exit 2; }
[[ $branch =~ ^[A-Za-z0-9._/-]+$ ]] && [ "$branch" != main ] || { say "'$branch' is not a branch this may push"; exit 2; }
[ -n "$remote" ] || { say "no public remote"; exit 2; }
[ -f "$gate_src/scripts/hooks/install-export-hook.sh" ] || { say "no export gate in $gate_src"; exit 2; }

if [ ! -d "$dir/.git" ]; then
  say "cloning $remote into $dir (once)"
  mkdir -p "$(dirname "$dir")"
  git clone --quiet --no-checkout "$remote" "$dir"
fi
git -C "$dir" fetch --quiet origin main
git -C "$dir" bundle verify --quiet "$bundle" || { say "the bundle does not fit the public main"; exit 2; }
git -C "$dir" fetch --quiet --force "$bundle" "refs/heads/filex-nightly:refs/heads/filex-nightly"
got=$(git -C "$dir" rev-parse refs/heads/filex-nightly)
[ "$got" = "$sha" ] || { say "the bundle carries $got, not $sha"; exit 2; }

bash "$gate_src/scripts/hooks/install-export-hook.sh" "$dir" >&2
say "pushing ${sha:0:8} to $branch"
read -r -a under <<< "$wrap"
"${under[@]}" git -C "$dir" push --force origin "refs/heads/filex-nightly:refs/heads/$branch"
say "pushed ${sha:0:8} to $branch"
