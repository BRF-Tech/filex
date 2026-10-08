// Three admin forms that used to send what they should have refused.
//
// Release-candidate sweep, 2026-09-21:
//
//   · ADD USER with nothing filled in reached the server and came back as
//     "email required" — English, in a toast drawn behind the dialog's
//     backdrop, so nobody read it;
//   · NEW WEBHOOK marked no field required, answered an empty save with a
//     toast behind the backdrop ("İsim ve URL zorunlu"), and a URL that was
//     not http(s) with the server's English;
//   · NEW API/MCP TOKEN offered Create with no scope ticked and said "If none
//     are selected, all scopes are granted" — and such a token read the admin
//     API. The owner's decision: no token without an explicit list, `admin`
//     never implied.
//
// What has to stay true, for all three: nothing is SENT while the form knows
// it is wrong; what is wrong is said INSIDE the dialog, under the box it is
// about, in the reader's language; and what the server still refuses lands
// in the dialog too — never in a toast behind it.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';

import { ACCOUNT_CHECK_DELAY_MS } from '@brftech/filex-core/src/lib/accountRules';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

// The server's account check (POST /api/auth/account/check), asked while the
// Add user form is typed (0.54 #209: the form keeps no rule of its own). A
// unit test never reaches the network (helpers/noNetwork), so it answers here.
const checkAccount = vi.fn(async (_q: { email?: string; username?: string; for?: string }) => ({}) as Record<string, { error: string; message: string }>);
vi.mock('@/api/auth', async (importOriginal) => {
  const real = await importOriginal<typeof import('@/api/auth')>();
  return { ...real, AuthApi: { ...real.AuthApi, checkAccount: (q: { email?: string; username?: string; for?: string }) => checkAccount(q) } };
});
const usersCreate = vi.fn();
// The Users list's Groups column and filter, and the Add user dialog's
// groups (best effort).
vi.mock('@/api/groups', () => ({
  GroupsApi: { list: vi.fn(async () => []), memberships: vi.fn(async () => ({})), forUser: vi.fn(async () => []), addMembers: vi.fn() },
}));
vi.mock('@/api/users', () => ({
  UsersApi: {
    list: vi.fn(async () => ({ items: [], total: 0, page: 1, page_size: 25 })),
    create: (...a: unknown[]) => usersCreate(...a),
    update: vi.fn(),
    remove: vi.fn(),
    resetPassword: vi.fn(),
  },
}));

const hooksCreate = vi.fn();
vi.mock('@/api/webhooks', () => ({
  WebhooksApi: {
    list: vi.fn(async () => []),
    create: (...a: unknown[]) => hooksCreate(...a),
    update: vi.fn(),
    remove: vi.fn(),
    test: vi.fn(),
  },
}));

const tokenCreate = vi.fn();
const tokenList = vi.fn(async (): Promise<unknown[]> => []);
vi.mock('@/api/ai-tokens', () => ({
  AITokensApi: {
    list: () => tokenList(),
    create: (...a: unknown[]) => tokenCreate(...a),
    update: vi.fn(),
    remove: vi.fn(),
  },
}));

vi.mock('@/api/storages', () => ({
  StoragesApi: { list: vi.fn(async () => []) },
}));

// The Webhooks page's global-webhook card reads its config as it mounts.
vi.mock('@/api/notifications', () => ({
  NotificationsApi: {
    list: vi.fn(),
    unreadCount: vi.fn(),
    markRead: vi.fn(),
    markAllRead: vi.fn(),
    getSettings: vi.fn(),
    updateSettings: vi.fn(),
    adminList: vi.fn(),
    sendTest: vi.fn(),
    getWebhookConfig: vi.fn(async () => ({ url: '', token_set: false })),
    updateWebhookConfig: vi.fn(),
  },
}));

// ⚠ The Users page asks the roles endpoints for its custom roles as it
// mounts. Unmocked, that was a REAL request (happy-dom → localhost:3000,
// ECONNREFUSED) whose refusal landed at whatever moment the network chose —
// often in the NEXT test, after the page had been torn out of the document.
// The page then re-drew its table (and the row menu teleported under <body>)
// into DOM that was no longer there: "Cannot read properties of null
// (reading 'insertBefore')", an unhandled rejection that fails the whole
// run although every test passed (v0.49.0 release gate, task #127). The
// answers are ours now, and `rolesAnswer` lets a test hold them back.
let rolesAnswer: Promise<void> = Promise.resolve();
vi.mock('@/api/roles', () => ({
  RolesApi: {
    allOverrides: vi.fn(async () => {
      await rolesAnswer;
      return {};
    }),
    listRules: vi.fn(async () => {
      await rolesAnswer;
      return { rules: [], assignments: {} };
    }),
    setUserRole: vi.fn(),
  },
}));

import Users from '@/views/Users.vue';
import Webhooks from '@/views/Webhooks.vue';
import ApiMcp from '@/views/ApiMcp.vue';
import { useToastStore } from '@/stores/toast';
import { useCapabilitiesStore } from '@/stores/capabilities';
import { teardownDom, unmountAll } from '../helpers/teardown';

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () {
    this.setAttribute('open', '');
  };
  HTMLDialogElement.prototype.close = function () {
    this.removeAttribute('open');
  };
}

// What Vue reports while a page updates — a render that throws lands here
// instead of escaping as an unhandled rejection after the test.
const renderErrors: unknown[] = [];

function mountView(view: unknown, locale: 'en' | 'tr' = 'en'): VueWrapper {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }],
  });
  const w = mount(view as never, {
    global: {
      plugins: [i18n, router],
      config: { errorHandler: (err: unknown) => void renderErrors.push(err) },
    },
    attachTo: document.body,
  });
  return w;
}

function button(w: VueWrapper, text: string) {
  const b = w.findAll('button').filter((x) => x.text().trim() === text);
  expect(b.length, `no button "${text}"`).toBeGreaterThan(0);
  return b[b.length - 1];
}

function toasts() {
  return useToastStore().toasts.map((x) => x.message);
}

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
  rolesAnswer = Promise.resolve();
  renderErrors.length = 0;
});

// ⚠ Finish what the page still has in flight, UNMOUNT it, and only then
// clear the document (helpers/teardown; setup.ts does the same after every
// test of every file). Wiping <body> under a page that is still mounted (what
// the old beforeEach did) leaves a live component drawing into detached DOM
// the moment anything it awaits answers.
afterEach(async () => {
  await teardownDom();
  expect(renderErrors, 'a page threw while it re-drew').toEqual([]);
});

describe('Add user', () => {
  // ⚠ 0.54 (#209, B15/A12): the words are the SERVER's - its refusal on
  // Create, and its check while the address is typed. An empty form is the
  // server's to judge: its `email_required` lands under the box, in the
  // dialog, never as a toast behind the backdrop.
  it('an empty form is said, in the dialog, to need an address', async () => {
    usersCreate.mockRejectedValue({
      response: { status: 400, data: { error: 'email_required', field: 'email', message: 'Enter an email address.' } },
    });
    const w = mountView(Users);
    await flushPromises();
    await button(w, 'Add user').trigger('click');
    await flushPromises();

    await button(w, 'Create').trigger('click');
    await flushPromises();

    const box = w.find('input[name="new-user-email"]');
    expect(box.attributes('aria-invalid')).toBe('true');
    expect(w.find('#new-user-email-err').text()).toBe('Enter an email address.');
    expect(toasts()).toEqual([]);
  });

  it('a malformed address is refused while it is typed, in the server’s words', async () => {
    const said = '“bu-bir-eposta-degil” bir e-posta adresi değil. ad@ornek.com biçiminde yazın.';
    checkAccount.mockImplementation(async (q) =>
      q.email === 'bu-bir-eposta-degil' ? { email: { error: 'email_invalid', message: said } } : {},
    );
    const w = mountView(Users, 'tr');
    await flushPromises();
    await button(w, 'Kullanıcı ekle').trigger('click');
    await flushPromises();

    await w.find('input[name="new-user-email"]').setValue('bu-bir-eposta-degil');
    await new Promise((r) => setTimeout(r, ACCOUNT_CHECK_DELAY_MS + 50));
    await flushPromises();
    expect(checkAccount).toHaveBeenCalledWith(expect.objectContaining({ email: 'bu-bir-eposta-degil', for: 'new' }));
    expect(w.find('#new-user-email-err').text()).toBe(said);
    await button(w, 'Oluştur').trigger('click');
    expect(usersCreate).not.toHaveBeenCalled();
    checkAccount.mockImplementation(async () => ({}));
  });

  // RC re-test, 2026-09-21: Enter in the e-mail box did nothing — the
  // visible Create sits in the dialog footer, outside the <form>, and a form
  // of several fields with no submit button of its own ignores Enter. This
  // DOM runs no implicit submission, so the button that makes it work is
  // pinned, and the submit it produces is shown to reach the handler.
  it('Enter in a box submits the form', async () => {
    usersCreate.mockResolvedValue({ id: 9, email: 'new@example.com' });
    const w = mountView(Users);
    await flushPromises();
    await button(w, 'Add user').trigger('click');
    await flushPromises();
    const form = w.find('input[name="new-user-email"]').element.closest('form')!;
    const submit = form.querySelector('button[type="submit"]');
    expect(submit, 'the form has a submit button of its own').not.toBeNull();

    await w.find('input[name="new-user-email"]').setValue('new@example.com');
    await w.find('input[name="new-user-password"]').setValue('s3cret-pass-1');
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushPromises();
    expect(usersCreate).toHaveBeenCalledTimes(1);
    expect(usersCreate.mock.calls[0][0].email).toBe('new@example.com');
  });

  it('marks the address required and the display name not (the server needs no name)', async () => {
    const w = mountView(Users);
    await flushPromises();
    await button(w, 'Add user').trigger('click');
    await flushPromises();
    // Enter in a box submits the form: the checking must stay ours.
    expect(w.find('input[name="new-user-email"]').element.closest('form')?.hasAttribute('novalidate')).toBe(true);
    expect(w.find('input[name="new-user-email"]').attributes('required')).toBeDefined();
    expect(w.find('input[name="new-user-name"]').attributes('required')).toBeUndefined();
  });

  // Task #127, the sequence that failed the v0.49.0 release run, played on
  // purpose: a page with a row (whose menu is teleported under <body>) is
  // left, the next page opens, and only THEN does the first page's roles
  // answer arrive. Taken down properly (afterEach does the same), the old
  // page draws nothing; left mounted under a wiped <body>, its table re-drew
  // into detached DOM and threw "reading 'insertBefore'".
  it('a roles answer that arrives after the page is gone draws nothing', async () => {
    let answer!: () => void;
    rolesAnswer = new Promise<void>((r) => (answer = r));
    usersCreate.mockResolvedValue({ id: 9, email: 'new@example.com', role: 'viewer' });
    const first = mountView(Users);
    await flushPromises();
    await button(first, 'Add user').trigger('click');
    await flushPromises();
    await first.find('input[name="new-user-email"]').setValue('new@example.com');
    await first.find('input[name="new-user-password"]').setValue('s3cret-pass-1');
    await button(first, 'Create').trigger('click');
    await flushPromises();
    expect(first.text()).toContain('new@example.com');

    unmountAll();
    rolesAnswer = Promise.resolve();
    const next = mountView(Users);
    await flushPromises();

    answer();
    await flushPromises();
    expect(renderErrors).toEqual([]);
    expect(next.text()).not.toContain('new@example.com');
  });

  it("the server's refusal lands under the box, inside the dialog", async () => {
    usersCreate.mockRejectedValue({
      response: { status: 409, data: { error: 'email_taken', field: 'email', message: 'That address belongs to another account.' } },
    });
    const w = mountView(Users);
    await flushPromises();
    await button(w, 'Add user').trigger('click');
    await flushPromises();
    await w.find('input[name="new-user-email"]').setValue('taken@example.com');
    await w.find('input[name="new-user-password"]').setValue('s3cret-pass-1');
    await button(w, 'Create').trigger('click');
    await flushPromises();

    expect(usersCreate).toHaveBeenCalledTimes(1);
    expect(w.find('#new-user-email-err').text()).toBe('That address belongs to another account.');
    expect(toasts()).toEqual([]);
  });

  it('a refusal about nothing in particular is still said inside the dialog', async () => {
    usersCreate.mockRejectedValue({ response: { status: 500, data: { error: 'the database is on fire' } } });
    const w = mountView(Users);
    await flushPromises();
    await button(w, 'Add user').trigger('click');
    await flushPromises();
    await w.find('input[name="new-user-email"]').setValue('new@example.com');
    await button(w, 'Create').trigger('click');
    await flushPromises();

    expect(w.find('[data-testid="user-create-error"]').exists()).toBe(true);
    expect(toasts()).toEqual([]);
  });
});

describe('New webhook', () => {
  async function open(locale: 'en' | 'tr' = 'en') {
    const w = mountView(Webhooks, locale);
    await flushPromises();
    await button(w, locale === 'en' ? en.webhooks.add : tr.webhooks.add).trigger('click');
    await flushPromises();
    return w;
  }

  it('marks Name and URL required, and keeps the checking its own', async () => {
    const w = await open();
    expect(w.find('input[name="webhook-name"]').attributes('required')).toBeDefined();
    expect(w.find('input[name="webhook-url"]').attributes('required')).toBeDefined();
    // ⚠ This DOM runs no native validation, so the browser's half is pinned
    // by the attribute: without `novalidate` a real browser answers an empty
    // Save with its own bubble, in its own language, and save() never runs
    // (RC re-test, 2026-09-21).
    expect(w.find('form').attributes('novalidate')).toBeDefined();
  });

  // The screen keeps every event tickable — an operator may subscribe ahead of
  // switching a service on — and says beside the box what the event needs, in
  // the SERVER's words (`capabilities.event_off`, #211 audit B16): the rule is
  // backend capabilities_rules.go eventsOff, tested in Go; the screen keeps no
  // copy of it and no sentence of its own.
  describe('beside an event that cannot happen here', () => {
    const REQUEST_EVENTS = ['e2e.request_created', 'e2e.request_decided'];
    const SENTENCE = 'Only when the encryption policy asks for approval.';
    const note = (w: VueWrapper, ev: string) => w.find(`[data-testid="webhook-event-${ev}"]`).text();

    function serverSays(eventOff: Record<string, { reason: string; fixable: boolean; text: string }>) {
      const caps = useCapabilitiesStore();
      caps.data = { ...caps.data, event_off: eventOff };
    }

    it('prints the server’s sentence, and leaves the box tickable', async () => {
      serverSays(Object.fromEntries(REQUEST_EVENTS.map((ev) => [ev, { reason: 'e2e_approval', fixable: true, text: SENTENCE }])));
      const w = await open();
      for (const ev of REQUEST_EVENTS) {
        const row = w.find(`[data-testid="webhook-event-${ev}"]`);
        expect(row.exists(), `${ev} is listed`).toBe(true);
        expect(row.text(), ev).toContain(SENTENCE);
        // …and still tickable: the operator may subscribe ahead.
        expect(row.find('input').attributes('disabled'), ev).toBeUndefined();
      }
    });

    it('says nothing when the server lists nothing', async () => {
      serverSays({});
      const w = await open();
      for (const ev of REQUEST_EVENTS) expect(note(w, ev), ev).not.toContain(SENTENCE);
    });
  });

  // 0.54 (audit B10): the rules and their words are the SERVER's. The page
  // sends what was typed; a refusal names its box (`field`) and says why
  // (`message`, in the reader's language), and the sentence lands under that
  // box - never a toast behind the dialog, never a word of the page's own.
  const refusal = (field: string, message: string) => ({
    isAxiosError: true,
    response: { status: 400, data: { error: `${field} refused`, field, message } },
  });

  it("an empty name is refused by the server, and its sentence is shown under the box", async () => {
    hooksCreate.mockRejectedValueOnce(refusal('name', 'Webhook’a bir ad verin.'));
    const w = await open('tr');
    await w.find('form').trigger('submit');
    await flushPromises();

    expect(hooksCreate).toHaveBeenCalledTimes(1);
    expect(w.find('#webhook-name-err').text()).toBe('Webhook’a bir ad verin.');
    expect(w.find('#webhook-url-err').exists()).toBe(false);
    expect(w.find('[data-testid="webhook-form-error"]').exists()).toBe(false);
    expect(toasts()).toEqual([]);
  });

  it('a URL the server refuses gets its sentence under the URL box', async () => {
    hooksCreate.mockRejectedValueOnce(refusal('url', 'The address must start with http:// or https://.'));
    const w = await open();
    await w.find('input[name="webhook-name"]').setValue('ops');
    await w.find('input[name="webhook-url"]').setValue('ftp://example.com/hook');
    await w.find('form').trigger('submit');
    await flushPromises();
    expect(hooksCreate).toHaveBeenCalledTimes(1);
    expect(w.find('#webhook-url-err').text()).toBe('The address must start with http:// or https://.');
  });

  it('the page has no scheme rule of its own: HTTPS:// in capitals is sent as typed', async () => {
    hooksCreate.mockResolvedValueOnce({ id: 1 });
    const w = await open();
    await w.find('input[name="webhook-name"]').setValue('ops');
    await w.find('input[name="webhook-url"]').setValue('HTTPS://example.com/hook');
    expect(w.find('#webhook-url-err').exists()).toBe(false);
    await w.find('form').trigger('submit');
    await flushPromises();
    expect(hooksCreate).toHaveBeenCalledTimes(1);
    expect(hooksCreate.mock.calls[0][0]).toMatchObject({ url: 'HTTPS://example.com/hook' });
  });

  it("the server's refusal is shown inside the dialog", async () => {
    hooksCreate.mockRejectedValue({ response: { status: 400, data: { error: 'nope', message: 'The server said no.' } } });
    const w = await open();
    await w.find('input[name="webhook-name"]').setValue('ops');
    await w.find('input[name="webhook-url"]').setValue('https://example.com/hook');
    await w.find('form').trigger('submit');
    await flushPromises();

    expect(hooksCreate).toHaveBeenCalledTimes(1);
    expect(w.find('[data-testid="webhook-form-error"]').exists()).toBe(true);
    expect(toasts()).toEqual([]);
  });
});

describe('New API / MCP token', () => {
  async function open(locale: 'en' | 'tr' = 'en') {
    const w = mountView(ApiMcp, locale);
    await flushPromises();
    await button(w, locale === 'en' ? en.apiMcp.newToken : tr.apiMcp.newToken).trigger('click');
    await flushPromises();
    return w;
  }

  it('offers no Create while nothing is ticked, and says why', async () => {
    const w = await open();
    expect(w.find('form').attributes('novalidate')).toBeDefined();
    expect(w.find('form button[type="submit"]').exists(), 'Enter in a box submits').toBe(true);
    await w.find('input[type="text"], input:not([type])').setValue('ci');
    for (const s of ['read', 'write', 'delete', 'mcp', 'admin']) {
      await w.find(`[data-testid="ai-token-scope-${s}"]`).setValue(false);
    }
    const create = w.find('[data-testid="ai-token-create"]');
    expect(create.attributes('disabled')).toBeDefined();
    // ⚠ ONE sentence, in red while nothing is ticked and in grey after —
    // not a second key saying the same thing (v0.43.0 translation sweep).
    const said = w.find('[data-testid="ai-token-scopes-required"]');
    expect(said.text()).toBe(en.apiMcp.fields.scopesHint);
    expect(said.classes()).toContain('error-text');

    // Even a submit that gets past the button (Enter in the label box).
    await w.find('form').trigger('submit');
    await flushPromises();
    expect(tokenCreate).not.toHaveBeenCalled();
  });

  it('never says an empty list grants everything, and says admin is never implied', () => {
    for (const bundle of [en, tr]) {
      const hint = bundle.apiMcp.fields.scopesHint;
      expect(hint).not.toMatch(/all scopes are granted|tüm yetkiler verilir/i);
      expect(hint).toContain('admin');
    }
    expect(tr.apiMcp.fields.scopesHint).toContain('En az bir izin seçin');
  });

  it('admin is not ticked by default, and what is ticked is what is sent', async () => {
    tokenCreate.mockResolvedValue({ token: 'fx_secret', id: 1 });
    const w = await open();
    expect((w.find('[data-testid="ai-token-scope-admin"]').element as HTMLInputElement).checked).toBe(false);
    await w.find('input[type="text"], input:not([type])').setValue('ci');
    await w.find('[data-testid="ai-token-create"]').trigger('click');
    await flushPromises();
    expect(tokenCreate).toHaveBeenCalledTimes(1);
    const scopes = String(tokenCreate.mock.calls[0][0].scopes).split(',');
    expect(scopes).toEqual(['read', 'write', 'mcp']);
  });

  // Task #157: comments are a permission of their own. Every key reads them
  // unless it says otherwise, so `read` is chosen and sends nothing; Read and
  // write sends `comments:rw`, and "Write" alone does not.
  it('comments are read unless chosen, and Read and write sends comments:rw', async () => {
    tokenCreate.mockResolvedValue({ token: 'fx_secret', id: 1 });
    const w = await open();
    expect(w.find('[data-testid="ai-token-comments-read"]').attributes('aria-checked')).toBe('true');
    expect(w.find('[data-testid="ai-token-comments-rw"]').attributes('aria-checked')).toBe('false');
    // A choice of two is buttons that show both answers, never a native select.
    expect(w.find('[aria-labelledby="ai-token-comments-label"]').attributes('role')).toBe('radiogroup');
    await w.find('input[type="text"], input:not([type])').setValue('ci');
    await w.find('[data-testid="ai-token-create"]').trigger('click');
    await flushPromises();
    expect(String(tokenCreate.mock.calls[0][0].scopes).split(',')).toEqual(['read', 'write', 'mcp']);

    tokenCreate.mockClear();
    const again = await open();
    await again.find('[data-testid="ai-token-comments-rw"]').trigger('click');
    expect(again.find('[data-testid="ai-token-comments-rw"]').attributes('aria-checked')).toBe('true');
    await again.find('input[type="text"], input:not([type])').setValue('yorumcu');
    await again.find('[data-testid="ai-token-create"]').trigger('click');
    await flushPromises();
    expect(String(tokenCreate.mock.calls[0][0].scopes).split(',')).toEqual(['read', 'write', 'mcp', 'comments:rw']);
  });

  it("the table says each key's comments level, in the panel's language", async () => {
    tokenList.mockResolvedValueOnce([
      { id: 1, user_id: 1, label: 'eski', scopes: 'read,write,delete,mcp', usernames: '', created_at: '2026-10-06T00:00:00Z', permissions: { comments: 'read' } },
      { id: 2, user_id: 1, label: 'yorumcu', scopes: 'read,mcp,comments:rw', usernames: '', created_at: '2026-10-06T00:00:00Z', permissions: { comments: 'rw' } },
    ]);
    const w = mountView(ApiMcp, 'tr');
    await flushPromises();
    const cells = w.findAll('[data-testid="ai-token-comments-cell"]').map((c) => c.text());
    expect(cells.sort()).toEqual([tr.apiMcp.commentsLevel.read, tr.apiMcp.commentsLevel.rw].sort());
    // The level is not a verb: no chip prints `comments:rw`.
    const chips = w.findAll('[data-testid="ai-token-scope-chip"]').map((c) => c.attributes('title'));
    expect(chips).not.toContain('comments:rw');
  });

  it("the server's refusal is said inside the dialog", async () => {
    tokenCreate.mockRejectedValue({
      response: { status: 400, data: { error: 'scopes_required', message: 'Choose at least one scope.' } },
    });
    const w = await open();
    await w.find('input[type="text"], input:not([type])').setValue('ci');
    await w.find('[data-testid="ai-token-create"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="ai-token-create-error"]').exists()).toBe(true);
    expect(toasts()).toEqual([]);
  });
});
