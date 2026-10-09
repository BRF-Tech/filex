// The print page's own script (backend/internal/printframe/print.js, served at
// `/_print/`; `ui.print`, task #189; security review sec055 S9), run against
// stand-ins for its window, document, navigator and URL.
//
//  - it says it is ready to its parent and listens to nothing else;
//  - it frames the bytes as a PDF (always `application/pdf`) and draws ONE
//    button in the parent's words and look, and says so (`state: 'ask'`);
//  - it prints ONLY on the person's own click on that button: trusted, after
//    the parent armed it, while the browser reports the person's activation
//    on the page - never because the PDF loaded, never on a script's click
//    (red before S9: the page called print() as soon as the PDF's frame
//    loaded, whoever had framed it);
//  - "cancel" drops it all.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it, vi } from 'vitest';

const PRINT_JS = readFileSync(path.resolve(__dirname, '../../../backend/internal/printframe/print.js'), 'utf8');

interface FakeEl {
  tag: string;
  attrs: Record<string, string>;
  style: { props: Record<string, string>; setProperty(k: string, v: string): void };
  disabled: boolean;
  textContent: string;
  type: string;
  dir: string;
  className: string;
  src: string;
  parentNode: unknown;
  contentWindow?: { focus: ReturnType<typeof vi.fn>; print: ReturnType<typeof vi.fn> };
  setAttribute(k: string, v: string): void;
  addEventListener(t: string, fn: (ev: unknown) => void): void;
  fire(t: string, ev?: unknown): void;
  getBoundingClientRect(): { width: number; height: number };
}

function element(tag: string): FakeEl {
  const listeners: Record<string, Array<(ev: unknown) => void>> = {};
  return {
    tag,
    attrs: {},
    style: {
      props: {},
      setProperty(k: string, v: string) {
        this.props[k] = v;
      },
    },
    disabled: false,
    textContent: '',
    type: '',
    dir: '',
    className: '',
    src: '',
    parentNode: null,
    contentWindow: tag === 'iframe' ? { focus: vi.fn(), print: vi.fn() } : undefined,
    setAttribute(k: string, v: string) {
      this.attrs[k] = String(v);
    },
    addEventListener(t: string, fn: (ev: unknown) => void) {
      (listeners[t] ??= []).push(fn);
    },
    fire(t: string, ev: unknown = {}) {
      for (const fn of listeners[t] ?? []) fn(ev);
    },
    getBoundingClientRect: () => ({ width: 80, height: 28 }),
  };
}

/** Run print.js; `deliver` hands it a message as its window would. */
function runPage() {
  const toParent: Array<{ data: unknown; origin: string }> = [];
  const parent = { postMessage: (data: unknown, origin: string) => toParent.push({ data, origin }) };
  const listeners: Array<(ev: unknown) => void> = [];
  const win = {
    parent,
    addEventListener: (t: string, fn: (ev: unknown) => void) => {
      if (t === 'message') listeners.push(fn);
    },
  };
  const made: FakeEl[] = [];
  const body = {
    children: [] as FakeEl[],
    appendChild(c: FakeEl) {
      c.parentNode = body;
      body.children.push(c);
    },
    removeChild(c: FakeEl) {
      body.children = body.children.filter((x) => x !== c);
      c.parentNode = null;
    },
  };
  const doc = {
    body,
    createElement: (t: string) => {
      const e = element(t);
      made.push(e);
      return e;
    },
  };
  const nav = { userActivation: { isActive: false } };
  const urls: Blob[] = [];
  const url = {
    createObjectURL: vi.fn((b: Blob) => {
      urls.push(b);
      return 'blob:https://files.example.com/0b7c';
    }),
    revokeObjectURL: vi.fn(),
  };
  // eslint-disable-next-line @typescript-eslint/no-implied-eval
  new Function('window', 'document', 'navigator', 'URL', 'Blob', PRINT_JS)(win, doc, nav, url, Blob);

  const port = {
    posted: [] as any[],
    onmessage: null as null | ((m: { data: unknown }) => void),
    closed: false,
    postMessage(m: unknown) {
      this.posted.push(m);
    },
    close() {
      this.closed = true;
    },
  };
  const deliver = (ev: { source: unknown; data: unknown; ports?: unknown[] }) => listeners.forEach((fn) => fn(ev));
  const print = (pdf: unknown, over: Record<string, unknown> = {}) =>
    deliver({ source: parent, data: { type: 'filex:print', pdf, label: 'Allow', look: {}, ...over }, ports: [port] });
  const tell = (data: unknown) => port.onmessage?.({ data });
  const button = () => made.find((e) => e.tag === 'button');
  const frame = () => made.find((e) => e.tag === 'iframe');
  const click = (trusted: boolean) => button()!.fire('click', { isTrusted: trusted });
  return { parent, toParent, nav, url, urls, made, body, port, deliver, print, tell, button, frame, click };
}

const PDF = () => new Blob([new TextEncoder().encode('%PDF-1.7\n%%EOF\n')], { type: 'application/pdf' });
const asked = async (p: ReturnType<typeof runPage>) => {
  await vi.waitFor(() => expect(p.port.posted.length).toBeGreaterThan(0));
  return p.port.posted[0];
};

describe('the print page (print.js)', () => {
  it('says it is ready to its parent, and takes a print from its parent only', async () => {
    const p = runPage();
    expect(p.toParent).toEqual([{ data: { type: 'filex:print-ready' }, origin: '*' }]);
    p.deliver({ source: {}, data: { type: 'filex:print', pdf: PDF() }, ports: [p.port] });
    p.deliver({ source: p.parent, data: { type: 'filex:print', pdf: PDF() }, ports: [] });
    p.deliver({ source: p.parent, data: { type: 'other', pdf: PDF() }, ports: [p.port] });
    await new Promise((r) => setTimeout(r, 20));
    expect(p.made).toEqual([]);
    expect(p.port.posted).toEqual([]);
  });

  it('frames the bytes as a PDF, whatever type they came with, and asks with one button', async () => {
    const p = runPage();
    p.print(new Blob([new TextEncoder().encode('%PDF-1.7\n')], { type: 'text/html' }), {
      label: 'İzin ver',
      look: {
        'background-color': 'rgb(37, 99, 235)',
        color: 'rgb(255, 255, 255)',
        'font-family': '"Inter", system-ui, sans-serif',
        position: 'fixed',
        'padding-top': 'url(https://evil.example/x)',
        'border-top-color': 'red; display: none',
      },
    });
    expect(await asked(p)).toEqual({ state: 'ask', width: 86, height: 34 });
    expect(p.urls).toHaveLength(1);
    expect(p.urls[0].type, 'always served as a PDF').toBe('application/pdf');
    expect(p.frame()!.src).toBe('blob:https://files.example.com/0b7c');
    const b = p.button()!;
    expect(b.textContent).toBe('İzin ver');
    expect(b.disabled, 'asleep until the parent arms it').toBe(true);
    expect(b.style.props).toEqual({
      'background-color': 'rgb(37, 99, 235)',
      'outline-color': 'rgb(37, 99, 235)',
      color: 'rgb(255, 255, 255)',
      'font-family': '"Inter", system-ui, sans-serif',
    });
  });

  it('refuses what is not a PDF and draws nothing', async () => {
    const p = runPage();
    p.print(new Blob(['<html><script>x</script></html>'], { type: 'application/pdf' }));
    expect(await asked(p)).toEqual({ ok: false, code: 'invalid' });
    expect(p.made).toEqual([]);
    expect(p.url.createObjectURL).not.toHaveBeenCalled();
  });

  it('prints nothing by itself: not when the PDF loads, not with the person active', async () => {
    const p = runPage();
    p.print(PDF());
    await asked(p);
    p.nav.userActivation.isActive = true;
    p.tell({ type: 'arm' });
    p.frame()!.fire('load');
    await new Promise((r) => setTimeout(r, 150));
    expect(p.frame()!.contentWindow!.print).not.toHaveBeenCalled();
    expect(p.port.posted).toHaveLength(1);
  });

  it('prints only on the person’s own click: armed, trusted, with their activation on the page', async () => {
    const p = runPage();
    p.print(PDF());
    await asked(p);
    p.frame()!.fire('load');
    const printFn = p.frame()!.contentWindow!.print;

    p.nav.userActivation.isActive = true;
    p.click(true);
    expect(printFn, 'before the parent armed it').not.toHaveBeenCalled();

    p.tell({ type: 'arm' });
    expect(p.button()!.disabled).toBe(false);
    p.click(false);
    expect(printFn, 'a script’s click').not.toHaveBeenCalled();

    p.nav.userActivation.isActive = false;
    p.click(true);
    expect(printFn, 'no activation of the person’s on the page').not.toHaveBeenCalled();

    p.nav.userActivation.isActive = true;
    p.click(true);
    expect(printFn).toHaveBeenCalledTimes(1);
    expect(p.port.posted.at(-1)).toEqual({ ok: true });
    expect(p.button()!.disabled).toBe(true);

    p.click(true);
    expect(printFn, 'once').toHaveBeenCalledTimes(1);
  });

  it('a click before the PDF loaded prints once it has', async () => {
    const p = runPage();
    p.print(PDF());
    await asked(p);
    p.tell({ type: 'arm' });
    p.nav.userActivation.isActive = true;
    p.click(true);
    const printFn = p.frame()!.contentWindow!.print;
    expect(printFn).not.toHaveBeenCalled();
    p.frame()!.fire('load');
    await vi.waitFor(() => expect(printFn).toHaveBeenCalledTimes(1));
    expect(p.port.posted.at(-1)).toEqual({ ok: true });
  });

  it('"cancel" drops the PDF, its frame and the button', async () => {
    const p = runPage();
    p.print(PDF());
    await asked(p);
    const f = p.frame()!;
    p.tell({ type: 'cancel' });
    expect(p.body.children).toEqual([]);
    expect(p.url.revokeObjectURL).toHaveBeenCalledWith('blob:https://files.example.com/0b7c');
    expect(p.port.closed).toBe(true);
    f.fire('load');
    p.nav.userActivation.isActive = true;
    p.click(true);
    expect(f.contentWindow!.print).not.toHaveBeenCalled();
  });
});
