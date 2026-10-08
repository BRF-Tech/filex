// The role screens: the Roles page, the role editor, the person card and the
// Role field on a person's page, plus the Users list's role filter.
//
// What has to stay true (roles review, 2026-09-27):
//   · switching off a role people hold asks first — it leaves them with
//     nothing — and deleting one asks what they become;
//   · the built-in roles are counted by the server, not by fetching everyone;
//   · a new role starts from Standard user, not from nothing;
//   · the person card names the role they hold, not a yellow "Custom";
//   · the Role field is saved in one call, and only when it changed;
//   · the Users filter lists people on a custom role under that role only.
import { beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { createI18n } from "vue-i18n";
import { createMemoryHistory, createRouter } from "vue-router";

import en from "@/locales/en.json";
import { useCapabilitiesStore } from "@/stores/capabilities";
import { openRowMenu, pickMenuItem, menuEntries, closeRowMenus } from "../helpers/rowMenu";
import { useToastStore } from "@/stores/toast";

const { catalogue, noDelete, roles, people, usersApi } = vi.hoisted(() => {
  const catalogue = {
    permissions: [
      { key: "files.download", group: "files" },
      { key: "files.delete", group: "files", viewer_capped: true },
      { key: "access.desktop", group: "access" },
      { key: "admin.full", group: "admin", role_only: true },
    ],
    presets: [
      {
        name: "full_admin",
        permissions: [
          "files.download",
          "files.delete",
          "access.desktop",
          "admin.full",
        ],
      },
      {
        name: "standard",
        permissions: ["files.download", "files.delete", "access.desktop"],
      },
      { name: "read_only", permissions: ["files.download"] },
    ],
  };

  const noDelete = {
    id: 7,
    name: "NoDelete",
    description: "",
    enabled: true,
    permissions: ["files.download", "access.desktop"],
    targets: [],
    effects: { "files.delete": "allow" },
    settings: {},
    conditions: { paths: ["Scratch"] },
  };

  const roles = {
    catalogue: vi.fn(async () => catalogue),
    listRules: vi.fn(async () => ({
      rules: [noDelete],
      assignments: { "2": 7 } as Record<string, number>,
      builtinMembers: { admin: 1, user: 3, viewer: 0 },
    })),
    createRule: vi.fn(async (b: object) => ({ ...b, id: 8 })),
    updateRule: vi.fn(async (id: number, b: object) => ({ ...b, id })),
    deleteRule: vi.fn(async () => undefined),
    forUser: vi.fn(),
    setOverrides: vi.fn(),
    allOverrides: vi.fn(async () => ({})),
    getDefaults: vi.fn(async () => ({ permissions: ["files.download", "access.desktop"], preset: "", apps: {} })),
    putDefaults: vi.fn(async (permissions: string[]) => ({ permissions, preset: "", apps: {} })),
    gaps: vi.fn(async () => [] as unknown[]),
    restoreGap: vi.fn(async () => [] as unknown[]),
    dismissGap: vi.fn(async () => [] as unknown[]),
    userRole: vi.fn(async () => 7 as number | null),
    userRoleDetail: vi.fn(async () => ({ role_id: 7 as number | null, group_role: null })),
    setUserRole: vi.fn(async (_id: number, r: number | string) => ({
      role_id: typeof r === "number" ? r : null,
      role: "user",
    })),
  };
  const people = [
    {
      id: 1,
      email: "admin@local",
      display_name: "",
      username: "admin",
      role: "admin",
    },
    {
      id: 2,
      email: "demo@local",
      display_name: "",
      username: "demo",
      role: "user",
    },
    {
      id: 3,
      email: "bob@local",
      display_name: "",
      username: "bob",
      role: "user",
    },
  ];
  const usersApi = {
    // The search is the server's (GET /admin/users?q=, #207): a `q` answers
    // the accounts it matches, without one every account.
    list: vi.fn(async (params: { q?: string } = {}) => {
      const q = (params.q ?? "").toLowerCase();
      const items = q
        ? people.filter((p) => [p.email, p.username, p.display_name].some((v) => v.toLowerCase().includes(q)))
        : people;
      return { items, total: items.length, page: 1, page_size: 25 };
    }),
    get: vi.fn(async (id: number) => people.find((p) => p.id === id)),
    create: vi.fn(async (b: { email: string; role: string }) => ({ id: 9, display_name: '', username: '', ...b })),
    update: vi.fn(async (id: number, b: object) => ({
      ...people.find((p) => p.id === id),
      ...b,
    })),
    remove: vi.fn(),
    resetPassword: vi.fn(),
  };
  return { catalogue, noDelete, roles, people, usersApi };
});

vi.mock("@/api/roles", () => ({ RolesApi: roles }));
// The server's account check (POST /api/auth/account/check), asked while the
// Add user form is typed (0.54 #209: the form keeps no rule of its own). A
// unit test never reaches the network (helpers/noNetwork), so it answers here.
const checkAccount = vi.fn(async (_q: { email?: string; username?: string; for?: string }) => ({}) as Record<string, { error: string; message: string }>);
vi.mock('@/api/auth', async (importOriginal) => {
  const real = await importOriginal<typeof import('@/api/auth')>();
  return { ...real, AuthApi: { ...real.AuthApi, checkAccount: (q: { email?: string; username?: string; for?: string }) => checkAccount(q) } };
});
vi.mock("@/api/users", () => ({ UsersApi: usersApi }));
vi.mock("@/api/groups", () => ({
  GroupsApi: { list: vi.fn(async () => []), forUser: vi.fn(async () => []), memberships: vi.fn(async () => ({})) },
}));
vi.mock("@/api/storages", () => ({
  StoragesApi: { list: vi.fn(async () => []) },
}));
vi.mock("@/api/quota", () => ({
  quotaApi: {
    adminGet: vi.fn(async () => ({ used_bytes: 0, quota_bytes: 0 })),
    adminSet: vi.fn(),
    adminRecompute: vi.fn(),
  },
}));

import Roles from "@/views/Roles.vue";
import Users from "@/views/Users.vue";
import UserEdit from "@/views/UserEdit.vue";
import RoleEditor from "@/components/RoleEditor.vue";
import UserRolesCard from "@/components/UserRolesCard.vue";
import { chosenValue, listOffering, pickOption } from "../helpers/choiceSelect";

if (
  typeof HTMLDialogElement !== "undefined" &&
  !HTMLDialogElement.prototype.showModal
) {
  HTMLDialogElement.prototype.showModal = function () {
    this.setAttribute("open", "");
  };
  HTMLDialogElement.prototype.close = function () {
    this.removeAttribute("open");
  };
}

async function mountAt(
  view: unknown,
  opts: { props?: object; path?: string } = {},
): Promise<VueWrapper> {
  const i18n = createI18n({
    legacy: false,
    locale: "en",
    fallbackLocale: "en",
    messages: { en },
  });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: "/users/:id",
        name: "user-edit",
        component: { template: "<div />" },
      },
      { path: "/users", name: "users", component: { template: "<div />" } },
      { path: "/groups", name: "groups", component: { template: "<div />" } },
      { path: "/:p(.*)*", component: { template: "<div />" } },
    ],
  });
  await router.push(opts.path ?? "/");
  await router.isReady();
  const w = mount(view as never, {
    props: opts.props,
    global: { plugins: [i18n, router] },
    attachTo: document.body,
  });
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

/** Pick from a list (core ChoiceSelect, #160): open it, click the option. */
async function choose(list: Element, value: string) {
  await pickOption(list, value);
  await flushPromises();
}

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
});

describe("Roles page", () => {
  it("counts the built-in roles from the server, without fetching every user", async () => {
    await mountAt(Roles);
    expect(q('[data-testid="role-members-builtin-user"]').textContent).toContain("3");
    expect(q('[data-testid="role-members-builtin-admin"]').textContent).toContain("1");
    expect(usersApi.list).not.toHaveBeenCalled();
  });

  it("is the explorer's table, and the Administrator row has no actions", async () => {
    const w = await mountAt(Roles);
    // The one table (docs/CONTRIBUTING.md → "UI rules" → "One table"): the
    // shared DataTable, not a list drawn to look like one.
    expect(q('[data-testid="roles-list"]').classList.contains("fe-table")).toBe(true);
    expect(document.body.querySelector('[data-testid="role-actions-builtin-admin"]')).toBeNull();
    await openRowMenu(w, "role-actions-builtin-user");
    expect(menuEntries().map((e) => e.label)).toEqual(["Edit the User role"]);
    closeRowMenus();
    await openRowMenu(w, "role-actions-rule-7");
    expect(menuEntries().map((e) => e.label)).toEqual(["Edit role", "Delete"]);
    closeRowMenus();
  });

  // The built-in roles are one row for the whole platform, and the server
  // refuses a tenant admin's save (requireSupertenant in PutDefaults). The page
  // offers a tenant's admin the role to read — not a Save that always fails.
  it("a tenant admin sees a built-in role read-only: the note, the grid locked, no Save", async () => {
    const caps = useCapabilitiesStore();
    caps.$patch({ loaded: true, data: { ...caps.data, caller_admin: false } });
    const w = await mountAt(Roles);
    await openRowMenu(w, "role-actions-builtin-user");
    expect(menuEntries().map((e) => e.label)).toEqual(["View the User role"]);
    await pickMenuItem("role-actions-builtin-user-edit");
    await flushPromises();

    expect(q('[data-testid="builtin-role-platform-note"]').textContent).toContain("create a role");
    const boxes = Array.from(
      document.body.querySelectorAll<HTMLInputElement>('[data-testid="builtin-role-user"] input[type="checkbox"]'),
    );
    expect(boxes.length).toBeGreaterThan(0);
    expect(boxes.every((b) => b.disabled)).toBe(true);
    expect(document.body.querySelector('[data-testid="builtin-role-save"]')).toBeNull();
    expect(roles.putDefaults).not.toHaveBeenCalled();
  });

  it("the platform operator (or a single-tenant admin) still edits a built-in role", async () => {
    const caps = useCapabilitiesStore();
    caps.$patch({ loaded: true, data: { ...caps.data, caller_admin: true } });
    const w = await mountAt(Roles);
    await openRowMenu(w, "role-actions-builtin-user");
    expect(menuEntries().map((e) => e.label)).toEqual(["Edit the User role"]);
    await pickMenuItem("role-actions-builtin-user-edit");
    await flushPromises();

    expect(document.body.querySelector('[data-testid="builtin-role-platform-note"]')).toBeNull();
    await click(q('[data-testid="builtin-role-save"]'));
    expect(roles.putDefaults).toHaveBeenCalledTimes(1);
  });

  it("asks before switching off a role people hold, and sends nothing on No", async () => {
    await mountAt(Roles);
    const sw = q('[data-testid="role-enabled-rule-7"] [role="switch"]');

    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    await click(sw);
    expect(confirm).toHaveBeenCalledOnce();
    expect(roles.updateRule).not.toHaveBeenCalled();

    confirm.mockReturnValue(true);
    await click(sw);
    expect(roles.updateRule).toHaveBeenCalledWith(
      7,
      expect.objectContaining({ enabled: false }),
    );
  });

  it("deleting a role people hold asks what they become, and sends the answer", async () => {
    const w = await mountAt(Roles);
    const confirm = vi.spyOn(window, "confirm");
    await openRowMenu(w, "role-actions-rule-7");
    await pickMenuItem("role-actions-rule-7-delete");
    await flushPromises();

    expect(confirm).not.toHaveBeenCalled();
    await choose(q('[data-testid="role-delete-move"]'), "viewer");
    await click(q('[data-testid="role-delete-confirm"]'));
    expect(roles.deleteRule).toHaveBeenCalledWith(7, "viewer");
  });
});

// A save on 0.50 or older leaves the User role, or a custom role's folder
// part, allowing "Add files and folders" but not "Encrypt" (PR #86), and
// nothing tells that apart from a choice. The page points each out, with one
// click to give the permission back and one to say it was on purpose.
describe("Roles that may have lost a permission", () => {
  const userGap = { id: "builtin:user:files.encrypt", key: "files.encrypt", from: "files.create", role: "user" };
  const ruleGap = { id: "role:7:files.encrypt", key: "files.encrypt", from: "files.create", rule_id: 7, rule_name: "NoDelete" };

  it("shows nothing when no role lacks one", async () => {
    await mountAt(Roles);
    expect(roles.gaps).toHaveBeenCalledOnce();
    expect(document.body.querySelector('[data-testid="role-gaps"]')).toBeNull();
  });

  it("names each role, why it may have happened, and gives the permission back in one click", async () => {
    roles.gaps.mockResolvedValueOnce([userGap, ruleGap]);
    roles.restoreGap.mockResolvedValueOnce([ruleGap]);
    await mountAt(Roles);

    const box = q('[data-testid="role-gaps"]');
    expect(box.textContent).toContain("2 roles may have lost a permission");
    expect(box.textContent).toContain("0.50 or older");
    expect(box.textContent).toContain("if it was taken away on purpose, dismiss this");
    expect(q(`[data-testid="role-gap-${userGap.id}"]`).textContent).toContain(
      "User: “Add files and folders” is allowed, “Encrypt” is not.",
    );
    expect(q(`[data-testid="role-gap-${ruleGap.id}"]`).textContent).toContain(
      "NoDelete, in its folders: “Add files and folders” is allowed, “Encrypt” is not.",
    );

    await click(q(`[data-testid="role-gap-restore-${userGap.id}"]`));
    expect(roles.restoreGap).toHaveBeenCalledWith(userGap.id);
    expect(document.body.querySelector(`[data-testid="role-gap-${userGap.id}"]`)).toBeNull();
    expect(q('[data-testid="role-gaps"]').textContent).toContain("1 role may have lost a permission");
    expect(useToastStore().toasts.map((x) => x.message)).toContain("“Encrypt” given back to User.");
  });

  it("dismissing one says so and leaves the role as it is", async () => {
    roles.gaps.mockResolvedValueOnce([ruleGap]);
    roles.dismissGap.mockResolvedValueOnce([]);
    await mountAt(Roles);
    await click(q(`[data-testid="role-gap-dismiss-${ruleGap.id}"]`));
    expect(roles.dismissGap).toHaveBeenCalledWith(ruleGap.id);
    expect(roles.restoreGap).not.toHaveBeenCalled();
    expect(roles.updateRule).not.toHaveBeenCalled();
    expect(document.body.querySelector('[data-testid="role-gaps"]')).toBeNull();
  });
});

describe("Role editor", () => {
  it("a new role starts from Standard user", async () => {
    await mountAt(RoleEditor, {
      props: { modelValue: true, rule: null, catalogue, storages: [] },
    });
    const name = q<HTMLInputElement>(
      '[data-testid="rule-name"] input, input[data-testid="rule-name"]',
    );
    name.value = "Contractors";
    name.dispatchEvent(new Event("input"));
    await click(q('[data-testid="rule-save"]'));

    expect(roles.createRule).toHaveBeenCalledOnce();
    const body = roles.createRule.mock.calls[0][0] as {
      name: string;
      permissions: string[];
    };
    expect(body.name).toBe("Contractors");
    expect(body.permissions).toEqual([
      "files.download",
      "files.delete",
      "access.desktop",
    ]);
  });

  it("keeps the folder part of a role that has folders", async () => {
    await mountAt(RoleEditor, {
      props: { modelValue: true, rule: noDelete, catalogue, storages: [] },
    });
    await click(q('[data-testid="rule-save"]'));
    expect(roles.updateRule).toHaveBeenCalledWith(
      7,
      expect.objectContaining({
        effects: { "files.delete": "allow" },
        conditions: { paths: ["Scratch"] },
      }),
      expect.any(Array),
    );
  });

  it("offers Encrypt in a role's folder part, and keeps it on save", async () => {
    // files.encrypt differs by folder like files.create, and the catalogue
    // upgrade writes it into every folder part that allows files.create:
    // an editor that dropped it on save would undo the upgrade.
    const withEncrypt = {
      ...catalogue,
      permissions: [
        { key: "files.download", group: "files" },
        { key: "files.create", group: "files", viewer_capped: true },
        { key: "files.encrypt", group: "files", viewer_capped: true },
      ],
    };
    const dropBox = {
      ...noDelete,
      id: 9,
      name: "Drop box",
      permissions: ["files.download"],
      effects: { "files.create": "allow", "files.encrypt": "allow" },
      conditions: { paths: ["Drop"] },
    };
    await mountAt(RoleEditor, {
      props: { modelValue: true, rule: dropBox, catalogue: withEncrypt, storages: [] },
    });

    const part = '[data-testid="role-folder-effects"]';
    const row = q(`${part} [data-testid="perm-row-files.encrypt"]`);
    expect(row.textContent).toContain("Encrypt");
    expect(row.textContent).toContain("end-to-end encrypted folder or file");
    expect(q<HTMLButtonElement>(`${part} [data-testid="perm-files.encrypt-allow"]`).disabled).toBe(false);

    await click(q('[data-testid="rule-save"]'));
    expect(roles.updateRule).toHaveBeenCalledWith(
      9,
      expect.objectContaining({
        effects: { "files.create": "allow", "files.encrypt": "allow" },
      }),
      expect.any(Array),
    );
  });
});

// A permission a later filex stored is not in this one's catalogue: the
// editor has no row for it, so it must not drop it (0.50 dropped files.encrypt
// that way, PR #86).
describe("Role editor - a later version's permissions", () => {
  const laterRole = {
    ...noDelete,
    id: 11,
    name: "Later",
    permissions: ["files.download", "files.from_a_later_version"],
    effects: { "files.delete": "allow", "share.from_a_later_version": "deny" },
    conditions: { paths: ["Scratch"] },
  };

  it("keeps them in its list and in its folder part, and says what it showed", async () => {
    await mountAt(RoleEditor, {
      props: { modelValue: true, rule: laterRole, catalogue, storages: [] },
    });
    await click(q('[data-testid="rule-save"]'));
    expect(roles.updateRule).toHaveBeenCalledOnce();
    const [id, body, shown] = roles.updateRule.mock.calls[0] as unknown as [
      number,
      { permissions: string[]; effects: Record<string, string> },
      string[],
    ];
    expect(id).toBe(11);
    expect(body.permissions).toContain("files.from_a_later_version");
    expect(body.effects).toEqual({ "files.delete": "allow", "share.from_a_later_version": "deny" });
    expect(shown).toEqual(["files.download", "files.delete", "access.desktop", "admin.full"]);
  });

  it("a preset replaces what the editor shows, not what it cannot", async () => {
    await mountAt(RoleEditor, {
      props: { modelValue: true, rule: laterRole, catalogue, storages: [] },
    });
    const readOnly = Array.from(document.body.querySelectorAll<HTMLButtonElement>("button")).find(
      (b) => b.textContent?.trim() === "Read-only",
    );
    expect(readOnly, "the Read-only preset button").toBeTruthy();
    await click(readOnly!);
    await click(q('[data-testid="rule-save"]'));
    const body = roles.updateRule.mock.calls[0][1] as { permissions: string[] };
    expect(body.permissions).toEqual(["files.download", "files.from_a_later_version"]);
  });
});

describe("Person card", () => {
  const answer = (role: string) => ({
    user_id: 2,
    role,
    overrides: {},
    effective: {
      permissions: [],
      allowed: [],
      preset: "",
      settings: {},
      rules: [7],
      conditional_rules: [],
    },
  });

  it('names the custom role instead of a yellow "Custom"', async () => {
    roles.forUser.mockResolvedValue(answer("user"));
    await mountAt(UserRolesCard, {
      props: { userId: 2, role: "user", customRole: { name: "NoDelete" } },
    });
    expect(
      q('[data-testid="user-permissions-preset"]').textContent?.trim(),
    ).toBe("NoDelete");
  });

  it("Clear exceptions leaves a later version's exception it cannot show", async () => {
    roles.forUser.mockResolvedValue({
      ...answer("user"),
      overrides: { "files.delete": "deny", "files.from_a_later_version": "allow" },
    });
    roles.setOverrides.mockImplementation(async (_id: number, overrides: Record<string, string>) => ({
      ...answer("user"),
      overrides,
    }));
    await mountAt(UserRolesCard, {
      props: { userId: 2, role: "user", customRole: null },
    });
    await click(q('[data-testid="user-permissions-clear"]'));
    await click(q('[data-testid="user-permissions-save"]'));
    expect(roles.setOverrides).toHaveBeenCalledWith(2, { "files.from_a_later_version": "allow" });
    expect(q<HTMLButtonElement>('[data-testid="user-permissions-clear"]').disabled).toBe(true);
  });

  it("warns that a read-only role cannot be given file changes as exceptions", async () => {
    roles.forUser.mockResolvedValue(answer("viewer"));
    await mountAt(UserRolesCard, {
      props: { userId: 2, role: "viewer", customRole: { name: "Auditors" } },
    });
    expect(q('[data-testid="read-only-role-note"]').textContent).toContain(
      "Auditors",
    );
  });
});

describe("Role field on a person’s page", () => {
  function roleSelect(): HTMLElement {
    return q('form .fe-select__trigger');
  }
  async function submitDetails() {
    q("form").dispatchEvent(new Event("submit", { cancelable: true }));
    await flushPromises();
  }

  it("saving without changing the role sends no role change", async () => {
    roles.forUser.mockResolvedValue({
      user_id: 2,
      role: "user",
      overrides: {},
      effective: {
        permissions: [],
        allowed: [],
        preset: "",
        settings: {},
        rules: [],
        conditional_rules: [],
      },
    });
    await mountAt(UserEdit, { path: "/users/2" });
    expect(chosenValue(roleSelect())).toBe("custom:7");
    await submitDetails();
    expect(usersApi.get).toHaveBeenCalledTimes(2); // the save ran: it reads the account back
    expect(roles.setUserRole).not.toHaveBeenCalled();
  });

  it("a changed role is one call", async () => {
    roles.forUser.mockResolvedValue({
      user_id: 2,
      role: "user",
      overrides: {},
      effective: {
        permissions: [],
        allowed: [],
        preset: "",
        settings: {},
        rules: [],
        conditional_rules: [],
      },
    });
    await mountAt(UserEdit, { path: "/users/2" });
    await choose(roleSelect(), "viewer");
    await submitDetails();
    expect(roles.setUserRole).toHaveBeenCalledOnce();
    expect(roles.setUserRole).toHaveBeenCalledWith(2, "viewer");
  });

  it("a built-in role over a group's role asks first, and says when the group's still applies", async () => {
    roles.forUser.mockResolvedValue({
      user_id: 3,
      role: "user",
      overrides: {},
      effective: { permissions: [], allowed: [], preset: "", settings: {}, rules: [], conditional_rules: [] },
    });
    const viaFinance = { role_id: 7, group_id: 1, group_name: "Finance" };
    roles.userRoleDetail.mockResolvedValue({ role_id: null, group_role: viaFinance });
    roles.setUserRole.mockResolvedValueOnce({ role_id: null, role: "user", group_role: viaFinance } as never);
    const confirm = vi.spyOn(window, "confirm");
    await mountAt(UserEdit, { path: "/users/3" });
    expect(q('[data-testid="user-group-role"]').textContent).toContain("Finance");

    confirm.mockReturnValueOnce(false);
    await choose(roleSelect(), "viewer");
    await submitDetails();
    expect(confirm.mock.calls[0][0]).toContain("does not replace it");
    expect(roles.setUserRole, "No sends nothing").not.toHaveBeenCalled();

    confirm.mockReturnValueOnce(true);
    await submitDetails();
    expect(roles.setUserRole).toHaveBeenCalledWith(3, "viewer");
    const toasts = useToastStore().toasts;
    expect(toasts.at(-1)?.level).toBe("warn");
    expect(toasts.at(-1)?.message).toContain("still applies");
    roles.userRoleDetail.mockResolvedValue({ role_id: 7, group_role: null });
  });
});

describe("Users list — a role from a group", () => {
  it("shows the role in force and the group it comes from", async () => {
    roles.listRules.mockResolvedValueOnce({
      rules: [noDelete],
      assignments: { "2": 7 },
      groupAssignments: { "3": { role_id: 7, group_id: 1, group_name: "Finance" } },
      builtinMembers: { admin: 1, user: 1, viewer: 0 },
    } as never);
    await mountAt(Users);
    const row = [...document.body.querySelectorAll("tr, [role='row']")].find((r) => r.textContent?.includes("bob@local"));
    expect(row?.textContent).toContain(noDelete.name);
    expect(row?.textContent).toContain("through Finance");
  });

  it("someone with a role of their own shows that one badge — not the built-in one beside it", async () => {
    await mountAt(Users);
    const row = [...document.body.querySelectorAll("tr, [role='row']")].find((r) => r.textContent?.includes("demo@local"));
    expect(row?.textContent).toContain(noDelete.name);
    expect(row?.textContent).not.toContain(en.users.roles.user);
    expect(row?.querySelector('[data-testid="user-role-via"]')).toBeNull();
  });
});

describe("Users filter", () => {
  function shown(): string[] {
    return people
      .map((p) => p.email)
      .filter((e) => document.body.textContent?.includes(e));
  }
  async function filterSelect(): Promise<HTMLElement> {
    const s = await listOffering("custom:7");
    expect(s).toBeTruthy();
    return s as HTMLElement;
  }

  // 0.54 (#207): the search is the SERVER's (GET /admin/users?q=), asked
  // once typing pauses; the page filters no address of its own.
  it("search narrows the list by address (the server answers the matches)", async () => {
    await mountAt(Users);
    expect(shown()).toEqual(["admin@local", "demo@local", "bob@local"]);
    const box = q<HTMLInputElement>('input[placeholder="Search"]');
    box.value = "BOB";
    box.dispatchEvent(new Event("input"));
    await new Promise((r) => setTimeout(r, 300));
    await flushPromises();
    expect(usersApi.list).toHaveBeenLastCalledWith(expect.objectContaining({ q: "BOB" }));
    expect(shown()).toEqual(["bob@local"]);
    // …and together with the role filter.
    await choose(await filterSelect(), "custom:7");
    expect(shown()).toEqual([]);
  });

  it("lists a person on a custom role under that role, not under User", async () => {
    await mountAt(Users);
    await choose(await filterSelect(), "user");
    expect(shown()).toEqual(["bob@local"]);
    await choose(await filterSelect(), "custom:7");
    expect(shown()).toEqual(["demo@local"]);
  });
});

describe('Add user', () => {
  it('offers the custom roles, and gives the new account the one picked', async () => {
    const w = await mountAt(Users);
    const add = [...document.body.querySelectorAll('button')].find((b) => b.textContent?.trim() === 'Add user');
    await click(add!);
    const email = q<HTMLInputElement>('input[name="new-user-email"]');
    email.value = 'new@local';
    email.dispatchEvent(new Event('input'));
    let roleSelect: HTMLElement | null = null;
    for (const dialog of document.body.querySelectorAll('[role="dialog"], dialog')) {
      roleSelect = await listOffering('custom:7', dialog);
      if (roleSelect) break;
    }
    expect(roleSelect, 'the Add user role list offers NoDelete').toBeTruthy();
    await choose(roleSelect!, 'custom:7');
    await click(q('[data-testid="user-create-access-invite"]'));
    await click(q('[data-testid="user-create-submit"]'));

    expect(usersApi.create).toHaveBeenCalledWith(expect.objectContaining({ email: 'new@local', role: 'viewer' }));
    expect(roles.setUserRole).toHaveBeenCalledWith(9, 7);
    w.unmount();
  });
});

describe("Roles page — the table", () => {
  it("built-in roles first, then a Custom roles heading; every row says its permissions and status", async () => {
    roles.getDefaults = vi.fn(async (role: string) => ({ permissions: role === "viewer" ? ["files.download"] : ["files.download", "files.delete"], preset: "" }));
    await mountAt(Roles);
    const heads = [...document.body.querySelectorAll(".fe-list__group")].map((h) => h.textContent?.trim());
    expect(heads).toEqual([en.permissions.rules.builtinHeading, en.permissions.rules.customHeading]);

    expect(q('[data-testid="role-summary-builtin-admin"]').textContent).toContain(en.permissions.rules.allPermissions);
    expect(q('[data-testid="role-summary-builtin-user"]').textContent).toContain("2 of");
    expect(q('[data-testid="role-summary-builtin-viewer"]').textContent).toContain("1 of");
    expect(q('[data-testid="role-enabled-builtin-admin"]').textContent).toContain(en.permissions.rules.alwaysOn);
    expect(q('[data-testid="role-enabled-builtin-user"]').textContent).toContain(en.permissions.rules.alwaysOn);
    expect(q('[data-testid="role-enabled-rule-7"]').querySelector('input, button, [role="switch"]')).not.toBeNull();
  });

  it("says how people hold a role under its count, and its limits as a badge", async () => {
    roles.listRules.mockResolvedValueOnce({
      rules: [{ ...noDelete, settings: { share_link_max_days: 7, blocked_extensions: ["exe", "bat"] } }],
      assignments: {},
      groupAssignments: { "3": { role_id: 7, group_id: 1, group_name: "Finance" }, "4": { role_id: 7, group_id: 1, group_name: "Finance" } },
      builtinMembers: { admin: 1, user: 3, viewer: 0 },
    } as never);
    const groups = (await import("@/api/groups")).GroupsApi as unknown as { list: ReturnType<typeof vi.fn> };
    groups.list.mockResolvedValueOnce([{ id: 1, name: "Finance", role_id: 7, links: [], description: "", priority: 0 }]);
    await mountAt(Roles);
    const members = q('[data-testid="role-members-rule-7"]').textContent ?? "";
    expect(members).toContain("2 members");
    expect(q('[data-testid="role-members-how-rule-7"]').textContent).toContain("all through 1 group");
    const limits = q('[data-testid="role-limits-rule-7"]');
    expect(limits.textContent).toContain("2 limits");
    expect(limits.getAttribute("title")).toContain("exe");
    // Nobody on Viewer: its count is drawn faint.
    expect(q('[data-testid="role-members-builtin-viewer"] span').className).toContain("text-zinc-400");
  });

  it("a row opens its editor", async () => {
    const w = await mountAt(Roles);
    const row = [...document.body.querySelectorAll('[role="row"]')].find((r) => r.querySelector('[data-testid="role-name-rule-7"]')) as HTMLElement;
    row.click();
    await flushPromises();
    expect(document.body.querySelector('[data-testid="rule-editor"]')).not.toBeNull();
    w.unmount();
  });
});

