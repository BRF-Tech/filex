/**
 * 110-symlink-badge — a link that will not open says so, in a real browser.
 *
 * Issue #34, the half that was reported: a symlink to a directory inside a
 * `local` storage root listed as a 0-byte file that would not open. The
 * backend answers the in-root case by FOLLOWING the link — such a row is the
 * directory it points at and opens normally — and flags the rest
 * (`symlink: true`, sometimes with a `link_state`). Until the explorer drew
 * that flag, an out-of-root link was still an ordinary-looking file that
 * mysteriously fails, which is the complaint.
 *
 * ⚠⚠ THIS IS THE ONLY MEASUREMENT THAT SEES THE REAL THING. The unit suite
 * mounts the views from `packages/core/src`, so it proves the components
 * render a badge when handed a flagged row — it cannot prove the flag survives
 * the driver, the projector, the API and the SPA bundle. That whole chain is
 * what broke in the first place.
 *
 * ⚠ Skipped where the host cannot create a symlink (Windows without Developer
 * Mode or elevation), the same honest skip `local/symlink_test.go` takes. A
 * spec that silently exercised nothing would be worse than an absent one.
 *
 * What is measured, in order:
 *   · the out-of-root link is BADGED, and its accessible name is the whole
 *     sentence — not the badge's two words, which read aloud are a second
 *     riddle;
 *   · opening it is refused OUT LOUD: a toast carrying that sentence, and the
 *     listing does not move. Silence is the bug; an unexplained error is
 *     barely better;
 *   · an in-root directory link OPENS, with its contents — the backend half,
 *     verified rather than rebuilt;
 *   · and an ordinary file is untouched by any of it.
 */
import fs from 'node:fs';
import path from 'node:path';

import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { apiLogin, loginAs } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName, storageRoot } from '../helpers/seed';

const STORAGE = `e2e-symlink-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;

/** The link states the wire may carry. `unknown` is the client's word for a
 *  `symlink: true` row with no `link_state`: a row catalogued before 0.54
 *  until its folder's next scan, or a driver that gives no reason. Since 0.54
 *  the scan records the reason with the row (migration 00098), so the listing
 *  the catalogue answers names it too - the last test below holds that. */
const STATES = ['outside_root', 'broken', 'unresolved', 'unknown'];

let canSymlink = true;
let storageId = 0;

test.beforeAll(async ({ request }) => {
  await dropStorageByName(request, STORAGE);

  // The server reads THIS directory, so the fixture has to be built where
  // `storageRoot` says the mount really lands (POSIX-looking mounts are mapped
  // onto the OS temp dir by the harness).
  const root = storageRoot(MOUNT);
  const outside = path.join(path.dirname(root), `${path.basename(root)}-outside`);
  fs.mkdirSync(path.join(root, 'real'), { recursive: true });
  fs.mkdirSync(outside, { recursive: true });
  fs.writeFileSync(path.join(root, 'real', 'inside.txt'), 'INSIDE');
  fs.writeFileSync(path.join(root, 'plain.txt'), 'PLAIN');
  fs.writeFileSync(path.join(outside, 'secret.txt'), 'SECRET');

  try {
    // `escape` leaves the root: refused unless `follow_symlinks` is on, which
    // it is not here (the default).
    fs.symlinkSync(outside, path.join(root, 'escape'), 'dir');
    // `rel` stays inside it: followed, and therefore an ordinary directory.
    fs.symlinkSync('real', path.join(root, 'rel'), 'dir');
  } catch (err) {
    canSymlink = false;
    // eslint-disable-next-line no-console
    console.warn(`110-symlink-badge: this host cannot create symlinks (${String(err)})`);
    return;
  }

  storageId = (await seedLocalStorage(request, STORAGE, MOUNT)).id;
});

test.afterAll(async ({ request }) => {
  await dropStorageByName(request, STORAGE);
});

async function openStorage(page: Page) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await loginAs(page);
  await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
  await expect(page.getByTestId(`sidenav-storage-${STORAGE}`)).toBeVisible();
  await page.getByTestId(`sidenav-storage-${STORAGE}`).click();
  await expect(page.locator(`[data-fe-path="${STORAGE}://plain.txt"]`)).toBeVisible();
}

const rowFor = (page: Page, name: string) =>
  page.locator(`[data-fe-path="${STORAGE}://${name}"]`).first();

/** Starts a full scan of the storage and waits for a run that had not finished
 *  before it to finish "ok" (the 180-thumbnails pattern). ⚠ A run still
 *  walking when this starts counts: the storage is `fsnotify`, and "Scan now"
 *  answers 202 without a new run while one is already walking. */
async function scanAndWait(request: APIRequestContext, id: number) {
  const runs = async () => {
    const res = await request.get(`/api/admin/storages/${id}/sync-runs?limit=50`);
    expect(res.ok()).toBeTruthy();
    return ((await res.json()) as { entries?: Array<{ id: number; status: string }> }).entries ?? [];
  };
  const before = new Set((await runs()).filter((r) => r.status !== 'running').map((r) => r.id));
  const started = await request.post(`/api/admin/storages/${id}/sync`);
  expect(started.status(), await started.text()).toBeLessThan(300);
  await expect
    .poll(async () => (await runs()).find((r) => !before.has(r.id) && r.status !== 'running')?.status ?? 'pending', {
      timeout: 60_000,
    })
    .toBe('ok');
}

test.describe('A symlink filex will not follow', () => {
  test('is badged, and the badge carries the reason in full', async ({ page }) => {
    test.skip(!canSymlink, 'this host cannot create symlinks');
    await openStorage(page);

    const badge = rowFor(page, 'escape').getByTestId('symlink-badge');
    await expect(badge, 'the out-of-root link is drawn as an ordinary file').toBeVisible();

    const state = await badge.getAttribute('data-link-state');
    expect(STATES, `unknown link state on the wire: ${state}`).toContain(state);

    // ⚠ The badge's own text is two words at most; the accessible name and the
    // tooltip are the SENTENCE. "Outside storage" read aloud explains nothing.
    const label = (await badge.getAttribute('aria-label')) ?? '';
    expect(label.length, 'the badge has no sentence behind it').toBeGreaterThan(40);
    expect(await badge.getAttribute('title')).toBe(label);
    expect(label).toMatch(/cannot be opened|does not follow|will not follow/i);
    // Whichever state arrived, the sentence must not send somebody to a
    // setting that cannot help: only the out-of-root wording names one.
    if (state === 'outside_root' || state === 'unknown') {
      expect(label).toMatch(/outside this storage/i);
    }
    if (state === 'outside_root') {
      expect(label).toContain('Follow symlinks that leave this folder');
    }
  });

  test('is refused out loud when you open it, and the listing stays put', async ({ page }) => {
    test.skip(!canSymlink, 'this host cannot create symlinks');
    await openStorage(page);

    const before = page.url();
    const label = (await rowFor(page, 'escape').getByTestId('symlink-badge').getAttribute('aria-label')) ?? '';

    await rowFor(page, 'escape').dblclick();

    // The toast is the whole point: doing nothing is the reported bug, and a
    // generic failure from the driver's containment check reads as "filex is
    // broken" rather than "this boundary is a setting somebody chose".
    const toast = page.locator('.fe-toast__msg');
    await expect(toast).toBeVisible();
    await expect(toast).toHaveText(label);

    // And nothing opened: no preview, no navigation into a folder.
    await expect(page.locator('.fe-preview')).toHaveCount(0);
    await expect(rowFor(page, 'plain.txt')).toBeVisible();
    expect(page.url()).toBe(before);
  });

  test('an in-root link is followed: it is a folder, and it opens', async ({ page }) => {
    test.skip(!canSymlink, 'this host cannot create symlinks');
    await openStorage(page);

    // The backend half, verified rather than rebuilt. `rel -> real` must look
    // like nothing special at all — no badge, and a double-click navigates.
    const rel = rowFor(page, 'rel');
    await expect(rel).toBeVisible();
    await expect(rel.getByTestId('symlink-badge')).toHaveCount(0);

    await rel.dblclick();
    await expect(page.locator(`[data-fe-path="${STORAGE}://rel/inside.txt"]`)).toBeVisible();
  });

  test('an ordinary file is untouched', async ({ page }) => {
    test.skip(!canSymlink, 'this host cannot create symlinks');
    await openStorage(page);
    await expect(rowFor(page, 'plain.txt').getByTestId('symlink-badge')).toHaveCount(0);
  });

  // ⚠⚠ The steady state. Once the storage has been scanned its folders are
  // answered by the catalogue, and until 0.54 the catalogue kept only THAT a
  // row is a link filex will not follow: the badge that said "Outside storage"
  // before the first scan said the general "Link" every day after it. The scan
  // now records the reason with the row (migration 00098).
  test('after the storage is scanned, the badge still names the reason', async ({ page, request }) => {
    test.skip(!canSymlink, 'this host cannot create symlinks');
    test.setTimeout(90_000);
    await apiLogin(request);
    await scanAndWait(request, storageId);

    // The wire first: the catalogue's listing carries the reason.
    const res = await request.get(`/api/files/manager?action=index&path=${encodeURIComponent(`${STORAGE}://`)}`);
    expect(res.ok(), await res.text()).toBeTruthy();
    const body = (await res.json()) as { files?: Array<{ basename: string; symlink?: boolean; link_state?: string }> };
    const escape = body.files?.find((f) => f.basename === 'escape');
    expect(escape?.symlink, 'the out-of-root link is listed and flagged').toBe(true);
    expect(escape?.link_state, 'the scanned listing dropped the reason').toBe('outside_root');

    // Then the screen: the reason, not the general word.
    await openStorage(page);
    const badge = rowFor(page, 'escape').getByTestId('symlink-badge');
    await expect(badge).toHaveAttribute('data-link-state', 'outside_root');
    await expect(badge).toContainText('Outside storage');
    expect(await badge.getAttribute('aria-label')).toContain('Follow symlinks that leave this folder');
  });
});
