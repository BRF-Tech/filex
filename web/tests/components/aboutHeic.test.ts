// About → Thumbnail tools: HEIC is a row of its own.
//
// ⚠ Found in the 0.50 test phase: on Ubuntu 24.04 ImageMagick was installed
// and libheif had no HEVC decoder (libheif-plugin-libde265 is only suggested
// by libheif1), so About said "ImageMagick: Found", the capabilities said
// HEIC could be drawn, and every phone photo failed. The server now decodes a
// sample HEIC (enginebin.HEIC) and publishes the answer as `heic`; About
// shows it beside ImageMagick and says what to install when ImageMagick is
// there and HEIC is not.
import { describe, expect, it, beforeEach } from 'vitest';
import { mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import About from '@/views/About.vue';
import { useCapabilitiesStore } from '@/stores/capabilities';

function mountAbout(caps: { imagemagick: boolean; heic: boolean }, locale: 'en' | 'tr' = 'en') {
  const store = useCapabilitiesStore();
  store.data = { ...store.data, ...caps };
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } as never });
  return mount(About, { global: { plugins: [i18n] } });
}

beforeEach(() => {
  setActivePinia(createPinia());
});

describe('About: HEIC photos', () => {
  it('is its own row: ImageMagick found, HEIC not, and what to install', () => {
    const w = mountAbout({ imagemagick: true, heic: false });
    expect(w.find('[data-testid="about-tool-ImageMagick"]').text()).toBe('Found');
    const heic = w.find('[data-testid="about-tool-HEIC"]');
    expect(heic.exists()).toBe(true);
    expect(heic.text()).toBe('Not found');
    expect(heic.element.parentElement?.textContent).toContain('HEIC photos');
    expect(w.find('[data-testid="about-heic-hint"]').text()).toContain('libheif-plugin-libde265');
  });

  it('says nothing more when HEIC decodes', () => {
    const w = mountAbout({ imagemagick: true, heic: true });
    expect(w.find('[data-testid="about-tool-HEIC"]').text()).toBe('Found');
    expect(w.find('[data-testid="about-heic-hint"]').exists()).toBe(false);
  });

  it('without ImageMagick the ImageMagick row says it, not a decoder hint', () => {
    const w = mountAbout({ imagemagick: false, heic: false });
    expect(w.find('[data-testid="about-tool-HEIC"]').text()).toBe('Not found');
    expect(w.find('[data-testid="about-heic-hint"]').exists()).toBe(false);
  });

  it('in Turkish', () => {
    const w = mountAbout({ imagemagick: true, heic: false }, 'tr');
    expect(w.find('[data-testid="about-tool-HEIC"]').element.parentElement?.textContent).toContain('HEIC fotoğrafları');
    expect(w.find('[data-testid="about-heic-hint"]').text()).toContain('HEVC çözücüsü eksik');
  });

  // Four fixed columns broke "HEIC fotoğrafları" + "Bulunamadı" onto two
  // lines at 1440 px. happy-dom lays nothing out, so the browser measures the
  // lines (0.50 test phase, 390/958/1440 px, en and tr); this holds the two
  // things that make it hold: as many columns as fit, and a name that does
  // not break.
  it('the grid fits its columns to the names; a name never breaks', () => {
    const w = mountAbout({ imagemagick: true, heic: false }, 'tr');
    const grid = w.find('[data-testid="about-tools"]');
    expect(grid.classes().join(' ')).toMatch(/grid-cols-\[repeat\(auto-fill,minmax\(\d+rem,1fr\)\)\]/);
    expect(grid.classes().join(' ')).not.toMatch(/(^|\s)(sm:)?grid-cols-\d/);
    for (const li of grid.findAll('li')) expect(li.find('span').classes()).toContain('whitespace-nowrap');
  });
});

/* 0.50: office documents get their thumbnails from OnlyOffice and nothing
 * else (LibreOffice left the image): the row says whether it is configured,
 * and where to configure it when it is not. */
describe('About: office documents', () => {
  function mountOffice(office: boolean | undefined, locale: 'en' | 'tr' = 'en') {
    const store = useCapabilitiesStore();
    store.data = { ...store.data, imagemagick: true, heic: true, thumbs: office === undefined ? undefined : { office } };
    const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } as never });
    return mount(About, { global: { plugins: [i18n] } });
  }

  it('OnlyOffice configured: found, and no hint', () => {
    const w = mountOffice(true);
    const row = w.find('[data-testid="about-tool-Office"]');
    expect(row.text()).toBe('Found');
    expect(row.element.parentElement?.textContent).toContain('Office documents (ONLYOFFICE)');
    expect(w.find('[data-testid="about-office-hint"]').exists()).toBe(false);
    expect(w.find('[data-testid="about-tool-LibreOffice"]').exists(), 'LibreOffice draws nothing now').toBe(false);
  });

  it('not configured (or an older server): not found, and where to configure it', () => {
    for (const office of [false, undefined]) {
      const w = mountOffice(office);
      expect(w.find('[data-testid="about-tool-Office"]').text()).toBe('Not found');
      expect(w.find('[data-testid="about-office-hint"]').text()).toContain('Settings → External services');
    }
  });

  it('in Turkish', () => {
    const w = mountOffice(false, 'tr');
    expect(w.find('[data-testid="about-tool-Office"]').element.parentElement?.textContent).toContain('Ofis belgeleri (ONLYOFFICE)');
    expect(w.find('[data-testid="about-office-hint"]').text()).toContain('Dış servisler');
  });
});
