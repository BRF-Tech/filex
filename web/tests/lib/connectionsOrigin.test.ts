// The address the connection guides tell a client program to use.
//
// `connectionsOrigin` built the WebDAV URL and the `filex mount` / rclone
// lines from where the PAGE was loaded: the explorer's `apiBase` when it was
// absolute, else `window.location.origin`. Right for the admin SPA (the same
// binary serves both), wrong for any host that proxies `/api` to filex under
// its OWN origin — the explorer embedded in work.example.com or fishapp printed
// `https://<that app>/dav/`, which reaches the host and never filex. The same
// guide page's S3 endpoint and SFTP host always came from the server, so one
// page named two different machines.
//
// The server now publishes `public_url` on /api/files/capabilities — only when
// it is real (operator-configured, or a tenant's own host; see
// backend capabilities_public_url_test.go) — and it wins.
import { describe, expect, it } from 'vitest';

import { connectionsOrigin } from '@brftech/filex-core/src/composables/useConnections';
import type { ExplorerConfig } from '@brftech/filex-core';

const cfg = (c: Partial<ExplorerConfig>) => c as ExplorerConfig;

describe('connectionsOrigin', () => {
  it("the server's public address wins over where the page was loaded", () => {
    // an embed whose host proxies /api under its own origin
    expect(connectionsOrigin(cfg({ apiBase: '' }), 'https://files.example.com')).toBe('https://files.example.com');
    // the desktop app pointed at a LAN address of a server that has a public one
    expect(connectionsOrigin(cfg({ apiBase: 'http://10.0.0.5:5212' }), 'https://files.example.com/')).toBe(
      'https://files.example.com',
    );
  });

  it('with nothing from the server, an absolute apiBase is next', () => {
    expect(connectionsOrigin(cfg({ apiBase: 'https://fm.example.org/' }), null)).toBe('https://fm.example.org');
    expect(connectionsOrigin(cfg({ apiBase: 'https://fm.example.org' }))).toBe('https://fm.example.org');
  });

  it('and the page origin last — the admin SPA, served by the same binary', () => {
    expect(connectionsOrigin(cfg({ apiBase: '' }), undefined)).toBe(window.location.origin);
  });

  it('ignores a public_url that is not an absolute http(s) address', () => {
    expect(connectionsOrigin(cfg({ apiBase: 'https://fm.example.org' }), 'localhost:5212')).toBe('https://fm.example.org');
    expect(connectionsOrigin(cfg({ apiBase: 'https://fm.example.org' }), '   ')).toBe('https://fm.example.org');
    expect(connectionsOrigin(cfg({ apiBase: 'https://fm.example.org' }), 'javascript:alert(1)')).toBe('https://fm.example.org');
  });
});
