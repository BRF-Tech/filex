// Storage connections, in the desktop app.
//
// The point of this script is that the desktop app does NOT have its own
// connections screen. It mounts `<filex-connections>` from the same npm
// package as the file explorer, so what is measured here is the shared
// component running inside Electron: the generated "how to connect" page,
// built from the live deployment.
//
// ⚠⚠ There is no storage form here any more. v0.43.0 removed the panel's
// Storages tab on every surface — the owner, testing the release: "bu depolar
// sekmesine hiç ihtiyaç yok". Storages are created and edited in the admin
// panel, so this script CREATES its storage through the API (the guide needs
// one to name) instead of typing it into a form that no longer exists. Do not
// re-add the form checks: they would be measuring a screen the product does
// not have.
//
// It also re-measures the trap that cost a shipped bug in v0.19.0: a
// language switch must change the TEXT ON SCREEN inside the component, not
// merely its `locale` property.
//
// Run:
//   FILEX_SERVER=http://127.0.0.1:5299 FILEX_EMAIL=… FILEX_PASSWORD=… \
//     node scripts/connections-e2e.mjs
//
// ⚠ Point it at a THROWAWAY server. It creates and deletes a storage
// (through the admin API, not the UI).

import fs from 'node:fs';
import path from 'node:path';
import { SHOTS, SERVER, api, check, finish, launchApp, signIn, skipTour, sleep } from './lib/harness.mjs';

const STORAGE = process.env.CONN_STORAGE_NAME ?? 'desktop-conn-e2e';
// A path the SERVER can see — not this machine's view of it.
const MOUNT = process.env.CONN_STORAGE_PATH ?? '/tmp/filex-desktop-conn-e2e';

fs.mkdirSync(SHOTS, { recursive: true });
const shot = async (win, name) => {
  const p = path.join(SHOTS, `${name}.png`);
  await win.screenshot({ path: p });
  console.log(`      ↳ ${p}`);
};

const { app, profile, home } = await launchApp({ lang: 'en-US' });
const { win, adminToken } = await signIn(app, { label: 'filex desktop — connections e2e' });
await skipTour(win);

// Clean slate on the server, so a rerun measures a real creation.
async function dropStorage() {
  const res = await api('/api/admin/storages', {}, adminToken);
  if (!res.ok) return;
  for (const s of await res.json()) {
    if (s.name === STORAGE) {
      await api(`/api/admin/storages/${s.id}`, { method: 'DELETE' }, adminToken);
    }
  }
}
await dropStorage();

// The storage the guide names. ⚠ Through the API: the desktop app has no
// storage form since v0.43.0, and the admin panel is where one is typed.
{
  const res = await api(
    '/api/admin/storages',
    {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ name: STORAGE, driver: 'local', config: { path: MOUNT } }),
    },
    adminToken,
  );
  check('a storage exists for the guide to name', res.ok, `HTTP ${res.status}`);
}

// ── the entry point: Settings, not the admin panel ───────────────────
await win.evaluate(() => {
  const gear = [...document.querySelectorAll('.rail-btn')].pop();
  gear?.click();
});
await sleep(500);

const settingsText = await win.evaluate(() => document.querySelector('#settings')?.innerText ?? '');
check('Settings offers storage connections', /Storage connections/.test(settingsText),
  settingsText.split('\n').find((l) => /connection/i.test(l)) ?? 'not found');
check('…and it no longer says the server settings live elsewhere',
  !/live in its admin panel/.test(settingsText));

await win.evaluate(() => document.querySelector('#conn-open')?.click());
await sleep(1200);

check('the panel opens as its own surface',
  await win.evaluate(() => document.querySelector('#conn')?.classList.contains('open') === true));
check('…and it is the SHARED component, not a copy in app.html',
  await win.evaluate(() => !!document.querySelector('filex-connections [data-testid="connections-panel"]')));
check('…with the explorer hidden behind it',
  await win.evaluate(() => document.querySelector('#explorer-host')?.style.display === 'none'));
await shot(win, 'desktop-connections-guide-open');

// ── nothing but the guides ───────────────────────────────────────────
// ⚠ The absence IS the feature here. A tab strip or a storage form appearing
// in Electron while the web app has neither would be exactly the per-surface
// drift this shared component exists to prevent.
const stray = await win.evaluate(() =>
  ['tab-storages', 'tab-connect', 'storage-list', 'storage-add', 'storage-form', 'no-admin']
    .filter((id) => !!document.querySelector(`[data-testid="${id}"]`)));
check('the panel is the guides only — no tab strip, no storage form',
  stray.length === 0, stray.join(', ') || 'none');
check('…and the protocol picker is on screen without anything being clicked',
  await win.evaluate(() => !!document.querySelector('[data-testid="guide-protocol"]')));

// ── the instruction page ─────────────────────────────────────────────
await sleep(900);
const facts = await win.evaluate(
  () => document.querySelector('[data-testid="guide-facts"]')?.innerText ?? '');
const host = new URL(SERVER).host;
check('the guide names THIS deployment, not a documentation placeholder',
  facts.includes(host) && facts.includes('/dav/'), facts.split('\n')[1] ?? facts);
check('…and the caller’s own account as the username',
  facts.includes(process.env.FILEX_EMAIL ?? ''), facts);

const guideBody = await win.evaluate(() => document.querySelector('.fe-guide__body')?.innerText ?? '');
check('the Windows page carries the registry limits',
  /FileSizeLimitInBytes/.test(guideBody) && /net use/.test(guideBody));
await shot(win, 'desktop-connections-guide');

// The copy button. Electron's app:// scheme is registered secure, so the
// async clipboard API is the path taken here — the opposite of the web
// suite, which runs on plain http and exercises the fallback.
await win.evaluate(() => document.querySelector('[data-testid="copy-fact-0"]')?.click());
await sleep(400);
const copyLabel = await win.evaluate(
  () => document.querySelector('[data-testid="copy-fact-0"]')?.innerText ?? '');
check('the copy button reports success', /Copied|Kopyalandı/.test(copyLabel), copyLabel);
const clip = await app.evaluate(({ clipboard }) => clipboard.readText());
check('…and the OS clipboard really holds the address', clip.includes('/dav/'), clip);

// ── language: the screen, not the property ───────────────────────────
// ⚠⚠ v0.19.0 shipped a bug where the explorer's `locale` property said
// 'tr' and 10/10 property-level assertions passed while the file list on
// screen stayed English. Read the TEXT.
await win.evaluate(() => document.querySelector('[data-testid="connections-close"]')?.click());
await sleep(400);
await win.evaluate(() => document.querySelector('#settings [data-locale="tr"]')?.click());
await sleep(1200);
await win.evaluate(() => document.querySelector('#conn-open')?.click());
await sleep(1200);

const trText = await win.evaluate(
  () => document.querySelector('filex-connections')?.innerText ?? '');
check('the connections panel itself is in Turkish, not just its locale property',
  /Depo bağlantıları/.test(trText) && /Protokol/.test(trText),
  trText.split('\n').filter(Boolean).slice(0, 3).join(' · ') || 'empty');
// ⚠ Case-sensitive above, on purpose. The desktop shell styles `h2` with
// text-transform: uppercase for its own headings and that reaches into this
// component (shadowRoot: false, deliberately) — it rendered
// "DEPO BAĞLANTILARI" here while the web app rendered "Depo bağlantıları",
// until the package started stating its own heading type. A
// case-insensitive check would have let that straight through.

// The guide's own body, not just the chrome: the instructions are the thing
// a Turkish reader came for, and they are generated rather than translated
// wholesale, so they are where a missing catalogue key actually shows.
const trGuide = await win.evaluate(
  () => document.querySelector('.fe-guide__body')?.innerText ?? '');
check('…including the generated instructions, not only the headings',
  /Dosya Gezgini|Bağlan|Sürücü/.test(trGuide),
  trGuide.split('\n').filter(Boolean).slice(0, 5).join(' · ') || 'empty');
await shot(win, 'desktop-connections-tr');

// back to English so the profile is not left mid-experiment
await win.evaluate(() => document.querySelector('[data-testid="connections-close"]')?.click());
await sleep(300);
await win.evaluate(() => document.querySelector('#settings [data-locale="en"]')?.click());
await sleep(600);

await dropStorage();
await app.close();
fs.rmSync(profile, { recursive: true, force: true });
fs.rmSync(home, { recursive: true, force: true });
finish();
