// One rule, in one place, for "does this queued job have an honest number,
// and is it a count of objects inside one source?" (lib/opProgress).
//
// ⚠ #67 wrote "one source whose objects the server counts" three times —
// lib/opProgress, the explorer's PendingOpsTray and the admin app's
// PendingOpsTray — and #59's OperationsCenter decided "no honest number" by
// its own `totalCount > 1` beside opProgress, which already decides it
// (`percent === null`). Two answers to one question drift (lesson #119).
import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { opObjects, opPercent } from '@brftech/filex-core/src/lib/opProgress';
import { useOperations } from '@brftech/filex-core/src/composables/useOperations';
import OperationsCenter from '@brftech/filex-core/src/components/OperationsCenter.vue';
import PendingOpsTray from '@brftech/filex-core/src/components/PendingOpsTray.vue';
import { normalizeOp } from '@brftech/filex-core/src/composables/usePendingOps';

const REPO = resolve(__dirname, '../../..');

describe('opObjects', () => {
  it('counts the objects of ONE source whose driver counts them', () => {
    expect(opObjects({ status: 'running', progress_total: 1, progress_done: 0, objects_total: 100, objects_done: 25 })).toEqual({
      done: 25,
      total: 100,
    });
  });

  it('is nothing for several sources, or a source with no object count', () => {
    expect(opObjects({ status: 'running', progress_total: 3, progress_done: 1, objects_total: 100, objects_done: 25 })).toBeNull();
    expect(opObjects({ status: 'running', progress_total: 1, progress_done: 0 })).toBeNull();
  });

  it('is what opPercent draws for one source', () => {
    const op = { status: 'running', progress_total: 1, progress_done: 0, objects_total: 200, objects_done: 50 };
    expect(opPercent(op)).toBe(25);
  });

  it('is the only copy of the rule', () => {
    const coreTray = readFileSync(resolve(REPO, 'packages/core/src/components/PendingOpsTray.vue'), 'utf8');
    const adminTray = readFileSync(resolve(REPO, 'web/src/components/PendingOpsTray.vue'), 'utf8');
    for (const [name, src] of [['core tray', coreTray], ['admin tray', adminTray]] as const) {
      expect(src, name).toMatch(/opObjects\(op\)/);
      expect(src, `${name} spells the rule out again`).not.toMatch(/progress_total <= 1 && \(op\.objects_total/);
    }
  });
});

describe('the operations centre', () => {
  it('shows no count where opProgress says there is no honest number', async () => {
    // Bytes moving with no total yet across three sources: opPercent → null.
    const op = normalizeOp({
      id: 3, kind: 'copy', status: 'running', total: 3, done: 0, bytes_done: 5 << 20, bytes_total: 0,
      sources: ['a', 'b', 'c'], storage_id: 1,
    });
    expect(opPercent(op)).toBeNull();
    const center = useOperations();
    mount(PendingOpsTray, { props: { ops: [op], locale: 'en', center } });
    await nextTick();
    const w = mount(OperationsCenter, { props: { center, locale: 'en' }, attachTo: document.body });
    await nextTick();
    await w.find('.fe-opc__badge').trigger('click');
    expect(w.find('.fe-opc__state').text(), 'a count beside "no honest number"').not.toContain('0/3');
    expect(w.find('.fe-opc__state').text()).toBe('In progress');
  });
});
