// Packs an example app with its own interface - the board app (this directory)
// or the office app (../office-app) - the way an author ships one: ui.zip,
// the app's ui/ files and the SDK this tree just built
// (packages/app-ui/dist/filex-app-ui.iife.js), and a manifest pinning that
// zip's SHA-256. Shared by the scenes that install them (apps.mjs,
// defaultapps.mjs and store.mjs the board; officeapp.mjs and e2eapp.mjs the
// office app).
import { createHash } from 'node:crypto';
import { existsSync, readdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { zipStored } from '../fixtures.mjs';

const HERE = dirname(fileURLToPath(import.meta.url));
const SDK_IIFE = join(HERE, '..', '..', '..', 'packages', 'app-ui', 'dist', 'filex-app-ui.iife.js');

/**
 * The app in `appDir` (its filex-app.json and ui/), packed into `dir`:
 * `{ uiZip, manifestPath, manifest, zip }` - the paths the install wizard is
 * handed, the manifest as written, and the zip's bytes for an API install.
 */
export function packInterfaceApp(appDir, dir) {
  if (!existsSync(SDK_IIFE)) {
    throw new Error(`${SDK_IIFE} is missing - build the packages first (pnpm run build:packages; pnpm shots does)`);
  }
  const ui = join(appDir, 'ui');
  const entries = readdirSync(ui)
    .sort()
    .map((name) => ({ name, data: readFileSync(join(ui, name)) }));
  entries.push({ name: 'filex-app-ui.iife.js', data: readFileSync(SDK_IIFE) });
  const zip = zipStored(entries);
  const manifest = JSON.parse(readFileSync(join(appDir, 'filex-app.json'), 'utf8'));
  manifest.ui.bundle.sha256 = createHash('sha256').update(zip).digest('hex');
  const uiZip = join(dir, 'ui.zip');
  const manifestPath = join(dir, 'filex-app.json');
  writeFileSync(uiZip, zip);
  writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
  return { uiZip, manifestPath, manifest, zip };
}

/** The board app (this directory), packed into `dir`. */
export function packBoardApp(dir) {
  return packInterfaceApp(HERE, dir);
}
