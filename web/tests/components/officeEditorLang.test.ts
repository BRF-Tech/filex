// The ONLYOFFICE editor speaks the language on screen (GitHub Discussion #93).
//
// The server chooses the editor's language (backend onlyoffice/lang.go): the
// administrator's fixed one, else the request's, else the SCREEN's - which it
// can only know if the viewer says it. The viewer (core PreviewModal, the one
// path every host takes: the explorer, the editor tab, the desktop's document
// windows, the embeds) names the language it draws in as Accept-Language on
// the config request, the way every other call does (useFileApi rawRequest):
// the browser's own header is the language the browser was installed in.
//
// Red before #214: the config request carried no Accept-Language of its own,
// and ONLYOFFICE opened in English under a Turkish screen.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

import PreviewModal from '@brftech/filex-core/src/modals/PreviewModal.vue';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';
import { teardownDom } from '../helpers/teardown';

const CONFIG = '/api/files/onlyoffice/config';
const docx = {
  path: 'depo://rapor.docx',
  basename: 'rapor.docx',
  type: 'file',
  extension: 'docx',
  size: 1200,
  last_modified: 1_757_376_000,
} as FileNode;

let configHeaders: Array<Record<string, string>> = [];

/** What api.js does: the mount becomes the editor's frame. */
class FakeEditor {
  constructor(id: string) {
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
  configHeaders = [];
  (window as unknown as { DocsAPI: unknown }).DocsAPI = { DocEditor: FakeEditor };
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
    const u = String(input);
    const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { 'content-type': 'application/json' } });
    if (u.endsWith(CONFIG)) {
      configHeaders.push({ ...((init?.headers as Record<string, string> | undefined) ?? {}) });
      return json({ documentServerUrl: 'https://docs.example.com', config: { document: { fileType: 'docx' } } });
    }
    if (u.includes('/api/files/plugins/actions')) return json({ actions: [], views: [] });
    return new Response('', { status: 200 });
  });
});
// Pages down first (in-flight work lands, pages unmount, <body> empties),
// while this file's fetch still answers; only then is it taken away.
afterEach(async () => {
  await teardownDom();
  vi.restoreAllMocks();
  delete (window as unknown as { DocsAPI?: unknown }).DocsAPI;
});

function viewer(locale: string) {
  return mount(PreviewModal, {
    attachTo: document.body,
    props: {
      open: true,
      locale,
      file: docx,
      openMode: 'view',
      previewUrl: (p: string) => `/preview?path=${p}`,
      downloadUrl: (p: string) => `/download?path=${p}`,
      onlyOfficeBase: 'https://docs.example.com',
      onlyOfficeConfigEndpoint: CONFIG,
    },
  });
}

describe('the viewer names the screen’s language when it asks for the editor', () => {
  it('a Turkish screen asks in Turkish', async () => {
    viewer('tr');
    await settle();
    expect(configHeaders).toHaveLength(1);
    expect(configHeaders[0]['Accept-Language']).toBe('tr-TR');
  });

  it('an English screen asks in English, whatever the browser was installed in', async () => {
    viewer('en');
    await settle();
    expect(configHeaders).toHaveLength(1);
    expect(configHeaders[0]['Accept-Language']).toBe('en-US');
  });
});
