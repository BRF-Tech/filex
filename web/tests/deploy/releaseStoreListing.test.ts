// The Microsoft Store gate's fallback (storeListing in scripts/release/verify.mjs):
// a release whose tag run published nothing — 0.52.0 went out from a PC during
// a GitHub Actions outage and its bundle was submitted by hand — is checked
// against the Store's public listing instead of a run job that does not exist.

import { createRequire } from 'node:module';
import { describe, expect, it } from 'vitest';

import { storeListing } from '../../../scripts/release/verify.mjs';

const { storeVersion } = createRequire(import.meta.url)('../../../desktop/scripts/appx-manifest.cjs');

const ID = '9PKXDJLVZWXW';
const IDENTITY = 'BRFTech.filexapp';

/** A displaycatalog answer offering the given package full names and arches. */
function catalog(packages: { name: string; arches: string[] }[], status = 200) {
  return async () =>
    new Response(
      JSON.stringify({
        Products: [
          {
            DisplaySkuAvailabilities: [
              { Sku: { Properties: { Packages: packages.map((p) => ({ PackageFullName: p.name, Architectures: p.arches })) } } },
            ],
          },
        ],
      }),
      { status },
    );
}

const pkg = (v: string) => `${IDENTITY}_${v}_neutral_~_ghmyjhcwfex1m`;

describe('storeListing', () => {
  it('maps 0.52.0 to the Store version the package carries', () => {
    expect(storeVersion('0.52.0')).toBe('1.0.5200.0');
  });

  it('passes when the Store offers this version for x64 and arm64', async () => {
    const r = await storeListing(ID, IDENTITY, '1.0.5200.0', ['x64', 'arm64'], {
      fetch: catalog([{ name: pkg('1.0.5200.0'), arches: ['x64', 'arm64'] }]),
    });
    expect(r).toEqual({ ok: true, detail: 'the Store offers BRFTech.filexapp 1.0.5200.0 (x64 + arm64)' });
  });

  it('fails while the Store still offers the previous version (certification pending)', async () => {
    const r = await storeListing(ID, IDENTITY, '1.0.5200.0', ['x64', 'arm64'], {
      fetch: catalog([{ name: pkg('1.0.5100.0'), arches: ['x64', 'arm64'] }]),
    });
    expect(r.ok).toBe(false);
    expect(r.detail).toContain('offers BRFTech.filexapp 1.0.5100.0, not 1.0.5200.0');
  });

  it('fails when an architecture is missing from this version', async () => {
    const r = await storeListing(ID, IDENTITY, '1.0.5200.0', ['x64', 'arm64'], {
      fetch: catalog([{ name: pkg('1.0.5200.0'), arches: ['x64'] }]),
    });
    expect(r).toEqual({ ok: false, detail: 'the Store offers BRFTech.filexapp 1.0.5200.0 without arm64' });
  });

  it('does not take another product with a longer name for this one', async () => {
    const r = await storeListing(ID, IDENTITY, '1.0.5200.0', ['x64'], {
      fetch: catalog([{ name: `${IDENTITY}beta_1.0.5200.0_neutral_~_x`, arches: ['x64'] }]),
    });
    expect(r.ok).toBe(false);
  });

  it('fails on an HTTP error and on a network error', async () => {
    const http = await storeListing(ID, IDENTITY, '1.0.5200.0', ['x64'], { fetch: catalog([], 503) });
    expect(http).toEqual({ ok: false, detail: expect.stringContaining('answered HTTP 503') });
    const net = await storeListing(ID, IDENTITY, '1.0.5200.0', ['x64'], {
      fetch: async () => {
        throw new TypeError('fetch failed', { cause: { code: 'ENOTFOUND' } });
      },
    });
    expect(net).toEqual({ ok: false, detail: expect.stringContaining('could not check') });
  });
});
