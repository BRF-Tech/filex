// Copies the EXPLORER web component into the desktop shell.
//
// ⚠ Not the admin SPA. The desktop app is a file manager, not a server console:
// loading web/dist put the whole admin panel (dashboard, users, storages,
// settings…) in the window, which is not what anyone opens a desktop file app
// for. The window now shows the explorer and nothing else, and the server's
// admin panel is a link that opens in the browser where it belongs.
//
// This is also why the shell talks to the backend with an API token rather than
// a cookie session: `<filex-explorer>` takes `auth: { kind: 'bearer', token }`,
// which is exactly the credential the browser sign-in hands back.
import { cp, rm, stat } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const WC_DIST = path.resolve(__dirname, '..', '..', 'packages', 'webcomponent', 'dist');
const DEST = path.resolve(__dirname, '..', 'app');

try {
  const s = await stat(path.join(WC_DIST, 'filex.js'));
  if (!s.isFile()) throw new Error('not a file');
} catch {
  console.error(
    `[sync-web] explorer bundle not found at ${WC_DIST}\n` +
      `  Build it first:  pnpm run build:packages && pnpm --filter @brftech/filex build`,
  );
  process.exit(1);
}

await rm(DEST, { recursive: true, force: true });
// Source maps are ~40 MB of debug data nobody reads from inside a packaged app.
await cp(WC_DIST, DEST, {
  recursive: true,
  filter: (src) => !src.endsWith('.map'),
});
console.log(`[sync-web] copied ${WC_DIST} -> ${DEST} (explorer web component)`);
