/**
 * 277 - the built-in viewer reads a PDF from a path filex's own pages may
 * frame (GitHub #124), measured against the real binary.
 *
 * filex's pages name the frames they may show of filex by path (security
 * review UI-11), and Chrome checks a PDF in an <object> against that
 * frame-src. The manager's preview URL was not on the list: Chrome showed the
 * viewer's "could not show" fallback instead of the document, and the
 * fallback's "Open in new tab" pointed at the DOWNLOAD. Measured 2026-10-20
 * with the policy filex sends: "Framing '…/api/files/manager?action=preview…'
 * violates the following Content Security Policy directive: frame-src …".
 *
 * Here: the page's frame-src names /api/files/pdf/ and the server says
 * `pdf_preview`; the viewer's <object> reads the PDF from that path, nothing
 * reports a frame-src violation, the fallback link is the same preview (not
 * the download), and the path answers the PDF inline.
 *
 * ⚠ Headless Chromium has no PDF viewer of its own, so whether the document
 * DRAWS is not asserted here; it was measured in headed Chrome for the fix.
 * The rule is packages/core/src/lib/pdfPreview.ts + useFileApi.previewUrl
 * (web/tests/api/pdfPreviewPath.test.ts) and the route is
 * backend/internal/api/handlers/manager_pdf.go (manager_pdf_test.go).
 */
import { test, expect } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, seedLocalStorage } from '../helpers/seed';
import { underBase } from '../helpers/base';
import { minimalPDF } from '../helpers/appPlugin';

const STORAGE = `e2e-pdf-frame-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const NAME = 'Quarterly report.pdf';

test.describe('A PDF in the built-in viewer comes from a path filex pages may frame', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
    const res = await request.post(underBase('/api/files/manager?action=upload'), {
      multipart: { path: `${STORAGE}://`, 'file[]': { name: NAME, mimeType: 'application/pdf', buffer: minimalPDF('frame') } },
    });
    expect(res.ok(), `upload: ${res.status()} ${await res.text()}`).toBeTruthy();
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('the pages name the PDF path in frame-src, and the server says it has it', async ({ request }) => {
    const page = await request.get(underBase('/admin/'));
    expect(page.headers()['content-security-policy'] ?? '').toMatch(/frame-src [^;]*\/api\/files\/pdf\//);
    const caps = await (await request.get(underBase('/api/files/capabilities'))).json();
    expect(caps.pdf_preview).toBe(true);
  });

  test('the viewer reads it from that path, nothing refuses it, and the fallback opens the preview', async ({ page }) => {
    const refusals: string[] = [];
    page.on('console', (m) => {
      if (/Content Security Policy/i.test(m.text())) refusals.push(m.text());
    });
    await page.addInitScript(() => {
      localStorage.setItem('filex.tourDone', '1');
      const seen: string[] = [];
      (window as unknown as { __cspSeen: string[] }).__cspSeen = seen;
      document.addEventListener('securitypolicyviolation', (e) => seen.push(`${e.violatedDirective} ${e.blockedURI}`));
    });
    await loginAs(page);
    // Same origin first (as 274 does): WebKit's cross-origin hop can lose the
    // session token written a moment before.
    await page.goto(underBase('/healthz'));
    await page.goto(
      underBase(`/admin/files/edit?path=${encodeURIComponent(`${STORAGE}://${NAME}`)}&type=pdf&mode=view&app=builtin`),
    );

    const object = page.locator('.fe-preview object[type="application/pdf"]');
    await expect(object).toHaveCount(1, { timeout: 15_000 });
    const data = await object.getAttribute('data');
    expect(data, 'the <object> has a source').toBeTruthy();
    const src = new URL(data!, page.url());
    expect(src.pathname).toBe(underBase(`/api/files/pdf/${encodeURIComponent(NAME)}`));
    expect(src.searchParams.get('path')).toBe(`${STORAGE}://${NAME}`);
    expect(src.searchParams.get('action'), 'never the manager verb').toBeNull();

    // The fallback inside the <object> opens that same preview in a new tab.
    const fallback = object.locator('a[target="_blank"]');
    await expect(fallback).toHaveCount(1);
    expect(await fallback.getAttribute('href')).toBe(data);

    const res = await page.request.get(src.toString());
    expect(res.status()).toBe(200);
    expect(res.headers()['content-type'] ?? '').toMatch(/^application\/pdf/);
    expect(res.headers()['content-disposition'] ?? '', 'a preview, not an attachment').not.toMatch(/attachment/i);
    expect((await res.body()).subarray(0, 5).toString()).toBe('%PDF-');

    await page.waitForLoadState('networkidle');
    const seen = await page.evaluate(() => (window as unknown as { __cspSeen: string[] }).__cspSeen);
    expect(seen.filter((v) => /^(frame-src|child-src|object-src)/.test(v)), 'no frame refusal reported to the page').toEqual([]);
    expect(refusals.filter((t) => /frame-src|child-src|object-src/.test(t)), 'no frame refusal in the console').toEqual([]);
  });
});
