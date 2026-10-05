# Single sign-on (OIDC / OAuth2)

filex can delegate login to any **OpenID Connect**-compliant identity provider -
Keycloak, Authentik, Auth0, Okta, Dex, Google, Azure AD, … Users sign in at your
IdP and land in filex already authenticated; accounts are created on first login,
unless `auto_create` is off or `allowed_groups` does not match
([the first sign-in rule](LDAP.md#the-first-sign-in-rule-who-gets-an-account)).

SSO is **optional and additive**: you can run local password login, OIDC, or
**both at once** (a "Sign in with …" button next to the password form).

---

## How it works

```
 Browser                     filex                         IdP (Keycloak/…)
   │  GET /api/auth/oidc/start ─►│                              │
   │  ◄──────── 302 redirect ──── │ ──► authorization endpoint ─►│
   │                                                            │  (user logs in)
   │  ◄──────────────── 302 back to redirect_url ──────────────│
   │  GET /api/auth/oidc/callback?code=…&state=… ─►│            │
   │                             │ ── code exchange ───────────►│
   │                             │ ◄── id_token ───────────────│
   │                             │  verify signature + claims   │
   │                             │  upsert user, map role       │
   │  ◄── session cookie ────────│                              │
```

1. filex redirects the browser to the IdP with `openid profile email` scopes and
   a random `state` (stored in a short-lived cookie).
2. The IdP authenticates the user and redirects back to filex's `redirect_url`.
3. filex exchanges the code, **verifies the `id_token`** against the provider's
   keys, reads the `email` claim (required), finds the account - inside the
   tenant the sign-in is for, by the SSO identity it is bound to, else by the
   address ([Which account an SSO sign-in opens](#which-account-an-sso-sign-in-opens))
   - or opens one, maps a role, and mints a normal **12-hour session cookie**.

After the callback, filex uses its own session - the IdP is only involved at login.

---

## Which account an SSO sign-in opens

Since v0.50.0 these rules decide it, for every OIDC provider: the environment's,
the ones on **Admin → Identity providers**, a tenant's own and a tenant's OIDC
on its row. Before 0.50 an OIDC sign-in found its account by the email
address alone, on a multi-tenant install across **every** tenant when the
provider was not a tenant's row OIDC, never read `email_verified`, and never
compared the `sub` it kept (the security fix in the
[changelog](../CHANGELOG.md), 0.50.0).

1. **One tenant.** A sign-in is for one tenant: the one its flow was started
   for (`/api/auth/oidc/start?instance=&realm=`, [TENANT-ADMIN.md](TENANT-ADMIN.md)),
   the platform's own when no realm or address names another, and on a
   single-tenant install the only one there is. Every account lookup stays
   inside it, and an account of another tenant is never signed in to - the
   meaning a password sign-in's realm has. A tenant's own OIDC signs people in
   to its tenant only. On a multi-tenant install a callback that comes back
   without the flow cookie is refused (`expired`: start again); it used to be
   answered by the first OIDC the server had.
2. **The identity the account is bound to.** The first sign-in that opens or
   finds an account binds it to the identity that signed in: the provider's
   issuer (`iss`) and the person's `sub`. Every later sign-in finds the account
   by that pair, whatever address the provider sends then and whether or not
   it says the address is verified.
3. **Otherwise the address**, inside the tenant:
   - an account bound to **another** identity is refused
     (`identity_mismatch`) - the same address, somebody else, or the same
     person after the identity provider gave them a new identity or moved to a
     new issuer address. An administrator can
     [remove the account's SSO bind](#removing-an-accounts-sso-bind);
   - an account that kept only a `sub` before 0.50 (multi-tenant installs) is
     the same person when the `sub` matches: its issuer is filled in;
   - an account bound to nobody yet is signed in to, and bound, only when the
     provider marks the address **`email_verified: true`** - or when the
     operator trusts the provider's addresses (below). Otherwise it is refused
     (`email_unverified`) and left as it was.
4. **No account:** [the first sign-in rule](LDAP.md#the-first-sign-in-rule-who-gets-an-account)
   decides, and the new account is bound at once. When the provider did not
   mark the address verified, the account is opened **switched off**: the
   person reads `account_pending`, **Admin → Users** shows it *Waiting for
   approval*, the audit has `auth.account_pending`, and switching the account
   on (the user's page, or `PATCH /api/admin/users/{id}` `{"enabled": true}`)
   approves it.

`email_verified` counts only when it says `true` (a JSON boolean, or the
string `"true"` some providers send). `false`, a missing claim or anything else
is "not verified".

### Trust this provider's email addresses

Many providers never send `email_verified` (Microsoft Entra ID among them), and
some send `false` for addresses an administrator typed in (accounts imported
from a directory into Keycloak). For such a provider, switch on **Trust this
provider's email addresses** - `trust_email` on the provider's card (Admin →
Identity providers, Admin → My tenant), `oidc_trust_email` on a tenant's own
OIDC on its row (Admin → Tenants), `FILEX_OIDC_TRUST_EMAIL` for the
environment's OIDC. Every address the provider sends then counts as verified.

⚠ Switch it on only for a provider whose addresses all belong to the people who
hold them. Anybody who can set an address at that provider - self-registration
without a confirmation mail, a profile field people may edit - can then sign in
to the filex account with that address, as long as the account is not yet
bound to another identity. Rules 1 and 2 hold whatever this setting says.

**Off for a provider made from 0.50 on. On, after the upgrade, for the
providers that already existed** (the owner's decision: an upgrade locks nobody
out). At the first start of 0.50 on a database that already had accounts,
filex switches it on for the page's OIDC, every further OIDC instance and every
tenant's own OIDC, and their cards say *The upgrade switched this on so that
this provider works as it did before. Switch it off if the provider sends
email_verified.* until somebody saves the provider. The environment's OIDC
follows `FILEX_OIDC_TRUST_EMAIL` when it is set; when it is not, filex answered
once, at that first start, and kept the answer: on when the database had
accounts that came through SSO (an account with no password of its own, or
with a kept `sub`), off otherwise - and off on a fresh install. The card of the
environment's OIDC says the same note then. Changing the setting on the page is
audited (`changed_fields`, `oidc_trust_email_before` / `_after`).

### Removing an account's SSO bind

**Admin → Users →** the person **→ Remove SSO bind** (after a question), or
`PATCH /api/admin/users/{id}` `{"sso_unlink": true}` (also MCP
`admin_users_update`). The account's next SSO sign-in is matched by its address
again, as a first sign-in is (rule 3, verified address or trusted provider). Use
it when the identity provider gave the person a new identity, or when the
provider moved to a new issuer address (a renamed Keycloak realm): until then
every bound account of that provider answers `identity_mismatch`. A tenant's
administrator does it for the accounts of their own tenant; an administrator's
bind needs a sign-in in a browser session, like setting an administrator's
password. The request's audit row says `sso_unlinked`.

## Prerequisites

- A running OIDC provider you control (or a hosted one).
- filex reachable over HTTPS at a stable `FILEX_PUBLIC_URL`.
- A client/application registered in the IdP for filex (see below).

---

## Setup

### 1. Register filex as a client in your IdP

Create a **confidential** OIDC client with:

- **Redirect URI:** `https://files.example.com/api/auth/oidc/callback`
  (exactly `FILEX_PUBLIC_URL` + `/api/auth/oidc/callback`).
- **Post-logout redirect URIs:** `https://files.example.com/admin/login?signed_out=1`
  and `https://files.example.com/drive/login?signed_out=1` - or simply
  `https://files.example.com/*`. Where the IdP sends the browser back after
  [signing out](#signing-out); without them sign-out ends on the IdP's "invalid
  redirect URI" page.
- **Under a sub-path** ([`FILEX_BASE_PATH`](CONFIGURATION.md#base-path)) every
  one of these carries the base:
  `https://example.com/filex/api/auth/oidc/callback`,
  `https://example.com/filex/admin/login?signed_out=1`, … - the public URL
  already ends with it, so "`FILEX_PUBLIC_URL` + …" still holds.
- **Grant type:** Authorization Code (standard flow).
- **Client authentication:** on (you'll get a client secret).

Note the **issuer URL**, **client ID**, and **client secret**.

> **Keycloak:** the issuer is `https://id.example.com/realms/<realm>`. Create the
> client under that realm, enable "Client authentication", set the redirect URI,
> and copy the secret from the **Credentials** tab. Put the post-logout URIs in
> **Valid post logout redirect URIs** - left empty, Keycloak allows only the
> *Valid redirect URIs*, which for filex is the callback alone. **Service
> accounts** can stay off: the test asks the token endpoint a client-credentials
> question, and Keycloak's answer for a client without service accounts (`401
> unauthorized_client`) already says the client ID and secret are right; a
> wrong secret is `401 invalid_client` and fails the test.

### 2. Configure filex

```bash
# Enable the driver(s). Keep `local` too if you still want password login.
FILEX_AUTH_DRIVERS=local,oidc

FILEX_OIDC_ISSUER=https://id.example.com/realms/myrealm
FILEX_OIDC_CLIENT_ID=filex
FILEX_OIDC_CLIENT_SECRET=<client-secret-from-idp>
FILEX_OIDC_REDIRECT_URL=https://files.example.com/api/auth/oidc/callback

# Optional - admin mapping (see "Roles & admin access")
FILEX_OIDC_ROLE_CLAIM=realm_access.roles
FILEX_OIDC_ADMIN_GROUP=filex-admin
```

filex discovers the provider's endpoints and keys from
`<issuer>/.well-known/openid-configuration` at startup.

### 3. Sign in

A **Sign in with SSO** button appears on the login page (when `oidc` is in
`FILEX_AUTH_DRIVERS`). It sends the browser to `/api/auth/oidc/start`.

Its label is yours to set: **Admin → Branding → SSO button label** (the setting
`branding.sso_label`, up to 60 characters; a tenant admin sets their own). Empty
keeps the default, which names no provider. The button takes the branding
**accent colour** and stays visible whatever that colour is: its label is picked
from the accent, and in either theme it draws an edge when the accent would
otherwise blend into the sign-in card (a black accent on the dark theme, a white
one on the light theme).

---

## Zero-touch / env-driven setup

OIDC is configured **entirely from environment variables** - there are no
admin-UI clicks to enable SSO. A container/Helm deployment that ships these vars
comes up with SSO already live:

```bash
FILEX_AUTH_DRIVERS=local,oidc
FILEX_OIDC_ISSUER=https://id.example.com/realms/myrealm
FILEX_OIDC_CLIENT_ID=filex
FILEX_OIDC_CLIENT_SECRET=<client-secret-from-idp>
FILEX_OIDC_ROLE_CLAIM=realm_access.roles      # optional (admin mapping)
FILEX_OIDC_ADMIN_GROUP=filex-admin            # optional (admin mapping)
```

`FILEX_OIDC_REDIRECT_URL` is **optional** - when omitted it defaults to
`FILEX_PUBLIC_URL` + `/api/auth/oidc/callback` (still register that exact URL in
your IdP). Set it explicitly only if your callback lives elsewhere.

**LDAP** and **proxy-header** auth are likewise env-drivable now
(`FILEX_LDAP_*` / `FILEX_HEADER_*`) - see
[CONFIGURATION.md → Authentication](CONFIGURATION.md#authentication). The admin
account, SMTP, branding and an initial storage can also be seeded from env on
first boot - see
[CONFIGURATION.md → Zero-touch seeding](CONFIGURATION.md#zero-touch-seeding).

---

## Roles & admin access

- An SSO account is created on its **first** login, with the **`user`** role -
  or **`admin`**, when the mapping below matches at that moment - unless
  `auto_create` is off or `allowed_groups` does not match
  ([the first sign-in rule](LDAP.md#the-first-sign-in-rule-who-gets-an-account)).
- To map admins from the IdP, set `FILEX_OIDC_ROLE_CLAIM` to the claim that
  carries the user's roles/groups and `FILEX_OIDC_ADMIN_GROUP` to the value that
  means "admin". filex reads that claim (string **or** array, in the ID token or
  the access token) **on every sign-in**, since 0.41.1:
  - someone added to the admin group becomes an admin at their next sign-in;
  - someone removed from it goes back to the `user` role at their next sign-in;
  - a role set by hand **below** admin (`viewer`) is left alone unless the group
    now grants admin - the mapping owns the admin role and nothing else.
- ⚠ Two accounts are **never demoted** by the mapping, because demoting either
  could leave nobody able to administer filex: the account filex was set up with
  (the one the [recovery sign-in](#the-identity-provider-is-down-and-nobody-can-sign-in) admits) and the **last** admin.
  Each such sign-in logs a `WARN` naming the account.
- The change applies at sign-in, not instantly: an admin removed in the IdP keeps
  a session they already hold until it ends. Disable the account in
  **Admin → Users** to cut access at once.
- Before 0.41.1 the claim was read only when the account was created.
- Example (Keycloak realm roles): `FILEX_OIDC_ROLE_CLAIM=realm_access.roles`,
  `FILEX_OIDC_ADMIN_GROUP=filex-admin`, then assign the `filex-admin` realm role
  to the users who should administer filex.
- A **custom role** can name SSO groups: a **new** account whose groups claim
  (the same `FILEX_OIDC_ROLE_CLAIM`) carries one of them starts with that role
  instead of the built-in User - at creation only; afterwards the role is
  changed on the person's page, and a later sign-in neither gives it back nor
  takes it away ([PERMISSIONS.md → Starting role for SSO groups](PERMISSIONS.md#starting-role-for-sso-groups)).
  filex stores each sign-in's groups for that purpose.
- Per-file/folder access is governed separately by [RBAC](RBAC.md), and what an
  account may do by its role and exceptions ([PERMISSIONS.md](PERMISSIONS.md));
  SSO decides the admin role and the linked filex groups at every sign-in
  ([GROUPS.md](GROUPS.md#members-and-sso-links)), and a custom starting role once.

> SSO accounts have **no local password** (they authenticate via the IdP). If
> you later disable OIDC, give those users a password first (admin → reset) or
> they won't be able to log in.

---

## Signing out

An SSO session has two halves: filex's own session and the IdP's. **Sign out**
ends both (OpenID Connect RP-Initiated Logout 1.0):

1. filex deletes its session and clears the cookie, as always;
2. `POST /api/auth/logout` answers with the IdP's end-session URL
   (`logout_url`) - its `end_session_endpoint` from discovery, with the
   `id_token_hint` kept from sign-in, `client_id`, and
   `post_logout_redirect_uri`;
3. the web app sends the browser there; the IdP ends its session and sends the
   browser back to the sign-in page of the front door it came from
   (`/admin/login?signed_out=1` or `/drive/login?signed_out=1`);
4. that page says "You are signed out." and does **not** start SSO by itself,
   even with `FILEX_OIDC_AUTO_REDIRECT` - whoever signs in next picks the
   account.

Why both: ending only filex's half was not signing out. With
`FILEX_OIDC_AUTO_REDIRECT` the sign-in page went straight back to the IdP, whose
session was still open, and the IdP issued a new code without a form - the same
account was signed in again half a second later, and on a shared computer the
next person got the previous one's files.

Sign-out stays filex-only when:

- `FILEX_OIDC_LOGOUT=local` - the operator wants people to stay signed in at
  the IdP (other apps on the same SSO keep working);
- the IdP's discovery document has no `end_session_endpoint`;
- the session did not come from SSO (password, LDAP), or was signed in before
  the upgrade that added this (no id_token was kept; it expires within 12 h).

The id_token is kept on the session row (`sessions.id_token`) only when the IdP
can end sessions, and leaves with the session. It is never handed to a
different IdP: one whose `iss` does not match the tenant's issuer is ignored.

Multi-tenant: the end-session URL is the tenant's own IdP, resolved from the
request host exactly like sign-in, and each tenant's client needs its own
post-logout redirect URIs.

The IdP is the one filex runs **now**: an OIDC provider configured or changed
on the [Identity providers page](#managing-providers-on-the-identity-providers-page)
takes effect without a restart for sign-out exactly as for sign-in. A session
signed in through a provider that has since been replaced keeps the old
issuer's id_token, which is not handed to the new one - that session signs out
of filex only.

---

## Configuration reference

| Env var | Required | Description |
|---|---|---|
| `FILEX_AUTH_DRIVERS` | yes | Comma list, e.g. `local,oidc`. Include `oidc` to enable SSO. `oidc` alone is SSO-only: password sign-in is off, and a session from the identity provider is honoured like any other. (Before v0.41.0 leaving `local` out refused every session, see #24.) |
| `FILEX_OIDC_ISSUER` | yes | IdP issuer URL (has `/.well-known/openid-configuration`). |
| `FILEX_OIDC_CLIENT_ID` | yes | Client/application ID registered in the IdP. |
| `FILEX_OIDC_CLIENT_SECRET` | yes* | Client secret (confidential client). |
| `FILEX_OIDC_REDIRECT_URL` | no | Defaults to `FILEX_PUBLIC_URL` + `/api/auth/oidc/callback`. Whatever it resolves to must match the IdP exactly. |
| `FILEX_OIDC_AUTO_REDIRECT` | no | `true` makes the login page start the OIDC flow straight away instead of showing the password form; `?local=1` still reaches the form. See [CONFIGURATION.md](CONFIGURATION.md#authentication). |
| `FILEX_OIDC_LOGOUT` | no | What **Sign out** ends: `idp` (default) - filex's session and the IdP's, when the IdP supports it; `local` - filex's session only. See [Signing out](#signing-out). |
| `FILEX_OIDC_ROLE_CLAIM` | no | Claim holding roles/groups (string or array). Also what filex groups linked to SSO groups are matched against at every sign-in ([GROUPS.md](GROUPS.md#members-and-sso-links)). |
| `FILEX_OIDC_ADMIN_GROUP` | no | Value within that claim that elevates a user to admin. |
| `FILEX_OIDC_AUTO_CREATE` | no | `false` opens no account at a person's first sign-in: only people who already have one get in. Default `true`. See [the first sign-in rule](LDAP.md#the-first-sign-in-rule-who-gets-an-account). |
| `FILEX_OIDC_ALLOWED_GROUPS` | no | Comma list of groups (from the `FILEX_OIDC_ROLE_CLAIM` claim): only their members get an account on the first sign-in. Needs the role claim set. |
| `FILEX_OIDC_TRUST_EMAIL` | no | `true` takes every address the provider sends as verified, `email_verified` or not. Unset: the answer the upgrade to 0.50 took once (on when the database had accounts that came through SSO), off on a fresh install. See [Trust this provider's email addresses](#trust-this-providers-email-addresses). |

The legacy `FILEX_AUTH_OIDC_*` prefix is also accepted for all OIDC keys.
Requested scopes are always `openid profile email`.

---

## When an SSO sign-in is refused

The browser comes back to the sign-in page, and since v0.50.0 the page says
**why** and what to do, in the reader's language. The server sends a reason
**code** with the redirect (`/admin/login?error=oidc&reason=<code>`), never a
sentence and never the identity provider's own error text: whatever the IdP or
a hand-edited callback address says stays in the server log. A code the page
does not know reads as the generic sentence. The SSO-first redirect
(`FILEX_OIDC_AUTO_REDIRECT`) does not start again on any of these pages, so a
refusal cannot loop.

| What happened | Code | The page says |
|---|---|---|
| No account yet, and the provider opens none at a first sign-in (`auto_create` off, [the first sign-in rule](LDAP.md#the-first-sign-in-rule-who-gets-an-account)) | `auto_create_off` | This server does not open an account at a first SSO sign-in. Ask your administrator to open your account. |
| No account yet, and none of the person's groups is in `allowed_groups` | `group_not_allowed` | Your account is not in a group that may sign in here. Ask your administrator for access. |
| The id_token carries no `email` claim | `no_email` | Your identity provider did not send an email address, and your account is found by it. Ask your administrator to check the provider's settings. |
| The IdP sent the browser back with an `error` instead of a code (the person cancelled, the IdP refused them) | `idp_denied` | The sign-in was cancelled or refused at your identity provider. Try again; if it keeps happening, contact your administrator. |
| The code exchange or the id_token check failed (client secret, clock skew, a code the IdP did not issue) | `idp_error` | The sign-in could not be completed with your identity provider. Try again; if it keeps happening, contact your administrator. |
| The browser came back without this sign-in's `state` cookie (too slow, another tab, cookies stripped) | `expired` | The SSO sign-in took too long, or it was started in another tab or browser. Try again. |
| The account is disabled | `account_disabled` | Your account is disabled. Contact your administrator. |
| The account's tenant is suspended (`?maintenance=1&reason=`) | `tenant_suspended` | Your organization's access to this server is suspended. Contact your administrator. |
| Multi-tenant mode is off on an install with tenants ([maintenance mode](MULTI-TENANCY.md)) | `maintenance` | Sign-in is temporarily limited to the platform operator. Try again later. |
| Too many SSO sign-ins in flight on the server (a start refused) | `busy` | Too many sign-ins are in progress on this server. Try again in a few minutes. |
| The account with this address (in the sign-in's tenant) is bound to no SSO identity yet, and the provider did not mark the address verified ([Which account an SSO sign-in opens](#which-account-an-sso-sign-in-opens)) | `email_unverified` | Your identity provider did not confirm that your email address is verified, so your account is not opened by the address alone. Contact your administrator. |
| A first sign-in with an unverified address: the account was opened switched off, waiting for an administrator; also its later sign-ins until then | `account_pending` | Your account was opened and is waiting for your administrator's approval. Sign in again once it is approved. |
| The account with this address (in the sign-in's tenant) is bound to another SSO identity | `identity_mismatch` | This account is bound to another SSO identity. Contact your administrator. |
| On a multi-tenant install, the callback came back without the sign-in's flow cookie | `expired` | (the `expired` sentence above) |
| **The e-mail address has an account in another tenant** - the sign-in is for one tenant, the account is another's | none | SSO sign-in failed. Try again, or sign in with password. |
| A realm nobody has, an SSO the realm does not have, or one unbound from it during the sign-in | none | the same generic sentence |
| Anything else (a database error, an SSO that could not start) | none | the same generic sentence |

**Why one refusal stays vague.** An e-mail address has one account on an
install, in one tenant ([MULTI-TENANCY.md](MULTI-TENANCY.md)). Saying "this
address is registered to another tenant" would tell anybody who holds an
identity at a shared identity provider that another tenant exists and that the
address belongs to it. So that refusal gets the answer of a failure with no
reason at all: the same address, the same status, the same page - a realm
nobody has answers exactly the same, and for the same reason. The other codes
tell the person nothing about anybody else: the identity provider has already
said who they are, and the answer concerns their own sign-in.

The operator always gets the whole story. Every refusal is logged at WARN as
`oidc callback failed` (or `oidc sign-in refused` for the account and tenant
gates) with the error and the `reason` the page was sent - empty for the vague
ones, whose error says what really happened (`oidc: this email is registered to
another tenant: ...`, `oidc: account 12 is not in tenant 3, the one this
sign-in is for`). A first-login refusal also writes the audit row
`auth.first_login_refused`; an account opened waiting for approval,
`auth.account_pending`.

`email_unverified`, `account_pending` and `identity_mismatch` are said only
about an account of the tenant the sign-in is for, so they tell nothing about
another tenant either.

The same sentences reach the sign-in page two other ways
([the first sign-in rule](LDAP.md#the-first-sign-in-rule-who-gets-an-account)):

- **The header proxy.** When the proxy names a person the first sign-in rule
  refuses, the 401 of `/api/auth/me` (and of every other request) carries the
  code, `{"error":"unauthorized","reason":"auto_create_off"}`, and the page
  says it instead of starting SSO. A disabled account's 403 carries
  `account_disabled`.
- **The password form**, for LDAP, PAM and Windows, only when the provider's
  `show_refusal_reason` is on (off by default): a refusal that comes after a
  right password answers `403 {"error":"sign-in refused","reason":...}`. Off,
  it is answered like a wrong password, because a different answer would
  confirm the password to anybody guessing. A Windows system account the SID
  named after the password has its own code, `forbidden_account` ("This is a
  system or built-in administrator account, and those may not sign in here.
  Sign in with your own account."). The form's disabled-account and
  suspended-tenant answers carry `account_disabled` / `tenant_suspended` /
  `maintenance` too.

## What happens if it isn't configured

filex falls back to whatever else is in `FILEX_AUTH_DRIVERS` (usually `local`
password login). With no OIDC config there is simply no SSO button - nothing
else changes.

---

## Failure modes & troubleshooting

The sign-in page names most of these to the person
([When an SSO sign-in is refused](#when-an-sso-sign-in-is-refused)); the log
lines below are what the operator reads.

### No SSO button / `oidc: SSO disabled until restart` in the log
The issuer is wrong or unreachable. filex calls
`<issuer>/.well-known/openid-configuration` at startup and retries for about a
minute (an IdP booting in the same compose file is the common case). If it still
fails, filex **starts anyway without SSO** - password login keeps working - and
logs the error at ERROR followed by `oidc: SSO disabled until restart`. Nothing
retries after that: fix the issuer and restart. Verify `FILEX_OIDC_ISSUER` (for
Keycloak it **includes** `/realms/<realm>`, no trailing slash) and that filex's
network can reach the IdP.

That is the environment's OIDC. One configured on **Admin → Identity
providers** is re-tried in the background after a start (for about two
minutes) and shows on its card why it is not running; a save on the page tries
again at once.

### The identity provider is down and nobody can sign in

With `local` in `FILEX_AUTH_DRIVERS`, every local account still signs in with
its password. With SSO **only** (`FILEX_AUTH_DRIVERS=oidc`), the administrator
filex created at installation - `admin@local`, or the account
`FILEX_ADMIN_EMAIL` named - can still get in: open the login page, choose
**Administrator recovery sign-in** (`/admin/login?local=1`) and use that
account's password. No other account can, so password sign-in stays off for
everyone else; two-factor still applies, and the log records each such
sign-in at WARN (`auth: recovery sign-in as the bootstrap administrator`).

The [sign-in attempt limit](CONFIGURATION.md#sign-in-attempt-limits) applies
to the recovery sign-in too: no account is exempt, the bootstrap administrator
included, so once somebody guessing its password has locked it, even the right
password is refused until the lock runs out (15 minutes at most by default).
The way in while it lasts is an address on the IP allow-list
(`login.ip_allowlist`), which may sign in to a locked account; an administrator
who is signed in can also lift it with *Unlock* on **Admin → Sign-in
security**. Put your office or VPN address on the list before you need it.

On an installation older than v0.41.0 the account is worked out once at
startup: the oldest administrator that has a local password. If that account
is later deleted, recovery lets nobody in - it is not handed to another
administrator. Turn the whole thing off with `FILEX_AUTH_RECOVERY_LOGIN=false`.

### "state mismatch" after login
The `state` cookie didn't survive the round trip. Usually a cookie/proxy issue:
serve filex over HTTPS (the state cookie is `Secure` under TLS), don't strip
cookies at the proxy, and make sure the browser returns to the **same** host it
started on.

### "code exchange" / "verify id_token" error
Client ID/secret mismatch, or a clock skew between filex and the IdP. Re-check
`FILEX_OIDC_CLIENT_ID` / `FILEX_OIDC_CLIENT_SECRET`, and confirm the IdP client
is a **confidential** client using the authorization-code flow.

### "id_token missing email claim"
filex keys accounts off `email`. Configure the IdP client to include the `email`
claim (and scope) in the ID token. In Keycloak that's the default `email` client
scope - make sure it's assigned and the user has an email.

### Redirect fails / "invalid redirect_uri"
The IdP's registered redirect URI must equal `FILEX_OIDC_REDIRECT_URL` **exactly**
(scheme, host, path). Update the client in the IdP or the env var so they match.

### Sign-out ends on the IdP's "invalid redirect URI" page
The IdP does not allow the post-logout redirect. Add
`https://<host>/admin/login?signed_out=1` and `https://<host>/drive/login?signed_out=1`
(or `https://<host>/*`) to the client's post-logout redirect URIs - on Keycloak,
**Valid post logout redirect URIs**. Or set `FILEX_OIDC_LOGOUT=local` to keep
sign-out inside filex.

### Signing out and back in lands on the same account without a form
The IdP's session survived the sign-out. Check that the IdP advertises
`end_session_endpoint` in `/.well-known/openid-configuration` and that
`FILEX_OIDC_LOGOUT` is not `local`. A session signed in before the upgrade has
no id_token to end the IdP session with - sign in again once. Until then the
sign-in page at least waits after a sign-out instead of starting SSO by itself.

### `email_unverified` or `account_pending` for everybody
The provider does not send `email_verified: true`. Check the id_token (jwt.io):
a provider that never sends the claim, or sends `false` for addresses its
administrators typed in, needs **Trust this provider's email addresses** -
only when its addresses all belong to their people
([why](#trust-this-providers-email-addresses)). Accounts already opened
switched off are approved by switching them on in **Admin → Users**.

### `identity_mismatch` after changing the identity provider
The accounts are bound to the identity they signed in with, issuer included,
and the new provider (or the renamed realm) is another issuer. Remove the bind
of each account ([Removing an account's SSO bind](#removing-an-accounts-sso-bind));
its next sign-in binds it to the new identity.

### User logs in but isn't admin
The mapping is applied at every sign-in, so the person has to sign in again after
being added to the group - an already open session keeps the role it started
with. If a fresh sign-in still lands as `user`, confirm `FILEX_OIDC_ROLE_CLAIM`
names the actual claim in the token (inspect it at jwt.io) and that
`FILEX_OIDC_ADMIN_GROUP` matches a value inside it. On 0.41.0 and older the claim
was read only at account creation; set the role in **Admin → Users** there.

---

## Other auth drivers

filex ships more than OIDC. Each is enabled by adding it to `FILEX_AUTH_DRIVERS`
or on **Admin → Identity providers** - except `local`, which is the
environment's alone, and the operating-system providers `windows` and `pam`,
which are the page's alone (an entry for one in `FILEX_AUTH_DRIVERS` is refused
at start with a warning, see [OS-LOGIN.md](OS-LOGIN.md)):

- **`local`** - built-in email/password with optional TOTP 2FA.
- **`ldap`** - bind against an LDAP/Active Directory server, on the same password
  form as `local`. See [LDAP.md](LDAP.md).
- **`pam`** - sign in with the Linux login of the machine filex runs on, judged by
  PAM. Page-managed only, and it cannot be switched on until its test - a real
  sign-in with an account you name - passes. See [OS-LOGIN.md](OS-LOGIN.md).
- **`proxy-header`** - trust an authenticating reverse proxy (e.g. oauth2-proxy,
  Authelia) that sets a user header. Only enable when the proxy is the **only**
  path to filex.
- **`windows`** - sign in with the Windows account of the machine filex runs on
  (local or domain), checked by the operating system. Switched on only by a
  test that signed a real account in. See [OS-LOGIN.md](OS-LOGIN.md#windows).

Drivers can be combined. For the ones that read a password - `local`, `ldap`,
`windows` and `pam` - the order is the order they are **tried**: the first to accept wins, so keep `local` first
and `admin@local` stays answerable while the directory is unreachable.

OIDC, LDAP, the proxy header and the operating-system providers (`windows`, `pam`)
can also be configured on **Admin → Identity providers**, without a restart - see the
next section.

## Managing providers on the Identity providers page

Since v0.43.0 the **Identity providers** page really manages sign-in. (Before
it, the page saved settings no server ever read: sign-in came from the
environment alone, and the page's "restart the server" changed nothing.)

What the page does:

- **One tab per kind of sign-in** - **LDAP**, **Local**, **OIDC**,
  **Reverse-proxy header**, **Windows** and **Linux (PAM)** - each with a dot
  that is green while one of its providers runs; the tab is kept in the
  address (`?tab=oidc`). API keys are not here: they are always accepted, and
  issued on the **API / MCP** page. **Add a provider** makes another of a
  kind (a second directory, an SSO for some tenants only), switched off, and
  opens its page.
- **Each provider is a card** saying whether it runs and what it reaches -
  an LDAP directory's server, base DN, e-mail domains, its last sync, its
  schedule and **Sync now**. A card opens the provider's own page
  (`/admin/auth-providers/<slug>`): **Settings** (the whole form - for LDAP in
  sections: Connection, People, Groups, Directory sync - or the environment's
  read-only view, with Test now and Save and apply, the tenants it serves and,
  for one made with Add a provider, Delete) and, for a directory, **Sync** (its
  schedule and last report).
- **OIDC, LDAP, the proxy header, Windows and Linux (PAM)** can be configured and switched on here.
  A save is applied at once - no restart - through exactly the code the
  environment's configuration goes through, and the login page offers a
  provider the moment it runs.
- **Every save runs the real test first** ("Test now": connect, bind, read the
  base DN, then count the people the user filter finds, whether their groups
  can be read, and how many groups directory sync would bring in - up to 1000
  of each; fetch the discovery document, check the issuer and ask the token
  endpoint about the client). Switching a provider **on** while its test fails
  needs a confirmation that names the steps that failed; a provider saved
  **off** saves whatever its test says. The exception is an
  **operating-system provider** (`windows`, `pam`): its test signs a real account in
  (`test_account` on the request, used once and never stored), it must **pass**,
  no confirmation overrides it, and the account that passed becomes a super
  administrator ([OS-LOGIN.md](OS-LOGIN.md#switching-it-on)). Its card asks for
  that account in a **Test account** box beside **Test now** and **Save and
  apply**, empties the password box as soon as the request is answered, and
  shows the commands of a failed step as code to copy. A provider that is on but cannot start
  says so on its card, with the reason, and is simply not offered - it takes
  nothing else down with it.
- **Secrets** (the OIDC client secret, the LDAP bind password) are stored
  sealed with `FILEX_SECRET_KEY` - the same sealing the app settings use - and
  never sent back to the browser: the box says *set - type a new one to
  replace it*. Without `FILEX_SECRET_KEY` a secret cannot be stored at all (the
  page says so), and none is ever logged.
- **Every change is audited** (`auth_provider.update`): who, which provider,
  on/off before and after, the *names* of the fields that changed, and - when
  a failing test was confirmed - which steps failed. Never a value.
- An OIDC card has **Trust this provider's email addresses** (`trust_email`,
  off for a provider added on 0.50; [what it does](#trust-this-providers-email-addresses)),
  with the upgrade's note while the value is the one the upgrade to 0.50 set.
- **Instance-wide.** Only the platform operator (a supertenant administrator)
  sees or changes it; a tenant administrator gets `403 supertenant_only`, reads
  included - the instance's sign-in, and its secrets, are not a tenant's. A
  tenant's own sign-in lives on its provider row, and since 0.50 also on
  Admin → My tenant (its own OIDC and LDAP).
- **More than one of a kind, and per tenant** (0.50,
  [TENANT-ADMIN.md](TENANT-ADMIN.md#sign-in-providers-bound-to-tenants)).
  **Add a provider** makes another OIDC, LDAP, ... next to the first (its own
  name, its own label on the sign-in page), switched off. On a multi-tenant
  install each card says which tenants sign in through it; the ticks are the
  bindings. ⚠ The upgrade bound every provider that existed to every tenant
  that existed (what happened before), and the page says so until you have
  reviewed them: remove the tenants a provider should not serve. A provider
  added afterwards serves the platform's own tenant until you tick more. Every
  OIDC a tenant can use is its own button on its sign-in page, also on the
  platform's address once the Realm is typed.

### The environment wins, visibly

A provider listed in `FILEX_AUTH_DRIVERS` (or `auth.drivers` in the config
file) is **configured by the environment**: the page shows it read-only, with
where it is defined - `FILEX_AUTH_DRIVERS`, the config file's path, or the
built-in default - and refuses a change (`409 environment_managed`). If the
page also holds a configuration under the same name, it is kept but not used,
and the card says so. There is never a silent conflict: the environment's is
the one that runs. The operating-system providers (`windows`, `pam`) are the
exception: the environment cannot name them at all - the entry is refused at
start with a warning that points at this page, and the rest of the list starts.

Providers switched on here are **added after** the environment's. A directory
configured on the page is therefore never tried before `local`, so the
administrator's password is judged before any network round trip.

### Why nothing on this page can lock you out

- **Password sign-in and the recovery sign-in are the environment's.** The
  page has no switch for `local`, and none for the installation
  administrator's recovery sign-in (`FILEX_AUTH_RECOVERY_LOGIN`, on by default
  when `local` is not enabled). Changing either needs access to the host -
  which is exactly the person who can repair a broken identity provider. That
  is the break-glass, and it is deliberately out of the page's reach: the page
  is where an identity provider gets broken, so it must not also be where the
  way back is switched off.
- **The last way in stays on.** Switching off a provider is refused
  (`409 last_sign_in_method`) when it is the last way an administrator can sign
  in: no password sign-in an administrator can use (every administrator came
  in through SSO and has no password), no recovery sign-in, and no other
  running provider. On a multi-tenant install the question is asked per
  tenant, also when a tenant is unticked: the platform's own tenant is never
  locked out; another tenant only when the operator confirms it
  (`confirm_tenant_lockout`, `409 tenant_lockout` until then).
- **A misconfigured OIDC or LDAP never stops the local administrator.** A page
  provider that fails is left out; `local` stays first; a directory that cannot
  be reached is logged and the chain moves on.

### Upgrading from before v0.43.0

Settings saved on this page by an earlier version were **never applied**. On
the first start of v0.43.0 they are imported **switched off** and marked
*"saved before v0.43.0, never applied - review and enable"*, so nothing an
operator typed there long ago and forgot is switched on by the upgrade. A
client secret or bind password among them is sealed with `FILEX_SECRET_KEY`;
without the key it is cleared (it was never used) and the log names the
provider and the field. Review the page after upgrading and switch on what you
want.

⚠ On a multi-tenant install, `ldap`, `proxy-header`, `windows` and `pam` home
a just-in-time account in the tenant the sign-in names - its realm (the sign-in
form's Realm field, `realm/name` as the user name over SFTP, FTPS and WebDAV)
or its own address (the web page, WebDAV `Host`, FTPS SNI) - and **refuse to
create one** when the sign-in names no tenant, unless `provider` pins one
(`ldap` and `proxy-header` configured from the environment or `config.yaml`
only) - see
[MULTI-TENANCY.md → Realms](MULTI-TENANCY.md#realms-which-tenant-a-sign-in-is-for)
and
[LDAP.md → Which tenant a new account lands in](LDAP.md#which-tenant-a-new-account-lands-in).

*Per-tenant* auth is a different thing: it
lives on the provider row, under `/api/admin/providers`. Single-tenant installs
are unaffected. See
[MULTI-TENANCY.md](MULTI-TENANCY.md#instance-wide-admin-surfaces).

### Why did a `local` login fail?

The caller is told the same thing whatever went wrong - `401 {"error":"invalid
credentials"}`, plus the sign-in limit's `message`, `remaining` and `limit`
while it counts - and a name nobody owns is answered exactly like a real one.
That is deliberate - anything more specific lets a stranger enumerate accounts
by watching which addresses answer differently. While a lock lasts the answer
is `429 {"error":"too many attempts"}` with `Retry-After`, even for the right
password
([CONFIGURATION.md → Sign-in attempt limits](CONFIGURATION.md#sign-in-attempt-limits)).
The **server log** is where the difference lives. Run with
`FILEX_LOG_LEVEL=debug` and match the `reason=`:

| Log line | What happened |
|---|---|
| `local: login refused` `reason="password mismatch"` (debug) | The account exists, the password is wrong. |
| `local: login refused` `reason="no such account"` (debug) | No account with that email or username - including an identifier that is not a well-formed username, which is reported the same way on purpose. |
| `local: login refused` `reason="account has no local password"` (info) | The account exists but carries no local hash - a directory or OIDC account. No password will ever work on the `local` driver; it has to sign in the way it was created, or be given a password. |
| `local: could not judge the credentials` `reason="user lookup failed"` (**error**) | The *server* failed, not the caller - a locked sqlite file, a dropped connection. The caller still sees a plain 401, so without this line "the database is down" is indistinguishable from a typo. It is also returned as a real error rather than `unauthorized`, which is what lets a multi-driver chain report it. |
| `local: stored password hash is unusable` `reason="bad password hash"` (**error**) | `users.password_hash` is not a bcrypt hash - truncated, or written by something else. That account can never sign in until its password is reset. |
| `login refused` `reason="two-factor code required"` / `"invalid two-factor code"` (debug) | The password was **right**; the second factor was missing or wrong. This one the caller is also told (`totp_required: true`), because the form has to know to ask. |

---

## See also

- [OS-LOGIN.md](OS-LOGIN.md) - signing in with an operating-system account (Windows, Linux PAM)
- [CONFIGURATION.md](CONFIGURATION.md) - full config/env reference
- [RBAC.md](RBAC.md) - per-file/folder permissions
- [INSTALLATION.md](INSTALLATION.md) - running filex
