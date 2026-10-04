// Admin → Encryption: who may START encrypting here (backend
// internal/e2epolicy). A tenant's administrator chooses the policy and answers
// the requests the approval policy leaves; the platform operator also sees
// every tenant's ceiling, beside what it caps (not on Admin → Tenants).
import { closeRowMenus, menuEntries, openRowMenu, pickMenuItem } from '../helpers/rowMenu';
import { unmountAll } from '../helpers/teardown';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

interface Call {
  method: string;
  url: string;
  body?: unknown;
  params?: unknown;
}
const calls: Call[] = [];
let policy: Record<string, unknown> = {};
let requests: Record<string, unknown>[] = [];
let tenants: Record<string, unknown>[] = [];
/** What GET /capabilities answers NOW, for whoever is signed in. */
let capabilities: Record<string, unknown> = {};
/** Calls that fail, keyed "<method> <url>" (`get /admin/e2e`): the value is thrown instead of an answer. */
let failures: Record<string, unknown> = {};

/**
 * What axios throws for a refused call: the server's `{error, message}` under
 * `response` (every refusal of the policy's handlers carries both). Flagged
 * `isAxiosError`, because the real `extractError` only reads those.
 */
function refused(status: number, data: Record<string, unknown>): Error {
  return Object.assign(new Error(`Request failed with status code ${status}`), {
    isAxiosError: true,
    response: { status, data },
  });
}

function refuse(method: string, url: string): void {
  const failure = failures[`${method} ${url}`];
  if (failure) throw failure;
}

// ⚠ The REAL extractError runs here, not a stub that hands back the fallback: a
// refusal shows the sentence the page really shows — the server's own `message`
// first, then the status's words, then the caller's (api/client.ts).
vi.mock('@/api/client', async (importOriginal) => ({
  extractError: (await importOriginal<typeof import('@/api/client')>()).extractError,
  api: {
    get: vi.fn(async (url: string, cfg?: { params?: unknown }) => {
      calls.push({ method: 'get', url, params: cfg?.params });
      refuse('get', url);
      if (url === '/capabilities') return { data: capabilities };
      if (url === '/admin/e2e') return { data: policy };
      if (url === '/admin/e2e/requests') return { data: { requests, ttl_days: 7 } };
      if (url === '/admin/e2e/tenants') return { data: { tenants } };
      return { data: {} };
    }),
    patch: vi.fn(async (url: string, body: Record<string, unknown>) => {
      calls.push({ method: 'patch', url, body });
      refuse('patch', url);
      if (url === '/admin/e2e') return { data: { ...policy, ...body } };
      const id = Number(url.split('/').pop());
      return { data: { ...tenants.find((x) => x.id === id), ...body } };
    }),
    post: vi.fn(async (url: string, body: unknown) => {
      calls.push({ method: 'post', url, body });
      refuse('post', url);
      const status = url.endsWith('/approve') ? 'approved' : 'rejected';
      return { data: { request: { ...requests[0], status } } };
    }),
    put: vi.fn(),
    delete: vi.fn(),
  },
}));

const toasts: { kind: string; msg: string }[] = [];
vi.mock('@/stores/toast', () => ({
  useToastStore: () => ({
    success: (msg: string) => toasts.push({ kind: 'success', msg }),
    warn: (msg: string) => toasts.push({ kind: 'warn', msg }),
    error: (msg: string) => toasts.push({ kind: 'error', msg }),
  }),
}));

import Encryption from '@/views/Encryption.vue';
import { useCapabilitiesStore } from '@/stores/capabilities';

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () {
    this.setAttribute('open', '');
  };
  HTMLDialogElement.prototype.close = function () {
    this.removeAttribute('open');
  };
}

function waiting(): Record<string, unknown> {
  return {
    id: 7,
    path: 'Muhasebe-Bulut://Maaşlar',
    storage: 'Muhasebe-Bulut',
    kind: 'folder',
    reason: 'Bordro dosyaları',
    status: 'pending',
    requester: 'Ayşe',
    requester_id: 12,
    expires_at: '2026-10-06T10:00:00Z',
    created_at: '2026-09-29T10:00:00Z',
    tenant_id: 3,
  };
}

/**
 * `operator`: who is signed in NOW (what /capabilities answers). `held`: what
 * the capabilities store already holds when the page opens — App.vue fetches
 * it once per document, so after an in-page password sign-in it is the answer
 * given while nobody was signed in. Unless a test says otherwise, the two agree.
 */
async function page(opts: { operator?: boolean; held?: boolean; locale?: 'en' | 'tr' } = {}) {
  const caps = useCapabilitiesStore();
  capabilities = { caller_admin: opts.operator === true };
  caps.data = { ...caps.data, caller_admin: opts.held ?? opts.operator === true };
  const i18n = createI18n({ legacy: false, locale: opts.locale ?? 'en', fallbackLocale: 'en', messages: { en, tr } });
  const w = mount(Encryption, { global: { plugins: [i18n] }, attachTo: document.body });
  await flushPromises();
  await flushPromises();
  return w;
}

const asked = (method: string, url: string) => calls.filter((c) => c.method === method && c.url === url);

beforeEach(() => {
  setActivePinia(createPinia());
  calls.length = 0;
  toasts.length = 0;
  failures = {};
  policy = { available: true, policy: 'permitted', scope: 'tenant', tenant: { id: 3, name: 'acme' }, pending: 1 };
  requests = [waiting()];
  tenants = [
    { id: 1, slug: 'hosting', name: 'hosting', is_supertenant: true, e2e_allowed: true, e2e_policy: 'permitted' },
    { id: 3, slug: 'acme', name: 'acme', is_supertenant: false, e2e_allowed: true, e2e_policy: 'approval' },
  ];
  closeRowMenus();
});

describe('Admin → Encryption — the policy', () => {
  it('shows the tenant’s policy and saves a new one with the body the server reads', async () => {
    const w = await page();
    expect(w.find('[data-testid="encryption-policy"]').text()).toContain('Applies to acme.');
    const select = w.find('select[name="encryption-policy"]');
    expect((select.element as HTMLSelectElement).value).toBe('permitted');
    expect(w.find('[data-testid="encryption-policy-save"]').attributes('disabled'), 'nothing to save yet').toBeDefined();
    await select.setValue('approval');
    expect(w.text()).toContain('anyone who is not an administrator needs an approval here first');
    await w.find('[data-testid="encryption-policy-save"]').trigger('click');
    await flushPromises();
    expect(asked('patch', '/admin/e2e')).toEqual([{ method: 'patch', url: '/admin/e2e', body: { policy: 'approval' } }]);
    expect(toasts).toContainEqual({ kind: 'success', msg: 'Encryption policy saved' });
    w.unmount();
  });

  // Operator decision 2026-10-03: an approval opens its own kind - a folder
  // where it is, one new folder in a folder, one new file - and never a folder
  // below the one asked about.
  it('says what an approval opens: its own kind, in the folder asked about, never below it', async () => {
    const w = await page();
    await w.find('select[name="encryption-policy"]').setValue('approval');
    const hint = w.find('[data-testid="encryption-policy"]').text();
    expect(hint).toContain('is used once');
    expect(hint).toContain('one folder encrypted where it is');
    expect(hint).toContain('one new encrypted folder directly inside a folder');
    expect(hint).toContain('one new encrypted file');
    expect(hint).toContain('It never reaches the folders below.');
    // Not "one folder or file": that reads as an approval for that path alone.
    expect(hint).not.toContain('one folder or file');
    w.unmount();
  });

  it('says an administrator is never asked, and who is: everybody else — in both languages', async () => {
    // Decide (backend e2epolicy): an administrator is allowed outright under every
    // policy but off and admins. "Each encryption needs an approval" read as if
    // theirs did too.
    let w = await page();
    await w.find('select[name="encryption-policy"]').setValue('approval');
    let hint = w.find('[data-testid="encryption-policy"]').text();
    expect(hint).toContain('anyone who is not an administrator needs an approval here first. Administrators are never asked.');
    expect(hint).not.toContain('each encryption needs an approval');
    w.unmount();

    closeRowMenus();
    unmountAll();
    w = await page({ locale: 'tr' });
    await w.find('select[name="encryption-policy"]').setValue('approval');
    hint = w.find('[data-testid="encryption-policy"]').text();
    expect(hint).toContain('yönetici olmayan herkesin şifrelemeden önce burada onay alması gerekir. Yöneticilerden onay istenmez.');
    w.unmount();
  });

  it('a refused save says so in the card and keeps the choice where the person left it', async () => {
    failures['patch /admin/e2e'] = refused(400, {
      error: 'invalid_policy',
      message: 'policy must be one of off, admins, permitted, approval',
    });
    const w = await page();
    await w.find('select[name="encryption-policy"]').setValue('off');
    await w.find('[data-testid="encryption-policy-save"]').trigger('click');
    await flushPromises();
    // The server's own sentence, through the real extractError.
    expect(w.find('[data-testid="encryption-policy"] [role="alert"]').text()).toBe(
      'policy must be one of off, admins, permitted, approval',
    );
    expect(toasts, 'nothing was saved, so nothing is announced as saved').toEqual([]);
    expect((w.find('select[name="encryption-policy"]').element as HTMLSelectElement).value).toBe('off');
    w.unmount();
  });

  it('a refused save’s message is taken down when the choice changes: it was about the other choice', async () => {
    failures['patch /admin/e2e'] = refused(400, { error: 'invalid_policy', message: 'policy must be one of off, admins, permitted, approval' });
    const w = await page();
    const select = w.find('select[name="encryption-policy"]');
    await select.setValue('off');
    await w.find('[data-testid="encryption-policy-save"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="encryption-policy"] [role="alert"]').exists()).toBe(true);
    await select.setValue('admins');
    expect(w.find('[data-testid="encryption-policy"] [role="alert"]').exists()).toBe(false);
    w.unmount();
  });

  it('says so when the platform operator has switched encryption off for this tenant', async () => {
    policy = { ...policy, available: false };
    const w = await page({ locale: 'tr' });
    expect(w.find('[data-testid="encryption-unavailable"]').text()).toContain('Platform işletmecisi');
    // The choice stays: it takes effect when encryption is switched on again.
    expect(w.find('select[name="encryption-policy"]').exists()).toBe(true);
    w.unmount();
  });

  it('a single-tenant install says the policy is the whole server’s, and has no tenants to cap', async () => {
    policy = { available: true, policy: 'off', scope: 'instance', tenant: null, pending: 0 };
    const w = await page({ operator: true });
    expect(w.find('[data-testid="encryption-policy"]').text()).toContain('Applies to this whole server.');
    expect(w.find('[data-testid="encryption-tenants"]').exists()).toBe(false);
    expect(asked('get', '/admin/e2e/tenants')).toEqual([]);
    w.unmount();
  });
});

describe('Admin → Encryption — the requests', () => {
  it('lists a waiting request in the explorer table: the folder, who asked and why', async () => {
    const w = await page({ locale: 'tr' });
    expect(asked('get', '/admin/e2e/requests')[0].params).toEqual({ status: 'pending' });
    expect(w.find('[data-testid="e2e-request-7"]').text()).toContain('Muhasebe-Bulut://Maaşlar');
    expect(w.find('[data-testid="e2e-request-7"]').text()).toContain('Bu klasör, olduğu yerde şifrelenir');
    expect(w.text()).toContain('Ayşe');
    expect(w.text()).toContain('Bordro dosyaları');
    expect(w.find('[data-testid="encryption-requests-count"]').text()).toBe('1 bekliyor');
    expect(w.find('[data-testid="e2e-request-status-7"]').text()).toBe('Bekliyor');
    w.unmount();
  });

  it('a file request’s row says where it lands: a location, and one new encrypted file in that folder', async () => {
    // A single file's request is kept under the folder its `.fxe` lands in, so
    // the path is a FOLDER. Over the sub-line "File" it read as a file named
    // after the folder.
    requests = [{ ...waiting(), kind: 'file' }];
    let w = await page();
    let section = w.find('[data-testid="encryption-requests"]');
    expect(section.find('[data-testid="e2e-request-7"]').text()).toContain('Muhasebe-Bulut://Maaşlar');
    expect(section.find('[data-testid="e2e-request-7"]').text()).toContain('One new encrypted file in this folder');
    expect(section.text()).toContain('Location');
    expect(section.text()).not.toContain('Folder or file');
    w.unmount();

    closeRowMenus();
    unmountAll();
    w = await page({ locale: 'tr' });
    section = w.find('[data-testid="encryption-requests"]');
    expect(section.find('[data-testid="e2e-request-7"]').text()).toContain('Bu klasörde yeni bir şifreli dosya');
    expect(section.text()).toContain('Konum');
    expect(section.text()).not.toContain('Klasör ya da dosya');
    w.unmount();
  });

  // Operator decision 2026-10-03: the kinds are separate, and the row says
  // which one was asked for: the folder where it is, or a new folder in it.
  it('a folder request says which kind it is under its location', async () => {
    let w = await page();
    expect(w.find('[data-testid="e2e-request-kind-7"]').text()).toBe('This folder, encrypted where it is');
    expect(w.find('[data-testid="e2e-request-7"]').text()).not.toContain('One new encrypted file');
    w.unmount();

    closeRowMenus();
    unmountAll();
    requests = [{ ...waiting(), kind: 'new_folder' }];
    w = await page({ locale: 'tr' });
    expect(w.find('[data-testid="e2e-request-kind-7"]').text()).toBe('Bu klasörde yeni bir şifreli klasör');
    w.unmount();
  });

  it('approves with an optional note', async () => {
    const w = await page();
    await openRowMenu(w, 'e2e-request-actions-7');
    expect(menuEntries().map((e) => e.label)).toEqual(['Approve', 'Reject…']);
    await pickMenuItem('e2e-request-actions-7-approve');
    await flushPromises();
    const dialog = w.find('[data-testid="e2e-answer"]');
    // An in-place approval is for the folder asked about, where it is, and for
    // no folder inside it (operator decision 2026-10-03).
    expect(dialog.find('[data-testid="e2e-request-approve-body"]').text()).toBe(
      'Ayşe may then encrypt Muhasebe-Bulut://Maaşlar where it is. The approval can be used once, and not on a folder inside it.',
    );
    await dialog.find('textarea').setValue('  For the audit  ');
    await dialog.find('[data-testid="e2e-answer-send"]').trigger('click');
    await flushPromises();
    expect(calls.filter((c) => c.method === 'post')).toEqual([
      { method: 'post', url: '/admin/e2e/requests/7/approve', body: { note: 'For the audit' } },
    ]);
    expect(toasts[0]?.kind).toBe('success');
    w.unmount();
  });

  it('a file request is approved for one new encrypted file in the folder it is kept under', async () => {
    // The server keeps a file's request under the folder its `.fxe` lands in
    // (e2epolicy.ApprovalPath): the path is that folder, the kind says "file".
    requests = [{ ...waiting(), kind: 'file' }];
    const w = await page();
    await openRowMenu(w, 'e2e-request-actions-7');
    await pickMenuItem('e2e-request-actions-7-approve');
    await flushPromises();
    const text = w.find('[data-testid="e2e-answer"]').text();
    // The dialog's first row is the same place, under the same label.
    expect(w.find('[data-testid="e2e-answer"] dl').text()).toMatch(/Location\s*Muhasebe-Bulut:\/\/Maaşlar/);
    expect(text).toContain('Ayşe may then create one new encrypted file in Muhasebe-Bulut://Maaşlar.');
    expect(text).toContain('The approval can be used once.');
    expect(text, 'the folder sentence does not apply to a file request').not.toContain('one folder directly inside it');
    w.unmount();
  });

  it('says the same, in Turkish, for a folder and for a file', async () => {
    let w = await page({ locale: 'tr' });
    await openRowMenu(w, 'e2e-request-actions-7');
    await pickMenuItem('e2e-request-actions-7-approve');
    await flushPromises();
    let text = w.find('[data-testid="e2e-answer"]').text();
    expect(text).toContain('Ayşe bundan sonra Muhasebe-Bulut://Maaşlar klasörünü olduğu yerde şifreleyebilir.');
    expect(text).toContain('Onay bir kez kullanılabilir; içindeki bir klasör için kullanılamaz.');
    w.unmount();

    closeRowMenus();
    unmountAll();
    requests = [{ ...waiting(), kind: 'file' }];
    w = await page({ locale: 'tr' });
    await openRowMenu(w, 'e2e-request-actions-7');
    await pickMenuItem('e2e-request-actions-7-approve');
    await flushPromises();
    text = w.find('[data-testid="e2e-answer"]').text();
    expect(text).toContain('Ayşe bundan sonra Muhasebe-Bulut://Maaşlar içinde yeni bir şifreli dosya oluşturabilir.');
    w.unmount();
  });

  it('rejects only with a reason — the requester is told why', async () => {
    const w = await page();
    await openRowMenu(w, 'e2e-request-actions-7');
    await pickMenuItem('e2e-request-actions-7-reject');
    await flushPromises();
    const dialog = w.find('[data-testid="e2e-answer"]');
    await dialog.find('[data-testid="e2e-answer-send"]').trigger('click');
    await flushPromises();
    expect(calls.filter((c) => c.method === 'post'), 'a no without a reason is not sent').toEqual([]);
    expect(w.find('[data-testid="e2e-answer-error"]').text()).toBe('Write why - the requester is told.');
    await dialog.find('textarea').setValue('Payroll stays readable for HR');
    await dialog.find('[data-testid="e2e-answer-send"]').trigger('click');
    await flushPromises();
    expect(calls.filter((c) => c.method === 'post')).toEqual([
      { method: 'post', url: '/admin/e2e/requests/7/reject', body: { reason: 'Payroll stays readable for HR' } },
    ]);
    expect(toasts).toContainEqual({ kind: 'success', msg: 'Request rejected: Ayşe' });
    w.unmount();
  });

  it('a request answered meanwhile is said as such, and the list is read again', async () => {
    failures['post /admin/e2e/requests/7/approve'] = refused(409, {
      error: 'not_pending',
      message: 'this request was decided meanwhile',
    });
    const w = await page();
    await openRowMenu(w, 'e2e-request-actions-7');
    await pickMenuItem('e2e-request-actions-7-approve');
    await flushPromises();
    await w.find('[data-testid="e2e-answer-send"]').trigger('click');
    await flushPromises();
    expect(toasts).toEqual([
      { kind: 'warn', msg: 'This request is no longer waiting: it was answered or it lapsed.' },
    ]);
    expect(asked('get', '/admin/e2e/requests')).toHaveLength(2);
    w.unmount();
  });

  it('says the same in Turkish: answered, or its time ran out', async () => {
    // The refusal is the same 409 for a request somebody answered and one that
    // lapsed (an approval unused for 7 days, a request nobody decided).
    failures['post /admin/e2e/requests/7/approve'] = refused(409, {
      error: 'not_pending',
      message: 'this request is already expired',
    });
    const w = await page({ locale: 'tr' });
    await openRowMenu(w, 'e2e-request-actions-7');
    await pickMenuItem('e2e-request-actions-7-approve');
    await flushPromises();
    await w.find('[data-testid="e2e-answer-send"]').trigger('click');
    await flushPromises();
    expect(toasts).toEqual([{ kind: 'warn', msg: 'Bu istek artık beklemiyor: yanıtlanmış ya da süresi dolmuş.' }]);
    w.unmount();
  });

  it('the history says who answered, how and what they said, as text under the status', async () => {
    const answered = (over: Record<string, unknown>) => ({
      ...waiting(),
      status: 'approved',
      decider: 'Berk',
      decided_at: '2026-09-30T10:00:00Z',
      ...over,
    });
    requests = [
      waiting(),
      answered({ id: 8, decision_note: 'For the audit' }),
      answered({ id: 9, status: 'rejected', decision_note: 'Payroll stays readable for HR' }),
      answered({ id: 10 }),
      { ...waiting(), id: 11, status: 'expired' },
      answered({ id: 12, status: 'used' }),
      answered({ id: 13, status: 'expired' }),
    ];
    const w = await page();
    await w.find('[data-testid="encryption-requests-history"]').trigger('click');
    await flushPromises();
    const said = (id: number) => w.find(`[data-testid="e2e-request-answer-${id}"]`);
    expect(said(8).text()).toContain('approved by Berk');
    expect(said(8).text()).toContain('For the audit');
    expect(said(9).text()).toContain('rejected by Berk');
    expect(said(9).text()).toContain('Payroll stays readable for HR');
    expect(said(10).text(), 'an answer with no note says only who').toBe('approved by Berk');
    expect(said(7).exists(), 'nobody has answered a waiting request').toBe(false);
    expect(said(11).exists(), 'nobody answered an expired one either').toBe(false);
    // Under Used and Expired the line is the approval's: Berk approved it,
    // somebody else used it, or nobody did in time.
    expect(said(12).text(), 'a used approval').toBe('approved by Berk');
    expect(said(13).text(), 'an approval that expired unused').toBe('approved by Berk');
    // The badge still says what it is.
    expect(w.find('[data-testid="e2e-request-status-9"]').text()).toBe('Rejected');
    expect(w.find('[data-testid="e2e-request-status-12"]').text()).toBe('Used');
    expect(w.find('[data-testid="e2e-request-status-13"]').text()).toBe('Expired');
    w.unmount();
  });

  it('says who answered, and how, in Turkish', async () => {
    requests = [
      { ...waiting(), status: 'approved', decider: 'Berk', decision_note: 'Tamam' },
      { ...waiting(), id: 8, status: 'rejected', decider: 'Berk', decision_note: 'Hayır' },
    ];
    const w = await page({ locale: 'tr' });
    await w.find('[data-testid="encryption-requests-history"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="e2e-request-answer-7"]').text()).toContain('Berk tarafından onaylandı');
    expect(w.find('[data-testid="e2e-request-answer-7"]').text()).toContain('Tamam');
    expect(w.find('[data-testid="e2e-request-answer-8"]').text()).toContain('Berk tarafından reddedildi');
    w.unmount();
  });

  it('a new-folder request is approved for one new encrypted folder inside, never for one that holds something', async () => {
    requests = [{ ...waiting(), kind: 'new_folder' }];
    const w = await page();
    await openRowMenu(w, 'e2e-request-actions-7');
    await pickMenuItem('e2e-request-actions-7-approve');
    await flushPromises();
    expect(w.find('[data-testid="e2e-request-approve-body"]').text()).toBe(
      'Ayşe may then create one new encrypted folder directly inside Muhasebe-Bulut://Maaşlar. The approval can be used once, and not on a folder that already holds something.',
    );
    w.unmount();
  });

  // Operator decision 2026-10-03: the platform operator sees every tenant's
  // requests and answers only the platform's own (the server marks the row
  // `decidable`). Another tenant's row offers no Approve or Reject, says who
  // answers it, and is not counted as waiting for the operator.
  it('the operator sees another tenant’s request but is offered no answer to it', async () => {
    policy = { ...policy, tenant: { id: 1, name: 'hosting' } };
    requests = [
      { ...waiting(), decidable: false },
      { ...waiting(), id: 8, tenant_id: 1, requester: 'Berk', decidable: true },
    ];
    const w = await page({ operator: true });
    expect(w.find('[data-testid="e2e-request-actions-7"]').exists()).toBe(false);
    expect(w.find('[data-testid="e2e-request-elsewhere-7"]').text()).toBe("Its tenant's administrators answer it.");
    expect(w.find('[data-testid="e2e-request-actions-8"]').exists()).toBe(true);
    expect(w.find('[data-testid="e2e-request-elsewhere-8"]').exists()).toBe(false);
    expect(w.find('[data-testid="encryption-requests-count"]').text()).toBe('1 waiting');
    await openRowMenu(w, 'e2e-request-actions-8');
    expect(menuEntries().map((e) => e.label)).toEqual(['Approve', 'Reject…']);
    w.unmount();
  });

  it('names each request’s tenant for the platform operator — whose list mixes tenants — and for nobody else', async () => {
    policy = { ...policy, tenant: { id: 1, name: 'hosting' } };
    requests = [waiting(), { ...waiting(), id: 8, tenant_id: 1, requester: 'Berk' }];
    let w = await page({ operator: true });
    let section = w.find('[data-testid="encryption-requests"]');
    expect(section.text()).toContain('Tenant');
    expect(section.find('[data-testid="e2e-request-tenant-7"]').text()).toBe('acme');
    expect(section.find('[data-testid="e2e-request-tenant-8"]').text()).toBe('hosting');
    w.unmount();

    // A tenant's administrator reads one tenant's requests: a column of one name is noise.
    closeRowMenus();
    unmountAll();
    w = await page();
    section = w.find('[data-testid="encryption-requests"]');
    expect(section.find('[data-testid="e2e-request-tenant-7"]').exists()).toBe(false);
    expect(section.text()).not.toContain('Tenant');
    w.unmount();
  });

  it('shows the answered ones on request', async () => {
    const w = await page();
    await w.find('[data-testid="encryption-requests-history"]').trigger('click');
    await flushPromises();
    expect(asked('get', '/admin/e2e/requests').map((c) => c.params)).toEqual([{ status: 'pending' }, { status: 'all' }]);
    w.unmount();
  });
});

describe('Admin → Encryption — the tenants (the platform operator only)', () => {
  it('is not drawn — nor asked for — for a tenant’s administrator', async () => {
    const w = await page();
    expect(w.find('[data-testid="encryption-tenants"]').exists()).toBe(false);
    expect(asked('get', '/admin/e2e/tenants')).toEqual([]);
    w.unmount();
  });

  it('appears after an in-page password sign-in: the page asks who is signed in now, not the snapshot taken before', async () => {
    // A password sign-in stays inside the page (no reload), and the capabilities
    // were fetched once, while nobody was signed in: `caller_admin` was false.
    policy = { ...policy, tenant: { id: 1, name: 'hosting' } };
    const w = await page({ operator: true, held: false });
    expect(w.find('[data-testid="encryption-tenants"]').exists()).toBe(true);
    expect(asked('get', '/capabilities'), 'the page asks again instead of trusting the snapshot').toHaveLength(1);
    expect(asked('get', '/admin/e2e/tenants')).toHaveLength(1);
    w.unmount();
  });

  it('is gone again when the snapshot said operator and the person who signed in since is a tenant’s administrator', async () => {
    const w = await page({ operator: false, held: true });
    expect(w.find('[data-testid="encryption-tenants"]').exists()).toBe(false);
    expect(asked('get', '/capabilities')).toHaveLength(1);
    w.unmount();
  });

  it('lists every tenant with its policy and switches one off with the body the server reads', async () => {
    policy = { ...policy, tenant: { id: 1, name: 'hosting' } };
    const w = await page({ operator: true });
    const panel = w.find('[data-testid="encryption-tenants"]');
    expect(panel.exists()).toBe(true);
    expect(panel.find('[data-testid="e2e-tenant-1"]').text()).toContain('Platform operator');
    expect(panel.text()).toContain('Everyone whose role allows it, after an administrator’s approval');

    await panel.find('#e2e-tenant-allowed-3').trigger('click');
    await flushPromises();
    expect(asked('patch', '/admin/e2e/tenants/3')).toEqual([
      { method: 'patch', url: '/admin/e2e/tenants/3', body: { e2e_allowed: false } },
    ]);
    expect(toasts).toContainEqual({ kind: 'success', msg: 'Encryption switched off for acme' });
    // Another tenant moved: this page's own answer did not.
    expect(asked('get', '/admin/e2e')).toHaveLength(1);

    await w.find('#e2e-tenant-allowed-1').trigger('click');
    await flushPromises();
    // Its own tenant moved: the answer is read again, for the banner.
    expect(asked('get', '/admin/e2e')).toHaveLength(2);
    w.unmount();
  });
});

describe('Admin → Encryption — a refused ceiling switch', () => {
  it('is said, and the row stays as it was', async () => {
    // A single-tenant install answers 409 `single_tenant` to this call. The page
    // never draws the panel there (`scope: 'instance'`), but whatever refuses a
    // switch must not flip it: a row changes only from the server's own answer.
    const single =
      'This server is not multi-tenant, so there is no tenant whose encryption can be switched on or off. ' +
      'The encryption policy alone decides who may encrypt.';
    failures['patch /admin/e2e/tenants/1'] = refused(409, { error: 'single_tenant', message: single });
    policy = { ...policy, tenant: { id: 1, name: 'hosting' } };
    const w = await page({ operator: true });
    // Tenant 1 is this page's own: a change that DID happen would read the answer again.
    await w.find('#e2e-tenant-allowed-1').trigger('click');
    await flushPromises();
    expect(toasts).toEqual([{ kind: 'error', msg: single }]);
    expect(w.find('#e2e-tenant-allowed-1').attributes('aria-checked')).toBe('true');
    expect(asked('get', '/admin/e2e'), 'a change that did not happen is not read back').toHaveLength(1);
    w.unmount();
  });
});

// ── what goes wrong ─────────────────────────────────────────────────────
//
// Every refusal below is an axios-shaped error carrying the server's own
// `{error, message}`, and the REAL extractError turns it into the sentence on
// the screen: the server's `message` when it gave one, the page's own words
// when it did not (a bare 5xx says only that it failed).

describe('Admin → Encryption — when a read fails', () => {
  it('says so in the page, in the server’s own words, and draws nothing that needs the policy', async () => {
    failures['get /admin/e2e'] = refused(500, { error: 'failed', message: 'database is locked' });
    const w = await page({ operator: true });
    expect(w.find('[data-testid="encryption-page"] [role="alert"]').text()).toBe('database is locked');
    expect(w.find('[data-testid="encryption-policy"]').exists()).toBe(false);
    expect(w.find('[data-testid="encryption-requests"]').exists()).toBe(false);
    expect(w.find('[data-testid="encryption-tenants"]').exists()).toBe(false);
    expect(asked('get', '/admin/e2e/requests'), 'nothing is asked of a page that has no policy').toEqual([]);
    expect(asked('get', '/admin/e2e/tenants')).toEqual([]);
    w.unmount();
  });

  it('says it in the page’s own words when the server gave none — in the reader’s language', async () => {
    failures['get /admin/e2e'] = refused(500, {});
    const w = await page({ locale: 'tr' });
    expect(w.find('[data-testid="encryption-page"] [role="alert"]').text()).toBe('Yüklenemedi. Yeniden deneyin.');
    w.unmount();
  });

  it('a failed read of the requests is a toast, the rest of the page stands, and Refresh tries again', async () => {
    failures['get /admin/e2e/requests'] = refused(503, { error: 'failed', message: 'the store is starting up' });
    const w = await page();
    expect(toasts).toEqual([{ kind: 'error', msg: 'the store is starting up' }]);
    expect(w.find('[data-testid="encryption-policy"]').exists(), 'the policy does not depend on the list').toBe(true);
    expect(w.find('[data-testid="e2e-request-7"]').exists()).toBe(false);

    delete failures['get /admin/e2e/requests'];
    const refresh = w.findAll('button').find((b) => b.text() === 'Refresh');
    expect(refresh, 'the Refresh button').toBeDefined();
    await refresh!.trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="e2e-request-7"]').exists()).toBe(true);
    expect(toasts, 'the second read said nothing').toHaveLength(1);
    w.unmount();
  });

  it('a failed read of the requests is not an empty list: the table says it could not be read', async () => {
    failures['get /admin/e2e/requests'] = refused(503, { error: 'failed', message: 'the store is starting up' });
    const w = await page();
    const section = w.find('[data-testid="encryption-requests"]');
    expect(section.text()).toContain('The requests could not be loaded.');
    expect(section.text(), 'nothing says no request is waiting').not.toContain('No request is waiting.');
    w.unmount();
  });

  it('a failed read of the answered ones keeps the list it had, and says which list that is', async () => {
    const w = await page();
    expect(w.find('[data-testid="e2e-request-7"]').exists()).toBe(true);
    failures['get /admin/e2e/requests'] = refused(503, { error: 'failed', message: 'the store is starting up' });
    await w.find('[data-testid="encryption-requests-history"]').trigger('click');
    await flushPromises();
    expect(toasts).toEqual([{ kind: 'error', msg: 'the store is starting up' }]);
    expect(w.find('[data-testid="encryption-requests-history"]').text(), 'the button says the waiting list is still shown').toBe('Show answered requests');
    expect(w.find('[data-testid="e2e-request-7"]').exists(), 'the waiting request is still listed').toBe(true);

    delete failures['get /admin/e2e/requests'];
    const refresh = w.findAll('button').find((b) => b.text() === 'Refresh');
    await refresh!.trigger('click');
    await flushPromises();
    expect(asked('get', '/admin/e2e/requests').map((c) => c.params), 'Refresh reads the list that is shown').toEqual([
      { status: 'pending' },
      { status: 'all' },
      { status: 'pending' },
    ]);
    w.unmount();
  });

  it('says it in Turkish', async () => {
    failures['get /admin/e2e/requests'] = refused(503, {});
    const w = await page({ locale: 'tr' });
    expect(w.find('[data-testid="encryption-requests"]').text()).toContain('İstekler yüklenemedi.');
    w.unmount();
  });

  it('a refused list of tenants (403) draws no tenants panel and prints nothing: the snapshot was wrong', async () => {
    // The capabilities said operator, the account is not: the server's answer to
    // the list (`supertenant_only`) is the last word, and it is not an error to print.
    failures['get /admin/e2e/tenants'] = refused(403, {
      error: 'supertenant_only',
      message: 'only the platform operator can do this',
    });
    policy = { ...policy, tenant: { id: 1, name: 'hosting' } };
    const w = await page({ operator: true });
    expect(asked('get', '/admin/e2e/tenants'), 'it was asked').toHaveLength(1);
    expect(w.find('[data-testid="encryption-tenants"]').exists()).toBe(false);
    expect(toasts).toEqual([]);
    expect(w.find('[data-testid="encryption-policy"]').exists()).toBe(true);
    expect(w.find('[data-testid="e2e-request-7"]').exists()).toBe(true);
    expect(w.find('[data-testid="e2e-request-tenant-7"]').exists(), 'no list was read, so no tenant is named').toBe(false);
    w.unmount();
  });

  it('a tenants list that fails for any other reason is said, and the panel stays', async () => {
    failures['get /admin/e2e/tenants'] = refused(500, { error: 'failed', message: 'the tenants table is locked' });
    policy = { ...policy, tenant: { id: 1, name: 'hosting' } };
    const w = await page({ operator: true });
    expect(toasts).toEqual([{ kind: 'error', msg: 'the tenants table is locked' }]);
    expect(w.find('[data-testid="encryption-tenants"]').exists()).toBe(true);
    expect(w.find('[data-testid="e2e-request-tenant-7"]').exists(), 'no list was read, so no tenant is named').toBe(false);
    w.unmount();
  });
});

describe('Admin → Encryption — an answer that fails for any reason but "already answered"', () => {
  // `not_pending` (409) is its own case, above: the request changed under the
  // administrator, so the list is read again. Anything else changed nothing:
  // the dialog stays, says why, and keeps what was typed.
  const cases = [
    {
      verb: 'approve',
      what: 'an approval',
      typed: 'For the audit',
      failure: refused(403, {
        error: 'session_required',
        message:
          'approving an encryption request needs an administrator signed in to the admin panel; an API key cannot do it.',
      }),
      said: 'approving an encryption request needs an administrator signed in to the admin panel; an API key cannot do it.',
      success: 'Request approved: Ayşe',
    },
    {
      verb: 'reject',
      what: 'a rejection',
      typed: 'Payroll stays readable for HR',
      // A bare 5xx: no sentence from the server, so the page says it in its own words.
      failure: refused(500, {}),
      said: 'Your changes could not be saved. Try again.',
      success: 'Request rejected: Ayşe',
    },
  ];

  for (const c of cases) {
    it(`${c.what} that fails keeps the dialog open with what was typed, says why, and does not read the list again`, async () => {
      const url = `/admin/e2e/requests/7/${c.verb}`;
      failures[`post ${url}`] = c.failure;
      const w = await page();
      await openRowMenu(w, 'e2e-request-actions-7');
      await pickMenuItem(`e2e-request-actions-7-${c.verb}`);
      await flushPromises();
      await w.find('[data-testid="e2e-answer"] textarea').setValue(c.typed);
      await w.find('[data-testid="e2e-answer-send"]').trigger('click');
      await flushPromises();

      expect(w.find('[data-testid="e2e-answer-error"]').text()).toBe(c.said);
      expect(w.find('[data-testid="e2e-answer"]').exists(), 'the dialog stays open').toBe(true);
      expect((w.find('[data-testid="e2e-answer"] textarea').element as HTMLTextAreaElement).value).toBe(c.typed);
      expect(asked('get', '/admin/e2e/requests'), 'nothing changed, so the list is not read again').toHaveLength(1);
      expect(toasts, 'and nothing is announced').toEqual([]);
      expect(w.find('[data-testid="e2e-request-status-7"]').text()).toBe('Waiting');

      // The person tries again, and it goes through: the dialog closes, the list is read.
      delete failures[`post ${url}`];
      await w.find('[data-testid="e2e-answer-send"]').trigger('click');
      await flushPromises();
      expect(w.find('[data-testid="e2e-answer"]').exists()).toBe(false);
      expect(toasts).toEqual([{ kind: 'success', msg: c.success }]);
      expect(asked('get', '/admin/e2e/requests')).toHaveLength(2);
      w.unmount();
    });
  }
});

describe('Admin → Encryption — the route', () => {
  it('is the administrator’s alone: no `adminPerm` lets a delegated admin.* holder in', async () => {
    // ⚠ The mount base is read once, at module load (router/index.ts): import it fresh.
    window.history.replaceState({}, '', '/admin/');
    vi.resetModules();
    const router = (await import('@/router')).default;
    const meta = router.resolve({ name: 'encryption' }).meta;
    // The panel's own marker comes from the layout record above it…
    expect(meta.requiresAdmin).toBe(true);
    // …and the page names no permission: the policy binds administrators too, so
    // an account that holds `admin.audit` without the role is not who decides it.
    expect(meta.adminPerm).toBeUndefined();
    expect(meta.breadcrumb).toBe('nav.encryption');
    expect(router.resolve({ name: 'encryption' }).path).toBe('/encryption');
  }, 20_000);
});
