// App permissions on the role and exception editors (backend perm/app.go):
// an installed app declares what the administrator can give or take away per
// role and per person ("Request signatures" for the signing app), and every
// editor draws those rows in the SAME PermissionGrid as the catalogue's, each
// Default / Allow / Deny.
//
// What has to stay true (0.49.0):
//   · Allow / Deny put the key in the saved body — a built-in role's `apps`,
//     a custom role's `settings.apps`, a person's exceptions — and Default
//     takes it OUT (Default is "no decision", not a third value);
//   · "Default" says what it comes to for that role or person;
//   · a delegated administrator sees a person's app rows read-only — the
//     server refuses app keys from anyone but an administrator (403);
//   · a preset and "Clear exceptions" are the catalogue's: they leave a
//     person's app exceptions as they are, for an administrator too (writing
//     the 28 alone used to wipe them — 0.49 docs review); app exceptions are
//     cleared in the Apps group's own "Reset to defaults";
//   · no app declares any permission: no Apps group at all.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import { useAuthStore } from '@/stores/auth';

const KEY = 'app.sign.request';
/** The words are the catalogue's: a wording change is not a regression here. */
const EN = en.permissions;
const TR = tr.permissions;

const { withApps, withoutApps, roles } = vi.hoisted(() => {
  const permissions = [
    { key: 'files.download', group: 'files' },
    { key: 'files.delete', group: 'files', viewer_capped: true },
    { key: 'admin.full', group: 'admin', role_only: true },
  ];
  const presets = [
    { name: 'full_admin', permissions: ['files.download', 'files.delete', 'admin.full'] },
    { name: 'standard', permissions: ['files.download', 'files.delete'] },
    { name: 'read_only', permissions: ['files.download'] },
  ];
  const withApps = {
    permissions,
    presets,
    apps: [
      {
        key: 'app.sign.request',
        app: 'sign',
        app_label: { en: 'Sign', tr: 'İmza' },
        id: 'request',
        label: { en: 'Request signatures', tr: 'İmza isteme' },
        description: { en: 'Ask other people to sign a document.' },
        default: 'user',
      },
    ],
  };
  const withoutApps = { permissions, presets, apps: [] };
  const roles = {
    catalogue: vi.fn(async () => withApps),
    getDefaults: vi.fn(),
    putDefaults: vi.fn(async (permissions: string[], _role?: string, apps?: Record<string, string>) => ({ permissions, preset: '', apps: apps ?? {} })),
    createRule: vi.fn(async (b: object) => ({ ...b, id: 8 })),
    updateRule: vi.fn(async (id: number, b: object) => ({ ...b, id })),
    forUser: vi.fn(),
    setOverrides: vi.fn(),
  };
  return { withApps, withoutApps, roles };
});

/** What the built-in roles answer unless a test says otherwise: no decisions. */
async function noDecisions(_role?: string) {
  return { permissions: ['files.download', 'files.delete'], preset: 'standard', apps: {} as Record<string, string> };
}

vi.mock('@/api/roles', () => ({ RolesApi: roles }));

import BuiltinRoleEditor from '@/components/BuiltinRoleEditor.vue';
import RoleEditor from '@/components/RoleEditor.vue';
import UserRolesCard from '@/components/UserRolesCard.vue';

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () {
    this.setAttribute('open', '');
  };
  HTMLDialogElement.prototype.close = function () {
    this.removeAttribute('open');
  };
}

async function mountIt(view: unknown, props: object, locale = 'en'): Promise<VueWrapper> {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const w = mount(view as never, { props, global: { plugins: [i18n] }, attachTo: document.body });
  await flushPromises();
  return w;
}

function q<T extends Element = HTMLElement>(sel: string): T {
  const el = document.body.querySelector<T>(sel);
  expect(el, `nothing matches ${sel}`).not.toBeNull();
  return el as T;
}
async function click(el: Element) {
  (el as HTMLElement).click();
  await flushPromises();
}
/** Opens the Apps group (collapsed like every group but Files). */
async function openApps() {
  await click(q('[data-testid="perm-group-toggle-apps"]'));
}
function choice(opt: 'inherit' | 'allow' | 'deny'): HTMLButtonElement {
  return q<HTMLButtonElement>(`[data-testid="perm-${KEY}-${opt}"]`);
}

/** A person's answer from the server, with the app row's own answer. */
function personAnswer(overrides: Record<string, string>, app: { allowed: boolean; kind: string; inherited: boolean; inheritedKind: string }) {
  return {
    user_id: 2,
    role: 'user',
    overrides,
    effective: {
      permissions: [
        { key: 'files.download', allowed: true, source: { kind: 'base' } },
        { key: 'files.delete', allowed: !overrides['files.delete'], source: { kind: overrides['files.delete'] ? 'override' : 'base' } },
        { key: 'admin.full', allowed: false, source: { kind: 'role' } },
      ],
      allowed: ['files.download'],
      preset: '',
      settings: {},
      rules: [],
      conditional_rules: [],
      apps: [
        {
          key: KEY,
          allowed: app.allowed,
          source: { kind: app.kind },
          inherited: { allowed: app.inherited, source: { kind: app.inheritedKind } },
        },
      ],
    },
  };
}

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
  roles.getDefaults.mockReset();
  roles.getDefaults.mockImplementation(noDecisions);
  document.body.innerHTML = '';
});

describe('built-in role editor', () => {
  it("lists the app's permission under the app, Default saying what the app's default gives the User role", async () => {
    await mountIt(BuiltinRoleEditor, { modelValue: true, role: 'user', catalogue: withApps });
    await openApps();
    expect(q('[data-testid="perm-app-sign"]').textContent?.trim()).toBe('Sign');
    expect(q(`[data-testid="perm-row-${KEY}"]`).textContent).toContain('Request signatures');
    expect(choice('inherit').textContent?.trim()).toBe(EN.apps.defaultAllowed);
    expect(choice('inherit').dataset.default).toBe('allowed');
    expect(choice('inherit').getAttribute('aria-checked')).toBe('true');
    expect(q(`[data-testid="perm-app-default-${KEY}"]`).textContent?.trim()).toBe(EN.apps.holders.user);
  });

  it("for the Viewer role the same app default is 'not allowed'", async () => {
    await mountIt(BuiltinRoleEditor, { modelValue: true, role: 'viewer', catalogue: withApps });
    await openApps();
    expect(choice('inherit').textContent?.trim()).toBe(EN.apps.defaultDenied);
    expect(choice('inherit').dataset.default).toBe('denied');
  });

  it('Deny is saved as the role\'s decision; Default takes the key out of the body', async () => {
    await mountIt(BuiltinRoleEditor, { modelValue: true, role: 'user', catalogue: withApps });
    await openApps();
    await click(choice('deny'));
    await click(q('[data-testid="builtin-role-save"]'));
    expect(roles.putDefaults).toHaveBeenLastCalledWith(['files.download', 'files.delete'], 'user', { [KEY]: 'deny' });

    // Saved decisions come back on the next open, and Default removes one.
    roles.getDefaults.mockResolvedValueOnce({ permissions: ['files.download'], preset: '', apps: { [KEY]: 'deny' } });
    document.body.innerHTML = '';
    await mountIt(BuiltinRoleEditor, { modelValue: true, role: 'user', catalogue: withApps });
    await openApps();
    expect(choice('deny').getAttribute('aria-checked')).toBe('true');
    await click(choice('inherit'));
    await click(q('[data-testid="builtin-role-save"]'));
    expect(roles.putDefaults).toHaveBeenLastCalledWith(['files.download'], 'user', {});
  });

  it('reads the app in the reader\'s language', async () => {
    await mountIt(BuiltinRoleEditor, { modelValue: true, role: 'user', catalogue: withApps }, 'tr');
    await openApps();
    expect(q('[data-testid="perm-group-apps"]').textContent).toContain(TR.groups.apps);
    expect(q('[data-testid="perm-app-sign"]').textContent?.trim()).toBe('İmza');
    expect(q(`[data-testid="perm-row-${KEY}"]`).textContent).toContain('İmza isteme');
    // No Turkish description: the English one, not an empty line.
    expect(q(`[data-testid="perm-row-${KEY}"]`).textContent).toContain('Ask other people to sign a document.');
    expect(choice('inherit').textContent?.trim()).toBe(TR.apps.defaultAllowed);
    expect(choice('deny').textContent?.trim()).toBe(TR.effect.deny);
  });

  it('no app declares a permission: no Apps group', async () => {
    await mountIt(BuiltinRoleEditor, { modelValue: true, role: 'user', catalogue: withoutApps });
    expect(document.body.querySelector('[data-testid="perm-group-apps"]')).toBeNull();
    expect(document.body.querySelector('[data-testid="perm-group-files"]')).not.toBeNull();
  });
});

describe('custom role editor', () => {
  const rule = {
    id: 7,
    name: 'Signing desk',
    description: '',
    enabled: true,
    permissions: ['files.download', 'files.delete'],
    targets: [],
    effects: {},
    settings: { apps: { [KEY]: 'allow' } },
    conditions: {},
  };

  it('keeps the decision in settings.apps; Default takes it out', async () => {
    await mountIt(RoleEditor, { modelValue: true, rule, catalogue: withApps, storages: [] });
    await openApps();
    expect(choice('allow').getAttribute('aria-checked')).toBe('true');
    await click(choice('deny'));
    await click(q('[data-testid="rule-save"]'));
    expect(roles.updateRule).toHaveBeenLastCalledWith(7, expect.objectContaining({ settings: expect.objectContaining({ apps: { [KEY]: 'deny' } }) }));

    await click(choice('inherit'));
    await click(q('[data-testid="rule-save"]'));
    const body = roles.updateRule.mock.calls.at(-1)![1] as { settings: { apps?: Record<string, string> } };
    expect(body.settings.apps).toEqual({});
  });

  it("Default is the answer of the built-in role the role's people are on", async () => {
    // The User role has taken it away from everybody on User.
    roles.getDefaults.mockImplementation(async (role?: string) => ({
      permissions: [],
      preset: '',
      apps: role === 'user' ? { [KEY]: 'deny' } : {},
    }));
    const noApps = { ...rule, settings: {} };
    await mountIt(RoleEditor, { modelValue: true, rule: noApps, catalogue: withApps, storages: [] });
    await openApps();
    expect(choice('inherit').textContent?.trim()).toBe(EN.apps.defaultDenied);
    expect(choice('inherit').dataset.default).toBe('denied');
  });

  it("a read-only role's people are on Viewer, whose app default does not include a user permission", async () => {
    const readOnly = { ...rule, permissions: ['files.download'], settings: {} };
    await mountIt(RoleEditor, { modelValue: true, rule: readOnly, catalogue: withApps, storages: [] });
    await openApps();
    expect(choice('inherit').textContent?.trim()).toBe(EN.apps.defaultDenied);
    expect(choice('inherit').dataset.default).toBe('denied');
  });

  it('no app declares a permission: no Apps group, and the built-in roles are not even read', async () => {
    await mountIt(RoleEditor, { modelValue: true, rule, catalogue: withoutApps, storages: [] });
    expect(document.body.querySelector('[data-testid="perm-group-apps"]')).toBeNull();
    expect(roles.getDefaults).not.toHaveBeenCalled();
  });
});

describe("a person's exceptions", () => {
  function asAdministrator(full: boolean) {
    const auth = useAuthStore();
    auth.user = { id: 1, email: 'boss@local', username: 'boss', role: full ? 'admin' : 'user' } as never;
  }

  it('the answer and where it comes from; Default is the answer without their exception', async () => {
    asAdministrator(true);
    roles.catalogue.mockResolvedValue(withApps);
    roles.forUser.mockResolvedValue(personAnswer({ [KEY]: 'deny' }, { allowed: false, kind: 'override', inherited: true, inheritedKind: 'app_default' }));
    await mountIt(UserRolesCard, { userId: 2, role: 'user' });
    await openApps();
    const said = q(`[data-testid="perm-effective-${KEY}"]`);
    expect(said.dataset.allowed).toBe('false');
    expect(said.dataset.source).toBe('override');
    expect(said.textContent).toContain(EN.source.override);
    expect(choice('deny').getAttribute('aria-checked')).toBe('true');
    expect(choice('inherit').textContent?.trim()).toBe(EN.apps.defaultAllowed);
  });

  it("an administrator's Allow goes into the exceptions; Default takes it out, the rest untouched", async () => {
    asAdministrator(true);
    roles.catalogue.mockResolvedValue(withApps);
    roles.forUser.mockResolvedValue(personAnswer({ 'files.delete': 'deny' }, { allowed: true, kind: 'app_default', inherited: true, inheritedKind: 'app_default' }));
    roles.setOverrides.mockImplementation(async (_id: number, o: Record<string, string>) =>
      personAnswer(o, { allowed: o[KEY] !== 'deny', kind: o[KEY] ? 'override' : 'app_default', inherited: true, inheritedKind: 'app_default' }),
    );
    await mountIt(UserRolesCard, { userId: 2, role: 'user' });
    await openApps();
    expect(q(`[data-testid="perm-effective-${KEY}"]`).dataset.source).toBe('app_default');
    expect(q(`[data-testid="perm-effective-${KEY}"]`).textContent).toContain(EN.source.app_default);

    await click(choice('deny'));
    await click(q('[data-testid="user-permissions-save"]'));
    expect(roles.setOverrides).toHaveBeenLastCalledWith(2, { 'files.delete': 'deny', [KEY]: 'deny' });

    await click(choice('inherit'));
    await click(q('[data-testid="user-permissions-save"]'));
    expect(roles.setOverrides).toHaveBeenLastCalledWith(2, { 'files.delete': 'deny' });
  });

  it('a delegated administrator sees them read-only, with why, and no shortcut touches them', async () => {
    asAdministrator(false);
    roles.catalogue.mockResolvedValue(withApps);
    roles.forUser.mockResolvedValue(personAnswer({ [KEY]: 'allow', 'files.delete': 'deny' }, { allowed: true, kind: 'override', inherited: true, inheritedKind: 'app_default' }));
    roles.setOverrides.mockImplementation(async (_id: number, o: Record<string, string>) =>
      personAnswer(o, { allowed: true, kind: 'override', inherited: true, inheritedKind: 'app_default' }),
    );
    await mountIt(UserRolesCard, { userId: 2, role: 'user' });
    await openApps();
    expect(q('[data-testid="perm-apps-read-only"]').textContent?.trim()).toBe(EN.apps.readOnly);
    for (const opt of ['inherit', 'allow', 'deny'] as const) expect(choice(opt).disabled, opt).toBe(true);
    // No "all Allow / all Deny" and no reset for a group nobody here can change.
    const appsHeader = q('[data-testid="perm-group-apps"]').firstElementChild as HTMLElement;
    expect(appsHeader.querySelectorAll('button').length).toBe(1);
    expect(document.body.querySelector('[data-testid="perm-apps-reset"]')).toBeNull();

    // A preset pins the catalogue's permissions and leaves the app's exception.
    await click(q('[data-testid="preset-read_only"]'));
    await click(q('[data-testid="user-permissions-save"]'));
    expect(roles.setOverrides).toHaveBeenLastCalledWith(2, { [KEY]: 'allow', 'files.download': 'allow', 'files.delete': 'deny' });

    // "Clear exceptions" clears what they may clear.
    await click(q('[data-testid="user-permissions-clear"]'));
    await click(q('[data-testid="user-permissions-save"]'));
    expect(roles.setOverrides).toHaveBeenLastCalledWith(2, { [KEY]: 'allow' });
    // …and with only the app's left, there is nothing more to clear.
    expect(q<HTMLButtonElement>('[data-testid="user-permissions-clear"]').disabled).toBe(true);
  });

  it("an administrator's preset and Clear leave the app exceptions; the Apps group resets them on its own", async () => {
    asAdministrator(true);
    roles.catalogue.mockResolvedValue(withApps);
    roles.forUser.mockResolvedValue(
      personAnswer({ [KEY]: 'allow', 'files.delete': 'deny' }, { allowed: true, kind: 'override', inherited: true, inheritedKind: 'app_default' }),
    );
    roles.setOverrides.mockImplementation(async (_id: number, o: Record<string, string>) =>
      personAnswer(o, { allowed: true, kind: o[KEY] ? 'override' : 'app_default', inherited: true, inheritedKind: 'app_default' }),
    );
    await mountIt(UserRolesCard, { userId: 2, role: 'user' });

    // A preset pins the 28; the app's exception stays.
    await click(q('[data-testid="preset-read_only"]'));
    await click(q('[data-testid="user-permissions-save"]'));
    expect(roles.setOverrides).toHaveBeenLastCalledWith(2, { [KEY]: 'allow', 'files.download': 'allow', 'files.delete': 'deny' });

    // Clear takes the 28's away; the app's exception stays.
    await click(q('[data-testid="user-permissions-clear"]'));
    await click(q('[data-testid="user-permissions-save"]'));
    expect(roles.setOverrides).toHaveBeenLastCalledWith(2, { [KEY]: 'allow' });
    expect(q<HTMLButtonElement>('[data-testid="user-permissions-clear"]').disabled).toBe(true);

    // The Apps group's own reset is where it goes.
    await openApps();
    const reset = q<HTMLButtonElement>('[data-testid="perm-apps-reset"]');
    expect(reset.textContent?.trim()).toBe(EN.apps.resetAll);
    expect(reset.disabled).toBe(false);
    await click(reset);
    expect(choice('inherit').getAttribute('aria-checked')).toBe('true');
    expect(reset.disabled).toBe(true);
    await click(q('[data-testid="user-permissions-save"]'));
    expect(roles.setOverrides).toHaveBeenLastCalledWith(2, {});
  });

  it("a role's reset clears its app decisions and nothing else", async () => {
    roles.getDefaults.mockImplementation(async () => ({ permissions: ['files.download'], preset: '', apps: { [KEY]: 'deny' } }));
    await mountIt(BuiltinRoleEditor, { modelValue: true, role: 'user', catalogue: withApps });
    await openApps();
    await click(q('[data-testid="perm-apps-reset"]'));
    await click(q('[data-testid="builtin-role-save"]'));
    expect(roles.putDefaults).toHaveBeenLastCalledWith(['files.download'], 'user', {});
  });

  it('no app declares a permission: no Apps group', async () => {
    asAdministrator(true);
    roles.catalogue.mockResolvedValue(withoutApps);
    roles.forUser.mockResolvedValue({ ...personAnswer({}, { allowed: true, kind: 'app_default', inherited: true, inheritedKind: 'app_default' }) });
    await mountIt(UserRolesCard, { userId: 2, role: 'user' });
    expect(document.body.querySelector('[data-testid="perm-group-apps"]')).toBeNull();
  });
});
