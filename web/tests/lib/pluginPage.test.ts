// Where an app plugin's `page` view lives.
//
// ⚠⚠ The base is the whole test. The same bundle is served from `/admin/`,
// `/drive/` and `/p/`, and ONLY those prefixes fall back to index.html
// (backend routes.go → wireStatic) — so the contract's `/apps/{plugin}/{view}`
// is relative to the mount, and a bare `/apps/…` is a server 404 rather than a
// wizard. A trailing slash on one side and not the other is how that turns
// into `//apps/…`, which is a protocol-relative URL to a host called "apps".
import { describe, expect, it } from 'vitest';

import { PLUGIN_PAGE_SEGMENT, isPagePlacement, pluginPagePath, pluginPageUrl } from '@brftech/filex-core';

describe('a plugin page address', () => {
  it('is the segment, the plugin, the view and the file', () => {
    expect(PLUGIN_PAGE_SEGMENT).toBe('apps');
    expect(pluginPagePath({ plugin: 'sign', view: 'wizard', path: 'docs://reports/nda.pdf' })).toBe(
      'apps/sign/wizard?path=docs%3A%2F%2Freports%2Fnda.pdf',
    );
    // A `home`-ish page opens on nothing at all.
    expect(pluginPagePath({ plugin: 'sign', view: 'envelopes' })).toBe('apps/sign/envelopes');
  });

  it('escapes every part, so a name with a slash cannot become a path', () => {
    expect(pluginPagePath({ plugin: 'a/b', view: 'c d' })).toBe('apps/a%2Fb/c%20d');
  });

  it('joins the mount base with exactly one slash', () => {
    const target = { plugin: 'sign', view: 'wizard' };
    expect(pluginPageUrl('/admin/', target)).toBe('/admin/apps/sign/wizard');
    expect(pluginPageUrl('/admin', target)).toBe('/admin/apps/sign/wizard');
    expect(pluginPageUrl('/drive/', target)).toBe('/drive/apps/sign/wizard');
    expect(pluginPageUrl('https://fm.example.com/admin/', target)).toBe('https://fm.example.com/admin/apps/sign/wizard');
    // An empty base is a host that serves the SPA at the root.
    expect(pluginPageUrl('', target)).toBe('/apps/sign/wizard');
  });

  it('only `page` opens a tab; everything else stays a dialog', () => {
    expect(isPagePlacement('page')).toBe(true);
    expect(isPagePlacement('modal')).toBe(false);
    expect(isPagePlacement(undefined)).toBe(false);
    expect(isPagePlacement('')).toBe(false);
  });
});
