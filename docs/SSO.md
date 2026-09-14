# Single sign-on (OIDC / OAuth2)

filex can delegate login to any **OpenID Connect**-compliant identity provider —
Keycloak, Authentik, Auth0, Okta, Dex, Google, Azure AD, … Users sign in at your
IdP and land in filex already authenticated; accounts are created on first login.

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
   keys, reads the `email` claim (required), maps a role, **upserts the user**
   into its local users table, and mints a normal **12-hour session cookie**.

After the callback, filex uses its own session — the IdP is only involved at login.

---

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
- **Grant type:** Authorization Code (standard flow).
- **Client authentication:** on (you'll get a client secret).

Note the **issuer URL**, **client ID**, and **client secret**.

> **Keycloak:** the issuer is `https://id.example.com/realms/<realm>`. Create the
> client under that realm, enable "Client authentication", set the redirect URI,
> and copy the secret from the **Credentials** tab.

### 2. Configure filex

```bash
# Enable the driver(s). Keep `local` too if you still want password login.
FILEX_AUTH_DRIVERS=local,oidc

FILEX_OIDC_ISSUER=https://id.example.com/realms/myrealm
FILEX_OIDC_CLIENT_ID=filex
FILEX_OIDC_CLIENT_SECRET=<client-secret-from-idp>
FILEX_OIDC_REDIRECT_URL=https://files.example.com/api/auth/oidc/callback

# Optional — admin mapping (see "Roles & admin access")
FILEX_OIDC_ROLE_CLAIM=realm_access.roles
FILEX_OIDC_ADMIN_GROUP=filex-admin
```

filex discovers the provider's endpoints and keys from
`<issuer>/.well-known/openid-configuration` at startup.

### 3. Sign in

A "Sign in with SSO" affordance appears on the login page (when `oidc` is in
`FILEX_AUTH_DRIVERS`). It sends the browser to `/api/auth/oidc/start`.

---

## Zero-touch / env-driven setup

OIDC is configured **entirely from environment variables** — there are no
admin‑UI clicks to enable SSO. A container/Helm deployment that ships these vars
comes up with SSO already live:

```bash
FILEX_AUTH_DRIVERS=local,oidc
FILEX_OIDC_ISSUER=https://id.example.com/realms/myrealm
FILEX_OIDC_CLIENT_ID=filex
FILEX_OIDC_CLIENT_SECRET=<client-secret-from-idp>
FILEX_OIDC_ROLE_CLAIM=realm_access.roles      # optional (admin mapping)
FILEX_OIDC_ADMIN_GROUP=filex-admin            # optional (admin mapping)
```

`FILEX_OIDC_REDIRECT_URL` is **optional** — when omitted it defaults to
`FILEX_PUBLIC_URL` + `/api/auth/oidc/callback` (still register that exact URL in
your IdP). Set it explicitly only if your callback lives elsewhere.

**LDAP** and **proxy‑header** auth are likewise env‑drivable now
(`FILEX_LDAP_*` / `FILEX_HEADER_*`) — see
[CONFIGURATION.md → Authentication](CONFIGURATION.md#authentication). The admin
account, SMTP, branding and an initial storage can also be seeded from env on
first boot — see
[CONFIGURATION.md → Zero‑touch seeding](CONFIGURATION.md#zero-touch-seeding).

---

## Roles & admin access

- An SSO account is created on its **first** login, with the **`user`** role —
  or **`admin`**, when the mapping below matches at that moment.
- To make that first login an admin, set `FILEX_OIDC_ROLE_CLAIM` to the claim
  that carries the user's roles/groups and `FILEX_OIDC_ADMIN_GROUP` to the value
  that means "admin". filex reads that claim (string **or** array, in the ID
  token or the access token) when it creates the account.
- ⚠⚠ **The claim is read once, at account creation — not on every login.** An
  account that already exists keeps its role whatever the IdP says later:
  adding someone to the admin group in the IdP after their first filex login
  does **not** make them an admin, and removing them does **not** demote them.
  Change an existing account's role in **Admin → Users**.
- Example (Keycloak realm roles): `FILEX_OIDC_ROLE_CLAIM=realm_access.roles`,
  `FILEX_OIDC_ADMIN_GROUP=filex-admin`, then assign the `filex-admin` realm role
  to the users who should administer filex.
- Per-file/folder access is governed separately by [RBAC](RBAC.md); SSO only
  decides account role (user vs admin).

> SSO accounts have **no local password** (they authenticate via the IdP). If
> you later disable OIDC, give those users a password first (admin → reset) or
> they won't be able to log in.

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
| `FILEX_OIDC_ROLE_CLAIM` | no | Claim holding roles/groups (string or array). |
| `FILEX_OIDC_ADMIN_GROUP` | no | Value within that claim that elevates a user to admin. |

The legacy `FILEX_AUTH_OIDC_*` prefix is also accepted for all OIDC keys.
Requested scopes are always `openid profile email`.

---

## What happens if it isn't configured

filex falls back to whatever else is in `FILEX_AUTH_DRIVERS` (usually `local`
password login). With no OIDC config there is simply no SSO button — nothing
else changes.

---

## Failure modes & troubleshooting

### No SSO button / `oidc: SSO disabled until restart` in the log
The issuer is wrong or unreachable. filex calls
`<issuer>/.well-known/openid-configuration` at startup and retries for about a
minute (an IdP booting in the same compose file is the common case). If it still
fails, filex **starts anyway without SSO** — password login keeps working — and
logs the error at ERROR followed by `oidc: SSO disabled until restart`. Nothing
retries after that: fix the issuer and restart. Verify `FILEX_OIDC_ISSUER` (for
Keycloak it **includes** `/realms/<realm>`, no trailing slash) and that filex's
network can reach the IdP.

### The identity provider is down and nobody can sign in

With `local` in `FILEX_AUTH_DRIVERS`, every local account still signs in with
its password. With SSO **only** (`FILEX_AUTH_DRIVERS=oidc`), the administrator
filex created at installation — `admin@local`, or the account
`FILEX_ADMIN_EMAIL` named — can still get in: open the login page, choose
**Administrator recovery sign-in** (`/admin/login?local=1`) and use that
account's password. No other account can, so password sign-in stays off for
everyone else; two-factor still applies, and the log records each such
sign-in at WARN (`auth: recovery sign-in as the bootstrap administrator`).

On an installation older than v0.41.0 the account is worked out once at
startup: the oldest administrator that has a local password. If that account
is later deleted, recovery lets nobody in — it is not handed to another
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
scope — make sure it's assigned and the user has an email.

### Redirect fails / "invalid redirect_uri"
The IdP's registered redirect URI must equal `FILEX_OIDC_REDIRECT_URL` **exactly**
(scheme, host, path). Update the client in the IdP or the env var so they match.

### User logs in but isn't admin
First: did this person sign in to filex **before** they were given the admin
group? The mapping is applied only when the account is created, so an existing
account is not promoted by a later login — set the role in **Admin → Users**.
For accounts that are still to be created, confirm `FILEX_OIDC_ROLE_CLAIM` names
the actual claim in the token (inspect it at jwt.io) and that
`FILEX_OIDC_ADMIN_GROUP` matches a value inside it.

---

## Other auth drivers

filex ships more than OIDC. Each is enabled by adding it to `FILEX_AUTH_DRIVERS`:

- **`local`** — built-in email/password with optional TOTP 2FA.
- **`ldap`** — bind against an LDAP/Active Directory server, on the same password
  form as `local`. See [LDAP.md](LDAP.md).
- **`proxy-header`** — trust an authenticating reverse proxy (e.g. oauth2-proxy,
  Authelia) that sets a user header. Only enable when the proxy is the **only**
  path to filex.

Drivers can be combined. For the two that read a password — `local` and `ldap` — the
order is the order they are **tried**: the first to accept wins, so keep `local` first
and `admin@local` stays answerable while the directory is unreachable.

⚠ Driver configuration (`/api/admin/auth-providers`) is **instance-wide**: the
`auth.*` settings rows decide who can sign in to filex at all. In multi-tenant
mode the surface is therefore **supertenant-only**, reads included — a tenant
admin gets `403 supertenant_only`. ⚠ On a multi-tenant install, `ldap` and `proxy-header` home a just-in-time
account in the tenant whose host the login arrived on, and **refuse to create
one** when no host can decide it (an SFTP/FTPS/NFS login) unless a tenant is
named explicitly — see
[LDAP.md → Which tenant a new account lands in](LDAP.md#which-tenant-a-new-account-lands-in).

*Per-tenant* auth is a different thing: it
lives on the provider row, under `/api/admin/providers`. Single-tenant installs
are unaffected. See
[MULTI-TENANCY.md](MULTI-TENANCY.md#instance-wide-admin-surfaces).

### Why did a `local` login fail?

The caller is told one thing and only one thing: `401 {"error":"invalid
credentials"}`. That is deliberate — anything more specific lets a stranger
enumerate accounts by watching which addresses answer differently. The **server
log** is where the difference lives. Run with `FILEX_LOG_LEVEL=debug` and match
the `reason=`:

| Log line | What happened |
|---|---|
| `local: login refused` `reason="password mismatch"` (debug) | The account exists, the password is wrong. |
| `local: login refused` `reason="no such account"` (debug) | No account with that e-mail or username — including an identifier that is not a well-formed username, which is reported the same way on purpose. |
| `local: login refused` `reason="account has no local password"` (info) | The account exists but carries no local hash — a directory or OIDC account. No password will ever work on the `local` driver; it has to sign in the way it was created, or be given a password. |
| `local: could not judge the credentials` `reason="user lookup failed"` (**error**) | The *server* failed, not the caller — a locked sqlite file, a dropped connection. The caller still sees a plain 401, so without this line "the database is down" is indistinguishable from a typo. It is also returned as a real error rather than `unauthorized`, which is what lets a multi-driver chain report it. |
| `local: stored password hash is unusable` `reason="bad password hash"` (**error**) | `users.password_hash` is not a bcrypt hash — truncated, or written by something else. That account can never sign in until its password is reset. |
| `login refused` `reason="two-factor code required"` / `"invalid two-factor code"` (debug) | The password was **right**; the second factor was missing or wrong. This one the caller is also told (`totp_required: true`), because the form has to know to ask. |

---

## See also

- [CONFIGURATION.md](CONFIGURATION.md) — full config/env reference
- [RBAC.md](RBAC.md) — per-file/folder permissions
- [INSTALLATION.md](INSTALLATION.md) — running filex
