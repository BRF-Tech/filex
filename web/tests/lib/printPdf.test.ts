// lib/printPdf - an app's PDF printed from filex's print page (`ui.print`,
// task #189 P1b; security review sec055 S9 + S10).
//
//  - only bytes that start like a PDF are printed;
//  - collectPdf holds a PDF as ONE Blob: a stream that does not start `%PDF-`
//    is refused at its first chunk and not read further, nothing is read past
//    the limit, and loose chunks are folded into the Blob as they come (red
//    before S10: the module read a stream to its end into parts, then copied
//    the parts into a second buffer, and checked `%PDF-` only after that);
//  - the PDF goes to the print page only after THAT frame said it is ready
//    (`event.source`), and only to the page's origin, as the Blob itself;
//  - the page prints only on the person's click on its own button: the frame
//    is shown as that button when the page asks, and armed a moment later;
//    "Don't allow" tells the page and is `cancelled` (red before S9: the
//    frame stayed hidden and the page printed as soon as the PDF loaded);
//  - a page that never loads, or loads without saying it is ready (a site the
//    page may not be framed by), is `unavailable`, not a hang.
import { describe, expect, it, vi } from 'vitest';

import { collectPdf, isPdf, lookOf, PRINT_FOLD_BYTES, PRINT_LOOK, printPdf, PrintFailure } from '@brftech/filex-core/src/lib/printPdf';

const enc = (s: string) => new TextEncoder().encode(s);
const PDF_TEXT = '%PDF-1.7\n%%EOF\n';
const PDF = () => new Blob([enc(PDF_TEXT)], { type: 'application/pdf' });

/** A window, a document and a frame that record what printPdf does. */
function rig() {
  const listeners = new Set<(ev: MessageEvent) => void>();
  const posted: Array<{ data: any; origin: string; transfer: Transferable[] }> = [];
  const frameWin = {
    postMessage: vi.fn((data: unknown, origin: string, transfer: Transferable[] = []) => {
      posted.push({ data, origin, transfer });
    }),
  };
  const attrs: Record<string, string> = {};
  const frameListeners: Record<string, Array<() => void>> = {};
  const frame = {
    contentWindow: frameWin,
    dataset: {} as Record<string, string>,
    style: { cssText: '' },
    removed: false,
    setAttribute(k: string, v: string) {
      attrs[k] = v;
    },
    removeAttribute(k: string) {
      delete attrs[k];
    },
    addEventListener(t: string, fn: () => void) {
      (frameListeners[t] ??= []).push(fn);
    },
    removeEventListener(t: string, fn: () => void) {
      frameListeners[t] = (frameListeners[t] ?? []).filter((f) => f !== fn);
    },
    remove() {
      this.removed = true;
    },
  };
  const mount = { appendChild: vi.fn() };
  const win = {
    location: { href: 'https://files.example.com/drive/explore' },
    document: { createElement: () => frame, body: mount },
    addEventListener: (_: string, fn: (ev: MessageEvent) => void) => listeners.add(fn),
    removeEventListener: (_: string, fn: (ev: MessageEvent) => void) => listeners.delete(fn),
  };
  const fire = (data: unknown, source: unknown, origin = 'https://files.example.com') => {
    for (const fn of [...listeners]) fn({ data, source, origin } as unknown as MessageEvent);
  };
  const loaded = () => (frameListeners.load ?? []).forEach((fn) => fn());
  /** The page's end of the port, recording what printPdf tells it. */
  const pagePort = () => {
    // ⚠ Not `instanceof MessagePort`: under happy-dom the MessageChannel
    // printPdf makes is Node's, and its port is no instance of the
    // environment's MessagePort - the test then found no port at all.
    const port = posted[0].transfer.find((x) => !!x && typeof (x as MessagePort).postMessage === 'function') as MessagePort;
    const told: any[] = [];
    port.onmessage = (ev) => told.push(ev.data);
    return { port, told };
  };
  return { win: win as unknown as Window, frame, frameWin, attrs, mount, posted, fire, listeners, loaded, pagePort };
}

/** Wait until the rig's frame was drawn (printPdf reads the Blob's head first). */
async function drawn(r: ReturnType<typeof rig>) {
  await vi.waitFor(() => expect(r.mount.appendChild).toHaveBeenCalledTimes(1));
}

describe('isPdf', () => {
  it('takes %PDF- and nothing else, as a buffer or as bytes', () => {
    expect(isPdf(enc(PDF_TEXT).buffer as ArrayBuffer)).toBe(true);
    expect(isPdf(enc(PDF_TEXT))).toBe(true);
    expect(isPdf(enc('<html>').buffer as ArrayBuffer)).toBe(false);
    expect(isPdf(new ArrayBuffer(3))).toBe(false);
    expect(isPdf(enc('%PDF'))).toBe(false);
  });
});

describe('collectPdf', () => {
  /** Chunks as an app's stream hands them, counting what was read and whether it was let go of. */
  function source(chunks: Uint8Array[], endless?: Uint8Array) {
    const seen = { read: 0, closed: false };
    async function* gen() {
      try {
        for (const c of chunks) {
          seen.read++;
          yield c;
        }
        while (endless) {
          seen.read++;
          yield endless;
        }
      } finally {
        seen.closed = true;
      }
    }
    return { it: gen(), seen };
  }

  it('refuses a stream at its first chunk when it does not start like a PDF, and reads no further', async () => {
    const s = source([enc('<html><body>not a pdf')], new Uint8Array(1 << 20));
    await expect(collectPdf(s.it, 64 << 20)).rejects.toMatchObject({ code: 'invalid' });
    expect(s.seen.read, 'only the first chunk').toBe(1);
    expect(s.seen.closed, 'the stream is let go of').toBe(true);
  });

  it('reads the head across chunks', async () => {
    const s = source([enc('%P'), enc('DF'), enc('-1.7\n'), enc('%%EOF\n')]);
    const b = await collectPdf(s.it, 64 << 20);
    expect(b.type).toBe('application/pdf');
    expect(new TextDecoder().decode(await b.arrayBuffer())).toBe('%PDF-1.7\n%%EOF\n');

    await expect(collectPdf(source([enc('%P'), enc('NG..')]).it, 64 << 20)).rejects.toMatchObject({ code: 'invalid' });
    await expect(collectPdf(source([enc('%PD')]).it, 64 << 20), 'too short to be one').rejects.toMatchObject({ code: 'invalid' });
  });

  it('stops at the limit and reads nothing past it', async () => {
    const mib = 1 << 20;
    const s = source([enc(PDF_TEXT)], new Uint8Array(mib));
    await expect(collectPdf(s.it, 4 * mib)).rejects.toMatchObject({ code: 'too_large' });
    expect(s.seen.read).toBeLessThanOrEqual(5);
    expect(s.seen.closed).toBe(true);
  });

  it('holds one Blob: loose chunks are folded in as they come, never copied whole a second time', async () => {
    const Real = globalThis.Blob;
    let largestLoose = 0;
    class Counting extends Real {
      constructor(parts?: BlobPart[], opts?: BlobPropertyBag) {
        super(parts, opts);
        const loose = (parts ?? []).reduce((n, p) => n + (p instanceof Real ? 0 : typeof p === 'string' ? p.length : (p as ArrayBufferView).byteLength), 0);
        largestLoose = Math.max(largestLoose, loose);
      }
    }
    vi.stubGlobal('Blob', Counting);
    try {
      const chunk = new Uint8Array(1 << 20);
      chunk.set(enc('%PDF-'));
      const b = await collectPdf(source(Array.from({ length: 10 }, () => chunk)).it, 64 << 20);
      expect(b.size).toBe(10 << 20);
      expect(largestLoose, 'a Blob is never built from more loose bytes than one batch').toBeLessThanOrEqual(PRINT_FOLD_BYTES + chunk.byteLength);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it('takes an ArrayBuffer whole: a PDF within the limit only', async () => {
    const b = await collectPdf(enc(PDF_TEXT).buffer as ArrayBuffer, 64 << 20);
    expect(b.size).toBe(PDF_TEXT.length);
    expect(b.type).toBe('application/pdf');
    await expect(collectPdf(enc('<html>').buffer as ArrayBuffer, 64 << 20)).rejects.toMatchObject({ code: 'invalid' });
    await expect(collectPdf(enc(PDF_TEXT).buffer as ArrayBuffer, 4)).rejects.toMatchObject({ code: 'too_large' });
  });
});

describe('lookOf', () => {
  it('reads only the button properties the print page takes', () => {
    const b = document.createElement('button');
    b.style.color = 'rgb(255, 255, 255)';
    b.style.backgroundColor = 'rgb(37, 99, 235)';
    document.body.appendChild(b);
    const look = lookOf(b);
    b.remove();
    for (const k of Object.keys(look)) expect(PRINT_LOOK as readonly string[]).toContain(k);
    expect(look.color).toBe('rgb(255, 255, 255)');
    expect(lookOf(null)).toEqual({});
  });
});

describe('printPdf', () => {
  it('refuses bytes that are not a PDF before it draws anything', async () => {
    const r = rig();
    await expect(printPdf('https://files.example.com/_print/', new Blob([new Uint8Array(8)]), { win: r.win })).rejects.toBeInstanceOf(PrintFailure);
    expect(r.mount.appendChild).not.toHaveBeenCalled();
  });

  it('posts the PDF only after the frame it drew is ready, to its origin, as the Blob itself', async () => {
    const r = rig();
    const pdf = PDF();
    const done = printPdf('https://files.example.com/_print/', pdf, { win: r.win, label: 'Allow', look: { color: 'rgb(255, 255, 255)' } });
    await drawn(r);
    expect(r.attrs.src).toBe('https://files.example.com/_print/');
    expect(r.attrs.referrerpolicy).toBe('no-referrer');
    expect(r.attrs.sandbox).toBeUndefined();
    expect(r.attrs['aria-hidden'], 'hidden until the page asks').toBe('true');

    // Another window says ready: nothing is sent.
    r.fire({ type: 'filex:print-ready' }, {});
    // The frame, from another origin: nothing either.
    r.fire({ type: 'filex:print-ready' }, r.frameWin, 'https://evil.example');
    expect(r.posted).toHaveLength(0);

    r.fire({ type: 'filex:print-ready' }, r.frameWin);
    expect(r.posted).toHaveLength(1);
    expect(r.posted[0].origin).toBe('https://files.example.com');
    expect(r.posted[0].data.type).toBe('filex:print');
    expect(r.posted[0].data.pdf, 'the one Blob, not a copy').toBe(pdf);
    expect(r.posted[0].data.label).toBe('Allow');
    expect(r.posted[0].data.look).toEqual({ color: 'rgb(255, 255, 255)' });
    const { port } = r.pagePort();

    // A second "ready" sends nothing more.
    r.fire({ type: 'filex:print-ready' }, r.frameWin);
    expect(r.posted).toHaveLength(1);

    port.postMessage({ ok: true });
    await expect(done).resolves.toBe(r.frame);
    expect(r.frame.removed).toBe(false);
    expect(r.listeners.size).toBe(0);
  });

  it('shows the frame as the page’s button when it asks, and arms it a moment later', async () => {
    const r = rig();
    const asked: Array<{ width: number; height: number }> = [];
    const done = printPdf('https://files.example.com/_print/', PDF(), { win: r.win, armMs: 400, onAsk: (s) => asked.push(s) });
    await drawn(r);
    r.fire({ type: 'filex:print-ready' }, r.frameWin);
    const { port, told } = r.pagePort();

    port.postMessage({ state: 'ask', width: 92, height: 34 });
    await vi.waitFor(() => expect(asked).toEqual([{ width: 92, height: 34 }]), { interval: 5 });
    expect(r.frame.style.cssText).toContain('width:92px');
    expect(r.frame.style.cssText).toContain('height:34px');
    expect(r.frame.style.cssText).not.toContain('opacity:0');
    expect(r.attrs['aria-hidden']).toBeUndefined();
    expect(r.attrs.tabindex, 'the person can reach the button').toBeUndefined();
    expect(told, 'not armed at once: a click the app invited must not land on it').toEqual([]);
    await vi.waitFor(() => expect(told).toEqual([{ type: 'arm' }]), { timeout: 3000 });

    // A page asking twice, or a size out of all proportion, changes nothing.
    port.postMessage({ state: 'ask', width: 5000, height: 5000 });
    port.postMessage({ ok: true });
    await expect(done).resolves.toBe(r.frame);
    expect(asked).toHaveLength(1);
    expect(r.frame.style.cssText, 'hidden again, kept for the print dialog').toContain('opacity:0');
  });

  it('rejects and takes the frame away when the page could not print', async () => {
    const r = rig();
    const done = printPdf('https://files.example.com/_print/', PDF(), { win: r.win });
    await drawn(r);
    r.fire({ type: 'filex:print-ready' }, r.frameWin);
    const { port } = r.pagePort();
    port.postMessage({ ok: false, code: 'failed' });
    await expect(done).rejects.toMatchObject({ code: 'failed' });
    expect(r.frame.removed).toBe(true);
  });

  it('"Don’t allow" tells the page and is cancelled', async () => {
    const r = rig();
    const no = new AbortController();
    const done = printPdf('https://files.example.com/_print/', PDF(), { win: r.win, signal: no.signal, armMs: 10_000 });
    await drawn(r);
    r.fire({ type: 'filex:print-ready' }, r.frameWin);
    const { port, told } = r.pagePort();
    port.postMessage({ state: 'ask', width: 90, height: 34 });
    await vi.waitFor(() => expect(r.frame.style.cssText).toContain('width:90px'));
    no.abort();
    await expect(done).rejects.toMatchObject({ code: 'cancelled' });
    expect(r.frame.removed).toBe(true);
    await vi.waitFor(() => expect(told).toContainEqual({ type: 'cancel' }));

    const r2 = rig();
    const already = new AbortController();
    already.abort();
    await expect(printPdf('https://files.example.com/_print/', PDF(), { win: r2.win, signal: already.signal })).rejects.toMatchObject({ code: 'cancelled' });
    expect(r2.mount.appendChild).not.toHaveBeenCalled();
  });

  it('is unavailable, not a hang, when the page never says it is ready', async () => {
    const r = rig();
    const done = printPdf('https://files.example.com/_print/', PDF(), { win: r.win, readyMs: 20 });
    await expect(done).rejects.toMatchObject({ code: 'unavailable' });
    expect(r.frame.removed).toBe(true);
    expect(r.posted).toHaveLength(0);
  });

  it('is unavailable soon after a load that never said ready (a site the page may not be framed by)', async () => {
    const r = rig();
    const done = printPdf('https://files.example.com/_print/', PDF(), { win: r.win, readyMs: 60_000, loadGraceMs: 20 });
    await drawn(r);
    r.loaded();
    await expect(done).rejects.toMatchObject({ code: 'unavailable' });
    expect(r.frame.removed).toBe(true);
  });
});
