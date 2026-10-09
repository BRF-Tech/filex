/**
 * printPdf — print a PDF from filex's own print page (`ui.print`, task #189).
 *
 * An app's interface runs in a frame sandboxed with `allow-scripts` only,
 * where the browser's print dialog may not open (measured 2026-10-08:
 * Chromium ignores `print()` there and says so, Firefox ignores it). So
 * AppFrame takes the PDF over the bridge and prints it here.
 *
 * ⚠ Not in a frame of filex's own page: filex's pages never frame a `blob:`
 * or `data:` document (backend internal/secheaders - `frame-src` is the wall
 * around an app interface's own navigation). The server's print page
 * (`<root>/_print/`, backend internal/printframe) may: it holds no app code,
 * frames only the `blob:` PDF it makes from the bytes posted to it, and calls
 * that frame's `print()`.
 *
 * ⚠⚠ Only on the person's click IN the print page (security review sec055
 * S9). A click on a button of filex's own page is the person's activation on
 * THAT page, and a browser shares it only with frames of the same origin (not
 * in every engine) - never where the explorer is embedded in another site or
 * runs in the desktop app (`app://filex`). So the print page draws the
 * question row's Allow button itself, and the frame drawn here IS that
 * button. The exchange, with the frame this function drew and nothing else:
 *
 *  1. the page posts `{type: 'filex:print-ready'}` to its parent - taken only
 *     when `event.source` is that frame's window and from the page's origin;
 *  2. we post `{type: 'filex:print', pdf, label, look}` to the page's origin
 *     with a MessagePort: the PDF as a Blob (cloned by reference, never
 *     copied), the button's words and the look of filex's primary button;
 *  3. the page checks the first bytes, frames the PDF and answers
 *     `{state: 'ask', width, height}`: the frame is shown at that size
 *     (`onAsk` - the caller shows the row around it) and, `armMs` later, told
 *     `{type: 'arm'}` - the button wakes only once the question has been on
 *     screen a moment, so a click the app invited cannot land on it;
 *  4. the person's click on it prints, and the page answers `{ok: true}`, or
 *     `{ok: false, code}`. `signal` (the person's "Don't allow") tells the
 *     page `{type: 'cancel'}` and rejects `cancelled`.
 *
 * A host the print page may not be framed by (a site not in the server's
 * FILEX_FRAME_ANCESTORS) shows the browser's refusal in the frame, which
 * never says it is ready: `unavailable`, soon after it loaded.
 *
 * The frame stays until the caller removes it (the next print, the app's
 * frame going away): a browser may still be reading the PDF for the dialog
 * after `print()` returned.
 */

/** How long the print page has to say it is ready. */
export const PRINT_READY_MS = 10_000;
/** A print page that loaded without saying it is ready has been refused (its
 *  framing policy): `unavailable` this long after its load. */
export const PRINT_LOAD_GRACE_MS = 1_500;
/** How long the whole exchange may take (the person deciding, a print dialog open). */
export const PRINT_ANSWER_MS = 15 * 60_000;
/** The print page's button wakes this long after the question shows. */
export const PRINT_ARM_MS = 600;
/** Chunks are folded into the Blob each time this much is held loose. */
export const PRINT_FOLD_BYTES = 4 << 20;

const PDF_MIME = 'application/pdf';
/** `%PDF-` */
const PDF_HEAD = [0x25, 0x50, 0x44, 0x46, 0x2d];

/** What of filex's primary button the print page's button takes. */
export const PRINT_LOOK = [
  'background-color',
  'color',
  'border-top-color',
  'border-right-color',
  'border-bottom-color',
  'border-left-color',
  'border-top-width',
  'border-right-width',
  'border-bottom-width',
  'border-left-width',
  'border-top-style',
  'border-right-style',
  'border-bottom-style',
  'border-left-style',
  'border-top-left-radius',
  'border-top-right-radius',
  'border-bottom-right-radius',
  'border-bottom-left-radius',
  'font-family',
  'font-size',
  'font-weight',
  'line-height',
  'letter-spacing',
  'padding-top',
  'padding-right',
  'padding-bottom',
  'padding-left',
] as const;

/** The bytes start like a PDF (`%PDF-`). */
export function isPdf(bytes: ArrayBuffer | Uint8Array): boolean {
  const u = bytes instanceof Uint8Array ? bytes : bytes instanceof ArrayBuffer ? new Uint8Array(bytes) : null;
  if (!u || u.byteLength < PDF_HEAD.length) return false;
  return PDF_HEAD.every((b, i) => u[i] === b);
}

export type PrintFailureCode = 'invalid' | 'failed' | 'unavailable' | 'cancelled' | 'too_large';

export class PrintFailure extends Error {
  readonly code: PrintFailureCode;
  constructor(code: PrintFailureCode, message?: string) {
    super(message || code);
    this.name = 'PrintFailure';
    this.code = code;
  }
}

function notPdf(): PrintFailure {
  return new PrintFailure('invalid', 'the data is not a PDF');
}

/** The first PDF_HEAD.length bytes seen so far, with what `chunk` adds. */
function joinHead(head: Uint8Array, chunk: Uint8Array): Uint8Array {
  const take = Math.min(PDF_HEAD.length - head.byteLength, chunk.byteLength);
  if (take <= 0) return head;
  const out = new Uint8Array(head.byteLength + take);
  out.set(head);
  out.set(chunk.subarray(0, take), head.byteLength);
  return out;
}

/**
 * The PDF as ONE Blob (security review sec055 S10), never past `max` bytes:
 *
 *  - the first bytes are checked as they arrive: a stream that does not start
 *    `%PDF-` is refused (`invalid`) at its first chunk, not read to its end -
 *    and let go of: leaving the loop early cancels an async generator's
 *    stream (AppFrame chunksOf);
 *  - past `max`, `too_large` at once, and nothing more is read;
 *  - chunks are folded into the Blob every PRINT_FOLD_BYTES, so what is held
 *    is the Blob and one batch, never a second whole copy. A Blob built from
 *    a Blob keeps a reference to its bytes, and it crosses `postMessage`
 *    the same way: the print page gets this one.
 */
export async function collectPdf(source: ArrayBuffer | AsyncIterable<Uint8Array>, max: number): Promise<Blob> {
  const tooLarge = () => new PrintFailure('too_large', `at most ${Math.floor(max / (1 << 20))} MiB`);
  if (source instanceof ArrayBuffer) {
    if (source.byteLength > max) throw tooLarge();
    if (!isPdf(source)) throw notPdf();
    return new Blob([source], { type: PDF_MIME });
  }
  let blob: Blob | null = null;
  let batch: Uint8Array[] = [];
  let loose = 0;
  let size = 0;
  let head: Uint8Array = new Uint8Array(0);
  for await (const chunk of source) {
    if (!(chunk instanceof Uint8Array)) throw new PrintFailure('invalid', 'a PDF stream carries bytes (Uint8Array)');
    if (head.byteLength < PDF_HEAD.length) {
      head = joinHead(head, chunk);
      if (head.byteLength >= PDF_HEAD.length && !isPdf(head)) throw notPdf();
    }
    size += chunk.byteLength;
    if (size > max) throw tooLarge();
    batch.push(chunk);
    loose += chunk.byteLength;
    if (loose >= PRINT_FOLD_BYTES) {
      blob = foldInto(blob, batch);
      batch = [];
      loose = 0;
    }
  }
  if (!isPdf(head)) throw notPdf();
  return batch.length || !blob ? foldInto(blob, batch) : blob;
}

/** The Blob so far and the loose chunks, as one Blob (the bytes it holds are
 *  referenced, not copied again). */
function foldInto(blob: Blob | null, batch: Uint8Array[]): Blob {
  return new Blob((blob ? [blob, ...batch] : batch) as BlobPart[], { type: PDF_MIME });
}

/** filex's primary button, as the print page's button should look: the
 *  computed values of PRINT_LOOK on `el`. */
export function lookOf(el: Element | null | undefined): Record<string, string> {
  const out: Record<string, string> = {};
  const view = el?.ownerDocument?.defaultView;
  if (!el || !view || typeof view.getComputedStyle !== 'function') return out;
  const cs = view.getComputedStyle(el);
  for (const k of PRINT_LOOK) {
    const v = cs.getPropertyValue(k).trim();
    if (v) out[k] = v;
  }
  return out;
}

async function startsLikePdf(pdf: Blob): Promise<boolean> {
  if (typeof Blob === 'undefined' || !(pdf instanceof Blob) || pdf.size < PDF_HEAD.length) return false;
  try {
    return isPdf(await pdf.slice(0, PDF_HEAD.length).arrayBuffer());
  } catch {
    return false;
  }
}

export interface PrintPdfOptions {
  /** Where the frame goes (default: the document's body). */
  mount?: HTMLElement | null;
  win?: Window;
  readyMs?: number;
  loadGraceMs?: number;
  answerMs?: number;
  /** The button's words - filex's "Allow". */
  label?: string;
  /** The button's look (lookOf filex's primary button). */
  look?: Record<string, string>;
  /** How long after the question shows the button wakes. */
  armMs?: number;
  /** The person's "Don't allow": the page is told, and it is `cancelled`. */
  signal?: AbortSignal;
  /** The page shows its button, at this size: show the question around it. */
  onAsk?: (size: { width: number; height: number }) => void;
}

/** Laid out (a browser draws nothing into a frame it does not render, and a
 *  PDF it does not draw it may not print) but not seen. */
const HIDDEN = 'position:absolute;width:1px;height:1px;border:0;opacity:0;pointer-events:none;inset-inline-start:-10000px;top:0';

function shown(width: number, height: number): string {
  return `display:block;width:${width}px;height:${height}px;border:0;background:transparent;color-scheme:normal`;
}

function clamp(v: unknown, lo: number, hi: number, fallback: number): number {
  const n = typeof v === 'number' && Number.isFinite(v) ? Math.round(v) : fallback;
  return Math.min(hi, Math.max(lo, n));
}

/**
 * Print `pdf` from the print page at `url`, on the person's click on the
 * page's own button. Resolves with the frame once the page called `print()`
 * (the caller removes it later); rejects with a PrintFailure and takes the
 * frame away.
 */
export async function printPdf(url: string, pdf: Blob, opts: PrintPdfOptions = {}): Promise<HTMLIFrameElement> {
  const win = opts.win ?? window;
  const doc = win.document;
  if (opts.signal?.aborted) throw new PrintFailure('cancelled', 'the person said no');
  if (!(await startsLikePdf(pdf))) throw new PrintFailure('invalid', 'not a PDF');
  let origin: string;
  try {
    origin = new URL(url, win.location.href).origin;
  } catch {
    throw new PrintFailure('unavailable', 'no print page');
  }
  if (!origin || origin === 'null') throw new PrintFailure('unavailable', 'no print page');
  if (opts.signal?.aborted) throw new PrintFailure('cancelled', 'the person said no');

  const frame = doc.createElement('iframe');
  frame.setAttribute('referrerpolicy', 'no-referrer');
  frame.setAttribute('aria-hidden', 'true');
  frame.setAttribute('tabindex', '-1');
  frame.setAttribute('title', opts.label || 'print');
  frame.dataset.testid = 'appframe-print';
  frame.style.cssText = HIDDEN;
  frame.setAttribute('src', url);

  return new Promise<HTMLIFrameElement>((resolve, reject) => {
    let settled = false;
    let asked = false;
    let readyTimer: ReturnType<typeof setTimeout> | null = null;
    let graceTimer: ReturnType<typeof setTimeout> | null = null;
    let answerTimer: ReturnType<typeof setTimeout> | null = null;
    let armTimer: ReturnType<typeof setTimeout> | null = null;
    let channel: MessageChannel | null = null;

    function hide() {
      frame.style.cssText = HIDDEN;
      frame.setAttribute('aria-hidden', 'true');
      frame.setAttribute('tabindex', '-1');
    }

    function finish(err: PrintFailure | null) {
      if (settled) return;
      settled = true;
      win.removeEventListener('message', onMessage);
      frame.removeEventListener('load', onLoad);
      opts.signal?.removeEventListener('abort', onAbort);
      for (const t of [readyTimer, graceTimer, answerTimer, armTimer]) if (t) clearTimeout(t);
      if (channel) channel.port1.onmessage = null;
      if (err) {
        frame.remove();
        reject(err);
      } else {
        hide();
        resolve(frame);
      }
    }

    function onAbort() {
      try {
        channel?.port1.postMessage({ type: 'cancel' });
      } catch {
        /* the page is gone already */
      }
      finish(new PrintFailure('cancelled', 'the person said no'));
    }

    function onLoad() {
      if (settled || channel || graceTimer) return;
      graceTimer = setTimeout(() => {
        if (!channel) finish(new PrintFailure('unavailable', 'the print page may not be shown here'));
      }, opts.loadGraceMs ?? PRINT_LOAD_GRACE_MS);
    }

    function onAnswer(a: MessageEvent) {
      const r = a.data as { ok?: unknown; code?: unknown; state?: unknown; width?: unknown; height?: unknown } | null;
      if (settled || !r || typeof r !== 'object') return;
      if (r.state === 'ask') {
        if (asked) return;
        asked = true;
        const width = clamp(r.width, 24, 480, 96);
        const height = clamp(r.height, 16, 96, 36);
        frame.style.cssText = shown(width, height);
        frame.removeAttribute('aria-hidden');
        frame.removeAttribute('tabindex');
        opts.onAsk?.({ width, height });
        armTimer = setTimeout(() => {
          try {
            channel?.port1.postMessage({ type: 'arm' });
          } catch {
            /* the page is gone: the answer timer ends it */
          }
        }, opts.armMs ?? PRINT_ARM_MS);
        return;
      }
      if (r.ok === true) finish(null);
      else finish(new PrintFailure(r.code === 'invalid' ? 'invalid' : 'failed', 'the PDF could not be printed'));
    }

    function onMessage(ev: MessageEvent) {
      // ⚠ The SOURCE decides: only the frame drawn here, and only its
      // readiness; never anything a payload claims.
      const cw = frame.contentWindow;
      if (settled || !cw || ev.source !== cw) return;
      const d = ev.data as { type?: unknown } | null;
      if (!d || typeof d !== 'object' || d.type !== 'filex:print-ready' || channel) return;
      if (ev.origin !== origin) return;
      if (readyTimer) clearTimeout(readyTimer);
      if (graceTimer) clearTimeout(graceTimer);
      const ch = new MessageChannel();
      channel = ch;
      ch.port1.onmessage = onAnswer;
      answerTimer = setTimeout(() => finish(new PrintFailure('failed', 'the print page did not answer')), opts.answerMs ?? PRINT_ANSWER_MS);
      try {
        cw.postMessage({ type: 'filex:print', pdf, label: opts.label ?? '', look: opts.look ?? {} }, origin, [ch.port2]);
      } catch {
        finish(new PrintFailure('failed', 'the PDF could not be handed to the print page'));
      }
    }

    win.addEventListener('message', onMessage);
    frame.addEventListener('load', onLoad);
    opts.signal?.addEventListener('abort', onAbort);
    readyTimer = setTimeout(() => finish(new PrintFailure('unavailable', 'the print page did not load')), opts.readyMs ?? PRINT_READY_MS);
    (opts.mount ?? doc.body).appendChild(frame);
  });
}
