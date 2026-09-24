// "A screen can send you to a file" (v3 §3.0).
//
// ⚠⚠ Why this exists at all: a surface runs on the file it was opened on, so
// an app's HOME screen could list documents and then do NOTHING when a row
// was clicked — there was nowhere for a screen to send anybody. `Surface.open`
// is that missing edge.
//
// ⚠ The address is the one a notification deep link already uses
// (`?select=…&app=…&appAction|appView=…#folder`). A second spelling of "open
// this file with that screen on it" would be a second thing to keep working.
import { describe, expect, it, vi } from 'vitest';

import { isOpenRequest, openHashFor, openTargetFor } from '@brftech/filex-core';
import { usePluginSurface } from '@brftech/filex-core/src/composables/usePluginSurface';
import type { PluginSurface } from '@brftech/filex-core/src/types/Plugins';

describe('the request itself', () => {
  it('needs a path and nothing else', () => {
    expect(isOpenRequest({ path: 'depo://a.pdf' })).toBe(true);
    expect(isOpenRequest({ path: '   ' })).toBe(false);
    expect(isOpenRequest({ action: 'fill' })).toBe(false);
    expect(isOpenRequest(null)).toBe(false);
  });

  it('the hash is the FOLDER, because that is what the explorer opens', () => {
    expect(openHashFor('depo://reports/2026/nda.pdf')).toBe('depo/reports/2026');
    expect(openHashFor('depo://nda.pdf')).toBe('depo');
    expect(openHashFor('nonsense')).toBe('');
  });
});

describe('where it sends the person', () => {
  it('to the file, with the named ACTION, through the explorer', () => {
    const t = openTargetFor({ path: 'depo://reports/nda.pdf', action: 'fill' }, { plugin: 'sign', base: '/admin/' });
    expect(t.newTab).toBe(false);
    expect(t.href).toBe('/admin/explore?select=depo%3A%2F%2Freports%2Fnda.pdf&app=sign&appAction=fill#depo/reports');
  });

  it('a `page` VIEW opens on its own address, in its own tab', () => {
    // ⚠ The same rule a `page` action follows from the menu, so a plugin's
    // screen behaves the same however it was reached.
    const t = openTargetFor(
      { path: 'depo://nda.pdf', view: 'sign-fill' },
      { plugin: 'sign', base: '/admin/', placementOf: (id) => (id === 'sign-fill' ? 'page' : 'modal') },
    );
    expect(t.newTab).toBe(true);
    expect(t.href).toBe('/admin/apps/sign/sign-fill?path=depo%3A%2F%2Fnda.pdf');
  });

  it('a view whose placement is unknown goes through the explorer, which knows', () => {
    const t = openTargetFor({ path: 'depo://nda.pdf', view: 'status' }, { plugin: 'sign', base: '/drive/' });
    expect(t.newTab).toBe(false);
    expect(t.href).toContain('/drive/explore?select=');
    expect(t.href).toContain('appView=status');
  });

  it('naming neither just opens the file', () => {
    const t = openTargetFor({ path: 'depo://nda.pdf' }, { plugin: 'sign', base: '/admin/' });
    expect(t.href).toBe('/admin/explore?select=depo%3A%2F%2Fnda.pdf&app=sign#depo');
  });

  it('a frame with nowhere to go gets nowhere to go', () => {
    // A public page has no explorer behind it and must ignore the request.
    expect(openTargetFor({ path: 'depo://a.pdf' }, { plugin: '' }).href).toBe('');
    expect(openTargetFor(null, { plugin: 'sign' }).href).toBe('');
  });
});

describe('the conversation raises it', () => {
  function conv(answer: PluginSurface, events: Record<string, unknown> = {}) {
    const seen: string[] = [];
    const store = usePluginSurface(
      {
        api: { pluginViewEvent: async () => ({ surface: answer }) },
        plugin: 'sign',
        view: 'home',
        locale: () => 'en',
        errorText: () => 'err',
      },
      {
        onOp: () => seen.push('op'),
        onDone: () => seen.push('done'),
        onToast: () => seen.push('toast'),
        onOpen: (req) => seen.push(`open:${req.path}`),
        ...events,
      },
    );
    return { store, seen };
  }

  const base: PluginSurface = {
    nodes: [{ id: 'list', type: 'list', props: { columns: [], rows: [] } }],
    actions: [{ id: 'go', label: { en: 'Go' }, primary: true }],
  };

  it('raises `open` with the answer', async () => {
    const { store, seen } = conv({ ...base, open: { path: 'depo://nda.pdf', action: 'fill' } });
    store.setSurface(base);
    await store.press(base.actions![0]);
    expect(seen).toEqual(['open:depo://nda.pdf']);
  });

  it('with `done` it CLOSES first, then goes', async () => {
    // ⚠ The order is the contract's. Reversed, the person lands on the new
    // screen with the finished one still drawn over it.
    const { store, seen } = conv({ ...base, done: true, open: { path: 'depo://nda.pdf' } });
    store.setSurface(base);
    await store.press(base.actions![0]);
    expect(seen).toEqual(['done', 'open:depo://nda.pdf']);
  });

  it('ignores an `open` with no path rather than navigating nowhere', async () => {
    const { store, seen } = conv({ ...base, open: { path: '' } });
    store.setSurface(base);
    await store.press(base.actions![0]);
    expect(seen).toEqual([]);
  });

  it('a frame that passes no handler is simply not navigated', async () => {
    const onOpen = vi.fn();
    const { store } = conv({ ...base, open: { path: 'depo://nda.pdf' } }, { onOpen: undefined });
    store.setSurface(base);
    await expect(store.press(base.actions![0])).resolves.toBeUndefined();
    expect(onOpen).not.toHaveBeenCalled();
  });
});
