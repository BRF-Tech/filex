# Multi-tenancy (native)

> Status: **in progress** — Phase 1 (schema foundation) landed on `feat/multi-tenant`.
> This document is the design + build plan. It is the source of truth for the
> feature; keep it in sync as phases land.

## 1. Goal & shape

Serve **N independent tenants from one filex install**. A tenant = an **auth
realm (OIDC or local) bound to a host, linked to one or more storages**. Someone
who signs in through realm X sees only storage X — *even an admin*. Sharing works
between users of the same realm; users of other realms are invisible.

This is **not** built from scratch — filex already owns the hard isolation
primitives:

- `confine.Root` — server-side path jail, cannot be bypassed by the client.
- per-storage RBAC + item grants.
- scoped API tokens — work.brf.sh already runs a hand-rolled version of this
  (each project confined to `s3-test://projeler/<proje>`).

Native multi-tenancy makes that a first-class, host-resolved layer.

### The provider = tenant collapse

A **provider** row *is* the tenant. We do not add a separate `tenants` table:
the auth realm, the host binding, the storage links and the branding all hang off
the provider. (If a tenant ever needs two IdPs or local+OIDC, split provider from
tenant later — YAGNI while every tenant = one Keycloak realm.)

## 2. Two isolation layers (the de-risking argument)

Isolation is enforced in **two independent circuits**. Keep them separate in your
head and in the code:

1. **File data → storage confinement.** A request can only reach the storages
   its provider is linked to; every path is confined server-side (`confine.Root`,
   node→storage derivation, client never supplies a storage id it doesn't own).
   This is the *only* circuit that can leak file bytes.
2. **Directory → provider_id scoping.** User lists, share-pickers, grants, audit,
   search results are filtered by the requester's `provider_id`.

**Consequence:** a bug in layer 2 leaks *at most a user's name*, never file data,
because layer 1 is a separate circuit. That makes this "simple-layer" tenancy,
not the scary kind — the worst realistic outcome is a name leak, small blast
radius. Say this explicitly in user docs; it is the reason to trust the feature.

## 3. Mode gating (backward-compat is non-negotiable)

`FILEX_MULTI_TENANT` (config `multi_tenant`, env `FILEX_MULTI_TENANT=1|true`).

- **OFF (default):** on a plain single-tenant install (only the `default`
  provider, which is the supertenant) it behaves **exactly** as today — the
  scoped-store wrapper is a no-op, host resolution is skipped, admins see
  everything, `provider_id` is inert. A CI test asserts this is byte-identical to
  the pre-feature build — the trust anchor for OSS.
- **OFF with tenants present = maintenance mode.** If you turn the flag off on an
  install that already grew tenant providers, **no data is touched** and it is
  fully reversible; the flag just gates *login*: only the **supertenant**
  provider's users may authenticate, every tenant is locked out until you flip it
  back on. (This is why single-tenant is unchanged: there the only provider *is*
  the supertenant, so nobody is locked out.) Turning the mode off is therefore a
  safe operation, not a one-way door.
- **ON:** host resolution, per-provider confinement and directory scoping engage.

This lets the OSS product sell both postures: isolation-maximalists run
one-container-per-tenant (mode off, zero shared-process risk); scale-seekers run
one install, mode on.

### Activation / migration

Flipping the flag on must not require surgery:

1. There must be a super-admin. If a pre-existing OIDC provider exists, mark it
   `is_supertenant`; if the install is local-auth only, the local bootstrap
   admin is the super-admin.
2. All existing users get `provider_id` = the default provider.
3. Existing storages link to the default provider.

Migration 00014 already creates the `default` provider (with `is_supertenant=1`,
the "original org = owner") and backfills `users.provider_id`, so the invariant
"every user has a provider" holds from the first upgrade — inertly while mode is
off. **"Existing OIDC becomes supertenant" is a migration-time default (an
editable row), not a hard code rule.** Turning the mode back off never corrupts
data — it drops to the maintenance mode above (supertenant-only login), so it is
always reversible; no guard needed.

## 4. Data model

New in migration `00014_multi_tenant` (3 dialects, additive only):

- **`providers`** — the tenant/provider registry: `slug`, `name`, `host`,
  `auth_type` (`oidc|local`), `oidc_*` (issuer/client_id/client_secret/redirect),
  `role_claim`, `admin_group`, `is_supertenant`, `enabled`.
- **`provider_storages`** — M:N link (behaviour 1:1 in the first UI; join table
  from day 1 so 1:N is a UI change, not a migration).
- **`users.provider_id`** (nullable FK) + **`users.oidc_subject`**.
- A `default` provider row + `users.provider_id` backfill.

Placement rationale — **scope by ownership, not a column on every table.**
Everything reachable *only through a storage* (nodes, shares, grants, sync_runs,
thumbs, conflicts) inherits tenancy through `storage_id` → `provider_storages`;
it needs **no** `tenant_id`. Everything reachable through a user
(sessions, api_tokens, notifications) inherits through `user_id` → `provider_id`.
So the tenant tag lives on just **providers, users** (+ per-tenant `settings`
later). `api_tokens` need **no** column — a token's tenant is its user's tenant.

### Still to schema (later phases, deliberately deferred here)

- **users email uniqueness** → swap global `UNIQUE(email)` to
  `UNIQUE(provider_id, email)`. Needs a sqlite table-rebuild and a mysql index
  swap; lands with the JIT/login code that actually needs per-provider emails.
- **`oidc_client_secret`** should be encrypted at rest, reusing the
  `external_services.secret_enc` pattern.
- **`settings.provider_id`** (nullable = global) for per-tenant branding.

## 5. Tenant resolution

- **UI / browser → by Host.** `files.diyetlif.com.tr` → provider `diyetlif` →
  authenticate with that realm's OIDC. Each tenant keeps its own domain, no extra
  login page. Behind a proxy, resolve from a **trusted** `X-Forwarded-Host` only
  (untrusted host header must not select a tenant).
- **API / agents → by token.** `api_tokens` → `user_id` → `provider_id`. The
  MCP/agent path is tenant-scoped without any host.

Both feed a single `TenantID`/`ProviderID` into the request context.

## 6. Enforcement — one choke point, fail-closed

Do **not** sprinkle `WHERE provider_id = ?` across handlers (miss one → leak).
Put `provider_id` in the request context and wrap `db.Store` in a **scoped
store** that injects the tenant filter into every tenant-scoped query, and
**fails closed** (no tenant in context in mode-on → error). filex's small,
hand-rolled `Store` interface makes this a single wrapper instead of dozens of
edits.

**Background work is storage-scoped, not request-scoped.** Sync, the queue
(thumbs/ops/replica) and cron run outside any HTTP request — no host, no session.
They already operate *on a storage/node*, so they derive tenancy from that
storage, not from a context. The scoped-store wrapper is for the HTTP/API path;
workers are storage-native and already isolated. Do not thread request-tenant
context through workers.

## 7. Auth & identity (JIT)

- **First OIDC login → JIT-create** the user with `provider_id` = resolving
  provider, `oidc_subject` from the token. The tag is **immutable** (a user can't
  hop tenants).
- Uniqueness is **`(provider_id, email)`** and **`(provider_id, oidc_subject)`**,
  not global — two tenants may both have `admin@`.
- Role/scope: reuse the existing OIDC claim→role logic (roles read from *both*
  id_token and access_token, dotted-path `admin_group`). `scope = platform if
  provider.is_supertenant else tenant`.

## 8. Supertenant & super-admin

Super-admin is **not** a special mechanism — it's a provider flag:

- `providers.is_supertenant = true` → its admins are **platform-scoped** (see all
  tenants). Same JIT/admin_group path; the flag only changes what "admin" *means*.
- **Guardrails:**
  - Supertenant is **confine-exempt + platform-scoped**; it may optionally have
    its own storage (owner-org that is also ops), but that is orthogonal.
  - **At most one** supertenant (enforced).
  - Keep a **local bootstrap super-admin** (bcrypt) — chicken-and-egg (you need
    super-admin to configure the supertenant provider) + break-glass if the realm
    is down. Supertenant realm = daily ops; local admin = setup + emergency.
  - Supertenant realm should be a hardened, separate Keycloak realm; whoever owns
    it owns the platform. Tight `admin_group`.
  - Every cross-tenant super-admin action is **loudly audited**.

## 9. Admin scoping change (biggest behaviour delta)

Today `admin` is RBAC-exempt and sees **all** storages. In mode-on, a
**tenant-admin sees only their linked storages/users/audit**; only the
supertenant sees all. So `storages.list`, the user directory, audit, etc. filter
by the requester's provider. Gate this behind the mode so single-tenant admins
are unchanged.

## 10. Isolation checklist (the periphery that leaks if forgotten)

- [ ] **Search (bleve)** — filter hits to the requester's accessible
      `storage_id`s. *The #1 forgotten leak*; unfiltered search leaks content, not
      just names.
- [ ] **All pickers server-filtered** — user directory, storage-picker,
      share-picker, grant-picker (RBAC), audit, notifications, search. The
      negative-test matrix walks exactly this list.
- [ ] **Shared sidecars (OnlyOffice/convert)** — doc keys must be unguessable and
      storage derived server-side from the node (not client-supplied). Shared JWT
      secret means isolation rests entirely on doc-key→node→storage.
- [ ] **`/api/capabilities`** (pre-auth, host-resolved) — return only *this*
      tenant's branding/features; never reveal other tenants exist.
- [ ] **Public shares (`/s/{token}`) are intentionally host-agnostic** — a link
      is a link; the file stays confined via token→node→storage. Drop/upload
      (`/d/{token}`) confines the same way; client can't override storage.

## 11. Tenant lifecycle

- **Create (provisioning):** super-admin API → provider row + first admin +
  optional default storage (mirror work.brf.sh `TenantCreated`/provisioner).
- **Suspend:** disable login, keep data (billing/hold).
- **Delete:** cascade — users, storages (nodes/shares/grants/sync_runs/thumbs
  inherit via storage), tokens, audit. Get the cascade order right or you orphan
  rows. Needed for GDPR "delete this tenant".

## 12. Per-tenant settings & branding

Each tenant on its own host wants its own `site_name`, logo, default locale,
external-service URLs, and mail sender identity. `settings` gains a nullable
`provider_id` (null = global); the host-resolved `/api/capabilities` returns the
tenant's branding. Ties into `FILEX_DEFAULT_LOCALE` (already shipped).

## 13. Deploy (Compose & Helm)

- **Compose:** `deploy/compose/docker-compose.multi-tenant.yml` — a worked
  2-tenant example (per-host proxy vhosts + provisioning steps in the header).
- **Helm:** `ingress.extraHosts: [{host, tlsSecretName}]` in
  `deploy/helm/filex/values.yaml` — one Ingress rule + TLS cert per tenant
  host (cert-manager per host / SNI), all routed to the same filex.
- **Trusted Host (security):** filex resolves the tenant from the `Host` header
  the proxy forwards (Caddy/nginx/Ingress pass it through by default). Route
  only trusted hosts to filex; don't expose it directly to arbitrary Host
  values. (Even a spoofed host only reaches that tenant's login page — OIDC
  creds + storage confinement are separate layers — but keep the front door
  strict anyway.)

## 14. Test matrix

- **Negative isolation** (run in **both** modes): realm A user cannot see realm
  B's users / storages / nodes / shares / search hits — one case per picker in
  the §10 checklist.
- **Mode-off byte-identical**: mode-off behaviour equals the pre-feature build.

## 15. v2 / open

- Per-tenant quota (extend per-user `user_quota` to a tenant total).
- Per-tenant SMTP / webhook (v1 does per-tenant *sender identity* via branding).
- DB-per-tenant option (stronger isolation, N× migrations/backups) vs the
  shared-DB default here.

---

## Phased roadmap

- [x] **Phase 1 — schema foundation.** `providers` + `provider_storages` tables,
      `users.provider_id`/`oidc_subject`, default-provider backfill (00014 ×3),
      `model.Provider`, `config.MultiTenant` flag. Additive, inert, mode-off
      unchanged. *(verified: sqlite migration applies, backend builds, tests green.)*
- [x] **Phase 2 — provider store + resolver.** Provider CRUD (sqlite+postgres,
      mysql inherits) + `provider_storages` links + `GetProviderIDForStorage`;
      `auth.TenantResolver` (user.provider_id → context Scope) wired into the
      authed/admin/AI groups; `model.User` reads `provider_id`/`oidc_subject`.
      *(verified: `provider_test.go` — CRUD, host resolution, links, reverse lookup.)*
- [x] **Phase 3 — scoped store.** `tenantstore.Store` confines storage listings
      to the scope; resolver fails CLOSED (`tenant.DenyAll`); handlers get the
      scoped store, workers keep raw. *(verified: `store_test.go`.)*
- [x] **Phase 4 — auth/JIT.** (4a) every user auto-joins the default
      (supertenant) provider; `SetUserProvider` (JIT re-home) +
      `GetUserByProviderEmail`; **maintenance mode** + **suspend** in
      `auth.LoginAllowed`, wired into local + OIDC login. (4b)
      `multioidc.Dispatcher`: request host → provider row → lazily-initialised,
      config-cached per-realm `oidc.Driver` (`SetProviderID`); JIT lookup is
      provider-scoped, new users are stamped with the tenant + subject, the tag
      is immutable (cross-tenant email cannot hop realms), unknown hosts fall
      back to the config-file realm. *(verified: `user_provider_test.go`;
      ⚠ live multi-realm Keycloak E2E still to be exercised on a real deploy.)*
      `(provider_id,email)` unique swap (00015) stays a review-gated migration
      (not required for isolation; only for same-email across tenants).
- [x] **Phase 5 — supertenant + admin scoping.** Confine-exempt platform scope;
      at-most-one flag enforced by TRANSFER semantics (setting it on another
      provider un-flags the old holder; direct un-flag/disable/delete of the
      supertenant refused); local bootstrap admin (first-run) = break-glass.
      Tenant-admin sees only its own storages/users/lists.
- [x] **Phase 6 — isolation close-out.** User directory (permission/grant
      picker), search hits, browse-adapter gate, admin shares/audit/grants
      lists, per-tenant capabilities — all scope-filtered; sidecar doc keys were
      already server-derived from the node. Negative tests: tenant A ≠ tenant B
      for users + storages; DenyAll sees nothing.
- [x] **Phase 7 — lifecycle API.** `/api/admin/providers`: provision, suspend
      (enabled=false ⇒ login refused both modes), delete (+`?force=1` user
      cascade; storage rows/files never touched), storage link/unlink;
      supertenant-only management gate in multi-tenant mode. *(Admin SPA page
      for it: pending — API-first.)*
- [~] **Phase 8 — settings/branding.** Host-resolved `/api/capabilities` carries
      `tenant {slug,name}` and never reveals other tenants. Full per-tenant
      branding (`settings.provider_id`, logo/site_name/mail identity) = v2.
- [x] **Phase 9 — deploy + docs.** `docker-compose.multi-tenant.yml`, Helm
      `ingress.extraHosts` (per-host TLS), trusted-host note (§13).
- [~] **Phase 10 — test matrix & CI.** Negative isolation green (users,
      storages, lifecycle guards, suspend, maintenance mode); mode-off = full
      pre-existing suite green (25 pkgs). PENDING: postgres/mysql migration CI
      job, live multi-realm E2E, PR/review.
