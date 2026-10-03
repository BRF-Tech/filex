// @vitest-environment jsdom
//
// ⚠ jsdom, not happy-dom: the notes go through DOMPurify, and happy-dom's
// NodeIterator loses its place when a node is removed under it (see
// previewSanitize.test.ts) - a sanitizer test there can pass over a vector it
// never visited.
//
// An app's release notes on Admin -> Apps -> "Review update" are Markdown - a
// GitHub release's body. They are drawn through the explorer preview's own
// pipeline (core lib/markdownHtml: markdown-it, then the document sanitizer),
// not a renderer of the panel's own, and never as the raw string in v-html.
//
// RED PROOF (task #122, measured on 0.48.1 with filextext-app 0.1.1): the
// review printed the body as plain text, so `**bold**` reached the reader as
// asterisks and a list as dashes.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const WIRE = path.resolve(__dirname, '../../../backend/internal/api/handlers/testdata/wire');
let dryRunAnswer: Record<string, unknown> = {};

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    post: vi.fn(async (_url: string, _body: unknown, cfg?: { params?: Record<string, unknown> }) => {
      if (cfg?.params?.dry_run) return { data: dryRunAnswer };
      return { data: { id: 7, name: 'sign', label: { en: 'e-Signature' }, permissions: [] } };
    }),
    get: vi.fn(),
  },
}));

import AppPluginInstallWizard from '@/components/plugins/AppPluginInstallWizard.vue';

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () {
    this.setAttribute('open', '');
  };
  HTMLDialogElement.prototype.close = function () {
    this.removeAttribute('open');
  };
}

const sign = { id: 7, name: 'sign', version: '1.1.0', label: { en: 'e-Signature' }, permissions: [], source: 'github', source_url: 'https://github.com/BRF-Tech/filex-sign@v1.1.0' };

/** The server's own upgrade review (app_plugins_wire_test.go writes it), with `notes`. */
function reviewWith(notes: string): Record<string, unknown> {
  const review = JSON.parse(readFileSync(path.join(WIRE, 'app-plugin-upgrade-review.json'), 'utf8'));
  review.compat.ok = true;
  review.upgrade.notes = notes;
  return review;
}

async function openReview(notes: string, locale = 'en') {
  dryRunAnswer = reviewWith(notes);
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const w = mount(AppPluginInstallWizard, {
    props: { modelValue: false, requiresSignature: false, update: sign as never, installed: [sign] as never },
    global: { plugins: [i18n] },
    attachTo: document.body,
  });
  await w.setProps({ modelValue: true });
  await vi.waitFor(
    async () => {
      await flushPromises();
      expect(w.find('[data-testid="app-plugin-upgrade-notes-md"]').exists()).toBe(true);
    },
    { timeout: 5000 },
  );
  return w;
}

const NOTES = [
  '## 0.1.1',
  '',
  '- **Kalın** bir düzeltme: `Ctrl+S` artık kaydediyor',
  '- [Full Changelog](https://github.com/BRF-Tech/filextext-app/compare/v0.1.0...v0.1.1)',
  '',
  'Fixed <img src="x.png" onerror="window.pwned=1"> and <script>window.pwned=2</script> too.',
].join('\n');

describe('"Review update": the release notes are Markdown, drawn safely', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    delete (window as unknown as { pwned?: number }).pwned;
  });

  it('renders the Markdown: bold is bold, a list is a list, code and links are what they say', async () => {
    const w = await openReview(NOTES);
    const notes = w.find('[data-testid="app-plugin-upgrade-notes-md"]');
    expect(notes.find('h2').text()).toBe('0.1.1');
    expect(notes.find('strong').text()).toBe('Kalın');
    expect(notes.findAll('li')).toHaveLength(2);
    expect(notes.find('code').text()).toBe('Ctrl+S');
    expect(notes.find('a').attributes('href')).toBe('https://github.com/BRF-Tech/filextext-app/compare/v0.1.0...v0.1.1');
    expect(notes.text()).not.toContain('**');
    // The heading of the group is still the reader's language.
    expect(w.find('[data-testid="app-plugin-upgrade-notes"] h4').text()).toBe(en.appPlugins.wizard.diff.notes);
  });

  it('nothing in the notes runs: no script, no event attribute', async () => {
    const w = await openReview(NOTES, 'tr');
    const notes = w.find('[data-testid="app-plugin-upgrade-notes-md"]');
    expect(notes.find('script').exists()).toBe(false);
    const img = notes.find('img');
    expect(img.exists()).toBe(true);
    expect(img.attributes('onerror')).toBeUndefined();
    expect(notes.html()).not.toMatch(/onerror|<script|javascript:/i);
    expect((window as unknown as { pwned?: number }).pwned).toBeUndefined();
    expect(w.find('[data-testid="app-plugin-upgrade-notes"] h4').text()).toBe('Sürüm notları');
  });
});
