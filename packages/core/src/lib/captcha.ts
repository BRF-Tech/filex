/**
 * captcha - the bot check every anonymous form asks for: the provider's widget
 * (Cloudflare Turnstile, or Google reCAPTCHA v2), and the LOCAL proof of work
 * the browser does when the server cannot reach the provider.
 *
 * The contract is shared with the operator's other products and is not this
 * package's to bend (the shared local-fallback spec; its test vectors are
 * `backend/internal/captcha/testdata/captcha-local-vectors.json`, read by
 * web/tests/lib/captchaLocal.test.ts rather than copied):
 *
 *   GET  {base}/api/captcha/config            which provider, which forms
 *   POST {base}/api/captcha/local/challenge   a challenge, when the provider's
 *                                             script did not load here
 *   a protected request                       `captcha_token` (the widget's)
 *                                             or `captcha_local` (the solution,
 *                                             base64 of its JSON)
 *
 * The answers a protected request can get, and what each means here:
 *
 *   409 `{captcha: "fallback", challenge}`  the server could not reach the
 *        provider: solve the challenge and send the SAME request once more;
 *   422 `{captcha: "<reason>", message}`     the check failed (no token, a
 *        spent or a bad one): the widget starts over, the server's sentence is
 *        shown, and the fallback is NOT offered - it is for an outage only;
 *   503 `{captcha: "unavailable"}`           neither the provider nor the
 *        fallback (a test setting).
 *
 * ⚠ The proof of work runs in a Web Worker built from a `blob:` URL, with a
 * plain JavaScript SHA-256: no WASM, nothing to fetch, and the page's own
 * thread stays free to draw the progress bar.
 *
 * ⚠ Nothing here is stored. A token is good for one request: the composable
 * (composables/useCaptcha) starts the widget over after every send.
 */

/** The provider a server is set to; `none` = the check is off. */
export type CaptchaProvider = 'none' | 'turnstile' | 'recaptcha';

/** The forms filex protects, each on or off on the server. */
export type CaptchaForm = 'login' | 'demo_login' | 'share_password' | 'file_request';

/** Every form, in the order the settings page lists them. */
export const CAPTCHA_FORMS: readonly CaptchaForm[] = ['login', 'demo_login', 'share_password', 'file_request'];

/** What `GET /api/captcha/config` answers (no session, never cached). */
export interface CaptchaPublicConfig {
  provider: CaptchaProvider;
  site_key: string | null;
  /** The provider's script, as the server names it. */
  script: string | null;
  fallback: 'local' | 'off';
  /** The protected forms; `[]` when the provider is `none`. */
  forms: string[];
  /**
   * The server knows it cannot verify a token (no secret, no site key, a
   * secret it cannot read): the widget is not drawn, and the browser asks
   * the server for the local challenge straight away.
   */
  misconfigured: boolean;
  /** The server is in an outage window right now. */
  fallback_active: boolean;
  local_field: string;
  token_field: string;
}

/** The local challenge (the server's; signed, nothing to keep). */
export interface CaptchaChallenge {
  algorithm: string;
  /** `<hex>?expires=<unix seconds>[&...]` */
  salt: string;
  hash: string;
  signature: string;
  maxnumber: number;
}

/** What a protected request carries: one of the two, or nothing. */
export interface CaptchaFields {
  captcha_token?: string;
  captcha_local?: string;
}

/** A protected request's answer, as `captchaAnswerOf` reads it. */
export interface CaptchaReply {
  status: number;
  body: Record<string, unknown> | null;
}

/** The field names, unless a server says otherwise. */
export const CAPTCHA_TOKEN_FIELD = 'captcha_token';
export const CAPTCHA_LOCAL_FIELD = 'captcha_local';

/** The address of one of the two captcha routes, under a server root (no `/api`). */
export function captchaUrl(base: string | undefined, tail: 'config' | 'local/challenge'): string {
  return `${(base ?? '').replace(/\/+$/, '')}/api/captcha/${tail}`;
}

/** Is this form protected on a server set up like this? */
export function captchaFormOn(config: CaptchaPublicConfig | null | undefined, form: string): boolean {
  if (!config || config.provider === 'none') return false;
  return Array.isArray(config.forms) && config.forms.includes(form);
}

/**
 * What a protected request's answer asks of the widget.
 *
 *   `fallback`    409 with a challenge: solve it and send again;
 *   `refused`     422 with a captcha reason: start over, show the sentence;
 *   `unavailable` 503 captcha: say so;
 *   `null`        anything else - not the bot check's business (a wrong PIN,
 *                 a lock, a server error).
 */
export function captchaAnswerOf(reply: CaptchaReply | null | undefined): 'fallback' | 'refused' | 'unavailable' | null {
  if (!reply || !reply.body) return null;
  const reason = reply.body.captcha;
  if (typeof reason !== 'string' || !reason) return null;
  if (reply.status === 409 && reason === 'fallback' && challengeOf(reply.body.challenge)) return 'fallback';
  if (reply.status === 422) return 'refused';
  if (reply.status === 503) return 'unavailable';
  return null;
}

/** A challenge from an answer's body, or null when it is not one. */
export function challengeOf(v: unknown): CaptchaChallenge | null {
  if (!v || typeof v !== 'object') return null;
  const c = v as Record<string, unknown>;
  if (typeof c.salt !== 'string' || typeof c.hash !== 'string' || typeof c.signature !== 'string') return null;
  const max = Number(c.maxnumber);
  if (!Number.isInteger(max) || max < 0) return null;
  return {
    algorithm: typeof c.algorithm === 'string' && c.algorithm ? c.algorithm : 'SHA-256',
    salt: c.salt,
    hash: c.hash,
    signature: c.signature,
    maxnumber: max,
  };
}

/**
 * The solution as the form sends it: the JSON of the five fields, UTF-8,
 * standard base64 with padding (RFC 4648 §4).
 *
 * ⚠ The fields are the challenge's own, as the server sent them - the hash in
 * the case it came in (the server lowers it, vector P6) - and `number` is a
 * JSON integer: a string `"4242"` is refused (vector N11).
 */
export function encodeCaptchaSolution(challenge: CaptchaChallenge, number: number): string {
  const json = JSON.stringify({
    algorithm: challenge.algorithm || 'SHA-256',
    salt: challenge.salt,
    number: Math.trunc(number),
    hash: challenge.hash,
    signature: challenge.signature,
  });
  const bytes = new TextEncoder().encode(json);
  let bin = '';
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin);
}

/* ── the proof of work ─────────────────────────────────────────────────── */

/**
 * The worker's source. ⚠ A string on purpose: it is what the `blob:` worker
 * runs, and the test runs the SAME text against the shared vectors (a function
 * turned into a string by the bundler is not guaranteed to survive minifying).
 *
 * It takes `{salt, hash, maxnumber}`, scans `number` from 0 to `maxnumber`
 * (both ends included) for `sha256(salt + number) == hash`, reports progress
 * every 4096 tries, and answers `{type: "done", number, ms}` or
 * `{type: "fail", reason}`.
 */
export const CAPTCHA_WORKER_SOURCE = `
'use strict';
var K = new Uint32Array([
  0x428a2f98,0x71374491,0xb5c0fbcf,0xe9b5dba5,0x3956c25b,0x59f111f1,0x923f82a4,0xab1c5ed5,
  0xd807aa98,0x12835b01,0x243185be,0x550c7dc3,0x72be5d74,0x80deb1fe,0x9bdc06a7,0xc19bf174,
  0xe49b69c1,0xefbe4786,0x0fc19dc6,0x240ca1cc,0x2de92c6f,0x4a7484aa,0x5cb0a9dc,0x76f988da,
  0x983e5152,0xa831c66d,0xb00327c8,0xbf597fc7,0xc6e00bf3,0xd5a79147,0x06ca6351,0x14292967,
  0x27b70a85,0x2e1b2138,0x4d2c6dfc,0x53380d13,0x650a7354,0x766a0abb,0x81c2c92e,0x92722c85,
  0xa2bfe8a1,0xa81a664b,0xc24b8b70,0xc76c51a3,0xd192e819,0xd6990624,0xf40e3585,0x106aa070,
  0x19a4c116,0x1e376c08,0x2748774c,0x34b0bcb5,0x391c0cb3,0x4ed8aa4a,0x5b9cca4f,0x682e6ff3,
  0x748f82ee,0x78a5636f,0x84c87814,0x8cc70208,0x90befffa,0xa4506ceb,0xbef9a3f7,0xc67178f2
]);
var W = new Uint32Array(64);
var H = new Uint32Array(8);
var block = new Uint8Array(384);
var view = new DataView(block.buffer);

function sha256Words(bytes, len) {
  H[0]=0x6a09e667; H[1]=0xbb67ae85; H[2]=0x3c6ef372; H[3]=0xa54ff53a;
  H[4]=0x510e527f; H[5]=0x9b05688c; H[6]=0x1f83d9ab; H[7]=0x5be0cd19;
  var total = (len + 9 + 63) & ~63;
  for (var i = len; i < total; i++) bytes[i] = 0;
  bytes[len] = 0x80;
  view.setUint32(total - 4, (len * 8) >>> 0, false);
  view.setUint32(total - 8, Math.floor((len * 8) / 4294967296), false);
  for (var off = 0; off < total; off += 64) {
    for (var t = 0; t < 16; t++) W[t] = view.getUint32(off + t * 4, false);
    for (t = 16; t < 64; t++) {
      var w15 = W[t-15], w2 = W[t-2];
      var s0 = ((w15 >>> 7) | (w15 << 25)) ^ ((w15 >>> 18) | (w15 << 14)) ^ (w15 >>> 3);
      var s1 = ((w2 >>> 17) | (w2 << 15)) ^ ((w2 >>> 19) | (w2 << 13)) ^ (w2 >>> 10);
      W[t] = (W[t-16] + s0 + W[t-7] + s1) >>> 0;
    }
    var a=H[0],b=H[1],c=H[2],d=H[3],e=H[4],f=H[5],g=H[6],h=H[7];
    for (t = 0; t < 64; t++) {
      var S1 = ((e >>> 6) | (e << 26)) ^ ((e >>> 11) | (e << 21)) ^ ((e >>> 25) | (e << 7));
      var ch = (e & f) ^ (~e & g);
      var t1 = (h + S1 + ch + K[t] + W[t]) >>> 0;
      var S0 = ((a >>> 2) | (a << 30)) ^ ((a >>> 13) | (a << 19)) ^ ((a >>> 22) | (a << 10));
      var maj = (a & b) ^ (a & c) ^ (b & c);
      var t2 = (S0 + maj) >>> 0;
      h = g; g = f; f = e; e = (d + t1) >>> 0; d = c; c = b; b = a; a = (t1 + t2) >>> 0;
    }
    H[0]=(H[0]+a)>>>0; H[1]=(H[1]+b)>>>0; H[2]=(H[2]+c)>>>0; H[3]=(H[3]+d)>>>0;
    H[4]=(H[4]+e)>>>0; H[5]=(H[5]+f)>>>0; H[6]=(H[6]+g)>>>0; H[7]=(H[7]+h)>>>0;
  }
  return H;
}

self.onmessage = function (ev) {
  var data = ev.data || {};
  var salt = String(data.salt || '');
  var target = String(data.hash || '').toLowerCase();
  var max = Number(data.maxnumber);
  if (!/^[0-9a-f]{64}$/.test(target) || salt.length > 256 || !(max >= 0) || max !== Math.floor(max)) {
    self.postMessage({ type: 'fail', reason: 'bad_challenge' });
    return;
  }
  var tw = new Uint32Array(8);
  for (var i = 0; i < 8; i++) tw[i] = parseInt(target.substr(i * 8, 8), 16) >>> 0;
  var sl = salt.length;
  for (i = 0; i < sl; i++) {
    var code = salt.charCodeAt(i);
    if (code > 0x7f) { self.postMessage({ type: 'fail', reason: 'bad_salt' }); return; }
    block[i] = code;
  }
  var start = Date.now();
  for (var n = 0; n <= max; n++) {
    var s = String(n);
    var len = sl + s.length;
    for (i = 0; i < s.length; i++) block[sl + i] = s.charCodeAt(i);
    var hw = sha256Words(block, len);
    if (hw[0] === tw[0] && hw[1] === tw[1] && hw[2] === tw[2] && hw[3] === tw[3] &&
        hw[4] === tw[4] && hw[5] === tw[5] && hw[6] === tw[6] && hw[7] === tw[7]) {
      self.postMessage({ type: 'done', number: n, ms: Date.now() - start });
      return;
    }
    if ((n & 4095) === 0) self.postMessage({ type: 'progress', n: n, max: max });
  }
  self.postMessage({ type: 'fail', reason: 'not_found', ms: Date.now() - start });
};
`;

/** The smallest piece of a Worker the solver talks to (a test hands its own). */
export interface CaptchaWorkerLike {
  onmessage: ((ev: { data: unknown }) => void) | null;
  onerror: ((ev: unknown) => void) | null;
  postMessage(msg: unknown): void;
  terminate(): void;
}

let workerUrl: string | null = null;

/** The real worker, from a `blob:` URL made once per page. */
export function createCaptchaWorker(): CaptchaWorkerLike {
  if (workerUrl === null) {
    workerUrl = URL.createObjectURL(new Blob([CAPTCHA_WORKER_SOURCE], { type: 'text/javascript' }));
  }
  return new Worker(workerUrl) as unknown as CaptchaWorkerLike;
}

export interface CaptchaSolveOptions {
  /** 0..100 as the scan goes. */
  onProgress?: (percent: number) => void;
  /** Stops the worker (the form went away). The promise rejects with `aborted`. */
  signal?: AbortSignal;
  /** Where the worker comes from; the real one unless a test says otherwise. */
  createWorker?: () => CaptchaWorkerLike;
}

/** The number the challenge hides, and how long finding it took. */
export interface CaptchaSolution {
  number: number;
  ms: number;
  /** What the form sends as `captcha_local`. */
  payload: string;
}

/**
 * Finds the challenge's number in a worker. Rejects with an Error whose
 * message is the worker's reason (`bad_challenge`, `bad_salt`, `not_found`,
 * `worker_failed`, `aborted`).
 */
export function solveCaptchaChallenge(challenge: CaptchaChallenge, opts: CaptchaSolveOptions = {}): Promise<CaptchaSolution> {
  return new Promise<CaptchaSolution>((resolve, reject) => {
    let worker: CaptchaWorkerLike;
    try {
      worker = (opts.createWorker ?? createCaptchaWorker)();
    } catch {
      reject(new Error('worker_failed'));
      return;
    }
    let settled = false;
    const finish = (fn: () => void) => {
      if (settled) return;
      settled = true;
      opts.signal?.removeEventListener('abort', onAbort);
      try {
        worker.terminate();
      } catch {
        /* already gone */
      }
      fn();
    };
    const onAbort = () => finish(() => reject(new Error('aborted')));
    if (opts.signal?.aborted) {
      onAbort();
      return;
    }
    opts.signal?.addEventListener('abort', onAbort);
    worker.onmessage = (ev) => {
      const msg = (ev.data ?? {}) as { type?: string; n?: number; max?: number; number?: number; ms?: number; reason?: string };
      if (msg.type === 'progress') {
        if (msg.max) opts.onProgress?.(Math.min(100, Math.round(((msg.n ?? 0) / msg.max) * 100)));
        return;
      }
      if (msg.type === 'done' && typeof msg.number === 'number') {
        const number = msg.number;
        const ms = msg.ms ?? 0;
        opts.onProgress?.(100);
        finish(() => resolve({ number, ms, payload: encodeCaptchaSolution(challenge, number) }));
        return;
      }
      if (msg.type === 'fail') finish(() => reject(new Error(msg.reason || 'not_found')));
    };
    worker.onerror = () => finish(() => reject(new Error('worker_failed')));
    worker.postMessage({ salt: challenge.salt, hash: challenge.hash, maxnumber: challenge.maxnumber });
  });
}

/* ── the provider's script ─────────────────────────────────────────────── */

/** The two widgets' globals, as far as this file uses them. */
export interface TurnstileApi {
  render: (el: HTMLElement, opts: Record<string, unknown>) => string | undefined;
  reset: (id?: string) => void;
  remove: (id?: string) => void;
}
export interface RecaptchaApi {
  render: (el: HTMLElement, opts: Record<string, unknown>) => number;
  reset: (id?: number) => void;
}

type CaptchaWindow = Window & {
  turnstile?: TurnstileApi;
  grecaptcha?: RecaptchaApi;
  [key: string]: unknown;
};

/** The widget's global, once its script has loaded. */
export function providerGlobal(provider: CaptchaProvider): TurnstileApi | RecaptchaApi | null {
  if (typeof window === 'undefined') return null;
  const w = window as unknown as CaptchaWindow;
  if (provider === 'turnstile') return w.turnstile ?? null;
  if (provider === 'recaptcha') return w.grecaptcha?.render ? w.grecaptcha : null;
  return null;
}

const scriptLoads = new Map<string, Promise<void>>();
let onloadSeq = 0;

/**
 * Loads the provider's script once per page, for explicit rendering.
 *
 * ⚠ Rejects (`script_error`, `script_timeout`) when it does not arrive - a
 * content blocker is the usual reason - and the next call tries again. The
 * caller then asks the SERVER for the local challenge; the server answers
 * with one only when it cannot reach the provider either.
 *
 * `lang` goes to reCAPTCHA as `hl` (its widget takes the language from its
 * script); Turnstile takes it per widget.
 */
export function loadCaptchaScript(
  provider: CaptchaProvider,
  url: string,
  opts: { lang?: string; timeoutMs?: number } = {},
): Promise<void> {
  if (providerGlobal(provider)) return Promise.resolve();
  const key = `${provider} ${url}`;
  const pending = scriptLoads.get(key);
  if (pending) return pending;
  const p = new Promise<void>((resolve, reject) => {
    const w = window as unknown as CaptchaWindow;
    const cb = `__filexCaptchaOnload${++onloadSeq}`;
    const timer = window.setTimeout(() => {
      delete w[cb];
      reject(new Error('script_timeout'));
    }, opts.timeoutMs ?? 10000);
    w[cb] = () => {
      window.clearTimeout(timer);
      delete w[cb];
      resolve();
    };
    const u = new URL(url, window.location.href);
    u.searchParams.set('render', 'explicit');
    u.searchParams.set('onload', cb);
    if (provider === 'recaptcha' && opts.lang) u.searchParams.set('hl', opts.lang);
    const s = document.createElement('script');
    s.src = u.toString();
    s.async = true;
    s.dataset.filexCaptcha = provider;
    s.onerror = () => {
      window.clearTimeout(timer);
      delete w[cb];
      s.remove();
      reject(new Error('script_error'));
    };
    document.head.appendChild(s);
  });
  scriptLoads.set(key, p);
  p.catch(() => scriptLoads.delete(key));
  return p;
}

/**
 * The language a Turnstile widget is asked to speak: the page's when
 * Turnstile has it, else `auto` (the visitor's browser). ⚠ A code it does
 * not know is not passed: the widget answers that with an error rather than
 * with English.
 */
const TURNSTILE_LANGS = new Set([
  'ar', 'bg', 'cs', 'da', 'de', 'el', 'en', 'es', 'fa', 'fi', 'fr', 'he', 'hi', 'hr', 'hu', 'id', 'it', 'ja',
  'ko', 'lt', 'ms', 'nb', 'nl', 'pl', 'pt', 'ro', 'ru', 'sk', 'sl', 'sr', 'sv', 'th', 'tl', 'tr', 'uk', 'vi', 'zh',
]);
export function turnstileLanguage(locale: string | undefined): string {
  const code = String(locale ?? '').toLowerCase().split(/[-_]/)[0];
  return TURNSTILE_LANGS.has(code) ? code : 'auto';
}

/* ── the two routes ───────────────────────────────────────────────────── */

/** `GET /api/captcha/config`; null when the server does not answer one (an older filex, a network error). */
export async function fetchCaptchaConfig(base: string | undefined, fetchImpl?: typeof fetch): Promise<CaptchaPublicConfig | null> {
  try {
    const res = await (fetchImpl ?? fetch)(captchaUrl(base, 'config'), {
      headers: { Accept: 'application/json' },
      credentials: 'same-origin',
      cache: 'no-store',
    });
    if (!res.ok) return null;
    return readCaptchaConfig(await res.json());
  } catch {
    return null;
  }
}

/** A `/api/captcha/config` answer read defensively (an older or a foreign server: null). */
export function readCaptchaConfig(raw: unknown): CaptchaPublicConfig | null {
  const body = raw as Partial<CaptchaPublicConfig> | null;
  if (!body || typeof body !== 'object' || typeof body.provider !== 'string') return null;
  return {
    provider: (['none', 'turnstile', 'recaptcha'].includes(body.provider) ? body.provider : 'none') as CaptchaProvider,
    site_key: body.site_key ?? null,
    script: body.script ?? null,
    fallback: body.fallback === 'off' ? 'off' : 'local',
    forms: Array.isArray(body.forms) ? body.forms : [],
    misconfigured: body.misconfigured === true,
    fallback_active: body.fallback_active === true,
    local_field: body.local_field || CAPTCHA_LOCAL_FIELD,
    token_field: body.token_field || CAPTCHA_TOKEN_FIELD,
  };
}

/**
 * `POST /api/captcha/local/challenge`, for a page whose widget could not load.
 *
 *   `challenge`  the server cannot reach the provider either: solve this;
 *   `provider`   409 - the server CAN reach it, so the trouble is on this
 *                side (a content blocker): no fallback, the person is told;
 *   `disabled`   404 - the check is off;
 *   `unavailable` anything else.
 */
export async function requestCaptchaChallenge(
  base: string | undefined,
  opts: { fetchImpl?: typeof fetch; locale?: string } = {},
): Promise<{ kind: 'challenge'; challenge: CaptchaChallenge } | { kind: 'provider' | 'disabled' | 'unavailable'; message?: string }> {
  try {
    const headers: Record<string, string> = { Accept: 'application/json' };
    if (opts.locale) headers['Accept-Language'] = opts.locale;
    const res = await (opts.fetchImpl ?? fetch)(captchaUrl(base, 'local/challenge'), {
      method: 'POST',
      headers,
      credentials: 'same-origin',
      cache: 'no-store',
    });
    const body = (await res.json().catch(() => null)) as Record<string, unknown> | null;
    const challenge = challengeOf(body?.challenge);
    if (res.ok && challenge) return { kind: 'challenge', challenge };
    const message = typeof body?.message === 'string' ? body.message : undefined;
    if (res.status === 409) return { kind: 'provider', message };
    if (res.status === 404) return { kind: 'disabled', message };
    return { kind: 'unavailable', message };
  } catch {
    return { kind: 'unavailable' };
  }
}
