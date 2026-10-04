// filex 0.51 (GitHub #81, work #135): a .csv opens in ONLYOFFICE while it is
// configured - the product's default, not something each install sets up -
// with filex's read-only table second; without ONLYOFFICE the table alone.
// ONLYOFFICE is a choice like any other: "Open with", the person's "Always
// open with" (lib/openWith) and the administrator's Default apps all take it,
// and a choice of it falls to the table, without an error, while ONLYOFFICE
// is switched off.
//
// ⚠ Every case here fails on 0.50: ONLYOFFICE was no open handler at all
// (lib/appViewer offered the apps and `builtin` only, lib/openWith refused
// the id, normalizeOpenChoice turned it into `app:onlyoffice`).
import { describe, expect, it, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import * as appViewer from '@brftech/filex-core/src/lib/appViewer';
import * as serviceGate from '@brftech/filex-core/src/lib/serviceGate';
import { openWithChoice, setOpenWithChoice, clearOpenWithChoices } from '@brftech/filex-core/src/lib/openWith';
import { configurePrefs, currentPrefs, flushPrefs } from '@brftech/filex-core/src/lib/prefs';
import type { PluginViewRow } from '@brftech/filex-core/src/types/Plugins';
import { answerAccountPrefs } from '../helpers/accountPrefs';

answerAccountPrefs();

const { openHandlersFor, pickOpenHandler, normalizeOpenChoice } = appViewer;
const file = (name: string) => ({ type: 'file', basename: name, extension: name.split('.').pop() ?? '' });
const ids = (hs: { id: string }[]) => hs.map((h) => h.id);
const ON = { onlyOffice: true };
const OFF = { onlyOffice: false };

const sheetApp = {
  plugin: 'sheet',
  id: 'view',
  placement: 'viewer',
  label: { en: 'Sheet' },
  applies: { kind: 'file', ext: ['csv'] },
  ui: { url: '/_appui/sheet/0123456789abcdef/index.html', grants: [], engine: false, version: '1' },
} as unknown as PluginViewRow;

describe('a .csv opens in ONLYOFFICE while it is configured', () => {
  it('ONLYOFFICE first, filex’s table second; without it, the table alone', () => {
    expect(ids(openHandlersFor([], file('liste.csv'), null, ON).on)).toEqual(['onlyoffice', 'builtin']);
    expect(ids(openHandlersFor([], file('liste.csv'), null, OFF).on)).toEqual(['builtin']);
    expect(ids(openHandlersFor([], file('liste.csv')).on), 'a host that says nothing has no ONLYOFFICE').toEqual(['builtin']);
    expect(pickOpenHandler([], file('liste.csv'), null, null, null, ON)?.id).toBe('onlyoffice');
    expect(pickOpenHandler([], file('liste.csv'), null, null, null, OFF)?.id).toBe('builtin');
  });

  it('only .csv: an office document is not opened through the chain, nor a .tsv', () => {
    for (const name of ['rapor.docx', 'tablo.xlsx', 'liste.tsv', 'notlar.txt']) {
      expect(ids(openHandlersFor([], file(name), null, ON).on), name).toEqual(['builtin']);
    }
  });

  it('an app that opens .csv comes after ONLYOFFICE, before the table (backend assoc.OpenDefault)', () => {
    expect(ids(openHandlersFor([sheetApp], file('a.csv'), null, ON).on)).toEqual(['onlyoffice', 'app:sheet/view', 'builtin']);
  });

  it('the administrator’s order decides, and a rule naming ONLYOFFICE falls to the table while it is off', () => {
    const rules = { csv: { order: ['builtin', 'onlyoffice'], off: [] } };
    expect(ids(openHandlersFor([], file('a.csv'), rules, ON).on)).toEqual(['builtin', 'onlyoffice']);
    const first = { csv: { order: ['onlyoffice', 'builtin'], off: [] } };
    expect(pickOpenHandler([], file('a.csv'), null, first, null, OFF)?.id).toBe('builtin');
    const off = { csv: { order: [], off: ['onlyoffice'] } };
    const r = openHandlersFor([], file('a.csv'), off, ON);
    expect(ids(r.on)).toEqual(['builtin']);
    expect(ids(r.off)).toEqual(['onlyoffice']);
  });

  it('the person’s choice and "Open with" are followed while ONLYOFFICE is on, and fall to the table while it is off', () => {
    expect(pickOpenHandler([], file('a.csv'), null, null, 'builtin', ON)?.id).toBe('builtin');
    expect(pickOpenHandler([], file('a.csv'), 'builtin', null, 'onlyoffice', ON)?.id).toBe('builtin');
    expect(pickOpenHandler([], file('a.csv'), 'onlyoffice', null, 'builtin', ON)?.id).toBe('onlyoffice');
    expect(pickOpenHandler([], file('a.csv'), null, null, 'onlyoffice', OFF)?.id).toBe('builtin');
    expect(pickOpenHandler([], file('a.csv'), 'onlyoffice', null, null, OFF)?.id).toBe('builtin');
  });

  it('the id is ONLYOFFICE’s, not an app’s', () => {
    expect(normalizeOpenChoice('onlyoffice')).toBe('onlyoffice');
    expect(appViewer.isOfficeHandler({ id: 'onlyoffice' })).toBe(true);
    expect(appViewer.isOfficeHandler({ id: 'builtin' })).toBe(false);
  });

  it('the kinds are the backend’s: one list, twinned (backend internal/assoc onlyOfficeOpenKinds)', () => {
    const go = readFileSync(resolve(__dirname, '../../../backend/internal/assoc/assoc.go'), 'utf8');
    const m = go.match(/var onlyOfficeOpenKinds = \[\]string\{([^}]*)\}/);
    expect(m, 'the Go list is where it was').not.toBeNull();
    const kinds = [...m![1].matchAll(/"([a-z0-9]+)"/g)].map((x) => x[1]);
    expect([...appViewer.OFFICE_OPEN_KINDS]).toEqual(kinds);
    expect(kinds).toEqual(['csv']);
  });
});

describe('"Always open with ONLYOFFICE" is kept on the account', () => {
  it('for a .csv, and for nothing ONLYOFFICE does not open as a choice', async () => {
    const calls: Array<{ url: string; method: string; body?: string }> = [];
    configurePrefs({
      surface: 'web',
      fetchImpl: (async (url: string, init?: RequestInit) => {
        calls.push({ url: String(url), method: init?.method ?? 'GET', body: init?.body as string | undefined });
        return new Response(JSON.stringify({ choices: { csv: 'onlyoffice' } }), { status: 200 });
      }) as unknown as typeof fetch,
    });
    clearOpenWithChoices();
    setOpenWithChoice('csv', 'onlyoffice');
    expect(openWithChoice('csv')).toBe('onlyoffice');
    expect(JSON.parse(currentPrefs().openWith ?? '{}')).toEqual({ csv: 'onlyoffice' });
    await vi.waitFor(() => expect(calls.some((c) => c.url === '/api/me/open-with/csv')).toBe(true));
    expect(calls.find((c) => c.url === '/api/me/open-with/csv')).toEqual({
      url: '/api/me/open-with/csv',
      method: 'PUT',
      body: JSON.stringify({ handler: 'onlyoffice' }),
    });
    setOpenWithChoice('docx', 'onlyoffice');
    setOpenWithChoice('tsv', 'onlyoffice');
    expect(openWithChoice('docx')).toBeNull();
    expect(openWithChoice('tsv')).toBeNull();
    expect(calls.filter((c) => c.url.endsWith('/docx') || c.url.endsWith('/tsv')), 'nothing sent for them').toEqual([]);
    await flushPrefs();
  });
});

describe('ONLYOFFICE is there, by the capabilities answer (lib/serviceGate onlyOfficeUsable)', () => {
  it('an address, and a probe that did not fail', () => {
    const usable = serviceGate.onlyOfficeUsable;
    expect(usable({ onlyoffice_url: 'https://docs.example.com', external: { onlyoffice: { enabled: true, state: 'ok' } } })).toBe(true);
    expect(usable({ onlyoffice_url: 'https://docs.example.com' }), 'an older server without the probe').toBe(true);
    expect(usable({ onlyoffice_url: 'https://docs.example.com', external: { onlyoffice: { enabled: true, state: 'error' } } })).toBe(false);
    expect(usable({ onlyoffice_url: 'https://docs.example.com', external: { onlyoffice: { enabled: false, state: 'ok' } } })).toBe(false);
    expect(usable({ onlyoffice_url: null })).toBe(false);
    expect(usable(null)).toBe(false);
  });
});
