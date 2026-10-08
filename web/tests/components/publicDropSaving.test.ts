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
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { usePublicRequest } from '@brftech/filex-core/src/composables/usePublicLink';
import PublicRequestBody from '@brftech/filex-core/src/components/public/PublicRequestBody.vue';
import { ref } from 'vue';
import { PUBLIC_TEXT } from '@brftech/filex-core/src/composables/usePublicText';
import { publicTable } from '../helpers/publicStrings';

/** The server's public sentences (server.public.*): the page's only words. */
const pub = publicTable('en');

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

  // ⚠ Only a proxy's TIMEOUT means "maybe saved". nginx buffers the request
  // body, so a filex that is down answers 502 (Cloudflare 520) only after the
  // browser sent every byte: the drop never reached filex, and "it may still
  // be saving — check before sending it again" made the visitor lose it.
  it('a proxy that says filex is down after every byte was sent is a failure', async () => {
    for (const status of [502, 520]) {
      const d = drop();
      const done = d.upload([file()]);
      await flushPromises();
      FakeXHR.last!.sentAll();
      FakeXHR.last!.answer(status, '<html>Bad gateway</html>');
      await done;
      expect(d.uploads.value[0].state, `status ${status}`).toBe('failed');
      expect(d.uploads.value[0].error).toBe('Could not be sent');
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
      // What PublicLinkPage hands down after GET /api/public/strings.
      global: { provide: { [PUBLIC_TEXT as symbol]: { table: ref(pub), lang: ref('en'), ready: ref(true) } } },
    });
    const rows = w.findAll('.fe-pdrop__item');
    expect(rows[0].text()).toContain(pub.upload_saving);
    expect(rows[0].find('progress').attributes('value'), 'the bar should not sit at 100%').toBeUndefined();
    expect(rows[1].text()).toContain(pub.upload_unanswered);
    expect(rows[1].find('.fe-surface__error').exists(), 'it is not drawn as a failure').toBe(false);
  });

  // ⚠ #65 drew the unconfirmed sentence in the amber accent (--fe-warning,
  // #f59e0b): 2.1:1 on the white card — the one line that tells the visitor
  // not to send the files again, and the hardest to read. Text is ink; the
  // warning is the rule beside it.
  it('write the unconfirmed sentence in an ink that reads (4.5:1 on the card, light and dark)', () => {
    const css = readFileSync(resolve(__dirname, '../../../packages/core/src/styles/base.css'), 'utf8');
    const vars = readFileSync(resolve(__dirname, '../../../packages/core/src/styles/variables.css'), 'utf8');
    const rule = css.match(/\.fe-pdrop__unconfirmed \{[^}]*\}/)?.[0] ?? '';
    const token = rule.match(/(?:^|\s)color: var\((--fe-[a-z-]+)\)/)?.[1];
    expect(token, 'the sentence names no colour token').toBeTruthy();
    const light = vars.slice(0, vars.indexOf('--fe-ppage-card: #1f242c'));
    const dark = vars.slice(vars.indexOf('[data-theme="dark"]') > 0 ? vars.indexOf('[data-theme="dark"]') : vars.indexOf('#15171c'));
    const pick = (block: string, name: string) => block.match(new RegExp(`${name}:\\s*(#[0-9a-fA-F]{6})`))?.[1] ?? '';
    for (const [theme, block, card] of [
      ['light', light, pick(vars, '--fe-ppage-card')],
      ['dark', dark, '#1f242c'],
    ] as const) {
      const ink = pick(block, token!);
      expect(ink, `${token} in ${theme}`).not.toBe('');
      expect(contrast(ink, card), `${token} on the ${theme} card`).toBeGreaterThanOrEqual(4.5);
    }
  });
});

function contrast(a: string, b: string): number {
  const lum = (hex: string) => {
    const [r, g, bl] = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255).map((c) =>
      c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4,
    );
    return 0.2126 * r + 0.7152 * g + 0.0722 * bl;
  };
  const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p);
  return (x + 0.05) / (y + 0.05);
}
