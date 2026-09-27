// The legacy converter runs in another site's frame. The window takes its
// messages from THAT frame only, at the converter's configured origin, and
// addresses its own messages to that origin rather than to whatever the frame
// happens to hold.
//
// A message event carries who sent it (`source`, `origin`); a field inside
// the payload (`source: 'convert-embed'`, `{event: 'save'}`) is only what the
// sender chose to write, so it cannot be what decides.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';

import ConvertModal from '@brftech/filex-core/src/modals/ConvertModal.vue';

const mounted: VueWrapper[] = [];

afterEach(() => {
  mounted.splice(0).forEach((w) => w.unmount());
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  document.body.innerHTML = '';
});

function message(data: unknown, source: Window | null, origin: string) {
  window.dispatchEvent(new MessageEvent('message', { data, source, origin }));
}

// ── the converter ───────────────────────────────────────────────────────────

const CONVERT = 'https://convert.example';

async function openConvert() {
  const w = mount(ConvertModal, {
    props: {
      convertUrl: `${CONVERT}/convert`,
      fileName: 'rapor.docx',
      locale: 'en',
      fetchBytes: async () => new ArrayBuffer(4),
      upload: async () => undefined,
    },
    attachTo: document.body,
  });
  mounted.push(w);
  const frame = w.get('iframe').element as HTMLIFrameElement;
  const posted: Array<{ msg: { cmd: string; id: number }; target: string }> = [];
  vi.spyOn(frame.contentWindow!, 'postMessage').mockImplementation(((msg: { cmd: string; id: number }, target: string) => {
    posted.push({ msg, target });
  }) as never);
  return { w, frame: frame.contentWindow!, posted };
}

describe('the converter', () => {
  it('starts when its own frame says it is ready, and addresses the converter’s origin', async () => {
    const { frame, posted } = await openConvert();
    message({ source: 'convert-embed', event: 'ready' }, frame, CONVERT);
    await flushPromises();
    expect(posted.map((p) => p.msg.cmd)).toEqual(['listFormats']);
    expect(posted[0].target).toBe(CONVERT);
  });

  it('a "ready" or an answer from any other window is not the converter’s', async () => {
    const { w, frame, posted } = await openConvert();
    message({ source: 'convert-embed', event: 'ready' }, window, CONVERT);
    message({ source: 'convert-embed', event: 'ready' }, frame, 'https://elsewhere.example');
    await flushPromises();
    expect(posted).toEqual([]);

    message({ source: 'convert-embed', event: 'ready' }, frame, CONVERT);
    await flushPromises();
    const id = posted[0].msg.id;
    // An answer to the real request, from the wrong window: not taken.
    message({ source: 'convert-embed', id, ok: true, formats: [{ index: 0, ext: 'evil', format: 'evil', from: true, to: true }] }, window, CONVERT);
    await flushPromises();
    expect(w.findAll('.filex-cv__fmt')).toHaveLength(0);
    // The frame's own answer is.
    message(
      {
        source: 'convert-embed',
        id,
        ok: true,
        formats: [
          { index: 0, ext: 'docx', format: 'docx', mime: '', name: 'Word', from: true, to: false, category: null },
          { index: 1, ext: 'pdf', format: 'pdf', mime: '', name: 'PDF', from: false, to: true, category: null },
        ],
      },
      frame,
      CONVERT,
    );
    await flushPromises();
    const offered = w.findAll('.filex-cv__fmt').map((b) => b.text());
    expect(offered).toHaveLength(1);
    expect(offered[0]).toMatch(/pdf/i);
  });
});
