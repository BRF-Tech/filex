// ArchiveViewer must ask the endpoint the embedder configured.
//
// Red proof for the defect: the viewer hardcoded
// `fetch('/api/files/archive/list')`. Every other call in @brftech/filex-core
// goes through `api.endpoints.*`, which honour `apiBase` — so on the package's
// headline use case (an embed on a host page with
// `apiBase: 'https://files.example.com'`) the archive preview posted to the
// HOST page's origin and 404'd, while the rest of the explorer worked.
//
// packages/core has no runner of its own; the gate lives here, like coreKeys.
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount } from '@vue/test-utils';
import ArchiveViewer from '@brftech/filex-core/src/viewers/ArchiveViewer.vue';

const fetchMock = vi.fn();

beforeEach(() => {
  fetchMock.mockReset();
  fetchMock.mockResolvedValue({
    ok: true,
    status: 200,
    statusText: 'OK',
    json: async () => ({ entries: [] }),
  });
  vi.stubGlobal('fetch', fetchMock);
});

describe('ArchiveViewer', () => {
  it('posts to the configured cross-origin endpoint', async () => {
    mount(ArchiveViewer, {
      props: {
        url: 'https://files.example.com/api/files/manager?q=preview',
        filePath: 's3://docs/sample.zip',
        ext: 'zip',
        archiveListUrl: 'https://files.example.com/api/files/archive/list',
      },
    });
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalled());
    expect(fetchMock.mock.calls[0][0]).toBe('https://files.example.com/api/files/archive/list');
  });

  it('falls back to the same-origin path when nothing is configured', async () => {
    mount(ArchiveViewer, {
      props: { url: '/preview', filePath: 's3://docs/sample.zip', ext: 'zip' },
    });
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalled());
    expect(fetchMock.mock.calls[0][0]).toBe('/api/files/archive/list');
  });
});
