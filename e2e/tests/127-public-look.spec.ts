/**
 * 127 — how the outward-facing pages LOOK, measured in a real browser.
 *
 * ⚠⚠ Why a spec and not a screenshot: v0.43.0 unified `/s/`, `/d/` and an
 * app's public page into one shell — which is right — and levelled the
 * unified shell DOWN to the plainest of the three (owner, 2026-09-23: "böyle
 * güzel bir ekranımız vardı share katmanında … iki sayfa aynı olsun dediğim
 * için ikisini de kötü hale çevirmişsin"). Every number below was true of the
 * Go-rendered page up to v0.42.2, false in v0.43.0, and is true again:
 *
 *   · the card is a CARD — lighter than the ground behind it. v0.43.0 painted
 *     `--fe-bg-elev` (the app's SUNKEN tone, #f7f8fb) on `--fe-bg` (#ffffff):
 *     a card you cannot see.
 *   · a gate is ~400px and sits in the middle of the window, not 720px glued
 *     to the top with two thirds of the screen empty under it.
 *   · it opens with a round badge.
 *   · the field and the primary button SPAN the card.
 *   · the instance's mark is above the card, centred.
 *
 * The measurements are of the RENDERED boxes (getBoundingClientRect, resolved
 * colours), because a class name being present says nothing about what the
 * person sees — a rule further down the sheet can undo it.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { dropStorageByName, newAuthedRequest, seedLocalStorage } from '../helpers/seed';

const STORAGE = `e2e-look-${Date.now()}`;
const FOLDER = 'Invoices';
const FILE = 'guidelines.pdf';

/** sRGB relative luminance of a resolved `rgb(...)` / `rgba(...)` colour. */
function luminanceOf(css: string): number {
  const nums = (css.match(/[\d.]+/g) ?? []).slice(0, 3).map(Number);
  if (nums.length < 3) return -1;
  const lin = (c: number) => {
    const x = c / 255;
    return x <= 0.04045 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * lin(nums[0]) + 0.7152 * lin(nums[1]) + 0.0722 * lin(nums[2]);
}

async function measure(page: Page) {
  return page.evaluate(() => {
    const root = document.querySelector('[data-testid="public-page"]') as HTMLElement;
    const card = root.querySelector('.fe-ppage__card') as HTMLElement;
    const brand = document.querySelector('[data-testid="public-brand"]') as HTMLElement;
    const box = (el: Element | null) => {
      if (!el) return null;
      const r = el.getBoundingClientRect();
      return { x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height) };
    };
    return {
      layout: root.dataset.layout,
      root: box(root),
      card: box(card),
      brand: box(brand),
      brandInCard: !!brand && card.contains(brand),
      badge: box(root.querySelector('[data-testid="public-page-badge"]')),
      badgeKind: (root.querySelector('[data-testid="public-page-badge"]') as HTMLElement | null)?.dataset.badge ?? '',
      pin: box(root.querySelector('[data-testid="public-page-pin-input"]')),
      submit: box(root.querySelector('[data-testid="public-page-pin-submit"]')),
      download: box(root.querySelector('[data-testid="public-share-download"]')),
      cardBg: getComputedStyle(card).backgroundColor,
      // The ground is painted on the shell's own root, not on <body>.
      groundBg: getComputedStyle(root).backgroundImage,
      groundColour: getComputedStyle(document.body).backgroundColor,
      cardRadius: getComputedStyle(card).borderTopLeftRadius,
      cardShadow: getComputedStyle(card).boxShadow,
      cardPadTop: getComputedStyle(card).paddingTop,
      viewport: { w: window.innerWidth, h: window.innerHeight },
      scrollTop: root.getBoundingClientRect().top,
    };
  });
}

/** The colour a gradient's first stop resolves to, for the light/dark check. */
async function groundStop(page: Page): Promise<string> {
  return page.evaluate(() => {
    const root = document.querySelector('[data-testid="public-page"]') as HTMLElement;
    const v = getComputedStyle(root).getPropertyValue('--fe-ppage-ground-1').trim();
    if (!v) return '';
    const probe = document.createElement('span');
    probe.style.color = v;
    document.body.appendChild(probe);
    const out = getComputedStyle(probe).color;
    probe.remove();
    return out;
  });
}

test.describe('the outward-facing pages look like the product', () => {
  let api: APIRequestContext;
  let pinned = '';
  let plain = '';
  let folder = '';
  let drop = '';

  test.beforeAll(async ({ playwright, baseURL, request }) => {
    api = await newAuthedRequest(playwright, baseURL ?? '');
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, `/tmp/filex-${STORAGE}`);
    const mk = await api.post('/api/files/manager?action=newfolder', {
      data: { path: `${STORAGE}://`, name: FOLDER },
    });
    expect(mk.ok(), `newfolder ${mk.status()}`).toBeTruthy();

    const form = { name: FILE, mime: 'application/pdf' };
    const up = await api.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORAGE}://`,
        'file[]': { name: form.name, mimeType: form.mime, buffer: Buffer.from('%PDF-1.4 look\n') },
      },
    });
    expect(up.ok(), `upload ${up.status()} ${await up.text()}`).toBeTruthy();

    const make = async (data: Record<string, unknown>) => {
      const res = await api.post('/api/files/share', { data });
      expect(res.ok(), `share ${res.status()} ${await res.text()}`).toBeTruthy();
      return (await res.json()).share.token as string;
    };
    pinned = await make({ path: `${STORAGE}://${FILE}`, kind: 'file', pin: '4821' });
    plain = await make({ path: `${STORAGE}://${FILE}`, kind: 'file' });
    folder = await make({ path: `${STORAGE}://${FOLDER}`, kind: 'folder' });
    drop = await make({ path: `${STORAGE}://${FOLDER}`, kind: 'drop', drop_settings: { ask_name: true } });
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await api.dispose();
  });

  test('the PIN gate is the reference screen again', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto(`/s/${pinned}`);
    await page.getByTestId('public-page-pin').waitFor();
    const m = await measure(page);

    // A gate, whatever is behind it.
    expect(m.layout).toBe('gate');

    // ⚠ 400px, not 720. A four-digit code does not need half a desk.
    expect(m.card!.w, `gate card width ${m.card!.w}`).toBeGreaterThan(360);
    expect(m.card!.w, `gate card width ${m.card!.w}`).toBeLessThanOrEqual(420);

    // Centred in the window, within a third of its own height — v0.43.0 put
    // its top edge at y=24 of a 900px window and left the rest blank.
    const cardMid = m.card!.y + m.card!.h / 2;
    expect(Math.abs(cardMid - m.viewport.h / 2), `card centre ${cardMid} of ${m.viewport.h}`).toBeLessThan(
      m.card!.h / 3,
    );

    // The mark stands above the card, centred on it.
    expect(m.brandInCard).toBe(false);
    expect(m.brand!.y + m.brand!.h).toBeLessThanOrEqual(m.card!.y);
    const brandMid = m.brand!.x + m.brand!.w / 2;
    expect(Math.abs(brandMid - (m.card!.x + m.card!.w / 2))).toBeLessThan(4);

    // ⚠ The one act on the page is LIVE from the start. A 50%-opacity accent
    // slab is what a stranger met first while the box was still empty.
    const off = await page.getByTestId('public-page-pin-submit').isDisabled();
    expect(off, 'the Continue button is disabled on an empty box').toBe(false);

    // …and the badge is a WASH of colour, not a shape with no ground: its
    // background is opaque enough to see. (That the wash follows the
    // instance's ACCENT rather than the stock blue is measured where an
    // accent is actually set — tests/116-admin-says-which.spec.ts, on the
    // Corporate identity preview, and web/tests/components/brandingPreview.
    // test.ts on the three properties accentStyleOf emits.)
    const badgeInk = await page.evaluate(() => {
      const el = document.querySelector('[data-testid="public-page-badge"]') as HTMLElement;
      const cs = getComputedStyle(el);
      return { bg: cs.backgroundColor, fg: cs.color };
    });
    expect(badgeInk.bg, JSON.stringify(badgeInk)).not.toBe('rgba(0, 0, 0, 0)');
    expect(badgeInk.fg, JSON.stringify(badgeInk)).not.toBe(badgeInk.bg);

    // A round, tinted badge, and it is the padlock.
    expect(m.badgeKind).toBe('lock');
    expect(m.badge!.w).toBe(64);
    expect(m.badge!.h).toBe(64);

    // The field and the button SPAN the card — v0.43.0 left both hugging its
    // left edge at about a third of the width.
    expect(m.pin!.w, `pin ${m.pin!.w} of card ${m.card!.w}`).toBeGreaterThan(m.card!.w * 0.75);
    expect(m.submit!.w, `submit ${m.submit!.w} of card ${m.card!.w}`).toBeGreaterThan(m.card!.w * 0.75);
    // Generously padded, as the old card was (32px top).
    expect(parseFloat(m.cardPadTop)).toBeGreaterThanOrEqual(24);

    // A card, not a panel: 16px corners and a shadow.
    expect(m.cardRadius).toBe('16px');
    expect(m.cardShadow).not.toBe('none');

    // ⚠⚠ …and it is LIGHTER than the ground it sits on, in light mode.
    // This is the one that made the page look broken: the card was painted
    // in the app's SUNKEN tone on the app's surface tone.
    const ground = await groundStop(page);
    expect(luminanceOf(m.cardBg), `card ${m.cardBg} vs ground ${ground}`).toBeGreaterThan(luminanceOf(ground));
  });

  test('…and the same card, inverted, in dark mode', async ({ browser }) => {
    const ctx = await browser.newContext({ colorScheme: 'dark', viewport: { width: 1440, height: 900 } });
    const page = await ctx.newPage();
    await page.goto(`/s/${pinned}`);
    await page.getByTestId('public-page-pin').waitFor();
    const m = await measure(page);
    const ground = await groundStop(page);
    // In the dark the card is still the RAISED surface — lighter than the
    // ground, exactly as the old stylesheet had it (#1f242c on #12151a).
    expect(luminanceOf(m.cardBg), `card ${m.cardBg} vs ground ${ground}`).toBeGreaterThan(luminanceOf(ground));
    expect(m.card!.w).toBeLessThanOrEqual(420);
    await ctx.close();
  });

  test('a document, a folder and a drop are the same family at three widths', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });

    await page.goto(`/s/${plain}`);
    await page.getByTestId('public-share-file').waitFor();
    const file = await measure(page);
    expect(file.layout).toBe('gate');
    expect(file.badgeKind).toBe('file');
    // The one act on the page spans the card.
    expect(file.download!.w).toBeGreaterThan(file.card!.w * 0.75);
    // ⚠ The name is printed once. It used to be the heading AND the line
    // under it.
    expect(await page.getByText(FILE, { exact: false }).count()).toBe(1);

    await page.goto(`/s/${folder}`);
    await page.getByTestId('public-share-folder').waitFor();
    const dir = await measure(page);
    // ⚠ `form`, not `wide`: this server sends no LISTING for a folder share,
    // and an 880px card holding a heading and two buttons is a worse page
    // than a 520px one. It widens the moment entries arrive — that rule is
    // `lib/publicLayout`, pinned by web/tests/components/publicShell.test.ts
    // (which can hand the shell a listing without a server that sends one).
    expect(dir.layout).toBe('form');
    expect(dir.badgeKind).toBe('folder');
    expect(dir.card!.w).toBeGreaterThan(file.card!.w);
    // The action row's rule runs the full width of the card rather than
    // stopping under the buttons.
    const rule = await page.evaluate(() => {
      const a = document.querySelector('.fe-ppage__actions') as HTMLElement | null;
      const c = document.querySelector('.fe-ppage__card') as HTMLElement;
      if (!a) return null;
      return { actions: Math.round(a.getBoundingClientRect().width), inner: Math.round(c.clientWidth) };
    });
    expect(rule).not.toBeNull();
    expect(Math.abs(rule!.actions - (rule!.inner - 48)), JSON.stringify(rule)).toBeLessThan(8);

    await page.goto(`/d/${drop}`);
    await page.getByTestId('public-request-drop').waitFor();
    const box = await measure(page);
    expect(box.layout).toBe('form');
    // The gate is the narrow one and the form is wider; a drop and a
    // listing-less folder are the same middle card, which is the point of
    // there being three widths and not one per page.
    expect(box.card!.w).toBeGreaterThan(file.card!.w);
    expect(box.card!.w).toBe(dir.card!.w);
    // The page says what it is before it asks for anything.
    await expect(page.getByTestId('public-request-lead')).toBeVisible();

    // One family: the same corner radius on all three.
    expect(new Set([file.cardRadius, dir.cardRadius, box.cardRadius]).size).toBe(1);
  });

  test('a link that is gone is the same card with a warning badge', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto('/s/0000000000000000');
    await page.getByTestId('public-page-unavailable').waitFor();
    const m = await measure(page);
    expect(m.layout).toBe('gate');
    expect(m.badgeKind).toBe('alert');
    expect(m.card!.w).toBeLessThanOrEqual(420);
  });

  test('on a phone the card keeps its shape and the page does not scroll sideways', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    for (const url of [`/s/${pinned}`, `/s/${plain}`, `/s/${folder}`, `/d/${drop}`]) {
      await page.goto(url);
      await page.locator('.fe-ppage__card').waitFor();
      const m = await measure(page);
      // A 12px gutter each side, and nothing wider than the window.
      expect(m.card!.x, `${url} left gutter`).toBeGreaterThanOrEqual(8);
      expect(m.card!.x + m.card!.w, `${url} right edge`).toBeLessThanOrEqual(390);
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      );
      expect(overflow, `${url} horizontal overflow`).toBeLessThanOrEqual(1);
    }
  });
});
