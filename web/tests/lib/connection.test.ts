// ONE notice for a connection that is down, however many requests fall over
// while it is (core lib/connection).
//
// The defect this locks down, in the owner's words (2026-09-24): "bağlantı
// kopunca network error diye hata veriyor ki çok doğru ama … attığı tüm
// istekler için ayrı ayrı atıyor o yüzden çok fazla popover çıkıyor". The
// explorer alone fires several calls a second — the listing, a thumbnail per
// row, the bell's 15 s poll, the pending-operations poll — and every one of
// them raised its own toast with the same sentence in it.
import { beforeEach, describe, expect, it } from 'vitest';

import {
  LEAVE_GRACE_MS,
  connectionDown,
  connectionFolded,
  kindOfMethod,
  notePageLeaving,
  noteRequestFailed,
  noteRequestSucceeded,
  resetConnectionNotice,
} from '@brftech/filex-core/src/lib/connection';

beforeEach(() => resetConnectionNotice());

describe('which requests are the page’s and which are the person’s', () => {
  it('reads are the page keeping up to date; writes are something pressed', () => {
    for (const m of ['get', 'GET', 'head', 'options', undefined]) {
      expect(kindOfMethod(m), `${m} is the page's`).toBe('background');
    }
    for (const m of ['post', 'PUT', 'patch', 'delete']) {
      expect(kindOfMethod(m), `${m} is the person's`).toBe('action');
    }
  });
});

describe('a connection that is down is ONE fact about the page', () => {
  it('folds every background failure into a single notice', () => {
    expect(connectionDown.value).toBe(false);
    // A believable storm: one listing, twenty thumbnails, two polls.
    for (let i = 0; i < 23; i++) {
      expect(noteRequestFailed('background')).toBe('folded');
    }
    expect(connectionDown.value, 'the notice is up').toBe(true);
    expect(connectionFolded.value, 'and it stands in for all of them').toBe(23);
  });

  it('still lets an action the person is waiting on speak for itself', () => {
    // ⚠ The whole reason this is not simply "never say anything twice": a
    // failed upload or rename is something they are waiting to be told about.
    expect(noteRequestFailed('action')).toBe('say');
    expect(noteRequestFailed('action')).toBe('say');
    expect(connectionDown.value, 'and the shared notice is up too').toBe(true);
    expect(connectionFolded.value, 'an action is never folded').toBe(0);
  });
});

describe('and it takes itself down', () => {
  it('clears the moment anything gets an answer', () => {
    noteRequestFailed('background');
    noteRequestFailed('background');
    expect(connectionDown.value).toBe(true);
    noteRequestSucceeded();
    expect(connectionDown.value, 'nobody should dismiss a stale notice').toBe(false);
    expect(connectionFolded.value).toBe(0);
  });

  it('counts a refusal as an answer — the server spoke', () => {
    // A 404 or a 500 means the connection is fine and something else is
    // wrong; a strip saying "cannot reach the server" over a real refusal is
    // a lie the person then has to reason around.
    noteRequestFailed('background');
    noteRequestSucceeded();
    expect(connectionDown.value).toBe(false);
  });

  it('goes back up if it drops again', () => {
    noteRequestFailed('background');
    noteRequestSucceeded();
    expect(noteRequestFailed('background')).toBe('folded');
    expect(connectionDown.value).toBe(true);
    expect(connectionFolded.value, 'the count starts again with the outage').toBe(1);
  });

  it('is a no-op when nothing was wrong', () => {
    noteRequestSucceeded();
    expect(connectionDown.value).toBe(false);
    expect(connectionFolded.value).toBe(0);
  });
});

// task #260: the 0.56.3 GitHub gate's webkit red. The person's explorer was
// opened while the start page was still asking the server for things; WebKit
// fails every request in flight with a bare "Load failed" the moment a
// navigation starts, and the strip said "The server could not be reached" -
// about a server that was answering everything.
describe("the page's own failures are not a lost connection", () => {
  it('a request the page cancelled raises nothing, read or write', () => {
    const cancelled = [
      new DOMException('Aborted', 'AbortError'),
      { name: 'CanceledError', code: 'ERR_CANCELED', message: 'canceled' },
      { code: 'ECONNABORTED', message: 'Request aborted' },
    ];
    for (const cause of cancelled) {
      expect(noteRequestFailed('background', Date.now(), cause)).toBe('folded');
      expect(noteRequestFailed('action', Date.now(), cause), 'nothing for the caller to say either').toBe('folded');
    }
    expect(connectionDown.value).toBe(false);
    expect(connectionFolded.value).toBe(0);
  });

  it('a timeout IS the server not answering, and raises the notice', () => {
    expect(noteRequestFailed('action', Date.now(), { code: 'ECONNABORTED', message: 'timeout of 30000ms exceeded' })).toBe('say');
    expect(connectionDown.value).toBe(true);
  });

  it('a failure while the page is being left raises nothing; one after the grace does', () => {
    const t0 = 1_000_000;
    notePageLeaving(t0);
    expect(noteRequestFailed('background', t0 + 50, new TypeError('Load failed'))).toBe('folded');
    expect(connectionDown.value, 'WebKit dropping a leaving page’s requests').toBe(false);
    // Still here long after: the departure did not happen (a download link,
    // a cancelled prompt), and a real outage must not stay muted.
    expect(noteRequestFailed('background', t0 + LEAVE_GRACE_MS + 1, new TypeError('Load failed'))).toBe('folded');
    expect(connectionDown.value).toBe(true);
  });

  it('the browser tells it when the page goes, and when it is back', () => {
    window.dispatchEvent(new Event('pagehide'));
    noteRequestFailed('background', Date.now(), new TypeError('Load failed'));
    expect(connectionDown.value).toBe(false);
    window.dispatchEvent(new Event('pageshow'));
    noteRequestFailed('background', Date.now(), new TypeError('Load failed'));
    expect(connectionDown.value, 'back from the cache, a failure is a failure again').toBe(true);
  });
});
