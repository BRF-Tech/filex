// PluginPageView — an app's `page` view, drawn as a whole page.
//
// ⚠⚠ The same conversation a `modal` view has (that seam is pinned by
// pluginViewModal.test.ts); what is tested here is the frame's own two
// states, which a dialog has no use for:
//
//   • QUEUED. A `page` action never goes through `…/run` — the tab opens on
//     the view and the job is born from THIS screen's submit. A tab that just
//     closed itself would leave the person with no idea whether anything
//     happened, so the page says the job is queued and offers the way back to
//     the tray that is now tracking it. ⚠ `onOp` fires before `onDone`, so a
//     naive frame overwrites "queued" with "finished" and the running job is
//     never mentioned.
//   • DONE / ERROR. The plugin ended the conversation, or the first surface
//     never arrived — neither may render as a blank tab.
import { describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

import PluginPageView from '@brftech/filex-core/src/components/plugin/PluginPageView.vue';
import type { PluginSurface } from '@brftech/filex-core/src/types/Plugins';

const surface: PluginSurface = {
  title: { en: 'Signature wizard', tr: 'İmza sihirbazı' },
  state: { step: 1 },
  nodes: [{ type: 'text', props: { text: { en: 'Place the fields' } } }],
  actions: [{ id: 'send', label: { en: 'Send' }, primary: true }],
};

function open(api: Record<string, unknown>) {
  return mount(PluginPageView, {
    props: {
      locale: 'en' as const,
      api: api as never,
      plugin: 'sign',
      view: 'wizard',
      path: 'docs://nda.pdf',
      backHref: '/admin/explore',
      opsHref: '/admin/explore',
    },
  });
}

describe('PluginPageView', () => {
  it('asks for its own first surface, on the file the tab was opened for', async () => {
    const pluginView = vi.fn(async () => ({ surface }));
    const w = open({ pluginView, pluginViewEvent: vi.fn() });
    await flushPromises();
    expect(pluginView).toHaveBeenCalledWith('sign', 'wizard', 'docs://nda.pdf');
    expect(w.find('[data-testid="plugin-page-title"]').text()).toBe('Signature wizard');
    expect(w.find('[data-testid="plugin-page-action-send"]').exists()).toBe(true);
    // The frame is a document, not a dialog: no modal, and a way back.
    expect(w.find('[data-testid="plugin-page-back"]').attributes('href')).toBe('/admin/explore');
  });

  it('a queued job is SAID, with the tray one click away', async () => {
    const op = { id: 9, kind: 'plugin-action', status: 'pending' };
    const w = open({
      pluginView: vi.fn(async () => ({ surface })),
      pluginViewEvent: vi.fn(async () => ({ op })),
    });
    await flushPromises();
    await w.find('[data-testid="plugin-page-action-send"]').trigger('click');
    await flushPromises();
    expect(w.emitted('op')?.[0]).toEqual([op]);
    expect(w.find('[data-testid="plugin-page-queued"]').exists()).toBe(true);
    // ⚠ NOT the "all done" screen: the job is running, and saying it finished
    // is worse than saying nothing.
    expect(w.find('[data-testid="plugin-page-done"]').exists()).toBe(false);
    expect(w.find('[data-testid="plugin-page-ops"]').attributes('href')).toBe('/admin/explore');
  });

  it('`done` with nothing queued ends the walk instead', async () => {
    const w = open({
      pluginView: vi.fn(async () => ({ surface })),
      pluginViewEvent: vi.fn(async () => ({ surface: { ...surface, done: true } })),
    });
    await flushPromises();
    await w.find('[data-testid="plugin-page-action-send"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="plugin-page-done"]').exists()).toBe(true);
    expect(w.find('[data-testid="plugin-page-queued"]').exists()).toBe(false);
  });

  it('a first surface that never arrives is an error card, never a blank tab', async () => {
    const w = open({ pluginView: vi.fn(async () => ({})), pluginViewEvent: vi.fn() });
    await flushPromises();
    expect(w.find('[data-testid="plugin-page-error"]').exists()).toBe(true);
    expect(w.find('[data-testid="plugin-page-loading"]').exists()).toBe(false);
  });

  it('a refused view says so and offers a retry', async () => {
    const pluginView = vi.fn(async () => {
      throw new Error('Not found');
    });
    const w = open({ pluginView, pluginViewEvent: vi.fn() });
    await flushPromises();
    expect(w.find('[data-testid="plugin-page-error"]').text()).toContain('Not found');
    await w.find('[data-testid="plugin-page-error"] button').trigger('click');
    await flushPromises();
    expect(pluginView).toHaveBeenCalledTimes(2);
  });
});
