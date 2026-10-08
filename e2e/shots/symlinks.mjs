// A symlink filex will not follow says so, in words (issue #34).
//
//   node e2e/shots/symlinks.mjs        (from the repo root; `pnpm shots` runs it)
//
// Writes e2e/.artifacts/shots/capture/symlinks/:
//
//   symlink-badge-1440.png   a `local` storage whose `archive` is a link that
//                            leaves the storage root: badged in the listing,
//                            and the same sentence in its details panel. The
//                            link beside it that stays inside (`current`) is
//                            followed and looks like the folder it is.
//
// ⚠ Needs a host that can create symlinks. Linux and macOS always can; Windows
// only with Developer Mode or elevation. Where it cannot, this script FAILS —
// a picture that silently was not taken is a stale picture in the README.
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { mkdirSync, symlinkSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { chromium } from '@playwright/test';
import { pinTimes } from './clock.mjs';
import { syncAndWait } from './fixtures.mjs';
import { addLocalStorage, bootInstance, client, newContext, shot, signIn, sleep } from './scene.mjs';

const SET = 'symlinks';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };

async function main() {
  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' } });
  const browser = await chromium.launch();
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Demo' });

    // A project folder, a folder beside it that is NOT part of the storage,
    // and two links: one that stays inside the root, one that leaves it.
    const root = join(inst.files, 'projects');
    const outside = join(inst.files, 'old-projects');
    for (const d of [join(root, 'design'), join(root, 'invoices'), outside]) mkdirSync(d, { recursive: true });
    writeFileSync(join(root, 'design', 'logo-final.svg'), '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"/>');
    writeFileSync(join(root, 'invoices', 'INV-2026-09.txt'), 'Invoice 2026-09\n');
    writeFileSync(join(root, 'README.md'), '# Projects\n\nOne folder per client.\n');
    writeFileSync(join(root, 'roadmap.md'), '# Roadmap\n\n- Q4: launch\n');
    writeFileSync(join(outside, '2019-report.txt'), 'kept for the record\n');
    try {
      symlinkSync(outside, join(root, 'archive'), 'dir');
      symlinkSync('design', join(root, 'current'), 'dir');
    } catch (err) {
      throw new Error(
        `this host cannot create symlinks (${err.code ?? err.message}) — on Windows enable Developer Mode, ` +
          'or take this picture on Linux/macOS',
      );
    }
    pinTimes(root);
    // ⚠ Synced, and the sync waited for. Until 0.53.0 the storage was left
    // unsynced on purpose: a listing answered by the driver carried the
    // link's state (`outside_root`), so the badge said WHERE the target is,
    // while a catalogued row had no column for it and said only "Link" and
    // the general sentence. The price was a "This storage's first sync has
    // not finished" strip across the README's picture of a storage that is
    // fine. Since 0.54 the sync records the reason with the row (migration
    // 00098), so the synced listing - what a person sees every day after the
    // first scan - says "Outside storage" too, and the picture waits for it.
    const storage = await addLocalStorage(admin, 'projects', root);
    await syncAndWait((_token, path, init) => admin.call(path, init), null, storage.id);

    const ctx = await newContext(browser);
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);
    await admin.post('/api/notifications/read-all', {});
    await page.goto(`${inst.url}/admin/explore?storage=projects`);
    await page.getByTestId('view-list').click();
    const link = page.locator('[data-fe-path="projects://archive"]').first();
    await link.waitFor({ timeout: 25_000 });
    // The reason, not the general "Link": a picture of the general word is a
    // picture of the 0.53 catalogue, and it fails here rather than ships.
    const badge = link.locator('[data-testid="symlink-badge"][data-link-state="outside_root"]');
    await badge.waitFor({ timeout: 15_000 });
    const words = (await badge.textContent()) ?? '';
    if (!words.includes('Outside storage')) {
      throw new Error(`the symlink badge reads "${words.trim()}", expected "Outside storage"`);
    }
    // Selected, with its details open: the badge's two words, and the whole
    // sentence where somebody reads about the file.
    await link.locator('.fe-list__check').first().click();
    await page.getByTestId('tabs-inspector').click();
    await page.getByTestId('inspector-symlink').waitFor({ timeout: 15_000 });
    await page.mouse.move(0, 0);
    await sleep(600);
    await shot(page, SET, 'symlink-badge-1440.png');
    await ctx.close();
  } finally {
    await browser.close();
    await inst.stop();
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
