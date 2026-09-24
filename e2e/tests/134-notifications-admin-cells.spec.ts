/**
 * 134 — the admin Notifications page, measured where its two defects were
 * found: in a browser, in three languages (v0.43.0, the pack agent's last
 * look).
 *
 *   1. THE WEBHOOK CELL DREW OVER ITSELF. A DataTable cell is a flex ROW whose
 *      children may shrink below their content (`:where(.fe-list__cell) > *
 *      { min-width: 0 }`). The cell held the "Not sent" badge and the reason
 *      as two such children: when the reason wrapped, the badge was squeezed
 *      narrower than its own label and the label spilled under the reason.
 *      Long reasons made it certain (Spanish, Arabic); a narrow column made
 *      it happen in English too. jsdom has no layout, so the unit test can
 *      only hold the SHAPE (one element per cell); the overlap is measured
 *      here, line box by line box (`getClientRects`, lesson #373).
 *
 *   2. A ROOT FILE'S PATH READ BACKWARDS IN ARABIC. `/informe.pdf` was drawn
 *      `informe.pdf/`: its leading slash is a neutral, takes the line's
 *      direction and lands at the far end. The body already went through
 *      `foreignText`; the SHARED rule wanted a path of two segments, and a
 *      file at the root of a storage has one. Measured here by where the
 *      slash is actually painted.
 *
 * ⚠ Arabic is filex's right-to-left TEST fixture — not published, not
 * advertised, in no screenshot (Burak, 2026-09-19). Spanish is the real,
 * shipped pack on this machine when it is there, and skipped when it is not.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { existsSync, readFileSync } from 'node:fs';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage } from '../helpers/seed';
import { arabicPack, installLangPack, removeLangPack } from '../helpers/langPack';

const STORAGE = `e2e-notifcells-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
/** A file at the ROOT of the storage — a path of ONE segment. */
const ROOT_FILE = 'informe.pdf';

/** The real Spanish pack, when this machine has it. */
const ES_PACK = process.env.FILEX_E2E_LANG_PACK_ES ?? 'G:/filex-lang-es/filex-app.json';

let api: APIRequestContext;
const AR = arabicPack();

/** Install a pack by its manifest, whatever its direction. */
async function installManifest(path: string): Promise<string> {
  const res = await api.post('/api/admin/app-plugins', {
    multipart: { manifest: { name: 'filex-app.json', mimeType: 'application/json', buffer: readFileSync(path) } },
  });
  expect(res.ok(), `installing ${path}: ${res.status()} ${await res.text()}`).toBeTruthy();
  return String((await res.json()).name ?? '');
}

async function removeByName(name: string): Promise<void> {
  const list = await api.get('/api/admin/app-plugins');
  if (!list.ok()) return;
  for (const p of (await list.json()).plugins ?? []) {
    if (p.name === name) await api.delete(`/api/admin/app-plugins/${p.id}`);
  }
}

/**
 * Sign in, and open the page in the ACCOUNT's language.
 *
 * ⚠ The language is set on the account, never pre-seeded into this browser:
 * `loginAs` finds the form by its English or Turkish labels, and a login page
 * already in Spanish has neither — the first try here timed out on "Correo
 * electrónico". The account's language is applied the moment the session is
 * read (`fetchMe` → `applyAccountLocale`), which is also how a real person
 * gets it.
 */
async function openIn(page: Page, locale: string) {
  const set = await api.patch('/api/auth/profile', { data: { locale } });
  expect(set.ok(), `profile locale ${locale}: ${set.status()}`).toBe(true);
  await loginAs(page);
  await page.goto('/admin/notifications');
  /* ⚠ Wait for the CELL, not for the wrapper the fix puts in it: a break-test
     that removes the wrapper must fail on the overlap, not here (lesson
     #379 — the first break-test of this spec did exactly that). */
  await expect(page.locator('.fe-list__row .fe-list__cell[data-col="webhook"]').first()).toBeVisible({
    timeout: 20_000,
  });
  await expect(page.locator('html')).toHaveAttribute('lang', locale);
}

/**
 * Every Webhook cell on the page, measured: does the badge's label reach into
 * the reason's text, and is the badge narrower than its own label?
 *
 * ⚠ Per LINE, not per element — a wrapped inline element reports one union
 * box spanning every line it touches, which calls a readable cell an overlap
 * (lesson #373). The label is measured as TEXT, because the defect is the
 * label spilling out of a squeezed badge, not the badge box itself.
 */
async function webhookCells(page: Page) {
  return page.evaluate(() => {
    const textRects = (el: Element | null) => {
      if (!el) return [] as DOMRect[];
      const out: DOMRect[] = [];
      const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
      for (let n = walker.nextNode(); n; n = walker.nextNode()) {
        if (!(n.textContent ?? '').trim()) continue;
        const r = document.createRange();
        r.selectNodeContents(n);
        for (const rect of r.getClientRects()) if (rect.width > 0 && rect.height > 0) out.push(rect);
      }
      return out;
    };
    const cells = [...document.querySelectorAll('.fe-list__row .fe-list__cell[data-col="webhook"]')];
    return cells.map((cell) => {
      const badge = cell.querySelector('[data-testid="notif-webhook"] > *:first-child') ?? cell.firstElementChild;
      const reason = cell.querySelector('[data-testid="notif-webhook-reason"]');
      const label = textRects(badge);
      const said = textRects(reason);
      let overlap = '';
      for (const a of label) {
        for (const b of said) {
          const w = Math.min(a.right, b.right) - Math.max(a.left, b.left);
          const h = Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top);
          if (w > 1 && h > 1 && !overlap) {
            overlap = `label ${Math.round(a.left)}..${Math.round(a.right)} × reason ${Math.round(b.left)}..${Math.round(b.right)} by ${Math.round(w)}x${Math.round(h)}px`;
          }
        }
      }
      const b = badge as HTMLElement | null;
      return {
        children: cell.children.length,
        squeezed: b ? b.scrollWidth > b.clientWidth + 1 : false,
        badge: b ? `${b.scrollWidth}px of label in ${b.clientWidth}px` : '(none)',
        reasonLines: new Set(said.map((r) => Math.round(r.top))).size,
        cellWidth: Math.round(cell.getBoundingClientRect().width),
        overlap,
        text: (cell.textContent ?? '').replace(/\s+/g, ' ').trim().slice(0, 60),
      };
    });
  });
}

/** Measure at several widths; nothing may overlap, and the case must be HIT. */
async function measureWebhookCells(page: Page, widths: number[]) {
  let wrappedSomewhere = false;
  for (const width of widths) {
    await page.setViewportSize({ width, height: 900 });
    await page.waitForTimeout(400);
    const seen = await webhookCells(page);
    expect(seen.length, `no Webhook cells at ${width}px`).toBeGreaterThan(0);
    for (const c of seen) {
      expect(c.overlap, `${width}px, cell ${c.cellWidth}px "${c.text}": the badge's label is drawn under the reason — ${c.overlap}`).toBe('');
      expect(c.squeezed, `${width}px, cell ${c.cellWidth}px "${c.text}": the badge is squeezed below its label (${c.badge})`).toBe(false);
      if (c.reasonLines > 1) wrappedSomewhere = true;
    }
  }
  /* ⚠ A measurement that never met a wrapping reason proves nothing: the
     overlap only happens when the reason runs onto a second line. */
  expect(wrappedSomewhere, `the reason never wrapped at ${widths.join('/')}px — the case was not exercised`).toBe(true);
}

test.describe('The admin Notifications page draws its cells in one piece, in every language', () => {
  /* ⚠ NOT serial: each language is its own verdict. Serial would skip Spanish
     and Arabic the moment English failed, and a break-test would then show
     one red where there are three. `beforeAll` rebuilds its fixture from
     scratch (storage dropped first), so a worker restarted after a failure
     starts from the same place. */
  test.use({ serviceWorkers: 'block' });

  test.beforeAll(async ({ playwright, baseURL }) => {
    api = await newAuthedRequest(playwright, baseURL!);
    await dropStorageByName(api, STORAGE);
    await seedLocalStorage(api, STORAGE, MOUNT);
    // A write at the ROOT of the storage: the notification it raises names
    // the file by a path of ONE segment, `/informe.pdf`.
    const up = await api.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORAGE}://`,
        'file[]': { name: ROOT_FILE, mimeType: 'application/pdf', buffer: Buffer.from('%PDF-1.4\n%%EOF\n') },
      },
    });
    expect(up.ok(), `upload: ${up.status()} ${await up.text()}`).toBe(true);
    // …and one more row whose delivery is "not sent", so the Webhook column
    // has a reason to draw on every run.
    const t = await api.post('/api/admin/notifications/test');
    expect(t.ok(), `test notification: ${t.status()}`).toBe(true);
    // The premise, checked rather than assumed: the upload row is there and
    // names the file by its root path.
    await expect
      .poll(async () => {
        const r = await api.get('/api/admin/notifications?limit=50');
        const items = ((await r.json()).items ?? []) as Array<{ event: string; meta?: { node?: { path?: string } } }>;
        return items.some((n) => n.event === 'file.uploaded' && n.meta?.node?.path === `/${ROOT_FILE}`);
      }, { timeout: 20_000, message: `no file.uploaded row naming /${ROOT_FILE}` })
      .toBe(true);
  });

  test.afterAll(async () => {
    if (!api) return;
    await api.patch('/api/auth/profile', { data: { locale: 'en' } }).catch(() => undefined);
    await removeLangPack(api, AR).catch(() => undefined);
    await dropStorageByName(api, STORAGE);
    await api.dispose();
  });

  test('English, down to a narrow window: the badge and its reason never overlap', async ({ page }) => {
    await openIn(page, 'en');
    await measureWebhookCells(page, [1440, 1180, 1024, 900]);
  });

  test('Spanish, where the reason is long: the same', async ({ page }) => {
    test.skip(!existsSync(ES_PACK), `the Spanish pack is not on this machine (${ES_PACK})`);
    const name = await installManifest(ES_PACK);
    try {
      await openIn(page, 'es');
      await measureWebhookCells(page, [1440, 1180, 1024]);
    } finally {
      await removeByName(name).catch(() => undefined);
    }
  });

  test('Arabic: a root file’s path is drawn `/informe.pdf`, slash first — not `informe.pdf/`', async ({ page }) => {
    await removeLangPack(api, AR);
    await installLangPack(api, AR);
    await openIn(page, 'ar');
    await page.setViewportSize({ width: 1440, height: 900 });
    await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');

    const seen = await page.evaluate((name) => {
      const cell = [...document.querySelectorAll('.fe-list__row .fe-list__cell[data-col="body"]')].find((c) =>
        (c.textContent ?? '').includes(name),
      );
      if (!cell) return { found: false } as const;
      const text = cell.textContent ?? '';
      // Where is each character PAINTED? The slash and the first letter.
      const walker = document.createTreeWalker(cell, NodeFilter.SHOW_TEXT);
      let node: Node | null = null;
      for (let n = walker.nextNode(); n; n = walker.nextNode()) {
        if ((n.textContent ?? '').includes(name)) {
          node = n;
          break;
        }
      }
      if (!node) return { found: false } as const;
      const s = node.textContent ?? '';
      const at = s.indexOf(`/${name}`);
      const box = (i: number) => {
        const r = document.createRange();
        r.setStart(node!, i);
        r.setEnd(node!, i + 1);
        return r.getBoundingClientRect();
      };
      const slash = box(at);
      const first = box(at + 1);
      const last = box(at + name.length);
      const whole = document.createRange();
      whole.setStart(node, at);
      whole.setEnd(node, at + 1 + name.length);
      return {
        found: true,
        marks: [...text].filter((c) => c === String.fromCharCode(0x2066) || c === String.fromCharCode(0x2069)).length,
        slashX: Math.round(slash.left),
        firstX: Math.round(first.left),
        lastX: Math.round(last.right),
        rects: [...whole.getClientRects()].filter((r) => r.width > 0).length,
        dir: getComputedStyle(cell).direction,
      } as const;
    }, ROOT_FILE);

    expect(seen.found, `no body cell names ${ROOT_FILE}`).toBe(true);
    if (!seen.found) return;
    expect(seen.dir, 'the cell is laid out right to left').toBe('rtl');
    // ⚠⚠ THE measurement, first: in reading order the slash comes FIRST, so
    // in a left-to-right run it is painted to the LEFT of the name. The
    // defect put it at the far RIGHT end — after `informe.pdf`. Asserted
    // before the isolate marks, so a break-test fails on what a person SEES.
    expect(
      seen.slashX,
      `the slash is painted at ${seen.slashX}px, the name runs ${seen.firstX}..${seen.lastX}px — it reads "${ROOT_FILE}/"`,
    ).toBeLessThan(seen.firstX);
    expect(seen.rects, 'and the path is drawn as one run').toBe(1);
    expect(seen.marks, 'because it is wrapped in an isolate').toBe(2);
  });
});
