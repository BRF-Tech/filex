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

- Every SSO user is created with the default **`user`** role on first login.
- To grant **admin**, set `FILEX_OIDC_ROLE_CLAIM` to the claim that carries the
  user's roles/groups and `FILEX_OIDC_ADMIN_GROUP` to the value that means
  "admin". On each login filex checks that claim (string **or** array); if it
  contains the admin group, the user is elevated to `admin`.
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
| `FILEX_AUTH_DRIVERS` | yes | Comma list, e.g. `local,oidc`. Include `oidc` to enable SSO. |
| `FILEX_OIDC_ISSUER` | yes | IdP issuer URL (has `/.well-known/openid-configuration`). |
| `FILEX_OIDC_CLIENT_ID` | yes | Client/application ID registered in the IdP. |
| `FILEX_OIDC_CLIENT_SECRET` | yes* | Client secret (confidential client). |
| `FILEX_OIDC_REDIRECT_URL` | yes | `FILEX_PUBLIC_URL` + `/api/auth/oidc/callback`. Must match the IdP exactly. |
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

### filex won't start / "discover provider" error
The issuer is wrong or unreachable. filex calls
`<issuer>/.well-known/openid-configuration` at startup; if that 404s or times out
the OIDC driver fails to initialize. Verify `FILEX_OIDC_ISSUER` (for Keycloak it
**includes** `/realms/<realm>`, no trailing slash) and that filex's network can
reach the IdP.

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
Role mapping isn't matching. Confirm `FILEX_OIDC_ROLE_CLAIM` names the actual
claim in the token (inspect the ID token at jwt.io) and that
`FILEX_OIDC_ADMIN_GROUP` matches a value inside it. Roles are re-evaluated on
every login.

---

## Other auth drivers

filex ships more than OIDC. Each is enabled by adding it to `FILEX_AUTH_DRIVERS`:

- **`local`** — built-in email/password with optional TOTP 2FA.
- **`ldap`** — bind against an LDAP/Active Directory server. See
  [CONFIGURATION.md](CONFIGURATION.md).
- **`proxy-header`** — trust an authenticating reverse proxy (e.g. oauth2-proxy,
  Authelia) that sets a user header. Only enable when the proxy is the **only**
  path to filex.

Drivers can be combined; list them in the order you want them offered.

---

## See also

- [CONFIGURATION.md](CONFIGURATION.md) — full config/env reference
- [RBAC.md](RBAC.md) — per-file/folder permissions
- [INSTALLATION.md](INSTALLATION.md) — running filex
