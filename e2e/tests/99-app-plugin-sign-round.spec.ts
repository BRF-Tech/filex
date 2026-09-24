/**
 * 99-app-plugin-sign-round — the owner's second pass over the signing flow
 * (2026-09-21), measured where he met each thing: in a browser.
 *
 *   1. the Signatures screen is a page of its own, same tab, its sections in
 *      a menu, the section in the address (Back walks them);
 *   2. what is printed under a signature is chosen per box, and the signer
 *      sees it before signing;
 *   3. a signature box is drawn or typed, and only a typed one asks a face;
 *   4. a date / text / tick box is FILLED by somebody, not signed;
 *   6. placing and dragging does not redraw the document, and a dragged box
 *      stays under the pointer;
 *   7. boxes that belong to anyone are a row of the review;
 *   8. a required signature wears the `*`;
 *   9. the outside signer's page is the product's share page: no line frozen
 *      in the requester's language, the document offered as a shared file
 *      is, and the approve step shows the WHOLE page.
 *
 * The module is the real `filex-sign` build (e2e/helpers/app-locations.mjs);
 * without it the spec skips, unless FILEX_REQUIRE_WASM_FIXTURE=1.
 */
import { test, expect, type Page } from '@playwright/test';
import { loginAs, apiLogin } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';
import { guardFixture, installThroughWizard, minimalPDF, resolveApp } from '../helpers/appPlugin';
import { removeApp } from '../helpers/surface';
import { drawSignature, next, press, SEND, SIGN, walk } from '../helpers/wizard';

const APP = resolveApp('sign');
const STORAGE = `e2e-sign2-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const DOC = 'round.pdf';
const QUALIFIED = `${STORAGE}://${DOC}`;
const SIGNER = { email: 'round-signer@example.test', password: 'round-signer-pw-2026', name: 'Ayşe Round' };

// The wizard walk lives in ONE place, e2e/helpers/wizard.ts. ⚠ This spec's
// own `next()` was `.last().click()` with no wait at all — on a review step the
// footer's last button IS Send, so a press that landed a moment late sent the
// request. 130 failed exactly that way on the v0.43.0 release run; this copy
// had the same race and was lucky. Every press below is classified: an
// ADVANCE goes through next/advance/walk, which never act; an ACT (Send, Sign)
// goes through press(), by name.

/** The request wizard, from the first step to the review, measuring on the way. */
async function toTheReview(page: Page) {
  await page.goto(`/admin/apps/sign/request?path=${encodeURIComponent(QUALIFIED)}`);
  await page.getByTestId('surface-people-input').fill('round-signer');
  await page.getByTestId('surface-people-suggest').locator('li,button').first().click();
  await page.locator('textarea').first().fill('Dış Kişi <dis@example.test>');
  await next(page);
  await expect(page.getByText(/In what order|Hangi sırayla/)).toBeVisible();
  await next(page);
  await page.getByTestId('surface-pdf-add-signature').waitFor();
  for (const t of ['signature', 'signature', 'date']) await page.getByTestId(`surface-pdf-add-${t}`).click();
  const cards = page.locator('[data-testid^="surface-pdf-card-"][data-testid$="-editor"]');
  await expect(cards).toHaveCount(3);
  return cards;
}

test.describe('App plugin: sign — the owner’s second pass', () => {
  test.describe.configure({ mode: 'serial' });
  guardFixture(APP, test.skip);

  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT, { rbac_enabled: true });
    await apiLogin(request);
    const up = await request.post('/api/files/manager?action=upload', {
      multipart: { path: `${STORAGE}://`, 'file[]': { name: DOC, mimeType: 'application/pdf', buffer: minimalPDF('round') } },
    });
    expect(up.ok(), `upload: ${up.status()}`).toBe(true);
    await request.post('/api/admin/users', {
      data: { email: SIGNER.email, password: SIGNER.password, display_name: SIGNER.name, role: 'user' },
    });
    // ⚠ The list is a bare array (handlers/users.go); an envelope is
    // accepted too, so a later paginated answer does not silently find nobody.
    const raw = (await (await request.get('/api/admin/users')).json()) as
      | { id: number; email: string }[]
      | { users?: { id: number; email: string }[]; items?: { id: number; email: string }[] };
    const list = Array.isArray(raw) ? raw : (raw.users ?? raw.items ?? []);
    const me = list.find((u) => u.email === SIGNER.email);
    expect(me, 'the inside signer exists').toBeTruthy();
    const g = await request.post('/api/files/permissions', {
      data: { path: `${STORAGE}://`, user_id: me!.id, level: 'editor', is_dir: true },
    });
    expect(g.ok(), `grant: ${g.status()} ${await g.text()}`).toBe(true);
    await removeApp(request, 'sign');
  });

  test.afterAll(async ({ request }) => {
    await apiLogin(request);
    await removeApp(request, 'sign');
    await dropStorageByName(request, STORAGE);
  });

  test('install', async ({ page }) => {
    // Compiling the module is most of this; installThroughWizard adds its own
    // allowance (APP_INSTALL_ALLOWANCE_MS) to the test's timeout.
    await loginAs(page);
    await installThroughWizard(page, APP);
  });

  test('define, place, review — the requester’s side of items 2, 3, 4, 6 and 7', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 860 });
    await loginAs(page);
    const cards = await toTheReview(page);

    // Item 4 — a date is FILLED by somebody; a signature is signed.
    await expect(cards.nth(2).locator('[data-testid$="-assignee-label"]')).toHaveText(/Filled by|Dolduran/);
    await expect(cards.nth(0).locator('[data-testid$="-assignee-label"]')).toHaveText(/Signer|İmzacı/);
    // ...and a box that belongs to anyone SHOWS "Anyone" chosen.
    await expect(cards.nth(2).locator('[data-testid$="-assignee-*"]')).toHaveAttribute('aria-checked', 'true');

    // Item 3 — drawn by default, with no face to pick; typed asks one.
    await expect(cards.nth(1).locator('[data-testid$="-fonts"]')).toHaveCount(0);
    await cards.nth(1).locator('[data-testid$="-style-typed"]').click();
    await expect(cards.nth(1).locator('[data-testid$="-fonts"]')).toBeVisible();

    // Item 2 — the lines under a signature, previewed in the app's words.
    const preview = cards.nth(0).locator('[data-testid$="-lines-preview"]');
    await expect(preview).toContainText(/Signed \d{4}-\d{2}-\d{2}|İmza tarihi/);
    await expect(preview).not.toContainText(/IP/);
    await cards.nth(0).locator('[data-testid$="-line-ip"]').click();
    await expect(preview).toContainText(/IP address|IP adresi/);

    // Owners: the first signature to the inside signer, the second to the
    // outside one; the date stays anyone's.
    await cards.nth(0).locator('[data-testid$="-assignee-s1"]').click();
    await cards.nth(1).locator('[data-testid$="-assignee-s2"]').click();
    await next(page);

    // Item 6 — the place step. The document is drawn (it loaded on the
    // define step, where no page exists), and placing then dragging never
    // tears it down or lets a box leave the pointer.
    const canvas = page.locator('.fe-spdf__page canvas');
    await expect(canvas.first()).toBeVisible({ timeout: 30_000 });
    await page.evaluate(() => {
      const w = window as unknown as { __m: { torn: number; loading: number } };
      w.__m = { torn: 0, loading: 0 };
      new MutationObserver((muts) => {
        for (const m of muts) {
          for (const n of Array.from(m.removedNodes)) {
            if (n instanceof HTMLElement && (n.classList.contains('fe-spdf__page') || n.querySelector('.fe-spdf__page'))) w.__m.torn++;
          }
          for (const n of Array.from(m.addedNodes)) {
            if (n instanceof HTMLElement && /Loading|Yükleniyor/.test(n.textContent ?? '') && n.tagName === 'P') w.__m.loading++;
          }
        }
      }).observe(document.querySelector('.fe-spdf')!, { childList: true, subtree: true });
    });
    const pageBox = async () => (await page.locator('.fe-spdf__page').first().boundingBox())!;
    const before = await pageBox();
    const spots: Array<[number, number]> = [[0.3, 0.7], [0.7, 0.7], [0.5, 0.85]];
    for (const [fx, fy] of spots) {
      await page.locator('[data-testid^="surface-pdf-pending-"]').first().click();
      const b = await pageBox();
      await page.mouse.click(b.x + b.width * fx, b.y + b.height * fy);
    }
    await expect(page.getByTestId('surface-pdf-all-placed')).toBeVisible();
    // Long enough for every debounced `change` answer to have come back.
    await page.waitForTimeout(1500);
    const after = await pageBox();
    expect(Math.round(after.width), 'selecting a placed box must not re-fit the page').toBe(Math.round(before.width));

    // Drag each box slowly across the moment the previous drag's answer lands.
    const boxes = page.locator('.fe-spdf__field:not(.fe-spdf__field--ghost)');
    for (let i = 0; i < 2; i++) {
      const r = (await boxes.nth(i).boundingBox())!;
      const sx = r.x + r.width / 2;
      const sy = r.y + r.height / 2;
      await page.mouse.move(sx, sy);
      await page.mouse.down();
      for (let s = 1; s <= 30; s++) {
        await page.mouse.move(sx - 2 * s, sy - 2 * s);
        await page.waitForTimeout(20);
        const now = (await boxes.nth(i).boundingBox())!;
        const cx = now.x + now.width / 2;
        const cy = now.y + now.height / 2;
        expect(Math.abs(cx - (sx - 2 * s)), `box ${i} left the pointer at step ${s}`).toBeLessThan(4);
        expect(Math.abs(cy - (sy - 2 * s)), `box ${i} left the pointer at step ${s}`).toBeLessThan(4);
      }
      await page.mouse.up();
    }
    await page.waitForTimeout(800);
    const m = await page.evaluate(() => (window as unknown as { __m: { torn: number; loading: number } }).__m);
    expect(m.torn, 'the document was torn down and drawn again').toBe(0);
    expect(m.loading, '"Loading…" came back while boxes were placed and moved').toBe(0);

    // Item 7 — the review: every box in exactly one row, anyone's included.
    // To the review by the SEND BUTTON appearing, a step at a time — never
    // four blind presses: the fourth could land on the review, and there the
    // footer's last button is Send.
    await walk(page, async () => (await page.getByTestId(SEND).count()) > 0);
    const review = page.locator('[data-testid="surface-list"], table').first();
    await expect(review).toContainText(/Anyone|Herkes/);
    // ⚠ The row says what the person (or "Anyone") is ASKED FOR, in those
    // words — not arithmetic about them. Since 2026-09-23 a participant may
    // be asked for no signature at all, so "0 signatures · 1 to fill" would
    // be the commonest row on this table and the least readable one.
    await expect(review).toContainText(/fills in 1 box|1 kutu doldurur/);
    await expect(page.getByText(/Unassigned boxes|Atanmamış kutu/)).toHaveCount(0);
    await press(page, SEND);
    await expect(page.getByTestId('plugin-page-queued')).toBeVisible({ timeout: 20_000 });
  });

  test('the inside signer — items 8 and 2', async ({ page }) => {
    // ⚠ The install banner is NOT dismissed here, on purpose, and the window
    // is a laptop's: at 1366×768 the desktop-app chip hid this page's final
    // "Sign" button completely (2026-09-21, a tester). The chip now keeps
    // clear of anything a person has to press (web/src/lib/keepClear.ts);
    // this is the page it was measured on.
    await page.setViewportSize({ width: 1366, height: 768 });
    await page.goto('/admin/login');
    await page.getByLabel(/e-?mail|kullanıcı adı/i).fill(SIGNER.email);
    await page.getByLabel(/password|parola/i).fill(SIGNER.password);
    await page.getByRole('button', { name: 'Sign in', exact: true }).or(page.getByRole('button', { name: 'Oturum aç', exact: true })).first().click();
    await page.waitForURL(/\/drive\//);
    await page.goto(`/drive/apps/sign/sign-fill?path=${encodeURIComponent(QUALIFIED)}`);
    await next(page);
    // Item 8 — the signature is labelled like a field, star and all.
    const label = page.getByTestId('surface-signature-label').first();
    await expect(label).toBeVisible();
    await expect(label.locator('.fe-cfield__req')).toHaveText('*');
    // Item 3 — a drawn box: draw or a picture, never "type".
    await expect(page.getByTestId('surface-signature-mode-type')).toHaveCount(0);
    await drawSignature(page);
    const date = page.locator('.fe-surface__form input').first();
    await date.fill((await date.getAttribute('placeholder')) ?? '31.12.2000');
    await next(page);
    // Item 2 — what goes under the signature, before Sign, the IP included.
    const printed = page.getByText(/Printed under “|altına yazılacaklar:/).first();
    await expect(printed).toBeVisible({ timeout: 20_000 });
    await expect(printed).toContainText(/IP address: |IP adresi: /);
    // Nothing stands on the final button: the element at its centre is the
    // button itself.
    // By NAME: "the final button" is Sign. `.last()` would quietly measure
    // whichever button happened to be last.
    const sign = page.getByTestId(SIGN);
    await expect(sign).toBeEnabled();
    const box = (await sign.boundingBox())!;
    const onTop = await page.evaluate(([x, y]) => {
      const el = document.elementFromPoint(x, y);
      return el?.closest('[data-testid^="plugin-page-action-"]') ? 'the button' : (el?.className?.toString() ?? String(el));
    }, [box.x + box.width / 2, box.y + box.height / 2]);
    expect(onTop).toBe('the button');
  });

  test('the outside signer’s page is the product’s share page — item 9', async ({ page, request }) => {
    await apiLogin(request);
    const mine = (await (await request.get('/api/shares')).json()) as {
      items?: { share?: { id: number; token: string; page_id?: string }; node_path?: string }[];
    };
    const link = (mine.items ?? []).find((r) => r.share?.page_id === 'signer' && (r.node_path ?? '').endsWith(DOC));
    expect(link?.share, 'the request opened a signing link').toBeTruthy();
    const { pin } = (await (await request.get(`/api/shares/${link!.share!.id}/pin`)).json()) as { pin: string };

    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto(`/s/${link!.share!.token}`);
    await page.getByTestId('public-page-pin-input').fill(pin);
    await page.getByTestId('public-page-pin-submit').click();
    await expect(page.getByTestId('public-page-title')).toBeVisible();
    // No line frozen in the requester's language under the title.
    await expect(page.getByTestId('public-page-subject')).toHaveCount(0);
    // The document is offered the way a shared file is: name, size, Download.
    const files = page.getByTestId('public-page-files');
    await expect(files.locator('a.fe-btn')).toBeVisible();
    await expect(files).toContainText(DOC);

    await page.getByTestId('public-page-action-next').click();
    await page.getByTestId('surface-signature-typed').fill('Dış Kişi');
    const d = page.locator('.fe-surface__form input').first();
    if (await d.count()) await d.fill((await d.getAttribute('placeholder')) ?? '31.12.2000');
    await page.getByTestId('public-page-action-next').click();
    const pdfPage = page.locator('.fe-spdf__page').first();
    await expect(pdfPage.locator('canvas')).toBeVisible({ timeout: 30_000 });
    // The WHOLE page is on screen in its pane: no scrolling inside the card
    // to find your own box.
    const pane = (await page.locator('.fe-spdf__scroll').first().boundingBox())!;
    const pb = (await pdfPage.boundingBox())!;
    expect(pb.y + pb.height, 'the page runs past the bottom of its pane').toBeLessThanOrEqual(pane.y + pane.height + 1);
  });

  test('the Signatures page — item 1', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 860 });
    await loginAs(page);
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
    const row = page.locator('[data-testid^="sidenav-app-sign/"]').first();
    await expect(row).toBeVisible({ timeout: 20_000 });
    await row.click();
    // Same tab, its own page — not a dialog over the files.
    await page.waitForURL(/\/app\/sign\/envelopes/);
    expect(page.context().pages()).toHaveLength(1);
    await expect(page.getByTestId('plugin-view')).toHaveCount(0);
    const menu = page.getByTestId('surface-sections');
    await expect(menu).toBeVisible();
    await expect(page.getByTestId('surface-section-requested')).toBeVisible();

    await page.getByTestId('surface-section-requested').click();
    await expect(page).toHaveURL(/section=requested/);
    await expect(page.getByTestId('surface-section-requested')).toHaveAttribute('aria-selected', 'true');
    await expect(page.locator('.fe-apppage__body')).toContainText(DOC);
    // The due day (the outside signer's link runs out) is a DATE printed the
    // way the explorer prints dates, never the ISO day the app sends
    // (`format: "date"`, v0.43.0 wave 2).
    const docRow = page.locator('.fe-apppage__body .fe-list__row').filter({ hasText: DOC }).first();
    await expect(docRow).toContainText(/20\d\d/);
    await expect(docRow).not.toContainText(/20\d\d-\d\d-\d\d/);
    await page.getByTestId('surface-section-about').click();
    await expect(page).toHaveURL(/section=about/);
    // Back walks the sections.
    await page.goBack();
    await expect(page).toHaveURL(/section=requested/);
    await expect(page.getByTestId('surface-section-requested')).toHaveAttribute('aria-selected', 'true');
    // ...and a link lands on one, cold.
    await page.goto('/admin/app/sign/envelopes?section=about');
    await expect(page.getByTestId('surface-section-about')).toHaveAttribute('aria-selected', 'true');
  });

  // The owner's decision of 2026-09-21: a signing link stays in My shares,
  // marked as a signing request, opening the request's page, its revoke
  // saying that it cancels the request — and the signer's page views are
  // not downloads (the link above was opened; nothing was downloaded).
  test('My shares — a signing link is a signing request', async ({ page, request }) => {
    await apiLogin(request);
    const mine = (await (await request.get('/api/shares')).json()) as {
      items?: { share?: { id: number; page_id?: string; download_count: number; visit_count: number }; node_path?: string; app?: { view?: string; section?: string } }[];
    };
    const link = (mine.items ?? []).find((r) => r.share?.page_id === 'signer' && (r.node_path ?? '').endsWith(DOC));
    expect(link?.app, 'the row says what the link is').toBeTruthy();
    expect(link!.share!.visit_count, 'the signer opened the page').toBeGreaterThan(0);
    expect(link!.share!.download_count, 'opening a page downloads nothing').toBe(0);

    await page.setViewportSize({ width: 1280, height: 860 });
    await loginAs(page);
    await page.goto('/admin/my-shares');
    const id = link!.share!.id;
    const badge = page.getByTestId(`my-share-app-${id}`);
    await expect(badge).toHaveText(/Signing request|İmza isteği/);
    await page.getByTestId(`my-share-actions-${id}`).click();
    await page.locator(`[data-testid="my-share-actions-${id}-revoke"]`).last().click();
    await expect(page.getByTestId('my-share-revoke-warning')).toContainText(/cancels the whole signing request|imza isteğinin tamamını iptal eder/);
    await page.keyboard.press('Escape');
    await badge.click();
    await page.waitForURL(/\/app\/sign\/envelopes\?section=requested/);
    await expect(page.getByTestId('surface-section-requested')).toHaveAttribute('aria-selected', 'true');
  });
});
