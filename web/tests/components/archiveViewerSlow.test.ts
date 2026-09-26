// The archive preview says why a listing takes long, and a listing nobody is
// waiting for any more stops and says nothing about the file now shown.
//
// ⚠ The server reads the WHOLE archive from the storage before it can list it,
// which for a large one on an object store is minutes. All that time the
// preview said "Loading…". Stepping to the next file started a second listing
// beside the first, and the first one's end, whatever it was, landed on the
// second file's screen. Closing the preview left the server downloading to
// the end.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import ArchiveViewer from '@brftech/filex-core/src/viewers/ArchiveViewer.vue';

type Pending = { signal: AbortSignal; answer: (names: string[]) => void; path: string };

let pending: Pending[] = [];

function listing(names: string[]) {
  return {
    ok: true,
    status: 200,
    statusText: 'OK',
    json: async () => ({ entries: names.map((name) => ({ name, size: 10 })) }),
  };
}

beforeEach(() => {
  pending = [];
  // Like the browser's: held until answered, and rejected once its signal aborts.
  vi.stubGlobal(
    'fetch',
    vi.fn(
      (_url: string, init: RequestInit) =>
        new Promise((resolve, reject) => {
          const { path } = JSON.parse(String(init.body)) as { path: string };
          init.signal?.addEventListener('abort', () =>
            reject(new DOMException('The operation was aborted.', 'AbortError')),
          );
          pending.push({ signal: init.signal!, path, answer: (names) => resolve(listing(names)) });
        }),
    ),
  );
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('the archive preview', () => {
  it('says what it is doing when the listing takes long', async () => {
    vi.useFakeTimers();
    const w = mount(ArchiveViewer, { props: { url: '/p', filePath: 'depo://big.zip', ext: 'zip' } });
    await flushPromises();
    expect(w.text()).toContain('Loading…');

    await vi.advanceTimersByTimeAsync(4000);
    expect(w.text()).toContain('the whole archive is read before its contents can be listed');
    w.unmount();
  });

  it('stepping to another file stops the first listing, which says nothing on the new one', async () => {
    const w = mount(ArchiveViewer, { props: { url: '/p', filePath: 'depo://big.zip', ext: 'zip' } });
    await flushPromises();
    await w.setProps({ filePath: 'depo://small.zip' });
    await flushPromises();
    expect(pending.map((p) => p.path)).toEqual(['depo://big.zip', 'depo://small.zip']);
    expect(pending[0].signal.aborted, 'the first listing went on after the step').toBe(true);

    expect(w.text(), 'the cut-off listing ended the wait for the new one').toContain('Loading…');
    expect(w.text()).not.toContain('Could not read archive contents.');

    pending[1].answer(['small.txt']);
    await flushPromises();
    expect(w.text()).toContain('small.txt');
    expect(w.text()).not.toContain('Could not read archive contents.');
    w.unmount();
  });

  it('closing the preview stops the listing', async () => {
    const w = mount(ArchiveViewer, { props: { url: '/p', filePath: 'depo://big.zip', ext: 'zip' } });
    await flushPromises();
    w.unmount();
    expect(pending[0].signal.aborted).toBe(true);
  });
});
