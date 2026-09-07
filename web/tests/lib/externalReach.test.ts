// The browser half of the external-service check.
//
// # Why this file is written the way it is
//
// A green Test button used to answer three questions with one light. Only one
// of them was actually asked — "can the filex PROCESS reach the document
// server?" — and an operator on podman who typed the container name
// `http://onlyoffice` got that green light while the browser, which is what
// loads the editor, could not resolve the name at all (issue #17, twice).
//
// So the browser now probes for itself. The risk in that is the mirror image
// of the old bug: a probe that reports failure for a document server that
// works. The classification below is therefore asserted case by case, and the
// two mechanisms it is built on were verified against LIVE servers before this
// file existed:
//
//   - OnlyOffice `<script>` + `window.DocsAPI`, against a real OnlyOffice Docs
//     instance: `load` fired and `DocsAPI.DocEditor` was defined in ~0.5 s.
//   - drawio hidden `<iframe>` + `{"event":"init"}` postMessage, against a real
//     drawio: `init` arrived in ~3.2 s; a bogus host produced no handshake.
//   - `http://onlyoffice` (a name that does not resolve): script `error` after
//     ~5 s, and the `no-cors` fetch rejected → `unreachable`.
//   - `https://example.com` (something answers, 404 on the editor path):
//     script `error`, `no-cors` fetch RESOLVED → `wrong-content`.
//   - A black-holed address: neither event; bounded by the timeout.
//
// ⚠ A plain `fetch()` is the wrong mechanism and this is the whole reason the
// deps below exist: a healthy document server usually answers without CORS
// headers, so a naive fetch+catch reports failure for a working service.
import { describe, expect, it } from 'vitest';

import {
  browserProbeURL,
  probeExternalFromBrowser,
  type BrowserProbeDeps,
} from '../../../packages/core/src/lib/externalReach';

/** Deps that fail loudly if the probe reaches for something it should not. */
function deps(over: Partial<BrowserProbeDeps>): Partial<BrowserProbeDeps> {
  let clock = 0;
  return {
    pageProtocol: () => 'http:',
    now: () => (clock += 100),
    loadScript: async () => {
      throw new Error('loadScript not stubbed for this case');
    },
    loadFramed: async () => {
      throw new Error('loadFramed not stubbed for this case');
    },
    reachable: async () => {
      throw new Error('reachable must not be consulted for this case');
    },
    hasDocsAPI: () => false,
    ...over,
  };
}

describe('browserProbeURL', () => {
  it('probes the exact asset each viewer loads', () => {
    expect(browserProbeURL('onlyoffice', 'https://office.example.com')).toBe(
      'https://office.example.com/web-apps/apps/api/documents/api.js',
    );
    expect(browserProbeURL('drawio', 'https://draw.example.com')).toBe(
      'https://draw.example.com/?embed=1&proto=json',
    );
  });

  it('collapses trailing slashes so the two spellings agree', () => {
    expect(browserProbeURL('onlyoffice', 'https://office.example.com///')).toBe(
      'https://office.example.com/web-apps/apps/api/documents/api.js',
    );
  });

  it('has nothing to probe for a service the browser never fetches', () => {
    expect(browserProbeURL('convert', 'http://convert:8080')).toBe('');
  });
});

describe('probeExternalFromBrowser — onlyoffice', () => {
  it('reports ok only when the script loaded AND defined DocsAPI', async () => {
    const r = await probeExternalFromBrowser('onlyoffice', 'https://office.example.com', {
      deps: deps({ loadScript: async () => 'load', hasDocsAPI: () => true }),
    });
    expect(r.state).toBe('ok');
    expect(r.mechanism).toBe('script');
    expect(r.url).toBe('https://office.example.com/web-apps/apps/api/documents/api.js');
  });

  it('a script that loaded but defined nothing is the wrong server, not an outage', async () => {
    // ⚠ This is the distinction the operator needs: something answered at that
    // address, so the firewall is not the story.
    const r = await probeExternalFromBrowser('onlyoffice', 'https://files.example.com', {
      deps: deps({ loadScript: async () => 'load', hasDocsAPI: () => false }),
    });
    expect(r.state).toBe('wrong-content');
  });

  it('the reporter’s case: filex reaches the container name and the browser cannot', async () => {
    const r = await probeExternalFromBrowser('onlyoffice', 'http://onlyoffice', {
      deps: deps({ loadScript: async () => 'error', reachable: async () => false }),
    });
    expect(r.state).toBe('unreachable');
  });

  it('an error from an address that DOES answer is "wrong thing", not "cannot reach"', async () => {
    const r = await probeExternalFromBrowser('onlyoffice', 'https://example.com', {
      deps: deps({ loadScript: async () => 'error', reachable: async () => true }),
    });
    expect(r.state).toBe('wrong-content');
  });

  it('a black hole times out as its own state and never hangs', async () => {
    const r = await probeExternalFromBrowser('onlyoffice', 'http://10.255.255.1:8080', {
      deps: deps({ loadScript: async () => 'timeout' }),
      timeoutMs: 50,
    });
    expect(r.state).toBe('timeout');
  });

  it('mixed content is named, not reported as unreachable', async () => {
    // The browser blocks it before a packet leaves; "unreachable" would send
    // the operator to check firewalls that are fine.
    const r = await probeExternalFromBrowser('onlyoffice', 'http://office.example.com', {
      deps: deps({ pageProtocol: () => 'https:' }),
    });
    expect(r.state).toBe('blocked-mixed-content');
  });

  it('an https service on an https page is not mistaken for mixed content', async () => {
    const r = await probeExternalFromBrowser('onlyoffice', 'https://office.example.com', {
      deps: deps({
        pageProtocol: () => 'https:',
        loadScript: async () => 'load',
        hasDocsAPI: () => true,
      }),
    });
    expect(r.state).toBe('ok');
  });
});

describe('probeExternalFromBrowser — drawio', () => {
  it('the embed handshake is the proof', async () => {
    const r = await probeExternalFromBrowser('drawio', 'https://draw.example.com', {
      deps: deps({ loadFramed: async () => 'ready' }),
    });
    expect(r.state).toBe('ok');
    expect(r.mechanism).toBe('iframe');
  });

  it('no handshake plus no network is unreachable', async () => {
    const r = await probeExternalFromBrowser('drawio', 'http://drawio', {
      deps: deps({ loadFramed: async () => 'timeout', reachable: async () => false }),
    });
    expect(r.state).toBe('unreachable');
  });

  it('no handshake from an address that answers is the wrong server', async () => {
    const r = await probeExternalFromBrowser('drawio', 'https://example.com', {
      deps: deps({ loadFramed: async () => 'timeout', reachable: async () => true }),
    });
    expect(r.state).toBe('wrong-content');
  });
});

describe('probeExternalFromBrowser — what it refuses to judge', () => {
  it('skips a service with no URL rather than calling it broken', async () => {
    const r = await probeExternalFromBrowser('onlyoffice', '', { deps: deps({}) });
    expect(r.state).toBe('skipped');
  });

  it('skips a service the browser never loads', async () => {
    // Reporting "unreachable" for the converter — which only filex ever calls —
    // would be a new lie in place of the old one.
    const r = await probeExternalFromBrowser('convert', 'http://convert:8080', {
      deps: deps({}),
    });
    expect(r.state).toBe('skipped');
    expect(r.mechanism).toBe('none');
  });
});
