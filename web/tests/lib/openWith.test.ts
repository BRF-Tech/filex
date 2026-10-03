// 0.50 - which handler opens a file (lib/appViewer) and the person's
// "always open with" choice (lib/openWith): docs/APP-PLUGINS.md → Default apps.
//
// The rule is the server's (backend internal/assoc Apply) run in the browser:
// the administrator's order first, the switched-off handlers out, the rest in
// the default order (the apps' viewers, then filex's own). The person picks
// only among the ones that are on; a choice switched off since is kept and NOT
// used.
import { describe, expect, it, vi } from 'vitest';
import {
  normalizeOpenChoice,
  openHandlersFor,
  openKindOf,
  pickAppViewer,
  pickOpenHandler,
} from '@brftech/filex-core/src/lib/appViewer';
import {
  clearOpenWithChoices,
  openWithChoice,
  openWithChoices,
  setOpenWithChoice,
} from '@brftech/filex-core/src/lib/openWith';
import { configurePrefs, currentPrefs, flushPrefs } from '@brftech/filex-core/src/lib/prefs';
import type { PluginViewRow } from '@brftech/filex-core/src/types/Plugins';
import { answerAccountPrefs } from '../helpers/accountPrefs';

answerAccountPrefs();

const view = (plugin: string, id: string, ext: string[]): PluginViewRow =>
  ({
    plugin,
    id,
    placement: 'viewer',
    label: { en: plugin, tr: plugin },
    applies: { kind: 'file', ext },
    ui: { url: `/_appui/${plugin}/0123456789abcdef/index.html`, grants: [], engine: false, version: '1' },
  }) as unknown as PluginViewRow;

const VIEWS = [view('drawio', 'editor', ['drawio']), view('zeta', 'viewer', ['drawio', 'svg'])];
const file = (name: string) => ({ type: 'file', basename: name, extension: name.split('.').pop() ?? '' });
const ids = (hs: { id: string }[]) => hs.map((h) => h.id);

describe('openHandlersFor - the administrator’s order', () => {
  it('is the apps, then filex’s own, until a rule says otherwise', () => {
    const r = openHandlersFor(VIEWS, file('a.drawio'));
    expect(ids(r.on)).toEqual(['app:drawio/editor', 'app:zeta/viewer', 'builtin']);
    expect(r.custom).toBe(false);
    expect(ids(openHandlersFor(VIEWS, file('a.txt')).on)).toEqual(['builtin']);
    expect(openHandlersFor(VIEWS, { type: 'dir', basename: 'x' }).on).toEqual([]);
  });

  it('puts the named ones first, leaves the switched-off ones out, keeps the rest in the default order', () => {
    const rules = { drawio: { order: ['builtin', 'app:gone/x'], off: ['app:drawio/editor'] } };
    const r = openHandlersFor(VIEWS, file('A.DRAWIO'), rules);
    expect(ids(r.on)).toEqual(['builtin', 'app:zeta/viewer']);
    expect(ids(r.off)).toEqual(['app:drawio/editor']);
    expect(r.custom).toBe(true);
    expect(ids(openHandlersFor(VIEWS, file('b.svg'), rules).on), 'the rule is per kind').toEqual(['app:zeta/viewer', 'builtin']);
  });

  it('a handler both named and switched off is off, as on the server (assoc.Apply)', () => {
    const rules = { drawio: { order: ['app:drawio/editor', 'builtin'], off: ['app:drawio/editor'] } };
    const r = openHandlersFor(VIEWS, file('a.drawio'), rules);
    expect(ids(r.on)).toEqual(['builtin', 'app:zeta/viewer']);
    expect(ids(r.off)).toEqual(['app:drawio/editor']);
    expect(pickOpenHandler(VIEWS, file('a.drawio'), 'app:drawio/editor', rules)?.id).toBe('builtin');
  });
});

describe('pickOpenHandler - the one that opens it', () => {
  const rules = { drawio: { order: [], off: ['app:zeta/viewer'] } };

  it('follows "Open with", then the person’s choice, then the first that is on', () => {
    const f = file('a.drawio');
    expect(pickOpenHandler(VIEWS, f)?.id).toBe('app:drawio/editor');
    expect(pickOpenHandler(VIEWS, f, null, null, 'builtin')?.id).toBe('builtin');
    expect(pickOpenHandler(VIEWS, f, 'zeta/viewer', null, 'builtin')?.id, 'Open with wins').toBe('app:zeta/viewer');
    expect(pickAppViewer(VIEWS, f, null, null, 'app:zeta/viewer')?.plugin).toBe('zeta');
  });

  it('never uses a handler the administrator switched off - not for Open with, not for the person', () => {
    const f = file('a.drawio');
    expect(pickOpenHandler(VIEWS, f, 'app:zeta/viewer', rules)?.id).toBe('app:drawio/editor');
    expect(pickOpenHandler(VIEWS, f, null, rules, 'app:zeta/viewer')?.id, 'the next one that is on').toBe('app:drawio/editor');
  });

  it('opens in filex’s own viewer when every handler is off: a file must open somewhere', () => {
    const allOff = { drawio: { order: [], off: ['app:drawio/editor', 'app:zeta/viewer', 'builtin'] } };
    expect(pickOpenHandler(VIEWS, file('a.drawio'), null, allOff)).toBeNull();
    expect(pickAppViewer(VIEWS, file('a.drawio'), null, allOff)).toBeNull();
  });

  it('reads the spelling before 0.50 too', () => {
    expect(normalizeOpenChoice('drawio/editor')).toBe('app:drawio/editor');
    expect(normalizeOpenChoice('app:drawio/editor')).toBe('app:drawio/editor');
    expect(normalizeOpenChoice('builtin')).toBe('builtin');
    expect(normalizeOpenChoice('')).toBeNull();
    expect(openKindOf(file('x.tar.GZ'))).toBe('gz');
  });
});

describe('openWith - kept on the account', () => {
  it('keeps, forgets and clears a choice per kind, in the account document', () => {
    clearOpenWithChoices();
    setOpenWithChoice('DrawIO', 'app:drawio/editor');
    setOpenWithChoice('svg', 'builtin');
    expect(openWithChoice('drawio')).toBe('app:drawio/editor');
    expect(openWithChoices()).toEqual({ drawio: 'app:drawio/editor', svg: 'builtin' });
    expect(JSON.parse(currentPrefs().openWith ?? '{}')).toEqual({ drawio: 'app:drawio/editor', svg: 'builtin' });
    setOpenWithChoice('svg', null);
    expect(openWithChoice('svg')).toBeNull();
    clearOpenWithChoices();
    expect(openWithChoices()).toEqual({});
  });

  it('is ONE record for the account: each change goes to /api/me/open-with, never with the surface document', async () => {
    const calls: Array<{ url: string; method: string; body?: string }> = [];
    let answer: Record<string, string> = {};
    configurePrefs({
      surface: 'desktop',
      fetchImpl: (async (url: string, init?: RequestInit) => {
        calls.push({ url: String(url), method: init?.method ?? 'GET', body: init?.body as string | undefined });
        return new Response(JSON.stringify({ choices: answer }), { status: 200 });
      }) as unknown as typeof fetch,
    });

    answer = { drawio: 'app:drawio/editor', svg: 'builtin' };
    setOpenWithChoice('DrawIO', 'app:drawio/editor');
    await vi.waitFor(() => expect(calls.length).toBeGreaterThan(0));
    expect(calls[0]).toEqual({ url: '/api/me/open-with/drawio', method: 'PUT', body: JSON.stringify({ handler: 'app:drawio/editor' }) });
    // The account's answer is what the page keeps: here it also holds a
    // choice another surface made since this page booted.
    await vi.waitFor(() => expect(openWithChoices()).toEqual({ drawio: 'app:drawio/editor', svg: 'builtin' }));

    answer = { svg: 'builtin' };
    setOpenWithChoice('drawio', null);
    await vi.waitFor(() => expect(calls.length).toBe(2));
    expect(calls[1]).toMatchObject({ url: '/api/me/open-with/drawio', method: 'DELETE' });

    answer = {};
    clearOpenWithChoices();
    await vi.waitFor(() => expect(calls.length).toBe(3));
    expect(calls[2]).toMatchObject({ url: '/api/me/open-with', method: 'DELETE' });

    await flushPrefs();
    expect(calls.filter((c) => c.url.includes('/api/me/prefs')), 'nothing went with the surface document').toEqual([]);
  });

  it('refuses what is not a kind or a handler', () => {
    clearOpenWithChoices();
    setOpenWithChoice('a b', 'builtin');
    setOpenWithChoice('drawio', 'javascript:alert(1)');
    setOpenWithChoice('drawio', 'app:../x');
    expect(openWithChoices()).toEqual({});
  });
});
