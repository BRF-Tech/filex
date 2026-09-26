#!/usr/bin/env bash
# Sign the Microsoft CLA on the microsoft/winget-pkgs pull request a release
# has just opened.
#
#   GH_TOKEN=<brkfun's WINGET_TOKEN> CLA_COMPANY="…" winget-cla.sh "<exact PR title>"
#
# ⚠ Why: microsoft-github-policy-service asks for the CLA on EVERY pull request
# from brkfun, not once per account. It was signed on #441070 (2026-09-25),
# and #441303, #441437, #441440, #441711 and #441715 still came up
# "Needs-CLA" and sat unreviewed until somebody commented by hand. The release
# opens the pull request, so the release signs it.
#
# It waits for the bot's verdict instead of commenting at once: an "agree"
# posted before the bot has looked is not what it reads. No Needs-CLA label
# within five minutes means the bot did not ask, and nothing is written. An
# agreement already on the pull request is not repeated. Nothing here fails
# the release — a pull request left unsigned is a warning on the run.
set -uo pipefail

title="${1:?the exact pull request title}"
company="${CLA_COMPANY:?CLA_COMPANY}"
repo=microsoft/winget-pkgs
wait="${WINGET_CLA_WAIT:-15}"

pr=""
for _ in $(seq 1 20); do
  pr=$(gh pr list --repo "$repo" --state open --author "@me" --search "\"$title\" in:title" \
    --json number,title --jq ".[] | select(.title == \"$title\") | .number" 2>/dev/null | head -1)
  [ -n "$pr" ] && break
  sleep "$wait"
done
if [ -z "$pr" ]; then
  echo "::warning title=winget CLA::no open pull request titled \"$title\"; nothing signed."
  exit 0
fi

labels=""
for _ in $(seq 1 20); do
  labels=$(gh pr view "$pr" --repo "$repo" --json labels --jq '[.labels[].name] | join(",")' 2>/dev/null)
  case ",$labels," in *,Needs-CLA,*) break ;; esac
  sleep "$wait"
done
case ",$labels," in
  *,Needs-CLA,*) ;;
  *) echo "winget #$pr: the CLA bot did not ask (labels: ${labels:-none})"; exit 0 ;;
esac

if gh api "repos/$repo/issues/$pr/comments" --paginate --jq '.[].body' 2>/dev/null | grep -qF '@microsoft-github-policy-service agree'; then
  echo "winget #$pr: the CLA is already agreed on this pull request"
  exit 0
fi
if gh pr comment "$pr" --repo "$repo" --body "@microsoft-github-policy-service agree company=\"$company\""; then
  echo "winget #$pr: CLA agreed for $company"
else
  echo "::warning title=winget CLA::could not comment on #$pr; sign it by hand."
fi
