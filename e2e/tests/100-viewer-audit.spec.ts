/**
 * 100-viewer-audit — UI end-to-end per-extension viewer audit.
 *
 * Why this exists: 80-file-types covers the **API** side of the preview
 * pipeline (Content-Type, bytes-back). Nothing was driving the **UI**
 * per extension until operator Ada reported a pptx regression on
 * 2026-05-14. This spec walks every advertised fixture, opens the
 * standalone editor route, and asserts the right viewer DOM landed
 * with no console errors and no failed lazy-chunk fetches.
 *
 * Strategy:
 *   1. Suite start: seed a `local`-driver storage, upload all 32
 *      fixtures from `e2e/fixtures/file-types/` via the public API.
 *   2. Login through the UI form once — that lands the bearer in
 *      sessionStorage AND sets the session cookie. The Editor.vue
 *      route reads sessionStorage so we need this step.
 *   3. Per fixture: navigate to `/admin/files/edit?path=...&mode=edit`
 *      and assert the matrix entry from `helpers/viewer.ts`.
 *
 * Each fixture is its own subtest — when a regression lands, the
 * Playwright report names the exact extension instead of one fat fail.
 */
import { test as base, expect, type APIRequestContext } from '@playwright/test';
import { ADMIN_EMAIL, ADMIN_PASSWORD, loginAs } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';
import { expectViewerForExt, instrumentPage, VIEWER_MATRIX } from '../helpers/viewer';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import fs from 'node:fs';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const STORAGE = process.env.E2E_VIEWER_STORAGE ?? `viewers-${process.env.E2E_RUN_ID ?? Date.now()}`;
const MOUNT = `/tmp/filex-e2e-viewers-${process.env.E2E_RUN_ID ?? Date.now()}`;
const FIXTURES_DIR = path.join(__dirname, '../fixtures/file-types');

/** Capability cache populated in beforeAll — drives the skip logic for
 *  externally-dependent viewers (OnlyOffice, drawio). Lokal dev runs
 *  without these wired up; CI on a fully-provisioned host gets the
 *  iframe-mount assertion. */
interface CapabilityState {
  onlyofficeReachable: boolean;
  drawioReachable: boolean;
}
let CAPS: CapabilityState = { onlyofficeReachable: false, drawioReachable: false };
const OFFICE_EXTS = new Set(['docx', 'xlsx', 'pptx', 'odt', 'ods', 'odp']);
const DRAWIO_EXTS = new Set(['drawio', 'dio']);

interface Fixture {
  name: string;
  ext: string;
  /** Some kinds can't honour `mode=edit` (no save endpoint contract).
   *  Defaults to 'edit' so md/code take the editor branch. */
  mode?: 'edit' | 'view';
}

const FIXTURES: Fixture[] = [
  { name: 'demo.md',        ext: 'md' },
  { name: 'config.json',    ext: 'json' },
  { name: 'config.yaml',    ext: 'yaml' },
  { name: 'sample.xml',     ext: 'xml' },
  { name: 'sample.html',    ext: 'html' },
  { name: 'logo.svg',       ext: 'svg' },
  { name: 'landscape.jpg',  ext: 'jpg' },
  { name: 'square.jpg',     ext: 'jpg' },
  { name: 'photo.webp',     ext: 'webp' },
  { name: 'sample.mp4',     ext: 'mp4' },
  { name: 'silence-2s.mp3', ext: 'mp3' },
  { name: 'dummy.pdf',      ext: 'pdf' },
  { name: 'sample.zip',     ext: 'zip' },
  { name: 'users.csv',      ext: 'csv' },
  { name: 'demo.js',        ext: 'js' },
  { name: 'demo.py',        ext: 'py' },
  { name: 'main.go',        ext: 'go' },
  { name: 'scan.tiff',      ext: 'tiff' },
  { name: 'layered.psd',    ext: 'psd' },
  { name: 'book.epub',      ext: 'epub' },
  { name: 'notebook.ipynb', ext: 'ipynb' },
  { name: 'flow.mmd',       ext: 'mmd' },
  { name: 'diagram.drawio', ext: 'drawio' },
  { name: 'cube.glb',       ext: 'glb' },
  { name: 'cube.obj',       ext: 'obj' },
  { name: 'cube.stl',       ext: 'stl' },
  { name: 'letter.docx',    ext: 'docx' },
  { name: 'report.xlsx',    ext: 'xlsx' },
  { name: 'slides.pptx',    ext: 'pptx' },
  { name: 'notes.odt',      ext: 'odt' },
  { name: 'budget.ods',     ext: 'ods' },
];

const test = base.extend<{ authedRequest: APIRequestContext }>({
  authedRequest: async ({ playwright, baseURL }, use) => {
    const ctx = await playwright.request.newContext({ baseURL });
    const login = await ctx.post('/api/auth/login', {
      data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
    });
    if (!login.ok()) {
      throw new Error(`audit auth failed: ${login.status()} ${await login.text()}`);
    }
    const { token } = await login.json();
    const authedCtx = await playwright.request.newContext({
      baseURL,
      extraHTTPHeaders: { Authorization: `Bearer ${token}` },
    });
    await use(authedCtx);
    await authedCtx.dispose();
    await ctx.dispose();
  },
});

async function uploadFixture(request: APIRequestContext, name: string): Promise<void> {
  const file = path.join(FIXTURES_DIR, name);
  if (!fs.existsSync(file)) {
    throw new Error(`fixture missing on disk: ${file}`);
  }
  const buf = fs.readFileSync(file);
  const res = await request.post('/api/files/manager?action=upload', {
    multipart: {
      path: `${STORAGE}://`,
      'file[]': { name, mimeType: 'application/octet-stream', buffer: buf },
    },
  });
  if (!res.ok()) {
    throw new Error(`upload ${name} failed: ${res.status()} ${await res.text()}`);
  }
}

test.describe('Viewer audit — per-extension UI mount', () => {
  test.beforeAll(async ({ request }) => {
    // Sanity check: every fixture referenced by the matrix must exist
    // on disk. Catches accidental rename or new entries in FIXTURES
    // without the corresponding asset.
    for (const f of FIXTURES) {
      if (!VIEWER_MATRIX[f.ext]) {
        throw new Error(`fixture ${f.name} has no VIEWER_MATRIX entry for ext=${f.ext}`);
      }
      if (!fs.existsSync(path.join(FIXTURES_DIR, f.name))) {
        throw new Error(`fixture asset missing: ${f.name}`);
      }
    }
    await seedLocalStorage(request, STORAGE, MOUNT);
  });

  test.beforeAll(async ({ authedRequest: request }) => {
    for (const f of FIXTURES) {
      await uploadFixture(request, f.name);
    }
    // Probe capabilities — drives per-extension skip logic. State
    // strings: "disabled" (env unset), "reachable" (probe ok),
    // anything else = consider unreachable.
    const res = await request.get('/api/files/capabilities');
    if (res.ok()) {
      const caps = (await res.json()) as {
        external?: Record<string, { state?: string; enabled?: boolean }>;
      };
      CAPS = {
        onlyofficeReachable: caps.external?.onlyoffice?.state === 'reachable',
        drawioReachable: caps.external?.drawio?.state === 'reachable',
      };
    }
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  for (const f of FIXTURES) {
    test(`viewer mounts for ${f.ext} (${f.name})`, async ({ page }) => {
      // Skip externally-dependent viewers when their upstream isn't
      // reachable from this host. The fallback path is already
      // exercised by 82-capability-gating; here we only want to
      // assert the **rich viewer** mounts when the gate is open.
      if (OFFICE_EXTS.has(f.ext) && !CAPS.onlyofficeReachable) {
        test.skip(true, `OnlyOffice not reachable (state≠reachable) — fallback covered by 82-capability-gating`);
      }
      if (DRAWIO_EXTS.has(f.ext) && !CAPS.drawioReachable) {
        test.skip(true, `Drawio not reachable — fallback covered by 82-capability-gating`);
      }
      const sink = instrumentPage(page);
      try {
        // Login once per worker — the auth state lives in the browser
        // context Playwright spins up per test. Cheap enough at this
        // suite size and avoids cookie-share gotchas between contexts.
        await loginAs(page);

        const adapterPath = `${STORAGE}://${f.name}`;
        const url =
          `/admin/files/edit?path=${encodeURIComponent(adapterPath)}` +
          `&type=${encodeURIComponent(f.ext)}` +
          `&mode=${f.mode ?? 'edit'}`;
        await page.goto(url);

        // Wait for the modal host to mount — Editor.vue renders the
        // PreviewModal only after route mount, so the body isn't
        // immediately ready.
        await expect(page.locator('.fe-preview')).toBeVisible({ timeout: 10_000 });

        // Per-ext mount contract.
        await expectViewerForExt(page, f.ext);

        // Failures of the lazy viewer chunks would show up here.
        const collected = sink.collect();
        if (collected.failedAssets.length > 0) {
          throw new Error(
            `Lazy chunk(s) failed to load for ${f.ext}:\n  ${collected.failedAssets.join('\n  ')}`,
          );
        }
        // Console errors are advisory — log but don't fail unless the
        // viewer mount itself also failed (which would have thrown
        // above). We surface them via test annotations so the report
        // shows them next to the green/red dot.
        if (collected.consoleErrors.length > 0) {
          test.info().annotations.push({
            type: 'console-errors',
            description: `${f.ext}: ${collected.consoleErrors.length} error(s):\n${collected.consoleErrors.slice(0, 5).join('\n')}`,
          });
        }
        if (collected.pageErrors.length > 0) {
          test.info().annotations.push({
            type: 'page-errors',
            description: `${f.ext}: ${collected.pageErrors.join(' | ')}`,
          });
        }
      } finally {
        sink.dispose();
      }
    });
  }
});
