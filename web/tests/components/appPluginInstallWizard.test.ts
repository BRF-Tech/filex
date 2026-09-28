// The install wizard's permission gate: the review step lists every
// permission with its reason, and Install stays disabled until the operator
// ticks "I understand" — then the body carries exactly the manifest's list.
//
// ⚠⚠ The dry-run answer here is the SERVER's own bytes, not a fixture typed
// from the client: backend/internal/api/handlers/testdata/wire/
// app-plugin-dry-run.json is written and checked by the Go test
// app_plugins_wire_test.go from the very value the handler sends. This file
// used to invent the answer, with each permission's reason as a flat string —
// while the server sends `{"en": …, "tr": …}` — and so it passed, every time,
// over a review that printed every reason as raw JSON (2026-09-21). A fixture
// typed the client's way can only agree with the client.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const WIRE = path.resolve(__dirname, '../../../backend/internal/api/handlers/testdata/wire');
/**
 * The server's dry-run answer, as the server writes it. ⚠ The wire fixture is
 * the FULL shape — it also says the name is already installed and names a
 * missing engine — so the ordinary install walk gets it without those two
 * (a fresh name, every engine present), and the tests of those two get them
 * straight from the server's bytes.
 */
const wireDryRunFull = () => JSON.parse(readFileSync(path.join(WIRE, 'app-plugin-dry-run.json'), 'utf8'));
/** A fresh copy per call, so a test that edits it cannot leak into the next. */
const wireDryRun = () => {
  const d = wireDryRunFull();
  delete d.installed;
  delete d.engines_missing;
  return d;
};
const WIRE_DRY_RUN = wireDryRun();

const posts: Array<{ url: string; body: unknown; cfg?: { params?: Record<string, unknown>; timeout?: number } }> = [];
let refuse: { status: number; data: Record<string, unknown> } | null = null;
let dryRunAnswer: Record<string, unknown> = WIRE_DRY_RUN;

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    post: vi.fn(async (url: string, body: unknown, cfg?: { params?: Record<string, unknown>; timeout?: number }) => {
      posts.push({ url, body, cfg });
      if (cfg?.params?.dry_run) {
        return { data: dryRunAnswer };
      }
      if (refuse) {
        const e = Object.assign(new Error('refused'), { isAxiosError: true, response: refuse });
        throw e;
      }
      return { data: { id: 9, name: 'sign', label: { en: 'e-Signature' }, permissions: ['files:read', 'files:write', 'net:tsa'] } };
    }),
    get: vi.fn(),
  },
}));

import AppPluginInstallWizard from '@/components/plugins/AppPluginInstallWizard.vue';
import { INSTALL_TIMEOUT_MS } from '@/api/appPlugins';

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); };
}

function mountWizard(locale = 'en', installed: Record<string, unknown>[] = []) {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  return mount(AppPluginInstallWizard, {
    props: { modelValue: true, requiresSignature: false, installed: installed as never },
    global: { plugins: [i18n] },
    attachTo: document.body,
  });
}

async function reachReview(w: ReturnType<typeof mountWizard>) {
  await w.find('input[placeholder="BRF-Tech/filex-sign"]').setValue('BRF-Tech/filex-sign');
  await w.find('form').trigger('submit');
  await flushPromises();
}

describe('AppPluginInstallWizard', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    posts.length = 0;
    refuse = null;
    dryRunAnswer = wireDryRun();
    document.body.innerHTML = '';
  });

  it('the fixture is the real wire shape: a reason is an object of languages', () => {
    // A guard on the guard: if this ever reads a flat string, the fixture is no
    // longer the server's and the rest of this file proves nothing about it.
    const reasons = (WIRE_DRY_RUN.permissions as Array<{ reason?: unknown }>).map((p) => p.reason).filter(Boolean);
    expect(reasons.length).toBeGreaterThan(0);
    for (const r of reasons) expect(r).toEqual({ en: expect.any(String), tr: expect.any(String) });
  });

  it('dry-runs the source, lists every permission with its reason, and gates Install on the checkbox', async () => {
    const w = mountWizard();
    await reachReview(w);

    expect(posts[0]).toMatchObject({ url: '/admin/app-plugins', cfg: { params: { dry_run: 1 } } });
    expect(posts[0].body).toEqual({ github_repo: 'BRF-Tech/filex-sign', ref: '', permissions: [] });

    const rows = WIRE_DRY_RUN.permissions as Array<{ id: string; label: string; reason?: { en: string; tr: string } }>;
    const perms = w.findAll('[data-testid="app-plugin-permissions"] li');
    expect(perms).toHaveLength(rows.length);
    rows.forEach((row, i) => {
      expect(perms[i].text()).toContain(row.id);
      expect(perms[i].text()).toContain(row.label);
      // The app's own words, in the reader's language — and nothing else of
      // them: not the other language, not the object they arrived in.
      if (row.reason) {
        expect(perms[i].text()).toContain(row.reason.en);
        expect(perms[i].text()).not.toContain(row.reason.tr);
      } else {
        // No reason at all → said so, never blank.
        expect(perms[i].text()).toContain(en.appPlugins.wizard.noReason);
      }
    });
    const review = w.find('[data-testid="app-plugin-permissions"]').text();
    expect(review).not.toMatch(/"en"\s*:/);
    expect(review).not.toContain('[object Object]');
    expect(review).not.toContain('{');

    const install = w.find('[data-testid="app-plugin-install"]');
    expect(install.attributes('disabled')).toBeDefined();

    await install.trigger('click');
    expect(posts).toHaveLength(1);

    await w.find('input[type="checkbox"]').setValue(true);
    expect(w.find('[data-testid="app-plugin-install"]').attributes('disabled')).toBeUndefined();

    await w.find('[data-testid="app-plugin-install"]').trigger('click');
    await flushPromises();
    expect(posts[1]).toMatchObject({ url: '/admin/app-plugins' });
    // A real install, not a dry run: no `dry_run` query. (Its one config is
    // the longer wait for the server's compile — api/appPlugins.ts.)
    expect((posts[1].cfg as { params?: unknown } | undefined)?.params).toBeUndefined();
    expect(posts[1].cfg?.timeout, 'the install waits for the compile').toBe(INSTALL_TIMEOUT_MS);
    expect(posts[1].body).toEqual({ github_repo: 'BRF-Tech/filex-sign', ref: '', permissions: (WIRE_DRY_RUN.manifest as { permissions: string[] }).permissions });
    expect(w.emitted('installed')).toHaveLength(1);
    expect(w.find('[data-testid="app-plugin-done"]').exists()).toBe(true);
    w.unmount();
  });

  it('reads the reasons in Turkish on a Turkish screen', async () => {
    const w = mountWizard('tr');
    await reachReview(w);
    const rows = WIRE_DRY_RUN.permissions as Array<{ id: string; reason?: { en: string; tr: string } }>;
    const perms = w.findAll('[data-testid="app-plugin-permissions"] li');
    rows.forEach((row, i) => {
      if (!row.reason) return;
      expect(perms[i].text()).toContain(row.reason.tr);
      expect(perms[i].text()).not.toContain(row.reason.en);
    });
    expect(w.find('[data-testid="app-plugin-permissions"]').text()).not.toMatch(/"tr"\s*:/);
    w.unmount();
  });

  it('falls back to the manifest\'s reason when a review row carries none', async () => {
    // Derived from the real answer, so the fallback is exercised on the real
    // shape: the manifest's permission_reasons is a map of {en, tr} objects.
    const withoutRowReasons = wireDryRun();
    for (const row of withoutRowReasons.permissions as Array<{ reason?: unknown }>) delete row.reason;
    dryRunAnswer = withoutRowReasons;
    const w = mountWizard();
    await reachReview(w);
    const reasons = (withoutRowReasons.manifest as { permission_reasons: Record<string, { en: string }> }).permission_reasons;
    const [id, text] = Object.entries(reasons)[0];
    expect(w.find(`[data-testid="perm-${id}"]`).text()).toContain(text.en);
    w.unmount();
  });

  it('refuses a malformed repository before asking the server', async () => {
    const w = mountWizard();
    await w.find('input[placeholder="BRF-Tech/filex-sign"]').setValue('not a repo');
    await w.find('form').trigger('submit');
    await flushPromises();
    expect(posts).toHaveLength(0);
    expect(w.find('[data-testid="app-plugin-wizard-error"]').text()).toBe(en.appPlugins.wizard.errRepo);
    w.unmount();
  });

  it('names the missing permissions when the server answers permissions_incomplete', async () => {
    refuse = { status: 400, data: { error: 'permissions_incomplete', missing: ['net:tsa'] } };
    const w = mountWizard('tr');
    await reachReview(w);
    await w.find('input[type="checkbox"]').setValue(true);
    await w.find('[data-testid="app-plugin-install"]').trigger('click');
    await flushPromises();
    const msg = w.find('[data-testid="app-plugin-wizard-error"]').text();
    expect(msg).toContain('net:tsa');
    expect(msg).toBe(tr.appPlugins.wizard.errors.permissions_incomplete.replace('{missing}', 'net:tsa'));
    expect(w.emitted('installed')).toBeUndefined();
    w.unmount();
  });

  it('has a sentence for every refusal code the contract names', async () => {
    for (const code of ['sha256_mismatch', 'sha256_required', 'manifest_invalid', 'signature_required', 'name_taken', 'describe_mismatch', 'too_large', 'demo_refused', 'not_found'] as const) {
      refuse = { status: code === 'name_taken' ? 409 : 400, data: { error: code, message: 'detail' } };
      posts.length = 0;
      const w = mountWizard();
      await reachReview(w);
      await w.find('input[type="checkbox"]').setValue(true);
      await w.find('[data-testid="app-plugin-install"]').trigger('click');
      await flushPromises();
      const expected = (en.appPlugins.wizard.errors as Record<string, string>)[code].replace('{message}', 'detail');
      expect(w.find('[data-testid="app-plugin-wizard-error"]').text()).toBe(expected);
      w.unmount();
      document.body.innerHTML = '';
    }
  });

  // ⚠ The release-candidate sweep (2026-09-21): a repository that does not
  // exist answered, in the Turkish wizard, "filex-app.json not found in
  // BRF-Tech/yok-boyle-bir-depo: http 404 from raw.githubusercontent.com".
  it('says a refused fetch in the reader\'s words, with what to check — not the server\'s English', async () => {
    const fetchFailed = JSON.parse(readFileSync(path.join(WIRE, 'app-plugin-fetch-failed.json'), 'utf8'));
    const w = mountWizard('tr');
    // The dry run itself is refused: the mock answers dry runs, so make it throw.
    const { api } = await import('@/api/client');
    (api.post as unknown as ReturnType<typeof vi.fn>).mockImplementationOnce(async () => {
      throw Object.assign(new Error('refused'), { isAxiosError: true, response: { status: 502, data: fetchFailed } });
    });
    await reachReview(w);
    const msg = w.find('[data-testid="app-plugin-wizard-error"]').text();
    expect(msg).toContain('BRF-Tech/yok-boyle-bir-depo');
    expect(msg).toContain('main, master');
    expect(msg).toContain('herkese açık'); // "public" — what to check
    expect(msg).not.toContain('not found');
    expect(msg).not.toContain('raw.githubusercontent.com');
    w.unmount();
  });

  it('an error belongs to its tab: switching source clears it', async () => {
    const w = mountWizard();
    await w.find('input[placeholder="BRF-Tech/filex-sign"]').setValue('not a repo');
    await w.find('form').trigger('submit');
    await flushPromises();
    expect(w.find('[data-testid="app-plugin-wizard-error"]').exists()).toBe(true);
    await w.find('[data-testid="app-plugin-source-url"]').trigger('click');
    expect(w.find('[data-testid="app-plugin-wizard-error"]').exists()).toBe(false);
    await w.find('[data-testid="app-plugin-source-file"]').trigger('click');
    expect(w.find('[data-testid="app-plugin-wizard-error"]').exists()).toBe(false);
    w.unmount();
  });

  // ⚠ Said at the REVIEW, from what the dry run knows: before, the review
  // passed and only "Install" answered "an app with this name is already
  // installed" (sweep, 2026-09-21).
  it('says at the review that the app is already installed, and upgrades it instead', async () => {
    dryRunAnswer = wireDryRunFull();
    const inst = dryRunAnswer.installed as { id: number; version: string };
    const w = mountWizard('en', [{ id: inst.id, name: 'sign', version: inst.version, label: { en: 'e-Signature' }, permissions: [] }]);
    await reachReview(w);
    const box = w.find('[data-testid="app-plugin-already-installed"]');
    expect(box.exists()).toBe(true);
    expect(box.text()).toContain(inst.version);
    await w.find('input[type="checkbox"]').setValue(true);
    expect(w.find('[data-testid="app-plugin-install"]').attributes('disabled'), 'Install cannot go through on a taken name').toBeDefined();

    // "Upgrade it instead": the same source, reviewed again as that app's upgrade.
    dryRunAnswer = wireDryRun();
    await w.find('[data-testid="app-plugin-upgrade-instead"]').trigger('click');
    await flushPromises();
    const upgradeDry = posts.find((p) => p.url === `/admin/app-plugins/${inst.id}/upgrade` && p.cfg?.params?.dry_run);
    expect(upgradeDry, 'the upgrade was reviewed').toBeTruthy();
    expect(w.find('[data-testid="app-plugin-already-installed"]').exists()).toBe(false);
    await w.find('input[type="checkbox"]').setValue(true);
    await w.find('[data-testid="app-plugin-install"]').trigger('click');
    await flushPromises();
    // The real upgrade: no `dry_run` query, and the long wait for the
    // server's compile (feat/043-signing — an upgrade finishes or undoes
    // itself server-side; the page waits INSTALL_TIMEOUT_MS to hear which).
    const upgraded = posts.find((p) => p.url === `/admin/app-plugins/${inst.id}/upgrade` && !p.cfg?.params?.dry_run);
    expect(upgraded, 'the upgrade went through').toBeTruthy();
    expect(upgraded?.cfg?.timeout).toBe(INSTALL_TIMEOUT_MS);
    expect(w.emitted('upgraded')).toHaveLength(1);
    w.unmount();
  });

  it('names at the review the engines this server lacks', async () => {
    dryRunAnswer = wireDryRunFull();
    delete dryRunAnswer.installed;
    const w = mountWizard('tr');
    await reachReview(w);
    const box = w.find('[data-testid="app-plugin-engines-missing"]');
    expect(box.exists()).toBe(true);
    expect(box.text()).toContain('LibreOffice'); // by its name, not `engines:libreoffice`
    expect(box.text()).not.toContain('engines:');
    w.unmount();
  });
});

// ── Upgrades that follow the app's source, and the `filex` range ─────────
//
// Both reviews are the SERVER's bytes: app-plugin-upgrade-review.json (an
// upgrade's dry run: the version it leaves, what it adds to and drops from
// the grant, a range this filex is outside of) and
// app-plugin-incompatible.json (the refusal).
describe('AppPluginInstallWizard — upgrade review and range', () => {
  const upgradeReview = () => JSON.parse(readFileSync(path.join(WIRE, 'app-plugin-upgrade-review.json'), 'utf8'));
  const sign = { id: 7, name: 'sign', version: '1.1.0', label: { en: 'e-Signature' }, permissions: [], source: 'github', source_url: 'https://github.com/BRF-Tech/filex-sign@v1.1.0' };

  beforeEach(() => {
    setActivePinia(createPinia());
    posts.length = 0;
    refuse = null;
    dryRunAnswer = wireDryRun();
    document.body.innerHTML = '';
  });

  function mountUpdate(locale = 'en') {
    const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
    return mount(AppPluginInstallWizard, {
      props: { modelValue: false, requiresSignature: false, update: sign as never, installed: [sign] as never },
      global: { plugins: [i18n] },
      attachTo: document.body,
    });
  }

  it('"Review update" opens on the review of what the source has — the jump and how the grant changes', async () => {
    const review = upgradeReview();
    review.compat.ok = true; // this half is about the jump; the range is below
    dryRunAnswer = review;
    const w = mountUpdate();
    await w.setProps({ modelValue: true });
    await flushPromises();
    expect(posts[0]).toMatchObject({ url: '/admin/app-plugins/7/upgrade', body: { from_source: true, permissions: [] }, cfg: { params: { dry_run: 1 } } });
    expect(w.find('[data-testid="app-plugin-upgrade-jump"]').text()).toBe('Version 1.1.0 → 1.2.0');
    expect(w.find('[data-testid="app-plugin-upgrade-added"]').text()).toBe('New permissions this version asks for: public_pages');
    expect(w.find('[data-testid="app-plugin-upgrade-removed"]').text()).toBe('Permissions it no longer asks for: mail:send');
    // The new one is marked where it is reviewed, in the permission list.
    expect(w.find('[data-testid="perm-new-public_pages"]').exists()).toBe(true);
    expect(w.find('[data-testid="perm-new-files:read"]').exists()).toBe(false);
    // Nothing to go back to: the source is the app's own.
    expect(w.text()).not.toContain(en.appPlugins.wizard.back);

    await w.find('input[type="checkbox"]').setValue(true);
    await w.find('[data-testid="app-plugin-install"]').trigger('click');
    await flushPromises();
    const real = posts.find((p) => !p.cfg?.params?.dry_run);
    expect(real).toMatchObject({ url: '/admin/app-plugins/7/upgrade', body: { from_source: true } });
    expect(w.emitted('upgraded')).toHaveLength(1);
    w.unmount();
  });

  // ⚠⚠ Nothing updates itself (filex 0.48): this review is where every
  // newer version is approved, so it says what the version changes beyond
  // the grant — the server's own bytes (app-plugin-upgrade-review.json).
  it('says what else the version changes: module and interface, their files, the range, the signature, the notes', async () => {
    const review = upgradeReview();
    review.compat.ok = true;
    dryRunAnswer = review;
    const w = mountUpdate();
    await w.setProps({ modelValue: true });
    await flushPromises();
    expect(w.find('[data-testid="app-plugin-upgrade-module-hash"]').text()).toBe('Module: 5f1c0de90000 → a9950e0d84a6');
    expect(w.find('[data-testid="app-plugin-upgrade-ui"]').text()).toBe('This version adds its own interface.');
    const files = w.find('[data-testid="app-plugin-upgrade-files"]');
    expect(files.find('summary').text()).toBe('Interface files: 2 added, 0 removed, 0 changed');
    expect(files.text()).toContain('Added: index.html, app.js');
    expect(files.text()).not.toContain('Removed:');
    expect(w.find('[data-testid="app-plugin-upgrade-range"]').text()).toBe('Works with filex: >=0.45.0 → >=0.48.0');
    expect(w.find('[data-testid="app-plugin-upgrade-signed"]').text()).toBe('Signature: signed → unsigned');
    const notes = w.find('[data-testid="app-plugin-upgrade-notes"]');
    expect(notes.text()).toContain('Signers can now be reminded.');
    w.unmount();
  });

  it('shows the source’s notes as text, never as markup', async () => {
    const review = upgradeReview();
    review.compat.ok = true;
    review.upgrade.notes = 'Fixed <img src=x onerror="window.pwned=1"> and <b>more</b>';
    dryRunAnswer = review;
    const w = mountUpdate('tr');
    await w.setProps({ modelValue: true });
    await flushPromises();
    const notes = w.find('[data-testid="app-plugin-upgrade-notes"]');
    expect(notes.find('h4').text()).toBe('Sürüm notları');
    expect(notes.find('img').exists()).toBe(false);
    expect(notes.find('b').exists()).toBe(false);
    expect(notes.text()).toContain('<img src=x onerror="window.pwned=1">');
    expect(w.find('[data-testid="app-plugin-upgrade-signed"]').text()).toBe('İmza: imzalı → imzasız');
    w.unmount();
  });

  it('a range that leaves this filex out is said at the review, and nothing can be installed from it', async () => {
    dryRunAnswer = upgradeReview();
    const w = mountUpdate('tr');
    await w.setProps({ modelValue: true });
    await flushPromises();
    const box = w.find('[data-testid="app-plugin-incompatible"]');
    expect(box.text()).toBe('sign 1.2.0, filex >=0.48.0 ile çalışıyor; bu sunucu filex 0.47.0. Burada kurulamaz.');
    await w.find('input[type="checkbox"]').setValue(true);
    expect(w.find('[data-testid="app-plugin-install"]').attributes('disabled'), 'Install stays off').toBeDefined();
    w.unmount();
  });

  it('the server’s own refusal of an out-of-range install is a sentence, with the range and this filex', async () => {
    refuse = { status: 409, data: JSON.parse(readFileSync(path.join(WIRE, 'app-plugin-incompatible.json'), 'utf8')) };
    const w = mountWizard('tr');
    await reachReview(w);
    await w.find('input[type="checkbox"]').setValue(true);
    await w.find('[data-testid="app-plugin-install"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="app-plugin-wizard-error"]').text()).toBe(
      'Bu sürüm filex >=0.48.0 ile çalışıyor; bu sunucu filex 0.47.0, bu yüzden burada kurulamaz.',
    );
    w.unmount();
  });
});

// ── An app's own interface: the review's "Interface" group ────────────────
describe('AppPluginInstallWizard — an app with its own interface', () => {
  const FONT = 'https://fonts.example.net/inter/';
  const CSS = 'https://cdn.example.net/lib/x.css';
  const withUI = () => ({
    ...wireDryRun(),
    engine: false,
    wasm_sha256: '',
    ui: {
      sha256: 'ab'.repeat(32),
      files: 4,
      bytes: 2048,
      unpacked: 8192,
      external: [
        { url: FONT, as: 'font', mode: 'live', reason: { en: 'The Inter font', tr: 'Inter yazı tipi' } },
        { url: CSS, as: 'style', mode: 'mirror', sha256: 'cd'.repeat(32), bytes: 10, path: 'ext/cdn.example.net/lib/x.css' },
      ],
    },
  });

  beforeEach(() => {
    setActivePinia(createPinia());
    posts.length = 0;
    refuse = null;
    document.body.innerHTML = '';
  });

  it('shows the package, the addresses by risk — mirrored green, live amber — and always the honest note', async () => {
    dryRunAnswer = withUI();
    const w = mountWizard();
    await reachReview(w);
    const group = w.find('[data-testid="app-plugin-ui-group"]');
    expect(group.exists()).toBe(true);
    expect(group.text()).toContain(en.appPlugins.wizard.uiGroup.badge);
    expect(w.find('[data-testid="app-plugin-ui-sha256"]').text()).toBe('ab'.repeat(32));
    expect(w.find('[data-testid="app-plugin-ui-no-engine"]').text()).toBe(en.appPlugins.wizard.uiGroup.noEngine);
    expect(w.text(), 'no module: no module hash row').not.toContain(en.appPlugins.wizard.facts.sha256);

    const rows = w.findAll('[data-testid^="app-plugin-ui-external-"]');
    expect(rows.map((r) => r.attributes('data-testid')), 'mirrored first').toEqual(['app-plugin-ui-external-mirror', 'app-plugin-ui-external-live']);
    expect(rows[0].classes().join(' ')).toMatch(/emerald/);
    expect(rows[0].text()).toContain(en.appPlugins.wizard.uiGroup.mirror);
    expect(rows[0].text()).toContain(en.appPlugins.wizard.uiGroup.mirrorText);
    expect(rows[0].text()).toContain(CSS);
    expect(rows[1].classes().join(' ')).toMatch(/amber/);
    expect(rows[1].text()).toContain(en.appPlugins.wizard.uiGroup.live);
    expect(rows[1].text()).toContain(en.appPlugins.wizard.uiGroup.liveText);
    expect(rows[1].text()).toContain('The Inter font');
    expect(rows[1].text()).toContain(en.appPlugins.wizard.uiGroup.as.font);

    expect(w.find('[data-testid="app-plugin-ui-honest"]').text()).toBe(en.appPlugins.wizard.uiGroup.honest);
    // ⚠⚠ Never promise what a browser does not keep.
    expect(w.text()).not.toMatch(/cannot reach the network|no network|offline/i);
    w.unmount();
  });

  it('says it in Turkish on a Turkish screen, and the honest note is there with no address at all', async () => {
    const d = withUI();
    d.ui.external = [];
    dryRunAnswer = d;
    const w = mountWizard('tr');
    await reachReview(w);
    expect(w.find('[data-testid="app-plugin-ui-group"]').text()).toContain('Kendi arayüzü var');
    expect(w.find('[data-testid="app-plugin-ui-external"]').exists()).toBe(false);
    expect(w.find('[data-testid="app-plugin-ui-honest"]').text()).toBe(
      'Tarayıcılar bir arayüzün dışarı veri göndermesini tamamen engelleyemiyor (WebRTC; Chrome’da kapatılabiliyor, Firefox’ta kapatılamıyor). Bu uygulamaya, onunla açtığınız dosyaları görmesine güvendiğiniz biri kadar güvenin.',
    );
    expect(w.text()).not.toMatch(/ağa çıkamaz/i);
    w.unmount();
  });

  // ⚠ Measured against a built server (2026-09-27): the grant was the
  // manifest's list, and an app with an interface was refused
  // permissions_incomplete — `ui` is derived, it is in the review, not in
  // the manifest.
  it('grants what the review lists, the derived interface permissions included', async () => {
    const d = withUI();
    d.permissions = [...d.permissions, { id: 'ui', label: 'Runs its own interface in your browser' }];
    dryRunAnswer = d;
    const w = mountWizard();
    await reachReview(w);
    await w.find('input[type="checkbox"]').setValue(true);
    await w.find('[data-testid="app-plugin-install"]').trigger('click');
    await flushPromises();
    const real = posts.find((p) => !p.cfg?.params?.dry_run)!;
    expect((real.body as { permissions: string[] }).permissions).toContain('ui');
    expect((real.body as { permissions: string[] }).permissions).toEqual(d.permissions.map((p: { id: string }) => p.id));
    w.unmount();
  });

  it('an app without an interface has no Interface group', async () => {
    dryRunAnswer = wireDryRun();
    const w = mountWizard();
    await reachReview(w);
    expect(w.find('[data-testid="app-plugin-ui-group"]').exists()).toBe(false);
    w.unmount();
  });

  it('uploads the interface bundle as its own part', async () => {
    dryRunAnswer = withUI();
    const w = mountWizard();
    await w.find('[data-testid="app-plugin-source-file"]').trigger('click');
    const manifest = new File(['{}'], 'filex-app.json');
    const ui = new File([new Uint8Array([80, 75, 3, 4])], 'ui.zip');
    for (const [id, file] of [['app-plugin-manifest', manifest], ['app-plugin-ui', ui]] as const) {
      const input = w.find(`[data-testid="${id}"]`);
      Object.defineProperty(input.element, 'files', { value: [file], configurable: true });
      await input.trigger('change');
    }
    await w.find('form').trigger('submit');
    await flushPromises();
    const body = posts[0].body as FormData;
    const sent = body.get('ui') as File;
    expect([sent?.name, sent?.size]).toEqual(['ui.zip', 4]);
    expect((body.get('manifest') as File)?.name).toBe('filex-app.json');
    expect(body.has('wasm')).toBe(false);
    w.unmount();
  });
});
