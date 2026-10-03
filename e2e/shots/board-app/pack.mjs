// Packs the board app (this directory): ui.zip - its ui/ files and the SDK
// this tree just built (packages/app-ui/dist/filex-app-ui.iife.js) - and a
// manifest pinning that zip's SHA-256, what an author ships. Shared by the
// scenes that install it (apps.mjs, defaultapps.mjs).
import { createHash } from 'node:crypto';
import { existsSync, readdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { zipStored } from '../fixtures.mjs';

const HERE = dirname(fileURLToPath(import.meta.url));
const SDK_IIFE = join(HERE, '..', '..', '..', 'packages', 'app-ui', 'dist', 'filex-app-ui.iife.js');

export function packBoardApp(dir) {
  if (!existsSync(SDK_IIFE)) {
    throw new Error(`${SDK_IIFE} is missing - build the packages first (pnpm run build:packages; pnpm shots does)`);
  }
  const ui = join(HERE, 'ui');
  const entries = readdirSync(ui)
    .sort()
    .map((name) => ({ name, data: readFileSync(join(ui, name)) }));
  entries.push({ name: 'filex-app-ui.iife.js', data: readFileSync(SDK_IIFE) });
  const zip = zipStored(entries);
  const manifest = JSON.parse(readFileSync(join(HERE, 'filex-app.json'), 'utf8'));
  manifest.ui.bundle.sha256 = createHash('sha256').update(zip).digest('hex');
  const uiZip = join(dir, 'ui.zip');
  const manifestPath = join(dir, 'filex-app.json');
  writeFileSync(uiZip, zip);
  writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
  return { uiZip, manifestPath, manifest };
}
