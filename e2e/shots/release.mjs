// Where the shot scripts in this directory write, and the release number the
// filex they photograph is stamped with.
//
// ⚠⚠ The pictures are not kept in the repository any more (task #176,
// 2026-10-06). Every script writes into the CAPTURE folder below, under
// e2e/.artifacts (git-ignored); `pnpm shots` compares what was taken with the
// published set, and the pictures that changed are published on filex.sh
// under names that carry their content hash. e2e/shots/manifest.json names
// the current file of every picture, and README, the docs and filex.sh link
// those URLs. The whole flow: e2e/shots/README.md.
//
// Until 0.52.0 each release's pictures went into their own
// `docs/screenshots/vX.Y.Z/` and were committed - ~40 MB a release. Those
// folders are gone from the tree; the 0.52.0 set was published as it was and
// is the first published set.
//
// ⚠ SHOTS_RELEASE is a constant, not read from a package.json, on purpose. The
// root package.json is the monorepo's `0.1.0`, which names no release, and the
// packages' versions are bumped at release step 5 - AFTER the screenshots are
// retaken at step 2 - so reading them would stamp the pictures with the
// release that already shipped. Bump this line at step 2.

import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export const SHOTS_RELEASE = 'v0.54.0';

const REPO = resolve(dirname(fileURLToPath(import.meta.url)), '../..');

/** Repo-relative, forward slashes: where a run's pictures are written. */
export const SHOTS_ROOT_REL = 'e2e/.artifacts/shots/capture';

/** Absolute path of the capture folder. */
export const SHOTS_ROOT = join(REPO, 'e2e', '.artifacts', 'shots', 'capture');

/** The capture folder for one shot script's set (`driveshell`, `sidenav`, …). */
export const shotsDir = (sub = '') => (sub ? join(SHOTS_ROOT, sub) : SHOTS_ROOT);

/**
 * The linker flags every filex a shot script photographs is built with.
 *
 * ⚠⚠ The sign-in page prints `caps.data.version`, and an unstamped build says
 * `0.1.0-dev` (backend/internal/version/version.go). That line is IN the
 * README from v0.43.0 on - `appearance/themed-signin-1440.png` - so the shop
 * window would state a version of filex that does not exist. Stamping it the
 * way goreleaser does (.goreleaser.yml, same symbol path: `-X main.version`
 * silently no-ops) makes the picture true.
 *
 * ⚠ It also makes the pictures QUIETER rather than noisier: the update check
 * is on by default, `0.1.0-dev` parses and sits below every published release,
 * and the release being cut does not. Scenes that must not reach the network
 * at all still set `FILEX_UPDATE_CHECK=0` themselves.
 *
 * ⚠ Because the number is in some pictures, it is part of every scene's digest
 * (scripts/lib/shots-site.mjs → baseTokens) unless the scene declares INPUTS
 * without '@version': bumping it retakes the scenes that may show it.
 */
export const SHOTS_LDFLAGS =
  `-s -w -X github.com/brf-tech/filex/backend/internal/version.Version=${SHOTS_RELEASE.replace(/^v/, '')}`;
