// tema:v1 — the admin Appearance screen.
//
// What is worth testing here is not that the fields render. It is the four
// things that would fail silently:
//
//  1. THE SCREEN SUSPENDS THE OPERATOR STYLESHEET WHILE IT IS OPEN. This is
//     the guarantee that nobody can lock themselves out — the one guard that
//     survives a sheet writing `:root { display: none }`, which no amount of
//     selector scoping can undo. If the `onMounted` call is ever dropped, the
//     screen still looks perfect in every other test.
//  2. IT CARRIES THE IMMUNE CLASS the server's `@scope … to (.fe-css-immune)`
//     wrapper names. Same failure mode: invisible until somebody is locked out.
//  3. WHAT IT SAVES IS THE DERIVED PALETTE, not the twelve authored fields.
//     Posting the authored twelve would be refused by the server for a missing
//     token, and posting them unvalidated would be worse.
//  4. THE SWITCH IS OFF BY DEFAULT and one click removes the sheet.
import { describe, expect, it, beforeEach, vi } from 'vitest';
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import { closeRowMenus, menuEntries, openRowMenu, pickMenuItem } from '../helpers/rowMenu';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const list = vi.fn();
const put = vi.fn();
const remove = vi.fn();
const boot = vi.fn();
const invalidateBoot = vi.fn();

vi.mock('@/api/appearance', () => ({
  AppearanceApi: {
    list: (...a: unknown[]) => list(...(a as [])),
    put: (...a: unknown[]) => put(...(a as [])),
    remove: (...a: unknown[]) => remove(...(a as [])),
    boot: (...a: unknown[]) => boot(...(a as [])),
    invalidateBoot: () => invalidateBoot(),
    get: vi.fn(),
    customCss: vi.fn().mockResolvedValue({ css: '', enabled: false }),
  },
}));

const suspendCustomCss = vi.fn();
const resumeCustomCss = vi.fn();
const applyCustomCss = vi.fn();
const loadCustomCss = vi.fn();

vi.mock('@/lib/customCss', () => ({
  suspendCustomCss: () => suspendCustomCss(),
  resumeCustomCss: () => resumeCustomCss(),
  applyCustomCss: (...a: unknown[]) => applyCustomCss(...(a as [])),
  loadCustomCss: () => loadCustomCss(),
}));

const settingsUpdate = vi.fn();
const settingsFetch = vi.fn();
let settingsData: Record<string, unknown> = {};

vi.mock('@/stores/settings', () => ({
  useSettingsStore: () => ({
    get data() {
      return settingsData;
    },
    loading: false,
    saving: false,
    fetch: (...a: unknown[]) => settingsFetch(...(a as [])),
    update: (...a: unknown[]) => settingsUpdate(...(a as [])),
  }),
}));

vi.mock('@/lib/instanceThemes', () => ({ applyInstanceThemes: vi.fn() }));

import Appearance from '@/views/Appearance.vue';

const i18n = createI18n({
  legacy: false,
  locale: 'en',
  fallbackLocale: 'en',
  messages: { en, tr },
});

async function mountPage(): Promise<VueWrapper> {
  const wrapper = mount(Appearance, { global: { plugins: [i18n] } });
  await flushPromises();
  await flushPromises();
  return wrapper;
}

/**
 * Submit the editor form.
 *
 * ⚠ The form and not the button: the save control is a `type="submit"` inside
 * `<form @submit.prevent="save">`, which is right in a browser and does
 * nothing in jsdom — a click on a submit button there does not run implicit
 * form submission, so the handler is never reached and the test reads as
 * "saving is broken". Triggering `submit` exercises the same handler.
 */
async function saveDraft(wrapper: VueWrapper): Promise<void> {
  await wrapper.find('form').trigger('submit');
  await flushPromises();
}

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
  settingsData = {};
  list.mockResolvedValue([]);
  boot.mockResolvedValue({ themes: [], default_theme_id: 'default' });
  put.mockImplementation((_key: string, doc: unknown) => Promise.resolve(doc));
  remove.mockResolvedValue(undefined);
  settingsUpdate.mockResolvedValue({});
  settingsFetch.mockResolvedValue({});
});

// ─────────────────── 1 + 2: the lock-out guards ───────────────────

describe('the screen that turns custom CSS off cannot be reached by it', () => {
  it('suspends the operator stylesheet for as long as it is open', async () => {
    const wrapper = await mountPage();
    expect(suspendCustomCss).toHaveBeenCalled();
    expect(resumeCustomCss).not.toHaveBeenCalled();

    wrapper.unmount();
    expect(resumeCustomCss).toHaveBeenCalled();
    // …and re-reads it, so the rest of the panel wears what was just saved
    // rather than the copy this tab booted with.
    expect(loadCustomCss).toHaveBeenCalled();
  });

  it('carries the class the server scope wrapper excludes', async () => {
    const wrapper = await mountPage();
    // The name is a contract with handlers/custom_css.go — if it is renamed on
    // one side only, the wrapper stops excluding anything and nothing errors.
    expect(wrapper.find('[data-testid="appearance-page"]').classes()).toContain('fe-css-immune');
  });
});

// ─────────────────── 3: composing and saving ───────────────────

describe('composing a theme', () => {
  it('suggests a key from the name, including a Turkish one', async () => {
    const wrapper = await mountPage();
    await wrapper.find('[data-testid="theme-name"] input').setValue('Şirket Güneşi');
    await flushPromises();
    expect((wrapper.find('[data-testid="theme-key"] input').element as HTMLInputElement).value)
      .toBe('sirket-gunesi');
  });

  it('stops suggesting once the operator types a key of their own', async () => {
    const wrapper = await mountPage();
    await wrapper.find('[data-testid="theme-name"] input').setValue('Acme');
    await wrapper.find('[data-testid="theme-key"] input').setValue('brand-2026');
    await wrapper.find('[data-testid="theme-name"] input').setValue('Acme Bulut');
    await flushPromises();
    expect((wrapper.find('[data-testid="theme-key"] input').element as HTMLInputElement).value)
      .toBe('brand-2026');
  });

  it('saves the DERIVED palette, in both variants, not the twelve fields', async () => {
    const wrapper = await mountPage();
    await wrapper.find('[data-testid="theme-name"] input').setValue('Acme Bulut');
    await wrapper.find('[data-testid="tokhex---fe-primary"]').setValue('#aa0055');
    await saveDraft(wrapper);
    await flushPromises();

    expect(put).toHaveBeenCalledTimes(1);
    const [key, doc] = put.mock.calls[0] as [string, Record<string, never>];
    expect(key).toBe('acme-bulut');
    expect(doc.filex_theme).toBe(1);
    expect(doc.name).toBe('Acme Bulut');

    const light = doc.light as unknown as Record<string, string>;
    const dark = doc.dark as unknown as Record<string, string>;
    expect(light['--fe-primary']).toBe('#aa0055');
    // Derived tokens the operator never typed must be there, or the server
    // refuses the save for a missing token.
    for (const derived of [
      '--fe-bg-hover',
      '--fe-bg-selected',
      '--fe-border-soft',
      '--fe-border-strong',
      '--fe-text-on-primary',
      '--fe-danger-hover',
      '--fe-keep-ok',
      '--fe-radius',
      '--fe-font',
    ]) {
      expect(light[derived], `light is missing ${derived}`).toBeTruthy();
      expect(dark[derived], `dark is missing ${derived}`).toBeTruthy();
    }
  });

  it('edits only the variant that is on screen', async () => {
    const wrapper = await mountPage();
    await wrapper.find('[data-testid="theme-name"] input').setValue('Acme');
    await wrapper.find('[data-testid="tokhex---fe-bg"]').setValue('#111111');

    await wrapper.find('[data-testid="theme-variant-dark"]').trigger('click');
    await flushPromises();
    await wrapper.find('[data-testid="tokhex---fe-bg"]').setValue('#222222');

    await saveDraft(wrapper);
    await flushPromises();

    const [, doc] = put.mock.calls[0] as [string, Record<string, never>];
    expect((doc.light as unknown as Record<string, string>)['--fe-bg']).toBe('#111111');
    expect((doc.dark as unknown as Record<string, string>)['--fe-bg']).toBe('#222222');
  });

  it('refuses to save without a name, and flags an impossible key', async () => {
    const wrapper = await mountPage();
    await saveDraft(wrapper);
    await flushPromises();
    expect(put).not.toHaveBeenCalled();

    await wrapper.find('[data-testid="theme-name"] input').setValue('Acme');
    await wrapper.find('[data-testid="theme-key"] input').setValue('Not A Key');
    await flushPromises();
    expect(wrapper.text()).toContain(en.appearance.keyInvalid);
  });

  it('warns about an unreadable pairing without blocking the save', async () => {
    const wrapper = await mountPage();
    await wrapper.find('[data-testid="theme-name"] input').setValue('Acme');
    expect(wrapper.find('[data-testid="theme-contrast-warnings"]').exists()).toBe(false);

    await wrapper.find('[data-testid="tokhex---fe-text"]').setValue('#cccccc');
    await flushPromises();
    expect(wrapper.find('[data-testid="theme-contrast-warnings"]').exists()).toBe(true);

    await saveDraft(wrapper);
    await flushPromises();
    expect(put, 'a contrast warning is advice, not a refusal').toHaveBeenCalledTimes(1);
  });

  it('paints the live preview with the draft rather than a mock-up', async () => {
    const wrapper = await mountPage();
    await wrapper.find('[data-testid="tokhex---fe-primary"]').setValue('#aa0055');
    await flushPromises();

    const preview = wrapper.find('[data-testid="theme-preview"]');
    expect(preview.attributes('style')).toContain('--fe-primary: #aa0055');

    await wrapper.find('[data-testid="theme-variant-dark"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid="theme-preview"]').classes()).toContain('fe--theme-dark');
  });
});

// ─────────────────── the saved list ───────────────────

describe('the saved themes list', () => {
  beforeEach(() => {
    list.mockResolvedValue([
      {
        filex_theme: 1,
        key: 'acme',
        name: 'Acme Bulut',
        light: { '--fe-primary': '#aa0055' },
        dark: { '--fe-primary': '#aa0055' },
      },
    ]);
  });

  it('lists a stored theme with the palette id people will see', async () => {
    const wrapper = await mountPage();
    expect(wrapper.text()).toContain('Acme Bulut');
    expect(wrapper.text()).toContain('custom:acme');
  });

  it('marks the instance default and does not offer to set it again', async () => {
    settingsData = { 'ui.default_theme': 'custom:acme' };
    const wrapper = await mountPage();
    expect(wrapper.text()).toContain(en.appearance.isDefault);

    // ⚠⚠ Asserted through the OPEN MENU, and it has to be. The earlier
    // version of this test looked for a loose button's testid and called its
    // absence a pass — which stayed green the day the buttons moved into the
    // menu and would have stayed green if the menu HAD offered the verb. An
    // absence only proves something when you are looking in the right place.
    await openRowMenu(wrapper, 'theme-actions-acme');
    const labels = menuEntries().map((e) => e.label);
    expect(labels).toContain(en.common.edit);
    expect(labels).not.toContain(en.appearance.makeDefault);
    closeRowMenus();
  });

  it('sets the instance default through the settings key the server reads', async () => {
    const wrapper = await mountPage();
    await openRowMenu(wrapper, 'theme-actions-acme');
    await pickMenuItem('theme-actions-acme-default');
    await flushPromises();
    expect(settingsUpdate).toHaveBeenCalledWith({ 'ui.default_theme': 'custom:acme' });
  });

  it('republishes the palette list after a delete, so anybody on it falls back', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    const wrapper = await mountPage();
    await openRowMenu(wrapper, 'theme-actions-acme');
    await pickMenuItem('theme-actions-acme-delete');
    await flushPromises();

    expect(remove).toHaveBeenCalledWith('acme');
    // ⚠ Without this the operator deletes a theme and everybody — including
    // whoever is looking at this page — stays on it until the next reload.
    expect(invalidateBoot).toHaveBeenCalled();
    expect(boot).toHaveBeenCalled();
  });

  it('does not delete when the confirmation is declined', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(false);
    const wrapper = await mountPage();
    await openRowMenu(wrapper, 'theme-actions-acme');
    await pickMenuItem('theme-actions-acme-delete');
    await flushPromises();
    expect(remove).not.toHaveBeenCalled();
  });
});

// ─────────────────── 4: the escape hatch ───────────────────

describe('the raw-CSS panel', () => {
  it('is off by default and says what it can and cannot do', async () => {
    const wrapper = await mountPage();
    const panel = wrapper.find('[data-testid="custom-css-panel"]');
    expect(panel.exists()).toBe(true);
    expect(panel.find('[role="switch"]').attributes('aria-checked')).toBe('false');

    // The honest warnings are part of the feature, not decoration.
    expect(panel.text()).toContain(en.appearance.css.warnScope);
    // ⚠ Asserted on the RENDERED text, not on the catalogue source. vue-i18n
    // reads a bare "@" as the start of a linked message, so this string
    // escapes it (`{'@'}import`) — comparing against the raw source would pass
    // on a message that failed to compile and fell back to printing its own
    // escape syntax at the operator.
    expect(panel.text()).toContain('import is stripped');
    expect(panel.text(), 'the linked-message escape must not reach the screen')
      .not.toContain("{'@'}");
    expect(panel.text()).toContain(en.appearance.css.warnLie);
    expect(panel.text()).toContain(en.appearance.css.warnOperator);
  });

  it('writes the sheet and the switch together', async () => {
    const wrapper = await mountPage();
    await wrapper.find('[data-testid="custom-css-text"] textarea').setValue('.fe { --fe-bg: #000; }');
    await wrapper.find('[data-testid="custom-css-panel"] [role="switch"]').trigger('click');
    await wrapper.find('[data-testid="custom-css-save"]').trigger('click');
    await flushPromises();

    expect(settingsUpdate).toHaveBeenCalledWith({
      'ui.custom_css': '.fe { --fe-bg: #000; }',
      'ui.custom_css_enabled': 'true',
    });
  });

  it('removes the sheet AND switches off in one click', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    settingsData = { 'ui.custom_css': '.fe{}', 'ui.custom_css_enabled': 'true' };
    const wrapper = await mountPage();
    expect(wrapper.find('[data-testid="custom-css-panel"] [role="switch"]').attributes('aria-checked'))
      .toBe('true');

    await wrapper.find('[data-testid="custom-css-remove"]').trigger('click');
    await flushPromises();

    // ⚠ Both keys. Clearing the text but leaving the switch on would mean the
    // next thing anybody pasted went live without them turning it on.
    expect(settingsUpdate).toHaveBeenCalledWith({
      'ui.custom_css': '',
      'ui.custom_css_enabled': 'false',
    });
    expect(applyCustomCss).toHaveBeenCalledWith('');
  });
});

// ─────────────────── the strings ───────────────────

describe('every string on this screen exists in both catalogues', () => {
  it('has Turkish for every English appearance key, with real Turkish letters', () => {
    const flat = (o: Record<string, unknown>, p = ''): string[] =>
      Object.entries(o).flatMap(([k, v]) =>
        typeof v === 'object' && v ? flat(v as Record<string, unknown>, `${p}${k}.`) : [`${p}${k}`],
      );

    const enKeys = flat(en.appearance as unknown as Record<string, unknown>).sort();
    const trKeys = flat(tr.appearance as unknown as Record<string, unknown>).sort();
    expect(trKeys).toEqual(enKeys);
    expect(tr.nav.appearance).toBeTruthy();

    // ⚠⚠ The house rule: a Turkish surface uses Turkish letters. "Gorunum" and
    // "Ozel CSS" are the failure this asserts against — they read as a bug to
    // the person the screen is for.
    const trText = JSON.stringify(tr.appearance);
    expect(trText).toMatch(/[ışğüöçİŞĞÜÖÇ]/);
    expect(tr.appearance.title).toBe('Görünüm');
    expect(tr.appearance.css.title).toBe('Özel CSS');
    expect(tr.appearance.import).toBe('İçe aktar');
    for (const ascii of ['Gorunum', 'Ozel', 'Icе aktar', 'Kurulum temalari', 'Yukseltilmis']) {
      expect(trText).not.toContain(ascii);
    }
  });
});
