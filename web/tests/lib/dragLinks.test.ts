// #71 — the browser's drag-out for a page that signs its calls with a bearer.
//
// Chromium reads the `DownloadURL` off the dataTransfer during `dragstart`, and
// the dataTransfer is writable ONLY during that event: nothing can be awaited
// there. A bearer session cannot hand the browser a plain download URL (the
// download stack sends no Authorization header), so it needs a short-lived,
// credential-free link from the server — which is a network round trip. The
// only way to have one in time is to ask BEFORE the drag: when the pointer
// rests on a row, and again on the press. These tests are that contract.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { createDragLinks, type MintedLink } from '../../../packages/core/src/lib/dragOut';
import { requestFileLink } from '../../../packages/core/src/lib/downloadSelection';

/** A mint the test resolves by hand, recording what was asked. */
function manualMint() {
  const calls: string[] = [];
  const pending: Array<{ path: string; resolve: (v: MintedLink | null) => void; reject: (e: unknown) => void }> = [];
  const mint = vi.fn((path: string) => {
    calls.push(path);
    return new Promise<MintedLink | null>((resolve, reject) => pending.push({ path, resolve, reject }));
  });
  return { mint, calls, pending };
}

const flush = () => new Promise((r) => setTimeout(r, 0));

describe('createDragLinks', () => {
  let now = 1_000_000;
  const clock = () => now;
  beforeEach(() => {
    now = 1_000_000;
  });

  it('has nothing to hand over until a link was minted — dragstart cannot wait', () => {
    const { mint } = manualMint();
    const links = createDragLinks(mint, { now: clock });
    expect(links.take('main://a.txt')).toBeNull();
    expect(mint).not.toHaveBeenCalled();
  });

  it('a warmed link is handed over once, and only once (the server consumes it on use)', async () => {
    const m = manualMint();
    const links = createDragLinks(m.mint, { now: clock });
    links.warm('main://a.txt');
    expect(m.calls).toEqual(['main://a.txt']);
    m.pending[0].resolve({ url: 'https://h/z/one', ttlMs: 60_000 });
    await flush();
    expect(links.take('main://a.txt')).toBe('https://h/z/one');
    expect(links.take('main://a.txt')).toBeNull();
  });

  it('warming the same row again does not mint a second link', async () => {
    const m = manualMint();
    const links = createDragLinks(m.mint, { now: clock });
    links.warm('main://a.txt');
    links.warm('main://a.txt'); // still in flight
    m.pending[0].resolve({ url: 'https://h/z/one', ttlMs: 60_000 });
    await flush();
    links.warm('main://a.txt'); // fresh
    expect(m.calls).toEqual(['main://a.txt']);
  });

  it('one mint at a time: rows the pointer crossed while one was in flight collapse to the LAST one', async () => {
    const m = manualMint();
    const links = createDragLinks(m.mint, { now: clock });
    links.warm('main://1.txt');
    links.warm('main://2.txt');
    links.warm('main://3.txt');
    links.warm('main://4.txt');
    expect(m.calls).toEqual(['main://1.txt']);
    m.pending[0].resolve({ url: 'https://h/z/1', ttlMs: 60_000 });
    await flush();
    expect(m.calls).toEqual(['main://1.txt', 'main://4.txt']);
  });

  it('a link about to lapse is not handed over — a drop on it would be a 410', async () => {
    const m = manualMint();
    const links = createDragLinks(m.mint, { now: clock });
    links.warm('main://a.txt');
    m.pending[0].resolve({ url: 'https://h/z/old', ttlMs: 60_000 });
    await flush();
    now += 56_000;
    expect(links.take('main://a.txt')).toBeNull();
  });

  it('an ageing link is refreshed on the next warm, and the old one still serves meanwhile', async () => {
    const m = manualMint();
    const links = createDragLinks(m.mint, { now: clock });
    links.warm('main://a.txt');
    m.pending[0].resolve({ url: 'https://h/z/old', ttlMs: 60_000 });
    await flush();
    now += 40_000; // 20 s left: below the refresh line, above the usable one
    links.warm('main://a.txt'); // the press
    expect(m.calls).toEqual(['main://a.txt', 'main://a.txt']);
    // dragstart comes a few ms after the press, before the refresh answered:
    expect(links.take('main://a.txt')).toBe('https://h/z/old');
  });

  it('a refused mint is not retried on every hover', async () => {
    const m = manualMint();
    const links = createDragLinks(m.mint, { now: clock });
    links.warm('main://nope.txt');
    m.pending[0].reject(Object.assign(new Error('Forbidden'), { status: 403 }));
    await flush();
    links.warm('main://nope.txt');
    links.warm('main://nope.txt');
    expect(m.calls).toEqual(['main://nope.txt']);
    now += 31_000;
    links.warm('main://nope.txt');
    expect(m.calls).toEqual(['main://nope.txt', 'main://nope.txt']);
  });

  it('an older server (no file links) switches the whole thing off, once', async () => {
    const m = manualMint();
    const onUnsupported = vi.fn();
    const links = createDragLinks(m.mint, { now: clock, onUnsupported });
    links.warm('main://a.txt');
    m.pending[0].resolve(null);
    await flush();
    expect(links.supported).toBe(false);
    expect(onUnsupported).toHaveBeenCalledTimes(1);
    links.warm('main://b.txt');
    expect(m.calls).toEqual(['main://a.txt']);
    expect(links.take('main://a.txt')).toBeNull();
  });

  it('keeps a bounded number of links', async () => {
    const m = manualMint();
    const links = createDragLinks(m.mint, { now: clock, max: 2 });
    for (const [i, p] of ['main://1', 'main://2', 'main://3'].entries()) {
      links.warm(p);
      m.pending[i].resolve({ url: `https://h/z/${i}`, ttlMs: 60_000 });
      await flush();
    }
    expect(links.take('main://1')).toBeNull();
    expect(links.take('main://3')).toBe('https://h/z/2');
  });
});

describe('requestFileLink', () => {
  function fakeApi(manager: string, jsonFetch: ReturnType<typeof vi.fn>) {
    return { endpoints: { manager } as never, jsonFetch } as never as {
      endpoints: { manager: string };
      jsonFetch: ReturnType<typeof vi.fn>;
    };
  }
  afterEach(() => vi.restoreAllMocks());

  it('asks the archive mint for ONE file and makes the link absolute on the API origin', async () => {
    const jsonFetch = vi.fn().mockResolvedValue({
      url: '/z/tok', ticket: 'tok', name: 'a.txt', files: 1, bytes: 3, expires_at: 'x', mode: 'file', ttl_seconds: 60,
    });
    const got = await requestFileLink(fakeApi('https://files.example.com/api/files/manager', jsonFetch), 'main://a.txt');
    expect(got).toEqual({ url: 'https://files.example.com/z/tok', ttlMs: 60_000 });
    expect(jsonFetch.mock.calls[0][0]).toBe('https://files.example.com/api/files/archive/download');
    expect(JSON.parse(jsonFetch.mock.calls[0][1].body)).toEqual({ paths: ['main://a.txt'], mode: 'file' });
  });

  // ⚠ An older server ignores `mode` and mints a ZIP of the one file. Dropped
  // on the desktop under the file's own name, that is a broken file — worse
  // than a drag that carries nothing.
  it('refuses a server that answered with a zip', async () => {
    const jsonFetch = vi.fn().mockResolvedValue({
      url: '/z/tok', ticket: 'tok', name: 'a.txt.zip', files: 1, bytes: 3, expires_at: 'x',
    });
    expect(await requestFileLink(fakeApi('/api/files/manager', jsonFetch), 'main://a.txt')).toBeNull();
  });
});
