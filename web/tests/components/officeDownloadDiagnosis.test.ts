// ONLYOFFICE's "Download failed" (error -4) is one sentence for two failures:
// the document server could not download the document from filex, or the
// browser could not load the converted copy back from the document server
// (issue #80). filex knows which, and the editor now says it: under the
// document server's own sentence comes one of three, from what filex's fetch
// endpoint saw (GET /api/files/onlyoffice/diagnose).
//
// The viewer is the shared core component, so this one rule holds in the
// explorer, the standalone editor tab and the desktop's document windows.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';

import PreviewModal from '@brftech/filex-core/src/modals/PreviewModal.vue';
import { officeDiagnoseEndpoint, officeErrorCode } from '@brftech/filex-core/src/lib/officeDiagnosis';
import { en } from '@brftech/filex-core/src/locales/en';
import { tr } from '@brftech/filex-core/src/locales/tr';
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

type Events = { onError?: (e: unknown) => void };
let editorConfig: { events?: Events } | null = null;
const requested: string[] = [];
let diagnosisAnswer: Record<string, unknown> = {};

class FakeEditor {
  constructor(_id: string, cfg: { events?: Events }) {
    editorConfig = cfg;
  }
  destroyEditor() {}
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
  editorConfig = null;
  requested.length = 0;
  (window as unknown as { DocsAPI: unknown }).DocsAPI = { DocEditor: FakeEditor };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      requested.push(url);
      if (url === CONFIG) {
        return {
          ok: true,
          status: 200,
          json: async () => ({ documentServerUrl: 'https://docs.example.com', config: { document: {} } }),
          text: async () => '',
        };
      }
      if (url.startsWith('/api/files/onlyoffice/diagnose')) {
        return { ok: true, status: 200, json: async () => diagnosisAnswer, text: async () => '' };
      }
      return { ok: true, status: 200, json: async () => ({}), text: async () => '' };
    }),
  );
});

async function openAndFail(locale: 'en' | 'tr', errorCode: number) {
  const w = mount(PreviewModal, {
    props: {
      open: true,
      locale,
      file: docx,
      previewUrl: (p: string) => `/preview?path=${p}`,
      downloadUrl: (p: string) => `/download?path=${p}`,
      onlyOfficeBase: 'https://docs.example.com',
      onlyOfficeConfigEndpoint: CONFIG,
    },
  });
  opened.push(w);
  await settle();
  expect(editorConfig, 'the editor was started').not.toBeNull();
  editorConfig?.events?.onError?.({ type: 'error', data: { errorCode, errorDescription: 'Download failed.' } });
  await settle();
  return w;
}

describe('after "Download failed", the editor says which failure it was', () => {
  it('the document server got the file: the browser leg, and the proxy headers to check', async () => {
    diagnosisAnswer = { verdict: 'served', fetch: { at: '2026-10-01T10:00:00Z', status: 200 }, scope: 'this_process' };
    const w = await openAndFail('en', -4);
    const said = w.get('[data-testid="office-diagnosis"]').text();
    expect(said).toBe(en['viewer.office_download_served']);
    expect(said).toContain('X-Forwarded-Proto');
    expect(requested.some((u) => u === `/api/files/onlyoffice/diagnose?path=${encodeURIComponent(docx.path)}`)).toBe(true);
  });

  it('the document server never asked', async () => {
    diagnosisAnswer = { verdict: 'not_requested', scope: 'this_process' };
    const w = await openAndFail('en', -4);
    expect(w.get('[data-testid="office-diagnosis"]').text()).toBe(en['viewer.office_download_not_requested']);
  });

  it('filex refused it, and says why', async () => {
    diagnosisAnswer = {
      verdict: 'refused',
      fetch: { at: '2026-10-01T10:00:00Z', status: 500, reason_code: 'storage_unavailable', reason: 'the storage could not be opened' },
      scope: 'this_process',
    };
    const w = await openAndFail('en', -4);
    expect(w.get('[data-testid="office-diagnosis"]').text()).toBe(
      en['viewer.office_download_refused'].replace('{reason}', en['viewer.office_fetch_reason.storage_unavailable']),
    );
  });

  it('in the reader\'s language', async () => {
    diagnosisAnswer = { verdict: 'served', scope: 'this_process' };
    const w = await openAndFail('tr', -4);
    expect(w.get('[data-testid="office-diagnosis"]').text()).toBe(tr['viewer.office_download_served']);
  });

  it('another error is not diagnosed as a download', async () => {
    diagnosisAnswer = { verdict: 'served', scope: 'this_process' };
    const w = await openAndFail('en', -3);
    expect(w.find('[data-testid="office-diagnosis"]').exists()).toBe(false);
    expect(requested.some((u) => u.startsWith('/api/files/onlyoffice/diagnose'))).toBe(false);
  });
});

describe('the pieces it is made of', () => {
  it('finds the diagnosis endpoint beside the configuration one, prefix and all', () => {
    expect(officeDiagnoseEndpoint('/api/files/onlyoffice/config')).toBe('/api/files/onlyoffice/diagnose');
    expect(officeDiagnoseEndpoint('https://host/filex/api/files/onlyoffice/config')).toBe(
      'https://host/filex/api/files/onlyoffice/diagnose',
    );
    expect(officeDiagnoseEndpoint('/embedder/office-config')).toBeNull();
    expect(officeDiagnoseEndpoint(null)).toBeNull();
  });

  it('reads the code from ONLYOFFICE\'s error event, not its localised text', () => {
    expect(officeErrorCode({ data: { errorCode: -4, errorDescription: 'İndirme başarısız.' } })).toBe(-4);
    expect(officeErrorCode({ data: { errorDescription: 'Download failed.' } })).toBeNull();
  });
});
