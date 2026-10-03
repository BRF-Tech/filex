// #104 on the public page: a link to an entry its storage could not answer
// for. The state says `unavailable`; the page says why, in the visitor's
// language, and offers no download, zip or walk that the server would refuse
// (handlers/share.go answers those with the same sentence as an HTML page).
// Before, the page offered the download and the visitor met the storage
// driver's own error.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';

import PublicLink from '@/views/public/PublicLink.vue';
import { resetLocales } from '@brftech/filex-core';
import { en } from '@brftech/filex-core/src/locales/en';

function answer(status: number, body: unknown) {
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText: '',
    json: async () => body,
    text: async () => JSON.stringify(body),
  };
}

function router(routes: Record<string, unknown>) {
  return vi.fn(async (url: string) => {
    for (const [key, body] of Object.entries(routes)) {
      if (url === key) return answer(200, body);
    }
    return answer(404, { error: 'not_found' });
  });
}

const fetchMock = vi.fn();

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
  resetLocales();
  try {
    localStorage.clear();
  } catch {
    /* jsdom always has it */
  }
});
afterEach(() => vi.unstubAllGlobals());

const open = () => mount(PublicLink, { props: { kind: 'share', token: 'tok123' } });

describe('a link to an entry the storage could not answer for', () => {
  it('a file: the sentence, and no download and no picture', async () => {
    fetchMock.mockImplementation(
      router({
        '/api/public/s/tok123': {
          kind: 'file',
          needs_pin: false,
          unlocked: true,
          unavailable: true,
          node: { name: 'photo.jpg', size: 2048, mime: 'image/jpeg' },
        },
      }),
    );
    const w = open();
    await vi.waitFor(() => expect(w.find('[data-testid="public-share-unavailable"]').exists()).toBe(true));
    expect(w.get('[data-testid="public-share-unavailable"]').text()).toContain(en['public.unavailable']);
    expect(w.find('[data-testid="public-share-download"]').exists()).toBe(false);
    expect(w.find('.fe-ppage__shot').exists(), 'an image link would ask the server for the bytes').toBe(false);
  });

  it('a folder: no zip and no walk', async () => {
    fetchMock.mockImplementation(
      router({
        '/api/public/s/tok123': {
          kind: 'folder',
          needs_pin: false,
          unlocked: true,
          unavailable: true,
          node: { name: 'Proje' },
        },
      }),
    );
    const w = open();
    await vi.waitFor(() => expect(w.find('[data-testid="public-share-unavailable"]').exists()).toBe(true));
    expect(w.find('[data-testid="public-share-zip"]').exists()).toBe(false);
    expect(w.find('[data-testid="public-share-browse"]').exists()).toBe(false);
  });

  it('an ordinary link still offers its download', async () => {
    fetchMock.mockImplementation(
      router({
        '/api/public/s/tok123': { kind: 'file', needs_pin: false, unlocked: true, node: { name: 'a.pdf', size: 1 } },
      }),
    );
    const w = open();
    await vi.waitFor(() => expect(w.find('[data-testid="public-share-download"]').exists()).toBe(true));
    expect(w.find('[data-testid="public-share-unavailable"]').exists()).toBe(false);
  });
});
