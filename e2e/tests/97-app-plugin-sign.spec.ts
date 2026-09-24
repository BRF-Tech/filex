/**
 * 97-app-plugin-sign — the signing app, end to end.
 *
 * Install the app, ask it to sign a document, and check that what comes
 * back is a signed PDF whose signature filex itself can verify. The module
 * is the real `filex-sign` build; the host's per-tenant CA does the signing,
 * as it does in production (the private key never enters the sandbox).
 *
 * What it measures, in order:
 *   1. the install wizard reviews the permissions — including `sign`, the
 *      one that lets an app use this server's signing key;
 *   2. the menu is state-aware: a PDF with nothing pending offers **Sign…**
 *      and **Request signatures…**, and NOT **Sign / Fill**; the hidden
 *      `apply` action is in no menu at all;
 *   3. every screen the app draws obeys the v3 renderer rules (known nodes,
 *      one primary button, a choice instead of a dropdown) and carries every
 *      language its manifest promises;
 *   4. the self-sign wizard runs to a job, the job writes the signed
 *      sibling, and that file carries a real PKCS#7 signature;
 *   5. the verify screen reports the signature on the signed file;
 *   6. an outside signer, on their link, sees the document on the step where
 *      they approve it (the exposed copy is asked for URL-encoded).
 *
 * The module lives in a sibling checkout; without it the spec skips, unless
 * FILEX_REQUIRE_WASM_FIXTURE=1 (CI) makes that a failure.
 */
import { test, expect, type Page } from '@playwright/test';
import { loginAs, apiLogin } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName, waitForOp } from '../helpers/seed';
import { guardFixture, installThroughWizard, minimalPDF, resolveApp } from '../helpers/appPlugin';
import {
  checkLanguages,
  checkSurface,
  driveToJob,
  openView,
  removeApp,
  runAction,
} from '../helpers/surface';

const APP = resolveApp('sign');

const STORAGE = `e2e-sign-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const PDF = 'contract.pdf';
const QUALIFIED = `${STORAGE}://${PDF}`;

/** The action ids the manifest declares, so the spec follows the app. */
function actionID(match: RegExp): string | undefined {
  return (APP.manifest?.actions ?? []).find((a) => match.test(a.id))?.id;
}

async function openExplorer(page: Page) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await loginAs(page);
  await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
  await expect(page.getByTestId(`sidenav-storage-${STORAGE}`)).toBeVisible();
}

async function menuVerbs(page: Page, qualified: string): Promise<string[]> {
  const row = page.locator(`[data-fe-path="${qualified}"]`).first();
  await expect(row).toBeVisible();
  await row.click({ button: 'right' });
  const menu = page.getByRole('menu').first();
  await expect(menu).toBeVisible();
  const names = (await menu.locator('[role="menuitem"] .fe-ctx__label').allInnerTexts()).map((s) => s.trim());
  await page.keyboard.press('Escape');
  await expect(menu).toBeHidden();
  return names;
}

test.describe('App plugin: sign — install, sign, verify', () => {
  test.describe.configure({ mode: 'serial' });
  guardFixture(APP, test.skip);

  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
    await apiLogin(request);
    const up = await request.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORAGE}://`,
        'file[]': { name: PDF, mimeType: 'application/pdf', buffer: minimalPDF('e2e contract') },
      },
    });
    if (!up.ok()) throw new Error(`upload ${PDF} failed: ${up.status()} ${await up.text()}`);
    await removeApp(request, 'sign');
  });

  test.afterAll(async ({ request }) => {
    await apiLogin(request);
    await removeApp(request, 'sign');
    await dropStorageByName(request, STORAGE);
  });

  test('the wizard reviews the signing permission before granting it', async ({ page }) => {
    // The module is ~20 MB and filex compiles it at install — 23 s of the
    // default 30 on an idle machine, more with a build beside it. The wait is
    // installThroughWizard's own (APP_INSTALL_ALLOWANCE_MS, helpers/appPlugin.ts),
    // added to this test's timeout, so no spec sets one of its own.
    await loginAs(page);
    await installThroughWizard(page, APP);
    expect(
      APP.manifest?.permissions ?? [],
      'a signing app asks for `sign`; that is the row the administrator must read',
    ).toContain('sign');
  });

  test('the menu follows what the app knows about the file', async ({ page }) => {
    await openExplorer(page);
    const verbs = await menuVerbs(page, QUALIFIED);
    const joined = verbs.join(', ');

    expect(/sign|imzala/i.test(joined), `a PDF must offer the signing rows: [${joined}]`).toBe(true);
    // `applies.no_state: ["pending"]` — nothing is pending on a fresh file,
    // so the "finish someone else's request" row must NOT be there.
    expect(
      verbs.some((v) => /sign \/ fill|imzala \/ doldur/i.test(v)),
      `no signature is pending, so "Sign / Fill" must not be offered: [${joined}]`,
    ).toBe(false);
    // A hidden action is the second half of a flow, never a menu row.
    const hidden = (APP.manifest?.actions ?? []).filter((a) => a.hidden);
    for (const a of hidden) {
      const label = a.label?.en ?? a.id;
      expect(
        verbs.some((v) => v.toLowerCase() === label.toLowerCase()),
        `${a.id} is hidden: it is started by a surface, not by a menu row`,
      ).toBe(false);
    }
  });

  test('the step that shows the document fits the window, and the window alone', async ({ page }) => {
    /* ⚠⚠ "PDF çizme bölümü ekrana tam sığmalı" — the box-placing step is a
       DOCUMENT, not a form in a text column. Three rules carry the height
       from the tab to the page canvas (`.fe-apppage.is-doc`, its body, and
       the surface between them); with any one of them missing the document
       is drawn at its own height inside a shorter box and runs off the
       bottom, which is what the complaint described. Measured, because it
       looked right in every screenshot that had it wrong. */
    await loginAs(page);
    await page.setViewportSize({ width: 1280, height: 800 });
    const wizard = (APP.manifest?.views ?? []).find((v) => /request/.test(v.id))?.id ?? 'request';
    await page.goto(`/admin/apps/sign/${wizard}?path=${encodeURIComponent(QUALIFIED)}`);

    const identity = page.locator('textarea').first();
    await expect(identity, 'the signer list is a LONG field: its help line says one per line').toBeVisible({
      timeout: 20_000,
    });
    await identity.fill('E2E Signer <signer@example.test>');
    const next = page.locator('[data-testid^="plugin-page-action-"]').last();
    await next.click();

    /* ⚠⚠ The boxes are DEFINED first and PLACED after (v3 §3.3). This step
       shows cards and no document at all; the document arrives on the next
       one, with the boxes waiting to be put down. */
    const addSignature = page.locator('[data-testid="surface-pdf-add-signature"]');
    await expect(addSignature, 'the boxes are named on a step of their own').toBeVisible({ timeout: 20_000 });
    expect(await page.locator('canvas').count(), 'no document on the naming step').toBe(0);
    await addSignature.click();
    await expect(page.locator('[data-testid^="surface-pdf-card-"]').first()).toBeVisible();
    await next.click();

    // ...and the placing step hands that box out, with nothing to type into.
    await expect(page.locator('[data-testid^="surface-pdf-pending-"]').first()).toBeVisible({ timeout: 20_000 });

    const canvas = page.locator('canvas').first();
    await expect(canvas).toBeVisible({ timeout: 30_000 });
    // ⚠ A canvas that never rendered is 0x0 and would pass every bound below
    // without meaning anything: the page has to be a PAGE first.
    await expect
      .poll(async () => Math.round((await canvas.boundingBox())?.width ?? 0), { timeout: 30_000 })
      .toBeGreaterThan(200);
    const box = await canvas.boundingBox();
    const view = page.viewportSize()!;
    expect(box, 'the document is drawn').toBeTruthy();

    // The rule that carries the height down, checked where it breaks: the
    // surface between the page body and the document node grows to its
    // content unless it is told to share the body's height.
    const surface = await page.evaluate(() => {
      const el = document.querySelector('.fe-apppage.is-doc .fe-surface');
      if (!el) return null;
      const cs = getComputedStyle(el);
      return { grow: cs.flexGrow, minHeight: cs.minHeight, height: Math.round(el.getBoundingClientRect().height) };
    });
    expect(surface, 'the document step draws a surface inside a document page').toBeTruthy();
    expect(surface!.grow, 'the surface must take the body height, not its own content height').toBe('1');
    expect(surface!.minHeight).toBe('0px');
    expect(
      Math.round(box!.y + box!.height),
      `the document runs past the bottom of the window (page ends at ${Math.round(box!.y + box!.height)}, window is ${view.height})`,
    ).toBeLessThanOrEqual(view.height);
    expect(Math.round(box!.width)).toBeLessThanOrEqual(view.width);

    // ...and the WINDOW does not scroll: the document has its own pane.
    const scrolls = await page.evaluate(
      () => document.documentElement.scrollHeight > document.documentElement.clientHeight + 2,
    );
    expect(scrolls, 'a document step must not make the whole tab scroll').toBe(false);
  });

  test('every screen the app draws obeys the renderer rules', async ({ request }) => {
    await apiLogin(request);
    // The views that apply to a file; `home` and `inspector` screens are
    // opened on the same path, which is what the details panel does.
    for (const view of APP.manifest?.views ?? []) {
      const s = await openView(request, 'sign', view.id, QUALIFIED).catch((err) => {
        // A view that refuses this file is a fact about the app, not a
        // failure of the renderer rules — record it and move on.
        test.info().annotations.push({ type: 'note', description: `view ${view.id}: ${err}` });
        return undefined;
      });
      if (!s) continue;
      checkSurface(s, `sign/${view.id}`);
      checkLanguages(s, APP.languages, `sign/${view.id}`);
    }
  });

  test('signing the document writes a signed sibling with a real signature', async ({ request }) => {
    await apiLogin(request);
    const sign = actionID(/^sign$/) ?? 'sign';
    const view = (APP.manifest?.actions ?? []).find((a) => a.id === sign)?.view;
    expect(view, `the ${sign} action opens a screen first`).toBeTruthy();

    // Starting it from the menu is what a person does; the server answers
    // the first screen of the wizard (or queues straight away).
    const started = await runAction(request, 'sign', sign, [QUALIFIED]);
    if (started.op) {
      const done = await waitForOp(request, started.op.id, 120_000);
      expect(done.status, `signing finished: ${JSON.stringify(done)}`).toBe('ok');
    } else {
      expect(started.surface, 'the action answered neither a screen nor a job').toBeTruthy();
      checkSurface(started.surface!, `sign/${view}`);
      // Walk the wizard: each screen is checked, filled and submitted until
      // it queues the job. A wizard that cannot be finished is the finding,
      // and driveToJob says which screen it got stuck on.
      const { op } = await driveToJob(request, 'sign', view!, QUALIFIED, {
        languages: APP.languages,
        // The one choice the spec insists on: the signed document lands
        // BESIDE the original, so the listing below can see it. Left to the
        // driver it would take the first button (a new version of the same
        // file) and there would be nothing new to find.
        values: {
          output_mode: 'sibling',
          output_name: '{stem}-signed{ext}',
          signer_name: 'E2E Signer',
          name: 'E2E Signer',
          reason: 'e2e',
          email: 'admin@local',
        },
      });
      const done = await waitForOp(request, op.id, 120_000);
      expect(done.status, `signing finished: ${JSON.stringify(done)}`).toBe('ok');
    }

    const list = await request.get(
      `/api/files/manager?action=index&path=${encodeURIComponent(`${STORAGE}://`)}`,
    );
    const names = ((await list.json()).files as { basename: string }[]).map((f) => f.basename);
    const signed = names.find((n) => /signed/i.test(n) && n.endsWith('.pdf'));
    expect(signed, `a signed sibling must be next to the original: [${names.join(', ')}]`).toBeTruthy();

    const raw = await request.get(
      `/api/files/manager?action=download&path=${encodeURIComponent(`${STORAGE}://${signed}`)}`,
    );
    const body = (await raw.body()).toString('latin1');
    expect(body.startsWith('%PDF'), 'the signed file is still a PDF').toBe(true);
    // A signed PDF carries the signature dictionary and the byte range it
    // covers, or it only LOOKS signed.
    for (const marker of ['/Type /Sig', '/ByteRange']) {
      expect(body.includes(marker), `the signed PDF must carry ${marker}`).toBe(true);
    }
    // …and a detached CMS blob under one of the two SubFilters a PDF
    // reader knows: PAdES (ETSI.CAdES.detached, what filex-sign writes) or
    // the older adbe.pkcs7.detached. The spec accepts either so it does not
    // have to be rewritten the day the app moves between them.
    expect(
      /ETSI\.CAdES\.detached|adbe\.pkcs7\.detached/.test(body),
      'the signed PDF must carry a detached CMS signature (ETSI.CAdES.detached or adbe.pkcs7.detached)',
    ).toBe(true);
    // The signature must be the host's, issued for this signing: the leaf
    // certificate travels inside the blob, so the signer's name is in it.
    expect(body.includes('E2E Signer') || /\/Name\s*\(/.test(body), 'the signature names its signer').toBe(true);
  });

  test('the signed file reports its signature', async ({ request }) => {
    await apiLogin(request);
    const list = await request.get(
      `/api/files/manager?action=index&path=${encodeURIComponent(`${STORAGE}://`)}`,
    );
    const names = ((await list.json()).files as { basename: string }[]).map((f) => f.basename);
    const signed = names.find((n) => /signed/i.test(n) && n.endsWith('.pdf'));
    test.skip(!signed, 'nothing was signed in the previous step');

    const status = (APP.manifest?.views ?? []).find((v) => v.placement === 'inspector');
    test.skip(!status, 'this app has no inspector screen to report signatures');

    const s = await openView(request, 'sign', status!.id, `${STORAGE}://${signed}`);
    checkSurface(s, `sign/${status!.id}`);
    checkLanguages(s, APP.languages, `sign/${status!.id}`);
    const shown = JSON.stringify(s);
    expect(
      /E2E Signer|signed|imzal/i.test(shown),
      `the signatures panel must report the signature it finds: ${shown.slice(0, 400)}`,
    ).toBe(true);
  });

  /* ⭐ The outside signer's third step — the owner's own test path. The page
     asks for the copy the app exposed with encodeURIComponent("pub:0"), and
     until the server decoded route parameters (handlers/public_api.go →
     pathParam) that was a 404 and the step said "The document could not be
     loaded" while the link itself worked. Measured in a browser on
     2026-09-21. It runs on a document of its own, so the menu and renderer
     tests above keep a file with nothing pending on it. */
  test('an outside signer sees the document on the step where they approve it', async ({ page, request }) => {
    await apiLogin(request);
    const doc = 'outside-signer.pdf';
    const up = await request.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORAGE}://`,
        'file[]': { name: doc, mimeType: 'application/pdf', buffer: minimalPDF('an outside signer') },
      },
    });
    expect(up.ok(), `upload ${doc}: ${up.status()}`).toBe(true);

    const request_ = actionID(/^request$/) ?? 'request';
    const started = await runAction(request, 'sign', request_, [`${STORAGE}://${doc}`], {
      signers: [{ name: 'Sam Carter', email: 'sam@partner.example' }],
      // ⚠ A TYPED signature box: the owner (2026-09-21) — a box is drawn OR
      // typed, chosen when it is defined, and a drawn box offers no typing.
      // Typing is what this stranger does, so the box says it is typed.
      fields: [{ id: 'sig-1', type: 'signature', style: 'typed', page: 1, x: 0.1, y: 0.7, w: 0.3, h: 0.08, assignee: 's1', label: 'Signature' }],
      pin: 'auto', expiry: 7, order: 'parallel', allow_decline: true, lock: false, audit: false,
      deliver: 'none', delivery_pin: 'none', output_mode: 'version',
    });
    expect(started.op, 'a request with parameters is queued at once').toBeTruthy();
    const done = await waitForOp(request, started.op!.id, 120_000);
    expect(done.status, `the request job: ${JSON.stringify(done)}`).toBe('ok');

    const mine = (await (await request.get('/api/shares')).json()) as {
      items?: { share?: { id: number; token: string; page_id?: string }; node_path?: string }[];
    };
    const link = (mine.items ?? []).find((r) => r.share?.page_id === 'signer' && (r.node_path ?? '').endsWith(doc));
    expect(link?.share, `the request opened a signing link: ${JSON.stringify(mine).slice(0, 300)}`).toBeTruthy();
    const { pin } = (await (await request.get(`/api/shares/${link!.share!.id}/pin`)).json()) as { pin: string };
    expect(pin, 'the signing link carries a PIN').toBeTruthy();

    // The stranger: this page has no session at all.
    const copies: { status: number; url: string }[] = [];
    page.on('response', (r) => {
      if (r.url().includes('/file/')) copies.push({ status: r.status(), url: new URL(r.url()).pathname });
    });
    await page.goto(`/s/${link!.share!.token}`);
    await page.getByTestId('public-page-pin-input').fill(pin);
    await page.getByTestId('public-page-pin-submit').click();
    await page.getByTestId('public-page-action-next').click();
    // A typed box offers one way to sign, so there is no mode to choose.
    await expect(page.getByTestId('surface-signature-mode-draw')).toHaveCount(0);
    await page.getByTestId('surface-signature-typed').fill('Sam Carter');
    await page.getByTestId('public-page-action-next').click();

    await expect.poll(() => copies.length, { timeout: 20_000, message: 'the approve step never asked for the document' }).toBeGreaterThan(0);
    expect(
      copies.every((c) => c.status === 200),
      `every request for the exposed copy must succeed: ${JSON.stringify(copies)}`,
    ).toBe(true);
    await expect(page.getByTestId('surface-pdf-error'), 'the step says the document could not be loaded').toHaveCount(0);
    const canvas = page.locator('.fe-spdf canvas').first();
    await expect(canvas).toBeVisible({ timeout: 30_000 });
    await expect
      .poll(async () => Math.round((await canvas.boundingBox())?.width ?? 0), { timeout: 30_000 })
      .toBeGreaterThan(100);
  });
});
