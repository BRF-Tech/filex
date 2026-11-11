// "Get short link" in the share dialog (filex 0.60, docs/SHARING.md → Short
// links). ONE component draws the dialog on every surface (the explorer, the
// desktop app, every embed), so what is measured here is what each of them
// offers:
//
//   - nothing, unless the server said an app holding the link-shortener role
//     runs and this person may use it (`capabilities.short_links.available`);
//   - a short address the link already has is the link the row shows;
//   - pressing the button asks the server by the link's TOKEN, and the short
//     address becomes the link shown and copied, the full one a line below;
//   - shown decoded (an emoji reads as one), copied as the server spelled it.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

import PermissionsModal from '@brftech/filex-core/src/modals/PermissionsModal.vue';
import type { FileApi } from '@brftech/filex-core/src/composables/useFileApi';
import { requestFailure } from '@brftech/filex-core/src/lib/errorWords';

const SHORT_EMOJI = 'https://l.example.com/%F0%9F%98%80%F0%9F%9A%80%F0%9F%A6%95%F0%9F%8D%95%F0%9F%8E%B8';
const SHORT_EMOJI_SHOWN = 'https://l.example.com/\u{1F600}\u{1F680}\u{1F995}\u{1F355}\u{1F3B8}';

function fakeApi(opts: { existing?: Record<string, string>; listed?: string[]; refuse?: string } = {}) {
  let seq = 0;
  const asked: string[] = [];
  const shortened: string[] = [];
  const api = {
    listPermissions: vi.fn(async () => ({ direct: [], inherited: [], storage_rbac: true })),
    listShares: vi.fn(async () => ({
      shares: (opts.listed ?? []).map((tok, i) => ({ uuid: String(100 + i), url: `https://files.example/s/${tok}`, expires_at: null })),
    })),
    createShare: vi.fn(async (body: { kind?: string }) => {
      const token = `tok${++seq}`;
      return { share: { uuid: token, token, url: `https://files.example/${body.kind === 'drop' ? 'd' : 's'}/${token}`, expires_at: null } };
    }),
    revokeShare: vi.fn(async () => {}),
    shareMessage: vi.fn(async () => ({ subject: 's', body: 'b' })),
    shortLinkOf: vi.fn(async (token: string) => {
      asked.push(token);
      return opts.existing?.[token] ?? '';
    }),
    shortenLink: vi.fn(async (token: string) => {
      shortened.push(token);
      if (opts.refuse) throw requestFailure(422, JSON.stringify({ error: 'shorten_refused', message: opts.refuse }), 'en');
      return SHORT_EMOJI;
    }),
    resolveEmail: vi.fn(async () => ({ found: false })),
    searchUsers: vi.fn(async () => ({ users: [] })),
  };
  return { api: api as unknown as FileApi, raw: api, asked, shortened };
}

function dialog(api: FileApi, props: Record<string, unknown> = {}) {
  return mount(PermissionsModal, {
    props: { path: 'depo://Projeler/rapor.pdf', isDir: false, locale: 'en', shareMaxTtlDays: 7, api, mailReady: true, ...props },
    attachTo: document.body,
  });
}

let copied: string[] = [];
beforeEach(() => {
  copied = [];
  Object.defineProperty(navigator, 'clipboard', {
    value: { writeText: vi.fn(async (s: string) => void copied.push(s)) },
    configurable: true,
  });
});
afterEach(() => {
  delete (navigator as unknown as { clipboard?: unknown }).clipboard;
});

describe('Get short link: only when the server says so', () => {
  it('is not offered without the capability, and nothing is asked of the server', async () => {
    const { api, raw } = fakeApi({ listed: ['abc'] });
    const w = dialog(api);
    await flushPromises();
    expect(w.find('[data-testid="share-link"]').exists()).toBe(true);
    expect(w.find('[data-testid="share-short-get"]').exists()).toBe(false);
    expect(raw.shortLinkOf).not.toHaveBeenCalled();
    w.unmount();
  });

  it('is not offered to a person who may not make links', async () => {
    const { api } = fakeApi({ listed: ['abc'] });
    const w = dialog(api, { shortLinks: true, sharing: { links: false, uploadLinks: true, users: true } });
    await flushPromises();
    expect(w.find('[data-testid="share-short-get"]').exists()).toBe(false);
    w.unmount();
  });
});

describe('a short address is the link the row shows', () => {
  it('a link that already has one shows it at once, decoded, and copies it encoded', async () => {
    const { api, asked } = fakeApi({ listed: ['abc'], existing: { abc: SHORT_EMOJI } });
    const w = dialog(api, { shortLinks: true });
    await flushPromises();
    expect(asked, 'the listed link is asked about by its token').toEqual(['abc']);
    expect(w.get('[data-testid="share-link"]').text()).toBe(SHORT_EMOJI_SHOWN);
    expect(w.find('[data-testid="share-short-get"]').exists(), 'it has one: no button').toBe(false);
    expect(w.get('[data-testid="share-full-link"]').text()).toContain('https://files.example/s/abc');

    await w.get('[data-testid="share-copy"]').trigger('click');
    expect(copied).toEqual([SHORT_EMOJI]);
    await w.get('[data-testid="share-full-copy"]').trigger('click');
    expect(copied[1]).toBe('https://files.example/s/abc');
    w.unmount();
  });

  it('pressing the button asks by token; the answer leads, the full link stays one copy away', async () => {
    const { api, shortened } = fakeApi({ listed: ['abc'] });
    const w = dialog(api, { shortLinks: true });
    await flushPromises();
    expect(w.get('[data-testid="share-link"]').text()).toBe('https://files.example/s/abc');
    await w.get('[data-testid="share-short-get"]').trigger('click');
    await flushPromises();
    expect(shortened).toEqual(['abc']);
    expect(w.get('[data-testid="share-link"]').text()).toBe(SHORT_EMOJI_SHOWN);
    expect(w.get('[data-testid="share-link"]').attributes('href')).toBe(SHORT_EMOJI);
    await w.get('[data-testid="share-copy"]').trigger('click');
    expect(copied).toEqual([SHORT_EMOJI]);
    w.unmount();
  });

  it('a link made here is shortened by the token the server gave it', async () => {
    const { api, shortened } = fakeApi();
    const w = dialog(api, { shortLinks: true });
    await flushPromises();
    await w.get('[data-testid="share-switch"]').trigger('click');
    await flushPromises();
    await w.get('[data-testid="share-short-get"]').trigger('click');
    await flushPromises();
    expect(shortened).toEqual(['tok1']);
    expect(w.get('[data-testid="share-link"]').text()).toBe(SHORT_EMOJI_SHOWN);
    w.unmount();
  });

  it("a refusal is said in the server's words, and the full link stays", async () => {
    const { api } = fakeApi({ listed: ['abc'], refuse: 'This link cannot be shortened.' });
    const w = dialog(api, { shortLinks: true });
    await flushPromises();
    await w.get('[data-testid="share-short-get"]').trigger('click');
    await flushPromises();
    expect(w.get('[data-testid="share-short-error"]').text()).toContain('This link cannot be shortened.');
    expect(w.get('[data-testid="share-link"]').text()).toBe('https://files.example/s/abc');
    w.unmount();
  });
});

describe('the file request gets the same button', () => {
  it('a file request made here is shortened by its token, and its row shows the short address', async () => {
    const { api, shortened } = fakeApi();
    const w = dialog(api, { path: 'depo://Projeler', isDir: true, shortLinks: true });
    await flushPromises();
    await w.get('[data-testid="share-drop-toggle"]').trigger('click');
    await w.get('[data-testid="drop-create"]').trigger('click');
    await flushPromises();
    expect(w.get('[data-testid="drop-link"]').text()).toBe('https://files.example/d/tok1');
    await w.get('[data-testid="drop-short-get"]').trigger('click');
    await flushPromises();
    expect(shortened).toEqual(['tok1']);
    expect(w.get('[data-testid="drop-link"]').text()).toBe(SHORT_EMOJI_SHOWN);
    expect(w.get('[data-testid="drop-full-link"]').text()).toContain('https://files.example/d/tok1');
    await w.get('[data-testid="drop-copy"]').trigger('click');
    expect(copied).toEqual([SHORT_EMOJI]);
    w.unmount();
  });
});
