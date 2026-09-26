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
      'The search could not reach the server. Try again.',
    );
    expect(w.text()).not.toContain('No results');
  });

  it('says nothing of a failure when it searched and found nothing', async () => {
    const w = await searchWith(async () => [], 'sözleşme');
    expect(w.find('[data-testid="palette-everywhere-failed"]').exists()).toBe(false);
  });
});
