// An app's screen says it is opening before its first screen arrives, and
// stays up while its answer is on the way.
//
// ⚠ An action that opens a screen showed nothing at all until the app had
// answered `run`: the menu closed and the page sat still. And Escape, a click
// outside or × closed the dialog while a submit was on its way; the server
// still queued the job, but the dialog that hands the row up was gone, so the
// job ran with no row in the operations centre and no word on screen.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import PluginViewModal from '@brftech/filex-core/src/components/plugin/PluginViewModal.vue';
import type { PluginSurface } from '@brftech/filex-core/src/types/Plugins';

const screen: PluginSurface = {
  title: { en: 'Sign' },
  state: { step: 1 },
  nodes: [{ type: 'text', props: { text: { en: 'Send it for signing?' } } }],
  actions: [
    { id: 'cancel', label: { en: 'Cancel' } },
    { id: 'send', label: { en: 'Send' }, primary: true },
  ],
};

const mounted: VueWrapper[] = [];

function open(props: Record<string, unknown>) {
  const w = mount(PluginViewModal, {
    props: { open: true, locale: 'en', plugin: 'sign', view: 'wizard', path: 'docs://nda.pdf', ...props },
    attachTo: document.body,
  });
  mounted.push(w);
  return w;
}

afterEach(() => {
  mounted.splice(0).forEach((w) => w.unmount());
  document.body.innerHTML = '';
});

describe('an app screen', () => {
  it('opened before its first screen says it is loading, under the action’s name', async () => {
    const w = open({ api: {} as never, surface: null, label: 'Sign for approval' });
    await flushPromises();
    expect(document.body.textContent).toContain('Sign for approval');
    expect(w.text()).toContain('Loading…');
  });

  it('is not closed by Escape, a click outside or × while its answer is on the way', async () => {
    let answer: (v: unknown) => void = () => {};
    const ev = vi.fn(() => new Promise((r) => (answer = r)));
    const w = open({ api: { pluginViewEvent: ev } as never, surface: screen });
    await flushPromises();
    await w.get('[data-testid="plugin-view-action-send"]').trigger('click');
    await flushPromises();
    expect(ev).toHaveBeenCalledTimes(1);

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    const backdrop = document.querySelector('.fe-modal__backdrop, .fe-modal') as HTMLElement | null;
    backdrop?.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }));
    backdrop?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    (document.querySelector('.fe-modal__close') as HTMLElement | null)?.click();
    await flushPromises();
    expect(w.emitted('close'), 'the dialog closed while the submit was on its way').toBeUndefined();

    answer({ op: { id: 9, kind: 'plugin-action', status: 'pending' } });
    await flushPromises();
    expect(w.emitted('op'), 'the queued job was not handed up').toEqual([[{ id: 9, kind: 'plugin-action', status: 'pending' }]]);
    expect(w.emitted('close')).toHaveLength(1);
  });

  it('closes on Escape when nothing is on the way', async () => {
    const w = open({ api: { pluginViewEvent: vi.fn() } as never, surface: screen });
    await flushPromises();
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await flushPromises();
    expect(w.emitted('close')).toHaveLength(1);
  });
});

describe('the explorer', () => {
  // FileExplorer is not mounted in unit tests; the wiring is read off its
  // source, like the other explorer wiring tests.
  const src = readFileSync(
    resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'),
    'utf8',
  );
  const run = src.slice(src.indexOf('async function runPluginAction'), src.indexOf('function onPluginOpQueued'));

  it('opens an action’s screen before the app has answered `run`', () => {
    const opensFirst = run.indexOf('openingPluginView(');
    const asks = run.indexOf('await pluginActions.run(');
    expect(opensFirst, 'the dialog is not opened while the app is asked').toBeGreaterThan(-1);
    expect(opensFirst).toBeLessThan(asks);
  });

  it('passes the action’s name to the dialog', () => {
    expect(src).toMatch(/<PluginViewModal[\s\S]*?:label="pluginView\.label"/);
  });
});
