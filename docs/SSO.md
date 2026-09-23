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
- **Post-logout redirect URIs:** `https://files.example.com/admin/login?signed_out=1`
  and `https://files.example.com/drive/login?signed_out=1` — or simply
  `https://files.example.com/*`. Where the IdP sends the browser back after
  [signing out](#signing-out); without them sign-out ends on the IdP's "invalid
  redirect URI" page.
- **Grant type:** Authorization Code (standard flow).
- **Client authentication:** on (you'll get a client secret).

Note the **issuer URL**, **client ID**, and **client secret**.

> **Keycloak:** the issuer is `https://id.example.com/realms/<realm>`. Create the
> client under that realm, enable "Client authentication", set the redirect URI,
> and copy the secret from the **Credentials** tab. Put the post-logout URIs in
> **Valid post logout redirect URIs** — left empty, Keycloak allows only the
> *Valid redirect URIs*, which for filex is the callback alone.

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
- To map admins from the IdP, set `FILEX_OIDC_ROLE_CLAIM` to the claim that
  carries the user's roles/groups and `FILEX_OIDC_ADMIN_GROUP` to the value that
  means "admin". filex reads that claim (string **or** array, in the ID token or
  the access token) **on every sign-in**, since 0.41.1:
  - someone added to the admin group becomes an admin at their next sign-in;
  - someone removed from it goes back to the `user` role at their next sign-in;
  - a role set by hand **below** admin (`viewer`) is left alone unless the group
    now grants admin — the mapping owns the admin role and nothing else.
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
- Per-file/folder access is governed separately by [RBAC](RBAC.md); SSO only
  decides account role (user vs admin).

> SSO accounts have **no local password** (they authenticate via the IdP). If
> you later disable OIDC, give those users a password first (admin → reset) or
> they won't be able to log in.

---

## Signing out

An SSO session has two halves: filex's own session and the IdP's. **Sign out**
ends both (OpenID Connect RP-Initiated Logout 1.0):

1. filex deletes its session and clears the cookie, as always;
2. `POST /api/auth/logout` answers with the IdP's end-session URL
   (`logout_url`) — its `end_session_endpoint` from discovery, with the
   `id_token_hint` kept from sign-in, `client_id`, and
   `post_logout_redirect_uri`;
3. the web app sends the browser there; the IdP ends its session and sends the
   browser back to the sign-in page of the front door it came from
   (`/admin/login?signed_out=1` or `/drive/login?signed_out=1`);
4. that page says "You are signed out." and does **not** start SSO by itself,
   even with `FILEX_OIDC_AUTO_REDIRECT` — whoever signs in next picks the
   account.

Why both: ending only filex's half was not signing out. With
`FILEX_OIDC_AUTO_REDIRECT` the sign-in page went straight back to the IdP, whose
session was still open, and the IdP issued a new code without a form — the same
account was signed in again half a second later, and on a shared computer the
next person got the previous one's files.

Sign-out stays filex-only when:

- `FILEX_OIDC_LOGOUT=local` — the operator wants people to stay signed in at
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
| `FILEX_OIDC_LOGOUT` | no | What **Sign out** ends: `idp` (default) — filex's session and the IdP's, when the IdP supports it; `local` — filex's session only. See [Signing out](#signing-out). |
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

### Sign-out ends on the IdP's "invalid redirect URI" page
The IdP does not allow the post-logout redirect. Add
`https://<host>/admin/login?signed_out=1` and `https://<host>/drive/login?signed_out=1`
(or `https://<host>/*`) to the client's post-logout redirect URIs — on Keycloak,
**Valid post logout redirect URIs**. Or set `FILEX_OIDC_LOGOUT=local` to keep
sign-out inside filex.

### Signing out and back in lands on the same account without a form
The IdP's session survived the sign-out. Check that the IdP advertises
`end_session_endpoint` in `/.well-known/openid-configuration` and that
`FILEX_OIDC_LOGOUT` is not `local`. A session signed in before the upgrade has
no id_token to end the IdP session with — sign in again once. Until then the
sign-in page at least waits after a sign-out instead of starting SSO by itself.

### User logs in but isn't admin
The mapping is applied at every sign-in, so the person has to sign in again after
being added to the group — an already open session keeps the role it started
with. If a fresh sign-in still lands as `user`, confirm `FILEX_OIDC_ROLE_CLAIM`
names the actual claim in the token (inspect it at jwt.io) and that
`FILEX_OIDC_ADMIN_GROUP` matches a value inside it. On 0.41.0 and older the claim
was read only at account creation; set the role in **Admin → Users** there.

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
