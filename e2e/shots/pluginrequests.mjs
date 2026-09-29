// Plugin install requests — what an API key leaves instead of installing.
//
//   node e2e/shots/pluginrequests.mjs     (from the repo root; `pnpm shots` runs it)
//
// Writes docs/screenshots/<release>/pluginrequests/:
//
//   requests-1440.png   Plugins page: the "Install requests" panel above the
//                       tabs — two requests an agent's API key left (the
//                       e-Signature app, the Spanish language pack), who asked
//                       through which key, and why
//   review.png          one request's review: the frozen SHA-256, the source,
//                       the requester's words and the permissions the app asks
//                       for (the install wizard's own list), "I understand"
//                       and "Approve and install"
//
// ⚠⚠ The requests are left THE WAY AN AGENT LEAVES THEM: with an API key
// minted for the scene (`work-agent`, scopes admin + read), not with the
// administrator's session. A picture of requests the administrator made
// herself would show a flow nobody uses — and the key is refused if it tries
// to install (checked below, before the shutter).
//
// ⚠ The source is a small HTTP server on 127.0.0.1 inside this script (plain
// http is accepted for loopback only, wasmplugin/fetch.go), serving the builds
// the apps scene uses (e2e/helpers/app-locations.mjs → findApp): nothing here
// reaches GitHub.
//
// Environment: FILEX_BIN, FILEX_SIGN_APP_DIR, FILEX_LANG_ES_APP_DIR, SHOTS_OUT,
// SHOTS_KEEP (see apps.mjs).

import { createServer } from 'node:http';
import { readFileSync } from 'node:fs';
import { chromium } from '@playwright/test';
import { bootInstance, client, findApp, log, newContext, shot, signIn, sleep } from './scene.mjs';

const SET = 'pluginrequests';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };

/** Serves `files` (path → bytes) on a free loopback port. */
async function serve(files) {
  const srv = createServer((req, res) => {
    const body = files.get((req.url ?? '').split('?')[0]);
    if (!body) {
      res.writeHead(404).end();
      return;
    }
    res.writeHead(200, { 'Content-Type': 'application/octet-stream' }).end(body);
  });
  await new Promise((ok) => srv.listen(0, '127.0.0.1', ok));
  return { origin: `http://127.0.0.1:${srv.address().port}`, close: () => new Promise((ok) => srv.close(() => ok())) };
}

/** A call made with the agent's API key, never the administrator's session. */
function keyClient(url, key) {
  return async (path, body) => {
    const res = await fetch(`${url}${path}`, {
      method: body ? 'POST' : 'GET',
      headers: { Authorization: `Bearer ${key}`, ...(body ? { 'Content-Type': 'application/json' } : {}) },
      body: body ? JSON.stringify(body) : undefined,
    });
    const text = await res.text();
    return { status: res.status, body: text ? JSON.parse(text) : {} };
  };
}

/**
 * Grows the window until the dialog fits, then shoots it (apps.mjs →
 * shootReview).
 *
 * ⚠ The <dialog> is scrolled back to its top before every measurement: ticking
 * "I understand" (at the bottom of a review taller than the window) scrolls
 * it, the box then starts ABOVE the window (y < 0), and growing the window
 * alone never brings it back — the first take gave up with the whole review
 * inside a window it would have fitted.
 */
async function shootDialog(page, dialog, file) {
  let fits = '';
  for (let i = 0; i < 6; i++) {
    await page.evaluate(() => document.querySelector('dialog[open]')?.scrollTo(0, 0));
    await sleep(200);
    const box = await dialog.boundingBox();
    const view = page.viewportSize();
    if (!box || !view) throw new Error('the request review has no box to measure');
    if (box.y >= 0 && box.y + box.height <= view.height) {
      fits = 'yes';
      break;
    }
    fits = `${Math.ceil(box.y + box.height)}px of dialog (from ${Math.round(box.y)}) in a ${view.height}px window`;
    await page.setViewportSize({ width: view.width, height: Math.min(2600, Math.ceil(Math.max(0, box.y) + box.height + 96)) });
    await sleep(300);
  }
  if (fits !== 'yes') throw new Error(`the request review does not fit the window (${fits}) — the picture would be stitched`);
  await shot(dialog, SET, file);
}

async function main() {
  const sign = findApp('sign');
  const pack = findApp('lang-es');
  log(`sign ${sign.manifest.version}, ${pack.manifest.name} ${pack.manifest.version}`);

  const files = new Map([
    ['/sign/filex-app.json', readFileSync(sign.manifestPath)],
    ['/sign/plugin.wasm', readFileSync(sign.wasm)],
    ['/lang-es/filex-app.json', readFileSync(pack.manifestPath)],
  ]);
  const source = await serve(files);
  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' } });
  const browser = await chromium.launch();
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Demo' });
    const minted = await admin.post('/api/admin/ai-tokens', { label: 'work-agent', scopes: 'admin,read' });
    const agent = keyClient(inst.url, minted.token);

    // The key cannot install — the reason this screen exists.
    const refused = await agent('/api/admin/app-plugins', {
      url: `${source.origin}/sign/plugin.wasm`, manifest_url: `${source.origin}/sign/filex-app.json`,
      permissions: sign.manifest.permissions,
    });
    if (refused.status !== 403 || refused.body.error !== 'session_required') {
      throw new Error(`the API key was not refused an install (${refused.status} ${JSON.stringify(refused.body)})`);
    }

    const asks = [
      {
        kind: 'app', url: `${source.origin}/sign/plugin.wasm`, manifest_url: `${source.origin}/sign/filex-app.json`,
        reason: 'The legal team wants to sign contracts inside filex instead of emailing PDFs around.',
      },
      {
        kind: 'app', manifest_url: `${source.origin}/lang-es/filex-app.json`,
        reason: 'The Madrid office asked for the interface in Spanish.',
      },
    ];
    const made = [];
    for (const a of asks) {
      const res = await agent('/api/admin/plugin-requests', a);
      if (res.status !== 201) throw new Error(`request ${a.manifest_url}: ${res.status} ${JSON.stringify(res.body)}`);
      if (res.body.request.status !== 'pending') throw new Error(`a new request is ${res.body.request.status}, not pending`);
      made.push(res.body.request);
    }
    const signReq = made[0];
    if (signReq.sha256 !== sign.manifest.wasm.sha256) {
      throw new Error(`the request froze ${signReq.sha256}, the module is ${sign.manifest.wasm.sha256}`);
    }
    await admin.post('/api/notifications/read-all', {});

    const ctx = await newContext(browser, { height: 1000 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);
    await page.goto(`${inst.url}/admin/plugins`);
    const panel = page.getByTestId('plugin-requests');
    await panel.waitFor({ timeout: 20_000 });
    for (const r of made) await page.getByTestId(`plugin-request-${r.id}`).waitFor({ timeout: 20_000 });
    const text = (await panel.innerText()).replace(/\s+/g, ' ');
    for (const want of ['Install requests', 'e-Signature', 'API key “work-agent”', 'The legal team wants']) {
      if (!text.includes(want)) throw new Error(`the requests panel does not say "${want}": "${text.slice(0, 500)}"`);
    }
    await shot(panel, SET, 'requests-1440.png');

    await page.getByTestId(`plugin-request-actions-${signReq.id}`).click();
    await page.getByTestId(`plugin-request-actions-${signReq.id}-review`).click();
    const review = page.getByTestId('plugin-request-review');
    await review.waitFor({ timeout: 15_000 });
    const perms = review.getByTestId('app-plugin-permissions');
    await perms.waitFor({ timeout: 15_000 });
    // ⚠ The app's own reasons, in words — never a raw {"en": …} map
    // (apps.mjs refuses the same on the install review).
    const permText = await perms.innerText();
    if (permText.includes('{"') || permText.includes('No reason given')) {
      throw new Error(`the permissions read as raw or reasonless: "${permText.slice(0, 300)}"`);
    }
    const shown = (await review.getByTestId('plugin-request-sha256').innerText()).trim();
    if (shown !== signReq.sha256) throw new Error(`the review shows ${shown}, the request froze ${signReq.sha256}`);
    await review.locator('input[name="plugin-request-understand"]').check();
    const dialog = page.locator('[role="dialog"]').filter({ has: review });
    await shootDialog(page, dialog, 'review.png');
    await ctx.close();
  } finally {
    await browser.close();
    await inst.stop();
    await source.close();
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
