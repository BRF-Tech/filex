// Admin → Tools → Thumbnail repair (task #129, GitHub #79).
//
// What is pinned here:
//   1. Tools is a page of tabs drawn from one registry, the open tab in the
//      address; Thumbnail repair is the first tool.
//   2. The ask the server gets: a storage and an optional folder become ONE
//      qualified path; "all storages" is the empty path; the mode is sent.
//   3. A run that goes on is followed until it ends (progress, then the
//      result's counts); a second press while it runs is told it is busy.
//   4. The SVG limits card shows what is in force and saves; a tenant
//      administrator sees them read-only.
//   5. The files without a thumbnail are in the explorer's table
//      (DataTable), each with its reason in words; "Try again" repairs that
//      one file.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const api = vi.hoisted(() => ({
  thumbRepair: vi.fn(),
  thumbRepairStatus: vi.fn(),
  cancel: vi.fn(),
  thumbProblems: vi.fn(),
  thumbSettings: vi.fn(),
  saveThumbSettings: vi.fn(),
  thumbGenerators: vi.fn(),
}));
vi.mock('@/api/tools', () => ({ toolsApi: api }));
vi.mock('@/api/storages', () => ({
  StoragesApi: { list: vi.fn(async () => [{ id: 1, name: 'arsiv' }, { id: 2, name: 'fotolar' }]) },
}));

import Tools from '@/views/Tools.vue';
import ThumbnailRepairTab from '@/components/tools/ThumbnailRepairTab.vue';
import ThumbLimitsCard from '@/components/tools/ThumbLimitsCard.vue';

const SETTINGS = {
  folder_previews: true,
  svg_max_mb: 5,
  svg_timeout_seconds: 10,
  svg_max_mb_min: 1,
  svg_max_mb_max: 64,
  svg_timeout_seconds_min: 1,
  svg_timeout_seconds_max: 120,
  office_max_mb: 25,
  office_slots: 1,
  office_max_mb_min: 1,
  office_max_mb_max: 100,
  office_slots_min: 1,
  office_slots_max: 4,
  editable: true,
};

function i18n(locale: 'en' | 'tr' = 'en') {
  return createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } as never });
}

async function router(at = '/tools') {
  const r = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/tools', name: 'tools', component: { template: '<div />' } }],
  });
  await r.push(at);
  await r.isReady();
  return r;
}

async function mountTab(locale: 'en' | 'tr' = 'en') {
  const w = mount(ThumbnailRepairTab, { global: { plugins: [await router(), i18n(locale)] } });
  await flushPromises();
  return w;
}

beforeEach(() => {
  setActivePinia(createPinia());
  vi.useFakeTimers({ shouldAdvanceTime: true });
  for (const f of Object.values(api)) f.mockReset();
  api.thumbRepairStatus.mockResolvedValue({ running: false });
  api.thumbProblems.mockResolvedValue({ items: [], truncated: false });
  api.thumbSettings.mockResolvedValue(SETTINGS);
  api.thumbGenerators.mockResolvedValue([]);
});

afterEach(() => {
  vi.useRealTimers();
});

describe('Tools', () => {
  it('draws its tabs from the registry and keeps the open one in the address', async () => {
    const r = await router('/tools');
    const w = mount(Tools, { global: { plugins: [r, i18n()] } });
    await flushPromises();
    const tab = w.find('[data-testid="tools-tab-thumbnails"]');
    expect(tab.exists()).toBe(true);
    expect(tab.text()).toBe('Thumbnail repair');
    expect(tab.attributes('aria-selected')).toBe('true');
    await tab.trigger('click');
    await flushPromises();
    expect(r.currentRoute.value.query.tab).toBe('thumbnails');
  });

  it('is "Araçlar" with "Küçük resim onarımı" in Turkish', async () => {
    const w = mount(Tools, { global: { plugins: [await router(), i18n('tr')] } });
    await flushPromises();
    expect(w.find('h1').text()).toBe('Araçlar');
    expect(w.find('[data-testid="tools-tab-thumbnails"]').text()).toBe('Küçük resim onarımı');
  });
});

describe('Thumbnail repair', () => {
  it('asks for one qualified path, and every storage with the empty one', async () => {
    api.thumbRepair.mockResolvedValue({ running: false, started_at: '2026-09-30T10:00:00Z', status: 'ok', processed: 0 });
    const w = await mountTab();
    await w.find('[data-testid="thumb-repair-start"]').trigger('submit');
    await flushPromises();
    expect(api.thumbRepair).toHaveBeenLastCalledWith({ path: '', mode: 'fix' });

    const storage = w.find('[data-testid="thumb-repair-storage"] select');
    await storage.setValue('arsiv');
    await w.find('[data-testid="thumb-repair-path"] input').setValue('/Tatil/2024');
    await w.find('[data-testid="thumb-repair-mode"] select').setValue('rebuild');
    await w.find('form[data-testid="thumb-repair-form"]').trigger('submit');
    await flushPromises();
    expect(api.thumbRepair).toHaveBeenLastCalledWith({ path: 'arsiv://Tatil/2024', mode: 'rebuild' });
  });

  it('follows a run to its end and says what it did', async () => {
    api.thumbRepair.mockResolvedValue({ running: true, op_id: 9, total: 10, processed: 3, mode: 'fix', path: '' });
    api.thumbRepairStatus.mockResolvedValueOnce({ running: false }).mockResolvedValue({
      running: false, op_id: 9, status: 'partial', mode: 'fix', path: 'arsiv://',
      total: 10, processed: 10, ok: 7, failed: 1, skipped: 2,
      started_at: '2026-09-30T10:00:00Z', finished_at: '2026-09-30T10:01:00Z',
      refused: [{ storage_id: 2, storage: 'fotolar', code: 'never_synced' }],
    });
    const w = await mountTab();
    await w.find('form[data-testid="thumb-repair-form"]').trigger('submit');
    await flushPromises();
    const running = w.find('[data-testid="thumb-repair-running"]');
    expect(running.exists()).toBe(true);
    expect(running.text()).toContain('3 of 10');
    expect(running.find('[role="progressbar"]').attributes('aria-valuenow')).toBe('30');
    expect(w.find('[data-testid="thumb-repair-start"]').attributes('disabled')).toBeDefined();

    await vi.advanceTimersByTimeAsync(1600);
    await flushPromises();
    expect(w.find('[data-testid="thumb-repair-running"]').exists()).toBe(false);
    expect(w.find('[data-testid="thumb-repair-ok"]').text()).toBe('7');
    expect(w.find('[data-testid="thumb-repair-failed"]').text()).toBe('1');
    expect(w.find('[data-testid="thumb-repair-skipped"]').text()).toBe('2');
    expect(w.find('[data-testid="thumb-repair-refused"]').text()).toContain('never been synced');
  });

  it('a second press while one runs is told it is busy and follows that run', async () => {
    api.thumbRepair.mockRejectedValue({
      response: { status: 409, data: { code: 'BUSY', job: { running: true, op_id: 4, total: 5, processed: 1 } } },
    });
    api.thumbRepairStatus.mockResolvedValue({ running: true, op_id: 4, total: 5, processed: 1 });
    const w = await mountTab();
    await w.find('form[data-testid="thumb-repair-form"]').trigger('submit');
    await flushPromises();
    expect(w.find('[data-testid="thumb-repair-running"]').text()).toContain('1 of 5');
  });

  it('lists the files without a thumbnail in the explorer table, with the reason in words', async () => {
    api.thumbProblems.mockResolvedValue({
      truncated: false,
      items: [
        { node_id: 11, storage_id: 1, storage: 'arsiv', path: 'arsiv://harita.svg', name: 'harita.svg', size: 9 << 20, state: 'skipped', code: 'svg_too_large', limit: 5 << 20 },
        { node_id: 12, storage_id: 1, storage: 'arsiv', path: 'arsiv://plan.svg', name: 'plan.svg', size: 300000, state: 'skipped', code: 'svg_timeout', limit: 10000 },
        { node_id: 13, storage_id: 1, storage: 'arsiv', path: 'arsiv://bozuk.png', name: 'bozuk.png', size: 10, state: 'failed', code: 'failed', detail: 'thumb: decode: image: unknown format' },
        { node_id: 14, storage_id: 1, storage: 'arsiv', path: 'arsiv://IMG_0001.heic', name: 'IMG_0001.heic', size: 10, state: 'skipped', code: 'no_tool', tool: 'heif' },
        { node_id: 15, storage_id: 1, storage: 'arsiv', path: 'arsiv://rapor.doc', name: 'rapor.doc', size: 10, state: 'skipped', code: 'no_tool', tool: 'office' },
        { node_id: 16, storage_id: 1, storage: 'arsiv', path: 'arsiv://x.cad', name: 'x.cad', size: 10, state: 'skipped', code: 'no_tool', tool: 'cad' },
        { node_id: 17, storage_id: 1, storage: 'arsiv', path: 'arsiv://gizli.zip', name: 'gizli.zip', size: 10, state: 'skipped', code: 'archive_encrypted' },
      ],
    });
    const w = await mountTab();
    expect(w.find('table').exists()).toBe(false);
    const reasons = w.findAll('[data-testid="thumb-problem-reason"]').map((x) => x.text());
    expect(reasons).toEqual([
      'Larger than the SVG size limit (5 MB)',
      'Took longer than the SVG time limit (10 s)',
      'Could not be drawn',
      'ImageMagick with HEIC support is not installed',
      // 0.50: office documents are OnlyOffice's, not a program's here.
      'ONLYOFFICE is not configured (Settings → External services)',
      // A kind this client does not know yet still says what happened.
      'A program this kind of file needs is not installed',
      'Encrypted archive: its contents are not listed',
    ]);
    expect(w.findAll('[data-testid="thumb-problem-reason"]')[2].attributes('title')).toContain('unknown format');
    // A narrow column cuts the text; the whole reason, limit included, is in
    // the title (measured at 958 px in Turkish).
    expect(w.findAll('[data-testid="thumb-problem-reason"]')[0].attributes('title')).toBe('Larger than the SVG size limit (5 MB)');
  });

  it('says the reasons in Turkish', async () => {
    api.thumbProblems.mockResolvedValue({
      truncated: false,
      items: [{ node_id: 11, storage_id: 1, storage: 'arsiv', path: 'arsiv://harita.svg', name: 'harita.svg', size: 9 << 20, state: 'skipped', code: 'svg_too_large', limit: 5 << 20 }],
    });
    const w = await mountTab('tr');
    expect(w.find('[data-testid="thumb-problem-reason"]').text()).toMatch(/^SVG boyut sınırından büyük/);
  });

  it('says which program is missing in Turkish', async () => {
    api.thumbProblems.mockResolvedValue({
      truncated: false,
      items: [{ node_id: 14, storage_id: 1, storage: 'arsiv', path: 'arsiv://klip.mov', name: 'klip.mov', size: 10, state: 'skipped', code: 'no_tool', tool: 'video' }],
    });
    const w = await mountTab('tr');
    expect(w.find('[data-testid="thumb-problem-reason"]').text()).toBe('FFmpeg kurulu değil');
  });

  /* 0.50 test phase: on Ubuntu 24.04 ImageMagick was installed and libheif had
     no HEVC decoder, and every HEIC was "Could not be drawn". The server now
     measures it and skips them as no_tool / heic_codec: the row says what to
     install, in both languages, not the generic "a program is missing". */
  it('names the HEVC decoder for a HEIC ImageMagick cannot decode', async () => {
    const row = { node_id: 18, storage_id: 1, storage: 'arsiv', path: 'arsiv://IMG_0002.heic', name: 'IMG_0002.heic', size: 10, state: 'skipped', code: 'no_tool', tool: 'heic_codec' };
    api.thumbProblems.mockResolvedValue({ truncated: false, items: [row] });
    let w = await mountTab();
    expect(w.find('[data-testid="thumb-problem-reason"]').text()).toBe("libheif's HEVC decoder is not installed (libheif-plugin-libde265)");
    w.unmount();
    w = await mountTab('tr');
    expect(w.find('[data-testid="thumb-problem-reason"]').text()).toBe("libheif'in HEVC çözücüsü kurulu değil (libheif-plugin-libde265)");
  });
});

describe('SVG limits', () => {
  it('shows what is in force and saves a change', async () => {
    api.saveThumbSettings.mockResolvedValue({ ...SETTINGS, svg_max_mb: 12 });
    const w = mount(ThumbLimitsCard, { global: { plugins: [i18n()] } });
    await flushPromises();
    const size = w.find('[data-testid="thumb-limits-size"] input');
    expect((size.element as HTMLInputElement).value).toBe('5');
    expect((w.find('[data-testid="thumb-limits-time"] input').element as HTMLInputElement).value).toBe('10');
    await size.setValue('12');
    await w.find('form').trigger('submit');
    await flushPromises();
    expect(api.saveThumbSettings).toHaveBeenCalledWith({
      folder_previews: true, svg_max_mb: 12, svg_timeout_seconds: 10, office_max_mb: 25, office_slots: 1,
    });
  });

  it('turns folder previews off with the same save', async () => {
    api.saveThumbSettings.mockResolvedValue({ ...SETTINGS, folder_previews: false });
    const w = mount(ThumbLimitsCard, { global: { plugins: [i18n()] } });
    await flushPromises();
    const sw = w.find('[data-testid="thumb-folder-previews"] [role="switch"]');
    expect(sw.attributes('aria-checked')).toBe('true');
    await sw.trigger('click');
    expect(sw.attributes('aria-checked')).toBe('false');
    await w.find('form').trigger('submit');
    await flushPromises();
    expect(api.saveThumbSettings).toHaveBeenCalledWith({
      folder_previews: false, svg_max_mb: 5, svg_timeout_seconds: 10, office_max_mb: 25, office_slots: 1,
    });
  });

  it('saves the office documents\' size and slots (OnlyOffice, 0.50), within their bounds', async () => {
    api.saveThumbSettings.mockResolvedValue({ ...SETTINGS, office_max_mb: 40, office_slots: 2 });
    const w = mount(ThumbLimitsCard, { global: { plugins: [i18n()] } });
    await flushPromises();
    const size = w.find('[data-testid="thumb-limits-office-size"] input');
    const slots = w.find('[data-testid="thumb-limits-office-slots"] input');
    expect((size.element as HTMLInputElement).value).toBe('25');
    expect((slots.element as HTMLInputElement).value).toBe('1');
    await slots.setValue('5');
    await w.find('form').trigger('submit');
    await flushPromises();
    expect(api.saveThumbSettings, 'at most 4 at once').not.toHaveBeenCalled();
    await size.setValue('40');
    await slots.setValue('2');
    await w.find('form').trigger('submit');
    await flushPromises();
    expect(api.saveThumbSettings).toHaveBeenCalledWith({
      folder_previews: true, svg_max_mb: 5, svg_timeout_seconds: 10, office_max_mb: 40, office_slots: 2,
    });
  });

  it('does not save a value outside the bounds', async () => {
    const w = mount(ThumbLimitsCard, { global: { plugins: [i18n()] } });
    await flushPromises();
    await w.find('[data-testid="thumb-limits-size"] input').setValue('500');
    await w.find('form').trigger('submit');
    await flushPromises();
    expect(api.saveThumbSettings).not.toHaveBeenCalled();
  });

  it('is read-only for a tenant administrator', async () => {
    api.thumbSettings.mockResolvedValue({ ...SETTINGS, editable: false });
    const w = mount(ThumbLimitsCard, { global: { plugins: [i18n()] } });
    await flushPromises();
    expect(w.find('[data-testid="thumb-limits-readonly"]').exists()).toBe(true);
    expect(w.find('[data-testid="thumb-limits-save"]').exists()).toBe(false);
    expect(w.find('[data-testid="thumb-limits-size"] input').attributes('disabled')).toBeDefined();
    expect(w.find('[data-testid="thumb-folder-previews"] [role="switch"]').attributes('disabled')).toBeDefined();
  });
});

// Thumbnails drawn by apps (0.50, docs/thumbnails.md): the new reasons, the
// handlers asked, and who drew the thumbnails.
describe('Thumbnail repair - handlers', () => {
  it('says the reasons of a chain in words, and the handlers asked in order', async () => {
    api.thumbProblems.mockResolvedValue({
      truncated: false,
      items: [
        { node_id: 21, storage_id: 1, storage: 'arsiv', path: 'arsiv://a.jar', name: 'a.jar', size: 10, state: 'skipped', code: 'app_failed', app: 'pkglist',
          attempts: [{ handler: 'app:pkglist', app: 'pkglist', version: '0.1.0', ok: false, code: 'app_failed' }] },
        { node_id: 22, storage_id: 1, storage: 'arsiv', path: 'arsiv://b.jar', name: 'b.jar', size: 10, state: 'skipped', code: 'app_timeout', app: 'pkglist', limit: 10000 },
        { node_id: 23, storage_id: 1, storage: 'arsiv', path: 'arsiv://c.jar', name: 'c.jar', size: 10, state: 'skipped', code: 'app_too_large', app: 'pkglist', limit: 32 << 20 },
        { node_id: 24, storage_id: 1, storage: 'arsiv', path: 'arsiv://d.png', name: 'd.png', size: 10, state: 'skipped', code: 'no_handler', attempts: [] },
        { node_id: 25, storage_id: 1, storage: 'arsiv', path: 'arsiv://e.png', name: 'e.png', size: 10, state: 'ready', code: 'fell_back', generator: 'builtin',
          attempts: [
            { handler: 'app:pngplus', app: 'pngplus', version: '1.2', ok: false, code: 'app_failed' },
            { handler: 'builtin', ok: true },
          ] },
      ],
    });
    const w = await mountTab();
    const reasons = w.findAll('[data-testid="thumb-problem-reason"]').map((x) => x.text());
    expect(reasons).toEqual([
      'pkglist could not draw it',
      'pkglist took longer than its time limit (10 s)',
      'Larger than the size limit of pkglist (32 MB)',
      'Every handler of this kind is switched off (Default apps)',
      'Drawn by filex (built-in) after pngplus 1.2 could not',
    ]);
    const chains = w.findAll('[data-testid="thumb-problem-handlers"]').map((x) => x.text());
    expect(chains[0]).toBe('pkglist 0.1.0: pkglist could not draw it');
    expect(chains[4]).toBe('pngplus 1.2: pngplus could not draw it → filex (built-in): drew it');
  });

  it('counts who drew the thumbnails', async () => {
    api.thumbGenerators.mockResolvedValue([
      { generator: 'builtin', count: 1234 },
      { generator: 'app:pkglist@0.1.0', app: 'pkglist', version: '0.1.0', count: 56 },
      { generator: '', count: 7 },
    ]);
    const w = await mountTab();
    const card = w.find('[data-testid="thumb-generators"]');
    expect(card.find('[data-testid="thumb-generator-builtin"]').text()).toMatch(/^filex \(built-in\)\s*1,234$/);
    expect(card.find('[data-testid="thumb-generator-app:pkglist@0.1.0"]').text()).toMatch(/^pkglist 0\.1\.0\s*56$/);
    expect(card.find('[data-testid="thumb-generator-legacy"]').text()).toContain('Before 0.50');
  });

  it('says it in Turkish', async () => {
    api.thumbProblems.mockResolvedValue({
      truncated: false,
      items: [{ node_id: 22, storage_id: 1, storage: 'arsiv', path: 'arsiv://b.jar', name: 'b.jar', size: 10, state: 'skipped', code: 'app_timeout', app: 'pkglist', limit: 10000 }],
    });
    api.thumbGenerators.mockResolvedValue([{ generator: 'builtin', count: 3 }]);
    const w = await mountTab('tr');
    expect(w.find('[data-testid="thumb-problem-reason"]').text()).toBe('pkglist süre sınırını aştı (10 sn)');
    expect(w.find('[data-testid="thumb-generators"]').text()).toContain('Küçük resimleri kim çizdi');
    expect(w.find('[data-testid="thumb-generator-builtin"]').text()).toContain('filex (yerleşik)');
  });

  it('the page works when the count cannot be read', async () => {
    api.thumbGenerators.mockRejectedValue(new Error('nope'));
    const w = await mountTab();
    expect(w.find('[data-testid="thumb-generators-empty"]').exists()).toBe(true);
  });
});

/* 0.50: office documents are drawn by the OnlyOffice document server. What
 * its answers mean is said in words (thumb/office.go), and the handler is
 * named "OnlyOffice" in the chain and in "who drew the thumbnails". */
describe('the OnlyOffice document server', () => {
  const item = (node_id: number, name: string, extra: Record<string, unknown>) => ({
    node_id, storage_id: 1, storage: 'arsiv', path: `arsiv://${name}`, name, size: 10, ...extra,
  });
  const ITEMS = [
    item(21, 'bozuk.docx', { state: 'failed', code: 'oo_corrupt', detail: 'ds-3', note: 'corrupt', attempts: [{ handler: 'onlyoffice', ok: false, code: 'oo_corrupt', detail: 'ds-3' }] }),
    item(22, 'parolali.xlsx', { state: 'skipped', code: 'oo_password', note: 'encrypted' }),
    item(23, 'buyuk.pptx', { state: 'skipped', code: 'oo_too_large', limit: 25 << 20, note: 'too_large' }),
    item(24, 'sinirsiz.pptx', { state: 'skipped', code: 'oo_too_large', note: 'too_large' }),
    item(25, 'bekleyen.docx', { state: 'failed', code: 'oo_retry', tries: 2, detail: 'ds-4' }),
    item(26, 'birakildi.docx', { state: 'failed', code: 'oo_retry', tries: 6, detail: 'net' }),
  ];

  it('says what each answer means', async () => {
    api.thumbProblems.mockResolvedValue({ truncated: false, items: ITEMS });
    const w = await mountTab();
    expect(w.findAll('[data-testid="thumb-problem-reason"]').map((x) => x.text())).toEqual([
      'ONLYOFFICE could not read it: the file may be damaged',
      'Protected by a password',
      'Larger than the office size limit (25 MB)',
      "Larger than the document server's own limit",
      'ONLYOFFICE did not answer (ds-4); tried 2 times, tried again later',
      'ONLYOFFICE did not answer (net); gave up after 6 tries',
    ]);
    expect(w.findAll('[data-testid="thumb-problem-handlers"]')[0].text()).toBe('ONLYOFFICE: ONLYOFFICE could not read it: the file may be damaged');
  });

  it('in Turkish', async () => {
    api.thumbProblems.mockResolvedValue({ truncated: false, items: ITEMS });
    const w = await mountTab('tr');
    expect(w.findAll('[data-testid="thumb-problem-reason"]').map((x) => x.text())).toEqual([
      'ONLYOFFICE okuyamadı: dosya bozuk olabilir',
      'Parolayla korunuyor',
      'Ofis boyut sınırından büyük (25 MB)',
      'Belge sunucusunun kendi sınırından büyük',
      'ONLYOFFICE yanıt vermedi (ds-4); 2 kez denendi, sonra yeniden denenecek',
      'ONLYOFFICE yanıt vermedi (net); 6 denemeden sonra bırakıldı',
    ]);
  });

  it('names who drew the thumbnails', async () => {
    api.thumbGenerators.mockResolvedValue([{ generator: 'onlyoffice', count: 7 }]);
    const w = await mountTab();
    expect(w.find('[data-testid="thumb-generator-onlyoffice"]').text()).toContain('ONLYOFFICE');
  });
});
