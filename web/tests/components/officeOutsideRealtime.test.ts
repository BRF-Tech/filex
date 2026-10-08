// The web half of #184: an office document open in the viewer changed on the
// SERVER while it was open (another person's save over WebDAV, a sync client,
// an agent on a mounted folder). Nobody but the server knows; it tells the
// viewer through the realtime feed of the document's folder.
//
// A change frame naming the document is only "something happened to it": the
// viewer asks the server whether its editing session is still on the current
// version (POST /api/files/onlyoffice/session, `state`) - its own save looks
// the same on the feed - and only a stale session is a change. Then the same
// rule as the desktop's (officeOutsideChange.test.ts): reload when nothing is
// unsaved, ask otherwise; and the answer goes to the SERVER, which keeps or
// drops the session's save ('both' is its default: kept beside the file).
//
// Task #92: the cases about the editor run three times - api.js in the page,
// the editor in its frame on the interface origin, and in its frame on the
// document server's own origin (helpers/officeEditors).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';

import PreviewModal from '@brftech/filex-core/src/modals/PreviewModal.vue';
import { changeTouches, documentFolderOf } from '@brftech/filex-core/src/composables/useDocumentWatch';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';
import { OFFICE_MODES, installOfficeEditors, type OfficeEditors } from '../helpers/officeEditors';

const PATH = 'depo://Raporlar/Bütçe.xlsx';
const CONFIG = '/api/files/onlyoffice/config';
const SESSION = '/api/files/onlyoffice/session';

/** Enough of a WebSocket for lib/realtime RealtimeClient. */
class FakeSocket {
  static OPEN = 1;
  static all: FakeSocket[] = [];
  readyState = 1;
  sent: Array<Record<string, unknown>> = [];
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(public url: string) {
    FakeSocket.all.push(this);
  }
  send(data: string) {
    this.sent.push(JSON.parse(data));
  }
  close() {
    this.readyState = 3;
  }
  frame(m: Record<string, unknown>) {
    this.onmessage?.({ data: JSON.stringify(m) });
  }
}

const settle = async (rounds = 4) => {
  for (let i = 0; i < rounds; i++) {
    await new Promise((r) => setTimeout(r, 0));
    await flushPromises();
  }
};
/** Past the watch's debounce (400 ms) and the server's answer. */
const afterDebounce = async () => {
  await new Promise((r) => setTimeout(r, 480));
  await settle();
};

describe.each(OFFICE_MODES)('the document changed on the server while it was open in the viewer (#184), editor %s', (mode) => {
  let editors: OfficeEditors;
  let configs = 0;
  let staleAnswer = false;
  let sessionCalls: Array<{ path: string; key: string; action: string }> = [];
  /** The `token` each session call carried (0.54: an answer shows the editor
   *  configuration's signed token; a question carries none). */
  let sessionTokens: Array<string | undefined> = [];
  const opened: VueWrapper[] = [];

  afterEach(() => {
    opened.splice(0).forEach((w) => w.unmount());
    editors.uninstall();
    vi.unstubAllGlobals();
  });

  beforeEach(() => {
    configs = 0;
    staleAnswer = false;
    sessionCalls = [];
    sessionTokens = [];
    FakeSocket.all = [];
    editors = installOfficeEditors(mode);
    vi.stubGlobal('WebSocket', FakeSocket);
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: { body?: string }) => {
        const body = JSON.parse(String(init?.body ?? '{}'));
        if (url === CONFIG) {
          configs++;
          return {
            ok: true,
            status: 200,
            json: async () => ({
              documentServerUrl: 'https://docs.example.com',
              config: { document: { key: `v${configs}` }, token: `signed-v${configs}` },
              ...editors.answer(),
            }),
            text: async () => '',
          };
        }
        if (url === SESSION) {
          sessionCalls.push({ path: body.path, key: body.key, action: body.action });
          sessionTokens.push(body.token);
          return { ok: true, status: 200, json: async () => ({ stale: staleAnswer, known: true }), text: async () => '' };
        }
        return { ok: true, status: 200, json: async () => ({}), text: async () => '' };
      }),
    );
  });

  async function openEditor(path = PATH) {
    const basename = path.slice(path.lastIndexOf('/') + 1);
    const w = mount(PreviewModal, {
      attachTo: document.body,
      props: {
        open: true,
        locale: 'en',
        file: { path, basename, type: 'file', extension: 'xlsx', size: 1, last_modified: 1 } as FileNode,
        previewUrl: (p: string) => `/preview?path=${p}`,
        downloadUrl: (p: string) => `/download?path=${p}`,
        onlyOfficeBase: 'https://docs.example.com',
        onlyOfficeConfigEndpoint: CONFIG,
        chromeless: true,
        api: { wsTicket: async () => ({ ticket: 't', ws_url: 'ws://filex.test/api/ws' }) } as never,
      },
    });
    opened.push(w);
    await settle(6);
    expect(editors.created, 'the editor is on the page').toEqual(['v1']);
    return w;
  }

  function socket(): FakeSocket {
    const s = FakeSocket.all.at(-1);
    expect(s, 'the viewer joined the realtime feed').toBeTruthy();
    s!.onopen?.();
    return s!;
  }

  const dialog = () => document.querySelector('[data-testid="outside-change-dialog"]');
  const note = () => document.querySelector('[data-testid="outside-note"]')?.textContent?.trim() ?? '';
  const click = async (id: string) => {
    (document.querySelector(`[data-testid="${id}"]`) as HTMLButtonElement | null)?.click();
    await settle();
  };
  const changed = (s: FakeSocket, name = 'Bütçe.xlsx') =>
    s.frame({ type: 'change', path: 'depo://Raporlar', action: 'modify', name });

  it("joins the document's folder and says which file it is on", async () => {
    await openEditor();
    const s = socket();
    expect(s.sent).toContainEqual({ type: 'subscribe', path: 'depo://Raporlar' });
    expect(s.sent).toContainEqual({ type: 'focus', file: 'Bütçe.xlsx' });
  });

  it('a change the server calls current (the editor\'s own save) changes nothing; a stale one reloads in place', async () => {
    const w = await openEditor();
    const s = socket();
    changed(s);
    await afterDebounce();
    expect(sessionCalls).toEqual([{ path: PATH, key: 'v1', action: 'state' }]);
    expect(editors.created, 'the server said the session is current').toEqual(['v1']);

    staleAnswer = true;
    changed(s);
    await afterDebounce();
    expect(editors.destroyed()).toBe(1);
    expect(editors.created, 'a new editor on the new version').toEqual(['v1', 'v2']);
    expect(note()).toMatch(/updated outside filex/i);
    expect(w.emitted('outside-resolved')?.[0]?.[0]).toMatchObject({ choice: 'reloaded', origin: 'server', key: 'v1' });
  });

  it('a frame about another file in the folder is not asked about', async () => {
    await openEditor();
    const s = socket();
    staleAnswer = true;
    changed(s, 'baska.xlsx');
    await afterDebounce();
    expect(sessionCalls).toEqual([]);
    expect(editors.created).toEqual(['v1']);
  });

  it('an edit here: the question; "Write mine" tells the server, with this session\'s key', async () => {
    await openEditor();
    const s = socket();
    await editors.state(true);
    staleAnswer = true;
    changed(s);
    await afterDebounce();
    expect(dialog()).not.toBeNull();
    expect(editors.destroyed()).toBe(0);

    await click('outside-write-mine');
    expect(sessionCalls.at(-1)).toEqual({ path: PATH, key: 'v1', action: 'mine' });
    expect(sessionTokens.at(-1), "the answer shows the session's signed editor configuration").toBe('signed-v1');
    expect(sessionTokens[0], 'a question carries no token').toBeUndefined();
    expect(editors.destroyed(), 'the edited editor stays').toBe(0);
  });

  it('"Keep the outside version": the server drops the OLD session\'s save, then the new version loads', async () => {
    await openEditor();
    const s = socket();
    await editors.state(true);
    staleAnswer = true;
    changed(s);
    await afterDebounce();
    await click('outside-keep-theirs');
    await settle();
    expect(sessionCalls).toContainEqual({ path: PATH, key: 'v1', action: 'theirs' });
    expect(sessionTokens[sessionCalls.findIndex((c) => c.action === 'theirs')]).toBe('signed-v1');
    expect(editors.created).toEqual(['v1', 'v2']);
  });

  it('"Keep both" is the server\'s own default: nothing more is asked of it', async () => {
    await openEditor();
    const s = socket();
    await editors.state(true);
    staleAnswer = true;
    changed(s);
    await afterDebounce();
    await click('outside-keep-both');
    expect(sessionCalls.map((c) => c.action)).toEqual(['state']);
    expect(note()).toMatch(/beside it/i);
  });

  it("the desktop app's working copies are not watched (the app watches the person's file)", async () => {
    await openEditor('depo://.filex-open/aaaaaaaaaaaa-Bütçe.xlsx');
    await settle();
    expect(FakeSocket.all).toHaveLength(0);
  });
});

describe('which frames are about the document', () => {
  it('documentFolderOf', () => {
    expect(documentFolderOf('depo://a/b/c.docx')).toEqual({ dir: 'depo://a/b', name: 'c.docx' });
    expect(documentFolderOf('depo://c.docx')).toEqual({ dir: 'depo://', name: 'c.docx' });
    expect(documentFolderOf('no-storage')).toBeNull();
  });
  it('changeTouches', () => {
    expect(changeTouches({ action: 'modify', name: 'a.docx' }, 'a.docx')).toBe(true);
    expect(changeTouches({ action: 'rename', name: 'old.docx', new_name: 'a.docx' }, 'a.docx')).toBe(true);
    expect(changeTouches({ action: 'modify', name: 'b.docx' }, 'a.docx')).toBe(false);
    expect(changeTouches({ action: 'modify' }, 'a.docx'), 'a merged burst may be about it').toBe(true);
  });
});
