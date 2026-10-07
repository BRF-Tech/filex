#!/usr/bin/env bash
# A Latin sans-serif for the browser suites, for ci.yml's Playwright jobs:
#
#   bash .github/workflows/scripts/ci-fonts.sh
#
# `playwright install --with-deps` brings CJK fonts with the browsers, and
# fontconfig then answers sans-serif and system-ui with WenQuanYi Zen Hei,
# which sets Latin text wide: the suite's layout checks (a tile value that
# must fit its card, text that must fit a button) are calibrated on a Latin
# face and go red on it. The build host's chain maps the same four families
# to Liberation Sans (scripts/chain/run.mjs, FONTS_CONF); so does this, for the
# steps after it, through XDG_CONFIG_HOME in $GITHUB_ENV.
set -euo pipefail
dir="${RUNNER_TEMP:?not in a GitHub job}/fc"
mkdir -p "$dir/fontconfig"
cat > "$dir/fontconfig/fonts.conf" <<'EOF'
<?xml version="1.0"?>
<!DOCTYPE fontconfig SYSTEM "fonts.dtd">
<fontconfig>
  <alias binding="strong"><family>sans-serif</family><prefer><family>Liberation Sans</family></prefer></alias>
  <alias binding="strong"><family>system-ui</family><prefer><family>Liberation Sans</family></prefer></alias>
  <alias binding="strong"><family>ui-sans-serif</family><prefer><family>Liberation Sans</family></prefer></alias>
  <alias binding="strong"><family>Segoe UI</family><prefer><family>Liberation Sans</family></prefer></alias>
</fontconfig>
EOF
if ! fc-list | grep -q 'Liberation Sans'; then
  sudo apt-get update -qq
  sudo apt-get install -y -qq fonts-liberation
fi
echo "XDG_CONFIG_HOME=$dir" >> "${GITHUB_ENV:?not in a GitHub job}"
echo "sans-serif is now: $(XDG_CONFIG_HOME="$dir" fc-match sans-serif)"
