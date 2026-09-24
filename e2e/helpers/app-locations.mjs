// Where the two apps filex ships alongside itself are built — ONE table, read
// by the Playwright specs (helpers/appPlugin.ts → resolveApp) and by the
// screenshot scripts (shots/scene.mjs → findApp).
//
// ⚠ Plain JavaScript on purpose: the screenshot scripts run under bare `node`,
// which cannot import the TypeScript helper, and a second copy of this table
// is how the two would come to look for a build in different places.
//
// `sign` and `convert` live in their own repositories (BRF-Tech/filex-sign,
// BRF-Tech/filex-convert), so their `plugin.wasm` is not in this tree.

import { existsSync, readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const REPO = resolve(dirname(fileURLToPath(import.meta.url)), '../..');

/** Where each app's build lands, in the order the directories are looked in. */
export const APP_LOCATIONS = {
  convert: {
    env: 'FILEX_CONVERT_APP_DIR',
    dirs: [resolve(REPO, '../filex-convert'), resolve(REPO, '../filex-convert/dist')],
    build: 'bash scripts/build.sh in the filex-convert checkout',
    repo: 'BRF-Tech/filex-convert',
  },
  sign: {
    env: 'FILEX_SIGN_APP_DIR',
    dirs: [resolve(REPO, '../filex-sign/dist'), resolve(REPO, '../filex-sign')],
    build: 'bash scripts/build.sh in the filex-sign checkout',
    repo: 'BRF-Tech/filex-sign',
  },
  // A LANGUAGE PACK: a manifest and nothing that runs (docs/APP-PLUGINS.md →
  // Language packs). `dataOnly` is not a detail of this one entry — a lookup
  // that insists on a plugin.wasm can never find a pack, and the scene that
  // photographs one would report "not built" for a thing that has no build.
  //
  // ⚠⚠ THE THREE PACKS THAT SHIP, and only those. README: "Spanish, German
  // and French ship as examples." There is a fourth on the maintainer's
  // machine — `G:/filex-lang-ar` — and it is deliberately NOT here: Arabic is
  // filex's right-to-left test fixture, not published and not advertised
  // (Burak, 2026-09-19). The specs that need a right-to-left language reach it
  // by path through e2e/helpers/langPack.ts; nothing that takes a PICTURE may
  // find it, because these pictures are the README's.
  'lang-es': {
    env: 'FILEX_LANG_ES_APP_DIR',
    dirs: [resolve(REPO, '../filex-lang-es')],
    build: 'clone BRF-Tech/filex-lang-es beside this checkout (a pack has no build step — it is the manifest)',
    repo: 'BRF-Tech/filex-lang-es',
    dataOnly: true,
  },
  'lang-de': {
    env: 'FILEX_LANG_DE_APP_DIR',
    dirs: [resolve(REPO, '../filex-lang-de')],
    build: 'clone BRF-Tech/filex-lang-de beside this checkout (a pack has no build step — it is the manifest)',
    repo: 'BRF-Tech/filex-lang-de',
    dataOnly: true,
  },
  'lang-fr': {
    env: 'FILEX_LANG_FR_APP_DIR',
    dirs: [resolve(REPO, '../filex-lang-fr')],
    build: 'clone BRF-Tech/filex-lang-fr beside this checkout (a pack has no build step — it is the manifest)',
    repo: 'BRF-Tech/filex-lang-fr',
    dataOnly: true,
  },
};

/**
 * Locate an app's built module and its manifest.
 *
 * ⚠ An explicit `FILEX_*_APP_DIR` is AUTHORITATIVE: when it is set, that
 * directory is the only one looked in. Falling back to a sibling checkout
 * would silently use a different build than the one that was asked for, and
 * the run would look green for the wrong module.
 *
 * Returns `{ present, wasm, manifestPath, manifest, dirs, how }`; `how` says,
 * when nothing was found, where it looked and how to build one.
 */
export function locateApp(name) {
  const spot = APP_LOCATIONS[name];
  const pinned = spot?.env ? process.env[spot.env] : undefined;
  const dirs = pinned ? [pinned] : (spot?.dirs ?? []);
  for (const dir of dirs) {
    const wasm = resolve(dir, 'plugin.wasm');
    const manifestPath = resolve(dir, 'filex-app.json');
    if (!existsSync(manifestPath)) continue;
    // ⚠ A data-only app (a language pack) HAS no module, and `wasm` comes
    // back empty for it — an installer must send the manifest alone.
    if (!spot?.dataOnly && !existsSync(wasm)) continue;
    return {
      present: true,
      wasm: spot?.dataOnly ? '' : wasm,
      manifestPath,
      manifest: JSON.parse(readFileSync(manifestPath, 'utf8')),
      dirs,
      how: '',
    };
  }
  const how =
    (pinned ? `${spot?.env} is set to ${pinned}, and nothing is built there` : (spot?.build ?? 'build the plugin')) +
    ` (looked in ${dirs.join(', ') || 'nowhere — unknown app'})`;
  return { present: false, wasm: '', manifestPath: '', manifest: undefined, dirs, how };
}
