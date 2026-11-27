// MCP clients that connect with OAuth (0.62, docs/MCP.md → Connecting with
// OAuth): the page a person sees before an app may use their files.
//
//   node e2e/shots/mcpoauth.mjs     (from the repo root; `pnpm shots` runs it)
//
// Writes e2e/.artifacts/shots/capture/mcpoauth/ (or SHOTS_OUT):
//
//   consent-800.png   the consent page: which app asks, at which address, as
//                     which account, what it may do, where the browser goes
//                     back to, Deny and Allow access
//
// The way there is a client's: dynamic registration (RFC 7591) with a
// callback of the app's own, an authorization request with S256 PKCE and the
// MCP endpoint as its resource, opened in a browser that is not signed in -
// so filex's sign-in comes first and hands the person back to the waiting
// authorization (Login.vue's resume, task #374).
//
// It also MEASURES what the client gets once the person allows it: the code
// goes back to the callback with `state` and `iss`, the token endpoint trades
// it (with the verifier) for an `fxo_` key, the key lists the MCP tools at
// /api/ai/mcp, and the same key opens nothing else (/api/ai/root answers 401
// or 403).
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { createHash, randomBytes } from 'node:crypto';
import { chromium } from '@playwright/test';
import { PUBLIC_URL, bootInstance, client, log, mustSay, newContext, shot, sleep } from './scene.mjs';

export const INPUTS = [
  'backend/internal/api/handlers/mcp_oauth.go',
  'backend/internal/api/handlers/mcp_oauth_page.go',
  'backend/internal/api/handlers/mcp_oauth_tokens.go',
  'backend/internal/auth/apitoken_middleware.go',
  'web/src/views/Login.vue',
  'web/src/lib/loginReturn.ts',
];

const SET = 'mcpoauth';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
const APP_NAME = 'Example Agent';
const CALLBACK = 'https://agent.example.com/oauth/callback';
const RESOURCE = `${PUBLIC_URL}/api/ai/mcp`;

const b64url = (buf) => buf.toString('base64').replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');

/** Throws unless a fetch answered `status`. */
function must(res, status, what) {
  if (res.status !== status) throw new Error(`${what}: ${res.status}, want ${status}`);
}

async function main() {
  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' } });
  const browser = await chromium.launch();
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Dana Reyes' });
    await admin.post('/api/notifications/read-all', {});

    // ── the client registers itself ──────────────────────────────────────
    const reg = await fetch(`${inst.url}/api/oauth/register`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        client_name: APP_NAME,
        redirect_uris: [CALLBACK],
        token_endpoint_auth_method: 'none',
        grant_types: ['authorization_code', 'refresh_token'],
        response_types: ['code'],
      }),
    });
    const regText = await reg.text();
    if (reg.status !== 201 && reg.status !== 200) throw new Error(`registration: ${reg.status} ${regText.slice(0, 300)}`);
    const clientID = JSON.parse(regText).client_id;
    if (!clientID) throw new Error(`registration answered no client_id: ${regText.slice(0, 300)}`);

    const verifier = b64url(randomBytes(32));
    const challenge = b64url(createHash('sha256').update(verifier).digest());
    const state = b64url(randomBytes(12));
    const q = new URLSearchParams({
      response_type: 'code',
      client_id: clientID,
      redirect_uri: CALLBACK,
      code_challenge: challenge,
      code_challenge_method: 'S256',
      scope: 'read write mcp',
      state,
      resource: RESOURCE,
    });

    // ── a browser that is not signed in: sign-in first, then the consent ─
    const ctx = await newContext(browser, { width: 800, height: 620 });
    // The browser is sent to the app's callback by a redirect, which a route
    // does not see in Chromium: the request itself is what is read (the app's
    // host need not answer).
    let answered = null;
    ctx.on('request', (req) => {
      if (req.url().startsWith(`${CALLBACK}?`)) answered = req.url();
    });
    const page = await ctx.newPage();
    await page.goto(`${inst.url}/api/oauth/authorize?${q}`);
    await page.waitForURL(/\/admin\/login/, { timeout: 20_000 });
    await page.fill('#email', ADMIN.email);
    await page.fill('#password', ADMIN.password);
    await page.click('button[type="submit"]');
    const form = page.locator('form[action$="/api/oauth/authorize"]');
    await form.waitFor({ timeout: 20_000 });
    await sleep(300);
    await page.mouse.move(4, 4);
    const main = page.locator('main');
    await mustSay(main, 'the consent page', [APP_NAME, new URL(PUBLIC_URL).host, ADMIN.email, 'Allow access', 'Deny', 'agent.example.com']);
    await shot(page, SET, 'consent-800.png');

    // ── Allow access: the code goes back to the app ──────────────────────
    await page.getByRole('button', { name: 'Allow access' }).click();
    const deadline = Date.now() + 15_000;
    while (!answered && Date.now() < deadline) await sleep(100);
    if (!answered) {
      const said = (await page.locator('body').innerText().catch(() => '')).replace(/\s+/g, ' ').slice(0, 400);
      throw new Error(`Allow access did not send the browser back to the app: at ${page.url()}, "${said}"`);
    }
    const back = new URL(answered);
    const code = back.searchParams.get('code');
    if (!code) throw new Error(`the callback carries no code: ${back.search.slice(0, 200)}`);
    if (back.searchParams.get('state') !== state) throw new Error('the callback lost the state');
    if (!back.searchParams.get('iss')) throw new Error('the callback carries no iss (RFC 9207)');
    await ctx.close();

    // ── the token, and what it opens ─────────────────────────────────────
    const tok = await fetch(`${inst.url}/api/oauth/token`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: new URLSearchParams({
        grant_type: 'authorization_code',
        code,
        redirect_uri: CALLBACK,
        client_id: clientID,
        code_verifier: verifier,
        resource: RESOURCE,
      }),
    });
    const tokText = await tok.text();
    if (tok.status !== 200) throw new Error(`the token endpoint: ${tok.status} ${tokText.slice(0, 300)}`);
    const access = JSON.parse(tokText).access_token;
    if (!/^fxo_[0-9a-f]{64}$/.test(access ?? '')) throw new Error('the token endpoint answered no fxo_ key');
    let session = null;
    const mcp = (body) =>
      fetch(`${inst.url}/api/ai/mcp`, {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${access}`,
          'Content-Type': 'application/json',
          Accept: 'application/json, text/event-stream',
          ...(session ? { 'Mcp-Session-Id': session } : {}),
        },
        body: JSON.stringify(body),
      });
    const init = await mcp({ jsonrpc: '2.0', id: 1, method: 'initialize', params: { protocolVersion: '2025-06-18', capabilities: {}, clientInfo: { name: 'filex-shots', version: '1' } } });
    must(init, 200, 'MCP initialize with the OAuth key');
    session = init.headers.get('mcp-session-id');
    await init.text();
    const ready = await mcp({ jsonrpc: '2.0', method: 'notifications/initialized' });
    if (ready.status >= 300) throw new Error(`MCP notifications/initialized: ${ready.status}`);
    await ready.text();
    const tools = await mcp({ jsonrpc: '2.0', id: 2, method: 'tools/list', params: {} });
    must(tools, 200, 'MCP tools/list with the OAuth key');
    if (!(await tools.text()).includes('file_list')) throw new Error('tools/list does not name file_list');
    const other = await fetch(`${inst.url}/api/ai/root`, { headers: { Authorization: `Bearer ${access}` } });
    if (other.status !== 401 && other.status !== 403) throw new Error(`the OAuth key opened /api/ai/root: ${other.status}`);

    log(`the consent page; code (state, iss), token, MCP initialize + tools/list measured, /api/ai/root ${other.status}`);
  } finally {
    await browser.close();
    await inst.stop();
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
