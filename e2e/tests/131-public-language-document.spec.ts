/**
 * 131 — a stranger switches the language on a public page, and the DOCUMENT
 * follows: `<html lang>` and `<html dir>`, not only the wrapper.
 *
 * ⚠⚠ Why this is not covered by looking at the page. The shell puts `dir` on
 * its own wrapper, so the card, the buttons and the text all turn around and
 * every eye test passes. `<html>` kept what the SERVER rendered: a reader who
 * pressed العربية on a share link was reading Arabic inside
 * `<html lang="en" dir="ltr">` (v0.43.0). Nothing on screen says so — and
 * everything that does not look at pixels is misled: a screen reader
 * announces the language it is told, hyphenation, quotation marks and
 * `:lang()` rules follow `lang`, and the root's `dir` is what a print
 * stylesheet and any portal outside the wrapper inherit.
 *
 * `lang` alone was set, in two places, and only when the picker was the thing
 * that changed it; `dir` was never set at all. Both now follow one watcher on
 * the page's language (PublicLinkPage).
 *
 * ⚠ Arabic is filex's right-to-left test fixture: not published, not
 * advertised, in no screenshot (Burak, 2026-09-19). It is here because it is
 * the only language whose direction differs.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { newAuthedRequest, seedLocalStorage, dropStorageByName } from '../helpers/seed';
import { arabicPack, installLangPack, removeLangPack } from '../helpers/langPack';

const STORAGE = `e2e-publang-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const FILE = 'invoice.txt';

const PACK = arabicPack();

let api: APIRequestContext;
let shareUrl = '';

/** What the document element says about the language it is drawing. */
async function documentSays(page: Page) {
  return page.evaluate(() => ({
    lang: document.documentElement.getAttribute('lang') ?? '',
    dir: document.documentElement.getAttribute('dir') ?? '',
    wrapper: document.querySelector('[data-testid="public-page"]')?.getAttribute('dir') ?? '',
  }));
}

test.describe('A public page in another language', () => {
  test.describe.configure({ mode: 'serial' });

  test.beforeAll(async ({ playwright, baseURL }) => {
    api = await newAuthedRequest(playwright, baseURL!);
    await dropStorageByName(api, STORAGE);
    await seedLocalStorage(api, STORAGE, MOUNT);
    const up = await api.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORAGE}://`,
        'file[]': { name: FILE, mimeType: 'text/plain', buffer: Buffer.from('total: 42\n') },
      },
    });
    expect(up.ok(), `upload: ${up.status()}`).toBe(true);
    const share = await api.post('/api/files/share', { data: { path: `${STORAGE}://${FILE}`, kind: 'file' } });
    expect(share.ok(), `share: ${share.status()} ${await share.text()}`).toBe(true);
    const body = await share.json();
    const token = String(body.share?.token ?? body.token ?? '');
    expect(token, `no token in ${JSON.stringify(body).slice(0, 200)}`).not.toBe('');
    shareUrl = `/s/${token}`;
    await removeLangPack(api, PACK);
    await installLangPack(api, PACK);
  });

  test.afterAll(async () => {
    if (!api) return;
    await removeLangPack(api, PACK);
    await dropStorageByName(api, STORAGE);
    await api.dispose();
  });

  test('the document element follows the language the stranger picks', async ({ page }) => {
    // A browser that has never met this instance — no session, no stored
    // preference: the page a recipient actually opens.
    await page.goto(shareUrl);
    await expect(page.getByTestId('public-page')).toBeVisible({ timeout: 20_000 });

    const before = await documentSays(page);
    expect(before.dir, 'a left-to-right page starts left to right').not.toBe('rtl');

    const arabic = page.getByTestId('public-language-ar');
    await expect(arabic, 'the installed pack put Arabic on the picker').toBeVisible({ timeout: 20_000 });
    await arabic.click();

    // The wrapper turning around is the half that always worked; the document
    // element is the half that did not.
    await expect
      .poll(async () => (await documentSays(page)).lang, { timeout: 10_000 })
      .toBe('ar');
    const after = await documentSays(page);
    /* ⚠ The SERVER is the one that calls a language right to left, and the
       page derives `dir` from that list (lib/direction `rtlCodes`). Reading
       the branding answer here separates "the server never said so" from
       "the page did not follow", which is the difference between a pack
       problem and a product one. */
    const branding = await (await page.request.get('/api/public/branding')).json();
    const saysRtl = (branding.ui_locales ?? []).some((r: { code: string; rtl?: boolean }) => r.code === 'ar' && r.rtl === true);
    expect(saysRtl, `the branding answer does not flag Arabic: ${JSON.stringify(branding.ui_locales ?? [])}`).toBe(true);
    expect(after.dir, `<html dir> did not follow: ${JSON.stringify(after)}`).toBe('rtl');
    expect(after.wrapper, 'the wrapper and the document must agree').toBe('rtl');
  });

  test('and follows it back', async ({ page }) => {
    /* The visitor's choice lives in their browser and nowhere else (there is
       no account behind a link), so this is a browser that has been here
       before — and the document element has to say Arabic on the FIRST
       paint, not only after a click. The watcher is `immediate` for exactly
       that, and this is the case that would catch it if it were not. */
    await page.addInitScript(() => localStorage.setItem('filex.locale', 'ar'));
    await page.goto(shareUrl);
    await expect(page.getByTestId('public-page')).toBeVisible({ timeout: 20_000 });
    await expect.poll(async () => (await documentSays(page)).lang, { timeout: 10_000 }).toBe('ar');
    expect((await documentSays(page)).dir).toBe('rtl');

    await page.getByTestId('public-language-en').click();
    await expect.poll(async () => (await documentSays(page)).lang, { timeout: 10_000 }).toBe('en');
    const back = await documentSays(page);
    expect(back.dir, 'it has to turn back too').toBe('ltr');
    expect(back.wrapper).toBe('ltr');
  });
});
