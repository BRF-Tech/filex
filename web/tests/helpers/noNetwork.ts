/**
 * Unit tests never reach the network.
 *
 * setup.ts installs this guard in every test file. A request the test did not
 * mock is refused on the spot (fetch rejects, XMLHttpRequest.send and the
 * WebSocket / EventSource constructors throw) with the URL and what to do
 * about it, and the test it happened in is failed by setup.ts's afterEach
 * even when the code under test swallows the error (most pages catch a
 * refused call and show a toast).
 *
 * ⚠ Why this exists (task #127, v0.49.0): happy-dom turns an unmocked call
 * into a REAL request to its page origin, localhost:3000. Nothing listens
 * there, so it is refused ECONNREFUSED — but whenever the network chooses,
 * often in the NEXT test, after the page has been taken out of the document.
 * The page then re-drew into DOM that was gone ("reading 'insertBefore'"),
 * an unhandled rejection that failed the release run with every test green.
 *
 * Four doors are guarded:
 *  - `fetch` (happy-dom's own, which vitest installs as the global, or
 *    Node's in a jsdom file);
 *  - `XMLHttpRequest` (axios's browser adapter, i.e. every `@/api/*` call);
 *  - `WebSocket` and `EventSource`, when the environment has them;
 *  - Node's `http.request` / `https.request` underneath, which is how
 *    happy-dom loads what a page ASKS it to (an <iframe src>, a
 *    <script src>, a stylesheet) and how it sends everything above.
 *
 * `data:`, `blob:` and `about:` URLs never leave the process and pass.
 *
 * A file that truly needs the network (none does today) says so at its top:
 *
 *     import { allowNetwork } from '../helpers/noNetwork';
 *     allowNetwork('talks to the throwaway server it starts in beforeAll');
 *
 * The reason is required and shows up in review; the permission lasts for
 * that file only.
 */
import http from 'node:http';
import https from 'node:https';
import { syncBuiltinESMExports } from 'node:module';
import { expect } from 'vitest';

const HOW =
  "Unit tests never use the network: mock it — vi.mock('@/api/…') for an API module, " +
  "vi.stubGlobal('fetch', …) for a raw fetch. A file that truly needs the network says so " +
  "with allowNetwork('why') from tests/helpers/noNetwork.";

let allowedBecause: string | null = null;
const hits: Error[] = [];

/** Let THIS test file reach the network. Say why; it is read in review. */
export function allowNetwork(reason: string): void {
  if (!reason || !reason.trim()) throw new Error('allowNetwork(reason): say why this file needs the network');
  allowedBecause = reason;
}

/** The refusals so far, emptied. setup.ts fails the test when it is not empty. */
export function takeNetworkHits(): Error[] {
  return hits.splice(0);
}

/** Throw one error naming every refused request (setup.ts afterEach / afterAll). */
export function failOnNetworkHits(when: string): void {
  const got = takeNetworkHits();
  if (got.length === 0) return;
  const lines = got.map((e) => '  - ' + e.message.split('\n')[0]);
  const err = new Error(
    `[no-network] ${got.length} request(s) left a unit test ${when}:\n${lines.join('\n')}\n${HOW}`,
  );
  err.name = 'NetworkBlockedError';
  throw err;
}

function isLocal(url: string): boolean {
  return /^(data|blob|about):/i.test(url);
}

function absolute(url: unknown): string {
  const raw = url instanceof URL ? url.href : String(url);
  try {
    const base = typeof location !== 'undefined' && location.href ? location.href : 'http://localhost/';
    return new URL(raw, base).href;
  } catch {
    return raw;
  }
}

function refuse(door: string, method: string, url: string): Error {
  let test = '';
  try {
    test = expect.getState().currentTestName ?? '';
  } catch {
    /* outside a test */
  }
  const err = new Error(
    `[no-network] ${door}: ${method} ${url}${test ? ` (in "${test}")` : ''}\n${HOW}`,
  );
  err.name = 'NetworkBlockedError';
  hits.push(err);
  return err;
}

/** A refused request, or null when it may go. */
function check(door: string, method: string, url: string): Error | null {
  if (allowedBecause !== null || isLocal(url)) return null;
  return refuse(door, method, url);
}

type AnyFn = (...args: never[]) => unknown;
type Glob = Record<string, unknown>;
const g = globalThis as unknown as Glob;

// --- fetch -----------------------------------------------------------------
const realFetch = g.fetch as ((input: unknown, init?: { method?: string }) => Promise<unknown>) | undefined;
if (typeof realFetch === 'function') {
  g.fetch = function fetch(input: unknown, init?: { method?: string }): Promise<unknown> {
    const req = typeof input === 'object' && input !== null && !(input instanceof URL) ? (input as { url?: string; method?: string }) : null;
    const url = absolute(req && 'url' in req ? req.url : input);
    const method = String(init?.method ?? req?.method ?? 'GET').toUpperCase();
    const no = check('fetch', method, url);
    if (no) return Promise.reject(no);
    return realFetch(input, init);
  };
}

// --- XMLHttpRequest --------------------------------------------------------
const XHR = g.XMLHttpRequest as { prototype: Record<string, AnyFn> } | undefined;
if (typeof XHR === 'function') {
  const target = new WeakMap<object, { method: string; url: string }>();
  const proto = XHR.prototype;
  const open = proto.open as (this: object, ...args: unknown[]) => unknown;
  const send = proto.send as (this: object, ...args: unknown[]) => unknown;
  proto.open = function (this: object, ...args: unknown[]) {
    target.set(this, { method: String(args[0] ?? 'GET').toUpperCase(), url: absolute(args[1]) });
    return open.apply(this, args);
  } as AnyFn;
  proto.send = function (this: object, ...args: unknown[]) {
    const t = target.get(this);
    const no = t ? check('XMLHttpRequest', t.method, t.url) : null;
    if (no) throw no;
    return send.apply(this, args);
  } as AnyFn;
}

// --- WebSocket / EventSource ------------------------------------------------
for (const name of ['WebSocket', 'EventSource'] as const) {
  const Real = g[name] as (new (...args: unknown[]) => object) | undefined;
  if (typeof Real !== 'function') continue;
  g[name] = class extends Real {
    constructor(...args: unknown[]) {
      const no = check(name, 'OPEN', absolute(args[0]));
      if (no) throw no;
      super(...args);
    }
  };
}

// --- node:http / node:https (what happy-dom itself sends) -------------------
// A builtin module outlives the test file: the next file in this worker runs
// setup.ts again. So the wrapper is installed ONCE per worker and asks the
// CURRENT file's guard, which every setup run re-points.
const CURRENT = Symbol.for('filex.tests.noNetwork.current');
const WRAPPED = Symbol.for('filex.tests.noNetwork.wrapped');
type Checker = (door: string, method: string, url: string) => Error | null;
(process as unknown as Record<symbol, Checker>)[CURRENT] = check;

function httpTarget(scheme: string, args: unknown[]): { method: string; url: string } {
  const [a, b] = args as [unknown, unknown];
  const opts = (typeof a === 'object' && a !== null && !(a instanceof URL) ? a : b) as
    | { method?: string; protocol?: string; hostname?: string; host?: string; port?: string | number; path?: string }
    | undefined;
  const method = String(opts?.method ?? 'GET').toUpperCase();
  if (typeof a === 'string' || a instanceof URL) return { method, url: String(a) };
  const host = opts?.hostname ?? opts?.host ?? 'localhost';
  const port = opts?.port ? `:${opts.port}` : '';
  return { method, url: `${opts?.protocol ?? scheme + ':'}//${host}${port}${opts?.path ?? '/'}` };
}

for (const [mod, scheme] of [
  [http, 'http'],
  [https, 'https'],
] as const) {
  const m = mod as unknown as Record<string | symbol, unknown>;
  if (m[WRAPPED]) continue;
  for (const fn of ['request', 'get'] as const) {
    const real = m[fn] as (...args: unknown[]) => unknown;
    m[fn] = function (this: unknown, ...args: unknown[]) {
      const current = (process as unknown as Record<symbol, Checker | undefined>)[CURRENT];
      const t = httpTarget(scheme, args);
      const no = current ? current(`node ${scheme}.${fn}`, t.method, t.url) : null;
      if (no) throw no;
      return real.apply(this, args);
    };
  }
  m[WRAPPED] = true;
}
syncBuiltinESMExports();
