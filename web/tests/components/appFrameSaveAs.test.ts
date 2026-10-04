// An app's save-as (`file.saveAs`, `fx.saveAs` in the SDK): a NEW file, in a
// folder the person picks in filex's own folder dialog (docs/APP-PLUGINS-API.md
// → The interface bridge).
//
// ⚠ Task #149: until 0.51 the frame took the dialog as a `pickFolder` prop
// that no host passed — the viewer, an app's dialog, its page and its details
// section all answered `unavailable` while the documentation promised the
// dialog. The frame draws the dialog itself now (the one Move to… uses,
// modals/DestinationPickerModal), so every placement in every host offers
// save-as the same way. The second half of this file mounts each of the four
// places an interface opens and saves through it: a place that stops
// offering save-as goes red here, not in a person's hands.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

import AppFrame from '@brftech/filex-core/src/components/plugin/AppFrame.vue';
import AppFrameModal from '@brftech/filex-core/src/components/plugin/AppFrameModal.vue';
import PluginPageView from '@brftech/filex-core/src/components/plugin/PluginPageView.vue';
import PluginInspectorSection from '@brftech/filex-core/src/components/plugin/PluginInspectorSection.vue';
import PreviewModal from '@brftech/filex-core/src/modals/PreviewModal.vue';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';
import { HELLO } from '../../../packages/app-ui/src/protocol';
import { teardownDom, unmountAll } from '../helpers/teardown';
import { answerAccountPrefs } from '../helpers/accountPrefs';

// A frame that opens writes "seen this version" to the account (400 ms later).
answerAccountPrefs();

afterEach(async () => {
  await teardownDom();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const settle = async () => {
  for (let i = 0; i < 6; i++) {
    await new Promise((r) => setTimeout(r, 0));
    await flushPromises();
  }
};

const UI = { url: '/_appui/sketch/0123456789abcdef/index.html', grants: ['files:read', 'files:write', 'ui'], engine: false, version: '1.0.0' };
const FILE = 'main://docs/doc.sketch';

/** A folder listing as the manager answers it: subfolders and their access. */
function listing(path: string, dirs: Array<{ name: string; perm?: string }> = [], over: Record<string, unknown> = {}) {
  const base = path.replace(/\/$/, '');
  return {
    adapter: path.split('://')[0],
    storages: ['main'],
    dirname: path,
    read_only: false,
    perm: 'owner',
    files: dirs.map((d) => ({ type: 'dir', path: `${base}/${d.name}`, basename: d.name, perm: d.perm })),
    ...over,
  };
}

const TREE: Record<string, ReturnType<typeof listing>> = {
  '': listing('main://', [{ name: 'docs' }], { storages: ['main', 'box'] }),
  'main://': listing('main://', [{ name: 'docs' }, { name: 'work' }]),
  'main://docs': listing('main://docs', [{ name: 'out' }, { name: 'ro', perm: 'viewer' }]),
  'main://docs/out': listing('main://docs/out'),
  'main://docs/ro': listing('main://docs/ro', [], { perm: 'viewer' }),
  'main://work': listing('main://work'),
};

function apiStub(over: Record<string, unknown> = {}) {
  return {
    // about: — happy-dom would otherwise try to load the frame's address.
    appUIUrl: (u: string) => `about:blank#${u}`,
    index: vi.fn(async (p: string) => {
      const l = TREE[p];
      if (!l) throw Object.assign(new Error('not found'), { status: 404 });
      return l;
    }),
    pluginUISave: vi.fn(async (_p: string, _v: string, target: { dir: string; name: string }, body: Blob) => ({
      saved: true,
      path: `${target.dir.replace(/\/$/, '')}/${target.name}`,
      name: target.name,
      size: body.size,
    })),
    pluginActions: vi.fn(async () => ({ actions: [], views: [] })),
    pluginUICall: vi.fn(async () => ({ result: null })),
    fetchResponse: vi.fn(async () => new Response('file body', { headers: { 'content-length': '9' } })),
    ...over,
  };
}

/** A window-like the frame "is": what postMessage it received. */
function fakeWindow() {
  const sent: Array<{ data: unknown; ports: MessagePort[] }> = [];
  return {
    sent,
    postMessage(data: unknown, _origin: string, transfer?: Transferable[]) {
      sent.push({ data, ports: (transfer ?? []) as MessagePort[] });
    },
  };
}

/** Be the interface in the frame filex drew: its port. */
function connect(): MessagePort {
  const f = document.querySelector('iframe[data-testid="app-frame"]') as HTMLIFrameElement;
  expect(f, 'the interface frame is drawn').toBeTruthy();
  const win = fakeWindow();
  Object.defineProperty(f, 'contentWindow', { get: () => win });
  window.dispatchEvent(new MessageEvent('message', { data: { type: HELLO, v: 1 }, source: win as unknown as Window, origin: 'null' }));
  expect(win.sent, 'filex answered the hello').toHaveLength(1);
  return win.sent[0].ports[0];
}

let seq = 0;
/** Ask over the port; the answer, whenever it comes. */
function ask(port: MessagePort, method: string, params?: unknown): Promise<any> {
  const id = ++seq;
  return new Promise((resolve) => {
    port.addEventListener('message', (ev) => {
      if (ev.data?.id === id) resolve(ev.data);
    });
    port.start();
    port.postMessage({ id, method, params });
  });
}

const picker = () => document.querySelector('[data-testid="destpicker"]');
const card = () => picker()?.closest('.fe-modal__card') ?? null;
const click = async (sel: string) => {
  const el = document.querySelector(sel) as HTMLButtonElement | null;
  expect(el, sel).toBeTruthy();
  el!.click();
  await settle();
};
const cancelButton = () =>
  [...(card()?.querySelectorAll('button') ?? [])].find((b) => b.textContent?.trim() === 'Cancel') as HTMLButtonElement | undefined;

function mountFrame(props: Record<string, unknown> = {}, api = apiStub()) {
  const w = mount(AppFrame, {
    props: {
      api, app: 'sketch', view: 'editor', placement: 'viewer', ui: UI, locale: 'en', title: 'Sketch',
      files: [{ path: FILE, name: 'doc.sketch', size: 9 }],
      ...props,
    },
    attachTo: document.body,
  });
  return { w, api };
}

describe('save-as in the frame (AppFrame.vue)', () => {
  it("opens filex's own folder dialog in the file's folder, and saves the new file where the person chose", async () => {
    const { w, api } = mountFrame({ storages: ['main'] });
    await settle();
    const port = connect();

    const answer = ask(port, 'file.saveAs', { name: 'copy.sketch', data: 'v2' });
    await settle();
    expect(picker(), 'the folder dialog is on screen').toBeTruthy();
    // Who asks, and for which file (UI-14): the app's name is on the dialog.
    expect(card()?.textContent).toContain('Sketch: save “copy.sketch” to');
    expect(api.index).toHaveBeenCalledWith('main://docs');
    expect(document.querySelector('[data-testid="destpicker-confirm"]')?.textContent?.trim()).toBe('Save here');

    await click('[data-testid="destpicker-row-out"]');
    expect(api.index).toHaveBeenCalledWith('main://docs/out');
    await click('[data-testid="destpicker-confirm"]');

    expect((await answer).result).toEqual({ saved: true, name: 'copy.sketch', size: 2 });
    expect(api.pluginUISave).toHaveBeenCalledWith('sketch', 'editor', { dir: 'main://docs/out', name: 'copy.sketch' }, expect.any(Blob));
    expect(picker(), 'the dialog is gone').toBeNull();
    expect(w.emitted('saved-as')).toEqual([[{ path: 'main://docs/out/copy.sketch', name: 'copy.sketch', size: 2 }]]);
  });

  it('offers no folder the person may not write into', async () => {
    const { api } = mountFrame({ storages: ['main'] });
    await settle();
    const port = connect();
    const answer = ask(port, 'file.saveAs', { name: 'copy.sketch', data: 'v2' });
    await settle();
    await click('[data-testid="destpicker-row-ro"]');
    const confirm = document.querySelector('[data-testid="destpicker-confirm"]') as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);
    expect(document.querySelector('[data-testid="destpicker-reason"]')?.textContent).toContain('You cannot write into this folder.');
    cancelButton()!.click();
    await settle();
    expect((await answer).error.code).toBe('cancelled');
    expect(api.pluginUISave).not.toHaveBeenCalled();
  });

  it('answers cancelled when the person closes the dialog, and saves nothing', async () => {
    const { api } = mountFrame({ storages: ['main'] });
    await settle();
    const port = connect();
    const answer = ask(port, 'file.saveAs', { name: 'copy.sketch', data: 'v2' });
    await settle();
    expect(cancelButton(), 'the dialog has its Cancel').toBeTruthy();
    cancelButton()!.click();
    await settle();
    expect((await answer).error.code).toBe('cancelled');
    expect(picker()).toBeNull();
    expect(api.pluginUISave).not.toHaveBeenCalled();
  });

  it('holds one dialog at a time', async () => {
    mountFrame({ storages: ['main'] });
    await settle();
    const port = connect();
    const first = ask(port, 'file.saveAs', { name: 'a.sketch', data: 'a' });
    const second = await ask(port, 'file.saveAs', { name: 'b.sketch', data: 'b' });
    expect(second.error).toEqual({ code: 'unavailable', message: 'a question is already on screen' });
    await settle();
    expect(document.querySelectorAll('[data-testid="destpicker"]')).toHaveLength(1);
    cancelButton()!.click();
    expect((await first).error.code).toBe('cancelled');
  });

  it('asks nothing for a call it refuses anyway', async () => {
    const { api } = mountFrame({ storages: ['main'], ui: { ...UI, grants: ['files:read', 'ui'] } });
    await settle();
    const port = connect();
    expect((await ask(port, 'file.saveAs', { name: 'a.sketch', data: 'a' })).error.code).toBe('not_granted');
    unmountAll();

    mountFrame({ storages: ['main'] }, api);
    await settle();
    const port2 = connect();
    expect((await ask(port2, 'file.saveAs', { name: 'sub/a.sketch', data: 'a' })).error.code).toBe('invalid');
    expect((await ask(port2, 'file.saveAs', { name: 'a.sketch', data: 42 })).error.code).toBe('invalid');
    await settle();
    expect(picker()).toBeNull();
    expect(api.pluginUISave).not.toHaveBeenCalled();
  });

  it('lets a view-only opening save a new file elsewhere, while it may not save over its own', async () => {
    const { api } = mountFrame({ storages: ['main'], readOnly: true });
    await settle();
    const port = connect();
    expect((await ask(port, 'file.save', { data: 'x' })).error.code).toBe('read_only');
    const answer = ask(port, 'file.saveAs', { name: 'copy.sketch', data: 'v2' });
    await settle();
    expect(picker(), 'the folder dialog is on screen').toBeTruthy();
    await click('[data-testid="destpicker-row-out"]');
    await click('[data-testid="destpicker-confirm"]');
    expect((await answer).result).toEqual({ saved: true, name: 'copy.sketch', size: 2 });
    expect(api.pluginUISave).toHaveBeenCalledTimes(1);
    expect(api.pluginUISave).toHaveBeenCalledWith('sketch', 'editor', { dir: 'main://docs/out', name: 'copy.sketch' }, expect.any(Blob));
  });

  it('with no file, opens where the host says the person is', async () => {
    const { api } = mountFrame({ files: [], placement: 'home', storages: ['main'], startAt: 'main://work' });
    await settle();
    const port = connect();
    const answer = ask(port, 'file.saveAs', { name: 'new.sketch', data: 'x' });
    await settle();
    expect(api.index).toHaveBeenCalledWith('main://work');
    await click('[data-testid="destpicker-confirm"]');
    expect((await answer).result).toMatchObject({ saved: true, name: 'new.sketch' });
    expect(api.pluginUISave).toHaveBeenCalledWith('sketch', 'editor', { dir: 'main://work', name: 'new.sketch' }, expect.any(Blob));
  });

  it("opens a draft's save-as where the draft will go, never in filex's own folder", async () => {
    const draft = [{ path: 'main://.filex-drafts/1/abcdef12/doc.sketch', name: 'doc.sketch', size: 9 }];
    const { api } = mountFrame({ files: draft, storages: ['main'], startAt: 'main://work' });
    await settle();
    let port = connect();
    let answer = ask(port, 'file.saveAs', { name: 'copy.sketch', data: 'x' });
    await settle();
    expect(api.index).toHaveBeenCalledWith('main://work');
    expect(api.index.mock.calls.map((c) => String(c[0])).filter((p) => p.includes('.filex-'))).toEqual([]);
    cancelButton()!.click();
    expect((await answer).error.code).toBe('cancelled');
    unmountAll();

    // Nobody said where: that storage's root, not the drafts area.
    const second = mountFrame({ files: draft, storages: ['main'] });
    await settle();
    port = connect();
    answer = ask(port, 'file.saveAs', { name: 'copy.sketch', data: 'x' });
    await settle();
    expect(second.api.index).toHaveBeenCalledWith('main://');
    expect(second.api.index.mock.calls.map((c) => String(c[0])).filter((p) => p.includes('.filex-'))).toEqual([]);
    cancelButton()!.click();
    expect((await answer).error.code).toBe('cancelled');
  });

  it('with neither a file nor a list of storages, learns them from the listing and offers each', async () => {
    const { api } = mountFrame({ files: [], placement: 'home' });
    await settle();
    const port = connect();
    const answer = ask(port, 'file.saveAs', { name: 'new.sketch', data: 'x' });
    await settle();
    expect(api.index).toHaveBeenCalledWith('');
    expect(document.querySelector('[data-testid="destpicker-row-main"]')).toBeTruthy();
    expect(document.querySelector('[data-testid="destpicker-row-box"]')).toBeTruthy();
    await click('[data-testid="destpicker-row-main"]');
    await click('[data-testid="destpicker-confirm"]');
    expect((await answer).result).toMatchObject({ saved: true });
    expect(api.pluginUISave).toHaveBeenCalledWith('sketch', 'editor', { dir: 'main://', name: 'new.sketch' }, expect.any(Blob));
  });

  it('closes its dialog with the frame, answering cancelled', async () => {
    const { w, api } = mountFrame({ storages: ['main'] });
    await settle();
    const port = connect();
    const answer = ask(port, 'file.saveAs', { name: 'copy.sketch', data: 'v2' });
    await settle();
    expect(picker()).toBeTruthy();
    w.unmount();
    await settle();
    expect(picker()).toBeNull();
    expect(api.pluginUISave).not.toHaveBeenCalled();
    // The port went with the frame; nothing was saved either way.
    void answer;
  });
});

/* ── every place an interface opens offers it the same way ─────────────── */

/** Save as through whatever frame is on the page: the dialog, a folder, Save here. */
async function savesAsThroughTheDialog(api: ReturnType<typeof apiStub>) {
  const port = connect();
  const answer = ask(port, 'file.saveAs', { name: 'copy.sketch', data: 'v2' });
  await settle();
  expect(picker(), 'the folder dialog is on screen').toBeTruthy();
  expect(card()?.textContent).toContain('copy.sketch');
  expect(api.index).toHaveBeenCalledWith('main://docs');
  await click('[data-testid="destpicker-row-out"]');
  await click('[data-testid="destpicker-confirm"]');
  const r = await answer;
  expect(r.error, 'save-as is offered here').toBeUndefined();
  expect(r.result).toEqual({ saved: true, name: 'copy.sketch', size: 2 });
  expect(api.pluginUISave).toHaveBeenCalledWith('sketch', 'editor', { dir: 'main://docs/out', name: 'copy.sketch' }, expect.any(Blob));
}

function fileNode(): FileNode {
  return { path: FILE, basename: 'doc.sketch', type: 'file', extension: 'sketch', size: 9, last_modified: 1_757_376_000 } as FileNode;
}

describe('every place an interface opens offers save-as', () => {
  it('the viewer (PreviewModal)', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 200 })));
    const api = apiStub();
    mount(PreviewModal, {
      props: {
        open: true,
        locale: 'en',
        file: fileNode(),
        previewUrl: (p: string) => `/preview?path=${p}`,
        downloadUrl: (p: string) => `/download?path=${p}`,
        appViewer: { plugin: 'sketch', id: 'editor', placement: 'viewer', label: { en: 'Sketch' }, ui: UI },
        api: api as never,
      },
      attachTo: document.body,
    });
    await settle();
    await savesAsThroughTheDialog(api);
  });

  it("an app's dialog (AppFrameModal)", async () => {
    const api = apiStub();
    mount(AppFrameModal, {
      props: {
        api: api as never,
        locale: 'en',
        plugin: 'sketch',
        view: 'editor',
        placement: 'modal',
        ui: UI,
        label: 'Sketch',
        files: [{ path: FILE, name: 'doc.sketch', size: 9 }],
        storages: ['main'],
        startAt: 'main://docs',
      },
      attachTo: document.body,
    });
    await settle();
    await savesAsThroughTheDialog(api);
  });

  it("an app's page (PluginPageView)", async () => {
    const api = apiStub({
      pluginActions: vi.fn(async () => ({
        actions: [],
        views: [{ plugin: 'sketch', id: 'editor', placement: 'page', label: { en: 'Sketch' }, ui: UI }],
      })),
    });
    mount(PluginPageView, {
      props: { locale: 'en', api: api as never, plugin: 'sketch', view: 'editor', path: FILE, storages: ['main'] },
      attachTo: document.body,
    });
    await settle();
    await savesAsThroughTheDialog(api);
  });

  it('the details panel (PluginInspectorSection)', async () => {
    const api = apiStub();
    mount(PluginInspectorSection, {
      props: {
        api: api as never,
        view: { plugin: 'sketch', id: 'editor', placement: 'inspector', label: { en: 'Sketch' }, ui: UI },
        path: FILE,
        locale: 'en',
        storages: ['main'],
        node: fileNode(),
        writable: true,
      },
      attachTo: document.body,
    });
    await click('[data-testid="inspector-plugin-toggle-sketch-editor"]');
    await savesAsThroughTheDialog(api);
  });
});
