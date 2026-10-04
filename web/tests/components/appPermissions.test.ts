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
//   · "Default" says what it comes to for that role or person — for a role,
//     as the SERVER says it (the catalogue's `default_for`, the preview's
//     holder role); the editor keeps no copy of either rule;
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
        // The server's answer (perm.AppDefaultFor): what `default` comes to
        // on each built-in role. The editors read it; they work nothing out.
        default_for: { viewer: false, user: true, admin: true },
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
    // The built-in role a custom role's people would be on is the SERVER's
    // answer (perm.HolderRole, POST /api/admin/roles/preview): each test says
    // what the server answers — nothing here works it out.
    previewHolder: vi.fn(),
  };
  return { withApps, withoutApps, roles };
});

/**
 * The catalogue with the app permission's `default_for` replaced — or taken
 * away (`undefined`). The manifest's `default` stays 'user' throughout, so a
 * label that follows `default` instead of `default_for` shows up here.
 */
function saying(defaultFor: Record<string, boolean> | undefined) {
  return { ...withApps, apps: withApps.apps.map((a) => ({ ...a, default_for: defaultFor })) };
}

/** What the built-in roles answer unless a test says otherwise: no decisions. */
async function noDecisions(_role?: string) {
  return { permissions: ['files.download', 'files.delete'], preset: 'standard', apps: {} as Record<string, string> };
}

vi.mock('@/api/roles', () => ({ RolesApi: roles }));

import BuiltinRoleEditor from '@/components/BuiltinRoleEditor.vue';
import { HOLDER_PREVIEW_DELAY_MS } from '@/lib/appPermissions';
import RoleEditor from '@/components/RoleEditor.vue';
import UserRolesCard from '@/components/UserRolesCard.vue';
import { unmountAll } from '../helpers/teardown';

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () {
    this.setAttribute('open', '');
  };
  HTMLDialogElement.prototype.close = function () {
    this.removeAttribute('open');
  };
}

async function mountIt(view: unknown, props: object, locale = 'en', messages: object = { en, tr }): Promise<VueWrapper> {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: messages as never });
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
  roles.previewHolder.mockReset();
  roles.previewHolder.mockResolvedValue('user');
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
    unmountAll();
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
    expect(choice('deny').textContent?.trim()).toBe(TR.apps.effect.deny);
  });

  // The rule (the last layer of perm.Result.AppAllowed) is the server's:
  // 0.49.0 worked it out here from the manifest's `default` and could say
  // "allowed" where the server answers 403. Here the server says the
  // opposite of what that copy would, and the label follows the server.
  it("Default is the catalogue's default_for, not worked out from the manifest's default", async () => {
    const odd = saying({ viewer: true, user: false, admin: true });
    await mountIt(BuiltinRoleEditor, { modelValue: true, role: 'user', catalogue: odd });
    await openApps();
    expect(choice('inherit').textContent?.trim()).toBe(EN.apps.defaultDenied);
    expect(choice('inherit').dataset.default).toBe('denied');
    // The hint under the label is the manifest's own word, as it was.
    expect(q(`[data-testid="perm-app-default-${KEY}"]`).textContent?.trim()).toBe(EN.apps.holders.user);

    unmountAll();
    await mountIt(BuiltinRoleEditor, { modelValue: true, role: 'viewer', catalogue: odd });
    await openApps();
    expect(choice('inherit').textContent?.trim()).toBe(EN.apps.defaultAllowed);
    expect(choice('inherit').dataset.default).toBe('allowed');
  });

  it('a catalogue that does not say leaves "Default" as it is', async () => {
    await mountIt(BuiltinRoleEditor, { modelValue: true, role: 'user', catalogue: saying(undefined) });
    await openApps();
    expect(choice('inherit').textContent?.trim()).toBe(EN.apps.default);
    expect(choice('inherit').dataset.default).toBe('unknown');
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
    roles.previewHolder.mockResolvedValue('user');
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

  it("the server says the role's people are on Viewer, whose app default does not include a user permission", async () => {
    roles.previewHolder.mockResolvedValue('viewer');
    const readOnly = { ...rule, permissions: ['files.download'], settings: {} };
    await mountIt(RoleEditor, { modelValue: true, rule: readOnly, catalogue: withApps, storages: [] });
    await openApps();
    // The editor asked about the ticks it has, and did not work it out itself.
    expect(roles.previewHolder).toHaveBeenCalledTimes(1);
    expect(roles.previewHolder.mock.calls[0][0]).toEqual({ permissions: ['files.download'], effects: {}, conditions: {} });
    expect(choice('inherit').textContent?.trim()).toBe(EN.apps.defaultDenied);
    expect(choice('inherit').dataset.default).toBe('denied');
  });

  // Lesson #759: 0.49.0 kept a copy of perm.HolderRole in the editor. Now
  // the editor asks again once the ticks rest, and uses only the answer to
  // its latest question.
  it('asks again when the ticks change; an answer to an older question is not used', async () => {
    const answers: Array<(role: string) => void> = [];
    roles.previewHolder.mockImplementation(() => new Promise((resolve) => answers.push(resolve)));
    const readOnly = { ...rule, permissions: ['files.download'], settings: {} };
    await mountIt(RoleEditor, { modelValue: true, rule: readOnly, catalogue: withApps, storages: [] });
    await openApps();
    // Not answered yet: "Default", never a guess.
    expect(choice('inherit').textContent?.trim()).toBe(EN.apps.default);
    expect(choice('inherit').dataset.default).toBe('unknown');

    await click(q('[data-testid="perm-row-files.delete"] input[type="checkbox"]'));
    expect(roles.previewHolder, 'not on every tick: once they rest').toHaveBeenCalledTimes(1);
    await new Promise((r) => setTimeout(r, HOLDER_PREVIEW_DELAY_MS + 50));
    await flushPromises();
    expect(roles.previewHolder).toHaveBeenCalledTimes(2);
    expect((roles.previewHolder.mock.calls[1][0] as { permissions: string[] }).permissions.sort()).toEqual(['files.delete', 'files.download']);

    answers[1]('user');
    await flushPromises();
    expect(choice('inherit').textContent?.trim()).toBe(EN.apps.defaultAllowed);
    answers[0]('viewer');
    await flushPromises();
    expect(choice('inherit').textContent?.trim(), 'the answer about the old ticks came late').toBe(EN.apps.defaultAllowed);
  });

  it("the holder role's Default is read from the catalogue's default_for", async () => {
    // The server says the role's people are on Viewer, and that the app's
    // default gives Viewer the permission — whatever the manifest's word.
    roles.previewHolder.mockResolvedValue('viewer');
    const readOnly = { ...rule, permissions: ['files.download'], settings: {} };
    await mountIt(RoleEditor, { modelValue: true, rule: readOnly, catalogue: saying({ viewer: true, user: false, admin: true }), storages: [] });
    await openApps();
    expect(choice('inherit').textContent?.trim()).toBe(EN.apps.defaultAllowed);
    expect(choice('inherit').dataset.default).toBe('allowed');
  });

  it("the built-in role's own decision still comes before the app's default", async () => {
    roles.previewHolder.mockResolvedValue('viewer');
    roles.getDefaults.mockImplementation(async (role?: string) => ({
      permissions: [],
      preset: '',
      apps: role === 'viewer' ? { [KEY]: 'allow' } : {},
    }));
    const readOnly = { ...rule, permissions: ['files.download'], settings: {} };
    await mountIt(RoleEditor, { modelValue: true, rule: readOnly, catalogue: withApps, storages: [] });
    await openApps();
    expect(choice('inherit').textContent?.trim()).toBe(EN.apps.defaultAllowed);
  });

  it('a catalogue that does not say what the default comes to leaves "Default" as it is', async () => {
    roles.previewHolder.mockResolvedValue('user');
    await mountIt(RoleEditor, { modelValue: true, rule: { ...rule, settings: {} }, catalogue: saying(undefined), storages: [] });
    await openApps();
    expect(roles.previewHolder).toHaveBeenCalledTimes(1);
    expect(choice('inherit').textContent?.trim()).toBe(EN.apps.default);
    expect(choice('inherit').dataset.default).toBe('unknown');
  });

  it('a server that cannot say leaves "Default" as it is', async () => {
    roles.previewHolder.mockRejectedValue({ response: { status: 404 } });
    await mountIt(RoleEditor, { modelValue: true, rule: { ...rule, settings: {} }, catalogue: withApps, storages: [] });
    await openApps();
    expect(choice('inherit').textContent?.trim()).toBe(EN.apps.default);
    expect(choice('inherit').dataset.default).toBe('unknown');
  });

  it('no app declares a permission: no Apps group, and the built-in roles are not even read', async () => {
    await mountIt(RoleEditor, { modelValue: true, rule, catalogue: withoutApps, storages: [] });
    expect(document.body.querySelector('[data-testid="perm-group-apps"]')).toBeNull();
    expect(roles.getDefaults).not.toHaveBeenCalled();
    expect(roles.previewHolder).not.toHaveBeenCalled();
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

    // A preset pins the catalogue's permissions; the app's exception stays.
    await click(q('[data-testid="preset-read_only"]'));
    await click(q('[data-testid="user-permissions-save"]'));
    expect(roles.setOverrides).toHaveBeenLastCalledWith(2, { [KEY]: 'allow', 'files.download': 'allow', 'files.delete': 'deny' });

    // Clear takes the catalogue's permissions away; the app's exception stays.
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

  // The owner's call (2026-09-29): an app's Allow / Deny are keys of their
  // own (`permissions.apps.effect.*`), so renaming the apps' Deny — to
  // "Block", say — does not rename every Deny in the role editors. Same words
  // today; this pins that they are separate KEYS.
  it("an app's Allow / Deny are words of their own, not the catalogue's", async () => {
    asAdministrator(true);
    roles.catalogue.mockResolvedValue(withApps);
    roles.forUser.mockResolvedValue(personAnswer({}, { allowed: true, kind: 'app_default', inherited: true, inheritedKind: 'app_default' }));
    const renamed = JSON.parse(JSON.stringify(en)) as typeof en;
    renamed.permissions.apps.effect = { allow: 'Let', deny: 'Block' };
    await mountIt(UserRolesCard, { userId: 2, role: 'user' }, 'en', { en: renamed, tr });
    await openApps();
    expect(choice('allow').textContent?.trim()).toBe('Let');
    expect(choice('deny').textContent?.trim()).toBe('Block');
    // The Apps group's own shortcuts say the same words as its rows.
    const appsHeader = q('[data-testid="perm-group-apps"]').firstElementChild as HTMLElement;
    const shortcuts = Array.from(appsHeader.querySelectorAll('button')).map((b) => b.textContent?.trim());
    expect(shortcuts).toEqual(expect.arrayContaining(['Let', 'Block']));
    // A catalogue permission keeps the catalogue's words.
    expect(q('[data-testid="perm-files.delete-deny"]').textContent?.trim()).toBe(EN.effect.deny);
    expect(q('[data-testid="perm-files.delete-allow"]').textContent?.trim()).toBe(EN.effect.allow);
  });

  it('no app declares a permission: no Apps group', async () => {
    asAdministrator(true);
    roles.catalogue.mockResolvedValue(withoutApps);
    roles.forUser.mockResolvedValue({ ...personAnswer({}, { allowed: true, kind: 'app_default', inherited: true, inheritedKind: 'app_default' }) });
    await mountIt(UserRolesCard, { userId: 2, role: 'user' });
    expect(document.body.querySelector('[data-testid="perm-group-apps"]')).toBeNull();
  });
});
