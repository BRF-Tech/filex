// A store's install link carries its token in the FRAGMENT
// (`/admin/store-install#store=…&intent=…`, docs/APP-PLUGINS.md → Installing
// from a store). lib/storeLink.ts takes it off the address bar before the
// router runs - so neither the history, nor a sign-in redirect's `?redirect=`,
// nor a reload ever carries it - and hands it to the page once.
import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import {
  captureStoreFragment,
  captureStoreLink,
  dropStoreFragment,
  hasStoreLink,
  isFramed,
  keepStoreLink,
  parseStoreFragment,
  storeLinkArrivals,
  takeMalformedLink,
  takeStoreLink,
} from '@/lib/storeLink';

function at(url: string) {
  window.history.replaceState(null, '', url);
}

describe('store install link', () => {
  beforeEach(() => {
    sessionStorage.clear();
    takeStoreLink();
    takeMalformedLink();
  });
  afterEach(() => at('/'));

  it('reads the fragment the store writes', () => {
    expect(parseStoreFragment('#store=https%3A%2F%2Ffapps.brfd.app&intent=tok_abc123XYZ')).toEqual({
      store: 'https://fapps.brfd.app',
      token: 'tok_abc123XYZ',
    });
    expect(parseStoreFragment('#store=https%3A%2F%2Ffapps.brfd.app')).toBeNull();
    expect(parseStoreFragment('')).toBeNull();
  });

  // The fragment is whatever the address bar was given: anything but the two
  // keys, each once, in their own alphabet, is no link at all.
  it.each([
    ['a token of another alphabet', '#store=https%3A%2F%2Fs.example&intent=tok%20with%20space'],
    ['a token with a path in it', '#store=https%3A%2F%2Fs.example&intent=..%2F..%2Fkeys.json'],
    ['a token too short', '#store=https%3A%2F%2Fs.example&intent=abc'],
    ['a token too long', '#store=https%3A%2F%2Fs.example&intent=' + 'a'.repeat(513)],
    ['a fragment over 4 KiB', '#store=https%3A%2F%2Fs.example&intent=tok_00000001&x=' + 'a'.repeat(5000)],
    ['a key twice', '#store=https%3A%2F%2Fs.example&intent=tok_00000001&intent=tok_00000002'],
    ['a third key', '#store=https%3A%2F%2Fs.example&intent=tok_00000001&next=https%3A%2F%2Fevil.example'],
    ['a store with a path', '#store=https%3A%2F%2Fs.example%2Fv1&intent=tok_00000001'],
    ['a store with credentials', '#store=https%3A%2F%2Fu%3Ap%40s.example&intent=tok_00000001'],
    ['a javascript: store', '#store=javascript%3Aalert(1)&intent=tok_00000001'],
    ['a look-alike host (Cyrillic a)', '#store=https%3A%2F%2Ff%D0%B0pps.brfd.app&intent=tok_00000001'],
    ['a NUL in the store', '#store=https%3A%2F%2Fs.example%00.evil&intent=tok_00000001'],
    ['markup in the store', '#store=%3Cimg%20src%3Dx%20onerror%3Dalert(1)%3E&intent=tok_00000001'],
    ['a malformed escape', '#store=https%3A%2F%2Fs.example%E0%A4%A&intent=tok_00000001'],
  ])('refuses %s', (_name, hash) => {
    expect(parseStoreFragment(hash)).toBeNull();
  });

  it('accepts a trailing slash and a port', () => {
    expect(parseStoreFragment('#store=https%3A%2F%2Fs.example%3A8443%2F&intent=tok_0123456789')).toEqual({
      store: 'https://s.example:8443',
      token: 'tok_0123456789',
    });
  });

  it('a malformed fragment is removed, kept nowhere, and said as such', () => {
    at('/admin/store-install#store=javascript%3Aalert(1)&intent=tok_00000001');
    expect(captureStoreLink()).toBeNull();
    expect(window.location.hash).toBe('');
    expect(sessionStorage.getItem('filex.storeLink')).toBeNull();
    expect(takeMalformedLink()).toBe(true);
    expect(takeMalformedLink()).toBe(false);
  });

  it('takes the fragment off the address bar and keeps the link for this tab, once', () => {
    at('/admin/store-install?x=1#store=https%3A%2F%2Ffapps.brfd.app&intent=tok_abc123XYZ');
    const before = window.history.length;
    const got = captureStoreLink();
    expect(got).toEqual({ store: 'https://fapps.brfd.app', token: 'tok_abc123XYZ' });
    expect(window.location.hash).toBe('');
    expect(window.location.pathname).toBe('/admin/store-install');
    expect(window.location.search).toBe('?x=1');
    expect(window.location.href).not.toContain('tok_abc123XYZ');
    expect(window.history.length).toBe(before); // replaced, not pushed

    expect(takeStoreLink()).toEqual({ store: 'https://fapps.brfd.app', token: 'tok_abc123XYZ' });
    expect(takeStoreLink()).toBeNull();
  });

  it('under a base path too', () => {
    at('/filex/admin/store-install#store=https%3A%2F%2Fs.example&intent=tok_00000001');
    expect(captureStoreLink()).not.toBeNull();
    expect(window.location.hash).toBe('');
  });

  it('a malformed fragment is removed and nothing is kept', () => {
    at('/admin/store-install#intent=tok_onlythetoken');
    expect(captureStoreLink()).toBeNull();
    expect(window.location.hash).toBe('');
    expect(takeStoreLink()).toBeNull();
  });

  it('the path in another letter case is the same page (store fe review #6)', () => {
    at('/admin/Store-Install#store=https%3A%2F%2Fs.example&intent=tok_00000001');
    expect(captureStoreLink()).toEqual({ store: 'https://s.example', token: 'tok_00000001' });
    expect(window.location.hash).toBe('');
    expect(window.location.pathname).toBe('/admin/Store-Install');
  });

  it('leaves every other page alone', () => {
    at('/admin/plugins#store=https%3A%2F%2Fs.example&intent=tok_00000001');
    expect(captureStoreLink()).toBeNull();
    expect(window.location.hash).toBe('#store=https%3A%2F%2Fs.example&intent=tok_00000001');
    expect(takeStoreLink()).toBeNull();
  });

  it('knows a frame from a top-level page, and a frame it cannot look out of', () => {
    expect(isFramed(window)).toBe(false);
    const top = {};
    expect(isFramed({ self: window, top } as unknown as Window)).toBe(true);
    const sealed = Object.defineProperty({ self: window }, 'top', { get: () => { throw new Error('cross-origin'); } });
    expect(isFramed(sealed as unknown as Window)).toBe(true);
  });

  // store fe review #2: a second link in a tab already on the page reaches
  // the router, not a page load; it is taken the same way.
  it('a fragment given after the page loaded: off the address bar, kept, and the page told', () => {
    at('/admin/store-install');
    const before = storeLinkArrivals.value;
    window.history.pushState(null, '', '/admin/store-install#store=https%3A%2F%2Fs.example&intent=tok_00000002');
    expect(captureStoreFragment(window.location.hash)).toEqual({ store: 'https://s.example', token: 'tok_00000002' });
    expect(window.location.href).not.toContain('tok_00000002');
    expect(storeLinkArrivals.value).toBe(before + 1);
    expect(hasStoreLink()).toBe(true);
    expect(takeStoreLink()?.token).toBe('tok_00000002');
    expect(hasStoreLink()).toBe(false);
  });

  it('a newer fragment that is no link drops the one still waiting', () => {
    at('/admin/store-install#store=https%3A%2F%2Fs.example&intent=tok_00000001');
    captureStoreLink();
    expect(captureStoreFragment('#intent=tok_onlythetoken')).toBeNull();
    expect(takeMalformedLink()).toBe(true);
    expect(takeStoreLink()).toBeNull();
  });

  it('a link put back after the session ended keeps the time it arrived', () => {
    sessionStorage.setItem('filex.storeLink', JSON.stringify({ store: 'https://s.example', token: 'tok_00000003', at: Date.now() - 59 * 60_000 }));
    const l = takeStoreLink();
    expect(l?.token).toBe('tok_00000003');
    keepStoreLink(l!);
    expect(JSON.parse(sessionStorage.getItem('filex.storeLink') ?? '{}').at).toBeLessThan(Date.now() - 58 * 60_000);
  });

  it('a sign-in address never carries a store link', () => {
    expect(dropStoreFragment('/store-install#store=https%3A%2F%2Fs.example&intent=tok_00000001')).toBe('/store-install');
    expect(dropStoreFragment('/Store-Install?x=1#anything')).toBe('/Store-Install?x=1');
    expect(dropStoreFragment('/explore?storage=a#store=https%3A%2F%2Fs.example&intent=tok_00000001')).toBe('/explore?storage=a');
    expect(dropStoreFragment('/explore?storage=a#/docs/2026')).toBe('/explore?storage=a#/docs/2026');
    expect(dropStoreFragment('/dashboard')).toBe('/dashboard');
  });

  it('a page load drops a link that waited over an hour, on any page (store fe review #3)', () => {
    sessionStorage.setItem('filex.storeLink', JSON.stringify({ store: 'https://s.example', token: 'tok_00000001', at: Date.now() - 2 * 3600_000 }));
    at('/admin/dashboard');
    expect(captureStoreLink()).toBeNull();
    expect(sessionStorage.getItem('filex.storeLink')).toBeNull();
    sessionStorage.setItem('filex.storeLink', JSON.stringify({ store: 'https://s.example', token: 'tok_00000001', at: Date.now() }));
    captureStoreLink();
    expect(sessionStorage.getItem('filex.storeLink')).not.toBeNull();
  });

  it('a link left waiting for over an hour is dropped', () => {
    sessionStorage.setItem('filex.storeLink', JSON.stringify({ store: 'https://s.example', token: 'tok_00000001', at: Date.now() - 2 * 3600_000 }));
    expect(takeStoreLink()).toBeNull();
  });
});
