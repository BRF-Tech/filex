// A symlink filex will not follow says so, in words (issue #34).
//
//   node e2e/shots/symlinks.mjs        (from the repo root; `pnpm shots` runs it)
//
// Writes docs/screenshots/<release>/symlinks/:
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
    // ⚠ NOT synced. A listing answered by the driver carries the link's
    // state (`outside_root`), so the badge can say WHERE the target is and
    // name the setting that would allow it; a catalogued row has no column for
    // that state and says only "a link that cannot be opened". Both are
    // honest; the first is the one worth a picture.
    await addLocalStorage(admin, 'projects', root);

    const ctx = await newContext(browser);
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);
    await admin.post('/api/notifications/read-all', {});
    await page.goto(`${inst.url}/admin/explore?storage=projects`);
    await page.getByTestId('view-list').click();
    const link = page.locator('[data-fe-path="projects://archive"]').first();
    await link.waitFor({ timeout: 25_000 });
    await link.getByTestId('symlink-badge').waitFor();
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
