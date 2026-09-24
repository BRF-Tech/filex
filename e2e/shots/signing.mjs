// The signing round, end to end — the flagship of Apps, and the scenes the
// rest of this release is easiest to see in: a person's own links, the bell
// that tells them what moved, and the admin table that holds every link.
//
//   node e2e/shots/signing.mjs        (from the repo root; `pnpm shots` runs it)
//
// The story: Dana (an ordinary account) asks Alex (another account on this
// filex) and Sam (an outside partner with no account) to sign a service
// agreement. Writes docs/screenshots/<release>/signing/:
//
//   sign-define-1440.png           "The boxes": every box named and given to a
//                                  signer, and no document on screen yet
//   sign-place-1440.png            "Place them": the document, two boxes down,
//                                  the third in hand with its hint
//   sign-status-1440.png           Dana's explorer: the document frozen while
//                                  it is out, its Signatures panel open
//   sign-outside-pin-1440.png      Sam's link: the branded PIN gate
//   sign-outside-fill-1440.png     …and what it opens: Sam's own boxes, a typed
//                                  signature
//   bell-badge-1440.png            Dana's bell, the unread count ON it, open
//   notifications-list-1440.png    "View all": the full list, inside the explorer
//   sign-pins-1440.png             The app's own Signatures screen, PINs: every
//                                  link this request opened, each PIN behind
//                                  "Show PIN" until it is asked for
//   my-shares-1440.png             My shares (Dana, not an administrator): the
//                                  signing link's Actions menu, "Copy PIN" in it
//   admin-table-actions-1440.png   Admin → Shares: the same link among the others,
//                                  its one pinned Actions menu open
//
// Every step waits for the thing it is about to photograph; a scene that
// cannot reach its target FAILS, because a skipped shot keeps the old picture.
//
// Environment: FILEX_BIN, FILEX_SIGN_APP_DIR, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { writeFileSync, mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { chromium } from '@playwright/test';
import { syncAndWait } from './fixtures.mjs';
import {
  AGREEMENT,
  addLocalStorage,
  bootInstance,
  client,
  documentPDF,
  findApp,
  installApp,
  log,
  newContext,
  shot,
  signIn,
  sleep,
} from './scene.mjs';

const SET = 'signing';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
const DANA = { email: 'dana@example.com', password: 'dana-shots-pw', display_name: 'Dana Reyes' };
const ALEX = { email: 'alex@example.com', password: 'alex-shots-pw', display_name: 'Alex Morgan' };
const SAM = 'Sam Carter <sam@partner.example>';

const STORAGE = 'Contracts';
const DOC = 'service-agreement.pdf';
const DOC_PATH = `${STORAGE}://${DOC}`;

/** Other documents in the folder, so the listing reads like somebody's work. */
const NEIGHBOURS = {
  'brand-guidelines.pdf': [[20, 'Brand guidelines'], [11, 'Northwind Studio · version 3']],
  'statement-of-work.pdf': [[20, 'Statement of work'], [11, 'Annex A to agreement 2026-114']],
  'kickoff-notes.md': '# Kick-off notes\n\n- Milestone 1 review on 15 October\n- Logo directions: three\n',
};

/** Waits for the plugin job this person queued to finish, and fails loudly if it did not. */
async function waitForJob(api, action, { timeoutMs = 120_000 } = {}) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const { ops = [] } = await api.json('/api/files/ops');
    const op = ops.find((o) => o && o.kind === 'plugin-action' && o.action === action);
    if (op && ['ok', 'failed', 'partial', 'cancelled'].includes(op.status)) {
      if (op.status !== 'ok') throw new Error(`the ${action} job ended ${op.status}: ${op.error ?? ''}`);
      return op;
    }
    await sleep(500);
  }
  throw new Error(`the ${action} job did not finish within ${timeoutMs / 1000}s`);
}

async function main() {
  const sign = findApp('sign');
  log(`sign ${sign.manifest.version} from ${sign.wasm}`);

  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' } });
  const browser = await chromium.launch();
  try {
    // ── the world ─────────────────────────────────────────────────────────
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Demo' });
    for (const u of [DANA, ALEX]) {
      await admin.post('/api/admin/users', { ...u, role: 'user' });
    }

    const root = join(inst.files, STORAGE);
    mkdirSync(root, { recursive: true });
    writeFileSync(join(root, DOC), documentPDF(AGREEMENT));
    for (const [name, body] of Object.entries(NEIGHBOURS)) {
      writeFileSync(join(root, name), typeof body === 'string' ? body : documentPDF(body));
    }
    const storage = await addLocalStorage(admin, STORAGE, root);
    // Catalogued before anything is photographed, so the explorer lists the
    // whole folder from the catalogue. (A signing request on a file the
    // catalogue has not seen yet no longer needs this — share_create records
    // it — but the pictures want every neighbour listed, not only that one.)
    await syncAndWait((_t, p, init) => admin.call(p, init), null, storage.id);
    await installApp(admin, sign);

    // Two ordinary links beside the signing one, so the admin table shows an
    // app's link as what it is: one more row.
    await admin.post('/api/files/share', { path: `${STORAGE}://brand-guidelines.pdf`, kind: 'file', pin: '2468', max_downloads: 5 });
    const dana = client(inst.url);
    await dana.login(DANA.email, DANA.password);
    await dana.post('/api/files/share', { path: `${STORAGE}://statement-of-work.pdf`, kind: 'file' });

    // ── Dana asks for signatures ─────────────────────────────────────────
    const dctx = await newContext(browser);
    const page = await dctx.newPage();
    await signIn(page, inst.url, DANA);
    await page.goto(`${inst.url}/drive/apps/sign/request?path=${encodeURIComponent(DOC_PATH)}`);
    const next = page.getByTestId('plugin-page-action-next');

    // 1. Signers: one from this filex, one from outside.
    await page.getByTestId('surface-people-input').fill('Alex');
    await page.locator('[data-testid="surface-people-suggest"] li').first().click();
    await page.locator('[data-testid="surface-form"] textarea').fill(SAM);
    await next.click();
    // 2. Order: everybody at once (the default).
    await page.getByText('In what order?').waitFor();
    await next.click();

    // 3. The boxes — defined, not placed.
    await page.getByTestId('surface-pdf-add-signature').waitFor();
    // ⚠⚠ Drawn or TYPED is the REQUESTER's choice, made here, and the face
    // with it (filex-sign: a typed box travels to the signer as
    // `modes: ["type"]` with one font). The signer's pad therefore offers a
    // mode strip only when the box left them a choice — asking it for a
    // "type" button on a drawn box is a 30-second timeout, which is how this
    // script died on the v0.43.0 run. Sam's box is typed, so the picture of
    // the outside signer shows a name in the face Dana picked.
    const boxes = [
      ['signature', 'Client signature', 's1', ''],
      ['signature', 'Provider signature', 's2', 'dancing-script'],
      ['date', 'Date signed', 's2', ''],
    ];
    for (const [type, label, signer, face] of boxes) {
      await page.getByTestId(`surface-pdf-add-${type}`).click();
      const editor = page.locator('[data-testid^="surface-pdf-card-"][data-testid$="-editor"]').last();
      const prefix = (await editor.getAttribute('data-testid')).replace(/-editor$/, '');
      await page.getByTestId(`${prefix}-label`).fill(label);
      await page.getByTestId(`${prefix}-assignee-${signer}`).click();
      if (face) {
        await page.getByTestId(`${prefix}-style-typed`).click();
        await page.getByTestId(`${prefix}-font-${face}`).click();
      }
      const required = page.getByTestId(`${prefix}-required`);
      if (!(await required.isChecked())) await required.check();
    }
    await page.mouse.move(0, 0);
    await sleep(400);
    /* ⚠ The step SCROLLS now (v0.43.0: a date box asks two questions, so a
       third card is taller than it was). At 900px the picture came back with
       the last card sliced through by the sticky footer — a picture of a
       wizard cut in half is not a picture of the wizard. Grow the window
       until the row BELOW the last card is inside it, which is what a person
       does by making the window taller, and refuse the shot if it cannot be
       done rather than photographing the slice. */
    const define = page.viewportSize();
    const addRow = page.getByTestId('surface-pdf-add-signature');
    for (let h = define.height; h <= 1400; h += 50) {
      await page.setViewportSize({ width: define.width, height: h });
      await sleep(200);
      const b = await addRow.boundingBox();
      if (b && b.y + b.height + 56 <= h) break;
    }
    const fitted = await addRow.boundingBox();
    const tall = page.viewportSize().height;
    if (!fitted || fitted.y + fitted.height > tall - 8) {
      throw new Error(`the boxes step does not fit the window: "Add a box" ends at ${fitted ? Math.round(fitted.y + fitted.height) : 'nowhere'} of ${tall}`);
    }
    log(`sign-define framed: ${define.width}×${tall}, "Add a box" ends at ${Math.round(fitted.y + fitted.height)}`);
    await shot(page, SET, 'sign-define-1440.png');
    await page.setViewportSize(define);
    await next.click();

    // 4. Place them — two boxes down, the third picked up and waiting for a tap.
    const canvas = page.locator('.fe-spdf canvas').first();
    await canvas.waitFor({ timeout: 30_000 });
    for (let i = 0; i < 60 && ((await canvas.boundingBox())?.width ?? 0) < 200; i++) await sleep(250);
    const drop = async (pending, [x0, y0, x1, y1]) => {
      await page.locator(`[data-testid^="surface-pdf-pending-"]`, { hasText: pending }).first().click();
      // ⚠ Picking a box puts its hint ABOVE the page, which moves the page
      // down; a box measured before that lands somewhere else entirely.
      await page.getByTestId('surface-pdf-place-hint').waitFor();
      await sleep(250);
      const b = await canvas.boundingBox();
      await page.mouse.move(b.x + b.width * x0, b.y + b.height * y0);
      await page.mouse.down();
      await page.mouse.move(b.x + b.width * x1, b.y + b.height * y1, { steps: 8 });
      await page.mouse.up();
      await sleep(250);
    };
    // Just above the two signature lines of the agreement's last block.
    await drop('Client signature', [0.13, 0.585, 0.42, 0.64]);
    await drop('Provider signature', [0.49, 0.585, 0.78, 0.64]);
    await page.locator('[data-testid^="surface-pdf-pending-"]', { hasText: 'Date signed' }).first().click();
    await page.getByTestId('surface-pdf-place-hint').waitFor();
    await page.mouse.move(0, 0);
    await sleep(400);
    await shot(page, SET, 'sign-place-1440.png');
    // The date goes just under the provider's signature box — measured from
    // the box as drawn, because the page is scaled to the window.
    const provider = await page.locator('[data-testid^="surface-pdf-field-"]', { hasText: 'Provider signature' }).first().boundingBox();
    await page.mouse.click(provider.x + 24, provider.y + provider.height + 22);
    try {
      await page.getByTestId('surface-pdf-all-placed').waitFor({ timeout: 10_000 });
    } catch {
      const left = await page.$$eval('[data-testid^="surface-pdf-pending-"]', (els) => els.map((e) => e.textContent.trim()));
      throw new Error(`the last box did not land under ${JSON.stringify(provider)}: still pending ${JSON.stringify(left)}`);
    }
    await next.click();

    // 5–7. Time, behaviour, the result.
    await page.getByText('How long do they have?').waitFor();
    await page.locator('#fe-cf-remind_every').fill('3');
    await next.click();
    await page.getByText('How should the request behave?').waitFor();
    await page.getByLabel(/Freeze the file while signatures are collected/).check();
    await page.locator('[data-testid="surface-form"] textarea').fill('Please sign by Friday — the kick-off is on Monday. Thank you!');
    await next.click();
    await page.getByText('What happens when everybody has signed?').waitFor();
    await page.getByLabel(/Write an audit trail PDF/).check();
    await next.click();
    // 8. Review → Send.
    await page.getByText('Is this right?').waitFor();
    await page.getByTestId('plugin-page-action-send').click();
    await waitForJob(dana, 'request');

    // The requester's view of it: the file frozen, its Signatures panel open.
    await page.goto(`${inst.url}/drive/explore?storage=${STORAGE}`);
    const row = page.locator(`[data-fe-path="${DOC_PATH}"]`).first();
    await row.waitFor({ timeout: 25_000 });
    await row.locator('.fe-list__check').first().click();
    await page.getByTestId('tabs-inspector').click();
    const section = page.getByTestId('inspector-plugin-sign-status');
    await section.waitFor({ timeout: 20_000 });
    // The app's section is folded until somebody opens it (it is only asked
    // for then); opened, it says who has signed and who is still to.
    await page.getByTestId('inspector-plugin-toggle-sign-status').click();
    await section.locator('.fe-pinsp__body').getByText(/signed/).first().waitFor({ timeout: 20_000 });
    // ⚠ The panel scrolls under its OWN sticky tabs, so scrolling the
    // section into view drove the panel to its bottom and BEHEADED the lock
    // banner: the picture opened mid-sentence, with the "Held by an app"
    // title and its padlock out of frame (v0.43.0). The banner is the one
    // thing this scene is about — the file is frozen — so instead: a taller
    // window (the set already has 1000-tall pictures), the panel back at its
    // top, and a measurement that REFUSES the shot if either end is cut.
    const view900 = page.viewportSize();
    await page.setViewportSize({ width: view900.width, height: 1000 });
    await page.evaluate(() => document.querySelector('.fe-inspector__scroll')?.scrollTo(0, 0));
    await page.mouse.move(0, 0);
    await sleep(800);
    const framed = await page.evaluate(() => {
      const box = (sel) => {
        const b = document.querySelector(sel)?.getBoundingClientRect();
        return b ? { top: Math.round(b.top), bottom: Math.round(b.bottom) } : null;
      };
      return {
        scroll: box('.fe-inspector__scroll'),
        lock: box('[data-testid="inspector-lock"]'),
        section: box('[data-testid="inspector-plugin-sign-status"]'),
        height: window.innerHeight,
      };
    });
    if (!framed.lock || !framed.scroll || !framed.section) {
      throw new Error(`the inspector did not paint what the picture is of: ${JSON.stringify(framed)}`);
    }
    if (framed.lock.top < framed.scroll.top - 1) {
      throw new Error(`the lock banner is cut by the panel's tabs: ${JSON.stringify(framed)}`);
    }
    if (framed.section.bottom > framed.height - 1) {
      throw new Error(`the Signatures section runs past the window: ${JSON.stringify(framed)}`);
    }
    log(`sign-status framed: lock ${framed.lock.top}–${framed.lock.bottom}, section ends ${framed.section.bottom} of ${framed.height}`);

    /* ⚠⚠ The signer table earns its caption or the shot does not happen.
       docs/APP-PLUGINS.md promises one row per signer with its state —
       Waiting, Invited, Opened it, Signed, Refused — and in v0.43.0 the
       panel showed names, Actions buttons and a blank gap: the lead column
       stood at its 240px desktop width in a ~265px panel and the pinned
       Actions cell painted over what was left. Both are fixed in the table
       itself (DataTable: the lead falls to its minimum, a control is frozen
       only while it leaves room to scroll), and this measures the RESULT:
       how much of each cell a person can actually see, after the scrollport
       and after any cell that is pinned over it. A cell that is present,
       sized and invisible is exactly what shipped. */
    const cells = await page.evaluate(() => {
      const list = document.querySelector('[data-testid="surface-list"] .fe-list');
      if (!list) return null;
      const port = list.getBoundingClientRect();
      const menuEl = list.querySelector('.fe-list__row .fe-list__col--menu');
      const pinned = menuEl ? getComputedStyle(menuEl).position === 'sticky' : false;
      const edge = pinned && menuEl ? Math.min(port.right, menuEl.getBoundingClientRect().left) : port.right;
      const seen = (sel) => {
        const el = list.querySelector(sel);
        if (!el) return -1;
        const r = el.getBoundingClientRect();
        return Math.round(Math.max(0, Math.min(r.right, edge) - Math.max(r.left, port.left)));
      };
      return {
        pane: Math.round(port.width),
        pinned,
        who: seen('.fe-list__row .fe-list__col--lead'),
        state: seen('.fe-list__row [data-col="state"]'),
        when: seen('.fe-list__row [data-col="when"]'),
      };
    });
    if (!cells) throw new Error('the Signatures section drew no list at all');
    if (cells.state < 24 || cells.when < 24) {
      throw new Error(
        `the signer table hides what its caption promises: ${JSON.stringify(cells)} ` +
          '(state/when must be readable, not merely present)',
      );
    }
    log(`sign-status signer table: pane ${cells.pane}px, visible who=${cells.who} state=${cells.state} when=${cells.when} (menu pinned: ${cells.pinned})`);
    await shot(page, SET, 'sign-status-1440.png');
    await page.setViewportSize(view900);

    // ── Sam, outside, with the link and the PIN Dana passes on ───────────
    const mine = await dana.json('/api/shares');
    const signerLink = (mine.items ?? []).find((r) => r.share?.page_id === 'signer');
    if (!signerLink) throw new Error(`no signing link in Dana's shares: ${JSON.stringify(mine).slice(0, 300)}`);
    const { pin } = await dana.json(`/api/shares/${signerLink.share.id}/pin`);

    const sctx = await newContext(browser);
    const spage = await sctx.newPage();
    await spage.goto(`${inst.url}/s/${signerLink.share.token}`);
    await spage.getByTestId('public-page-pin').waitFor({ timeout: 20_000 });
    await sleep(500);
    await shot(spage, SET, 'sign-outside-pin-1440.png');
    await spage.getByTestId('public-page-pin-input').fill(pin);
    await spage.getByTestId('public-page-pin-submit').click();
    await spage.getByTestId('public-page-action-next').click();
    await spage.getByTestId('surface-signature').waitFor({ timeout: 20_000 });
    await spage.locator('[data-testid="surface-form"] input[type="text"]').first().fill('01.10.2026');
    // The box was defined as typed, so the pad opens in that mode already and
    // offers the one face the requester chose — no mode strip, no font row.
    await spage.getByTestId('surface-signature-typed').fill('Sam Carter');
    // The typed name is rendered into the preview by the pad; wait for it, or
    // the picture is of an empty signature box.
    await spage.locator('[data-testid="surface-signature"] .fe-ssig__img').first().waitFor({ timeout: 15_000 });
    await spage.mouse.move(0, 0);
    await sleep(600);
    await shot(spage, SET, 'sign-outside-fill-1440.png');
    await sctx.close();

    // ── Dana's bell, her whole list, her own links ──────────────────────
    await page.goto(`${inst.url}/drive/explore?storage=${STORAGE}`);
    await page.getByTestId('unread-badge').waitFor({ timeout: 20_000 });
    await page.getByTestId('notification-bell').click();
    await page.getByTestId('notification-panel').waitFor();
    await page.getByTestId('notification-row').first().waitFor();
    await sleep(500);
    await shot(page, SET, 'bell-badge-1440.png');
    await page.getByTestId('notification-view-all').click();
    await page.getByTestId('notifications-screen').waitFor();
    await page.getByTestId('notification-row').first().waitFor();
    await sleep(500);
    await shot(page, SET, 'notifications-list-1440.png');

    /* The app's own home, PINs section — where the requester finds the PIN to
       pass on. v0.43.0: an app may read its own link's PIN, so the digits
       here are the ones the page will actually accept, and each one stays
       hidden behind "Show PIN" until it is asked for (which is why this
       picture shows the section, not a PIN). */
    await page.goto(`${inst.url}/drive/apps/sign/envelopes?section=pins`);
    const pinsTab = page.getByTestId('surface-section-pins');
    await pinsTab.waitFor({ timeout: 20_000 });
    /* ⚠ The TAB existing is not the tab being OPEN. `?section=pins` opens it
       on the admin route; on the person's own screen the first shot came back
       showing "I asked for these" with the PINs tab merely present — a
       picture of the wrong screen that every wait passed. Selection is the
       thing to wait for, and clicking is the same thing a person does. */
    for (let i = 0; i < 40 && (await pinsTab.getAttribute('aria-selected')) !== 'true'; i++) {
      await pinsTab.click().catch(() => {});
      await sleep(250);
    }
    if ((await pinsTab.getAttribute('aria-selected')) !== 'true') {
      throw new Error('the PINs section never opened on the requester’s own screen');
    }
    await page.locator('[data-testid="surface-list"] .fe-list__row').first().waitFor({ timeout: 20_000 });
    await page.mouse.move(0, 0);
    await sleep(500);
    await shot(page, SET, 'sign-pins-1440.png');

    await page.goto(`${inst.url}/drive/my-shares`);
    await page.getByTestId(`my-share-actions-${signerLink.share.id}`).click();
    await page.getByRole('menuitem', { name: /Copy PIN/ }).waitFor();
    await sleep(400);
    await shot(page, SET, 'my-shares-1440.png');
    await page.keyboard.press('Escape');
    await dctx.close();

    // ── the administrator's table of every link ─────────────────────────
    const actx = await newContext(browser);
    const apage = await actx.newPage();
    await signIn(apage, inst.url, ADMIN);
    await apage.goto(`${inst.url}/admin/shares`);
    await apage.getByTestId(`share-actions-${signerLink.share.id}`).click();
    await apage.getByRole('menuitem', { name: /Copy PIN/ }).waitFor();
    await sleep(400);
    await shot(apage, SET, 'admin-table-actions-1440.png');
    await actx.close();
  } finally {
    await browser.close();
    await inst.stop();
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
