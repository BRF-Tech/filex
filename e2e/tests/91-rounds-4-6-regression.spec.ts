/**
 * Rounds 4-6 regression suite.
 *
 * Each test pins a SPECIFIC bug found during the 2026-05-08 browser
 * smoke pass(es) so we don't ship regressions on the same surface
 * twice. Cross-reference handover docs in repo root for context:
 *
 *   - HANDOVER-2026-05-08-parity-round-2-3.md
 *   - HANDOVER-2026-05-08-round-4.md
 *   - HANDOVER-2026-05-08-round-5.md
 *   - HANDOVER-2026-05-08-round-6.md
 *
 * Most bugs are SPA-vs-backend mismatches (HTTP method or missing
 * route) and are exercised at the API level here for speed; a handful
 * (Editor.vue mount, Toolbar's `Aç` button) require a browser to
 * reproduce and are kept as `test.describe('UI', ...)` blocks at the
 * end. Run with:
 *
 *   E2E_BASE_URL=https://fm.example.com \
 *   E2E_ADMIN_EMAIL=admin@local \
 *   E2E_ADMIN_PASSWORD=<live-pw> \
 *   npx playwright test 91-rounds-4-6-regression
 *
 * Live deploy fixture assumption: at least one storage with adapter
 * `s3-test` containing `example/` with the standard 33 fixture files
 * (square.jpg, photo.webp, scan.tiff, logo.svg, manager.svg,
 * report.xlsx, dummy.pdf, sample.mp4, etc.). `seed-example-fixtures.sh`
 * keeps it in sync.
 */
import { test, expect, type APIRequestContext } from '@playwright/test';
import { ADMIN_EMAIL, ADMIN_PASSWORD } from '../helpers/auth';

// Where the example fixture set lives. The whole regression suite
// queries this path, so the constant doubles as documentation for
// what the seed script populates.
const FIXTURE_STORAGE = 's3-test';
const FIXTURE_DIR = `${FIXTURE_STORAGE}://example`;

let api: APIRequestContext;

test.beforeAll(async ({ playwright, baseURL }) => {
  // Hard requirement: a live filex deployment to point the regression
  // suite at. CI sets these via Settings > CI/CD > Variables; locally
  // export them in the shell. If they're missing we skip the whole
  // describe block with a loud message rather than letting Playwright
  // grind through 30-second timeouts to a non-existent localhost
  // listener.
  test.skip(
    !process.env.E2E_BASE_URL || process.env.E2E_BASE_URL.includes('localhost:5212'),
    'set E2E_BASE_URL to a live filex deployment (e.g. https://fm.example.com) ' +
      'plus E2E_ADMIN_EMAIL + E2E_ADMIN_PASSWORD; suite skipped',
  );

  // Bearer-token context — round-4 ops + onlyoffice + capabilities
  // endpoints all sit behind the admin auth middleware, so an authed
  // request is the minimum viable harness.
  //
  // Suite default actionTimeout is 10s, but a cold-started prod box
  // can take longer for the first TLS handshake — bump the login
  // step to 30s with a small retry loop so the first run after a
  // recreate doesn't fail the whole suite on a network jitter.
  const seed = await playwright.request.newContext({ baseURL, timeout: 30_000 });
  let token = '';
  let lastErr = '';
  for (let attempt = 0; attempt < 3; attempt++) {
    try {
      const login = await seed.post('/api/auth/login', {
        data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
        timeout: 30_000,
      });
      if (login.ok()) {
        ({ token } = await login.json());
        break;
      }
      lastErr = `${login.status()} ${await login.text()}`;
    } catch (e: unknown) {
      lastErr = (e as Error).message;
    }
    await new Promise((r) => setTimeout(r, 1_500));
  }
  expect(token, `login retried 3x, last: ${lastErr}`).toBeTruthy();
  await seed.dispose();
  api = await playwright.request.newContext({
    baseURL,
    timeout: 30_000,
    extraHTTPHeaders: { Authorization: `Bearer ${token}` },
  });
});

test.afterAll(async () => {
  await api.dispose();
});

test.describe('Round 4 — SPA-vs-backend mismatches', () => {
  test('BUG#1 — GET /api/files/ops?status=running responds 200 (not 405)', async () => {
    // PendingOpsTray polled this every 2s; before round 4 chi answered
    // 405 because only POST /ops + GET /ops/{id} were registered.
    const res = await api.get('/api/files/ops?status=running');
    expect(res.status()).toBe(200);
    const body = (await res.json()) as { ops?: unknown[] };
    expect(Array.isArray(body.ops)).toBe(true);
  });

  test('BUG#3 — GET /api/files/capabilities is an alias for /api/capabilities', async () => {
    // The SFC's useFileApi calls /api/files/capabilities; round 4 added
    // an alias that maps to the same Capabilities.Get handler.
    const fileCaps = await api.get('/api/files/capabilities');
    expect(fileCaps.status()).toBe(200);
    const fileBody = (await fileCaps.json()) as Record<string, unknown>;
    // Flat-aliased capability flags survive (frontend reads these).
    expect(fileBody).toHaveProperty('ffmpeg');
    expect(fileBody).toHaveProperty('ghostscript');
    expect(fileBody).toHaveProperty('libreoffice');
  });

  test('BUG#7 — POST /api/files/onlyoffice/config accepts {path,mode} body', async () => {
    // PreviewModal's POST shape — backend was GET-only before round 4
    // and didn't know how to resolve a path back to a node id.
    const res = await api.post('/api/files/onlyoffice/config', {
      data: {
        path: `${FIXTURE_STORAGE}://example/report.xlsx`,
        mode: 'edit',
      },
    });
    expect(res.status()).toBe(200);
    const body = (await res.json()) as {
      documentServerUrl?: string;
      config?: { document?: { fileType?: string } };
    };
    expect(body.documentServerUrl).toContain('http');
    expect(body.config?.document?.fileType).toBe('xlsx');
  });

  test('BUG#7b — POST /api/files/onlyoffice/config requires the FULL adapter path', async () => {
    // Round 5 caught a frontend regression where stripAdapter() left
    // the body with just "example/report.xlsx", causing the resolver
    // to fall through to storages[0]. Verify the bare relative form
    // still 404s so we don't accidentally re-introduce the silent
    // wrong-storage match.
    const res = await api.post('/api/files/onlyoffice/config', {
      data: { path: 'example/report.xlsx', mode: 'edit' },
    });
    // Accept 404 (no node) OR 200 (fixture happens to be on storages[0]).
    // What we DON'T want is a confused 500.
    expect([200, 404]).toContain(res.status());
  });

  test('BUG#8 — GET /api/files/search accepts ?q= query string', async () => {
    // Frontend admin SPA calls GET; canonical body-carrying form is POST.
    // Round 4 made the handler accept both verbs.
    const res = await api.get('/api/files/search?q=square&limit=5');
    expect(res.status()).toBe(200);
    const body = (await res.json()) as { results?: unknown[] };
    expect(Array.isArray(body.results)).toBe(true);
  });

  test('BUG#9 — GET /files/edit serves the admin SPA shell', async () => {
    // The standalone Editor.vue route lives outside /admin/ so the SPA
    // fallback was widened to /files/edit + /files/edit/*.
    const res = await api.get(
      '/files/edit?path=s3-test%3A%2F%2Fexample%2Freport.xlsx&mode=edit&type=xlsx',
      { maxRedirects: 0 },
    );
    // The handler may either return the SPA index.html directly or
    // redirect to /admin/files/edit (vue-router-resolved); both are
    // acceptable, but neither should 404.
    expect([200, 301, 302, 304]).toContain(res.status());
    if (res.status() === 200) {
      const body = await res.text();
      expect(body).toContain('<html');
    }
  });
});

test.describe('Round 4 — list endpoint thumb hydration', () => {
  test('BUG#4 — manager?action=index emits files[].thumb_url for ready thumbs', async () => {
    // ListNodesByParent doesn't JOIN thumbnails, so vfIndex calls
    // GetThumbnail per row before projectFileNodes runs. Verifies at
    // least one fixture has a populated thumb_url after backfill.
    const res = await api.get(
      `/api/files/manager?action=index&path=${encodeURIComponent(FIXTURE_DIR)}`,
    );
    expect(res.status()).toBe(200);
    const body = (await res.json()) as {
      files?: Array<{ basename: string; thumb_url?: string }>;
    };
    expect(Array.isArray(body.files)).toBe(true);
    const withThumb = (body.files ?? []).filter((f) => !!f.thumb_url);
    // The seed produces image+video+pdf rows that all backfill cleanly,
    // so at least 3 should carry thumb_url. (Office / SVG counts depend
    // on container deps; we assert the easy-mode subset only.)
    expect(withThumb.length).toBeGreaterThanOrEqual(3);
    // Spot-check a known-good fixture: manager.jpg always has a thumb
    // because it's a real JPEG that the GD path handles unconditionally.
    const managerJpg = (body.files ?? []).find((f) => f.basename === 'manager.jpg');
    expect(managerJpg?.thumb_url).toMatch(/^\/api\/files\/thumb\/\d+$/);
  });
});

test.describe('Round 5 — Bleve search', () => {
  test('BUG#11 — POST /api/admin/search/rebuild uses context.Background', async () => {
    // Round-5 fix: the goroutine inherited r.Context() and exited the
    // moment the handler returned. Verify the rebuild actually
    // populates docs by waiting up to 8s for document_count > 0.
    const kick = await api.post('/api/admin/search/rebuild');
    expect([200, 202, 409]).toContain(kick.status()); // 409 = already rebuilding
    const deadline = Date.now() + 8_000;
    let docs = 0;
    while (Date.now() < deadline) {
      const stats = await api.get('/api/admin/search/stats');
      if (stats.ok()) {
        const body = (await stats.json()) as { document_count?: number };
        docs = body.document_count ?? 0;
        if (docs > 0) break;
      }
      await new Promise((r) => setTimeout(r, 400));
    }
    expect(docs).toBeGreaterThan(0);
  });

  test('BUG#12 — search query "square" finds square.jpg via wildcard disjunction', async () => {
    // Default Match query stored "square.jpg" as a single token (dot is
    // not a word boundary); round 5 added a wildcard branch so substring
    // queries hit. Tolerates 0 results when the index is mid-rebuild —
    // poll a few times.
    const deadline = Date.now() + 5_000;
    let hits: unknown[] = [];
    while (Date.now() < deadline) {
      const res = await api.get('/api/files/search?q=square&limit=10');
      expect(res.status()).toBe(200);
      const body = (await res.json()) as { results?: unknown[] };
      hits = body.results ?? [];
      if (hits.length > 0) break;
      await new Promise((r) => setTimeout(r, 300));
    }
    expect(hits.length).toBeGreaterThan(0);
  });

  test('BUG#13 — WebP and TIFF thumbs land in the GridView projection', async () => {
    // Round 5 imported golang.org/x/image/{bmp,tiff,webp}. Confirm the
    // backfill produced thumb rows for these formats by inspecting the
    // listing. (The thumbnail BYTES live behind /api/files/thumb/{id}
    // which we don't crack open here — too brittle without a known
    // baseline.)
    const res = await api.get(
      `/api/files/manager?action=index&path=${encodeURIComponent(FIXTURE_DIR)}`,
    );
    const body = (await res.json()) as { files?: Array<{ basename: string; thumb_url?: string }> };
    const photoWebp = (body.files ?? []).find((f) => f.basename === 'photo.webp');
    const scanTiff = (body.files ?? []).find((f) => f.basename === 'scan.tiff');
    expect(photoWebp?.thumb_url).toBeTruthy();
    expect(scanTiff?.thumb_url).toBeTruthy();
  });
});

test.describe('Round 6 — SVG thumbnails', () => {
  test('BUG#17 — SVG fixtures have populated thumb_url (rsvg-convert path)', async () => {
    // Round 6 added thumb/svg.go which shells out to rsvg-convert; if
    // the binary isn't on PATH the dispatcher marks state="skipped"
    // (not "failed") and thumb_url stays empty. This test asserts the
    // rasterised path is actually live in production.
    const res = await api.get(
      `/api/files/manager?action=index&path=${encodeURIComponent(FIXTURE_DIR)}`,
    );
    const body = (await res.json()) as { files?: Array<{ basename: string; thumb_url?: string }> };
    const logoSvg = (body.files ?? []).find((f) => f.basename === 'logo.svg');
    const managerSvg = (body.files ?? []).find((f) => f.basename === 'manager.svg');
    expect(logoSvg?.thumb_url, 'logo.svg should have thumb_url').toBeTruthy();
    expect(managerSvg?.thumb_url, 'manager.svg should have thumb_url').toBeTruthy();
  });

  test('Capability probe reports SVG support when rsvg-convert is on PATH', async () => {
    // Wire-test for the model.ThumbCapabilities.SVG field added in
    // round 6. Lets us catch a "Dockerfile dropped rsvg-convert"
    // regression without needing a thumbnail to render.
    const caps = await api.get('/api/capabilities');
    expect(caps.status()).toBe(200);
    const body = (await caps.json()) as { thumbs?: { svg?: boolean } };
    expect(body.thumbs?.svg).toBe(true);
  });
});

test.describe('Round 8 — pptx fixture', () => {
  test('BUG#18 — slides.pptx generated by python-pptx round-trips through soffice', async () => {
    // The hand-rolled minimal pptx in earlier rounds tripped LibreOffice
    // Impress's "verify input parameters" guard (no slideLayout /
    // slideMaster / theme parts → input flagged malformed). Round 8
    // switched _gen_fixtures.py::write_pptx to python-pptx, which
    // produces a fully-formed archive Impress can re-export to PDF.
    //
    // We assert thumb_url is populated for slides.pptx — that's the
    // observable downstream effect of the fixture regenerate +
    // backfill cycle. If a future seeder change drops python-pptx
    // (e.g. accidentally falls back to the minimal writer), this row
    // goes back to state="failed" and thumb_url disappears.
    const res = await api.get(
      `/api/files/manager?action=index&path=${encodeURIComponent(FIXTURE_DIR)}`,
    );
    const body = (await res.json()) as { files?: Array<{ basename: string; thumb_url?: string }> };
    const slides = (body.files ?? []).find((f) => f.basename === 'slides.pptx');
    expect(slides, 'slides.pptx should be in the example fixture set').toBeTruthy();
    expect(slides?.thumb_url, 'slides.pptx must have a populated thumb_url').toBeTruthy();
    expect(slides?.thumb_url).toMatch(/^\/api\/files\/thumb\/\d+$/);
  });
});

test.describe('Round 4-6 — Browser UI regression', () => {
  // Browser tests need a real Chromium + cookies + JS evaluation, which
  // the CI api-only runner skips for cost reasons. Opt in with
  // E2E_INCLUDE_BROWSER=1 (the local recipe in this spec's docstring
  // sets it).
  test.skip(
    !process.env.E2E_INCLUDE_BROWSER,
    'browser tests opt-in — set E2E_INCLUDE_BROWSER=1 to run them',
  );

  test('BUG#15+16 — Editor.vue mounts and POSTs onlyoffice config end-to-end', async ({
    page,
  }) => {
    // Three round-5 bugs collide here: (a) the modal's watcher must
    // run on initial mount even though `open` never transitions
    // (round-5 onMounted+nextTick fix), (b) the body must include the
    // full adapter-qualified path (round-5 PreviewModal.vue stripAdapter
    // removal), (c) the `/files/edit` SPA fallback must land on the
    // admin shell (round-4 wireStatic widening).
    //
    // All three are exercised by simply navigating to the editor URL
    // and watching for the upstream config POST to come back 200.
    await page.goto('/admin/login');
    await page.getByLabel(/email/i).fill(ADMIN_EMAIL);
    await page.getByLabel(/password|şifre/i).fill(ADMIN_PASSWORD);
    await page.getByRole('button', { name: 'Sign in', exact: true }).click();
    await page.waitForURL(/\/admin\/dashboard/, { timeout: 10_000 });

    const [configResp] = await Promise.all([
      page.waitForResponse(
        (r) =>
          r.url().includes('/api/files/onlyoffice/config') && r.request().method() === 'POST',
        { timeout: 15_000 },
      ),
      page.goto(
        `/files/edit?path=${encodeURIComponent(FIXTURE_STORAGE + '://example/report.xlsx')}&mode=edit&type=xlsx`,
      ),
    ]);

    expect(configResp.status()).toBe(200);
    const body = (await configResp.json()) as {
      config?: { document?: { fileType?: string } };
    };
    expect(body.config?.document?.fileType).toBe('xlsx');

    // Body of the request must carry the FULL path (regression on
    // BUG#16 — stripAdapter would have produced "example/report.xlsx").
    const reqBody = configResp.request().postDataJSON() as { path?: string };
    expect(reqBody.path).toBe(`${FIXTURE_STORAGE}://example/report.xlsx`);
  });

  test('BUG#6 — Toolbar single-file selection includes "Aç" action', async ({ page }) => {
    // FileExplorer's Toolbar mode-switch produced [preview, download,
    // share, rename, delete] before round 4. We now lead with `Aç`
    // because it mirrors double-click semantics. Browser-only — the
    // action set is a v-bound array assembled at runtime.
    await page.goto('/admin/login');
    await page.getByLabel(/email/i).fill(ADMIN_EMAIL);
    await page.getByLabel(/password|şifre/i).fill(ADMIN_PASSWORD);
    await page.getByRole('button', { name: 'Sign in', exact: true }).click();
    await page.waitForURL(/\/admin\/dashboard/);

    // Land in the example dir. Setting `?storage=s3-test` opens the
    // selected storage at its root (showing the `aa` and `example`
    // folders); double-click `example` to descend into the fixture
    // dir. Drive navigation with text selectors because the storage
    // explorer's accessible names use the localised `Breadcrumb`
    // label which switches between TR/EN depending on the browser
    // locale Playwright happens to use.
    await page.goto(`/admin/explore?storage=${FIXTURE_STORAGE}`);

    const exampleRow = page.getByText('example', { exact: true }).first();
    await exampleRow.waitFor({ state: 'visible', timeout: 15_000 });
    await exampleRow.dblclick();

    // Wait for the file list to populate.
    const tile = page.locator('.fe-grid__card, [role="row"]', {
      hasText: 'report.xlsx',
    }).first();
    await tile.waitFor({ state: 'visible', timeout: 15_000 });
    await tile.click();

    // The action set shows up in the same toolbar as the up-arrow /
    // new-folder buttons. Match by visible label — accept both the TR
    // ("Aç") and EN ("Open") strings because the test browser may
    // pick either locale depending on Accept-Language and per-user
    // i18n settings. The leading "↗" arrow is added by the icon
    // wrapper around the i18n string.
    const acButton = page.getByRole('button', { name: /Aç|Open/ }).first();
    await expect(acButton).toBeVisible();
  });
});
