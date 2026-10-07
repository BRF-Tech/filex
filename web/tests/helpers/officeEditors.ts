/**
 * The ONLYOFFICE editor, as the viewer meets it, both ways it can run
 * (task #92):
 *
 *  - `in-page`: no origin of their own for app interfaces; api.js is loaded
 *    into the page. The stand-in does to the page what api.js does: the mount
 *    is replaced by an iframe named frameEditor, and destroyEditor() swaps the
 *    iframe for a fresh placeholder with the mount's id (api.js
 *    _destroyEditor).
 *  - `framed`: the server names a frame on the interface origin
 *    (FILEX_APP_UI_ORIGIN, the config answer's `frame`). The stand-in is the
 *    page there (backend/internal/onlyoffice/frame/frame.js): when the viewer
 *    puts its frame in the document it says hello from that frame's window,
 *    takes the config and the port the viewer answers with, and sends the
 *    editor's events over the port.
 *  - `framed-on-ds`: the same page on the document server's own origin
 *    (FILEX_ONLYOFFICE_FRAME_ORIGIN, /filex-frame/editor) - the placement
 *    the maintainers chose for their own install (no new domain).
 *
 * A test written against `OfficeEditors` runs unchanged on both, which is the
 * point: #184's rules (reload when nothing is unsaved, ask otherwise, the
 * answer to the host and the server) hold wherever the editor runs.
 */
import { flushPromises } from '@vue/test-utils';
import { OFFICE_FRAME_PROTOCOL, OFFICE_FRAME_VERSION } from '@brftech/filex-core/src/lib/officeFrame';

export type OfficeMode = 'in-page' | 'framed' | 'framed-on-ds';
export const OFFICE_MODES: OfficeMode[] = ['in-page', 'framed', 'framed-on-ds'];

export const UI_ORIGIN = 'https://apps.usercontent.example';
export const FRAME_URL = `${UI_ORIGIN}/_appui/_onlyoffice/editor`;
/** The document server the tests' configs name, and the frame on its origin. */
export const DS_ORIGIN = 'https://docs.example.com';
export const DS_FRAME_URL = `${DS_ORIGIN}/filex-frame/editor`;

type Config = { document?: { key?: unknown }; events?: Record<string, (e: unknown) => void> } & Record<string, unknown>;

export interface OfficeEditors {
  readonly mode: OfficeMode;
  /** What the config endpoint answers besides `documentServerUrl` and `config`. */
  answer(): Record<string, unknown>;
  /** The key of every editor opened, in order. */
  readonly created: string[];
  /** Editors closed so far. */
  destroyed(): number;
  /** Editors on the page right now. */
  frames(): number;
  /** The config the last editor was given (framed: as it crossed to the frame). */
  lastConfig(): Config | null;
  /** ONLYOFFICE's onDocumentStateChange. */
  state(dirty: boolean): Promise<void>;
  /** ONLYOFFICE's onError / onWarning, its own event shape. */
  problem(channel: 'onError' | 'onWarning', code: number, description: string): Promise<void>;
  /** framed only: a protocol message as frame.js would send it (in-page: nothing). */
  raw(m: Record<string, unknown>): Promise<void>;
  uninstall(): void;
}

const tick = async () => {
  // A port's message lands on a later turn of the event loop than a promise.
  await new Promise((r) => setTimeout(r, 10));
  await flushPromises();
};

/** api.js, in the page. */
export function installInPage(): OfficeEditors {
  const created: string[] = [];
  let destroyed = 0;
  let events: Record<string, (e: unknown) => void> = {};
  let last: Config | null = null;

  class ApiJsLikeEditor {
    private frame: HTMLIFrameElement | null;
    constructor(
      private placeholderId: string,
      cfg: Config,
    ) {
      events = cfg.events ?? {};
      last = cfg;
      created.push(String(cfg.document?.key ?? ''));
      const target = document.getElementById(placeholderId)!;
      this.frame = document.createElement('iframe');
      this.frame.name = 'frameEditor';
      target.parentNode!.replaceChild(this.frame, target);
    }
    destroyEditor() {
      destroyed++;
      const target = document.createElement('div');
      target.id = this.placeholderId;
      this.frame?.parentNode?.replaceChild(target, this.frame);
      this.frame = null;
    }
  }
  (window as unknown as { DocsAPI: unknown }).DocsAPI = { DocEditor: ApiJsLikeEditor };

  return {
    mode: 'in-page',
    answer: () => ({}),
    created,
    destroyed: () => destroyed,
    frames: () => document.querySelectorAll('iframe[name^="frameEditor"]').length,
    lastConfig: () => last,
    async state(dirty) {
      events.onDocumentStateChange?.({ data: dirty });
      await flushPromises();
    },
    async problem(channel, code, description) {
      events[channel]?.({ data: { errorCode: code, errorDescription: description } });
      await flushPromises();
    },
    async raw() {
      await flushPromises();
    },
    uninstall() {
      delete (window as unknown as { DocsAPI?: unknown }).DocsAPI;
    },
  };
}

/** The page on another origin (`frameUrl`'s), and api.js in it. */
export function installFramed(frameUrl: string = FRAME_URL): OfficeEditors {
  const frameOrigin = new URL(frameUrl).origin;
  const created: string[] = [];
  const seen: HTMLIFrameElement[] = [];
  let port: MessagePort | null = null;
  let last: Config | null = null;

  const post = (m: Record<string, unknown>) =>
    port?.postMessage({ proto: OFFICE_FRAME_PROTOCOL, v: OFFICE_FRAME_VERSION, ...m });

  function adopt(el: HTMLIFrameElement) {
    if (seen.includes(el)) return;
    seen.push(el);
    const cw = el.contentWindow as Window | null;
    if (!cw) throw new Error('the office frame has no window');
    // What the viewer sends the frame: the config, and the port.
    (cw as unknown as { postMessage: unknown }).postMessage = (data: unknown, _origin: string, transfer?: Transferable[]) => {
      const m = data as { type?: string; config?: Config };
      if (m?.type !== 'open') return;
      last = m.config ?? null;
      created.push(String(m.config?.document?.key ?? ''));
      port = ((transfer ?? [])[0] as MessagePort | undefined) ?? null;
    };
    const session = new URL(el.getAttribute('src') ?? '').hash.slice(1);
    window.dispatchEvent(
      new MessageEvent('message', {
        data: { proto: OFFICE_FRAME_PROTOCOL, v: OFFICE_FRAME_VERSION, type: 'hello', session },
        origin: frameOrigin,
        source: cw,
      }),
    );
  }

  const observer = new MutationObserver((records) => {
    for (const r of records) {
      for (const n of Array.from(r.addedNodes)) {
        if (n instanceof HTMLIFrameElement && n.dataset.testid === 'office-frame') adopt(n);
      }
    }
  });
  observer.observe(document.body, { childList: true, subtree: true });

  return {
    mode: frameUrl === FRAME_URL ? 'framed' : 'framed-on-ds',
    answer: () => ({ frame: frameUrl }),
    created,
    destroyed: () => seen.filter((el) => !el.isConnected).length,
    frames: () => document.querySelectorAll('iframe[data-testid="office-frame"]').length,
    lastConfig: () => last,
    async state(dirty) {
      post({ type: 'state', dirty });
      await tick();
    },
    async problem(channel, code, description) {
      post({ type: 'error', channel, code, description });
      await tick();
    },
    async raw(m) {
      post(m);
      await tick();
    },
    uninstall() {
      observer.disconnect();
    },
  };
}

export function installOfficeEditors(mode: OfficeMode): OfficeEditors {
  if (mode === 'framed') return installFramed(FRAME_URL);
  if (mode === 'framed-on-ds') return installFramed(DS_FRAME_URL);
  return installInPage();
}
