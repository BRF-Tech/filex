// A Mermaid diagram (.mmd) - looked at, and edited as its text (filex 0.56,
// #216: Edit used to open the diagram viewer again, which only draws and has
// no save):
//
//   node e2e/shots/mermaid.mjs     (from the repo root; `pnpm shots` runs it)
//
// Writes e2e/.artifacts/shots/capture/mermaid/ (or SHOTS_OUT):
//
//   mermaid-view-1280.png  a .mmd file opened: the diagram drawn in the
//                          browser, with Edit in the viewer's bar
//   mermaid-edit-1280.png  what Edit opens: the diagram's source in the
//                          built-in text editor, a line added, Save ready
//
// The scene then presses Save and requires save-text to take it: a picture of
// an editor that cannot save is the fault #216 fixed. Mermaid needs no
// service - the library is in the web build - so nothing outside this
// machine is reached.
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { chromium } from '@playwright/test';
import { pinTimes } from './clock.mjs';
import { addLocalStorage, bootInstance, client, dismissToasts, log, newContext, shot, signIn, sleep, uploadTree } from './scene.mjs';

const SET = 'mermaid';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
const FILE = 'Release flow.mmd';
const SOURCE = `flowchart LR
  merge[Merge to main] --> nightly{Nightly run green?}
  nightly -- yes --> rc[Release candidate]
  nightly -- no --> fix[Fix what is red]
  fix --> merge
  rc --> pictures[Screenshots looked at]
  pictures --> publish[Publish]
`;
const ADDED = '  publish --> announce[Announce]';

async function main() {
  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' } });
  const browser = await chromium.launch();
  const seed = mkdtempSync(join(tmpdir(), 'filex-shots-mermaid-'));
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Dana Reyes' });
    writeFileSync(join(seed, FILE), SOURCE);
    writeFileSync(join(seed, 'README.md'), '# Release\n\nHow a release goes out: Release flow.mmd.\n');
    pinTimes(seed);
    const docs = await addLocalStorage(admin, 'Docs', inst.storageRoot('docs'), { onHost: !inst.container });
    await uploadTree(admin, 'Docs://', seed);

    const ctx = await newContext(browser, { width: 1280, height: 860 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);
    await page.goto(`${inst.url}/admin/explore?storage=Docs`);
    const row = page.locator(`[data-fe-path="Docs://${FILE}"]`).first();
    await row.waitFor({ timeout: 25_000 });
    await sleep(700);
    await row.dblclick();

    // ── 1. the diagram, drawn ───────────────────────────────────────────
    const drawn = page.locator('.filex-viewer-mermaid__surface svg').first();
    await drawn.waitFor({ state: 'visible', timeout: 30_000 });
    const nodes = await page.locator('.filex-viewer-mermaid__surface svg .node').count();
    if (nodes < 6) throw new Error(`the diagram drew ${nodes} of its 6 boxes`);
    const edit = page.locator('button.fe-viewer__act[title="Edit"]');
    await edit.waitFor({ state: 'visible', timeout: 10_000 });
    await dismissToasts(page);
    await page.mouse.move(4, 4);
    await sleep(600);
    await shot(page, SET, 'mermaid-view-1280.png');

    // ── 2. Edit: its text, in the text editor, saved as text ────────────
    const opened = ctx.waitForEvent('page', { timeout: 15_000 });
    await edit.click();
    const tab = await opened;
    await tab.setViewportSize({ width: 1280, height: 860 });
    const editor = tab.locator('.fe-preview__code-editor:not(.is-hidden)');
    await editor.waitFor({ state: 'visible', timeout: 30_000 });
    if ((await tab.locator('.filex-viewer-mermaid').count()) !== 0) throw new Error('Edit opened the diagram viewer again, which has no save (#216)');
    await tab.getByText('nightly -- no', { exact: false }).first().waitFor({ timeout: 15_000 });
    // The editor's badge names what the file is: a diagram's source, not
    // "PLAINTEXT" (the first 0.56 take showed that; fixed in PreviewModal).
    const badge = (await tab.getByTestId('code-lang').innerText()).trim();
    if (!/^mermaid$/i.test(badge)) throw new Error(`the editor's badge over a Mermaid diagram's source says "${badge}", not mermaid`);
    // The cursor to the end of the source, and one more step in the flow.
    await editor.click();
    await tab.keyboard.press('ControlOrMeta+End');
    await tab.keyboard.type(ADDED, { delay: 10 });
    await tab.evaluate(() => document.activeElement?.blur?.());
    await dismissToasts(tab);
    await tab.mouse.move(4, 4);
    await sleep(600);
    await shot(tab, SET, 'mermaid-edit-1280.png');
    const saved = tab.waitForResponse((r) => r.url().includes('/api/files/save-text') && r.request().method() === 'POST', { timeout: 15_000 });
    await tab.getByRole('button', { name: /^Save/ }).first().click();
    const res = await saved;
    if (res.status() !== 200) throw new Error(`Save answered ${res.status()}: the text editor did not save the diagram`);
    const sent = res.request().postDataJSON();
    if (!String(sent?.content ?? '').includes('announce[Announce]')) {
      throw new Error(`Save sent the diagram without the added line: ${JSON.stringify(sent).slice(0, 400)}`);
    }
    const back = await admin.call(`/api/files/read?storage=${docs.id}&path=${encodeURIComponent(FILE)}`);
    const text = await back.text();
    if (!text.includes('announce[Announce]')) throw new Error(`the saved diagram does not hold the added line: ${back.status} ${text.slice(0, 300)}`);
    log('a Mermaid diagram is drawn, and Edit opens its text, which Save keeps');
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
