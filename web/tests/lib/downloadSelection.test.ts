// "Download the selection as one archive" — the client half.
//
// Two things here are easy to get wrong in a way no status code reveals:
//
//  1. The mint URL must be derived from the CONFIGURED manager URL. Hardcoding
//     `/api/files/archive/download` works on the admin app and posts to the
//     wrong origin on every cross-origin embed — the exact defect
//     archiveViewerEndpoint.test.ts was written for after it shipped once.
//  2. The download must not be a `window.open`. It happens AFTER an await, so
//     the user-gesture token is spent and the popup blocker takes it — which
//     is the same failure the whole feature exists to remove, moved one step
//     later where nobody looks for it.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  absoluteTicketUrl,
  archiveTicketUrl,
  downloadArchive,
  requestArchive,
  triggerFileNavigation,
} from '@brftech/filex-core/src/lib/downloadSelection';

// happy-dom really tries to LOAD an iframe's src, which turns every assertion
// below into a failed request to localhost and buries real failures in abort
// traces. The navigation is the thing under test, not what the server answers.
beforeEach(() => {
  vi.stubGlobal(
    'fetch',
    vi.fn().mockResolvedValue({ ok: true, status: 200, text: async () => '', headers: new Map() }),
  );
  document.body.innerHTML = '';
});
afterEach(() => {
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

const TICKET = {
  url: '/z/abc123',
  ticket: 'abc123',
  name: 'Faturalar.zip',
  files: 4,
  bytes: 900,
  expires_at: '2026-09-13T10:00:00Z',
};

function fakeApi(manager: string, jsonFetch = vi.fn().mockResolvedValue(TICKET)) {
  return { endpoints: { manager } as never, jsonFetch } as never as {
    endpoints: { manager: string };
    jsonFetch: ReturnType<typeof vi.fn>;
  };
}

describe('archiveTicketUrl', () => {
  it('swaps the manager segment, the way every other off-map route is built', () => {
    expect(archiveTicketUrl('/api/files/manager')).toBe('/api/files/archive/download');
    expect(archiveTicketUrl('/api/files/manager?q=index')).toBe('/api/files/archive/download');
  });

  it('keeps a cross-origin embed on its own API host', () => {
    expect(archiveTicketUrl('https://files.example.com/api/files/manager')).toBe(
      'https://files.example.com/api/files/archive/download',
    );
  });
});

describe('absoluteTicketUrl', () => {
  it('leaves a same-origin ticket relative', () => {
    expect(absoluteTicketUrl('/api/files/manager', '/z/abc')).toBe('/z/abc');
  });

  // ⚠ The server answers a server-relative `/z/<token>`, which is right for
  // the admin app and points at the HOST page on an embed. The origin has to
  // come from the manager URL, the one thing that is always correct.
  it('puts a cross-origin ticket back on the API origin', () => {
    expect(absoluteTicketUrl('https://files.example.com/api/files/manager', '/z/abc')).toBe(
      'https://files.example.com/z/abc',
    );
  });

  it('leaves an absolute ticket alone', () => {
    expect(absoluteTicketUrl('/api/files/manager', 'https://cdn.example.com/z/abc')).toBe(
      'https://cdn.example.com/z/abc',
    );
  });
});

describe('requestArchive', () => {
  it('posts the paths to the derived endpoint', async () => {
    const jsonFetch = vi.fn().mockResolvedValue(TICKET);
    const api = fakeApi('https://files.example.com/api/files/manager', jsonFetch);
    const got = await requestArchive(api, ['main://a.txt', 'main://b']);
    expect(got).toEqual(TICKET);
    expect(jsonFetch.mock.calls[0][0]).toBe('https://files.example.com/api/files/archive/download');
    const init = jsonFetch.mock.calls[0][1];
    expect(init.method).toBe('POST');
    expect(JSON.parse(init.body)).toEqual({ paths: ['main://a.txt', 'main://b'] });
  });

  it('passes a caller-chosen name through', async () => {
    const jsonFetch = vi.fn().mockResolvedValue(TICKET);
    await requestArchive(fakeApi('/api/files/manager', jsonFetch), ['main://a'], 'Seçtiklerim');
    expect(JSON.parse(jsonFetch.mock.calls[0][1].body)).toEqual({
      paths: ['main://a'],
      name: 'Seçtiklerim',
    });
  });

  it('lets the api client error through so the caller can branch on status', async () => {
    const err = Object.assign(new Error('Conflict'), { status: 409 });
    const jsonFetch = vi.fn().mockRejectedValue(err);
    await expect(requestArchive(fakeApi('/api/files/manager', jsonFetch), ['x'])).rejects.toBe(err);
  });
});

describe('triggerFileNavigation', () => {
  it('navigates a hidden iframe, not a window', () => {
    triggerFileNavigation('/z/abc');
    const frame = document.querySelector('iframe');
    expect(frame).toBeTruthy();
    expect(frame!.getAttribute('src')).toBe('/z/abc');
    expect(frame!.hidden).toBe(true);
  });
});

describe('downloadArchive', () => {
  it('mints, then starts the download', async () => {
    const jsonFetch = vi.fn().mockResolvedValue(TICKET);
    const ticket = await downloadArchive(fakeApi('/api/files/manager', jsonFetch), [
      'main://a.txt',
      'main://b.txt',
    ]);
    expect(ticket.name).toBe('Faturalar.zip');
    expect(document.querySelector('iframe')!.getAttribute('src')).toBe('/z/abc123');
  });

  // ⚠ Red proof for the popup trap: `window.open` after an await is a popup.
  // If this ever goes back to window.open, this fails.
  it('never calls window.open', async () => {
    const open = vi.fn();
    vi.stubGlobal('open', open);
    await downloadArchive(fakeApi('/api/files/manager'), ['main://a.txt']);
    expect(open).not.toHaveBeenCalled();
    vi.unstubAllGlobals();
  });

  it('does not start a download when the mint fails', async () => {
    const jsonFetch = vi.fn().mockRejectedValue(new Error('nope'));
    await expect(
      downloadArchive(fakeApi('/api/files/manager', jsonFetch), ['main://a.txt']),
    ).rejects.toThrow('nope');
    expect(document.querySelector('iframe')).toBeNull();
  });
});
