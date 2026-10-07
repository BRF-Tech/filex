# Tenant self-service: sign-in providers, own domains, the tenant screen

> **Status (0.50).** Three pieces that grow
> [native multi-tenancy](MULTI-TENANCY.md) from "the operator configures every
> tenant through the API" into "a tenant runs itself". All of them are built;
> the one part not yet measured against the real thing is said in its row.
>
> | Piece | State |
> |---|---|
> | [Tenant screen](#the-tenant-screen) (Admin → Tenants, Admin → My tenant) and the `admin_tenants_*` MCP tools | built |
> | [Data model](#data-model) (`auth_instances`, `provider_auth_instances`, `provider_domains`; migration 00076) | built |
> | [Sign-in providers bound to tenants](#sign-in-providers-bound-to-tenants) | built; measured in `e2e/realenv` against Keycloak 26.8 (the operator's OIDC bound to two tenants, a tenant's own OIDC, the first sign-in rule, group links, the admin group) and OpenLDAP over ldaps with a pasted CA (a tenant's own LDAP through the guard, and the guard's refusals) |
> | [The sign-in page per realm](#the-sign-in-page-per-realm) | built; measured in a browser against Keycloak in `e2e/realenv` (the buttons with and without a realm, `/api/auth/oidc/start?instance=&realm=`) |
> | [Addresses and own domains](#addresses-and-own-domains) (platform subdomain, CNAME proof, TLS) | built; measured against real servers in `e2e/realenv` (2026-10-02): filex's own ACME against Pebble, Let's Encrypt's test authority (TLS-ALPN-01, HTTP-01, a CNAME'd own domain, renewal, refusals), and `ask` / `get_certificate http` against a real Caddy issuing from the same Pebble |
>
> Every design decision below was taken by the owner (2026-09-30 and
> 2026-10-01); where one was a choice between options, the option not taken
> is named so a later reader knows it was considered.

## What the owner decided

**2026-09-30, the scope:**

1. **A sign-in with no realm is the platform's own tenant** (the supertenant),
   and it is offered the sign-in providers the platform's tenant has. If the
   platform's tenant signs in with Windows accounts, the platform's sign-in
   page offers Windows accounts.
2. **Every sign-in provider** (OIDC, LDAP, Linux PAM, Windows, header proxy)
   can be bound to tenants: to **one** (1-1) or to **several** (1-n). A tenant
   decides its own set; the platform's tenant too.
3. **Who binds.** The platform operator (an administrator of the platform's
   tenant) binds any provider to any tenants. A tenant's administrator adds
   **their own OIDC or LDAP** for their own tenant and nothing else. The
   operating-system providers (Linux PAM, Windows: the accounts of the machine
   filex runs on) and the header proxy are the operator's alone.
4. **The `provider` pin of 0.50** (`FILEX_LDAP_PROVIDER`,
   `FILEX_HEADER_PROVIDER`) is read as an **implicit binding** while installs
   move over. Removing it comes last.
5. **Own domains**, proven, served with a certificate, managed on the
   tenant's screen.
6. **The tenant screen**, and MCP tools for it. The realm is **suggested from
   the slug** while a tenant is being created and **read-only** after, with the
   reason shown.

**2026-10-01, the forks:**

1. **TLS is an installation setting**, both ways supported: the reverse proxy
   in front issues certificates (an "ask" endpoint for Caddy's on-demand TLS),
   or filex issues them itself (ACME / Let's Encrypt). Besides either, a tenant
   may **bring its own certificate** (certificate and key, PEM). Not chosen:
   only one of the two ways.
2. **The upgrade binds every provider that exists to every tenant that exists**,
   and says so: in the CHANGELOG's upgrade note and on the screen ("review,
   and remove the tenants you do not want"). A provider added after the
   upgrade is bound to the platform's tenant only. The 0.50 pin is an implicit
   binding. Not chosen: an "all tenants, also future ones" flag.
3. **The operating-system test account** is promoted when a provider is
   switched on **in a scope for the first time**: in the platform's tenant it
   becomes a platform super administrator (as today), in a tenant it becomes
   that tenant's administrator, with an address in that tenant's realm. A
   later test promotes nobody. Not chosen: promotion in the platform's tenant
   only.
4. **A tenant's own OIDC and LDAP go through a guarded client**: no loopback,
   private, link-local or overlay address (`internal/netguard`, after DNS and
   on every redirect), LDAP only over `ldaps://` or StartTLS, the CA pasted as
   PEM text and never named as a file on the server. A platform super
   administrator may tick **"allow insecure and internal-network providers"**
   for one tenant; that tenant's providers then connect unguarded (an internal
   address, plain `ldap://`). Off by default, every change audited. Not chosen:
   no guard at all, or an approval step per provider.
5. **Domains stay simple.** Every tenant gets a **platform subdomain**
   (`<realm>.<tenant domain>`, the tenant domain an installation setting). A
   tenant points its own domain at that subdomain with a **CNAME**, and the
   CNAME is the proof: no TXT record. A domain belongs to one tenant at a
   time. A periodic check **suspends** a domain whose CNAME no longer points
   at its tenant's subdomain (kept, not deleted; the tenant is told; it comes
   back when the record does). No takeover logic beyond that.

**2026-10-02, which account an SSO sign-in opens** (after a review measured
three takeovers: [SSO.md](SSO.md#which-account-an-sso-sign-in-opens)):

1. **The tenant boundary.** The account an OIDC sign-in finds may belong only
   to the tenant the sign-in is for - the meaning a password sign-in's realm
   has. Anything else is refused with the generic answer.
2. **No fallback without a flow.** On a multi-tenant install a callback that
   brings no flow cookie is refused (`expired`), never answered by the first
   OIDC of the set.
3. **Every OIDC is bound to a tenant when it is built**: a tenant's own is
   pinned to it, a shared one is the platform's tenant's until a flow names
   another. No OIDC looks an account up across the platform.
4. **The (issuer, sub) bind**: after the first match an account answers to
   that identity, and the same address with another identity is refused
   (`identity_mismatch`); an administrator can remove the bind.
5. **`email_verified`**: an unverified address opens no unbound existing
   account (`email_unverified`) and opens a new account switched off
   (`account_pending`), unless the provider's addresses are trusted
   (`trust_email`, off by default). The OIDC providers that existed when 0.50
   first started keep their old behaviour: trust comes ON for them, and their
   cards say so until saved.

## Data model

Three tables and one column, additive (migration `00076_tenant_self_service`;
a branch merged first takes the number and this one moves up).

### `auth_instances`: one configured sign-in provider

Until now a provider was its driver: `auth.ldap.*` settings rows meant "the
LDAP", and the environment's `ldap` replaced it. Two tenants with two
directories need two LDAPs, so a configured provider becomes a row.

| Column | Meaning |
|---|---|
| `id` | |
| `slug` | Stable name, unique. The first instance of each driver keeps the driver's name (`ldap`, `oidc`, ...), so `/api/admin/auth-providers/ldap` and the `admin_auth_providers_*` tools keep addressing what they addressed before. |
| `driver` | `oidc`, `ldap`, `pam`, `windows`, `proxy-header` |
| `label` | What the sign-in page calls it ("Acme SSO"). Empty: the driver's default words. |
| `origin` | `environment` (built from `FILEX_AUTH_DRIVERS` / the config file; the row carries only its bindings), `page` (the operator made it), `tenant` (a tenant's administrator made it) |
| `owner_provider_id` | The tenant that made it (`origin = tenant`), else NULL. A tenant's own instance is bound to that tenant only, ever. |
| `enabled` | |
| `config_json` | The fields of the driver's schema (`authsetup.Schema`), secrets sealed with `FILEX_SECRET_KEY`. Used by every instance except a driver's **first** (slug = the driver's name): that one keeps its configuration in the `auth.<driver>.*` settings rows it always had, so nothing is copied and a rollback finds everything where it was. Empty for `origin = environment`. |
| `legacy` | Reserved for an instance saved before v0.43.0 and never applied (a driver's first carries that mark in `auth.<name>.legacy_unapplied`, as before). |
| `promoted_json` | The tenants (and the platform, as `0`) in which an operating-system instance has already promoted its test account: the first switch-on in a scope promotes, no later one does. |
| `created_by`, `created_at`, `updated_at` | |

### `provider_auth_instances`: which tenant may sign in through which provider

`(provider_id, instance_id)`, both cascading, plus `source`: `explicit` (somebody
bound it), `upgrade` (the one-time upgrade below), `pin` (read from a 0.50 pin).
An instance with no binding row signs nobody in on a multi-tenant install.

### `provider_domains`: a tenant's own domains

| Column | Meaning |
|---|---|
| `provider_id` | The tenant |
| `domain` | Lower case, ASCII (an IDN is stored in its `xn--` form), **UNIQUE**: one tenant at a time, whatever the state |
| `status` | `pending` (the CNAME was not seen yet), `active`, `suspended` (it was seen and is gone) |
| `checked_at`, `active_since`, `last_error` | The last check, and what it found when it failed |
| `tls_cert_pem` | A certificate the tenant brought (chain, PEM), or empty |
| `tls_key_sealed` | Its private key, sealed with `FILEX_SECRET_KEY` (a brought certificate needs the key set) |
| `tls_not_after` | The brought certificate's expiry, for the screen and the warning before it |
| `created_by`, `created_at` | |

### Columns on `providers`

- `allow_insecure_auth`: the operator's per-tenant switch of decision 4.
  Written only by a platform super administrator, audited on every change.
- `oidc_trust_email` (migration 00079): the tenant's own OIDC on its row
  takes every address it sends as verified
  ([SSO.md](SSO.md#trust-this-providers-email-addresses)). Off for a tenant
  made on 0.50; on for one whose own OIDC existed at the upgrade. Its own
  statement, never written by an ordinary update; audited when it changes.
- The platform subdomain is **not stored**: it is `<realm>.<tenant domain>`,
  computed, so it cannot drift from the realm (which never changes) and needs
  no backfill.

## Moving existing installs over (nothing breaks on upgrade)

The upgrade is a Go step (`authsetup.EnsureAnchors`), run on every start and
every reload of the providers, idempotent. What it binds is decided once per
database, the first time it runs (the mark `auth.instances.upgraded`):

1. **Page providers.** Every managed driver with `auth.<driver>.*` rows gets
   an instance row with slug `<driver>` (its "anchor"). Its configuration
   **stays** in the settings rows: nothing is copied, no secret moves, and a
   binary of the previous version finds everything after a rollback.
2. **Environment providers.** Each provider the environment lists gets a row
   with `origin = environment` and slug `<driver>`. Its configuration stays in
   the environment; the row exists so its bindings have something to point at.
   The environment's still shadows the page's of the same driver, as before.
3. **A tenant's own OIDC on its provider row** (`oidc_issuer`,
   `oidc_client_id`, ...) stays where it is and is read as that tenant's own
   SSO (choice `tenant` on its sign-in page): nothing is copied, no secret
   moves, and `PATCH /api/admin/providers/{id}` changes it as before. A tenant
   may have more of its own besides (instances with `origin = tenant`).
4. **Bindings: every instance that exists at the upgrade is bound to the
   platform's tenant and to every tenant that exists at that moment**
   (`source = upgrade`). That is what happens today: one LDAP serves every
   tenant. The CHANGELOG's upgrade note and the screen (a banner over the
   bindings until the operator dismisses it) say it in so many words: review
   the bindings, and remove the tenants a provider should not serve. A
   provider made after the upgrade (on the page, or a new `FILEX_AUTH_DRIVERS`
   entry) is bound to the platform's tenant only, and a tenant created after
   the upgrade has no shared provider until somebody binds one.
5. **Pins.** A 0.50 pin keeps doing what it does: a sign-in that names no
   tenant (a bare SFTP name, FTPS without SNI) is judged by the pinned
   instance and its new account is homed in the pinned tenant. The pin also
   counts as a binding to the pinned tenant (`source = pin`), read again at
   every start; a pin that names no tenant binds nothing and says so in the
   log.

6. **OIDC trust** (2026-10-02, a separate Go step, `authsetup.UpgradeOIDCTrust`,
   once per database and only on one that already had accounts): every OIDC
   that exists - the page's, every further instance, every tenant's own on its
   row - gets **Trust this provider's email addresses** switched on, so it
   signs people in as before 0.50, and its card says the upgrade set it until
   somebody saves it ([SSO.md](SSO.md#trust-this-providers-email-addresses)).

A **single-tenant install** reads none of this: every enabled instance serves
every sign-in, as every provider does today. The tables are filled anyway, so
turning multi-tenant mode on later (Admin → Multi-tenant mode, or
`FILEX_MULTI_TENANT`) starts from the same bindings. While the mode is off the
Identity providers page shows no bindings and lists no tenant's own provider;
the rows are kept for when it is back on.

## Sign-in providers bound to tenants

### The running set

`authsetup.Live` builds **one driver per instance** (the single construction
path, `authsetup.Build`, is unchanged) and keeps, beside the instance-wide view,
**a view per tenant**: `Set.For(tenantID)` is the login chain, the OIDC
instances, the directory and the header proxies bound to that tenant. Building
stays per instance, so two tenants bound to one LDAP share one driver and one
connection budget.

- **Password sign-in** (`Live.Login`): the chain of the tenant the sign-in is
  for (`auth.LoginRealmFrom(ctx)`, resolved before any driver runs), in this
  order: the environment's `local` where it is, then the tenant's bound
  directories in binding order, then recovery for the platform's tenant only.
  `local` (filex's own passwords) is the environment's to switch on, for every
  tenant, as today.
- **File protocols** (`Live.Dir`): the realm is already on the context when the
  directory is asked (`protocolauth.realmOf`), so the directory chain is the
  tenant's.
- **Header proxy**: asked per request, only when the request's host belongs to
  a tenant it is bound to (the platform's address: the platform's tenant).
- **OIDC**: an instance is started by id (below), never "the" OIDC.

### Who may do what

| | Platform operator | Tenant administrator |
|---|---|---|
| See every instance and every binding | yes | their own instances and the shared ones bound to them (no configuration, no secrets) |
| Make an OIDC or LDAP instance | yes (`origin = page`) | for their tenant only (`origin = tenant`) |
| Make a PAM, Windows or header-proxy instance | yes | no |
| Bind a shared instance to tenants | yes | no |
| Unbind a shared instance from their own tenant | yes | no (asking is the operator's: a tenant cutting the operator's directory off could lock its own people out) |
| Change or delete a tenant's own instance | yes | their own |
| Allow a tenant insecure and internal-network providers | yes (a platform super administrator) | no |

**A tenant's own instance is guarded** (decision 4): its OIDC issuer and LDAP
URL are dialled through `internal/netguard`; an LDAP needs `ldaps://` or
StartTLS; its CA is pasted PEM (`ca_pem`), and `ca_file` is not a field a
tenant may set. The tenant's `allow_insecure_auth` lifts the first two for that
tenant only. The guard is applied where the connection is made (the driver
gets a dialer), so a test, a sign-in and a file-protocol password check are
held to the same rule.

### Test, probe and "the last way in"

- The test (`Probe`) is the driver's, unchanged, and runs on the instance's
  configuration, through the same guard as its sign-ins.
- **The last way in** is asked per tenant: switching off, deleting or unbinding
  an instance is refused (409 `last_sign_in_method`) when the tenant would keep
  no way for one of its administrators to sign in: `local` with an enabled
  administrator of that tenant holding a password, another running instance
  bound to it, or (platform's tenant only) recovery. The platform's tenant is
  never locked out, whoever asks. A tenant can be, but only when the operator
  confirms it (`confirm_tenant_lockout`), because the operator can always bind
  something back; a tenant administrator never can.

### The operating-system test account in a tenant

Switching on PAM or Windows needs a test that **passed**, with a real account
(`test_account`). The test signs the account in **in a realm**
(`test_account.realm`; default: the platform's tenant when the instance is bound
to it, else the one tenant it is bound to; an instance serving several tenants
and not the platform's asks for it, `409 test_realm_required`). When the
instance is switched on in that scope **for the first time** (`promoted_json`),
the account is
promoted: a platform super administrator in the platform's tenant, as today; an
administrator of the tenant, at an address in the tenant's realm
(`alex@acme.local`), anywhere else. A later test, in a scope where the
instance was on before, promotes nobody.

## The sign-in page per realm

The page has to know the realm before it can draw the buttons.

- The buttons the page first draws arrive with `/api/capabilities`, as
  `auth_sso: [{id, label}]`: the SSO of the tenant the address names, else the
  platform's own. On a tenant's own address the realm is fixed, so that is the
  whole answer.
- On the platform's address, typing a realm asks
  `GET /api/auth/methods?realm=<realm>` (public): `{password, recovery, sso}`
  for the tenant the realm and the address name (`auth.ResolveLoginRealm`, the
  same rule as the sign-in itself), and the buttons follow the field.
- The Realm field sits above every way in, because it decides the buttons; it
  still belongs to the password form (Enter in it signs in).
- ⚠ **No realm oracle.** A realm nobody has answers exactly like a tenant with
  no SSO: the password form and no buttons. For a tenant, `password` says
  whether the instance answers a password form at all, never whether that
  tenant has a directory. A realm with SSO reveals itself by its buttons, as
  starting its sign-in would reveal it anyway (the browser is sent to its
  identity provider).
- `/api/capabilities` keeps `auth_drivers`, so a client that never learned
  about realms keeps its one SSO button.

### OIDC for a tenant with no address of its own

`GET /api/auth/oidc/start?instance=<slug>&realm=<realm>` starts that SSO of the
realm's tenant (`instance=tenant` is the tenant's own OIDC on its provider
row). An SSO the tenant does not have, or a realm nobody has, goes back to the
sign-in page with `?error=oidc`, the same answer for both. What the start
decided (the instance, the tenant, where to return) is kept on the server under
a random id in a cookie (`filex_oidc_flow`: ten minutes, used once). Like the
handoff tickets it lives in the process's memory, so behind several replicas
the callback needs the replica the start was served by (sticky routing). The
redirect URI is the address the flow started on when that is one of the
tenant's own, else the instance's own. The callback finds its instance and
its tenant in that record, never by the address, signs the person in **in that
tenant** - the account is looked up and made there, and an account of another
tenant is refused with the generic answer, whatever the identity provider
sent ([SSO.md](SSO.md#which-account-an-sso-sign-in-opens)) - and then:

- when the tenant has an address of its own and the callback did not arrive on
  it: a [handoff ticket](MULTI-TENANCY.md#realms-which-tenant-a-sign-in-is-for)
  (`login_handoff`, 60 seconds, one use) and the bounce page goes to
  `https://<tenant>/admin/login#handoff=...`;
- otherwise the session is opened where the person is, as today.

A shared OIDC bound to several tenants signs each person into the tenant the
sign-in was for. Its identity provider must allow every redirect URI it is
started on. A tenant's own OIDC (its row, or its own instance) signs people in
to its tenant only. A callback that comes back with no flow cookie (a flow
started before the upgrade, a cookie the browser dropped) is refused on a
multi-tenant install - the page says the sign-in expired, and starting again
works; a single-tenant install finishes its one OIDC without a flow, as it
always did.

## Addresses and own domains

### The platform subdomain

`FILEX_TENANT_DOMAIN` (installation setting, e.g. `tenants.files.example`)
gives every tenant an address of its own without anybody typing one:
`<realm>.<tenant domain>` (`acme.tenants.files.example`). It needs a wildcard
DNS record (`*.tenants.files.example`) pointing at the platform, and it is
recognised like a provider's `host`: on it, the sign-in page names the tenant,
links are minted on it, and its session cookie belongs to it. A tenant whose
`host` is set keeps that address as its primary one; the subdomain answers
too. Without `FILEX_TENANT_DOMAIN` there is no subdomain, and a tenant is
reached on its `host` or on the platform's address with its realm, as today.

### Bringing a domain

1. The tenant administrator (or the operator, for any tenant) adds
   `files.acme.example`. filex answers the record to create: a **CNAME** from
   `files.acme.example` to the tenant's subdomain
   (`acme.tenants.files.example`).
2. **Check** looks the CNAME up (`net.Resolver.LookupCNAME`, swappable in
   tests). Pointing at the tenant's subdomain: the domain is `active` and
   routes to the tenant. Anything else: it stays `pending` and the screen says
   what the record answers instead, in the reader's language. The API answers
   it as a code and its names (`last_error_code`: `no_record`, `no_cname`,
   `points_elsewhere`, `wildcard_cname`, `dns_failed`; `last_error_params`:
   `domain`, `found`, `target`, `tenant_domain`, `detail`), and `last_error`
   keeps the English sentence for a log, a tool or a code a screen does not
   know. The suspension notice says it the same way, in each reader's
   language.
3. **One tenant at a time.** A domain another tenant has, in any state, is
   refused (`409 domain_taken`, never naming the holder); the operator can
   remove it from that tenant. A provider's `host`, the platform's own host,
   anything under the tenant domain, IP addresses and single-label names are
   refused outright.
4. **The periodic check** (every 6 hours) looks at every active and suspended
   domain. A definite answer that the CNAME no longer points at the tenant's
   subdomain (another target, no record) **suspends** the domain: it stops
   routing and stops being certified, the row is kept, and the tenant's
   administrators are notified. The next check that finds the CNAME back makes
   it active again. A lookup that fails (timeout, SERVFAIL) changes nothing:
   a resolver's bad minute must not take a tenant's address away.
5. ⚠ A CNAME cannot sit on a bare apex domain (`acme.example`) under the DNS
   rules, and providers that "flatten" one answer with addresses, not a CNAME.
   The screen says to use a subdomain (`files.acme.example`).

### Routing an own domain

`GetProviderByHost` matches a provider's `host`, the platform subdomain **or**
an active own domain, so every reader of the request host (tenant resolution,
the realm rule, the capabilities' tenant block, cookie domain, OIDC redirect)
treats them alike. Two rules move with it:

- links and mail (`tenanturl`) are minted on the tenant's `host` when it has
  one, as before. A tenant without one gets its links on the address the
  request arrived on when that is its platform subdomain or an active own
  domain (compared with the data, never echoed), else on its first active
  own domain, else on its platform subdomain;
- the session cookie's `Domain` (the tenant's `cookie_domain`, or the one
  derived from its `host`) is set only when the address the request arrived
  on is under it; on the platform subdomain or an own domain the cookie is
  that address's alone (a cookie scoped to another apex is rejected by the
  browser, and with it the sign-in).

### TLS: an installation setting, plus a tenant's own certificate

`FILEX_TLS_MODE` decides who issues certificates for the tenants' addresses:

- **`proxy`** (the default; filex serves plain HTTP behind a reverse proxy, as
  today). filex answers the proxy's questions:
  - `GET /api/tls/ask?domain=<name>`: `200` for an enabled tenant's `host`,
    platform subdomain or active own domain, `404` for anything else. Caddy's
    `on_demand_tls { ask ... }` calls it before it requests a certificate, so a
    stranger pointing a name at the platform gets none.
  - `GET /api/tls/certificate?server_name=<name>`: a tenant's own
    certificate (chain and key, PEM) for that name, or `204` when the tenant
    brought none, so the proxy issues its own. The shape of Caddy's
    `get_certificate http` hook (measured against Caddy before it is called
    built).
  - Both answer **the proxy itself** only (`clientip.ProxyItself`): a peer
    the sign-in limit trusts as a proxy (`FILEX_TRUSTED_PROXIES`), on a
    request that carries no `X-Forwarded-For`, `X-Real-IP` or `Forwarded`
    header. The same proxy forwards every stranger's request to filex, and
    those carry the headers, so they get `404`. The second hands out a
    private key: never to the internet. For good measure, answer `/api/tls/*`
    with a 404 in the proxy's own site block.
  - ⚠ The default trusted list is every loopback, private and link-local
    address, so anything that reaches filex's own port directly from the
    private network counts as the proxy. Keep that port unpublished (the
    proxy reaches it on the container network), or set
    `FILEX_TRUSTED_PROXIES` to the proxy's own address.
  - Proxies without such hooks (Traefik, nginx, an Ingress) issue certificates
    per host as they do today; the screen says the certificate is the proxy's.

  ```caddyfile
  {
      on_demand_tls {
          ask http://filex:5212/api/tls/ask
      }
  }
  https:// {
      tls {
          on_demand
          get_certificate http http://filex:5212/api/tls/certificate
      }
      reverse_proxy filex:5212
  }
  ```

- **`acme`**: filex terminates TLS itself on `FILEX_TLS_LISTEN` (default
  `:443`, plus `:80` for the HTTP-01 challenge and the redirect to HTTPS) and
  issues Let's Encrypt certificates (`x/crypto/acme/autocert`, cache in the
  data directory, `FILEX_TLS_ACME_EMAIL`). Its host policy is the same
  question as `/api/tls/ask`. A tenant's own certificate is served first for
  its domain (SNI), before ACME is asked. When the authority cannot validate
  an address, the log says what it answered: `tls: the ACME authority could
  not validate an address`, with the address, the challenge and the
  authority's words (`DNS problem: NXDOMAIN looking up A for ...`); the TLS
  handshake error beside it (autocert's) says only `no viable challenge type
  found`. An authority that finishes an order asynchronously and answers the
  finalize request without a `Location` header (Pebble, EJBCA; RFC 8555 does
  not require one there) is polled at the order's own address, where
  x/crypto's client alone would ask an empty one and never fetch the
  certificate. Let's Encrypt sets the header.

  The tenant screen's certificate column says what filex's own ACME last did
  for each own domain: **not obtained yet** (no TLS handshake has asked for
  it), **until `<date>`** (obtained), or **could not be obtained
  (`<time>`)** with the authority's reason quoted under it (`DNS problem:
  ...`, `Connection refused`; the error the attempt ended on when the
  authority said nothing, such as an untrusted directory certificate). The
  API gives it as `acme` on each domain of `GET /api/admin/tenant`
  (`state`: `none`, `obtained`, `failed`; `not_after`, `reason`, `at`). ⚠ It is the memory of the filex
  process that answered, not stored: after a restart every domain says "not
  obtained yet" until its next handshake, and behind several replicas each
  replica knows only the handshakes it served, so one replica may say "not
  obtained yet" for a domain another has a certificate for.

**A tenant's own certificate** (bring your own): certificate and key in PEM on
the domain's row, the key sealed. filex checks on upload that the key matches,
that the certificate names the domain, and that it has not expired; the screen
warns 14 days before `tls_not_after`. Without one, the installation's way
issues the certificate.

⚠ **FTPS** presents one certificate (`FILEX_FTPS_CERT_FILE`, or a self-signed
pair): a client that checks the name will refuse a tenant's address on FTPS.
SFTP and WebDAV are unaffected (WebDAV goes through the proxy, or through
filex's own TLS in `acme` mode).

## The tenant screen

**Admin → Tenants** (the platform operator's; an administrator of a tenant is
refused like the API refuses them, and the menu shows it only to the operator
of a multi-tenant install). One table (the shared `DataTable`, remembered as
`admin.tenants`): name, realm, address, sign-in, storages, people, state.

**Admin → My tenant** is the same for a tenant's administrator, for their own
tenant: its own OIDC and LDAP (`/api/admin/tenant/auth-providers`), the
platform's providers that serve it (named, never configured), and its
addresses and own domains (`/api/admin/tenant/domains`). The operator sees
exactly that section on every tenant's page (one component,
`TenantSelfService`), plus the insecure switch. Own domains are one more table
(`admin.tenant.domains`).

- **New tenant**: a name and a slug; the **realm is suggested from the slug**
  (`GET /api/admin/providers/realm-suggestion`, the server's one rule,
  `tenant.SuggestRealm`: marks dropped, `Müşteri` → `musteri`, the first free
  variant `acme-2` when `acme` is taken) and may be edited until the tenant is
  created. Refusals are the API's (`realm_invalid`, `realm_reserved`,
  `realm_taken`, `slug_taken`) shown on the field.
- **A tenant's page**: name, slug, address (`host`), cookie domain, the
  tenant's own OIDC (the provider row's, with **Trust this provider's email
  addresses**, `oidc_trust_email`:
  [SSO.md](SSO.md#trust-this-providers-email-addresses)), suspend / resume, storages
  (link / unlink), delete. The **realm is read-only**, and the page says why: it
  is part of the tenant's account addresses (`alex@acme.local`) and of the user
  names its people saved in their clients (`acme/alex`), so changing it would
  strand both.
- The platform's own tenant has no realm and cannot be suspended or deleted;
  the page says so instead of offering it.
- **Two tenants never share an address or a slug**: `providers.host` has no
  unique index (migration 00014) and `GetProviderByHost` answers one row, so
  the API refuses the second (`409 host_taken`, `409 slug_taken`), a suspended
  tenant's address included.

### MCP tools

`admin_tenants_list`, `admin_tenants_get`, `admin_tenants_suggest_realm`,
`admin_tenants_create`, `admin_tenants_update`, `admin_tenants_delete` (`force`
deletes the tenant's accounts too), `admin_tenants_link_storage`,
`admin_tenants_unlink_storage`. They run the same handler as the screen
(`/api/ai/admin/providers`), with the same guards: platform operator only,
realm given at creation and never a field afterwards, the platform's tenant
never deleted or suspended, every write audited (`ai.providers.*`), a demo's
writes refused by the handler itself (the in-process call never passes
`api.DemoGuard`). Moving the platform flag (`is_supertenant`) has no tool.

## What this does not change

- A single-tenant install signs in exactly as before; nothing here is read.
- The realm rule ([MULTI-TENANCY.md, Realms](MULTI-TENANCY.md#realms-which-tenant-a-sign-in-is-for))
  is unchanged: the bindings decide **which providers** judge a sign-in, the
  realm still decides **which tenant** it is for.
- The handoff ticket is unchanged; the host-less OIDC callback is one more
  issuer of `login_handoff` tickets.
