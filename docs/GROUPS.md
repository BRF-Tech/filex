# filex — groups

Backend `internal/group`. A **group** is a named set of people in one tenant.
It can be given the two things a person can be given:

- **Folder access** — share a folder or a whole storage with the group in the
  explorer's sharing panel (Share → People), exactly as with a person. Every
  member reaches it at the level the group was given
  ([RBAC.md](RBAC.md)).
- **A role** — a custom role ([PERMISSIONS.md](PERMISSIONS.md)) that every
  member with **no role of their own** holds.

People are in a group because an administrator **added them**, or because the
group is **linked to an SSO group** their sign-in carries.

Nothing changes on upgrade: migration 00072 only adds tables, and until an
administrator makes a group nobody is in one.

- [Folder access](#folder-access)
- [The role](#the-role)
- [Members and SSO links](#members-and-sso-links)
- [Tenants](#tenants)
- [Who may manage groups](#who-may-manage-groups)
- [API](#api)
- [Audit](#audit)
- [Things to know](#things-to-know)

## Folder access

A group's grant counts exactly as the same grant to each member would:

- the **highest** level covering a path wins — a person's own grant, or any
  of their groups';
- the account's ceiling still caps it — a **Viewer** account in a group
  granted Editor reaches the folder as a viewer (so, unlike a person's grant,
  any level can be given to a group);
- grants apply on storages with RBAC on only, like a person's;
- the item appears under **Shared with me** for every member — once, at the
  highest level they hold there (their own grant or a group's), capped by
  their account as the folder itself is.

Leaving the group ends the reach at once (the next request); deleting the
group deletes its grants. Protocol sessions (WebDAV, SFTP, …) see the change
when they reconnect, as for a person's grant.

## The role

Every person has **one** role ([PERMISSIONS.md](PERMISSIONS.md#custom-roles)).
Groups only fill it in for someone who has none of their own:

1. The person's **own custom role**, picked on their page, wins — whatever
   their groups hold.
2. Otherwise, the role of the group they are in with the **highest role
   priority** (a number on the group's page, 0 by default; equal priorities:
   the older group). A group whose role is **switched off** is skipped for
   the next one — but if every role their groups hold is switched off, they
   get the first of them, which gives nothing, never the built-in User role.
3. Otherwise, their built-in role — User or Viewer.

A person whose Role field shows a **built-in** role (User or Viewer) has no
role of their own, so a group's role applies to them — picking User or Viewer
there does not replace it. The page says so before saving, and after. To
override a group's role for one person, give them a custom role on their
page, or take them out of the group. Administrators are bound by no role,
from a group or otherwise.

Everything else is as if the role were given on the person's page: a
switched-off role gives nothing, "different in some folders" applies per path,
and the **level underneath** (User or Viewer, see
[PERMISSIONS.md](PERMISSIONS.md#custom-roles)) follows the role — joining a
group whose role is read-only makes the account a Viewer, and while it is
one its own Editor and Owner folder shares count as Viewer too (the person's
page says so). filex keeps the level the account had **before** a group's
role first moved it, and puts it back when no group role applies any more:
leaving a read-only group gives back User; leaving a group whose role made a
Viewer account a User gives back Viewer — never the full built-in User role
the account never had.

The person's page, the Users list and the Roles page's counts say where a
role comes from ("through the group Contractors"), and so does every
refusal:

```json
{
  "error": "permission_denied",
  "permission": "files.delete",
  "source": { "kind": "rule", "rule_id": 3, "rule_name": "No delete", "group_id": 2, "group_name": "Contractors" },
  "message": "The role “No delete”, which you have through the group “Contractors”, does not allow you to delete files."
}
```

Deleting a role groups hold asks, like one people hold, what they get
instead: another role, or — for `?to=user` / `?to=viewer` — none, with their
members left on that built-in level. Another role must be one every holder
can hold: a tenant's role is refused for a group or a person of another
tenant (`400`), before anything moves.

## Members and SSO links

A membership is either:

| Source | How | Kept until |
|---|---|---|
| **Added** (`manual`) | An administrator, on the group's page or the person's | Someone removes it |
| **SSO** (`sso`) | The group names an SSO group the sign-in carried | The next sign-in that no longer carries it |

A group's **SSO groups** are values of the provider's `role_claim` — the same
claim the admin mapping and a role's starting-role targets read
([SSO.md](SSO.md)), in the ID or the access token, dotted paths such as
`realm_access.roles` included. Compared exactly, one per line on the group's
page. At **every** SSO sign-in the account joins each group of its tenant
that names one of its values and leaves each group it was in **only** through
SSO that no longer does — the identity provider is the authority. A person
added by hand stays whatever the provider says; adding by hand someone who is
in through SSO makes the membership manual.

Adding, changing or removing a group's SSO links takes effect **at once**,
from the groups each person's last SSO sign-in carried (filex keeps them) —
not only at their next sign-in.

Removing an SSO member by hand is allowed, but they join again at their next
sign-in while the provider still puts them in the group — the page says so
before it does it.

No `role_claim` configured, or a token without it, is no SSO groups: the
account leaves every group it was in through SSO.

## Tenants

A group belongs to **one tenant**, like a custom role
([MULTI-TENANCY.md](MULTI-TENANCY.md)):

- A tenant administrator's group is its tenant's, whatever the request says;
  they see and change only their tenant's groups, and anything of another
  tenant reads as not found.
- Only people of the group's tenant can be members — by hand or through SSO.
  So the same SSO group name (`finance`) in two tenants fills two separate
  groups. An account moved to another tenant leaves its old tenant's groups,
  and the role and level they gave it.
- Its role must be install-wide or of the same tenant; it can be given
  folders only on its tenant's storages, and only its tenant's people see it
  in the sharing panel.
- The supertenant, or an install without tenants, can make an
  **install-wide** group. Its SSO links match only the supertenant's
  sign-ins — another tenant's identity provider sending the same value does
  not put its people in it; the supertenant adds them by hand if it means
  to. On a single-tenant install every group is one. On
  a multi-tenant install such a group can hold people of every tenant, so a
  tenant's owners are never offered it for their folders.

## Who may manage groups

Groups are managing people, so Admin → Groups is **`admin.users`**
([delegated administration](PERMISSIONS.md#delegated-administration)). A
delegated administrator:

- gives a group only a role whose every *allow* they hold — and cannot add
  people to a group whose role they could not give them directly;
- never changes **their own** memberships (a group can carry folders and a
  role) — nor deletes a group they are in, or changes its role or priority,
  which would change their own role the same way;
- never changes a group's **SSO links**, which decide membership at sign-in —
  theirs included — nor its **role priority**, which decides whose role a
  member of several groups gets. Both are an administrator's.

A group whose role has administration rights (any `admin.*` permission) makes
its members delegated administrators, so giving a group such a role, changing
its priority or SSO links, or adding people to it needs an administrator
signed in to the admin panel — an API key is refused with `403
{"error":"session_required"}`, as when the role is given to one person
([PERMISSIONS.md](PERMISSIONS.md#handing-out-administration-takes-a-session)).

Anyone with `admin.users` can change who is in a group, so sharing a folder
with a group trusts whoever manages it. Giving a group folder access is done
by the folder's **owner** in the sharing panel (`share.users`), like sharing
with a person. Giving a group **Owner** lets every member manage who has
access there, so the panel asks first.

## API

| Method & path | Who | |
|---|---|---|
| `GET /api/admin/groups` | `admin.users` | the tenant's groups, with `member_count`, `grant_count` |
| `POST /api/admin/groups` | `admin.users` | `{name, description, role_id, priority, links, provider_id?}` → `201 {group, members, grants}`. `provider_id` only from an unconfined caller; a name the tenant has → `409` |
| `GET /api/admin/groups/{id}` | `admin.users` | `{group, members[{user_id, email, name, role, source, added_at}], grants[{id, path, level, …}]}` |
| `PUT /api/admin/groups/{id}` | `admin.users` | name, description, role, priority (absent keeps it), links (the tenant never changes) |
| `DELETE /api/admin/groups/{id}` | `admin.users` | the group, its memberships and its grants → `204` |
| `POST /api/admin/groups/{id}/members` | `admin.users` | `{"user_ids":[3,4]}` |
| `DELETE /api/admin/groups/{id}/members/{user_id}` | `admin.users` | |
| `GET /api/admin/users/{id}/groups` | `admin.users` | `{groups[{id, name, role_id, source}]}` |
| `GET` / `PUT /api/admin/users/{id}/roles` | `admin.users` | now also `group_role: {role_id, group_id, group_name}` when the person has none of their own — on a `PUT` of a built-in role, the group role that still decides |
| `GET /api/admin/roles` | `admin.users` | now also `group_assignments`: user id → `{role_id, group_id, group_name}`; `builtin_members` no longer counts them |
| `GET /api/files/permissions/groups?q=` | signed in | groups the caller could share with (names only) |
| `POST /api/files/permissions` | owner + `share.users` | `{path, group_id, level}` — a group instead of `user_id` |
| `PATCH` / `DELETE /api/files/permissions/groups/{id}` | owner + `share.users` | a group's grant (its own id space) |
| `DELETE /api/admin/grants/groups/{id}` | `admin.grants` | |

`GET /api/files/permissions` lists group grants beside people's, each with
`kind: "user" | "group"` (a group's with `group_id` and `group_name`);
`GET /api/admin/grants` does the same.

A group body:

```json
{
  "name": "Finance",
  "description": "Everyone who approves invoices",
  "role_id": 3,
  "priority": 0,
  "links": [{ "kind": "sso", "value": "finance" }]
}
```

Names are unique within a tenant — the database holds it (install-wide
groups count as one tenant), so two saves at once cannot both win. `links`
kinds other than `sso` are refused. `priority` is between −1000 and 1000.

## Audit

| Action | Details |
|---|---|
| `group.create` / `group.delete` | the group (`name`, `role_id`, `links`); delete also `members` |
| `group.update` | `before` / `after` |
| `group.members_add` | `user_ids` |
| `group.member_remove` | `user_id` |

Deleting a role groups hold adds `groups` (how many) to its
`permission_rule.delete` row.

## Things to know

- **Storage.** `user_groups` (with `priority` and `tenant_key`, the unique
  name's scope), `user_group_members` (with `source`), `group_file_grants` —
  a separate table with its own ids, so nothing that reads a person's grants
  ever meets a row without a person — and `user_group_levels`, the level to
  give back.
- **Several processes** on one database see a membership or role change
  within 3 seconds (the permissions snapshot); folder access is read per
  request.
- **Starting role or group?** A role's *Starting role for SSO groups*
  ([PERMISSIONS.md](PERMISSIONS.md#starting-role-for-sso-groups)) sets a new
  account's role once, at its first sign-in. A group linked to the SSO group
  and holding the role follows the provider at every sign-in — use that.
