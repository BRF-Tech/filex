// The admin layout's operations tray names what a row is. A kind it did not
// know fell through to "Copying", so a folder being renamed, or brought back
// from the trash — both jobs of the queue now — read as a copy.
import { describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { nextTick } from 'vue';

import { normalizeOp } from '@brftech/filex-core';
import PendingOpsTray from '@/components/PendingOpsTray.vue';
import { usePendingOpsStore } from '@/stores/pendingOps';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

async function trayText(locale: 'en' | 'tr', kind: string): Promise<string> {
  setActivePinia(createPinia());
  const store = usePendingOpsStore();
  vi.spyOn(store, 'start').mockImplementation(() => {});
  store.items = [{ op: normalizeOp({ id: 1, kind, status: 'running', total: 1, done: 0, storage_id: 1 }), settledAt: null, missCount: 0 }];
  const i18n = createI18n({ legacy: false, locale, messages: { en, tr } });
  const w = mount(PendingOpsTray, { global: { plugins: [i18n] } });
  await nextTick();
  const text = w.text();
  w.unmount();
  return text;
}

describe('the admin layout’s operations tray', () => {
  it('names a queued rename as a rename, not a copy', async () => {
    const text = await trayText('en', 'rename');
    expect(text).toContain('Renaming');
    expect(text).not.toContain('Copying');
  });

  it('names a queued restore as a restore', async () => {
    expect(await trayText('en', 'restore')).toContain('Restoring');
    expect(await trayText('tr', 'restore')).toContain('Geri getiriliyor');
  });
});
