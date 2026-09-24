/**
 * 127 — the owner's 2026-09-23 pass over the request wizard, measured where
 * he met each thing: in a browser.
 *
 *   A. "kutucuğa isim verirken isimde boşluk bırakamıyorum hiç izin vermiyor"
 *      — a box's NAME is free text in any script, spaces and all. The ASCII
 *      IDENTITY the PDF names the field by is derived from it and shown to
 *      nobody, so the two are measured apart: what is typed comes back from
 *      the request, and `/T` in the signed PDF is the slug while `/TU` is
 *      the name.
 *   B. "kutucukları oluşturduğumuz sayfada overflow hidden olduğu için …
 *      aşağıda kalan ekstra kutucuklara kaydırarak gidemiyoruz" — the step
 *      that NAMES the boxes shows no document, so the frame must not be
 *      shaped to the window: the list scrolls, at every height.
 *   C. "tarih biçimi ve ayraçlarını ayrı ayrı seçebilir olalım … örnek
 *      halini alta gösterelim" — a date box's layout is two choices, with
 *      today's date underneath in the shape they make.
 *
 * The module is the real `filex-sign` build; without it the spec skips
 * unless FILEX_REQUIRE_WASM_FIXTURE=1.
 */
import { test, expect, type Page } from '@playwright/test';
import { ADMIN_EMAIL, loginAs, apiLogin } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName, newAuthedRequest } from '../helpers/seed';
import { arabicPack, installLangPack, removeLangPack } from '../helpers/langPack';
import { guardFixture, installThroughWizard, minimalPDF, resolveApp } from '../helpers/appPlugin';
import { removeApp } from '../helpers/surface';
import { drawSignature, next, press, SEND, SIGN, walk } from '../helpers/wizard';

const APP = resolveApp('sign');
const STORAGE = `e2e-boxname-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const DOC = 'names.pdf';
const QUALIFIED = `${STORAGE}://${DOC}`;

/** The name the owner could not type: two words, Turkish letters, a space. */
const NAME = 'Yetkili İmzası';
/** ...and what the PDF must call the field instead — ASCII, derived. */
const IDENTITY = 'yetkili-imzasi';


/**
 * Scroll to something the way a PERSON does: the wheel.
 *
 * ⚠⚠ NOT `scrollIntoViewIfNeeded`. An `overflow: hidden` box can still be
 * scrolled BY SCRIPT (only `clip` refuses), so the convenience helper walks
 * straight past the very bug being measured — the owner could not get to the
 * lower boxes "kaydırarak", by scrolling. Measured with the wheel, a clipped
 * page never moves.
 */
async function wheelTo(page: Page, what: ReturnType<Page['locator']>, viewport: { width: number; height: number }) {
  const fits = async () => {
    const b = await what.boundingBox();
    return !!b && b.y >= 0 && b.y + b.height <= viewport.height;
  };
  const where = () => page.evaluate(() => Math.round(document.scrollingElement!.scrollTop));
  await page.mouse.move(viewport.width / 2, viewport.height / 2);
  let moved = -1;
  for (let i = 0; i < 40; i++) {
    if (await fits()) return;
    const from = await where();
    if (from === moved) return; // the page will not move any further
    moved = from;
    await page.mouse.wheel(0, 400);
    await page.waitForTimeout(120);
  }
}

/** The footer button that SENDS the request. Pressed once, deliberately. */
// The wizard walk — activeStep / next / walk / press — lives in ONE place,
// e2e/helpers/wizard.ts, shared by 99, 113, 129 and 130. It was a copy here
// first; the copy in 130 kept the race after this one was fixed.

/**
 * The wizard as far as the DEFINE step, with `count` boxes on it.
 *
 * ⚠ The step that asks for an ORDER is only there when there is an order to
 * choose — one signer, no question — so the walk is "press Next until the
 * box palette is on the screen", never a fixed number of presses.
 */
async function toDefine(page: Page, count: number) {
  await page.goto(`/admin/apps/sign/request?path=${encodeURIComponent(QUALIFIED)}`);
  await page.locator('textarea').first().fill('Dış Kişi <dis127@example.test>');
  await walk(page, () => page.getByTestId('surface-pdf-add-signature').isVisible().catch(() => false), 3);
  await page.getByTestId('surface-pdf-add-signature').waitFor();
  for (let i = 0; i < count; i++) await page.getByTestId('surface-pdf-add-signature').click();
  const cards = page.locator('[data-testid^="surface-pdf-card-"][data-testid$="-editor"]');
  await expect(cards).toHaveCount(count);
  return cards;
}

test.describe('App plugin: sign — naming a box, and a date’s layout', () => {
  test.describe.configure({ mode: 'serial' });
  guardFixture(APP, test.skip);

  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT, {});
    await apiLogin(request);
    const up = await request.post('/api/files/manager?action=upload', {
      multipart: { path: `${STORAGE}://`, 'file[]': { name: DOC, mimeType: 'application/pdf', buffer: minimalPDF('names') } },
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

  // ── A ────────────────────────────────────────────────────────────────
  test('a two-word name types, letter by letter, and stays typed', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await loginAs(page);
    const cards = await toDefine(page, 1);
    const input = cards.nth(0).locator('input[data-testid$="-label"]');

    // ⚠⚠ Typed with real key presses, not `fill`. The bug was the field
    // being rewritten BETWEEN keystrokes: `fill` sets the value in one go
    // and would never have seen it.
    await input.click();
    await input.press('Control+a');
    await input.press('Delete');
    await input.pressSequentially(NAME, { delay: 20 });
    await expect(input, 'the space between the two words survives typing').toHaveValue(NAME);

    // ...and it survives the plugin's own echo (the `change` event is
    // debounced at 300 ms, so this is the round trip that used to undo it).
    await page.waitForTimeout(1200);
    await expect(input, 'and the plugin hands back what was typed').toHaveValue(NAME);

    // The name is what the box is CALLED, everywhere it is drawn.
    await next(page);
    await expect(page.getByTestId('surface-pdf-place-hint').or(page.getByTestId('surface-pdf-idle-hint'))).toBeVisible();
    await expect(page.locator('[data-testid^="surface-pdf-pending-"]').first()).toContainText(NAME);
  });

  // ── C ────────────────────────────────────────────────────────────────
  test('a date box asks two questions and shows the date they make', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await loginAs(page);
    await toDefine(page, 0);
    await page.getByTestId('surface-pdf-add-date').click();
    const card = page.locator('[data-testid^="surface-pdf-card-"][data-testid$="-editor"]').nth(0);

    const orders = card.locator('[data-testid*="-order-"]');
    const seps = card.locator('[data-testid*="-sep-"]');
    await expect(orders, 'five arrangements').toHaveCount(5);
    await expect(seps, 'four separators').toHaveCount(4);
    await expect(card.locator('select'), 'buttons, never a dropdown (v3 §2)').toHaveCount(0);

    const shown = card.locator('[data-testid$="-date-example"]');
    const now = new Date();
    const dd = String(now.getDate()).padStart(2, '0');
    const mm = String(now.getMonth() + 1).padStart(2, '0');
    const yyyy = String(now.getFullYear());
    await expect(shown, 'a fresh box is day-first with a dot').toContainText(`${dd}.${mm}.${yyyy}`);

    // One answer at a time, and the example follows each.
    await card.locator('[data-testid$="-sep-/"]').click();
    await expect(shown).toContainText(`${dd}/${mm}/${yyyy}`);
    await card.locator('[data-testid$="-order-MMDDYY"]').click();
    await expect(shown, 'the separator already chosen is kept').toContainText(`${mm}/${dd}/${yyyy.slice(2)}`);
    await card.locator('[data-testid$="-order-YYYYMMDD"]').click();
    await card.locator('[data-testid$="-sep--"]').click();
    await expect(shown).toContainText(`${yyyy}-${mm}-${dd}`);
  });

  // ── C2: the captions, for a reader the APP does not speak ────────────
  /**
   * v0.43.0: everything filex drew on this step was Arabic and the three
   * captions the APP supplies were English — because `labelOf(app) || t(host)`
   * answers the app's English before filex's own words are ever asked for.
   * The pack has all three; this measures that the reader gets them.
   *
   * ⚠ With the real local pack when it is on the machine, and SKIPPED rather
   * than faked when the fixture pack has no words for these keys: a test that
   * cannot tell Arabic from English proves nothing.
   */
  test('the app’s captions come out in the reader’s language, not the app’s English', async ({ page, playwright, baseURL }) => {
    const pack = arabicPack();
    const wantOrder = pack.strings['plugin.pdf.date_order'];
    const wantSep = pack.strings['plugin.pdf.date_separator'];
    test.skip(!wantOrder || !wantSep, 'this Arabic pack does not translate the date captions');
    const admin = await newAuthedRequest(playwright, baseURL!);
    try {
      await removeLangPack(admin, pack);
      await installLangPack(admin, pack);

      await page.setViewportSize({ width: 1440, height: 900 });
      await loginAs(page);
      await page.goto('/admin/dashboard?settings=1');
      await expect(page.getByTestId('user-settings-dialog')).toBeVisible({ timeout: 15_000 });
      await page.getByTestId('user-settings-tab-preferences').click();
      await page.getByTestId('user-settings-locale-ar').click();
      await expect(page.locator('html')).toHaveAttribute('lang', 'ar');
      await page.keyboard.press('Escape');

      await toDefine(page, 0);
      await page.getByTestId('surface-pdf-add-date').click();
      const card = page.locator('[data-testid^="surface-pdf-card-"][data-testid$="-editor"]').nth(0);
      await expect(card).toBeVisible();

      const text = (await card.textContent()) ?? '';
      expect(text, 'filex’s own Arabic for the order caption').toContain(wantOrder);
      expect(text, 'filex’s own Arabic for the separator caption').toContain(wantSep);
      // The defect, spelled out: the app ships these in English only.
      expect(text, 'the app’s English beat the pack').not.toContain('Date order');
    } finally {
      await removeLangPack(admin, pack).catch(() => undefined);
      await admin.patch('/api/auth/profile', { data: { locale: 'en' } }).catch(() => undefined);
      await admin.dispose();
    }
  });

  // ── B ────────────────────────────────────────────────────────────────
  for (const size of [
    { name: '1440x900', width: 1440, height: 900 },
    { name: 'a laptop', width: 1280, height: 720 },
    { name: 'a phone', width: 390, height: 844 },
  ]) {
    test(`every box on the define step can be reached — ${size.name}`, async ({ page }) => {
      await page.setViewportSize({ width: size.width, height: size.height });
      await loginAs(page);
      const cards = await toDefine(page, 6);

      // ⚠⚠ THE measurement, and it is a measurement of REACHING the box, not
      // of a class name: six boxes are taller than any of these windows, and
      // with the document steps' window-high `overflow: hidden` frame on
      // this step the sixth one simply sat below the fold with no scroller
      // anywhere (the owner, 2026-09-23).
      const last = cards.nth(6 - 1);
      await wheelTo(page, last, size);
      await expect(last, 'the last box can be reached BY SCROLLING').toBeInViewport();
      const box = (await last.boundingBox())!;
      expect(box.y + box.height, 'the last box ends above the bottom edge').toBeLessThanOrEqual(size.height + 1);
      // ...and it can be USED down there: the name field takes a name.
      const input = last.locator('input[data-testid$="-label"]');
      await input.click();
      await input.pressSequentially('Son Kutu', { delay: 10 });
      await expect(input).toHaveValue('Son Kutu');
      // The "add a box" toolbar under the list is reachable too — the very
      // bottom of the page, which is what "the list ends here" means.
      const add = page.getByTestId('surface-pdf-add-signature');
      await wheelTo(page, add, size);
      await expect(add).toBeInViewport();

      // ...and WHY it works: the frame is an ordinary page here, not the
      // viewport-shaped one a document step needs.
      const frame = page.getByTestId('plugin-page');
      await expect(frame).not.toHaveClass(/is-doc/);
      const shape = await frame.evaluate((el) => ({
        overflowY: getComputedStyle(el).overflowY,
        scrollable: document.scrollingElement!.scrollHeight > window.innerHeight,
      }));
      expect(shape.overflowY, 'the page is not clipped').not.toBe('hidden');
      expect(shape.scrollable, 'six boxes are taller than the window').toBe(true);
    });
  }

  // ── A, all the way to the paper ──────────────────────────────────────
  test('the signed PDF names the field by its identity and titles it with the name', async ({ page, request }) => {
    // Signing really signs: a certificate, a PDF rewritten and a seal.
    test.setTimeout(180_000);
    await page.setViewportSize({ width: 1440, height: 900 });
    await loginAs(page);
    // ⚠ The signer is ME: the round has to be finished inside this test, and
    // only a person with an account here can sign in the app.
    await page.goto(`/admin/apps/sign/request?path=${encodeURIComponent(QUALIFIED)}`);
    await page.getByTestId('surface-people-input').fill(ADMIN_EMAIL.split('@')[0]);
    await page.getByTestId('surface-people-suggest').locator('li,button').first().click();
    await walk(page, () => page.getByTestId('surface-pdf-add-signature').isVisible().catch(() => false), 3);
    await page.getByTestId('surface-pdf-add-signature').click();
    const cards = page.locator('[data-testid^="surface-pdf-card-"][data-testid$="-editor"]');
    await expect(cards).toHaveCount(1);
    const input = cards.nth(0).locator('input[data-testid$="-label"]');
    await input.click();
    await input.press('Control+a');
    await input.press('Delete');
    await input.pressSequentially(NAME, { delay: 20 });
    await expect(input).toHaveValue(NAME);
    await next(page);

    // Place it, then walk the rest of the wizard.
    await expect(page.locator('.fe-spdf__page canvas').first()).toBeVisible({ timeout: 30_000 });
    await page.locator('[data-testid^="surface-pdf-pending-"]').first().click();
    const pageBox = (await page.locator('.fe-spdf__page').first().boundingBox())!;
    await page.mouse.click(pageBox.x + pageBox.width * 0.3, pageBox.y + pageBox.height * 0.75);
    await expect(page.getByTestId('surface-pdf-all-placed')).toBeVisible();
    // ⚠ Walk to the review by the SEND BUTTON, not by the heading: the
    // heading of the step just left is still on the screen for a frame, so
    // a loop that watches the words presses Next once more.
    const send = page.getByTestId(SEND);
    await walk(page, async () => (await send.count()) > 0);
    await expect(page.getByText(/Is this right\?|Doğru mu\?/)).toBeVisible();
    await expect(send, 'the wizard is ready to send').toBeEnabled();

    /* ⚠⚠ WHY `next()` REFUSES, measured on the screen rather than assumed.
       The review step's LAST footer action is Send itself, so a press that
       arrives here — one the caller aimed at "Next" from a screen that had
       already moved on — does not go forward, it SENDS. That is the failure
       reported on bc92f3a7, and it reads like a product fault because the
       wizard behaves perfectly correctly while doing it. */
    const lastAction = await page
      .locator('[data-testid^="plugin-page-action-"]')
      .last()
      .getAttribute('data-testid');
    expect(lastAction, 'the review step’s last footer action is Send').toBe(SEND);
    /* …so the stale press is made HERE, deliberately, where the race would
       have put it: it must be refused, and nothing may be sent by it. */
    expect(await next(page), 'a press that lands on the review step is refused').toBeNull();
    await expect(page.getByText(/Is this right\?|Doğru mu\?/), 'still on the review step').toBeVisible();
    await expect(page.getByTestId('plugin-page-queued'), 'and nothing was sent').toHaveCount(0);

    await press(page, SEND);

    await expect(page.getByTestId('plugin-page-queued')).toBeVisible({ timeout: 20_000 });
    await apiLogin(request);

    // ...and now sign it, as the person the box belongs to. ⚠ The request
    // is a JOB: the record it writes is not there the moment Send is
    // pressed, and a signing screen opened before it exists says there is
    // nothing to sign.
    const start = page.getByTestId('plugin-page-action-next');
    await expect
      .poll(
        async () => {
          await page.goto(`/admin/apps/sign/sign-fill?path=${encodeURIComponent(QUALIFIED)}`);
          // ⚠ The surface is fetched AFTER the navigation: counting straight
          // away counts the "Loading…" frame and never sees the screen.
          await start.first().waitFor({ timeout: 5_000 }).catch(() => undefined);
          return start.count();
        },
        { timeout: 90_000, message: 'the request lands and the signing screen opens' },
      )
      .toBeGreaterThan(0);
    // The box is asked for by the NAME that was typed, in the plain form the
    // signer fills before seeing the document.
    await expect(page.locator('body')).toContainText(NAME);
    await next(page); // Start
    await drawSignature(page);
    await next(page);
    await press(page, SIGN);

    await expect
      .poll(
        async () => {
          const r = await request.get(`/api/files/manager?action=download&path=${encodeURIComponent(QUALIFIED)}`);
          if (!r.ok()) return '';
          return Buffer.from(await r.body()).toString('latin1');
        },
        { timeout: 120_000, message: 'the signed document is written back' },
      )
      .toContain('/Sig');

    const raw = Buffer.from(
      await (await request.get(`/api/files/manager?action=download&path=${encodeURIComponent(QUALIFIED)}`)).body(),
    ).toString('latin1');
    // ⚠⚠ Name and identity are two different things, and this is where it
    // shows: the PDF's own field name is the ASCII slug, and the title a
    // reader sees is the name exactly as it was typed (UTF-16 in the PDF,
    // so it is matched by its bytes).
    expect(raw, 'the PDF field is named by the derived identity').toContain(`/T (${IDENTITY})`);
    // A PDF text string with a letter outside ASCII is written as UTF-16BE
    // hex with a byte-order mark — `<FEFF0059…>` — so the Turkish name is
    // matched in that shape, not as the bytes of the word.
    const hex = '<FEFF' + [...NAME].map((c) => c.codePointAt(0)!.toString(16).toUpperCase().padStart(4, '0')).join('') + '>';
    expect(raw, 'and titled with the name exactly as typed').toContain(hex);
  });
});
