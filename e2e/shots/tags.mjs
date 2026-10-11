// Tags — the two kinds a file can carry, and what opening one shows.
//
//   node e2e/shots/tags.mjs        (from the repo root; `pnpm shots` runs it)
//
// Writes e2e/.artifacts/shots/capture/tags/:
//
//   tags-kinds-1440.png   the explorer: the person's OWN tags and their TEAM's
//                         under their own headings in the navigation panel,
//                         with a team tag opened and its files listed from
//                         every folder they live in
//
// ⚠ The explorer, not Admin → Tagged files. Until 0.56 this picture was the
// admin panel's page, and the README shows it in "The explorer" under a
// caption about the explorer: true of the data, wrong about the screen, and
// an administrator's page is not where most people meet their tags.
//
// ⚠⚠ Why this is its own scene rather than another shot in starstags.mjs: that
// script photographs the explorer's tag PANEL and asserts exact counts on it
// ("8 shown, Show all for the rest"). Seeding a second KIND of tag there to get
// this picture would have moved those numbers, and a picture is not worth
// rewriting somebody else's measurements for.
//
// ⚠ The tag kinds are the v0.43.0 change: a personal tag is visible only to the
// person who put it there, a team tag to everyone in the tenant who can see the
// file. The picture must therefore show BOTH groups — one alone says nothing
// about the distinction, which is the whole feature.
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { chromium } from '@playwright/test';
import { seedFixtures, syncAndWait } from './fixtures.mjs';
import { addLocalStorage, bootInstance, client, log, newContext, shot, signIn, sleep, uploadTree } from './scene.mjs';

const SET = 'tags';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots', display_name: 'Dana Reyes' };

/**
 * Put a whole set of tags on one node, each with its kind.
 *
 * ⚠⚠ `POST /tags` SETS the caller's visible list, it does not add to it
 * (backend/internal/api/handlers/tags.go → `set`). Calling it once per tag
 * quietly leaves each file wearing only the LAST one — which is how the first
 * run of this scene ended up with a tag the picture then could not find.
 * `items` is the v0.43 shape: the whole set, with a kind per name.
 */
async function tagNode(api, nodeId, items) {
  await api.post('/api/files/manager/tags', { node_id: nodeId, items });
}

/** The files filex catalogued under a folder, with their node ids. */
async function filesUnder(api, qualified) {
  const body = await api.json(`/api/files/manager?action=index&path=${encodeURIComponent(qualified)}`);
  return (Array.isArray(body.files) ? body.files : []).filter((f) => f.type === 'file');
}

async function main() {
  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' } });
  const browser = await chromium.launch();
  const seed = mkdtempSync(join(tmpdir(), 'filex-shots-tags-seed-'));
  try {
    const api = client(inst.url);
    await api.login(ADMIN.email, ADMIN.password);
    await api.patch('/api/auth/profile', { locale: 'en', display_name: ADMIN.display_name });

    seedFixtures(seed);
    const demo = await addLocalStorage(api, 'demo', inst.storageRoot('demo'), { onHost: !inst.container });
    await uploadTree(api, 'demo://', seed);
    // One finished sync: uploads catalogue the files but never finish a
    // storage's FIRST sync, and the panel's storage line then reads "at least
    // 78.6 KB used" - true of a half-indexed storage, beside the point here.
    await syncAndWait((_token, path, init) => api.call(path, init), null, demo.id);

    const photos = await filesUnder(api, 'demo://Photos');
    const docs = await filesUnder(api, 'demo://Documents');
    if (photos.length < 3 || docs.length < 2) {
      throw new Error(`not enough seeded files to tag (${photos.length} photos, ${docs.length} documents)`);
    }

    // The team's labels go on work other people need to find; the personal
    // ones are one person's own shelf, on the same tree — and some files wear
    // both, which is the point of the two kinds existing.
    const ours = (name) => ({ name, kind: 'team' });
    const mine = (name) => ({ name, kind: 'personal' });
    await tagNode(api, docs[0].id, [ours('contracts'), ours('q4-launch')]);
    await tagNode(api, docs[1].id, [ours('contracts'), mine('to-read')]);
    await tagNode(api, photos[0].id, [ours('contracts'), mine('to-read')]);
    await tagNode(api, photos[1].id, [ours('contracts'), mine('my-drafts')]);
    await tagNode(api, photos[2].id, [ours('q4-launch')]);

    // What the server says the person can see, before a browser is opened.
    const seen = await api.json('/api/files/manager/tags/all');
    log(`tags: ${JSON.stringify(seen.items ?? seen.tags ?? seen)}`);

    const ctx = await newContext(browser, { height: 1000 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);
    // The uploads above each rang the bell; this picture is about tags.
    await api.post('/api/notifications/read-all', {});

    await page.goto(`${inst.url}/drive/explore`);
    const personal = page.getByTestId('sidenav-tags-personal');
    const team = page.getByTestId('sidenav-tags-team');
    await personal.waitFor({ timeout: 25_000 });
    await team.waitFor({ timeout: 25_000 });

    // Open a TEAM tag, so the list beside the panel is the answer to a
    // question the picture has already asked on screen.
    const contracts = page.locator('[data-testid="sidenav-tag-contracts"][data-tag-kind="team"]');
    await contracts.click();
    // The list, whatever view the account last left: its Location column is
    // what says "from every folder they live in".
    const listView = page.getByTestId('view-list').first();
    if (await listView.isVisible().catch(() => false)) await listView.click();
    await page.waitForFunction(
      () => document.querySelectorAll('.fe-list__row[data-fe-path]').length >= 4,
      undefined,
      { timeout: 20_000 },
    );

    // ⚠ Both headings have to be READABLE in the picture, not merely present
    // in the DOM, and the files must come from more than one folder: one
    // heading alone turns "two kinds of tag" into "a list of tags", and one
    // folder's files say nothing about where a tag reaches.
    const hidden = await page.evaluate(() => {
      const out = [];
      for (const id of ['sidenav-tags-personal', 'sidenav-tags-team']) {
        const el = document.querySelector(`[data-testid="${id}"]`);
        if (!el) return [`${id} is not on the page`];
        const r = el.getBoundingClientRect();
        if (r.top < 0 || r.bottom > window.innerHeight) out.push(`${id} is outside the window`);
      }
      const active = document.querySelector('[data-testid="sidenav-tag-contracts"][data-tag-kind="team"]');
      if (!active?.classList.contains('is-active')) out.push('the team tag "contracts" is not the open view');
      const folders = new Set(
        [...document.querySelectorAll('.fe-list__row[data-fe-path]')].map((r) =>
          (r.getAttribute('data-fe-path') ?? '').replace(/\/[^/]*$/, ''),
        ),
      );
      if (folders.size < 2) out.push(`the tagged files come from ${folders.size} folder(s): ${[...folders].join(', ')}`);
      return out;
    });
    if (hidden.length) throw new Error(`tags-kinds-1440.png would hide half the story: ${hidden.join('; ')}`);

    await page.mouse.move(4, 4);
    await sleep(600);
    await shot(page, SET, 'tags-kinds-1440.png');
    log('personal and team tags, with a team tag opened');
    await ctx.close();
  } finally {
    await browser.close();
    await inst.stop();
    rmSync(seed, { recursive: true, force: true });
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
