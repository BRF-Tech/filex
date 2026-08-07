// Copy the built web bundle into the desktop shell so the app:// protocol can
// serve it. Run after `pnpm --filter @brftech/filex-admin build` (which itself
// needs packages/core built first). Fails loudly if the bundle is missing —
// shipping a shell with no app inside is worse than a clear error.
import { cp, rm, stat } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const WEB_DIST = path.resolve(__dirname, '..', '..', 'web', 'dist');
const DEST = path.resolve(__dirname, '..', 'app');

try {
  const s = await stat(path.join(WEB_DIST, 'index.html'));
  if (!s.isFile()) throw new Error('not a file');
} catch {
  console.error(
    `[sync-web] web bundle not found at ${WEB_DIST}\n` +
      `  Build it first:  pnpm run build:packages && pnpm --filter @brftech/filex-admin build`,
  );
  process.exit(1);
}

await rm(DEST, { recursive: true, force: true });
await cp(WEB_DIST, DEST, { recursive: true });
console.log(`[sync-web] copied ${WEB_DIST} -> ${DEST}`);
