// A queued job held back for a moment by a scan of its storage says why it
// waits, in the server's words, where the trays said only "Queued".
//
// ⚠ GitHub #127 (chengjie): a folder delete queued while a local storage of
// tens of thousands of entries was scanned read "Delete · Queued" for the
// scan's four or five minutes, with nothing to say what it was waiting for.
// The server now sends `wait_text` (backend ops/gate_lane.go attachWait, said
// in the reader's language by ops/say.go) on such a pending row; the explorer's
// operations centre and the admin layout's tray show it. The sentence is the
// server's - the trays make none of their own.
import { describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { nextTick } from 'vue';

import { normalizeOp as coreNormalizeOp } from '@brftech/filex-core/src/composables/usePendingOps';
import { useOperations } from '@brftech/filex-core/src/composables/useOperations';
import OperationsCenter from '@brftech/filex-core/src/components/OperationsCenter.vue';
import CorePendingOpsTray from '@brftech/filex-core/src/components/PendingOpsTray.vue';
import { normalizeOp } from '@brftech/filex-core';
import AdminPendingOpsTray from '@/components/PendingOpsTray.vue';
import { usePendingOpsStore } from '@/stores/pendingOps';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const WAIT_TR = 'Sırasını bekliyor: depo taranıyor';
const WAIT_EN = 'Waiting for its turn: the storage is being scanned';

function row(over: Record<string, unknown>): Record<string, unknown> {
  return { id: 7, kind: 'delete', status: 'pending', total: 1, done: 0, sources: ['Klasor'], storage_id: 1, ...over };
}

async function centreState(raw: Record<string, unknown>, locale: 'en' | 'tr'): Promise<string> {
  const center = useOperations();
  mount(CorePendingOpsTray, { props: { ops: [coreNormalizeOp(raw)], locale, center } });
  await nextTick();
  const w = mount(OperationsCenter, { props: { center, locale }, attachTo: document.body });
  await nextTick();
  await w.find('.fe-opc__badge').trigger('click');
  const text = w.find('.fe-opc__state').text();
  w.unmount();
  return text;
}

async function adminTrayText(raw: Record<string, unknown>, locale: 'en' | 'tr'): Promise<string> {
  setActivePinia(createPinia());
  const store = usePendingOpsStore();
  vi.spyOn(store, 'start').mockImplementation(() => {});
  store.items = [{ op: normalizeOp(raw), settledAt: null, missCount: 0 }];
  const i18n = createI18n({ legacy: false, locale, messages: { en, tr } });
  const w = mount(AdminPendingOpsTray, { global: { plugins: [i18n] } });
  await nextTick();
  const text = w.text();
  w.unmount();
  return text;
}

describe('a queued job held back by a scan of its storage', () => {
  it('carries the server’s sentence through the normalizer', () => {
    expect(coreNormalizeOp(row({ wait_reason: 'storage_scan', wait_text: WAIT_EN })).wait_text).toBe(WAIT_EN);
    expect(coreNormalizeOp(row({})).wait_text).toBeUndefined();
  });

  it('the operations centre says why it waits instead of "Queued"', async () => {
    expect(await centreState(row({ wait_reason: 'storage_scan', wait_text: WAIT_TR }), 'tr')).toBe(WAIT_TR);
    expect(await centreState(row({ wait_reason: 'storage_scan', wait_text: WAIT_EN }), 'en')).toBe(WAIT_EN);
  });

  it('a job that simply waits its turn still reads "Queued"', async () => {
    expect(await centreState(row({}), 'en')).toBe('Queued');
    expect(await centreState(row({}), 'tr')).toBe('Sırada');
  });

  it('a running job does not repeat a reason it no longer has', async () => {
    const state = await centreState(row({ status: 'running', wait_text: WAIT_EN }), 'en');
    expect(state).not.toContain(WAIT_EN);
  });

  it('the admin layout’s tray says the same sentence', async () => {
    expect(await adminTrayText(row({ wait_reason: 'storage_scan', wait_text: WAIT_TR }), 'tr')).toContain(WAIT_TR);
    expect(await adminTrayText(row({}), 'en')).not.toContain(WAIT_EN);
  });
});
