// The page a store's install link opens (views/StoreInstall.vue): the trust
// question (nothing from the link read yet; nothing trusted until the box is
// ticked and Trust pressed, which names the fingerprints shown), the review
// in the install dialog marked "From store", the license key of a paid app,
// and a refusal said in a sentence.
//
// The dry run inside the review is the SERVER's own bytes (the wire fixture
// app_plugins_wire_test.go writes), as in appPluginInstallWizard.test.ts.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const WIRE = path.resolve(__dirname, '../../../backend/internal/api/handlers/testdata/wire');
const dryRun = () => {
  const d = JSON.parse(readFileSync(path.join(WIRE, 'app-plugin-dry-run.json'), 'utf8'));
  delete d.installed;
  delete d.engines_missing;
  return d;
};

const FPR = 'a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90';
const posts: Array<{ url: string; body: unknown }> = [];
let intentAnswers: Array<{ status: number; data: unknown }> = [];
/** Set: the install answers only once it resolves (an install on its way). */
let installGate: Promise<void> | null = null;

function axiosError(status: number, data: unknown) {
  return Object.assign(new Error('refused'), { isAxiosError: true, response: { status, data } });
}

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    post: vi.fn(async (url: string, body: unknown) => {
      posts.push({ url, body });
      if (url.endsWith('/store-intent')) {
        const a = intentAnswers.shift();
        if (!a) throw new Error('no answer queued');
        if (a.status !== 200) throw axiosError(a.status, a.data);
        return { data: a.data };
      }
      if (url.endsWith('/stores')) return { data: { origin: 'https://fapps.brfd.app', source: 'admin', keys: [] } };
      if (url.endsWith('/store-intent/install')) {
        if (installGate) await installGate;
        return { data: { plugin: { id: 4, name: 'sign' }, license: { app: 'sign', required: true, status: 'revoked', held: true, store_trusted: true } } };
      }
      if (url.endsWith('/store-intent/cancel')) return { data: null };
      throw new Error('unexpected POST ' + url);
    }),
    get: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}));

const flags = vi.hoisted(() => ({ framed: false }));
vi.mock('@/lib/storeLink', async (importOriginal) => {
  const orig = await importOriginal<typeof import('@/lib/storeLink')>();
  return { ...orig, isFramed: () => flags.framed };
});

import StoreInstall from '@/views/StoreInstall.vue';
import { captureStoreFragment } from '@/lib/storeLink';
import AppPluginInstallWizard from '@/components/plugins/AppPluginInstallWizard.vue';
import { unmountAll } from '../helpers/teardown';

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); };
}

function waitingLink() {
  sessionStorage.setItem('filex.storeLink', JSON.stringify({ store: 'https://fapps.brfd.app', token: 'tok_0123456789', at: Date.now() }));
}

function mountPage() {
  const i18n = createI18n({ legacy: false, locale: 'en', fallbackLocale: 'en', messages: { en, tr } });
  return mount(StoreInstall, {
    global: { plugins: [i18n], stubs: { RouterLink: { template: '<a><slot /></a>' } } },
    attachTo: document.body,
  });
}

const trustQuestion = (code = 'store_trust_required', previous = false) => ({
  status: 409,
  data: {
    error: code,
    message: 'x',
    detail: {
      store: 'https://fapps.brfd.app',
      keys: [
        { id: 'idx-1', use: 'index', status: 'active', fingerprint: FPR },
        { id: 'lic-1', use: 'license', status: 'active', fingerprint: FPR.split('').reverse().join('') },
      ],
      fingerprints: ['index:idx-1:' + FPR, 'license:lic-1:x'],
      ...(previous ? { previous_keys: [{ id: 'idx-0', use: 'index', fingerprint: '00'.repeat(32) }] } : {}),
    },
  },
});

const review = (paid = false, extra: Record<string, unknown> = {}) => ({
  status: 200,
  data: {
    ...extra,
    handle: 'h-1',
    store: 'https://fapps.brfd.app',
    store_trust: 'admin',
    intent: {
      store: 'https://fapps.brfd.app', token_id: 't1', app: 'sign', kind: 'app', version: '0.3.0',
      repo: 'BRF-Tech/filex-sign', ref: 'v0.3.0', commit: '214e9e8a0c7d4b5e9f1a2b3c', paid,
      ...(paid ? { license_key_prefix: 'FXL-7Q…' } : {}), expires_at: '2026-10-04T18:30:00Z',
    },
    review: dryRun(),
  },
});

describe('StoreInstall', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    sessionStorage.clear();
    posts.length = 0;
    intentAnswers = [];
    installGate = null;
    flags.framed = false;
    unmountAll();
  });

  const closeButton = () => Array.from(document.body.querySelectorAll('button')).find((b) => b.getAttribute('aria-label') === 'Close');

  /** Ticks "I understand" and presses Install. */
  async function pressInstall() {
    const understand = document.body.querySelector('input[name="app-plugin-understand"]') as HTMLInputElement;
    understand.checked = true;
    understand.dispatchEvent(new Event('change'));
    await flushPromises();
    (document.body.querySelector('[data-testid="app-plugin-install"]') as HTMLButtonElement).click();
    await flushPromises();
  }

  // store fe review #1: a close while the install was on its way told the
  // store `cancelled` and said "Nothing was installed" over an app that
  // landed a moment later.
  it('while Install is on its way the review cannot be closed, the store is told nothing, and the page then says installed', async () => {
    waitingLink();
    intentAnswers = [review()];
    let release!: () => void;
    installGate = new Promise<void>((ok) => (release = ok));
    const w = mountPage();
    await flushPromises();
    expect(closeButton()).toBeTruthy();
    await pressInstall();

    expect(closeButton(), 'no ×').toBeUndefined();
    const dlg = document.body.querySelector('dialog') as HTMLDialogElement;
    const esc = new Event('cancel', { cancelable: true });
    dlg.dispatchEvent(esc);
    expect(esc.defaultPrevented, 'Escape does nothing').toBe(true);
    dlg.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    // A close that reaches the page anyway is not a cancel either.
    w.findComponent(AppPluginInstallWizard).vm.$emit('update:modelValue', false);
    await flushPromises();
    expect(posts.find((p) => p.url.endsWith('/store-intent/cancel'))).toBeUndefined();
    expect(w.find('[data-testid="store-install-cancelled"]').exists()).toBe(false);
    expect(document.body.querySelector('[data-testid="app-plugin-wizard"]')).not.toBeNull();

    release();
    await flushPromises();
    const done = document.body.querySelector('[data-testid="app-plugin-done"]');
    expect(done).not.toBeNull();
    (done!.querySelector('button') as HTMLButtonElement).click();
    await flushPromises();
    expect(w.find('[data-testid="store-install-done"]').text()).toContain('sign is installed.');
    expect(w.find('[data-testid="store-install-cancelled"]').exists()).toBe(false);
    expect(posts.find((p) => p.url.endsWith('/store-intent/cancel'))).toBeUndefined();
  });

  // store fe review #2: a second link opened in the tab while the page is
  // open reaches the router, not a page load; the page reads it.
  it('a second link while the review of the first is open: the first is cancelled, the second read', async () => {
    waitingLink();
    intentAnswers = [review(), { ...review(), data: { ...review().data, handle: 'h-2' } }];
    mountPage();
    await flushPromises();
    captureStoreFragment('#store=https%3A%2F%2Ffapps.brfd.app&intent=tok_second0001');
    await flushPromises();
    expect(posts.find((p) => p.url.endsWith('/store-intent/cancel'))?.body).toEqual({ handle: 'h-1' });
    const reads = posts.filter((p) => p.url.endsWith('/store-intent'));
    expect(reads.map((p) => (p.body as { token: string }).token)).toEqual(['tok_0123456789', 'tok_second0001']);
    expect(document.body.querySelector('[data-testid="app-plugin-wizard"]')).not.toBeNull();
  });

  it('a second link while Install is on its way waits until that is answered', async () => {
    waitingLink();
    intentAnswers = [review(), { ...review(), data: { ...review().data, handle: 'h-2' } }];
    let release!: () => void;
    installGate = new Promise<void>((ok) => (release = ok));
    const w = mountPage();
    await flushPromises();
    await pressInstall();
    captureStoreFragment('#store=https%3A%2F%2Ffapps.brfd.app&intent=tok_second0002');
    await flushPromises();
    expect(posts.filter((p) => p.url.endsWith('/store-intent'))).toHaveLength(1);
    expect(posts.find((p) => p.url.endsWith('/store-intent/cancel'))).toBeUndefined();
    release();
    installGate = null;
    await flushPromises();
    (document.body.querySelector('[data-testid="app-plugin-done"] button') as HTMLButtonElement).click();
    await flushPromises();
    expect(posts.find((p) => p.url.endsWith('/store-intent/cancel'))).toBeUndefined();
    expect(posts.filter((p) => p.url.endsWith('/store-intent')).map((p) => (p.body as { token: string }).token)).toEqual(['tok_0123456789', 'tok_second0002']);
    expect(w.find('[data-testid="store-install-review"]').exists()).toBe(true);
  });

  it('the session ended: the link waits in the tab for the sign-in', async () => {
    waitingLink();
    intentAnswers = [{ status: 401, data: { error: 'unauthorized' } }];
    mountPage();
    await flushPromises();
    expect(JSON.parse(sessionStorage.getItem('filex.storeLink') ?? '{}').token).toBe('tok_0123456789');
  });

  it('a 401 while the session holds keeps nothing (the router would bring the page back to it forever)', async () => {
    const { api } = await import('@/api/client');
    vi.mocked(api.get).mockResolvedValueOnce({ data: { user: { id: 1, email: 'a@test.local', role: 'admin' } } });
    // The signed-in account's view preferences, which /auth/me's answer loads.
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } })));
    try {
      waitingLink();
      intentAnswers = [{ status: 401, data: { error: 'unauthorized' } }];
      const w = mountPage();
      await flushPromises();
      expect(sessionStorage.getItem('filex.storeLink')).toBeNull();
      expect(w.find('[data-testid="store-install-error"]').exists()).toBe(true);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  // store fe review #8: the upgrade review said nothing of where the
  // installed app came from.
  it.each([
    ['https://fapps.brfd.app', 'Installed 0.2.0, from the store https://fapps.brfd.app (BRF-Tech/filex-sign) → this link 0.3.0, from the store https://fapps.brfd.app (BRF-Tech/filex-sign)'],
    ['', 'Installed 0.2.0, from GitHub directly, without a store (BRF-Tech/filex-sign) → this link 0.3.0, from the store https://fapps.brfd.app (BRF-Tech/filex-sign)'],
  ])('an upgrade says where the installed app came from and where the link comes from (store %j)', async (from, line) => {
    waitingLink();
    intentAnswers = [review(false, { upgrade_of: { id: 4, version: '0.2.0', store: from, repo: 'BRF-Tech/filex-sign', source_url: 'https://github.com/BRF-Tech/filex-sign@v0.2.0' } })];
    mountPage();
    await flushPromises();
    expect(document.body.querySelector('[data-testid="app-plugin-store-upgrade-from"]')?.textContent?.trim()).toBe(line);
  });

  it('a new install says no source of an installed app', async () => {
    waitingLink();
    intentAnswers = [review()];
    mountPage();
    await flushPromises();
    expect(document.body.querySelector('[data-testid="app-plugin-wizard"]')).not.toBeNull();
    expect(document.body.querySelector('[data-testid="app-plugin-store-upgrade-from"]')).toBeNull();
  });

  it('an upgrade through a store says the version it went to', async () => {
    waitingLink();
    intentAnswers = [review(false, { upgrade_of: { id: 4, version: '0.2.0', store: 'https://fapps.brfd.app', repo: 'BRF-Tech/filex-sign', source_url: 'https://github.com/BRF-Tech/filex-sign@v0.2.0' } })];
    const w = mountPage();
    await flushPromises();
    await pressInstall();
    (document.body.querySelector('[data-testid="app-plugin-done"] button') as HTMLButtonElement).click();
    await flushPromises();
    expect(w.find('[data-testid="store-install-done"]').text()).toContain('sign is upgraded to 0.3.0.');
  });

  it('inside a frame: refuses, asks nothing, and drops the link', async () => {
    waitingLink();
    flags.framed = true;
    intentAnswers = [review()];
    const w = mountPage();
    await flushPromises();
    expect(w.find('[data-testid="store-install-framed"]').exists()).toBe(true);
    expect(w.find('[data-testid="store-install-trust"]').exists()).toBe(false);
    expect(posts).toHaveLength(0);
    expect(sessionStorage.getItem('filex.storeLink')).toBeNull();
  });

  it('with no link waiting, says to open the store link again and asks nothing', async () => {
    const w = mountPage();
    await flushPromises();
    expect(w.find('[data-testid="store-install-none"]').exists()).toBe(true);
    expect(posts).toHaveLength(0);
  });

  it('an untrusted store: the fingerprints, and Trust only once the box is ticked; then the review', async () => {
    waitingLink();
    intentAnswers = [trustQuestion(), review()];
    const w = mountPage();
    await flushPromises();

    expect(posts[0]).toEqual({ url: '/admin/app-plugins/store-intent', body: { store: 'https://fapps.brfd.app', token: 'tok_0123456789' } });
    expect(w.find('[data-testid="store-trust-new"]').exists()).toBe(true);
    expect(w.find('[data-testid="store-trust-origin"]').text()).toBe('https://fapps.brfd.app');
    const fps = w.findAll('[data-testid="store-trust-fingerprint"]');
    expect(fps).toHaveLength(2);
    expect(fps[0].text()).toBe('a1b2 c3d4 e5f6 0718 293a 4b5c 6d7e 8f90');

    const approve = w.find('[data-testid="store-trust-approve"]');
    expect(approve.attributes('disabled')).toBeDefined();
    await approve.trigger('click');
    expect(posts.filter((p) => p.url.endsWith('/stores'))).toHaveLength(0);

    await w.find('input[name="store-trust-compared"]').setValue(true);
    await w.find('[data-testid="store-trust-approve"]').trigger('click');
    await flushPromises();
    const trust = posts.find((p) => p.url.endsWith('/stores'));
    expect(trust?.body).toEqual({ store: 'https://fapps.brfd.app', fingerprints: ['index:idx-1:' + FPR, 'license:lic-1:x'] });
    expect(posts.filter((p) => p.url.endsWith('/store-intent'))).toHaveLength(2);
    expect(document.body.querySelector('[data-testid="app-plugin-from-store"]')?.textContent).toContain('https://fapps.brfd.app');
    expect(document.body.querySelector('[data-testid="app-plugin-store-source"]')?.textContent).toContain('BRF-Tech/filex-sign @ v0.3.0');
  });

  it('a store whose keys changed is said in red, with the keys it was trusted with', async () => {
    waitingLink();
    intentAnswers = [trustQuestion('store_key_changed', true)];
    const w = mountPage();
    await flushPromises();
    expect(w.find('[data-testid="store-trust-changed"]').exists()).toBe(true);
    expect(w.find('[data-testid="store-trust-previous"]').text()).toContain('idx-0');
    await w.find('[data-testid="store-trust-decline"]').trigger('click');
    expect(w.find('[data-testid="store-install-declined"]').exists()).toBe(true);
    expect(posts.filter((p) => p.url.endsWith('/stores'))).toHaveLength(0);
  });

  it('a paid app: the key by its prefix only, a typed key sent, the hold said after the install', async () => {
    waitingLink();
    intentAnswers = [review(true)];
    mountPage();
    await flushPromises();
    const body = document.body;
    expect(body.querySelector('[data-testid="app-plugin-store-license-prefix"]')?.textContent).toContain('FXL-7Q…');
    const key = body.querySelector('[data-testid="app-plugin-store-license-key"] input, input[data-testid="app-plugin-store-license-key"]') as HTMLInputElement;
    key.value = 'FXL-TYPED-0001';
    key.dispatchEvent(new Event('input'));
    const understand = body.querySelector('input[name="app-plugin-understand"]') as HTMLInputElement;
    understand.checked = true;
    understand.dispatchEvent(new Event('change'));
    await flushPromises();
    (body.querySelector('[data-testid="app-plugin-install"]') as HTMLButtonElement).click();
    await flushPromises();
    const inst = posts.find((p) => p.url.endsWith('/store-intent/install'));
    expect(inst?.body).toMatchObject({ handle: 'h-1', license_key: 'FXL-TYPED-0001' });
    expect(body.querySelector('[data-testid="app-plugin-store-held"]')?.textContent).toContain('revoked');
  });

  it('closing the review without installing tells the store the link was cancelled', async () => {
    waitingLink();
    intentAnswers = [review()];
    const w = mountPage();
    await flushPromises();
    const cancel = Array.from(document.body.querySelectorAll('button')).find((b) => b.getAttribute('aria-label') === 'Close');
    expect(cancel).toBeTruthy();
    (cancel as HTMLButtonElement).click();
    await flushPromises();
    expect(posts.find((p) => p.url.endsWith('/store-intent/cancel'))?.body).toEqual({ handle: 'h-1' });
    expect(w.find('[data-testid="store-install-cancelled"]').exists()).toBe(true);
  });

  it("a refusal is the server's sentence, as it came", async () => {
    waitingLink();
    // 0.55 (#209): the sentence is the server's (srvtext
    // server.store.intent_pin_mismatch, in the reader's language), printed as
    // it came - the page builds none of its own from the code and the detail.
    const said = "What the app's repository serves is not what the store approved (manifest_sha256). Nothing was installed.";
    intentAnswers = [{ status: 409, data: { error: 'intent_pin_mismatch', message: said, detail: { mismatches: [{ field: 'manifest_sha256', link: 'a', source: 'b' }] } } }];
    const w = mountPage();
    await flushPromises();
    const err = w.find('[data-testid="store-install-error"]').text();
    expect(err).toContain(said);
    expect(err, 'no code on the screen').not.toContain('intent_pin_mismatch');
  });
});
