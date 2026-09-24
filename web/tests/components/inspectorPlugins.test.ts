// The details panel's `inspector` placement: one collapsible section per
// plugin view whose `applies` rule accepts the selected single item, loaded
// only when opened, posting its footer through the same event route as the
// modal — and a `{op}` answer handed up for the tray.
import { describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';

import InspectorPanel from '@brftech/filex-core/src/components/InspectorPanel.vue';
import type { PluginViewRow } from '@brftech/filex-core/src/types/Plugins';

async function flush() {
  for (let i = 0; i < 6; i++) await Promise.resolve();
}

const refuse = () => vi.fn(async () => {
  throw new Error('not here');
});

function api(extra: Record<string, unknown> = {}) {
  return {
    listShares: refuse(),
    listVersions: refuse(),
    listPermissions: refuse(),
    listComments: refuse(),
    pluginView: vi.fn(async () => ({
      surface: {
        nodes: [{ type: 'text', props: { text: { en: 'Signed by 2 of 3' } } }],
        actions: [{ id: 'remind', label: { en: 'Remind' }, primary: true }],
      },
    })),
    pluginViewEvent: vi.fn(async () => ({ op: { id: 4, kind: 'plugin-action', status: 'pending' } })),
    ...extra,
  };
}

const VIEWS: PluginViewRow[] = [
  { plugin: 'sign', id: 'sign-status', placement: 'inspector', label: { en: 'Signatures' }, applies: { kind: 'file', ext: ['pdf'] } },
  { plugin: 'img', id: 'exif', placement: 'inspector', label: { en: 'Photo data' }, applies: { kind: 'file', mime: ['image/*'] } },
  { plugin: 'sign', id: 'envelopes', placement: 'home', label: { en: 'Envelopes' } },
];

const PDF = { id: 1, path: 'demo://nda.pdf', basename: 'nda.pdf', type: 'file' as const, extension: 'pdf', mime_type: 'application/pdf', size: 10 };
const PNG = { id: 2, path: 'demo://a.png', basename: 'a.png', type: 'file' as const, extension: 'png', mime_type: 'image/png', size: 10 };

function panel(nodes: unknown[], a = api()) {
  const w = mount(InspectorPanel, {
    props: { api: a as never, nodes: nodes as never, dirLabel: 'demo', dirCount: 2, locale: 'en', pluginViews: VIEWS },
  });
  return { w, a };
}

const sections = (w: ReturnType<typeof mount>) => w.findAll('[data-testid^="inspector-plugin-"]:not([data-testid^="inspector-plugin-toggle"])');

describe('InspectorPanel — plugin sections', () => {
  it('shows only the inspector views whose rule accepts the selected item', async () => {
    const { w } = panel([PDF]);
    await flush();
    const s = sections(w);
    expect(s).toHaveLength(1);
    expect(s[0].attributes('data-testid')).toBe('inspector-plugin-sign-sign-status');
    expect(s[0].text()).toContain('Signatures');
  });

  it('a picture gets the image view, not the pdf one; a multi-selection gets none', async () => {
    const { w } = panel([PNG]);
    await flush();
    expect(sections(w).map((s) => s.attributes('data-testid'))).toEqual(['inspector-plugin-img-exif']);
    const { w: multi } = panel([PDF, PNG]);
    await flush();
    expect(sections(multi)).toHaveLength(0);
  });

  it('loads the view only when opened, then posts its footer and hands `{op}` up', async () => {
    const { w, a } = panel([PDF]);
    await flush();
    expect(a.pluginView).not.toHaveBeenCalled();
    await w.find('[data-testid="inspector-plugin-toggle-sign-sign-status"]').trigger('click');
    await flush();
    expect(a.pluginView).toHaveBeenCalledWith('sign', 'sign-status', 'demo://nda.pdf');
    await w.vm.$nextTick();
    expect(w.text()).toContain('Signed by 2 of 3');
    await w.find('[data-testid="inspector-plugin-action-remind"]').trigger('click');
    await flush();
    expect(a.pluginViewEvent.mock.calls[0]).toEqual([
      'sign',
      'sign-status',
      { path: 'demo://nda.pdf', state: {}, event: 'submit', action_id: 'remind', data: { values: {} } },
    ]);
    expect(w.emitted('plugin-op')).toEqual([[{ id: 4, kind: 'plugin-action', status: 'pending' }]]);
  });

  it('no plugin views → no section at all', async () => {
    const w = mount(InspectorPanel, {
      props: { api: api() as never, nodes: [PDF] as never, dirLabel: 'demo', dirCount: 1, locale: 'en' },
    });
    await flush();
    expect(sections(w)).toHaveLength(0);
  });
});
