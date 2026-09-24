// Corporate identity's live preview IS the public page.
//
// ⚠⚠ Release-candidate sweep, 2026-09-21 (QA #25): the preview was a hand-drawn
// mock — a centred card, an icon and a full-width button reading "1.2 MB" —
// while the page it previewed is left-aligned and plain. It now mounts the
// public shell and share body `/s/<token>` mounts (core PublicLinkPreview),
// fed the unsaved form values.
import { describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f?: string) => f ?? 'error',
  api: { get: vi.fn(async () => ({ data: {} })), patch: vi.fn(async () => ({ data: {} })) },
}));

import Branding from '@/views/Branding.vue';
import { useSettingsStore } from '@/stores/settings';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

async function mountBranding(values: Record<string, string>) {
  const pinia = createPinia();
  setActivePinia(pinia);
  const st = useSettingsStore();
  st.fetch = vi.fn(async () => {}) as typeof st.fetch;
  st.data = values;
  const i18n = createI18n({ legacy: false, locale: 'tr', fallbackLocale: 'en', messages: { en, tr } });
  const w = mount(Branding, { global: { plugins: [pinia, i18n] }, attachTo: document.body });
  await flushPromises();
  return w;
}

describe('Corporate identity preview', () => {
  it('draws the real public page with the unsaved values', async () => {
    const w = await mountBranding({
      'branding.name': 'Acme Bulut',
      'branding.accent': '#aa3355',
      'branding.footer_text': '© Acme',
      'branding.hide_powered_by': 'false',
    });
    const preview = w.find('[data-testid="public-link-preview"]');
    expect(preview.exists()).toBe(true);
    // The public shell itself — the component `/s/<token>` mounts.
    const shell = preview.find('[data-testid="public-page"]');
    expect(shell.exists(), 'the preview is the public shell, not a mock').toBe(true);
    expect(preview.find('[data-testid="public-brand"]').text()).toContain('Acme Bulut');
    expect(preview.find('[data-testid="public-share-download"]').exists()).toBe(true);
    expect(preview.find('[data-testid="public-footer-text"]').text()).toBe('© Acme');
    // The accent reaches the shell the way the page's own branding does —
    // ⚠ the TINT and the ink on it too, not only the solid button. With only
    // `--fe-primary` the operator's colour lit the Download button while the
    // round badge, the PIN box's focus ring and the row hover stayed the
    // stock blue (measured on the restored gate, 2026-09-23).
    const style = shell.attributes('style') ?? '';
    expect(style).toContain('--fe-primary: #aa3355');
    expect(style).toContain('--fe-primary-soft: rgba(170, 51, 85, 0.14)');
    expect(style).toContain('--fe-primary-ink: #aa3355');
    // A preview is looked at: its controls do not act inside the admin page.
    expect(preview.attributes('inert')).toBeDefined();
    w.unmount();
  });

  it('follows the form as it is typed, before anything is saved', async () => {
    const w = await mountBranding({});
    const name = w.findAll('input').find((i) => i.attributes('placeholder') === 'Acme Cloud');
    expect(name).toBeTruthy();
    await name!.setValue('Yeni Ad');
    expect(w.find('[data-testid="public-brand"]').text()).toContain('Yeni Ad');
    const powered = () => w.find('[data-testid="public-footer-powered"]');
    expect(powered().exists()).toBe(true);
    w.unmount();
  });
});
