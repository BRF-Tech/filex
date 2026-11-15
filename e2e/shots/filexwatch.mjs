// filex-watch (0.61, task #352, docs/FILEX-WATCH.md): the card on a storage's
// page for the agent that runs on the machine that owns the storage's disk.
//
//   node e2e/shots/filexwatch.mjs     (from the repo root; `pnpm shots` runs it)
//
// Writes e2e/.artifacts/shots/capture/filexwatch/ (or SHOTS_OUT):
//
//   filex-watch-key-1440.png  Admin -> Storages -> a storage -> the filex-watch
//                             card right after "Make a key": the key shown
//                             once, the keys' table, the Linux install commands
//   filex-watch-1440.png      the same card once the key is copied: two keys,
//                             one with what its agent last said (version, host,
//                             folder, last report), the platforms to install on
//
// It also MEASURES, in a real browser: the agent's report reaches the card (a
// report sent with the real key, as filex-watch sends it), the key is refused
// at every door but its own, and at 958 and 1440 px, in English and Turkish,
// the storage page with the card has no horizontal scroll and nothing of its
// own sticks out. A failed measurement throws; the pictures are only written
// by a run that passed it.
//
// ⚠ The key in the pictures is a fixed example (the answer is rewritten on its
// way to the page): a real key would make the picture change on every run, and
// a picture of a credential is not one to publish. The measurements use the
// real one.
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { mkdirSync } from 'node:fs';
import { chromium } from '@playwright/test';
import { bootInstance, client, dismissToasts, layoutProblems, log, mustSay, newContext, setLanguage, shot, signIn, sleep } from './scene.mjs';

export const INPUTS = [
  'web/src/components/StorageWatchAgent.vue',
  'web/src/api/storageWatch.ts',
  'web/src/views/StorageEdit.vue',
  'packages/core/src/components/DataTable.vue',
  'packages/core/src/components/ChoiceButtons.vue',
  'backend/internal/api/handlers/storage_watch.go',
  'backend/internal/watchproto',
  'backend/internal/storage/watchagent.go',
];

const SET = 'filexwatch';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
/** What the pictures show instead of the key the server made. */
const EXAMPLE_KEY = 'fxw_EXAMPLE-shown-once-copy-it-now-7Rk2Vq9Ld4';
/** What the agent on the NAS says about itself in its reports. */
const AGENT = { id: 'a1b2c3d4e5f60718', version: '0.1.0', host: 'nas01', os: 'linux', arch: 'amd64', root: '/volume1/media' };

async function main() {
  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' } });
  const browser = await chromium.launch();
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Dana Reyes' });

    // A storage whose folder is, in the story, a NAS share mounted here.
    const root = inst.storageRoot('media');
    if (!inst.container) mkdirSync(root, { recursive: true });
    const media = await admin.post('/api/admin/storages', {
      name: 'Media',
      driver: 'local',
      mount_path: root,
      config: { path: root },
      sync_mode: 'ondemand',
      sync_interval_s: 0,
      enabled: true,
      read_only: false,
    });

    // The NAS's key, and the report its agent sends at start (a resync),
    // with the real key - as filex-watch sends it.
    const nas = await admin.post(`/api/admin/storages/${media.id}/watch/keys`, { name: 'nas01' });
    if (!nas?.key?.startsWith('fxw_')) throw new Error('the server answered no filex-watch key');
    const report = (key, body) =>
      fetch(`${inst.url}/api/storages/${media.id}/watch/events`, {
        method: 'POST',
        headers: { Authorization: `Bearer ${key}`, 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
    const first = await report(nas.key, { protocol: 1, agent: AGENT, seq: 1, resync: true, folders: ['/Photos/2026'] });
    const ans = await first.json();
    if (first.status !== 202 || ans.accepted !== true || !ans.instance) throw new Error(`report: ${first.status} ${JSON.stringify(ans)}`);
    // The key opens its own door and no other.
    for (const [method, path] of [['GET', '/api/ai/root'], ['GET', '/api/admin/storages'], ['GET', '/api/auth/me']]) {
      const res = await fetch(`${inst.url}${path}`, { method, headers: { Authorization: `Bearer ${nas.key}` } });
      if (res.status !== 401 && res.status !== 403) throw new Error(`a filex-watch key opened ${method} ${path}: ${res.status}`);
    }

    const ctx = await newContext(browser, { height: 1000 });
    const page = await ctx.newPage();
    // The key made in the browser is rewritten on its way to the page.
    await page.route('**/api/admin/storages/*/watch/keys', async (route) => {
      if (route.request().method() !== 'POST') return route.continue();
      const res = await route.fetch();
      const body = await res.json();
      const shown = JSON.parse(JSON.stringify(body).split(body.key).join(EXAMPLE_KEY));
      await route.fulfill({ response: res, json: shown });
    });
    await signIn(page, inst.url, ADMIN);
    const card = page.getByTestId('storage-watch');
    const open = async (width) => {
      await page.setViewportSize({ width, height: 1000 });
      await page.goto(`${inst.url}/admin/storages/${media.id}`);
      await card.waitFor({ timeout: 20_000 });
      await page.getByTestId(`storage-watch-row-${nas.row.id}`).waitFor({ timeout: 10_000 });
      await page.mouse.move(4, 4);
      await sleep(400);
    };

    // ── measure: both widths, both languages ────────────────────────────────
    const problems = [];
    for (const locale of ['en', 'tr']) {
      await setLanguage(admin, page, locale);
      for (const width of [958, 1440]) {
        await open(width);
        for (const x of await layoutProblems(page, { frames: ['storage-watch'], scrollers: ['[data-testid="storage-watch-command"]'] })) {
          problems.push(`${locale} ${width}px: ${x}`);
        }
      }
      if (locale === 'tr') {
        await mustSay(card, 'the Turkish card', ['Başka makinedeki değişiklikler (filex-watch)', 'Canlı izleme yeter', 'Anahtar oluştur', 'nas01']);
      }
    }
    if (problems.length) throw new Error(`layout problems:\n  ${problems.join('\n  ')}`);
    log('no overflow or overlap at 958 and 1440 px, in English and Turkish');

    // ── pictures, English ───────────────────────────────────────────────────
    await setLanguage(admin, page, 'en');
    await open(1440);
    await mustSay(card, 'the card with the agent heard', ['Changes made on another machine (filex-watch)', 'nas01', '0.1.0 · nas01 · linux/amd64', '/volume1/media']);

    // 1. Right after "Make a key": the key once, the Linux commands with it.
    await page.getByTestId('storage-watch-name').locator('input').fill('backup-server');
    await page.getByTestId('storage-watch-create').click();
    await page.getByTestId('storage-watch-key').waitFor({ timeout: 10_000 });
    const command = page.getByTestId('storage-watch-command');
    await mustSay(card, 'the key shown once', [EXAMPLE_KEY, 'The key is shown this once', 'backup-server']);
    await mustSay(command, 'the Linux commands', ['systemctl enable --now filex-watch', EXAMPLE_KEY]);
    if ((await card.innerText()).includes(nas.key)) throw new Error('a real key is on the screen');
    await dismissToasts(page);
    await card.scrollIntoViewIfNeeded();
    await page.mouse.move(4, 4);
    await sleep(400);
    await shot(card, SET, 'filex-watch-key-1440.png');

    // 2. Copied: the keys, the agent's word, the commands without a key.
    await page.getByTestId('storage-watch-made-done').click();
    await page.getByTestId('storage-watch-made').waitFor({ state: 'detached', timeout: 10_000 });
    await mustSay(command, 'the commands without the key', ['PASTE-THE-KEY-HERE']);
    await page.mouse.move(4, 4);
    await sleep(400);
    await shot(card, SET, 'filex-watch-1440.png');

    log('the card after Make a key (an example key) and with the agent heard; the report and the closed doors measured');
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
