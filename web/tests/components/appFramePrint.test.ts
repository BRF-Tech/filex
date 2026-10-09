// `ui.print` and `FileInfo.encrypted` in the frame an app's interface runs in
// (AppFrame.vue, task #189 P1b; security review sec055 S9 + S10).
//
//  - `ui.print` needs the app's `ui:print` grant, a file name and PDF bytes;
//  - filex asks EVERY time, even right after a gesture in the frame: the
//    question row's Allow is the print page's own button (the frame of
//    `/_print/` sits in the row, lib/printPdf), because only a click in that
//    page counts as the person's there - not one on filex's page, not one in
//    the app's frame (red before S9: a gesture printed with no question, and
//    "Allow" was a button of filex's page);
//  - "Don't allow" prints nothing and is `cancelled`; one print at a time;
//  - the PDF is held once, as one Blob; a stream that does not start `%PDF-`
//    is refused at its first chunk and let go of, and nothing past
//    LIMITS.maxPrintBytes (64 MiB) is printed (red before S10: the stream was
//    read to its end before the check, and the limit was the download's
//    256 MiB);
//  - a host that names no print page answers `unavailable`;
//  - `encrypted` is handed over as the host gave it (the server's stamp),
//    and is absent for a file that is not encrypted.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import type { PrintPdfOptions } from '@brftech/filex-core/src/lib/printPdf';

interface PrintCall {
  url: string;
  pdf: Blob;
  opts: PrintPdfOptions;
  frame: HTMLIFrameElement;
  /** The person's click on the print page's button. */
  click: () => void;
}
const printed: PrintCall[] = [];
vi.mock('@brftech/filex-core/src/lib/printPdf', async (orig) => {
  const real = await orig<typeof import('@brftech/filex-core/src/lib/printPdf')>();
  return {
    ...real,
    printPdf: vi.fn(
      (url: string, pdf: Blob, opts: PrintPdfOptions = {}) =>
        new Promise<HTMLIFrameElement>((resolve, reject) => {
          const frame = document.createElement('iframe');
          frame.dataset.testid = 'appframe-print';
          (opts.mount ?? document.body).appendChild(frame);
          opts.signal?.addEventListener('abort', () => {
            frame.remove();
            reject(new real.PrintFailure('cancelled', 'the person said no'));
          });
          printed.push({ url, pdf, opts, frame, click: () => resolve(frame) });
          opts.onAsk?.({ width: 88, height: 32 });
        }),
    ),
  };
});

import AppFrame from '@brftech/filex-core/src/components/plugin/AppFrame.vue';
import { HELLO, LIMITS } from '../../../packages/app-ui/src/protocol';
import { teardownDom, unmountAll } from '../helpers/teardown';
import { answerAccountPrefs } from '../helpers/accountPrefs';

answerAccountPrefs();

afterEach(async () => {
  await teardownDom();
  vi.restoreAllMocks();
  printed.length = 0;
  delete (navigator as unknown as Record<string, unknown>).userActivation;
});

const settle = async () => {
  for (let i = 0; i < 4; i++) {
    await new Promise((r) => setTimeout(r, 0));
    await flushPromises();
  }
};

function fakeWindow() {
  const sent: Array<{ data: unknown; ports: MessagePort[] }> = [];
  return {
    sent,
    postMessage(data: unknown, _origin: string, transfer?: Transferable[]) {
      sent.push({ data, ports: (transfer ?? []) as MessagePort[] });
    },
  };
}

/** The answers each port is waiting for, by id: two calls may be open at once. */
const waiting = new WeakMap<MessagePort, Map<number, (v: any) => void>>();

function ask(port: MessagePort, id: number, method: string, params?: unknown, transfer: Transferable[] = []): Promise<any> {
  let open = waiting.get(port);
  if (!open) {
    const m = new Map<number, (v: any) => void>();
    port.onmessage = (ev) => {
      const done = m.get(ev.data?.id);
      if (!done) return;
      m.delete(ev.data.id);
      done(ev.data);
    };
    waiting.set(port, m);
    open = m;
  }
  const pending = open;
  return new Promise((resolve) => {
    pending.set(id, resolve);
    port.postMessage({ id, method, params }, transfer);
  });
}

/** ui.print with a stream, transferred as the SDK does. */
function askStream(port: MessagePort, id: number, data: ReadableStream<Uint8Array>): Promise<any> {
  return ask(port, id, 'ui.print', { name: 'report.pdf', data }, [data as unknown as Transferable]);
}

const enc = (s: string) => new TextEncoder().encode(s);
const PDF_TEXT = '%PDF-1.7\n%%EOF\n';
const PDF = () => enc(PDF_TEXT).buffer as ArrayBuffer;
const UI = { url: '/_appui/office/0123456789abcdef/index.html', grants: ['files:read', 'files:write', 'ui'], engine: false, version: '1.0.0' };
const PRINT = { ...UI, grants: [...UI.grants, 'ui:print'] };

async function connectFrame(props: Record<string, unknown> = {}, api: Record<string, unknown> = {}) {
  const w = mount(AppFrame, {
    props: {
      api: {
        appUIUrl: (u: string) => `about:blank#${u}`,
        printFrameUrl: () => 'https://files.example.com/_print/',
        ...api,
      },
      app: 'office', view: 'editor', placement: 'viewer', ui: UI, locale: 'en', title: 'Office',
      files: [{ path: 'main://report.docx', name: 'report.docx', size: 9 }],
      ...props,
    },
    attachTo: document.body,
  });
  await settle();
  const f = document.querySelector('iframe') as HTMLIFrameElement;
  const win = fakeWindow();
  Object.defineProperty(f, 'contentWindow', { get: () => win });
  window.dispatchEvent(new MessageEvent('message', { data: { type: HELLO, v: 1 }, source: win as unknown as Window, origin: 'null' }));
  return { w, port: win.sent[0].ports[0] };
}

function activation(active: boolean) {
  Object.defineProperty(navigator, 'userActivation', { configurable: true, get: () => ({ isActive: active, hasBeenActive: active }) });
}

const row = () => document.querySelector('[data-testid="appframe-print-ask"]') as HTMLElement | null;

describe('ui.print', () => {
  it('is refused without the grant, and for what is not a named PDF', async () => {
    const { port } = await connectFrame();
    activation(true);
    expect((await ask(port, 1, 'ui.print', { name: 'a.pdf', data: PDF() })).error.code).toBe('not_granted');
    unmountAll();
    const { port: p2 } = await connectFrame({ ui: PRINT });
    activation(true);
    for (const name of ['', '../a.pdf', 'a/b.pdf']) {
      expect((await ask(p2, 2, 'ui.print', { name, data: PDF() })).error.code, name).toBe('invalid');
    }
    expect((await ask(p2, 3, 'ui.print', { name: 'a.pdf', data: '%PDF-1.7' })).error.code, 'a string').toBe('invalid');
    expect((await ask(p2, 4, 'ui.print', { name: 'a.pdf', data: PDF(), mime: 'text/html' })).error.code, 'another type').toBe('invalid');
    expect((await ask(p2, 5, 'ui.print', { name: 'a.pdf', data: enc('<html>').buffer })).error.code, 'not a PDF').toBe('invalid');
    expect(printed).toHaveLength(0);
    expect(row(), 'nothing is asked').toBeNull();
  });

  it('asks every time, even right after a gesture in the frame - and the Allow is the print page’s own button', async () => {
    const { port } = await connectFrame({ ui: PRINT });
    activation(true);
    const pdf = PDF();
    const size = pdf.byteLength;
    const answer = ask(port, 1, 'ui.print', { name: 'report.pdf', data: pdf, mime: 'application/pdf' }, [pdf]);
    await settle();
    expect(printed).toHaveLength(1);
    const call = printed[0];
    expect(call.url).toBe('https://files.example.com/_print/');

    const bar = row();
    expect(bar, 'filex asks, gesture or not').not.toBeNull();
    expect(bar!.dataset.shown).toBe('true');
    expect(bar!.textContent).toContain('Office');
    expect(bar!.textContent).toContain('report.pdf');
    // The print page's frame is IN the row, where Allow goes; filex draws no
    // Allow of its own (a click on it would not count in the print page).
    expect(bar!.contains(call.frame)).toBe(true);
    expect(call.opts.mount && bar!.contains(call.opts.mount)).toBe(true);
    expect(document.querySelector('[data-testid="appframe-consent-allow"]')).toBeNull();
    expect(call.opts.label).toBe('Allow');
    expect(call.opts.armMs, 'armed like every other question').toBe(600);
    expect(typeof call.opts.look).toBe('object');

    let done = false;
    void answer.then(() => (done = true));
    await settle();
    expect(done, 'nothing is printed until the person clicks').toBe(false);

    call.click();
    expect((await answer).result).toEqual({ printed: true, size });
    await settle();
    // Kept for the print dialog, out of sight.
    expect(row()?.dataset.shown).toBe('false');
    expect(row()?.getAttribute('aria-hidden')).toBe('true');
    expect(document.contains(call.frame)).toBe(true);
  });

  it('"Don’t allow" prints nothing and tells the app cancelled', async () => {
    const { port } = await connectFrame({ ui: PRINT });
    activation(false);
    const answer = ask(port, 1, 'ui.print', { name: 'report.pdf', data: PDF() });
    await settle();
    expect(row()?.dataset.shown).toBe('true');
    (document.querySelector('[data-testid="appframe-print-deny"]') as HTMLButtonElement).click();
    expect((await answer).error.code).toBe('cancelled');
    await settle();
    expect(row()).toBeNull();
    expect(document.querySelector('iframe[data-testid="appframe-print"]')).toBeNull();
  });

  it('prints one at a time: a second while the first is asked about is unavailable', async () => {
    const { port } = await connectFrame({ ui: PRINT });
    const first = ask(port, 1, 'ui.print', { name: 'report.pdf', data: PDF() });
    await settle();
    expect((await ask(port, 2, 'ui.print', { name: 'other.pdf', data: PDF() })).error.code).toBe('unavailable');
    expect(printed).toHaveLength(1);
    printed[0].click();
    expect((await first).result.printed).toBe(true);
  });

  it('hands the print page ONE Blob of the PDF, typed as one', async () => {
    const { port } = await connectFrame({ ui: PRINT });
    const pieces = [enc('%PD'), enc('F-1.7\n'), enc('%%EOF\n')];
    const stream = new ReadableStream<Uint8Array>({
      pull(c) {
        const p = pieces.shift();
        if (p) c.enqueue(p);
        else c.close();
      },
    });
    const answer = askStream(port, 1, stream);
    await vi.waitFor(() => expect(printed).toHaveLength(1));
    expect(printed[0].pdf).toBeInstanceOf(Blob);
    expect(printed[0].pdf.type).toBe('application/pdf');
    expect(printed[0].pdf.size).toBe(PDF_TEXT.length);
    printed[0].click();
    expect((await answer).result).toEqual({ printed: true, size: PDF_TEXT.length });
  });

  it('refuses a stream at its first chunk when it is not a PDF, and lets go of it', async () => {
    const { port } = await connectFrame({ ui: PRINT });
    let pulls = 0;
    let cancelled = false;
    const stream = new ReadableStream<Uint8Array>({
      pull(c) {
        if (pulls++ < 200) c.enqueue(enc('<html><body>' + 'x'.repeat(200) + '</body></html>'));
        else c.close();
      },
      cancel() {
        cancelled = true;
      },
    });
    expect((await askStream(port, 1, stream)).error.code).toBe('invalid');
    await vi.waitFor(() => expect(cancelled, 'the rest is never read').toBe(true));
    expect(pulls).toBeLessThan(200);
    expect(printed).toHaveLength(0);
  });

  it('prints at most LIMITS.maxPrintBytes (64 MiB), streamed or not', async () => {
    expect(LIMITS.maxPrintBytes).toBe(64 << 20);
    expect(LIMITS.maxPrintBytes).toBeLessThan(LIMITS.maxDownloadBytes);
    const { port } = await connectFrame({ ui: PRINT });
    activation(true);
    const big = new Uint8Array(LIMITS.maxPrintBytes + 1);
    big.set(enc('%PDF-'));
    expect((await ask(port, 1, 'ui.print', { name: 'big.pdf', data: big.buffer }, [big.buffer])).error.code).toBe('too_large');

    const mib = 1 << 20;
    const piece = new Uint8Array(mib);
    piece.set(enc('%PDF-'));
    let sent = 0;
    let cancelled = false;
    // ⚠ Longer than the limit by more than the transferred stream reads
    // ahead: a stream that ends one piece past the limit has been read to
    // its close before the reader's cancel reaches it, and a closed stream is
    // never cancelled - the test then saw no cancel although the reading had
    // stopped at the limit.
    const pieces = (LIMITS.maxPrintBytes >> 20) + 16;
    const stream = new ReadableStream<Uint8Array>({
      pull(c) {
        if (sent++ < pieces) c.enqueue(piece);
        else c.close();
      },
      cancel() {
        cancelled = true;
      },
    });
    expect((await askStream(port, 2, stream)).error.code).toBe('too_large');
    await vi.waitFor(() => expect(cancelled, 'let go of once past the limit').toBe(true));
    expect(sent, 'not read to its end').toBeLessThan(pieces);
    expect(printed).toHaveLength(0);
  });

  it('is unavailable where the host names no print page', async () => {
    const { port } = await connectFrame({ ui: PRINT }, { printFrameUrl: undefined });
    activation(true);
    expect((await ask(port, 1, 'ui.print', { name: 'report.pdf', data: PDF() })).error.code).toBe('unavailable');
    expect(printed).toHaveLength(0);
  });

  it('takes the question away with the frame', async () => {
    const { port } = await connectFrame({ ui: PRINT });
    const answer = ask(port, 1, 'ui.print', { name: 'report.pdf', data: PDF() });
    await settle();
    expect(printed[0].opts.signal?.aborted).toBe(false);
    void answer;
    unmountAll();
    await settle();
    expect(printed[0].opts.signal?.aborted).toBe(true);
    expect(document.querySelector('iframe[data-testid="appframe-print"]')).toBeNull();
  });
});

describe('FileInfo.encrypted', () => {
  it('hands the server’s stamp to the interface, and nothing for a plain file', async () => {
    const { port } = await connectFrame({
      files: [
        { path: 'main://Kasa/a.docx', name: 'a.docx', size: 1, encrypted: 'vault' },
        { path: 'main://Sifreli/b.docx', name: 'b.docx', size: 1, encrypted: 'folder' },
        { path: 'main://c.docx', name: 'c.docx', size: 1 },
      ],
    });
    const s = await ask(port, 1, 'session.get');
    expect(s.result.files[0].encrypted).toBe('vault');
    expect(s.result.files[1].encrypted).toBe('folder');
    expect('encrypted' in s.result.files[2]).toBe(false);
  });

  // sec055 S8: filex 0.55 decrypts nothing for an app and the server refuses
  // it an encrypted file's bytes and its save, so the interface is told the
  // file is read-only and a save is refused here before anything is sent.
  // Red before: readOnly was false (the app holds files:write) and the save
  // went to the server.
  it('an encrypted file is read-only to the interface, and its save never leaves the frame', async () => {
    const pluginUISave = vi.fn(async () => ({ path: 'x', size: 1 }));
    const { port } = await connectFrame(
      {
        files: [
          { path: 'main://Sifreli/b.docx', name: 'b.docx', size: 1, encrypted: 'folder' },
          { path: 'main://rapor.docx.fxe', name: 'rapor.docx.fxe', size: 1, encrypted: 'file' },
          { path: 'main://c.docx', name: 'c.docx', size: 1 },
        ],
      },
      { pluginUISave },
    );
    const s = await ask(port, 1, 'session.get');
    expect(s.result.files[0].readOnly).toBe(true);
    expect(s.result.files[1].readOnly).toBe(true);
    expect(s.result.files[1].encrypted).toBe('file');
    expect(s.result.files[2].readOnly, 'a plain file the app may write').toBe(false);
    for (const [i, index] of [0, 1].entries()) {
      const r = await ask(port, 2 + i, 'file.save', { index, data: 'x' });
      expect(r.error?.code, `file ${index}`).toBe('read_only');
    }
    expect(pluginUISave).not.toHaveBeenCalled();
  });
});

describe('editing together (coedit.*)', () => {
  it('is a known method that filex 0.55 does not offer: unavailable, never unknown_method', async () => {
    const { port } = await connectFrame();
    for (const [i, m] of ['coedit.join', 'coedit.subscribe', 'coedit.append', 'coedit.lease', 'coedit.cursor', 'coedit.blob.put', 'coedit.blob.get', 'coedit.leave'].entries()) {
      expect((await ask(port, i + 1, m, {})).error.code, m).toBe('unavailable');
    }
  });
});
