// An archive job (PR #48) is an ops row of its own kind. The operations centre
// has a name and a glyph for `archive-create` / `archive-extract`, but the
// tray only passed through the kinds it listed and folded every other one
// into the generic app job: a running archive read "App · 10/100" under the
// puzzle-piece icon (measured in the browser, 0.44.0 integration).
import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';

import { normalizeOp, type PendingOp } from '@brftech/filex-core/src/composables/usePendingOps';
import { useOperations } from '@brftech/filex-core/src/composables/useOperations';
import PendingOpsTray from '@brftech/filex-core/src/components/PendingOpsTray.vue';
import OperationsCenter from '@brftech/filex-core/src/components/OperationsCenter.vue';

function archiveRow(kind: 'archive-create' | 'archive-extract'): PendingOp {
  return normalizeOp({
    id: 9, kind, status: 'running', total: 100, done: 10, cancellable: true,
    sources: ['main://source/report.txt'], dest: 'archives/report.7z', storage_id: 1,
  });
}

describe('archive ops rows', () => {
  for (const kind of ['archive-create', 'archive-extract'] as const) {
    it(`${kind} is drawn as its own kind, with its percent, and may be stopped`, async () => {
      const center = useOperations();
      mount(PendingOpsTray, { props: { ops: [archiveRow(kind)], locale: 'en', center } });
      await nextTick();
      const [row] = center.active.value;
      expect(row.kind).toBe(kind);
      expect(row.percent).toBe(10);
      expect(row.cancellable).toBe(true);
    });
  }

  it('reads as an archive in the operations centre, in both languages', async () => {
    for (const [locale, words] of [['en', /Creating archive/], ['tr', /Arşiv oluşturuluyor/]] as const) {
      const center = useOperations();
      mount(PendingOpsTray, { props: { ops: [archiveRow('archive-create')], locale, center } });
      await nextTick();
      const w = mount(OperationsCenter, { props: { center, locale }, attachTo: document.body });
      await nextTick();
      await w.find('.fe-opc__badge').trigger('click');
      await nextTick();
      const text = document.body.textContent ?? '';
      expect(text, locale).toMatch(words);
      expect(text, `${locale}: not the generic app job`).not.toMatch(/\bApp\b|Uygulama/);
      expect(text, `${locale}: a percent, not "10/100"`).not.toContain('10/100');
      w.unmount();
    }
  });
});
