#!/usr/bin/env bash
# filex-ship.sh X.Y.Z [options] - once the tag run is green: deploy -> done.
#
# The steps, the options and the settings are in scripts/train/ship.mjs
# (`bash scripts/train/filex-ship.sh --help`); this is only the name it is
# known by. Run it as `bash scripts/train/filex-ship.sh`: the public tree does
# not keep the executable bit (lesson #1108).
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec node "$here/ship.mjs" "$@"
