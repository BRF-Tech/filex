// filex 0.51 (GitHub #81, work #135): a .csv in ONLYOFFICE, on every surface
// that opens files - the explorer's viewer (core PreviewModal `inOffice`),
// the standalone editor tab (/files/edit, views/Editor.vue), "Choose an app…"
// (OpenWithDialog) and the explorer's file menu.
//
// ⚠ Fails on 0.50: the viewer had no `inOffice` and drew the read-only table
// (CsvViewer) whatever the host said, the editor tab never offered ONLYOFFICE
// for a .csv, and the dialog named it "filex viewer (built-in)".
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import en from '@/locales/en.json';
import Editor from '@/views/Editor.vue';
import PreviewModal from '@brftech/filex-core/src/modals/PreviewModal.vue';
import OpenWithDialog from '@brftech/filex-core/src/modals/OpenWithDialog.vue';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';
import { teardownDom } from '../helpers/teardown';
import { answerAccountPrefs } from '../helpers/accountPrefs';

answerAccountPrefs();

const CONFIG = '/api/files/onlyoffice/config';
const csv = {
  path: 'depo://liste.csv',
  basename: 'liste.csv',
  type: 'file',
  extension: 'csv',
  size: 120,
  last_modified: 1_757_376_000,
} as FileNode;

let editorConfigs: Array<Record<string, unknown>> = [];
let configBodies: Array<Record<string, unknown>> = [];

/** What api.js does: the mount becomes the editor's frame. */
class FakeEditor {
  constructor(id: string, cfg: Record<string, unknown>) {
    editorConfigs.push(cfg);
    const target = document.getElementById(id)!;
    const frame = document.createElement('iframe');
    frame.name = 'frameEditor';
    target.parentNode!.replaceChild(frame, target);
  }
  destroyEditor() {}
}

const settle = async () => {
  for (let i = 0; i < 6; i++) {
    await new Promise((r) => setTimeout(r, 0));
    await flushPromises();
  }
};

beforeEach(() => {
  editorConfigs = [];
  configBodies = [];
  (window as unknown as { DocsAPI: unknown }).DocsAPI = { DocEditor: FakeEditor };
});
// Pages down first (in-flight work lands, pages unmount, <body> empties),
// while this file's mocks still answer; only then are the mocks taken away.
afterEach(async () => {
  await teardownDom();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  delete (window as unknown as { DocsAPI?: unknown }).DocsAPI;
});

function answer(caps: Record<string, unknown> = {}) {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
    const u = String(input);
    const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { 'content-type': 'application/json' } });
    if (u.endsWith(CONFIG)) {
      configBodies.push(JSON.parse(String(init?.body ?? '{}')));
      return json({ documentServerUrl: 'https://docs.example.com', config: { document: { fileType: 'csv' } } });
    }
    if (u.includes('/api/files/plugins/actions')) return json({ actions: [], views: [] });
    if (u.includes('/api/files/capabilities')) return json(caps);
    return new Response('name,qty\nelma,3\n', { status: 200 });
  });
}

function viewer(props: Record<string, unknown>) {
  return mount(PreviewModal, {
    attachTo: document.body,
    props: {
      open: true,
      locale: 'en',
      file: csv,
      previewUrl: (p: string) => `/preview?path=${p}`,
      downloadUrl: (p: string) => `/download?path=${p}`,
      onlyOfficeBase: 'https://docs.example.com',
      onlyOfficeConfigEndpoint: CONFIG,
      ...props,
    },
  });
}

const note = () => document.querySelector('[data-testid="office-csv-note"]');
const frames = () => document.querySelectorAll('iframe[name="frameEditor"]').length;

describe('the viewer opens a .csv in ONLYOFFICE when the host says so', () => {
  it('the office editor, asked for this file in the mode the host gave', async () => {
    answer();
    viewer({ inOffice: true, openMode: 'edit' });
    await settle();
    expect(configBodies).toEqual([{ path: 'depo://liste.csv', mode: 'edit' }]);
    expect(frames(), 'ONLYOFFICE’s editor is on the page').toBe(1);
    expect(note()?.textContent).toContain('Saved as CSV: only the values of the active sheet are kept.');
  });

  it('a look first: view mode, no note, and an Edit that opens the tab in ONLYOFFICE', async () => {
    answer();
    const open = vi.fn();
    vi.stubGlobal('open', open);
    // The explorer passes newTabEnabled (an absent Boolean prop is false).
    viewer({ inOffice: true, openMode: 'view', newTabEnabled: true });
    await settle();
    expect(configBodies).toEqual([{ path: 'depo://liste.csv', mode: 'view' }]);
    expect(note(), 'nothing is saved from a look').toBeNull();
    const edit = document.querySelector('button[title="Edit"]') as HTMLButtonElement | null;
    expect(edit, 'an office document’s Edit button').not.toBeNull();
    edit!.click();
    expect(open).toHaveBeenCalledTimes(1);
    const url = String(open.mock.calls[0][0]);
    expect(url).toContain('path=depo%3A%2F%2Fliste.csv');
    expect(url).toContain('mode=edit');
    expect(url).toContain('&app=onlyoffice');
  });

  it('without the host saying so, the read-only table, as before', async () => {
    answer();
    viewer({ openMode: 'edit' });
    await settle();
    expect(configBodies, 'ONLYOFFICE was not asked').toEqual([]);
    expect(frames()).toBe(0);
    expect(note()).toBeNull();
  });

  it('says it in Turkish', async () => {
    answer();
    viewer({ inOffice: true, openMode: 'edit', locale: 'tr' });
    await settle();
    expect(note()?.textContent).toContain('CSV olarak kaydedilir: yalnızca etkin sayfanın değerleri kalır.');
  });
});

async function openTab(query: string, caps: Record<string, unknown>) {
  answer(caps);
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/files/edit', component: Editor }] });
  await router.push(`/files/edit?${query}`);
  await router.isReady();
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
  mount({ template: '<router-view />' }, { global: { plugins: [router, i18n] }, attachTo: document.body });
  await settle();
}

const OO = { onlyoffice_url: 'https://docs.example.com', external: { onlyoffice: { enabled: true, state: 'ok' } } };

describe('the editor tab (/files/edit) follows the same rule', () => {
  it('a .csv opens in ONLYOFFICE while it is there', async () => {
    await openTab('path=depo://liste.csv&type=csv&mode=edit', OO);
    expect(configBodies).toEqual([{ path: 'depo://liste.csv', mode: 'edit' }]);
    expect(note()).not.toBeNull();
  });

  it('in the table when "Open with" chose it, and when ONLYOFFICE is not there', async () => {
    await openTab('path=depo://liste.csv&type=csv&mode=edit&app=builtin', OO);
    expect(configBodies).toEqual([]);
    await teardownDom();
    await openTab('path=depo://liste.csv&type=csv&mode=edit', {
      onlyoffice_url: 'https://docs.example.com',
      external: { onlyoffice: { enabled: true, state: 'error' } },
    });
    expect(configBodies).toEqual([]);
  });
});

describe('"Choose an app…" names ONLYOFFICE', () => {
  it('as itself, not as filex’s viewer', async () => {
    mount(OpenWithDialog, {
      props: {
        open: true,
        locale: 'en',
        name: 'liste.csv',
        kind: 'csv',
        handlers: [
          { id: 'onlyoffice', view: null },
          { id: 'builtin', view: null },
        ],
        current: 'onlyoffice',
        remembered: null,
      },
      attachTo: document.body,
    });
    await flushPromises();
    expect(document.querySelector('[data-testid="open-with-choice-onlyoffice"]')?.textContent).toContain('ONLYOFFICE (spreadsheet editor)');
    expect(document.querySelector('[data-testid="open-with-choice-builtin"]')?.textContent).toContain('filex viewer (built-in)');
  });
});

describe('the explorer’s wiring', () => {
  // FileExplorer is not mounted in unit tests; its wiring is read off the
  // source, like the other explorer wiring tests (openWithDialog.test.ts).
  const src = readFileSync(resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');

  it('ONLYOFFICE is a handler while the explorer’s own probe says it is there', () => {
    expect(src).toMatch(/const openOpts = computed<OpenHandlerOptions>\(\(\) => \(\{ onlyOffice: !!effectiveOnlyOfficeBase\.value \}\)\);/);
    const of = src.slice(src.indexOf('function openHandlersOf'), src.indexOf('function personalOpenChoice'));
    expect(of).toMatch(/openHandlersFor\(pluginViewList\.value, n, pluginOpenRules\.value, openOpts\.value\)/);
    expect(src).toMatch(/:in-office="previewInOffice/);
  });

  it('missing, it is greyed with where to set it up for an administrator and not offered to anybody else (lib/serviceGate)', () => {
    const rows = src.slice(src.indexOf('function openWithRows'), src.indexOf('/* ── "Choose an app…"'));
    expect(rows).toMatch(/officeOpensKind\(openKindOf\(sel\[0\]\)\) && !openOpts\.value\.onlyOffice/);
    expect(rows).toMatch(/gateOnService\(false, callerAdmin\.value, t\('ctx\.needs_onlyoffice'\)\)/);
    expect(rows).toMatch(/\.filter\(\(r\) => !r\.hidden\)/);
  });

  it('a .csv opened in ONLYOFFICE is a look first, as an office document is', () => {
    const open = src.slice(src.indexOf('function openNode'), src.indexOf('const VIEW_DEFAULT_EXTS'));
    expect(open).toMatch(/\} else if \(isOfficeHandler\(handler\)\) \{\s*previewMode\.value = 'view';/);
  });
});
