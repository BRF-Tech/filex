// The ADMIN's own app-settings screen (Plugins → Apps → Details → Settings),
// held to the same rule as every other plugin surface.
//
// ⚠⚠ This screen had a mapper of its OWN (`api/appPlugins.fieldToStorageField`)
// beside the core package's `storageFieldOf`, so one manifest field meant two
// different things on two screens: a `select` was a row of buttons inside the
// plugin's view and a native dropdown here, and a `multi` select silently kept
// a single value. Neither installed plugin happened to declare such a setting,
// so nothing on screen said so — the next plugin would have found out. The
// mapper is gone; these tests are why it stays gone.
//
// ⚠ The old mapper also carried `advanced`, which would have folded the field
// into a collapsed block. Measured 2026-09-20 on a real instance: a manifest
// that declares `advanced` is refused at install outright — `400
// manifest_invalid: json: unknown field "advanced"` — so the fold was not
// reachable THROUGH A MANIFEST. It is asserted anyway, because a mapper that
// carries a key the contract deleted is one call site away from drawing it.
//
// ⚠ The storage DRIVER form (`StorageDriverFields`) is NOT covered by this:
// it is our own admin chrome and keeps its dropdown and its advanced block
// (tests/components/storageDriverFields.test.ts locks that side).
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const row = {
  id: 7,
  name: 'throwaway',
  version: '0.1.0',
  label: { en: 'Throwaway' },
  enabled: true,
  state: 'running',
  source: 'upload',
  signed: false,
  permissions: [],
  actions: 0,
  views: 0,
  public_pages: 0,
  created_at: '',
  updated_at: '',
};

// ⚠ Everything the `vi.mock` factory reads lives in `vi.hoisted`: the factory
// is lifted above the file's own `const`s, so a plain top-level fixture is
// still uninitialised when the mocked module is first imported.
const fx = vi.hoisted(() => {
  /** A manifest that declares exactly the three shapes that used to break. */
  const manifest = {
    manifest_version: 1,
    name: 'throwaway',
    version: '0.1.0',
    label: { en: 'Throwaway' },
    permissions: [],
    actions: [],
    views: [],
    settings: [
      {
        key: 'mode',
        type: 'select',
        label: { en: 'Where the output goes' },
        options: [
          { value: 'version', label: { en: 'A new version' } },
          { value: 'sibling', label: { en: 'A new file beside it' } },
        ],
        default: 'version',
      },
      {
        key: 'formats',
        type: 'select',
        multi: true,
        label: { en: 'Formats to handle' },
        options: [{ value: 'pdf', label: { en: 'PDF' } }, { value: 'docx', label: { en: 'Word' } }],
      },
      {
        key: 'tsa_enabled',
        type: 'bool',
        style: 'choice',
        label: { en: 'Add a time stamp?' },
      },
      // A field in the shape the old mapper folded away. The server would
      // refuse this key today (see the header); the mapper must refuse it too.
      { key: 'endpoint', type: 'string', label: { en: 'Endpoint' }, advanced: true },
    ],
  };
  return {
    manifest,
    put: vi.fn(async () => ({ data: {} })),
    stored: {} as Record<string, string>,
  };
});

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      if (url.endsWith('/logs')) return { data: { lines: [], next: 0 } };
      if (url.endsWith('/locks')) return { data: { locks: [] } };
      if (url === '/admin/app-plugins/7') {
        return {
          data: {
            ...row,
            manifest: fx.manifest,
            granted: [],
            overrides: [],
            settings: fx.stored,
          },
        };
      }
      return { data: {} };
    }),
    post: vi.fn(),
    patch: vi.fn(),
    put: fx.put,
    delete: vi.fn(),
  },
}));

import AppPluginDetail from '@/components/plugins/AppPluginDetail.vue';

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); };
}

async function openDetail(locale = 'en') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const w = mount(AppPluginDetail, { props: { plugin: row }, global: { plugins: [i18n] } });
  await flushPromises();
  return w;
}

describe('app settings obey the surface rule', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    fx.stored = {};
    fx.put.mockClear();
  });

  it('a `select` setting is a row of buttons, not a dropdown', async () => {
    const w = await openDetail();
    // ⚠ The whole drawer, not just the settings section: there must be no
    // `<select>` anywhere a plugin's declaration can reach.
    expect(w.findAll('select')).toHaveLength(0);
    const labels = w.findAll('[data-testid^="fe-choice-mode-"]').map((b) => b.text());
    expect(labels).toEqual(['A new version', 'A new file beside it']);
    expect(w.find('[data-testid="fe-choice-mode-version"]').attributes('aria-checked')).toBe('true');

    await w.find('[data-testid="fe-choice-mode-sibling"]').trigger('click');
    expect(w.find('[data-testid="fe-choice-mode-sibling"]').attributes('aria-checked')).toBe('true');
  });

  it('a `multi` select keeps EVERY answer, and stores them all', async () => {
    const w = await openDetail();
    await w.find('[data-testid="fe-choice-formats-pdf"]').trigger('click');
    await w.find('[data-testid="fe-choice-formats-docx"]').trigger('click');
    // Both pressed — the old mapper dropped `multi`, so the second click
    // replaced the first and the setting could only ever hold one value.
    expect(w.find('[data-testid="fe-choice-formats-pdf"]').attributes('aria-pressed')).toBe('true');
    expect(w.find('[data-testid="fe-choice-formats-docx"]').attributes('aria-pressed')).toBe('true');

    await w.find('[data-testid="app-plugin-save-settings"]').trigger('click');
    await flushPromises();
    expect(fx.put).toHaveBeenCalled();
    expect(fx.put.mock.calls[0][1]).toMatchObject({ values: { formats: 'pdf,docx' } });
  });

  it('a `bool` the plugin declared as a decision is two buttons here too', async () => {
    const w = await openDetail();
    expect(w.find('[data-testid="fe-choice-tsa_enabled-true"]').text()).toBe('Yes');
    expect(w.find('[data-testid="fe-choice-tsa_enabled-false"]').text()).toBe('No');
    expect(w.findAll('input[type="checkbox"]')).toHaveLength(0);

    await w.find('[data-testid="fe-choice-tsa_enabled-true"]').trigger('click');
    await w.find('[data-testid="app-plugin-save-settings"]').trigger('click');
    await flushPromises();
    expect(fx.put.mock.calls[0][1]).toMatchObject({ values: { tsa_enabled: 'true' } });
  });

  it('there is no folded section: `advanced` never reaches a plugin field', async () => {
    const w = await openDetail();
    // Neither renderer's disclosure: the core form's (`fe-cfield__adv`) nor
    // the driver form's, whose words would be these.
    expect(w.find('.fe-cfield__adv').exists()).toBe(false);
    expect(w.text()).not.toContain(en.storages.advancedFields);
    expect(w.text()).not.toContain('Advanced settings');
    // …and the field itself is on the screen, where a field that matters goes.
    expect(w.find('#fe-cf-endpoint').exists()).toBe(true);
  });

  it('stored strings come back as the shapes their fields hold', async () => {
    // The wire is `map[string]string` (the contract); a `"true"` that arrives
    // at a bool as a string is neither answer, and a comma-joined list is one
    // unknown option. Read back once, on the way in.
    fx.stored = { tsa_enabled: 'true', formats: 'pdf,docx', mode: 'sibling' };
    const w = await openDetail();
    expect(w.find('[data-testid="fe-choice-tsa_enabled-true"]').attributes('aria-checked')).toBe('true');
    expect(w.find('[data-testid="fe-choice-formats-pdf"]').attributes('aria-pressed')).toBe('true');
    expect(w.find('[data-testid="fe-choice-formats-docx"]').attributes('aria-pressed')).toBe('true');
    expect(w.find('[data-testid="fe-choice-mode-sibling"]').attributes('aria-checked')).toBe('true');
  });
});
