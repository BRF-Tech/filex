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
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const usersCreate = vi.fn();
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
vi.mock('@/api/ai-tokens', () => ({
  AITokensApi: {
    list: vi.fn(async () => []),
    create: (...a: unknown[]) => tokenCreate(...a),
    update: vi.fn(),
    remove: vi.fn(),
  },
}));

vi.mock('@/api/storages', () => ({
  StoragesApi: { list: vi.fn(async () => []) },
}));

import Users from '@/views/Users.vue';
import Webhooks from '@/views/Webhooks.vue';
import ApiMcp from '@/views/ApiMcp.vue';
import { useToastStore } from '@/stores/toast';

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () {
    this.setAttribute('open', '');
  };
  HTMLDialogElement.prototype.close = function () {
    this.removeAttribute('open');
  };
}

function mountView(view: unknown, locale: 'en' | 'tr' = 'en'): VueWrapper {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }],
  });
  return mount(view as never, { global: { plugins: [i18n, router] }, attachTo: document.body });
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
  document.body.innerHTML = '';
});

describe('Add user', () => {
  it('an empty form sends nothing and says, in the dialog, that an address is needed', async () => {
    const w = mountView(Users);
    await flushPromises();
    await button(w, 'Add user').trigger('click');
    await flushPromises();

    await button(w, 'Create').trigger('click');
    await flushPromises();

    expect(usersCreate).not.toHaveBeenCalled();
    const box = w.find('input[name="new-user-email"]');
    expect(box.attributes('aria-invalid')).toBe('true');
    expect(w.find('#new-user-email-err').text()).toBe('Enter an email address.');
    expect(toasts()).toEqual([]);
  });

  it('a malformed address is refused while it is typed', async () => {
    const w = mountView(Users, 'tr');
    await flushPromises();
    await button(w, 'Kullanıcı ekle').trigger('click');
    await flushPromises();

    await w.find('input[name="new-user-email"]').setValue('bu-bir-eposta-degil');
    expect(w.find('#new-user-email-err').text()).toBe('Bu bir e-posta adresi değil. ad@ornek.com biçiminde yazın.');
    await button(w, 'Oluştur').trigger('click');
    expect(usersCreate).not.toHaveBeenCalled();
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

  it("the server's refusal lands under the box, inside the dialog", async () => {
    usersCreate.mockRejectedValue({
      response: { status: 409, data: { error: 'email_taken', field: 'email', message: 'That address belongs to another account.' } },
    });
    const w = mountView(Users);
    await flushPromises();
    await button(w, 'Add user').trigger('click');
    await flushPromises();
    await w.find('input[name="new-user-email"]').setValue('taken@example.com');
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

  it('an empty save sends nothing and names both boxes, in the dialog', async () => {
    const w = await open('tr');
    await w.find('form').trigger('submit');
    await flushPromises();

    expect(hooksCreate).not.toHaveBeenCalled();
    expect(w.find('#webhook-name-err').text()).toBe(tr.webhooks.errName);
    expect(w.find('#webhook-url-err').text()).toBe(tr.webhooks.errUrl);
    expect(toasts()).toEqual([]);
  });

  it('a URL that is not http(s) is refused in words before it is sent', async () => {
    const w = await open();
    await w.find('input[name="webhook-name"]').setValue('ops');
    await w.find('input[name="webhook-url"]').setValue('ftp://example.com/hook');
    expect(w.find('#webhook-url-err').text()).toBe(en.webhooks.errUrlScheme);
    await w.find('form').trigger('submit');
    await flushPromises();
    expect(hooksCreate).not.toHaveBeenCalled();
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
