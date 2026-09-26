// The converter window says which step a conversion is on, lets a long one
// finish, and is not closed from under it.
//
// ⚠ It said "Converting…" from the first byte read to the last byte saved, and
// after 180 s it called the conversion failed while the converter was still at
// it: a large video or document simply took longer. A click on the backdrop,
// or on ×, closed the window mid-way, which threw the conversion away with the
// frame that ran it and said nothing.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';

import ConvertModal from '@brftech/filex-core/src/modals/ConvertModal.vue';

type Posted = { id: number; cmd: string };

let posted: Posted[] = [];
let resolveBytes: (b: ArrayBuffer) => void = () => {};
let resolveUpload: () => void = () => {};

const FORMATS = [
  { index: 0, ext: 'docx', format: 'docx', mime: 'application/docx', name: 'Word', from: true, to: false, category: null },
  { index: 1, ext: 'pdf', format: 'pdf', mime: 'application/pdf', name: 'PDF', from: false, to: true, category: null },
];

/** The converter frame answers the message `id`. */
function reply(id: number, body: Record<string, unknown>) {
  window.dispatchEvent(new MessageEvent('message', { data: { source: 'convert-embed', id, ok: true, ...body } }));
}

async function openModal(): Promise<VueWrapper> {
  const w = mount(ConvertModal, {
    props: {
      convertUrl: 'https://convert.example',
      fileName: 'rapor.docx',
      locale: 'en',
      fetchBytes: () => new Promise<ArrayBuffer>((r) => (resolveBytes = r)),
      upload: () => new Promise<void>((r) => (resolveUpload = r)),
    },
    attachTo: document.body,
  });
  const frame = w.get('iframe').element as HTMLIFrameElement;
  vi.spyOn(frame.contentWindow!, 'postMessage').mockImplementation((msg: unknown) => {
    posted.push(msg as Posted);
  });
  window.dispatchEvent(new MessageEvent('message', { data: { source: 'convert-embed', event: 'ready' } }));
  await flushPromises();
  reply(posted.find((p) => p.cmd === 'listFormats')!.id, { formats: FORMATS });
  await flushPromises();
  await w.findAll('.filex-cv__fmt')[0].trigger('click');
  return w;
}

function stepText(w: VueWrapper) {
  return w.get('.filex-cv__convert').text();
}

beforeEach(() => {
  vi.useFakeTimers();
  posted = [];
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  document.body.innerHTML = '';
});

describe('a conversion on its way', () => {
  it('says which step it is on', async () => {
    const w = await openModal();
    await w.get('.filex-cv__convert').trigger('click');
    await flushPromises();
    expect(stepText(w)).toBe('Reading the file…');

    resolveBytes(new ArrayBuffer(8));
    await flushPromises();
    expect(stepText(w)).toBe('Converting…');

    reply(posted.find((p) => p.cmd === 'convert')!.id, { bytes: new ArrayBuffer(4), ext: 'pdf' });
    await flushPromises();
    expect(stepText(w)).toBe('Saving the result…');

    resolveUpload();
    await flushPromises();
    expect(w.emitted('done')?.[0]).toEqual(['rapor.pdf']);
  });

  it('is not called failed while the converter is still at it', async () => {
    const w = await openModal();
    await w.get('.filex-cv__convert').trigger('click');
    resolveBytes(new ArrayBuffer(8));
    await flushPromises();

    await vi.advanceTimersByTimeAsync(10 * 60 * 1000);
    await flushPromises();
    expect(w.find('.filex-cv__err').exists(), 'a long conversion was called failed').toBe(false);
    expect(stepText(w)).toBe('Converting…');
  });

  it('is not closed from under the conversion by a stray click', async () => {
    const w = await openModal();
    await w.get('.filex-cv__convert').trigger('click');
    resolveBytes(new ArrayBuffer(8));
    await flushPromises();

    await w.get('.filex-cv__bg').trigger('click');
    expect(w.emitted('close'), 'the backdrop closed it mid-way').toBeUndefined();

    vi.stubGlobal('confirm', () => false);
    await w.get('.filex-cv__x').trigger('click');
    expect(w.emitted('close'), '× closed it without asking').toBeUndefined();

    vi.stubGlobal('confirm', () => true);
    await w.get('.filex-cv__x').trigger('click');
    expect(w.emitted('close')).toHaveLength(1);
  });
});
