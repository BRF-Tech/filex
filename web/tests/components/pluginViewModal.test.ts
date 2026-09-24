import { closeRowMenus, openRowMenu, pickMenuItem } from '../helpers/rowMenu';
// PluginViewModal — the conversation, as the plugin sees it on the wire.
//
// The values of every node land in `data.values` on submit / action, a form
// edit becomes ONE `change` 300 ms after the last keystroke (and none before),
// the person's entries survive the `change` answer, and `{op}` closes the
// dialog with the row handed up. The renderer's own tests cover the nodes;
// this is the seam between them and the server.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';

import PluginViewModal from '@brftech/filex-core/src/components/plugin/PluginViewModal.vue';
import { SURFACE_CHANGE_DEBOUNCE_MS } from '@brftech/filex-core/src/composables/usePluginSurface';
import type { PluginSurface } from '@brftech/filex-core/src/types/Plugins';

async function flush() {
  for (let i = 0; i < 6; i++) await Promise.resolve();
}

const first: PluginSurface = {
  title: { en: 'Sign', tr: 'İmzala' },
  state: { step: 1 },
  nodes: [
    { type: 'form', props: { fields: [{ key: 'subject', type: 'string', default: 'Please sign' }] } },
    { id: 'to', type: 'people-picker', props: { value: [{ email: 'a@b.c' }], allow_external: true } },
    { id: 'pin', type: 'pin-input', props: { length: 4 } },
  ],
  actions: [
    { id: 'cancel', label: { en: 'Cancel' } },
    { id: 'next', label: { en: 'Next' }, primary: true },
  ],
};

function open(pluginViewEvent: ReturnType<typeof vi.fn>, surface: PluginSurface = first) {
  return mount(PluginViewModal, {
    props: {
      open: true,
      locale: 'en',
      api: { pluginViewEvent } as never,
      plugin: 'sign',
      view: 'wizard',
      surface,
      path: 'docs://nda.pdf',
    },
  });
}

describe('PluginViewModal', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('submit carries every node\'s value, the echoed state and the path', async () => {
    const ev = vi.fn(async () => ({ surface: { ...first, done: true } }));
    const w = open(ev);
    await w.find('[data-testid="surface-pin"] input').setValue('1234');
    await w.find('[data-testid="plugin-view-action-next"]').trigger('click');
    await flush();
    expect(ev).toHaveBeenCalledWith('sign', 'wizard', {
      path: 'docs://nda.pdf',
      state: { step: 1 },
      event: 'submit',
      action_id: 'next',
      data: { values: { subject: 'Please sign', to: [{ email: 'a@b.c' }], pin: '1234' } },
    });
    expect(w.emitted('close')).toHaveLength(1);
  });

  it('a non-primary button posts `action`; `{op}` goes up and closes', async () => {
    const ev = vi.fn(async () => ({ op: { id: 9, kind: 'plugin-action', status: 'pending' } }));
    const w = open(ev);
    await w.find('[data-testid="plugin-view-action-cancel"]').trigger('click');
    await flush();
    expect(ev.mock.calls[0][2]).toMatchObject({ event: 'action', action_id: 'cancel' });
    expect(w.emitted('op')).toEqual([[{ id: 9, kind: 'plugin-action', status: 'pending' }]]);
    expect(w.emitted('close')).toHaveLength(1);
  });

  it('a form edit posts ONE `change` 300 ms after the last keystroke, keeping what was typed', async () => {
    const answered: PluginSurface = {
      ...first,
      nodes: [{ type: 'form', props: { fields: [{ key: 'subject', type: 'string' }], values: { subject: 'Ple' } } }],
    };
    const ev = vi.fn(async () => ({ surface: answered }));
    const w = open(ev);
    const input = w.find('#fe-cf-subject');
    await input.setValue('Ple');
    vi.advanceTimersByTime(SURFACE_CHANGE_DEBOUNCE_MS - 1);
    await input.setValue('Please');
    vi.advanceTimersByTime(SURFACE_CHANGE_DEBOUNCE_MS - 1);
    expect(ev).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    await flush();
    expect(ev).toHaveBeenCalledTimes(1);
    expect(ev.mock.calls[0][2]).toMatchObject({ event: 'change', data: { values: { subject: 'Please' } } });
    await w.vm.$nextTick();
    expect((w.find('#fe-cf-subject').element as HTMLInputElement).value).toBe('Please');
  });

  it('a list row action posts `action` with `row_id` and the values', async () => {
    const ev = vi.fn(async () => ({ surface: first }));
    const w = open(ev, {
      nodes: [
        {
          type: 'list',
          props: { columns: [{ key: 'n', label: 'N' }], rows: [{ id: 'r1', cells: { n: 'x' }, actions: [{ id: 'del', label: 'Del' }] }] },
        },
      ],
    });
    await openRowMenu(w, 'surface-list-actions-r1');
    await pickMenuItem('surface-list-action-r1-del');
    closeRowMenus();
    await flush();
    expect(ev.mock.calls[0][2]).toMatchObject({ event: 'action', action_id: 'del', data: { values: {}, row_id: 'r1' } });
  });

  it('a `size` override wins over the surface (home views are drawn xl)', () => {
    const w = mount(PluginViewModal, {
      props: {
        open: true,
        locale: 'en',
        api: { pluginViewEvent: vi.fn() } as never,
        plugin: 'sign',
        view: 'envelopes',
        surface: { ...first, size: 'sm' },
        size: 'xl',
      },
    });
    expect(w.find('.fe-modal__card--xl').exists()).toBe(true);
  });
});
