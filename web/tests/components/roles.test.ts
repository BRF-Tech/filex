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
    userRole: vi.fn(async () => 7 as number | null),
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
    list: vi.fn(async () => ({
      items: people,
      total: people.length,
      page: 1,
      page_size: 25,
    })),
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
vi.mock("@/api/users", () => ({ UsersApi: usersApi }));
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

async function choose(select: HTMLSelectElement, value: string) {
  select.value = value;
  select.dispatchEvent(new Event("change"));
  await flushPromises();
}

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
  document.body.innerHTML = "";
});

describe("Roles page", () => {
  it("counts the built-in roles from the server, without fetching every user", async () => {
    await mountAt(Roles);
    expect(q('[data-testid="role-builtin-user"]').textContent).toContain("3");
    expect(q('[data-testid="role-builtin-admin"]').textContent).toContain("1");
    expect(usersApi.list).not.toHaveBeenCalled();
  });

  it("asks before switching off a role people hold, and sends nothing on No", async () => {
    await mountAt(Roles);
    const sw = q('[data-testid="rule-7"] [role="switch"]');

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
    await mountAt(Roles);
    const confirm = vi.spyOn(window, "confirm");
    const del = [
      ...document.body.querySelectorAll('[data-testid="rule-7"] button'),
    ].find((b) => b.getAttribute("aria-label") === "Delete");
    expect(del).toBeTruthy();
    await click(del!);

    expect(confirm).not.toHaveBeenCalled();
    await choose(
      q<HTMLSelectElement>('[data-testid="role-delete-move"] select'),
      "viewer",
    );
    await click(q('[data-testid="role-delete-confirm"]'));
    expect(roles.deleteRule).toHaveBeenCalledWith(7, "viewer");
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
    );
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
  function roleSelect(): HTMLSelectElement {
    return q<HTMLSelectElement>("form select");
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
    expect(roleSelect().value).toBe("custom:7");
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
});

describe("Users filter", () => {
  function shown(): string[] {
    return people
      .map((p) => p.email)
      .filter((e) => document.body.textContent?.includes(e));
  }
  function filterSelect(): HTMLSelectElement {
    const s = [...document.body.querySelectorAll("select")].find((x) =>
      [...x.options].some((o) => o.value === "custom:7"),
    );
    expect(s).toBeTruthy();
    return s as HTMLSelectElement;
  }

  it("lists a person on a custom role under that role, not under User", async () => {
    await mountAt(Users);
    await choose(filterSelect(), "user");
    expect(shown()).toEqual(["bob@local"]);
    await choose(filterSelect(), "custom:7");
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
    const roleSelect = [...document.body.querySelectorAll('[role="dialog"] select, dialog select')].find((x) =>
      [...(x as HTMLSelectElement).options].some((o) => o.value === 'custom:7'),
    ) as HTMLSelectElement | undefined;
    expect(roleSelect, 'the Add user role list offers NoDelete').toBeTruthy();
    await choose(roleSelect!, 'custom:7');
    await click(q('[data-testid="user-create-submit"]'));

    expect(usersApi.create).toHaveBeenCalledWith(expect.objectContaining({ email: 'new@local', role: 'viewer' }));
    expect(roles.setUserRole).toHaveBeenCalledWith(9, 7);
    w.unmount();
  });
});
