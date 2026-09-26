// ⌘K's "Everywhere" says when it could not search.
//
// ⚠ A search request that failed was drawn as an empty answer: the group went
// away exactly as it does when a search looked everywhere and found nothing
// (and "No results" when nothing else was on offer). A person then believes the
// file is not there. It now says the search did not happen.
import { afterEach, describe, expect, it } from 'vitest';
import { mount, type VueWrapper } from '@vue/test-utils';

import CommandPalette from '@brftech/filex-core/src/components/CommandPalette.vue';
import type { GlobalSearchHit } from '@brftech/filex-core/src/composables/useFileApi';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';
import { networkFailure, requestFailure } from '@brftech/filex-core/src/lib/errorWords';
import { en } from '@brftech/filex-core/src/locales/en';

let wrapper: VueWrapper | null = null;
afterEach(() => {
  wrapper?.unmount();
  wrapper = null;
});

async function searchWith(globalSearch: (q: string) => Promise<GlobalSearchHit[]>, q: string) {
  wrapper = mount(CommandPalette, {
    props: { open: false, locale: 'en' as const, files: [] as FileNode[], viewMode: 'list' as const, globalSearch },
    attachTo: document.body,
  });
  await wrapper.setProps({ open: true });
  await wrapper.vm.$nextTick();
  await wrapper.find('.fe-cmdp__input').setValue(q);
  // the palette debounces the everywhere query by 250 ms
  await new Promise((r) => setTimeout(r, 400));
  await wrapper.vm.$nextTick();
  return wrapper;
}

describe('Everywhere, when the search fails', () => {
  it('says it could not search, not that nothing was found', async () => {
    const w = await searchWith(async () => {
      throw new Error('502 Bad Gateway');
    }, 'sözleşme');
    expect(w.find('[data-testid="palette-everywhere-failed"]').text()).toBe(
      'The search could not be done. Try again.',
    );
    expect(w.text()).not.toContain('No results');
  });

  // ⚠ #65 said "could not reach the server" for EVERY failure — a 403, a 500,
  // a search the server refused — which sends a person to check a connection
  // that is fine. The failure's own words say which it was.
  it('says why, in the failure’s words: a refusal is not "unreachable"', async () => {
    const w = await searchWith(async () => {
      throw requestFailure(403, JSON.stringify({ error: 'forbidden' }), 'en');
    }, 'sözleşme');
    const said = w.find('[data-testid="palette-everywhere-failed"]').text();
    expect(said).toBe(en['err.status.403']);
    expect(said).not.toMatch(/reach the server/);
  });

  it('and an unreachable server is said as one', async () => {
    const w = await searchWith(async () => {
      throw networkFailure('en', new TypeError('Failed to fetch'));
    }, 'sözleşme');
    expect(w.find('[data-testid="palette-everywhere-failed"]').text()).toBe(en['err.network']);
  });

  it('says nothing of a failure when it searched and found nothing', async () => {
    const w = await searchWith(async () => [], 'sözleşme');
    expect(w.find('[data-testid="palette-everywhere-failed"]').exists()).toBe(false);
  });
});
