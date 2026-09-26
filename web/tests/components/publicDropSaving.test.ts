// The file-request page says the server is saving once every byte is sent,
// and does not call a drop that arrived "could not be sent".
//
// ⚠ The bar reached 100% when the browser had sent the last byte, and stayed
// there while the server wrote every file to its storage, one after another,
// which for a large drop into an object store is minutes. A proxy that gave up
// in the meantime (Cloudflare at 100 s) answered 524, and the page said
// "Could not be sent" about files that had arrived; sending them again made a
// second submission of them.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

import { usePublicRequest } from '@brftech/filex-core/src/composables/usePublicLink';
import PublicRequestBody from '@brftech/filex-core/src/components/public/PublicRequestBody.vue';
import { en } from '@brftech/filex-core/src/locales/en';

class FakeXHR {
  static last: FakeXHR | null = null;
  upload: { onprogress: ((e: ProgressEvent) => void) | null; onload: (() => void) | null } = {
    onprogress: null,
    onload: null,
  };
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  status = 0;
  responseText = '';
  withCredentials = false;
  open() {}
  setRequestHeader() {}
  send() {
    FakeXHR.last = this;
  }
  /** The browser has sent every byte of the body. */
  sentAll() {
    this.upload.onprogress?.({ lengthComputable: true, loaded: 100, total: 100 } as ProgressEvent);
    this.upload.onload?.();
  }
  answer(status: number, body = '') {
    this.status = status;
    this.responseText = body;
    this.onload?.();
  }
}

function drop() {
  return usePublicRequest('tok', {
    locale: () => 'en',
    errorText: () => 'Could not be sent',
    unansweredText: () => 'UNANSWERED',
    fetchImpl: (async () => new Response(JSON.stringify({ kind: 'request' }), { status: 200 })) as typeof fetch,
  });
}

const file = () => new File(['x'.repeat(100)], 'rapor.pdf');

beforeEach(() => {
  FakeXHR.last = null;
  vi.stubGlobal('XMLHttpRequest', FakeXHR);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('a drop on its way', () => {
  it('says the server is saving once every byte is sent, and done when it answers', async () => {
    const d = drop();
    const done = d.upload([file()]);
    await flushPromises();
    FakeXHR.last!.sentAll();
    expect(d.uploads.value[0].state).toBe('saving');

    FakeXHR.last!.answer(200, '{"ok":true,"count":1}');
    await done;
    expect(d.uploads.value[0].state).toBe('done');
  });

  it('a proxy that gives up after every byte was sent is not "could not be sent"', async () => {
    for (const status of [504, 524]) {
      const d = drop();
      const done = d.upload([file()]);
      await flushPromises();
      FakeXHR.last!.sentAll();
      FakeXHR.last!.answer(status, '<html>A timeout occurred</html>');
      await done;
      expect(d.uploads.value[0].state, `status ${status}`).toBe('unconfirmed');
      expect(d.uploads.value[0].error).toBe('UNANSWERED');
    }
  });

  it('a connection lost after every byte was sent is not "could not be sent" either', async () => {
    const d = drop();
    const done = d.upload([file()]);
    await flushPromises();
    FakeXHR.last!.sentAll();
    FakeXHR.last!.onerror?.();
    await done;
    expect(d.uploads.value[0].state).toBe('unconfirmed');
  });

  it('a failure before every byte was sent is a failure', async () => {
    const d = drop();
    const done = d.upload([file()]);
    await flushPromises();
    FakeXHR.last!.answer(524, '');
    await done;
    expect(d.uploads.value[0].state).toBe('failed');
    expect(d.uploads.value[0].error).toBe('Could not be sent');
  });

  it('a refusal the server words is still a refusal, whenever it comes', async () => {
    const d = drop();
    const done = d.upload([file()]);
    await flushPromises();
    FakeXHR.last!.sentAll();
    FakeXHR.last!.answer(503, JSON.stringify({ error: 'storage_unavailable', message: 'The storage is not answering.' }));
    await done;
    expect(d.uploads.value[0].state).toBe('failed');
    expect(d.uploads.value[0].error).toBe('The storage is not answering.');
  });
});

describe('the rows on the page', () => {
  it('draw "Saving…" and the unconfirmed sentence', () => {
    const w = mount(PublicRequestBody, {
      props: {
        info: null,
        locale: 'en',
        uploads: [
          { name: 'a.pdf', size: 10, percent: 100, state: 'saving' },
          { name: 'b.pdf', size: 10, percent: 100, state: 'unconfirmed' },
        ],
      },
    });
    const rows = w.findAll('.fe-pdrop__item');
    expect(rows[0].text()).toContain(en['public.upload_saving']);
    expect(rows[0].find('progress').attributes('value'), 'the bar should not sit at 100%').toBeUndefined();
    expect(rows[1].text()).toContain(en['public.upload_unanswered']);
    expect(rows[1].find('.fe-surface__error').exists(), 'it is not drawn as a failure').toBe(false);
  });
});
