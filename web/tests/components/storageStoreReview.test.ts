// #215 - a store's install link for a STORAGE plugin, on the page every
// store link opens (views/StoreInstall.vue): its own review
// (StorageStoreReview), never the app wizard.
//
// What has to stay true:
//   · every sentence the review says is the server's (`storage_review.notices`),
//     drawn as it came - the page writes none of its own about what
//     installing a native program means;
//   · the build's facts are shown (its sha256, this server's platform);
//   · Install sends the review's handle (and a paid plugin's key), and the
//     page then points at the Storage plugins list;
//   · when the server says the install cannot go ahead (`can_install`
//     false: a signature this server requires and cannot verify), Install is
//     off, and closing the review tells the store it was cancelled.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const posts: Array<{ url: string; body: unknown }> = [];
let intentAnswer: unknown = null;

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    post: vi.fn(async (url: string, body: unknown) => {
      posts.push({ url, body });
      if (url.endsWith('/store-intent')) return { data: intentAnswer };
      if (url.endsWith('/store-intent/install')) {
        return { data: { kind: 'storage', plugin: { id: 9, name: 'myfs', kind: 'binary', state: 'starting', version: '1.3.0' } } };
      }
      if (url.endsWith('/store-intent/cancel')) return { data: null };
      throw new Error('unexpected POST ' + url);
    }),
    get: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}));

import StoreInstall from '@/views/StoreInstall.vue';
import { unmountAll } from '../helpers/teardown';

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); };
}

const SHA = 'ab'.repeat(32);

function storageAnswer(canInstall = true, paid = false) {
  return {
    handle: 'h-s',
    store: 'https://apps.filex.sh',
    store_trust: 'admin',
    intent: {
      store: 'https://apps.filex.sh', token_id: 't9', app: 'myfs', kind: 'storage', version: '1.3.0',
      repo: 'acme/filex-myfs', ref: 'v1.3.0', commit: 'c'.repeat(40), paid, expires_at: '2026-10-08T18:30:00Z',
    },
    storage_review: {
      name: 'myfs', version: '1.3.0', platform: 'linux/amd64', platforms: ['linux/amd64', 'linux/arm64'],
      sha256: SHA, size: 4096, url: 'https://github.com/acme/filex-myfs/releases/download/v1.3.0/myfs-linux-amd64',
      feed_url: 'https://github.com/acme/filex-myfs/releases/download/v1.3.0/filex-storage.json',
      notes: 'Faster listings.', source: 'acme/filex-myfs', paid,
      conformance: { platform: 'linux/amd64', filex: '0.55.0', verified: true, passed: 9, failed: 0, skipped: 7, capabilities: ['write'] },
      capabilities: [{ id: 'write', label: 'writing' }],
      signature: { store_signed: true, publisher_signed: false, required: !canInstall, verifies: false },
      notices: [
        { level: 'warning', text: 'SERVER-SAYS: a native program with the server’s rights.' },
        { level: 'info', text: 'SERVER-SAYS: the store ran the probes.' },
        ...(canInstall ? [] : [{ level: 'error', text: 'SERVER-SAYS: no signature verifies.' }]),
      ],
      can_install: canInstall,
    },
  };
}

function waitingLink() {
  sessionStorage.setItem('filex.storeLink', JSON.stringify({ store: 'https://apps.filex.sh', token: 'tok_storage0001', at: Date.now() }));
}

function mountPage() {
  const i18n = createI18n({ legacy: false, locale: 'en', fallbackLocale: 'en', messages: { en, tr } });
  return mount(StoreInstall, {
    global: { plugins: [i18n], stubs: { RouterLink: { props: ['to'], template: '<a :data-to="JSON.stringify(to)"><slot /></a>' } } },
    attachTo: document.body,
  });
}

const q = (id: string) => document.body.querySelector<HTMLElement>(`[data-testid="${id}"]`);
const isOpen = (id: string) => !!q(id)?.closest('dialog')?.hasAttribute('open');

describe('a storage plugin’s store link', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    sessionStorage.clear();
    posts.length = 0;
    unmountAll();
  });

  it('opens its own review with the server’s sentences, never the app wizard', async () => {
    waitingLink();
    intentAnswer = storageAnswer();
    mountPage();
    await flushPromises();
    // ⚠ Both are Modals, and a Modal is a <dialog> that is always in the
    // page: which one is OPEN is what the person sees.
    expect(isOpen('storage-store-review'), 'its own review is open').toBe(true);
    expect(isOpen('app-plugin-wizard'), 'not the app wizard').toBe(false);
    const notices = q('storage-store-notices')!.textContent ?? '';
    expect(notices).toContain('SERVER-SAYS: a native program');
    expect(notices).toContain('SERVER-SAYS: the store ran the probes.');
    expect(q('storage-store-sha256')!.textContent).toBe(SHA);
    expect(q('storage-store-capabilities')!.textContent).toContain('writing');
  });

  it('installs with the review’s handle, then points at the Storage plugins list', async () => {
    waitingLink();
    intentAnswer = storageAnswer();
    const w = mountPage();
    await flushPromises();
    (q('storage-store-install') as HTMLButtonElement).click();
    await flushPromises();
    expect(posts.find((p) => p.url.endsWith('/store-intent/install'))?.body).toEqual({ handle: 'h-s' });
    expect(posts.find((p) => p.url.endsWith('/store-intent/cancel')), 'an install is not a cancel').toBeUndefined();
    const done = w.find('[data-testid="store-install-done-storage"]');
    expect(done.text()).toContain('myfs 1.3.0 is installed.');
    expect(w.find('[data-testid="store-install-open-storage"]').attributes('data-to')).toContain('"tab":"storage"');
  });

  it('a paid plugin sends the key typed in the review', async () => {
    waitingLink();
    intentAnswer = storageAnswer(true, true);
    mountPage();
    await flushPromises();
    const key = document.body.querySelector<HTMLInputElement>('input[name="storage-store-license"]')!;
    key.value = '  FXL-AAAA-BBBB  ';
    key.dispatchEvent(new Event('input'));
    await flushPromises();
    (q('storage-store-install') as HTMLButtonElement).click();
    await flushPromises();
    expect(posts.find((p) => p.url.endsWith('/store-intent/install'))?.body).toEqual({ handle: 'h-s', license_key: 'FXL-AAAA-BBBB' });
  });

  it('when the server says no, Install is off and closing tells the store it was cancelled', async () => {
    waitingLink();
    intentAnswer = storageAnswer(false);
    const w = mountPage();
    await flushPromises();
    expect(q('storage-store-notice-error')!.textContent).toContain('SERVER-SAYS: no signature verifies.');
    expect((q('storage-store-install') as HTMLButtonElement).disabled).toBe(true);
    (q('storage-store-cancel') as HTMLButtonElement).click();
    await flushPromises();
    expect(posts.find((p) => p.url.endsWith('/store-intent/cancel'))?.body).toEqual({ handle: 'h-s' });
    expect(w.find('[data-testid="store-install-cancelled"]').exists()).toBe(true);
  });
});
