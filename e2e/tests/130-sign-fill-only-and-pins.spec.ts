/**
 * 128 — two more of the owner's 2026-09-23 findings, measured in a browser.
 *
 *   D. "her kişi için imza yerleştirmek zorunlu olmasın bazı kişiler sadece
 *      metin doldurabilir. Bu kuralı da kapatalım lütfen." — a participant
 *      may be asked only to FILL boxes in. The request sends, their screen
 *      asks them to fill rather than to sign, the round completes and is
 *      sealed, and the PDF names the act each party performed.
 *   E. "imzalama pinlerini sadece siz görürsünüz dedin ama o pinleri
 *      göstermiyorsun bir yerde … imzalar sayfasında ayrı bir sekmede
 *      tutuyor olmalıyız." — the Signatures page has a PINs section, the PIN
 *      read there is the one the link really wants, and a link with no PIN
 *      says so instead of showing a blank.
 *
 * ⚠ The PIN is read through the app's own tab, never through the platform's
 * "My shares" endpoint — that is the thing being measured. Only the TOKEN
 * (the address of the page) comes from the shares list.
 */
import { test, expect, type Page, type APIRequestContext } from '@playwright/test';
import { ADMIN_EMAIL, loginAs, apiLogin } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';
import { guardFixture, installThroughWizard, minimalPDF, resolveApp } from '../helpers/appPlugin';
import { removeApp } from '../helpers/surface';
import { drawSignature, next, press, SEND, SIGN, walk } from '../helpers/wizard';

const APP = resolveApp('sign');
const STORAGE = `e2e-fillonly-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const DOC = 'fill-only.pdf';
const QUALIFIED = `${STORAGE}://${DOC}`;
const OUTSIDER = 'Dolduran Kişi <dolduran128@example.test>';

/** The signing page's token, from the platform's own share list. */
async function signingToken(request: APIRequestContext): Promise<string> {
  const mine = (await (await request.get('/api/shares')).json()) as {
    items?: { share?: { token: string; page_id?: string }; node_path?: string }[];
  };
  const link = (mine.items ?? []).find((r) => r.share?.page_id === 'signer' && (r.node_path ?? '').endsWith(DOC));
  return link?.share?.token ?? '';
}

// The wizard walk lives in ONE place, e2e/helpers/wizard.ts. ⚠ This spec had
// its own copy of `next()` — the one 129 had BEFORE its race was fixed — and
// on the v0.43.0 release run a late press landed on Send and queued the
// request (the screenshot showed the product doing everything right). The fix
// had been made in 129's copy only. Now there is no copy to leave behind.


/** The PINs section of the Signatures page. */
async function openPins(page: Page) {
  await page.goto('/admin/app/sign/envelopes?section=pins');
  await expect(page.getByTestId('surface-section-pins')).toHaveAttribute('aria-selected', 'true');
}

/** One row of the PINs table, by the kind of link it is. */
function pinRow(page: Page, kind: 'signing' | 'receipt' | 'delivery') {
  return page.locator(`[data-testid^="surface-list-actions-"][data-testid*="|${kind}|"]`);
}

test.describe('App plugin: sign — a participant who only fills, and the PINs tab', () => {
  test.describe.configure({ mode: 'serial' });
  guardFixture(APP, test.skip);

  let pin = '';

  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT, {});
    await apiLogin(request);
    const up = await request.post('/api/files/manager?action=upload', {
      multipart: { path: `${STORAGE}://`, 'file[]': { name: DOC, mimeType: 'application/pdf', buffer: minimalPDF('fill only') } },
    });
    expect(up.ok(), `upload: ${up.status()}`).toBe(true);
    await removeApp(request, 'sign');
  });

  test.afterAll(async ({ request }) => {
    await apiLogin(request);
    await removeApp(request, 'sign');
    await dropStorageByName(request, STORAGE);
  });

  test('install', async ({ page }) => {
    await loginAs(page);
    await installThroughWizard(page, APP);
  });

  // ── D: the wizard sends a request one participant never signs ────────
  test('the review says who signs and who fills, and the request sends', async ({ page }) => {
    test.setTimeout(120_000);
    await page.setViewportSize({ width: 1440, height: 900 });
    await loginAs(page);
    await page.goto(`/admin/apps/sign/request?path=${encodeURIComponent(QUALIFIED)}`);
    await page.getByTestId('surface-people-input').fill(ADMIN_EMAIL.split('@')[0]);
    await page.getByTestId('surface-people-suggest').locator('li,button').first().click();
    await page.locator('textarea').first().fill(OUTSIDER);
    await walk(page, () => page.getByTestId('surface-pdf-add-signature').isVisible().catch(() => false), 3);
    // One signature for me, one text box for them — and nothing else.
    await page.getByTestId('surface-pdf-add-signature').click();
    await page.getByTestId('surface-pdf-add-text').click();
    const cards = page.locator('[data-testid^="surface-pdf-card-"][data-testid$="-editor"]');
    await expect(cards).toHaveCount(2);
    await cards.nth(0).locator('[data-testid$="-assignee-s1"]').click();
    await cards.nth(1).locator('[data-testid$="-assignee-s2"]').click();
    await next(page);

    // Place them both.
    await expect(page.locator('.fe-spdf__page canvas').first()).toBeVisible({ timeout: 30_000 });
    for (const at of [0.7, 0.5]) {
      await page.locator('[data-testid^="surface-pdf-pending-"]').first().click();
      const b = (await page.locator('.fe-spdf__page').first().boundingBox())!;
      await page.mouse.click(b.x + b.width * 0.3, b.y + b.height * at);
    }
    await expect(page.getByTestId('surface-pdf-all-placed')).toBeVisible();

    // To the review by the SEND BUTTON appearing, waiting for the step to
    // change between presses — not a fixed 300 ms guess per press. `walk`
    // stops on the review step without pressing, so it cannot send.
    const send = page.getByTestId(SEND);
    await walk(page, async () => (await send.count()) > 0);
    // ⭐ The review says what each person is asked for — and one of them is
    // never asked for a signature. It used to refuse to go any further.
    const table = page.locator('.fe-apppage__body');
    await expect(table).toContainText(/signs/);
    await expect(table).toContainText(/fills in 1 box/);
    await press(page, SEND);
    await expect(page.getByTestId('plugin-page-queued')).toBeVisible({ timeout: 20_000 });
  });

  // ── E: the PIN is where the requester was told it would be ───────────
  test('the PINs section holds the signing link’s PIN, and shows it once asked', async ({ page, request }) => {
    test.setTimeout(120_000);
    await loginAs(page);
    await apiLogin(request);
    await expect.poll(() => signingToken(request), { timeout: 90_000 }).not.toBe('');

    await expect
      .poll(
        async () => {
          await openPins(page);
          return pinRow(page, 'signing').count();
        },
        { timeout: 60_000, message: 'the signing link is listed' },
      )
      .toBeGreaterThan(0);

    // Nothing is shown until somebody asks — every read is an audited act.
    const body = page.locator('.fe-apppage__body');
    await expect(body).toContainText(/hidden/);
    await expect(body).toContainText(/written to filex's audit trail/);

    const menu = pinRow(page, 'signing').first();
    await menu.click();
    await page.getByText('Show PIN', { exact: true }).click();

    const shown = page.getByTestId('surface-form').locator('input').first();
    await expect(shown).toBeVisible({ timeout: 20_000 });
    pin = await shown.inputValue();
    expect(pin, 'the PIN is six digits').toMatch(/^\d{6}$/);
  });

  // ── D + E: that PIN opens their page, and their page says FILL ───────
  test('the PIN from the tab opens the page, which asks them to fill it in', async ({ page, request }) => {
    test.setTimeout(180_000);
    await apiLogin(request);
    const token = await signingToken(request);
    expect(token).not.toBe('');
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto(`/s/${token}`);
    // ⭐ Exactly the PIN the tab handed over — no other source was used.
    await page.getByTestId('public-page-pin-input').fill(pin);
    await page.getByTestId('public-page-pin-submit').click();

    /* Their screen is about filling in, not about signing — and this is the
       only place it can be measured. The heading and the body belong to the
       OPEN page (PublicShell draws them while `status === 'ready'`); once the
       round is accepted the shell swaps the whole card for "Received, thank
       you", so a check after the last button reads the thank-you screen and
       finds nothing of what was on the page before it. Measure the wording
       here, where it exists. */
    const body = page.locator('body');
    await expect(body).toContainText(/asks you to fill in/);
    await expect(body).not.toContainText(/asks you to sign/);
    await expect(page.getByTestId('public-page-title')).toHaveText(/Fill in/);
    await expect(page.getByTestId('public-page-title')).not.toHaveText(/Sign/);
    await page.getByTestId('public-page-action-next').click();
    await page.locator('input[type="text"], textarea').first().fill('Genel Müdür');
    await page.getByTestId('public-page-action-next').click();
    await expect(page.locator('.fe-spdf__page canvas').first()).toBeVisible({ timeout: 30_000 });
    // ...and the button they press says what they are doing.
    const done = page.getByTestId('public-page-action-sign');
    await expect(done).toHaveText(/Confirm/);
    await done.click();
    await expect(page.getByText(/thank you|teşekkür/i)).toBeVisible({ timeout: 30_000 });
  });

  test('the round completes, is sealed, and the PDF names each act', async ({ page, request }) => {
    test.setTimeout(180_000);
    // The inside signer (me) signs too.
    await loginAs(page);
    await page.setViewportSize({ width: 1366, height: 900 });
    const start = page.getByTestId('plugin-page-action-next');
    await expect
      .poll(
        async () => {
          await page.goto(`/admin/apps/sign/sign-fill?path=${encodeURIComponent(QUALIFIED)}`);
          await start.first().waitFor({ timeout: 5_000 }).catch(() => undefined);
          return start.count();
        },
        { timeout: 90_000, message: 'my own signing screen opens' },
      )
      .toBeGreaterThan(0);
    await expect(page.locator('body'), 'a signer is still asked to SIGN').toContainText(/asks you to sign/);
    await next(page);
    await drawSignature(page);
    await next(page);
    await press(page, SIGN);
    await expect(page.getByTestId('plugin-page-queued')).toBeVisible({ timeout: 30_000 });

    await apiLogin(request);
    const pdf = async () => {
      const r = await request.get(`/api/files/manager?action=download&path=${encodeURIComponent(QUALIFIED)}`);
      return r.ok() ? Buffer.from(await r.body()).toString('latin1') : '';
    };
    // Two parties and filex's seal: three signatures on a document only one
    // person was ever asked to sign.
    await expect
      .poll(async () => ((await pdf()).match(/\/Type \/Sig \/Filter/g) ?? []).length, {
        timeout: 150_000,
        message: 'both submissions and the seal land',
      })
      .toBe(3);

    const raw = await pdf();
    // ⚠⚠ The decision, measured: a fill-only participant DOES sign — an
    // invisible signature over their own revision, so the chain stays
    // unbroken and attributable — but what the signature SAYS is what they
    // did. Every PDF reader shows this line, and so does Verify.
    expect(raw, 'the filling in is named as one').toContain('Filled in at the request of');
    expect(raw, 'and the signature is still named as a signature').toContain('Signed at the request of');
  });

  test('the download link’s PIN is in the same tab once everybody is done', async ({ page }) => {
    test.setTimeout(120_000);
    await loginAs(page);
    await expect
      .poll(
        async () => {
          await openPins(page);
          return pinRow(page, 'delivery').count();
        },
        { timeout: 90_000, message: 'the download link is listed' },
      )
      .toBeGreaterThan(0);

    const menu = pinRow(page, 'delivery').first();
    await menu.click();
    await page.getByText('Show PIN', { exact: true }).click();
    const shown = page.getByTestId('surface-form').locator('input').first();
    await expect(shown).toBeVisible({ timeout: 20_000 });
    expect(await shown.inputValue(), 'the download link’s PIN').toMatch(/^\d{6}$/);

    // ...and the finished signing link no longer offers a PIN that would
    // open nothing.
    await expect(page.locator('.fe-apppage__body')).toContainText(/the link has ended/);
  });
});
