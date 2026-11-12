// Setting an app up (filex 0.60, docs/APP-PLUGINS.md → Setting an app up):
// the wizard's steps, in the contract's order and only the ones the app has;
// an address the server refuses is said under the field it is about; the
// "Who may use it" step names who else holds a role it is about to change;
// Finish starts the app.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

let setupAnswer: Record<string, unknown> = {};
let addRefusal: Record<string, unknown> | null = null;
const calls: Array<[string, string, unknown]> = [];

const refuse = (status: number, data: unknown) =>
  Object.assign(new Error('refused'), { isAxiosError: true, response: { status, data } });

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      calls.push(['GET', url, undefined]);
      if (url === '/admin/app-plugins/7/setup') return { data: setupAnswer };
      if (url === '/admin/app-plugins/7/setup/view') {
        return { data: { surface: { nodes: [{ type: 'text', props: { text: { en: 'Pick an alphabet' } } }], actions: [{ id: 'save', label: { en: 'Save' }, primary: true }] }, app_done: false } };
      }
      if (url === '/admin/roles/builtin') return { data: { permissions: ['files.read'], preset: '', apps: {} } };
      if (url === '/admin/roles') {
        return {
          data: {
            rules: [{ id: 5, name: 'Sales', description: '', enabled: true, permissions: [], targets: [], effects: {}, settings: {}, conditions: {} }],
            assignments: { 11: 5, 12: 5 },
            group_assignments: { 13: { role_id: 5, group_id: 2, group_name: 'Field team' } },
            builtin_members: { user: 14, viewer: 2 },
          },
        };
      }
      if (url === '/admin/groups') {
        return {
          data: {
            groups: [
              { id: 2, name: 'Field team', description: '', role_id: 5, priority: 0, links: [], member_count: 4 },
              { id: 3, name: 'Partners', description: '', role_id: 5, priority: 0, links: [], member_count: 9 },
            ],
          },
        };
      }
      if (url === '/admin/roles/exceptions') return { data: { overrides: {} } };
      if (url === '/admin/users') {
        return { data: [{ id: 11, email: 'alice@example.com', display_name: 'Alice', role: 'user' }, { id: 12, email: 'bob@example.com', display_name: 'Bob', role: 'user' }, { id: 13, email: 'carol@example.com', display_name: 'Carol', role: 'user' }] };
      }
      return { data: {} };
    }),
    post: vi.fn(async (url: string, body: unknown) => {
      calls.push(['POST', url, body]);
      if (url === '/admin/app-plugins/7/setup/routes/go/bases') {
        if (addRefusal) throw refuse(400, addRefusal);
        return { data: { base: { id: 31, host: '', prefix: 'l', tenant_id: 0, url: 'https://fm.example.com/l', enabled: true, default: true } } };
      }
      if (url === '/admin/app-plugins/7/setup/complete') return { data: { plugin: { ...plugin, state: 'running' } } };
      return { data: {} };
    }),
    patch: vi.fn(async () => ({ data: {} })),
    put: vi.fn(async (url: string, body: unknown) => {
      calls.push(['PUT', url, body]);
      return { data: body };
    }),
    delete: vi.fn(async () => ({ data: {} })),
  },
}));

import AppSetupWizard from '@/components/plugins/AppSetupWizard.vue';

enableAutoUnmount(afterEach);

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); };
}

const plugin = {
  id: 7, name: 'shortlink', version: '0.1.0', label: { en: 'Short links' }, enabled: true, state: 'setup',
  source: 'github', signed: true, permissions: ['data', 'public_routes'], scheduled: false, actions: 0, views: 2,
  public_pages: 0, created_at: '', updated_at: '',
};

const route = { id: 'go', label: { en: 'Short links', tr: 'Kısa bağlantılar' }, default_prefix: 'l', bases: [] as unknown[] };
const perm = { id: 'use', key: 'app.shortlink.use', label: { en: 'Get short links' }, default: 'user' };

function fullSetup(over: Record<string, unknown> = {}) {
  return {
    state: 'setup', needed: true, done: false, app_step: { view: 'setup', done: false },
    routes: [{ ...route, bases: [] }], permissions: [perm], own_hosts: ['fm.example.com'], multi_tenant: false,
    tenants: [], data: { driver: 'sqlite', node_local: false }, warning: null, ...over,
  };
}

function mountWizard(locale = 'en') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  return mount(AppSetupWizard, { props: { modelValue: true, plugin }, global: { plugins: [i18n] }, attachTo: document.body });
}

function stepNames(w: ReturnType<typeof mountWizard>): string[] {
  return w.findAll('[data-testid^="app-setup-step-"]').map((x) => x.attributes('data-testid')!.replace('app-setup-step-', ''));
}

describe('AppSetupWizard', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    calls.length = 0;
    addRefusal = null;
    setupAnswer = fullSetup();
  });

  it('the steps are the contract’s, in its order, and only the ones the app has', async () => {
    let w = mountWizard();
    await flushPromises();
    expect(stepNames(w)).toEqual(['app', 'addresses', 'people', 'finish']);
    w.unmount();

    setupAnswer = fullSetup({ app_step: null, permissions: [] });
    w = mountWizard();
    await flushPromises();
    expect(stepNames(w)).toEqual(['addresses', 'finish']);
  });

  it('the app’s own screen is drawn, and Next waits for the app’s part', async () => {
    const w = mountWizard();
    await flushPromises();
    expect(w.get('[data-testid="app-setup-surface"]').text()).toContain('Pick an alphabet');
    expect(w.find('[data-testid="app-setup-next"]').exists(), 'the screen’s own Save is the one primary button').toBe(false);
  });

  it('an address the server refuses is said under its field; Next opens once a route has one', async () => {
    setupAnswer = fullSetup({ app_step: null, permissions: [] });
    addRefusal = { error: 'short_link_prefix_reserved', field: 'prefix', message: 'filex uses /s itself.' };
    const w = mountWizard();
    await flushPromises();
    expect(w.get('[data-testid="app-setup-next"]').attributes('disabled'), 'no address yet').toBeDefined();
    await w.get('input[name="app-setup-prefix-go"]').setValue('s');
    await w.get('[data-testid="app-setup-add-button-go"]').trigger('click');
    await flushPromises();
    const field = w.get('input[name="app-setup-prefix-go"]').element.closest('div')!;
    expect(field.textContent).toContain('filex uses /s itself.');
    expect(w.get('[data-testid="app-setup-next"]').attributes('disabled')).toBeDefined();

    addRefusal = null;
    await w.get('input[name="app-setup-prefix-go"]').setValue('l');
    await w.get('[data-testid="app-setup-add-button-go"]').trigger('click');
    await flushPromises();
    const adds = calls.filter((c) => c[0] === 'POST' && c[1].endsWith('/bases'));
    expect(adds.map((c) => c[2])).toEqual([
      { host: '', prefix: 's', tenant_id: 0, default: true },
      { host: '', prefix: 'l', tenant_id: 0, default: true },
    ]);
    expect(w.get('[data-testid="app-setup-base-31"]').text()).toBe('https://fm.example.com/l');
    expect(w.get('[data-testid="app-setup-next"]').attributes('disabled')).toBeUndefined();
  });

  it('each kind of address keeps its own prefix: a host of its own starts with none', async () => {
    // ⚠ 0.60.0: the own-address prefix ("l", the route's default) was carried
    // into the optional prefix of a host of its own, and l.example.com became
    // https://l.example.com/l. The default is the placeholder there, never a value.
    setupAnswer = fullSetup({ app_step: null, permissions: [] });
    const w = mountWizard('tr');
    await flushPromises();
    const prefix = () => w.get('input[name="app-setup-prefix-go"]').element as HTMLInputElement;
    expect(prefix().value, 'filex’s own address starts with the route’s default').toBe('l');

    await w.get('[data-testid="app-setup-kind-go-host"]').trigger('click');
    await flushPromises();
    expect(prefix().value, 'a host of its own starts with no prefix').toBe('');
    expect(prefix().placeholder, 'the default is only shown').toBe('l');
    await w.get('input[name="app-setup-host-go"]').setValue('l.example.com');
    expect(w.get('[data-testid="app-setup-preview-go"]').text()).toContain('https://l.example.com/kod');
    expect(w.get('[data-testid="app-setup-preview-go"]').text()).not.toContain('https://l.example.com/l/');

    // What a person types in one kind stays with that kind.
    await w.get('input[name="app-setup-prefix-go"]').setValue('x');
    await w.get('[data-testid="app-setup-kind-go-own"]').trigger('click');
    await flushPromises();
    expect(prefix().value).toBe('l');
    await w.get('input[name="app-setup-prefix-go"]').setValue('go');
    await w.get('[data-testid="app-setup-kind-go-host"]').trigger('click');
    await flushPromises();
    expect(prefix().value).toBe('x');
    await w.get('input[name="app-setup-prefix-go"]').setValue('');
    await w.get('[data-testid="app-setup-add-button-go"]').trigger('click');
    await flushPromises();
    await w.get('[data-testid="app-setup-kind-go-own"]').trigger('click');
    await flushPromises();
    expect(prefix().value).toBe('go');

    const adds = calls.filter((c) => c[0] === 'POST' && c[1].endsWith('/bases'));
    expect(adds.map((c) => c[2])).toEqual([{ host: 'l.example.com', prefix: '', tenant_id: 0, default: true }]);
  });

  it('"Who may use it" names who else holds a role, for the role and for a group', async () => {
    setupAnswer = fullSetup({ app_step: null, routes: [] });
    const w = mountWizard();
    await flushPromises();
    expect(stepNames(w)).toEqual(['people', 'finish']);
    const holders = w.get('[data-testid="app-setup-holders-rule:5"]').text();
    expect(holders).toContain('3 people');
    expect(holders).toContain('Alice');
    expect(holders).toContain('Field team (4)');
    expect(holders).toContain('Partners (9)');
    // The group's decision is the role's: it says who ELSE it reaches.
    const shared = w.get('[data-testid="app-setup-group-shared-2"]').text();
    expect(shared).toContain('Sales');
    expect(shared).toContain('Partners (9)');
    expect(shared).not.toContain('Field team');
    expect(w.get('[data-testid="app-setup-roles-note-use"]').text()).toBe(en.appPlugins.setup.people.rolesNote);
  });

  it('a decision on the User role is written with the role’s permissions, at once', async () => {
    setupAnswer = fullSetup({ app_step: null, routes: [] });
    const w = mountWizard();
    await flushPromises();
    await w.get('[data-testid="app-setup-role-builtin:user-use-deny"]').trigger('click');
    await flushPromises();
    const put = calls.find((c) => c[0] === 'PUT' && c[1] === '/admin/roles/builtin');
    expect(put?.[2]).toEqual({ permissions: ['files.read'], apps: { 'app.shortlink.use': 'deny' } });
  });

  it('Finish starts the app and hands it back', async () => {
    setupAnswer = fullSetup({ app_step: null, permissions: [], routes: [{ ...route, bases: [{ id: 31, host: '', prefix: 'l', tenant_id: 0, url: 'https://fm.example.com/l', enabled: true, default: true }] }], data: { driver: 'sqlite', node_local: true } });
    const w = mountWizard();
    await flushPromises();
    await w.get('[data-testid="app-setup-next"]').trigger('click');
    expect(w.find('[data-testid="app-setup-node-local"]').exists(), 'node-local data is said before it starts').toBe(true);
    await w.get('[data-testid="app-setup-complete"]').trigger('click');
    await flushPromises();
    expect(calls.some((c) => c[0] === 'POST' && c[1] === '/admin/app-plugins/7/setup/complete')).toBe(true);
    expect(w.emitted('done')?.[0]?.[0]).toMatchObject({ state: 'running' });
  });
});
