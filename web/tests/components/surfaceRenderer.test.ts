import { closeRowMenus, openRowMenu, pickMenuItem } from '../helpers/rowMenu';
// SurfaceRenderer — the M2 catalogue, each node drawn from props and each
// value-holding node writing back into ONE map the host owns. What is
// pinned here is the event SHAPE the host posts to the plugin: a form edit
// is `update:values` + `change`, a list button is `action` with the row id,
// a person / a PIN / a file lands under the node's id.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';

import SurfaceRenderer from '@brftech/filex-core/src/components/plugin/SurfaceRenderer.vue';
import type { SurfaceNode } from '@brftech/filex-core/src/types/Plugins';

async function flush() {
  for (let i = 0; i < 6; i++) await Promise.resolve();
}

function draw(nodes: SurfaceNode[], extra: Record<string, unknown> = {}) {
  return mount(SurfaceRenderer, {
    props: { nodes, locale: 'en', values: {}, ...extra },
  });
}

describe('SurfaceRenderer — M2 nodes', () => {
  it('form: an edit emits the whole value map (flat by field key) and `change`', async () => {
    const w = draw(
      [
        {
          type: 'form',
          props: { fields: [{ key: 'quality', type: 'int', label: { en: 'Quality' } }, { key: 'note', type: 'string' }] },
        },
      ],
      { values: { note: 'keep' } },
    );
    expect(w.find('label[for="fe-cf-quality"]').text()).toContain('Quality');
    await w.find('#fe-cf-quality').setValue('85');
    expect(w.emitted('update:values')).toEqual([[{ note: 'keep', quality: 85 }]]);
    expect(w.emitted('change')).toHaveLength(1);
  });

  it('form: `errors[key]` marks the field invalid with the plugin\'s own words', () => {
    const w = draw(
      [{ type: 'form', props: { fields: [{ key: 'email', type: 'string' }] } }],
      { errors: { email: { en: 'Not a valid address', tr: 'Geçersiz adres' } } },
    );
    expect(w.find('.fe-cfield__row').classes()).toContain('is-invalid');
    expect(w.find('.fe-cfield__error').text()).toBe('Not a valid address');
  });

  it('list: a row button emits `action` with the button id and the row id', async () => {
    const w = draw([
      {
        type: 'list',
        props: {
          columns: [{ key: 'who', label: { en: 'Who' } }],
          rows: [
            { id: 'r1', cells: { who: 'Ayşe' }, actions: [{ id: 'remind', label: { en: 'Remind' } }] },
            { id: 'r2', cells: { who: { en: 'Bob' } } },
          ],
        },
      },
    ]);
    // The product's one table (DataTable): a row is `.fe-list__row`.
    expect(w.findAll('.fe-list__row')).toHaveLength(2);
    expect(w.text()).toContain('Bob');
    // ⚠ The row's verbs are behind its ONE `Actions` control now, and the
    // menu teleports to <body> — so the button is opened first and the entry
    // is picked in the document, not in this wrapper's tree. The action's own
    // address is unchanged: a plugin's `action_id` still names its entry.
    expect(w.findAll('.tbl-rowactions').length).toBe(1);
    await openRowMenu(w, 'surface-list-actions-r1');
    await pickMenuItem('surface-list-action-r1-remind');
    expect(w.emitted('action')).toEqual([[{ action_id: 'remind', row_id: 'r1' }]]);
    closeRowMenus();
  });

  it('list: no rows → the `empty` text (else the catalogue line)', () => {
    const w = draw([{ type: 'list', props: { columns: [], rows: [], empty: { en: 'No envelopes' } } }]);
    expect(w.text()).toContain('No envelopes');
    const w2 = draw([{ type: 'list', props: { columns: [], rows: [] } }]);
    expect(w2.text()).toContain('Nothing here yet');
  });

  it('steps: the active step is marked, the done one ticked', () => {
    const w = draw([
      {
        type: 'steps',
        props: {
          items: [
            { id: 'a', label: { en: 'Upload' }, state: 'done' },
            { id: 'b', label: { en: 'Sign' }, state: 'active' },
            { id: 'c', label: 'Send' },
          ],
        },
      },
    ]);
    const items = w.findAll('.fe-steps__item');
    expect(items.map((i) => i.classes().find((c) => c.startsWith('is-')))).toEqual(['is-done', 'is-active', 'is-todo']);
    expect(items[1].attributes('aria-current')).toBe('step');
    expect(w.text()).toContain('Send');
  });

  it('progress: a value is a progressbar with aria-valuenow, null is indeterminate', () => {
    const w = draw([{ type: 'progress', props: { value: 42, label: { en: 'Signing' } } }]);
    const bar = w.find('[role="progressbar"]');
    expect(bar.attributes('aria-valuenow')).toBe('42');
    expect(w.text()).toContain('42%');
    const w2 = draw([{ type: 'progress', props: { value: null } }]);
    expect(w2.find('[role="progressbar"]').classes()).toContain('is-indeterminate');
    expect(w2.find('[role="progressbar"]').attributes('aria-valuenow')).toBeUndefined();
  });

  it('pin-input: the length is clamped to 4..8 and the value truncated to it', async () => {
    const w = draw([{ id: 'pin', type: 'pin-input', props: { length: 12 } }]);
    const input = w.find('[data-testid="surface-pin"] input');
    expect(input.attributes('maxlength')).toBe('8');
    await input.setValue('12 3456789012');
    expect(w.emitted('update:values')).toEqual([[{ pin: '12345678' }]]);
  });

  it('unknown node types stay visible as unsupported (M3 types included)', () => {
    const w = draw([{ type: 'signature-pad', props: {} }, { type: 'text', props: { text: 'ok' } }]);
    expect(w.find('.fe-surface__unknown').attributes('data-node-type')).toBe('signature-pad');
    expect(w.text()).toContain('ok');
  });
});

describe('SurfaceRenderer — people-picker', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  const node: SurfaceNode = { id: 'to', type: 'people-picker', props: { multi: true, allow_external: false } };

  it('searches internal users through the plugin lookup and adds a chip', async () => {
    const pluginUsers = vi.fn(async () => ({ users: [{ user_id: 3, email: 'ayse@x.io', name: 'Ayşe' }] }));
    const w = draw([node], { api: { pluginUsers }, plugin: 'sign', values: { to: [{ email: 'bob@x.io', name: 'Bob' }] } });
    expect(w.findAll('[data-testid="surface-people-chips"] li')).toHaveLength(1);
    expect(w.find('[data-testid="surface-people-add"]').exists()).toBe(false);

    await w.find('[data-testid="surface-people-input"]').setValue('ay');
    vi.advanceTimersByTime(200);
    await flush();
    expect(pluginUsers).toHaveBeenCalledWith('sign', 'ay');
    await w.vm.$nextTick();
    const sugg = w.find('[data-testid="surface-people-suggest"] li');
    expect(sugg.exists()).toBe(true);
    await sugg.trigger('mousedown');
    expect(w.emitted('update:values')).toEqual([
      [{ to: [{ email: 'bob@x.io', name: 'Bob' }, { user_id: 3, email: 'ayse@x.io', name: 'Ayşe' }] }],
    ]);
  });

  it('falls back to free e-mail entry when the lookup answers 403', async () => {
    const pluginUsers = vi.fn(async () => {
      throw Object.assign(new Error('Forbidden'), { status: 403 });
    });
    const w = draw([node], { api: { pluginUsers }, plugin: 'sign' });
    const input = w.find('[data-testid="surface-people-input"]');
    await input.setValue('c');
    vi.advanceTimersByTime(200);
    await flush();
    await w.vm.$nextTick();
    expect(pluginUsers).toHaveBeenCalledTimes(1);
    expect(input.attributes('placeholder')).toBe('Email address');
    expect(w.find('[data-testid="surface-people-add"]').exists()).toBe(true);

    await input.setValue('carol@x.io');
    vi.advanceTimersByTime(200);
    await flush();
    expect(pluginUsers).toHaveBeenCalledTimes(1);
    await input.trigger('keydown', { key: 'Enter' });
    expect(w.emitted('update:values')).toEqual([[{ to: [{ email: 'carol@x.io' }] }]]);
  });

  it('refuses a typed address when externals are off and the lookup works', async () => {
    const pluginUsers = vi.fn(async () => ({ users: [] }));
    const w = draw([node], { api: { pluginUsers }, plugin: 'sign' });
    const input = w.find('[data-testid="surface-people-input"]');
    await input.setValue('carol@x.io');
    await input.trigger('keydown', { key: 'Enter' });
    expect(w.emitted('update:values')).toBeUndefined();
    expect(w.find('.fe-surface__error').text()).toContain('account');
  });

  it('single (`multi` off): one chip, and the box goes away', () => {
    const w = draw([{ id: 'to', type: 'people-picker', props: { allow_external: true } }], {
      values: { to: [{ email: 'a@b.c' }] },
    });
    expect(w.findAll('[data-testid="surface-people-chips"] li')).toHaveLength(1);
    expect(w.find('[data-testid="surface-people-input"]').exists()).toBe(false);
  });
});

describe('SurfaceRenderer — file-chooser', () => {
  const listing = (path: string, files: Array<Record<string, unknown>>) => ({
    adapter: 'main',
    storages: ['main'],
    dirname: path,
    read_only: false,
    perm: 'owner',
    files,
  });
  const TREE: Record<string, ReturnType<typeof listing>> = {
    'main://': listing('main://', [
      { path: 'main://docs', basename: 'docs', type: 'dir' },
      { path: 'main://nda.pdf', basename: 'nda.pdf', type: 'file' },
    ]),
  };

  it('opens the explorer\'s picker and answers an adapter-qualified file path', async () => {
    const index = vi.fn(async (p: string) => TREE[p]);
    const w = draw([{ id: 'src', type: 'file-chooser', props: { kind: 'file' } }], {
      api: { index },
      storages: ['main'],
      startAt: 'main://',
    });
    expect(w.find('[data-testid="surface-file-chooser-value"]').text()).toBe('Nothing chosen');
    await w.find('[data-testid="surface-file-chooser-open"]').trigger('click');
    await flush();
    await new Promise((r) => setTimeout(r, 0));
    expect(index).toHaveBeenCalledWith('main://');
    const file = w.find('[data-testid="destpicker-row-nda.pdf"]');
    expect(file.exists()).toBe(true);
    const confirm = w.find('[data-testid="destpicker-confirm"]');
    expect(confirm.attributes('disabled')).toBeDefined();
    await file.trigger('click');
    expect(confirm.attributes('disabled')).toBeUndefined();
    await confirm.trigger('click');
    expect(w.emitted('update:values')).toEqual([[{ src: 'main://nda.pdf' }]]);
  });

  it('shows the chosen name and clears to an empty string', async () => {
    const w = draw([{ id: 'src', type: 'file-chooser', props: { kind: 'file' } }], {
      api: { index: vi.fn() },
      values: { src: 'main://docs/nda.pdf' },
    });
    expect(w.find('[data-testid="surface-file-chooser-value"]').text()).toBe('nda.pdf');
    await w.find('[data-testid="surface-file-chooser-clear"]').trigger('click');
    expect(w.emitted('update:values')).toEqual([[{ src: '' }]]);
  });

  it('without an api the button is disabled rather than broken', () => {
    const w = draw([{ id: 'src', type: 'file-chooser', props: { kind: 'dir' } }]);
    expect(w.find('[data-testid="surface-file-chooser-open"]').attributes('disabled')).toBeDefined();
  });
});

describe('SurfaceRenderer — preview', () => {
  beforeEach(() => {
    if (!('revokeObjectURL' in URL)) (URL as unknown as { revokeObjectURL: () => void }).revokeObjectURL = () => {};
  });

  it('shows a picture through the authenticated preview fetch', async () => {
    const fetchBlob = vi.fn(async () => ({ url: 'blob:pic', mime: 'image/png', blob: new Blob() }));
    const w = draw([{ type: 'preview', props: { path: 'main://photos/a.png' } }], { api: { fetchBlob } });
    await flush();
    await w.vm.$nextTick();
    expect(fetchBlob).toHaveBeenCalledWith('main://photos/a.png');
    const img = w.find('img.fe-spreview__media');
    expect(img.exists()).toBe(true);
    expect(img.attributes('src')).toBe('blob:pic');
    expect(w.text()).toContain('a.png');
  });

  it('a refused file says so instead of erroring', async () => {
    const fetchBlob = vi.fn(async () => {
      throw new Error('403');
    });
    const w = draw([{ type: 'preview', props: { path: 'main://secret.pdf' } }], { api: { fetchBlob } });
    await flush();
    await w.vm.$nextTick();
    expect(w.find('[data-testid="surface-preview"]').attributes('data-state')).toBe('error');
    expect(w.text()).toContain('could not be loaded');
  });
});
