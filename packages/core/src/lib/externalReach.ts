/**
 * externalReach — "can THIS browser reach the external service?"
 *
 * # Why this exists
 *
 * Three machines must reach three addresses before the Office editor works,
 * and until now only one of them was ever checked:
 *
 * | address                | who must reach it                     | who checked it   |
 * | ---------------------- | ------------------------------------- | ---------------- |
 * | the Document Server URL| the **browser** (loads the editor JS) | nobody           |
 * | the Document Server URL| the filex process                     | the Test button  |
 * | `FILEX_PUBLIC_URL`     | the **document server** (fetch + save)| nobody           |
 *
 * So an operator on podman types `http://onlyoffice`, filex reaches it, Test
 * goes green, and the browser cannot resolve that name at all. The editor then
 * fails with the same message as a missing configuration — issue #17, twice.
 *
 * The admin page runs **in the browser that will actually open the editor**,
 * so it can answer the first row directly instead of disclaiming it. That is
 * what this module does.
 *
 * # Mechanism, and why not `fetch`
 *
 * ⚠ A plain `fetch()` is the obvious choice and the wrong one: a document
 * server that works perfectly will usually answer without CORS headers, the
 * promise rejects, and a naive `catch` reports failure for a healthy service.
 *
 * Instead each service is probed **the same way the real viewer loads it**:
 *
 * - **onlyoffice** — a `<script>` pointing at
 *   `<base>/web-apps/apps/api/documents/api.js`. Script `load`/`error` is not
 *   subject to CORS for load detection, and a successful load leaves
 *   `window.DocsAPI.DocEditor` defined, which proves the thing that answered
 *   really is a Document Server. (Verified against a live OnlyOffice Docs
 *   instance: `load` + `DocsAPI` in ~0.5 s.)
 * - **drawio** — a hidden `<iframe>` at `<base>/?embed=1&proto=json`, which
 *   posts `{"event":"init"}` to its parent when the editor is ready. Same
 *   handshake `DrawioViewer.vue` uses. (Verified against a live drawio.)
 *
 * Both distinguish **"could not reach"** from **"reached, wrong thing"** with
 * a second signal: a `no-cors` `fetch`, whose promise resolves for any HTTP
 * status and rejects only on a network-level failure. Reached + no editor =
 * `wrong-content`; not reached at all = `unreachable`.
 *
 * Every probe is bounded by a timeout and reports the timeout as its own
 * state, so a black-holed address stops the spinner instead of hanging.
 */

/** The result of one probe. Each value is a different sentence to the operator. */
export type BrowserProbeState =
  /** Loaded, and it really is the service it claims to be. */
  | 'ok'
  /** Something answered at that address, but it is not this service. */
  | 'wrong-content'
  /** Nothing answered: DNS failure, connection refused, blocked. */
  | 'unreachable'
  /** Nothing answered and nothing refused either — a black hole. */
  | 'timeout'
  /** The admin page is HTTPS and the service URL is HTTP; the browser will refuse. */
  | 'blocked-mixed-content'
  /** No URL configured, or a service with no browser-side entry point to probe. */
  | 'skipped';

export interface BrowserProbeResult {
  service: string;
  state: BrowserProbeState;
  /** The exact URL attempted — the same one the real viewer loads. */
  url: string;
  /** Milliseconds elapsed. Worth showing: a 6 s "unreachable" is a DNS story. */
  ms: number;
  mechanism: 'script' | 'iframe' | 'none';
}

/**
 * Injection seam. The defaults touch the DOM and the network; tests replace
 * them so the classification logic can be asserted without either.
 */
export interface BrowserProbeDeps {
  loadScript(url: string, timeoutMs: number): Promise<'load' | 'error' | 'timeout'>;
  loadFramed(url: string, timeoutMs: number): Promise<'ready' | 'timeout'>;
  reachable(url: string): Promise<boolean>;
  hasDocsAPI(): boolean;
  pageProtocol(): string;
  now(): number;
}

const ONLYOFFICE_API_PATH = '/web-apps/apps/api/documents/api.js';
const DRAWIO_EMBED_QUERY = '/?embed=1&proto=json';

/**
 * Default timeout. ⚠ Measured, not guessed: a browser takes ~5 s to give up on
 * a DNS name that does not exist (`http://onlyoffice`), and that case must
 * report `unreachable`, not `timeout`. 10 s leaves room and still bounds a
 * black hole.
 */
export const DEFAULT_PROBE_TIMEOUT_MS = 10_000;

function trimBase(base: string): string {
  return base.trim().replace(/\/+$/, '');
}

function domLoadScript(url: string, timeoutMs: number): Promise<'load' | 'error' | 'timeout'> {
  return new Promise((resolve) => {
    const el = document.createElement('script');
    let done = false;
    const finish = (r: 'load' | 'error' | 'timeout') => {
      if (done) return;
      done = true;
      clearTimeout(tid);
      el.remove();
      resolve(r);
    };
    const tid = setTimeout(() => finish('timeout'), timeoutMs);
    el.async = true;
    el.src = url;
    el.onload = () => finish('load');
    el.onerror = () => finish('error');
    document.head.appendChild(el);
  });
}

function domLoadFramed(url: string, timeoutMs: number): Promise<'ready' | 'timeout'> {
  return new Promise((resolve) => {
    let origin: string | null = null;
    try {
      origin = new URL(url, window.location.href).origin;
    } catch {
      origin = null;
    }
    const frame = document.createElement('iframe');
    frame.setAttribute('aria-hidden', 'true');
    frame.style.cssText = 'position:absolute;left:-9999px;width:1px;height:1px;border:0';
    let done = false;
    const finish = (r: 'ready' | 'timeout') => {
      if (done) return;
      done = true;
      clearTimeout(tid);
      window.removeEventListener('message', onMessage);
      frame.remove();
      resolve(r);
    };
    const onMessage = (e: MessageEvent) => {
      // ⚠ Origin check first: any page may postMessage to us, and a probe that
      // accepts an unfiltered message would report a healthy drawio because
      // something else on the page said so.
      if (origin && e.origin !== origin) return;
      let data: unknown = e.data;
      if (typeof data === 'string') {
        try {
          data = JSON.parse(data);
        } catch {
          return;
        }
      }
      if (data && typeof data === 'object' && (data as { event?: string }).event === 'init') {
        finish('ready');
      }
    };
    window.addEventListener('message', onMessage);
    const tid = setTimeout(() => finish('timeout'), timeoutMs);
    frame.src = url;
    document.body.appendChild(frame);
  });
}

async function domReachable(url: string): Promise<boolean> {
  try {
    // `no-cors` gives an opaque response we cannot read — which is fine, the
    // only question is whether the request completed at all. It resolves for
    // any HTTP status (including 404) and rejects on a network failure, which
    // is exactly the "reached, wrong thing" vs "could not reach" split.
    await fetch(url, { mode: 'no-cors', cache: 'no-store' });
    return true;
  } catch {
    return false;
  }
}

const domDeps: BrowserProbeDeps = {
  loadScript: domLoadScript,
  loadFramed: domLoadFramed,
  reachable: domReachable,
  hasDocsAPI: () =>
    !!(window as unknown as { DocsAPI?: { DocEditor?: unknown } }).DocsAPI?.DocEditor,
  pageProtocol: () => window.location.protocol,
  now: () => (typeof performance !== 'undefined' ? performance.now() : Date.now()),
};

/** The URL each service is probed at — the same one its viewer loads. */
export function browserProbeURL(service: string, base: string): string {
  const b = trimBase(base);
  if (!b) return '';
  if (service === 'onlyoffice') return b + ONLYOFFICE_API_PATH;
  if (service === 'drawio') return b + DRAWIO_EMBED_QUERY;
  return '';
}

/**
 * Probe one external service from the browser running this page.
 *
 * Returns `skipped` — never a failure — for a service with no URL and for a
 * service filex does not load in the browser at all (the converter), because
 * reporting "unreachable" for something the browser never fetches would be a
 * new lie in place of the old one.
 */
export async function probeExternalFromBrowser(
  service: string,
  base: string | null | undefined,
  opts: { timeoutMs?: number; deps?: Partial<BrowserProbeDeps> } = {},
): Promise<BrowserProbeResult> {
  const deps: BrowserProbeDeps = { ...domDeps, ...(opts.deps ?? {}) };
  const timeoutMs = opts.timeoutMs ?? DEFAULT_PROBE_TIMEOUT_MS;
  const url = browserProbeURL(service, base ?? '');
  const mechanism: BrowserProbeResult['mechanism'] =
    service === 'onlyoffice' ? 'script' : service === 'drawio' ? 'iframe' : 'none';
  if (!url) {
    return { service, state: 'skipped', url: '', ms: 0, mechanism };
  }

  // ⚠ Pre-empt mixed content rather than reporting it as "unreachable". The
  // browser blocks an http:// subresource on an https:// page before a packet
  // leaves, and "unreachable" would send the operator to check firewalls.
  if (deps.pageProtocol() === 'https:' && url.startsWith('http://')) {
    return { service, state: 'blocked-mixed-content', url, ms: 0, mechanism };
  }

  const t0 = deps.now();
  const elapsed = () => Math.round(deps.now() - t0);

  if (mechanism === 'script') {
    const ev = await deps.loadScript(url, timeoutMs);
    if (ev === 'timeout') return { service, state: 'timeout', url, ms: elapsed(), mechanism };
    if (ev === 'load') {
      // Loaded AND it defined the global only a Document Server defines.
      return {
        service,
        state: deps.hasDocsAPI() ? 'ok' : 'wrong-content',
        url,
        ms: elapsed(),
        mechanism,
      };
    }
    const reached = await deps.reachable(url);
    return {
      service,
      state: reached ? 'wrong-content' : 'unreachable',
      url,
      ms: elapsed(),
      mechanism,
    };
  }

  if (mechanism === 'iframe') {
    const ev = await deps.loadFramed(url, timeoutMs);
    if (ev === 'ready') return { service, state: 'ok', url, ms: elapsed(), mechanism };
    // No handshake. An iframe fires `load` for error pages too, so the frame
    // itself cannot tell us why — ask the network instead.
    const reached = await deps.reachable(url);
    return {
      service,
      state: reached ? 'wrong-content' : 'unreachable',
      url,
      ms: elapsed(),
      mechanism,
    };
  }

  return { service, state: 'skipped', url: '', ms: 0, mechanism };
}
