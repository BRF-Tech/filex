// `filex 0.43.0` — which filex this is, where a person can find it.
//
// Burak, 2026-09-24: in the account menu or in user settings. The version was
// only on the sign-in page and the administrators' About page, so anybody
// already signed in who is not an administrator had no way to say which
// version they were on.
import { afterEach, describe, expect, it } from 'vitest';
import { mount, type VueWrapper } from '@vue/test-utils';
import { readFileSync } from 'node:fs';
import path from 'node:path';

import ProductVersion from '@brftech/filex-core/src/components/ProductVersion.vue';
import { PRODUCT_NAME, productVersionLine } from '@brftech/filex-core/src/lib/productVersion';

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

  it('says nothing while the version is not known — never the client’s placeholder', () => {
    // `0.0.0` is what the capabilities store holds before the server answers;
    // a menu opened in that moment must not announce a version that never was.
    for (const v of ['', '0.0.0', null, undefined]) {
      expect(productVersionLine(v), String(v)).toBe('');
    }
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
  /* The three places Burak asked for, held to the ONE piece: a menu that grew
     its own `filex {{ version }}` would be a second spelling of one line, and
     a refactor that dropped the line would lose it without a sound. */
  const SRC = path.resolve(__dirname, '../../src/components');
  for (const f of ['TopNav.vue', 'AccountMenu.vue', 'UserSettingsModal.vue']) {
    it(`${f} draws ProductVersion from the server’s capabilities`, () => {
      const src = readFileSync(path.join(SRC, f), 'utf8');
      expect(src, `${f} does not draw the version line`).toMatch(/<ProductVersion\b[^>]*:version="caps\.data\.version"/);
      expect(src, `${f} spells the line out itself`).not.toMatch(/filex \{\{\s*caps\.data\.version/);
    });
  }

  it('adds no catalogue key — a name and a number need no translation', () => {
    const src = readFileSync(
      path.resolve(__dirname, '../../../packages/core/src/components/ProductVersion.vue'),
      'utf8',
    );
    expect(src).not.toMatch(/\bt\(/);
  });
});
