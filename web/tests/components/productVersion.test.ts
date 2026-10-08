// `filex 0.43.0` — which filex this is, where a person can find it.
//
// The maintainer, 2026-09-24: in the account menu or in user settings. The version was
// only on the sign-in page and the administrators' About page, so anybody
// already signed in who is not an administrator had no way to say which
// version they were on.
import { afterEach, describe, expect, it } from 'vitest';
import { mount, type VueWrapper } from '@vue/test-utils';
import { readFileSync } from 'node:fs';
import path from 'node:path';

import ProductVersion from '@brftech/filex-core/src/components/ProductVersion.vue';
import * as productVersion from '@brftech/filex-core/src/lib/productVersion';
import { PRODUCT_NAME, productVersionLine, shortCommit } from '@brftech/filex-core/src/lib/productVersion';

let open: VueWrapper | null = null;
afterEach(() => {
  open?.unmount();
  open = null;
});

describe('productVersionLine — one spelling of the line', () => {
  it('names the software and the server’s own version', () => {
    expect(PRODUCT_NAME).toBe('filex');
    expect(productVersionLine('0.43.0')).toBe('filex 0.43.0');
  });

  it('keeps the server’s string as it is — a development build says so', () => {
    expect(productVersionLine('0.1.0-dev')).toBe('filex 0.1.0-dev');
    expect(productVersionLine('  0.43.0 ')).toBe('filex 0.43.0');
  });

  /* ⚠ The server's one-line string (version.String) is the release, then the
     commit and the build time in brackets; printed whole, the avatar menu
     grew a sideways scroll bar (the maintainer, 2026-09-26). Since 0.54
     (#211, audit A11) the server sends `release` apart, and the line is built
     from that: nothing on the client parses the one-line string any more. */
  it('is built from the release the server sends apart, and parses nothing', () => {
    expect(productVersionLine('v0.46.0')).toBe('filex v0.46.0');
    expect('parseServerVersion' in productVersion, 'the parser is gone').toBe(false);
  });

  it('says nothing while the version is not known — never the client’s placeholder', () => {
    // `0.0.0` is what the capabilities store holds before the server answers;
    // a menu opened in that moment must not announce a version that never was.
    for (const v of ['', '0.0.0', null, undefined]) {
      expect(productVersionLine(v), String(v)).toBe('');
    }
  });
});

describe('shortCommit', () => {
  it('shortens a commit to the seven characters git itself shows', () => {
    expect(shortCommit('a2d7e34d1971707c638a5a44756685f1cd010bd6')).toBe('a2d7e34');
    expect(shortCommit('abc1234')).toBe('abc1234');
    expect(shortCommit('')).toBe('');
  });
});

describe('ProductVersion — the quiet line the menus draw', () => {
  it('draws the line, isolated, so it reads the same in a right-to-left menu', () => {
    open = mount(ProductVersion, { props: { version: '0.43.0' } });
    const p = open.find('[data-testid="product-version"]');
    expect(p.exists()).toBe(true);
    expect(p.classes()).toContain('fe-version');
    expect(p.find('bdi').text()).toBe('filex 0.43.0');
  });

  it('draws nothing at all while the version is unknown', () => {
    open = mount(ProductVersion, { props: { version: '0.0.0' } });
    expect(open.find('[data-testid="product-version"]').exists()).toBe(false);
  });
});

describe('where a person finds it', () => {
  /* The three places the maintainer asked for, held to the ONE piece: a menu that grew
     its own `filex {{ version }}` would be a second spelling of one line, and
     a refactor that dropped the line would lose it without a sound. */
  const SRC = path.resolve(__dirname, '../../src/components');
  for (const f of ['TopNav.vue']) {
    it(`${f} draws ProductVersion from the server’s capabilities`, () => {
      const src = readFileSync(path.join(SRC, f), 'utf8');
      expect(src, `${f} does not draw the version line`).toMatch(/<ProductVersion\b[^>]*:version="caps\.data\.release"/);
      expect(src, `${f} spells the line out itself`).not.toMatch(/filex \{\{\s*caps\.data\.version/);
    });
  }

  /* The explorer page's avatar is core's AccountMenu since 2026-09-27 (the
     desktop app draws the same one): the web file hands it the server's
     version, and core draws the line with the same piece. */
  it('the avatar hands core the server’s version, and core draws ProductVersion from it', () => {
    const web = readFileSync(path.join(SRC, 'AccountMenu.vue'), 'utf8');
    expect(web).toMatch(/<AccountMenu\b[^>]*:version="caps\.data\.release"/);
    expect(web).not.toMatch(/filex \{\{/);
    const core = readFileSync(path.resolve(__dirname, '../../../packages/core/src/components/AccountMenu.vue'), 'utf8');
    expect(core).toMatch(/<ProductVersion\b[^>]*:version="version"/);
    expect(core).not.toMatch(/filex \{\{/);
  });

  /* The settings dialog is core's UserSettingsDialog since 2026-09-27 (the
     desktop app opens the same one in its window): the web file hands it the
     server's capabilities, and core draws the line with the same piece. */
  it('the settings dialog hands core the server’s capabilities, and core draws ProductVersion from them', () => {
    const web = readFileSync(path.join(SRC, 'UserSettingsModal.vue'), 'utf8');
    expect(web).toMatch(/get capabilities\(\) \{\s*return caps\.data;/);
    const core = readFileSync(path.resolve(__dirname, '../../../packages/core/src/components/UserSettingsDialog.vue'), 'utf8');
    expect(core).toMatch(/<ProductVersion\b[^>]*:version="host\.capabilities\?\.release"/);
    expect(core).not.toMatch(/filex \{\{/);
  });

  /* The sign-in page and the About page printed the server's whole string
     themselves — the same 40-digit commit, twice on sign-in. */
  it('the sign-in page draws the same line, not the server’s whole string', () => {
    const src = readFileSync(path.resolve(__dirname, '../../src/views/Login.vue'), 'utf8');
    expect(src).not.toMatch(/filex \{\{\s*caps\.data\.version/);
    expect(src).toMatch(/productVersionLine\(caps\.data\.release\)/);
  });

  it('the About page shows the release, and the commit short', () => {
    const src = readFileSync(path.resolve(__dirname, '../../src/views/About.vue'), 'utf8');
    expect(src).not.toMatch(/\{\{\s*data\.version\s*\}\}/);
    expect(src).not.toMatch(/parseServerVersion\(/);
    expect(src).toMatch(/data\.value\.release/);
    expect(src).toMatch(/shortCommit\(/);
  });

  it('adds no catalogue key — a name and a number need no translation', () => {
    const src = readFileSync(
      path.resolve(__dirname, '../../../packages/core/src/components/ProductVersion.vue'),
      'utf8',
    );
    expect(src).not.toMatch(/\bt\(/);
  });
});
