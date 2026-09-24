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
import { createArchivePreviewCache } from '@brftech/filex-core/src/lib/archivePreviewCache';

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

  it('prompts for an encrypted archive, retries with the password, and keeps directory navigation', async () => {
    const archivePreviewCache = createArchivePreviewCache();
    fetchMock
      .mockReset()
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        json: async () => ({ code: 'PASSWORD_REQUIRED' }),
      })
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        json: async () => ({ code: 'BAD_PASSWORD' }),
      })
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        statusText: 'OK',
        json: async () => ({
          entries: [
            { name: 'folder/report.txt', size: 42 },
            { name: 'root.txt', size: 7 },
          ],
        }),
      });

    const wrapper = mount(ArchiveViewer, {
      props: { url: '/preview', filePath: 'main://protected.7z', ext: '7z', archivePreviewCache },
    });
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));

    await vi.waitFor(() => expect(wrapper.find('input[type="password"]').exists()).toBe(true));
    let password = wrapper.find<HTMLInputElement>('input[type="password"]');
    await password.setValue('wrong');
    await wrapper.find('.fe-btn--primary').trigger('click');
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    await vi.waitFor(() => expect(wrapper.text()).toMatch(/incorrect/i));

    password = wrapper.find<HTMLInputElement>('input[type="password"]');
    await password.setValue('correct');
    await wrapper.find('.fe-btn--primary').trigger('click');
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3));
    const request = JSON.parse(String(fetchMock.mock.calls[2][1]?.body));
    expect(request).toEqual({ path: 'main://protected.7z', password: 'correct' });
    await vi.waitFor(() => expect(wrapper.text()).toContain('folder'));

    const folder = wrapper.findAll('.filex-viewer-archive__entry')
      .find((entry) => entry.text().includes('folder'));
    expect(folder).toBeDefined();
    await folder!.trigger('click');
    expect(wrapper.text()).toContain('report.txt');
    expect(wrapper.text()).not.toContain('root.txt');

    wrapper.unmount();
    const reopened = mount(ArchiveViewer, {
      props: { url: '/preview', filePath: 'main://protected.7z', ext: '7z', archivePreviewCache },
    });
    await vi.waitFor(() => expect(reopened.text()).toContain('folder'));
    expect(fetchMock).toHaveBeenCalledTimes(3);
    expect(reopened.find('input[type="password"]').exists()).toBe(false);
    reopened.unmount();
    archivePreviewCache.clear();
  });

  it('expires cached encrypted archive listings after two minutes', () => {
    vi.useFakeTimers();
    const archivePreviewCache = createArchivePreviewCache();
    archivePreviewCache.remember('main://protected.7z', [{ name: 'report.txt', size: 42 }]);
    expect(archivePreviewCache.recall('main://protected.7z')).toHaveLength(1);

    vi.advanceTimersByTime(2 * 60 * 1000);
    expect(archivePreviewCache.recall('main://protected.7z')).toBeUndefined();
    archivePreviewCache.clear();
    vi.useRealTimers();
  });
});

/* ⚠ QA, 2026-09-21: the zip preview's size column read "1.9 KB" — base 1024,
   a dot, English units — beside the explorer's "1,96 KB" for the same
   bytes. It is the product's one byte formatter now. */
describe('ArchiveViewer sizes', () => {
  it('are written the way the interface language writes a size', async () => {
    fetchMock.mockResolvedValue({
      ok: true,
      status: 200,
      statusText: 'OK',
      json: async () => ({ entries: [{ name: 'a.txt', size: 1960 }] }),
    });
    const w = mount(ArchiveViewer, {
      props: { url: '/preview', filePath: 's3://docs/sample.zip', ext: 'zip', locale: 'tr', t: (k: string) => k },
    });
    await vi.waitFor(() => expect(w.text()).toContain('1,96'));
    expect(w.text()).not.toContain('1.9 KB');
  });
});
