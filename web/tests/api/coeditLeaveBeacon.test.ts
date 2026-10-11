// A member leaving a co-edit session as its page goes away (AppFrame's
// `leave({ keepalive: true })` on pagehide) is a beacon.
//
// The keepalive fetch it used to be left Firefox only now and then and
// WebKit never (measured 2026-10-10 by the office editor's browser runs), so
// the member stayed in the others' "editing" list until the relay timed it
// out. navigator.sendBeacon is the request a browser promises to deliver
// after the page is gone. A beacon carries no header, so it is used only
// where the session cookie alone authenticates (`auth: none`); a bearer or a
// host's CSRF header keeps the keepalive fetch.
import { afterEach, describe, expect, it, vi } from 'vitest';

import { useFileApi } from '@brftech/filex-core/src/composables/useFileApi';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function stubFetch() {
  const fetchMock = vi.fn(async () =>
    new Response(JSON.stringify({ left: true }), { status: 200, headers: { 'Content-Type': 'application/json' } }),
  );
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}

function stubBeacon(queued: boolean) {
  vi.restoreAllMocks();
  return vi.spyOn(navigator, 'sendBeacon').mockImplementation(() => queued);
}

const member = { client: 'c1', token: 'tok-1' };

describe('a member leaving as its page goes away', () => {
  it('goes as a beacon with the member in its body, on a cookie session', async () => {
    const fetchMock = stubFetch();
    const beacon = stubBeacon(true);
    const api = useFileApi({ apiBase: '', auth: { kind: 'none' } });
    const out = await api.coeditCall('sketch', 'editor', 'a1b2', 'leave', member, { keepalive: true });

    expect(beacon).toHaveBeenCalledTimes(1);
    const [url, body] = beacon.mock.calls[0];
    expect(url).toBe('/api/files/plugins/ui/sketch/editor/coedit/a1b2/leave');
    expect(typeof body).toBe('string');
    expect(JSON.parse(body as string)).toEqual(member);
    expect(fetchMock).not.toHaveBeenCalled();
    expect(out).toEqual({});
  });

  it('falls back to the keepalive fetch when the browser did not queue the beacon', async () => {
    const fetchMock = stubFetch();
    const beacon = stubBeacon(false);
    const api = useFileApi({ apiBase: '', auth: { kind: 'none' } });
    await api.coeditCall('sketch', 'editor', 'a1b2', 'leave', member, { keepalive: true });

    expect(beacon).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(init.keepalive).toBe(true);
    expect(JSON.parse(init.body as string)).toEqual(member);
  });

  it('keeps the keepalive fetch where a header authenticates (a bearer, a host CSRF header)', async () => {
    for (const auth of [{ kind: 'bearer', token: 't' }, { kind: 'csrf', csrf: 'x' }] as const) {
      const fetchMock = stubFetch();
      const beacon = stubBeacon(true);
      const api = useFileApi({ apiBase: 'https://files.example.com', auth });
      await api.coeditCall('sketch', 'editor', 'a1b2', 'leave', member, { keepalive: true });
      expect(beacon, auth.kind).not.toHaveBeenCalled();
      expect(fetchMock, auth.kind).toHaveBeenCalledTimes(1);
      const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
      expect(url).toBe('https://files.example.com/api/files/plugins/ui/sketch/editor/coedit/a1b2/leave');
      expect(init.keepalive, auth.kind).toBe(true);
      vi.unstubAllGlobals();
    }
  });

  it('a call while the page stays is an ordinary request, never a beacon', async () => {
    const fetchMock = stubFetch();
    const beacon = stubBeacon(true);
    const api = useFileApi({ apiBase: '', auth: { kind: 'none' } });
    await api.coeditCall('sketch', 'editor', 'a1b2', 'leave', member);
    expect(beacon).not.toHaveBeenCalled();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});
