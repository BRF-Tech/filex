// The client-side mirror of wasmplugin.Matches — which selections an app's
// action is offered for. The server re-checks on every run, so a rule that
// disagrees here is a menu row that comes back 422; the matrix below is the
// Go function's, case by case.
import { describe, expect, it } from 'vitest';

import {
  appliesItemOf,
  appliesMatches,
  appliesToNodes,
} from '@brftech/filex-core/src/lib/pluginApplies';
import { pluginActionsFor } from '@brftech/filex-core/src/lib/pluginMenu';
import type { PluginActionRow } from '@brftech/filex-core/src/types/Plugins';

const pdf = { type: 'file', extension: 'pdf', mime_type: 'application/pdf', basename: 'nda.pdf' } as const;
const png = { type: 'file', extension: 'png', mime_type: 'image/png', basename: 'a.png' } as const;
const jpg = { type: 'file', extension: 'jpg', mime_type: 'image/jpeg', basename: 'b.jpg' } as const;
const txt = { type: 'file', extension: 'txt', mime_type: 'text/plain', basename: 'c.txt' } as const;
const dir = { type: 'dir', extension: '', mime_type: '', basename: 'docs' } as const;

describe('appliesMatches — kind', () => {
  it('an empty selection never matches', () => {
    expect(appliesMatches({ kind: 'any' }, [])).toBe(false);
  });
  it('an absent kind means file', () => {
    expect(appliesToNodes({}, [pdf])).toBe(true);
    expect(appliesToNodes({}, [dir])).toBe(false);
  });
  it('dir accepts only folders, any accepts both', () => {
    expect(appliesToNodes({ kind: 'dir' }, [dir])).toBe(true);
    expect(appliesToNodes({ kind: 'dir' }, [pdf])).toBe(false);
    expect(appliesToNodes({ kind: 'any', multi: true }, [dir, pdf])).toBe(true);
  });
});

describe('appliesMatches — ext and mime', () => {
  it('ext is compared lower-case, without the dot', () => {
    expect(appliesToNodes({ ext: ['pdf'] }, [pdf])).toBe(true);
    expect(appliesToNodes({ ext: ['PDF'] }, [pdf])).toBe(true);
    expect(appliesToNodes({ ext: ['pdf'] }, [{ ...pdf, extension: 'PDF' }])).toBe(true);
    expect(appliesToNodes({ ext: ['pdf'] }, [txt])).toBe(false);
  });
  it('falls back to the basename when the row carries no extension', () => {
    // ⚠ `state` is always present (v2): the keys an action's own plugin keeps
    // on the row, prefix stripped. Empty when nobody asked (no plugin name) or
    // when the row carries none — never absent, so no caller has to branch.
    expect(appliesItemOf({ type: 'file', basename: 'Scan.PDF' })).toEqual({
      kind: 'file',
      ext: 'pdf',
      mime: '',
      state: [],
    });
  });

  it('reads only the ASKING plugin\'s state keys off a row (v2)', () => {
    const row = { type: 'file', extension: 'pdf', app_state: ['sign:pending', 'convert:queued'] };
    expect(appliesItemOf(row, 'sign').state).toEqual(['pending']);
    expect(appliesItemOf(row, 'convert').state).toEqual(['queued']);
    // No plugin named — an action can only ever ask about its own keys.
    expect(appliesItemOf(row).state).toEqual([]);
  });

  it('state / no_state decide which of one app\'s rows a file gets', () => {
    const pending = { type: 'file', extension: 'pdf', app_state: ['sign:pending'] };
    const fresh = { type: 'file', extension: 'pdf' };
    // "Sign / Fill" — only where a signature is pending.
    expect(appliesToNodes({ ext: ['pdf'], state: ['pending'] }, [pending], 'sign')).toBe(true);
    expect(appliesToNodes({ ext: ['pdf'], state: ['pending'] }, [fresh], 'sign')).toBe(false);
    // "Request signatures" — only where none is.
    expect(appliesToNodes({ ext: ['pdf'], no_state: ['pending'] }, [fresh], 'sign')).toBe(true);
    expect(appliesToNodes({ ext: ['pdf'], no_state: ['pending'] }, [pending], 'sign')).toBe(false);
    // ⚠ Another app's key of the same name is not this app's: the prefix is
    // the whole point, and matching without it is how a state rule silently
    // never fires.
    expect(appliesToNodes({ ext: ['pdf'], state: ['pending'] }, [pending], 'convert')).toBe(false);
  });
  it('mime is exact or an image/* prefix', () => {
    expect(appliesToNodes({ mime: ['image/png'] }, [png])).toBe(true);
    expect(appliesToNodes({ mime: ['image/png'] }, [jpg])).toBe(false);
    expect(appliesToNodes({ mime: ['image/*'], multi: true }, [png, jpg])).toBe(true);
    expect(appliesToNodes({ mime: ['image/*'] }, [pdf])).toBe(false);
  });
  it('either list is enough when both are given', () => {
    const rule = { ext: ['pdf'], mime: ['image/*'], multi: true };
    expect(appliesToNodes(rule, [pdf, png])).toBe(true);
    expect(appliesToNodes(rule, [pdf, txt])).toBe(false);
  });
  it('with both lists empty anything of the right kind passes', () => {
    expect(appliesToNodes({ kind: 'file', multi: true }, [pdf, txt, png])).toBe(true);
  });
});

describe('appliesMatches — multi, min, max', () => {
  it('two rows need multi', () => {
    expect(appliesToNodes({ ext: ['pdf'] }, [pdf, pdf])).toBe(false);
    expect(appliesToNodes({ ext: ['pdf'], multi: true }, [pdf, pdf])).toBe(true);
  });
  it('min and max bound the count only when set', () => {
    expect(appliesToNodes({ multi: true, min: 2 }, [pdf])).toBe(false);
    expect(appliesToNodes({ multi: true, min: 2 }, [pdf, pdf])).toBe(true);
    expect(appliesToNodes({ multi: true, max: 2 }, [pdf, pdf, pdf])).toBe(false);
    expect(appliesToNodes({ multi: true, max: 0 }, [pdf, pdf, pdf])).toBe(true);
  });
});

describe('pluginActionsFor — the rows a selection gets', () => {
  const sign: PluginActionRow = { plugin: 'sign', id: 'sign', label: { en: 'Sign…' }, applies: { kind: 'file', ext: ['pdf'] } };
  const shrink: PluginActionRow = { plugin: 'img', id: 'shrink', label: { en: 'Shrink' }, applies: { mime: ['image/*'], multi: true } };
  const zipdir: PluginActionRow = { plugin: 'zip', id: 'dir', label: { en: 'Zip folder' }, applies: { kind: 'dir' } };
  const all = [sign, shrink, zipdir];

  it('a pdf gets Sign only', () => {
    expect(pluginActionsFor(all, [pdf]).map((a) => a.id)).toEqual(['sign']);
  });
  it('two images get Shrink only', () => {
    expect(pluginActionsFor(all, [png, jpg]).map((a) => a.id)).toEqual(['shrink']);
  });
  it('a folder gets the folder action', () => {
    expect(pluginActionsFor(all, [dir]).map((a) => a.id)).toEqual(['dir']);
  });
  it('a mixed selection gets nothing', () => {
    expect(pluginActionsFor(all, [pdf, png])).toEqual([]);
    expect(pluginActionsFor(all, [])).toEqual([]);
  });
});
