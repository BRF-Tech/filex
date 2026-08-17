// Storage connections, in the desktop app.
//
// The point of this script is that the desktop app does NOT have its own
// connections screen. It mounts `<filex-connections>` from the same npm
// package as the file explorer, so what is measured here is the shared
// component running inside Electron: the driver form (fields supplied by
// the server's own descriptors), a real create against a real server, and
// the generated "how to connect" page.
//
// It also re-measures the trap that cost a shipped bug in v0.19.0: a
// language switch must change the TEXT ON SCREEN inside the component, not
// merely its `locale` property.
//
// Run:
//   FILEX_SERVER=http://127.0.0.1:5299 FILEX_EMAIL=… FILEX_PASSWORD=… \
//     node scripts/connections-e2e.mjs
//
// ⚠ Point it at a THROWAWAY server. It creates and deletes a storage.

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
await shot(win, 'desktop-connections-list');

// ── create a storage FROM THE DESKTOP APP ────────────────────────────
await win.evaluate(() => document.querySelector('[data-testid="storage-add"]')?.click());
await sleep(400);

// ⚠ The fields are whatever the SERVER's descriptor declared. Filling
// `#fe-cf-path` proves the desktop app rendered the driver's real contract
// — the failure this whole mechanism exists to stop is a surface that
// collects a key the backend never reads.
const set = (sel, value, kind = 'input') =>
  win.evaluate(
    ({ sel, value, kind }) => {
      const el = document.querySelector(sel);
      if (!el) return false;
      const proto = kind === 'change' ? HTMLSelectElement.prototype : HTMLInputElement.prototype;
      Object.getOwnPropertyDescriptor(proto, 'value').set.call(el, value);
      el.dispatchEvent(new Event(kind, { bubbles: true }));
      return true;
    },
    { sel, value, kind },
  );

await set('[data-testid="storage-name"]', STORAGE);
await set('[data-testid="storage-driver"]', 'local', 'change');
// ⚠ After the render the change triggers, not in the same turn: switching
// driver replaces the whole field set, and asking for the new field in the
// evaluate that caused the switch measures the OLD form.
await sleep(400);
check('the form renders the local driver contract from the server',
  await win.evaluate(() => !!document.querySelector('#fe-cf-path')));

await set('#fe-cf-path', MOUNT);
await sleep(200);

await win.evaluate(() => document.querySelector('[data-testid="storage-test"]')?.click());
await sleep(2500);
const testText = await win.evaluate(
  () => document.querySelector('[data-testid="test-result"]')?.innerText ?? '');
check('the connection test answers before anything is saved', /Connected|Could not connect/.test(testText), testText);
await shot(win, 'desktop-connections-form');

await win.evaluate(() => document.querySelector('[data-testid="storage-save"]')?.click());
await sleep(2500);

// The server is the authority.
const listed = await (await api('/api/admin/storages', {}, adminToken)).json();
const made = listed.find((s) => s.name === STORAGE);
check('the storage really exists on the server now', !!made, made ? `id=${made.id}` : 'not found');
check('…with the path the descriptor asked for, under the key the driver reads',
  made?.config?.path === MOUNT, JSON.stringify(made?.config ?? {}));
check('…and it shows up in the list on screen',
  await win.evaluate((n) => !!document.querySelector(`[data-testid="storage-edit-${n}"]`), STORAGE));

// ── the instruction page ─────────────────────────────────────────────
await win.evaluate(() => document.querySelector('[data-testid="tab-connect"]')?.click());
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
  /Depo bağlantıları/.test(trText) && /Depolar/.test(trText),
  trText.split('\n').filter(Boolean).slice(0, 3).join(' · ') || 'empty');
// ⚠ Case-sensitive above, on purpose. The desktop shell styles `h2` with
// text-transform: uppercase for its own headings and that reaches into this
// component (shadowRoot: false, deliberately) — it rendered
// "DEPO BAĞLANTILARI" here while the web app rendered "Depo bağlantıları",
// until the package started stating its own heading type. A
// case-insensitive check would have let that straight through.

// The panel keeps the tab it was left on — it was reopened, not remounted,
// which is exactly what makes the check above meaningful.
await win.evaluate(() => document.querySelector('[data-testid="tab-storages"]')?.click());
await sleep(400);
await win.evaluate(() => document.querySelector('[data-testid="storage-add"]')?.click());
await sleep(600);
const trForm = await win.evaluate(
  () => document.querySelector('[data-testid="storage-form"]')?.innerText ?? '');
check('…including the labels the server descriptor named',
  /Görünen isim/.test(trForm) && /Dizin yolu|Temel yol|Bucket/.test(trForm),
  trForm.split('\n').filter(Boolean).slice(0, 5).join(' · ') || 'empty');
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
