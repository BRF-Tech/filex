// The office screen is never split in two (issue #80 follow-up).
//
// Measured against ONLYOFFICE Docs 9.4 in the 0.50 final run: after "Download
// failed" the page showed the dead editor - its own "Download failed / OK"
// dialog still up - on one half, and filex's fallback (the sentence, the
// diagnosis, Download) on the other. api.js REPLACES the element it is given
// with its iframe, so the fallback taking that element's place left the frame
// standing beside it.
//
// The rule (lib/officeDiagnosis officeEventEndsTheEditor): ONLYOFFICE reports
// what its editor cannot go on from through `onError` and everything else
// through `onWarning`. The first closes the editor and the fallback has the
// screen to itself; the second is the document server's to show, over an
// editor that goes on.
//
// The fake editor below does to the page what api.js does: the mount is
// replaced by an iframe named frameEditor, and destroyEditor() swaps the
// iframe for a fresh placeholder of its own (api.js _destroyEditor).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';

import PreviewModal from '@brftech/filex-core/src/modals/PreviewModal.vue';
import { officeErrorText, officeEventEndsTheEditor } from '@brftech/filex-core/src/lib/officeDiagnosis';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';

const docx = {
  path: 'depo://rapor.docx',
  basename: 'rapor.docx',
  type: 'file',
  extension: 'docx',
  size: 38_000,
  last_modified: 1_757_376_000,
} as FileNode;

const CONFIG = '/api/files/onlyoffice/config';

type Events = { onError?: (e: unknown) => void; onWarning?: (e: unknown) => void };
let events: Events = {};
let destroyed = 0;

/** What api.js does to the page. */
class ApiJsLikeEditor {
  private frame: HTMLIFrameElement | null;
  constructor(
    private placeholderId: string,
    cfg: { events?: Events },
  ) {
    events = cfg.events ?? {};
    const target = document.getElementById(placeholderId)!;
    this.frame = document.createElement('iframe');
    this.frame.name = 'frameEditor';
    target.parentNode!.replaceChild(this.frame, target);
  }
  destroyEditor() {
    destroyed++;
    const target = document.createElement('div');
    target.id = this.placeholderId;
    this.frame?.parentNode?.replaceChild(target, this.frame);
    this.frame = null;
  }
}

const settle = async () => {
  for (let i = 0; i < 3; i++) {
    await new Promise((r) => setTimeout(r, 0));
    await flushPromises();
  }
};

const opened: VueWrapper[] = [];
afterEach(() => {
  opened.splice(0).forEach((w) => w.unmount());
  delete (window as unknown as { DocsAPI?: unknown }).DocsAPI;
});

beforeEach(() => {
  events = {};
  destroyed = 0;
  (window as unknown as { DocsAPI: unknown }).DocsAPI = { DocEditor: ApiJsLikeEditor };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url === CONFIG) {
        return {
          ok: true,
          status: 200,
          json: async () => ({ documentServerUrl: 'https://docs.example.com', config: { document: {} } }),
          text: async () => '',
        };
      }
      if (url.startsWith('/api/files/onlyoffice/diagnose')) {
        return { ok: true, status: 200, json: async () => ({ verdict: 'served', scope: 'this_process' }), text: async () => '' };
      }
      return { ok: true, status: 200, json: async () => ({}), text: async () => '' };
    }),
  );
});

async function openEditor() {
  const w = mount(PreviewModal, {
    attachTo: document.body,
    props: {
      open: true,
      locale: 'en',
      file: docx,
      previewUrl: (p: string) => `/preview?path=${p}`,
      downloadUrl: (p: string) => `/download?path=${p}`,
      onlyOfficeBase: 'https://docs.example.com',
      onlyOfficeConfigEndpoint: CONFIG,
    },
  });
  opened.push(w);
  await settle();
  expect(document.querySelector('iframe[name="frameEditor"]'), 'the editor is on the page').not.toBeNull();
  return w;
}

const frames = () => document.querySelectorAll('iframe[name^="frameEditor"]').length;
const fallback = () => document.querySelector('[data-testid="office-fallback"]');

describe('the office screen is never split', () => {
  it('"Download failed": the editor goes, the fallback has the screen to itself', async () => {
    await openEditor();
    events.onError?.({ data: { errorCode: -4, errorDescription: 'Download failed.' } });
    await settle();
    expect(fallback(), 'the fallback is drawn').not.toBeNull();
    expect(frames(), 'no editor frame beside it').toBe(0);
    expect(destroyed, 'the editor was closed, not just covered').toBe(1);
    expect(document.getElementById('fe-onlyoffice-mount'), "nor api.js's leftover placeholder").toBeNull();
    expect(document.querySelector('[data-testid="office-diagnosis"]')?.textContent).toMatch(/downloaded this file from filex/);
  });

  it('a token the document server refused (-20) ends the editor the same way, its words as text', async () => {
    await openEditor();
    // Word for word what ONLYOFFICE Docs 9.4 sends (e2e/realenv, #80 S2 and
    // S5): the fallback showed the `<br>` as two words of its own.
    events.onError?.({
      data: { errorCode: -20, errorDescription: 'The document security token is not correctly formed.<br>Please contact your Document Server administrator.' },
    });
    await settle();
    expect(fallback()).not.toBeNull();
    expect(frames()).toBe(0);
    const text = fallback()!.querySelector('p')!.textContent!;
    expect(text).toBe('The document security token is not correctly formed. Please contact your Document Server administrator.');
    expect(fallback()!.innerHTML).not.toContain('&lt;br');
  });

  it('the description is read as text: tags go, entities are read', () => {
    expect(officeErrorText('A<br/>B<BR >C')).toBe('A B C');
    expect(officeErrorText('<b>Bold</b> &amp; &lt;plain&gt; &quot;q&quot; it&#39;s')).toBe('Bold & <plain> "q" it\'s');
    expect(officeErrorText('Download failed.')).toBe('Download failed.');
  });

  it('a warning is the document server\'s to show: no fallback, the editor stays', async () => {
    await openEditor();
    events.onWarning?.({ data: { errorCode: -6, errorDescription: 'Database connection error.' } });
    await settle();
    expect(fallback(), 'no fallback over a working editor').toBeNull();
    expect(frames(), 'the editor is still there').toBe(1);
    expect(destroyed).toBe(0);
  });

  it('the rule is the channel the document server chose', () => {
    expect(officeEventEndsTheEditor('onError')).toBe(true);
    expect(officeEventEndsTheEditor('onWarning')).toBe(false);
  });
});
