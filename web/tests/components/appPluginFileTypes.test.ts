// The install review's File types group (filex 0.50) and an app's
// Thumbnails section.
//
// What is pinned here:
//   1. An install's review lists every kind the app would open or draw, who
//      handles it now, and where the app goes - as a row of buttons, the
//      server's default picked. Only the rows changed from that default are
//      sent (`associations`): in the JSON body, and as a form field of an
//      upload. Nothing changed: the body is what it always was.
//   2. An upgrade asks the same about the kinds it ADDS - the rows the server
//      sends for it - and sends the changed ones with the upgrade.
//   3. A choice the server could not write is said after the install.
//   4. The Thumbnails section shows the kinds and the limits, sends 0 for an
//      empty box (the default), refuses a value outside its range before
//      asking, and says the server's own out_of_range under the box.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const WIRE = path.resolve(__dirname, '../../../backend/internal/api/handlers/testdata/wire');
/** The server's own answers (backend file_types_wire_test.go writes them):
 *  the dry run, with the File types group the server adds for an app that
 *  opens .drawio (two apps and filex's viewer open it now) and draws .whl
 *  thumbnails (nobody draws them yet). */
const wire = (name: string) => JSON.parse(readFileSync(path.join(WIRE, name), 'utf8'));
function dryRun() {
  const d = wire('app-plugin-dry-run.json');
  delete d.installed;
  delete d.engines_missing;
  d.file_types = wire('app-plugin-file-types.json').file_types;
  return d;
}

const posts: Array<{ url: string; body: unknown }> = [];
let installAnswer: Record<string, unknown> = { id: 9, name: 'sign', label: { en: 'e-Signature' }, permissions: [] };
let dry: Record<string, unknown> = dryRun();
const gets: Record<string, unknown> = {};
const puts: Array<{ url: string; body: unknown }> = [];
let putRefusal: { status: number; data: Record<string, unknown> } | null = null;

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    post: vi.fn(async (url: string, body: unknown, cfg?: { params?: Record<string, unknown> }) => {
      posts.push({ url, body });
      if (cfg?.params?.dry_run) return { data: dry };
      return { data: installAnswer };
    }),
    get: vi.fn(async (url: string) => ({ data: gets[url] ?? {} })),
    put: vi.fn(async (url: string, body: unknown) => {
      puts.push({ url, body });
      if (putRefusal) throw Object.assign(new Error('refused'), { isAxiosError: true, response: putRefusal });
      return { data: gets[url] };
    }),
  },
}));

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), warn: vi.fn(), info: vi.fn() }));
vi.mock('@/stores/toast', () => ({ useToastStore: () => toast }));

import AppPluginInstallWizard from '@/components/plugins/AppPluginInstallWizard.vue';
import AppPluginThumbLimits from '@/components/plugins/AppPluginThumbLimits.vue';

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); };
}

function i18n(locale: 'en' | 'tr' = 'en') {
  return createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } as never });
}

function mountWizard(props: Record<string, unknown> = {}, locale: 'en' | 'tr' = 'en') {
  return mount(AppPluginInstallWizard, {
    props: { modelValue: true, requiresSignature: false, installed: [], ...props } as never,
    global: { plugins: [i18n(locale)] },
    attachTo: document.body,
  });
}

async function reachReview(w: ReturnType<typeof mountWizard>) {
  await w.find('input[placeholder="BRF-Tech/filex-sign"]').setValue('BRF-Tech/filex-sign');
  await w.find('form').trigger('submit');
  await flushPromises();
}

async function install(w: ReturnType<typeof mountWizard>) {
  await w.find('input[type="checkbox"]').setValue(true);
  await w.find('[data-testid="app-plugin-install"]').trigger('click');
  await flushPromises();
}

beforeEach(() => {
  setActivePinia(createPinia());
  posts.length = 0;
  puts.length = 0;
  putRefusal = null;
  dry = dryRun();
  installAnswer = { id: 9, name: 'sign', label: { en: 'e-Signature' }, permissions: [] };
  for (const f of Object.values(toast)) f.mockReset();
});

describe('install review - File types', () => {
  it('lists each kind with who handles it now, the server\'s default picked', async () => {
    const w = mountWizard();
    await reachReview(w);
    const group = w.find('[data-testid="install-file-types"]');
    expect(group.exists()).toBe(true);
    expect(group.find('table').exists(), 'the explorer table').toBe(false);
    const open = w.find('[data-testid="install-file-type-open-drawio"]');
    expect(open.find('[data-testid="install-place-open-drawio-last"]').attributes('aria-checked')).toBe('true');
    expect(w.find('[data-testid="install-place-thumbnail-whl-first"]').attributes('aria-checked')).toBe('true');
    expect(group.text()).toContain('.drawio');
    expect(group.text()).toContain('draw.io, Zeta viewer, filex (built-in)');
    expect(group.text()).toContain('Nobody');
    expect(group.text()).toContain('Opening');
    expect(group.text()).toContain('Thumbnails');
  });

  it('nothing changed: the install body is what it always was', async () => {
    const w = mountWizard();
    await reachReview(w);
    await install(w);
    expect(posts[1].body).not.toHaveProperty('associations');
  });

  it('sends only the rows changed from the default', async () => {
    const w = mountWizard();
    await reachReview(w);
    await w.find('[data-testid="install-place-open-drawio-first"]').trigger('click');
    await install(w);
    expect((posts[1].body as { associations: unknown }).associations).toEqual([
      { capability: 'open', ext: 'drawio', handler: 'app:sign/editor', place: 'first' },
    ]);
  });

  it('an upload carries the choices as a form field', async () => {
    const w = mountWizard();
    await w.find('[data-testid="app-plugin-source-file"]').trigger('click');
    const manifest = new File(['{}'], 'filex-app.json', { type: 'application/json' });
    const input = w.find('[data-testid="app-plugin-manifest"]');
    Object.defineProperty(input.element, 'files', { value: [manifest], configurable: true });
    await input.trigger('change');
    await w.find('form').trigger('submit');
    await flushPromises();
    await w.find('[data-testid="install-place-thumbnail-whl-off"]').trigger('click');
    await install(w);
    const form = posts[1].body as FormData;
    expect(JSON.parse(String(form.get('associations')))).toEqual([
      { capability: 'thumbnail', ext: 'whl', handler: 'app:sign', place: 'off' },
    ]);
  });

  it('an upgrade asks about the kinds it adds, and sends what was changed with the upgrade', async () => {
    // The server lists only the kinds the new version adds (the order the
    // administrator has for the others is not reopened): here, .whl.
    dry = { ...dryRun(), file_types: dryRun().file_types.filter((r: { ext: string }) => r.ext === 'whl') };
    const w = mountWizard({ upgrade: { id: 9, name: 'sign', version: '0.9.0' } });
    await reachReview(w);
    const group = w.find('[data-testid="install-file-types"]');
    expect(group.exists()).toBe(true);
    expect(group.find('[data-testid="install-file-types-desc"]').text()).toContain('The kinds this version adds');
    expect(w.find('[data-testid="install-file-type-open-drawio"]').exists()).toBe(false);
    await w.find('[data-testid="install-place-thumbnail-whl-off"]').trigger('click');
    await install(w);
    expect(posts[1].url).toBe('/admin/app-plugins/9/upgrade');
    expect((posts[1].body as { associations: unknown }).associations).toEqual([
      { capability: 'thumbnail', ext: 'whl', handler: 'app:sign', place: 'off' },
    ]);
  });

  it('says the choices the server could not write', async () => {
    installAnswer = { ...installAnswer, association_errors: ['open .drawio: app:sign/editor does not open .drawio files here'] };
    const w = mountWizard();
    await reachReview(w);
    await w.find('[data-testid="install-place-open-drawio-first"]').trigger('click');
    await install(w);
    expect(toast.warn).toHaveBeenCalledWith(expect.stringContaining('some file type choices were not saved'));
    expect(w.find('[data-testid="app-plugin-done"]').exists(), 'installed all the same').toBe(true);
  });

  it('in Turkish', async () => {
    const w = mountWizard({}, 'tr');
    await reachReview(w);
    const group = w.find('[data-testid="install-file-types"]').text();
    expect(group).toContain('Dosya türleri');
    expect(group).toContain('İlk sırada');
    expect(group).toContain('Onlardan sonra');
    expect(group).toContain('Küçük resim');
  });
});

/** The server's answer for an app's limits: the time per file raised to
 *  20 s, the rest the defaults. */
const LIMITS = wire('app-plugin-thumb-limits.json');

async function mountLimits(locale: 'en' | 'tr' = 'en') {
  gets['/admin/app-plugins/9/thumbnails'] = LIMITS;
  const w = mount(AppPluginThumbLimits, { props: { pluginId: 9 }, global: { plugins: [i18n(locale)] } });
  await flushPromises();
  return w;
}

describe('an app\'s Thumbnails section', () => {
  it('shows the kinds and the limits, the defaults as placeholders', async () => {
    const w = await mountLimits();
    expect(w.find('[data-testid="app-thumb-kinds"]').text()).toContain('.jar, .apk, application/java-archive');
    const time = w.find('[data-testid="app-thumb-timeout_s"] input').element as HTMLInputElement;
    expect(time.value).toBe('20');
    const size = w.find('[data-testid="app-thumb-max_input_mb"] input').element as HTMLInputElement;
    expect(size.value).toBe('');
    expect(size.placeholder).toBe('32');
    expect(w.find('[data-testid="app-thumb-max_input_mb"]').text()).toContain('1 to 256');
  });

  it('sends 0 for an empty box, and the numbers', async () => {
    gets['/admin/app-plugins/9/thumbnails'] = LIMITS;
    const w = await mountLimits();
    await w.find('[data-testid="app-thumb-concurrency"] input').setValue('4');
    await w.find('form').trigger('submit');
    await flushPromises();
    expect(puts[0]).toEqual({ url: '/admin/app-plugins/9/thumbnails', body: { max_input_mb: 0, timeout_s: 20, memory_mb: 0, concurrency: 4 } });
    expect(toast.success).toHaveBeenCalled();
  });

  it('refuses a value outside its range before asking', async () => {
    const w = await mountLimits();
    await w.find('[data-testid="app-thumb-timeout_s"] input').setValue('61');
    await w.find('form').trigger('submit');
    await flushPromises();
    expect(puts).toHaveLength(0);
    expect(w.find('[data-testid="app-thumb-timeout_s"]').text()).toContain('between 1 and 60');
  });

  it('says the server\'s out_of_range under the box it names', async () => {
    putRefusal = { status: 400, data: { error: 'out_of_range', field: 'memory_mb', message: 'memory_mb must be 16..256' } };
    const w = await mountLimits();
    await w.find('[data-testid="app-thumb-memory_mb"] input').setValue('100');
    await w.find('form').trigger('submit');
    await flushPromises();
    expect(w.find('[data-testid="app-thumb-memory_mb"]').text()).toContain('between 16 and 256');
    expect(toast.error).not.toHaveBeenCalled();
  });

  it('in Turkish', async () => {
    const w = await mountLimits('tr');
    expect(w.find('h3').text()).toBe('Küçük resimler');
    expect(w.find('[data-testid="app-thumb-max_input_mb"]').text()).toContain('Gönderilen en büyük dosya');
  });
});
