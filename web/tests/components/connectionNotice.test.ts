// The strip a page shows while the server cannot be reached — one line for
// the whole outage, in place of a toast per failed request.
//
// ⚠ A strip rather than a toast is the decision, not a detail: being offline
// is a STATE, and a toast is an event with a timer on it, so saying a state
// with toasts means re-arming them — which is the storm — and leaves a stack
// of stale ones behind when the connection comes back.
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';

import ConnectionNotice from '@brftech/filex-core/src/components/ConnectionNotice.vue';
import {
  noteRequestFailed,
  noteRequestSucceeded,
  resetConnectionNotice,
} from '@brftech/filex-core/src/lib/connection';
import { en as coreEn } from '@brftech/filex-core/src/locales/en';
import { tr as coreTr } from '@brftech/filex-core/src/locales/tr';

beforeEach(() => resetConnectionNotice());
afterEach(() => resetConnectionNotice());

describe('ConnectionNotice', () => {
  it('says nothing while the server answers', () => {
    const w = mount(ConnectionNotice, { props: { locale: 'en' } });
    expect(w.find('[data-testid="connection-notice"]').exists()).toBe(false);
  });

  it('appears ONCE however many requests fell over', async () => {
    const w = mount(ConnectionNotice, { props: { locale: 'en' } });
    for (let i = 0; i < 30; i++) noteRequestFailed('background');
    await nextTick();
    const notices = w.findAll('[data-testid="connection-notice"]');
    expect(notices, 'thirty failures, one line').toHaveLength(1);
    expect(notices[0].text()).toContain(coreEn['err.network']);
  });

  it('takes itself down when the connection comes back', async () => {
    const w = mount(ConnectionNotice, { props: { locale: 'en' } });
    noteRequestFailed('background');
    await nextTick();
    expect(w.find('[data-testid="connection-notice"]').exists()).toBe(true);
    noteRequestSucceeded();
    await nextTick();
    expect(
      w.find('[data-testid="connection-notice"]').exists(),
      'it clears by itself — there is nothing to dismiss',
    ).toBe(false);
  });

  it('is said in the reader’s language, from the catalogue', async () => {
    // ⚠ No string of its own: it reuses `err.network`, so every language pack
    // that already translates the product translates this too, and the
    // release's packs stay at 100%.
    const w = mount(ConnectionNotice, { props: { locale: 'tr' } });
    noteRequestFailed('background');
    await nextTick();
    expect(w.text()).toContain(coreTr['err.network']);
    expect(w.text()).not.toContain(coreEn['err.network']);
  });
});
