// The standalone editor tab (/files/edit, views/Editor.vue) opens a file in
// the app's own interface exactly as the explorer's preview does (the maintainer,
// 2026-09-27 — one surface): the same `viewer` views, the same "first app
// for the type" rule, the same "Open with" choice (`app=` in the address).
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';

import en from '@/locales/en.json';
import Editor from '@/views/Editor.vue';
import { teardownDom, unmountAll } from '../helpers/teardown';
import { answerAccountPrefs } from '../helpers/accountPrefs';

// An app frame that opens writes "seen this version" to the account 400 ms
// later (the editor tab's and the preview's alike).
answerAccountPrefs();


const VIEWS = [
  {
    plugin: 'sketch',
    id: 'editor',
    placement: 'viewer',
    label: { en: 'Sketch' },
    applies: { ext: ['sketch'] },
    ui: { url: '/_appui/sketch/0123456789abcdef/index.html', grants: ['files:read', 'files:write', 'ui'], engine: false, version: '1.0.0' },
  },
  {
    plugin: 'board',
    id: 'main',
    placement: 'viewer',
    label: { en: 'Board' },
    applies: { ext: ['sketch'] },
    ui: { url: '/_appui/board/fedcba9876543210/index.html', grants: ['files:read', 'ui'], engine: false, version: '2.0.0' },
  },
];

// Pages down first (in-flight work lands, pages unmount, <body> empties),
// while this file's mocks still answer; only then are the mocks taken away.
afterEach(async () => {
  await teardownDom();
  vi.restoreAllMocks();
});

const settle = async () => {
  for (let i = 0; i < 6; i++) {
    await new Promise((r) => setTimeout(r, 0));
    await flushPromises();
  }
};

async function openEditor(query: string) {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input: RequestInfo | URL) => {
    const u = String(input);
    const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { 'content-type': 'application/json' } });
    if (u.includes('/api/files/plugins/actions')) return json({ actions: [], views: VIEWS });
    if (u.includes('/api/files/capabilities')) return json({});
    return new Response('x', { status: 200 });
  });
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/files/edit', component: Editor }] });
  await router.push(`/files/edit?${query}`);
  await router.isReady();
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
  const w = mount({ template: '<router-view />' }, { global: { plugins: [router, i18n] }, attachTo: document.body });
  await settle();
  return w;
}

const frameSrc = () => document.querySelector('iframe[data-testid="app-frame"]')?.getAttribute('src') ?? null;

describe('the editor tab opens an app’s own interface', () => {
  it('in the first app for the type, like the explorer', async () => {
    await openEditor('path=main://doc.sketch&mode=edit');
    expect(frameSrc()).toContain('/_appui/sketch/0123456789abcdef/index.html');
  });

  it('in the app "Open with" chose', async () => {
    await openEditor('path=main://doc.sketch&mode=edit&app=board/main');
    expect(frameSrc()).toContain('/_appui/board/fedcba9876543210/index.html');
  });

  it('in filex’s own viewer when that was the choice, or when no app opens the type', async () => {
    await openEditor('path=main://doc.sketch&mode=edit&app=builtin');
    expect(frameSrc()).toBeNull();
    unmountAll();
    await openEditor('path=main://notes.txt&mode=edit');
    expect(frameSrc()).toBeNull();
  });
});

// ── the one rule (lib/appViewer) and the link that carries the choice ──────

describe('pickAppViewer — the rule every surface uses', () => {
  it('is the first app for the type, the one chosen, or none', async () => {
    const { pickAppViewer, appViewersFor } = await import('@brftech/filex-core/src/lib/appViewer');
    const views = VIEWS as never[];
    const file = { type: 'file', extension: 'sketch', basename: 'a.sketch' };
    expect(appViewersFor(views, file).map((v: { plugin: string }) => v.plugin)).toEqual(['sketch', 'board']);
    expect(pickAppViewer(views, file)?.plugin).toBe('sketch');
    expect(pickAppViewer(views, file, 'board/main')?.plugin).toBe('board');
    expect(pickAppViewer(views, file, 'builtin')).toBeNull();
    // 0.50: a choice that is not (or no longer) one of the handlers that are
    // on falls to the next default, as a person's own choice does
    // (docs/APP-PLUGINS.md → Default apps) - not to filex's viewer.
    expect(pickAppViewer(views, file, 'gone/view')?.plugin).toBe('sketch');
    expect(pickAppViewer(views, { type: 'dir', extension: '', basename: 'x' })).toBeNull();
    expect(pickAppViewer(views, { type: 'file', extension: 'txt', basename: 'a.txt' })).toBeNull();
    expect(pickAppViewer([{ ...VIEWS[0], placement: 'modal' }] as never[], file), 'only viewers').toBeNull();
  });
});

describe('"Open in new tab" from an app’s interface', () => {
  it('names the app, so the tab opens the file in the same one', async () => {
    const PreviewModal = (await import('@brftech/filex-core/src/modals/PreviewModal.vue')).default;
    const opened = vi.spyOn(window, 'open').mockImplementation(() => null);
    const w = mount(PreviewModal, {
      attachTo: document.body,
      props: {
        open: true,
        locale: 'en',
        file: { path: 'main://doc.sketch', basename: 'doc.sketch', type: 'file', extension: 'sketch', size: 3 } as never,
        openMode: 'edit',
        newTabEnabled: true,
        previewUrl: (p: string) => `/preview?path=${p}`,
        downloadUrl: (p: string) => `/download?path=${p}`,
        appViewer: VIEWS[1] as never,
        api: { appUIUrl: (u: string) => `about:blank#${u}` } as never,
      },
    });
    await settle();
    const btn = document.querySelector<HTMLButtonElement>('button[aria-label="Open in new tab"]');
    expect(btn, 'the viewer bar has the button').not.toBeNull();
    btn!.click();
    expect(String(opened.mock.calls[0]?.[0])).toContain('app=board%2Fmain');
    w.unmount();
  });
});
