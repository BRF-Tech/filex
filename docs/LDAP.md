# LDAP / Active Directory & reverse-proxy header auth

Besides local passwords and [OIDC/SSO](SSO.md), filex ships two more auth
drivers for enterprise directories and gateway‑fronted deployments:

- **`ldap`** — simple‑bind against an LDAP or Active Directory server.
- **`proxy-header`** — trust identity headers set by an authenticating reverse
  proxy (oauth2‑proxy, Authelia, Cloudflare Access, …).

> **Both are `config.yaml`‑only.** Unlike `local`/`oidc`, these two drivers have
> **no `FILEX_*` environment variables** for their settings. You still pick which
> drivers are *enabled* the usual way (env `FILEX_AUTH_DRIVERS` or `auth.drivers`
> in the file), but everything else lives under `auth.ldap.*` /
> `auth.header_proxy.*` in `config.yaml`. See
> [CONFIGURATION.md → Authentication](CONFIGURATION.md#authentication).

Both drivers **upsert the user into filex's local users table** on success, so
[RBAC](RBAC.md) grants, shares and the rest of filex treat them like any other
account.

- [LDAP / Active Directory](#ldap--active-directory)
- [Reverse-proxy header auth](#reverse-proxy-header-auth)
- [See also](#see-also)

---

## LDAP / Active Directory

### How it works

LDAP plugs into the **normal password login form**. When a user submits their
email + password, filex tries each enabled driver in order; the LDAP driver
performs a classic *search‑then‑bind*:

```
 filex                                     Directory (LDAP/AD)
   │  dial url (ldap:// or ldaps://) ─────────►│
   │  (optional) StartTLS upgrade ────────────►│   ← only for ldap:// + start_tls
   │  (optional) bind as service account ─────►│   ← only if bind_dn set
   │  subtree search: user_filter(email) ─────►│
   │  ◄──────────────── user DN + email attr ──│
   │  re-bind as that DN with the user's pw ──►│   ← this is the auth check
   │  ◄──────────────────────── bind result ───│
   │  upsert into local users table            │
   │  mint 12h `filex_session` cookie          │
```

1. **Dial.** The `url` scheme decides the transport: `ldaps://` is implicit TLS,
   `ldap://` is plaintext (optionally upgraded — see `start_tls`).
2. **Optional StartTLS** upgrades a plain `ldap://` connection in‑band.
3. **Optional service bind.** If `bind_dn` is set, filex binds with it to run the
   search; if omitted, the search runs **anonymously**.
4. **Search.** A whole‑subtree search under `base_dn` using `user_filter` with
   `%s` replaced by the login email (lower‑cased and LDAP‑escaped), returning at
   most one entry.
5. **Re‑bind.** filex re‑binds as the **found DN** with the **user's own
   password** — that bind succeeding *is* the authentication.
6. **Upsert.** The account's canonical email is read from `email_attr` (falling
   back to the typed email) and the user is created if new, then a normal
   **12‑hour `filex_session` cookie** is minted (same session machinery as local
   login).

> **LDAP users are always created with the `user` role.** There is **no
> group → admin mapping** for LDAP (unlike [OIDC](SSO.md#roles--admin-access)).
> To make an LDAP user an admin, elevate them once in the admin UI
> (**Users**); the role then sticks in filex's DB.

**TLS note.** `ldaps://` is selected purely by the URL scheme. StartTLS currently
uses Go's **default** TLS verification (system trust roots) and does **not** pin
or accept a custom CA certificate — a self‑signed directory cert will fail the
handshake. Use a cert from a trusted CA (or terminate TLS at a sidecar).

### Configuration — `auth.ldap.*`

| Key | Required | Default | Meaning |
|---|---|---|---|
| `url` | **yes** | — | Directory URL. `ldap://host:389` or `ldaps://host:636`. Scheme = transport. |
| `base_dn` | **yes** | — | Search base for the user subtree, e.g. `ou=people,dc=example,dc=com`. |
| `bind_dn` | no | — | Service‑account DN for the search bind. **Omit → anonymous search.** |
| `bind_password` | no | — | Password for `bind_dn`. |
| `user_filter` | no | `(mail=%s)` | LDAP filter; `%s` is substituted with the escaped, lower‑cased login email. For AD, `(userPrincipalName=%s)` or `(sAMAccountName=%s)` are common. |
| `email_attr` | no | `mail` | Attribute read back as the account's canonical email. |
| `start_tls` | no | `false` | Upgrade a plain `ldap://` connection via StartTLS. Ignored for `ldaps://`. |

`url` and `base_dn` are the only hard requirements; everything else has a working
default.

### Example `config.yaml`

```yaml
auth:
  drivers: [local, ldap]        # or set FILEX_AUTH_DRIVERS=local,ldap
  ldap:                         # file-only — no env vars
    url: ldaps://ldap.example.com
    bind_dn: "cn=filex-svc,ou=services,dc=example,dc=com"
    bind_password: "s3cr3t"
    base_dn: "ou=people,dc=example,dc=com"
    user_filter: "(mail=%s)"
    email_attr: mail
    start_tls: false
```

Active Directory variant (bind by UPN, upgrade plaintext with StartTLS):

```yaml
auth:
  drivers: [local, ldap]
  ldap:
    url: ldap://ad.example.com
    bind_dn: "CN=filex svc,CN=Users,DC=example,DC=com"
    bind_password: "s3cr3t"
    base_dn: "DC=example,DC=com"
    user_filter: "(userPrincipalName=%s)"
    email_attr: mail
    start_tls: true
```

> Keep `local` in the driver list if you still want the built‑in `admin@local`
> account (and any other password users) to work alongside LDAP.

### Failure modes & troubleshooting

| Symptom / log | Cause & fix |
|---|---|
| `ldap: url and base_dn required` (warning at boot) | `url` or `base_dn` missing. The driver is **skipped** and filex boots without it — LDAP logins silently won't work. Fill both keys. |
| `ldap: dial: …` on login | Can't reach the server — wrong host/port/scheme or a firewall. Confirm the `url` and that filex's network can reach it. |
| `ldap: starttls: …` on login | StartTLS negotiation failed: the server doesn't offer it, or the cert isn't trusted by system roots (custom/self‑signed CAs are **not** supported yet). Use a trusted cert, or drop `start_tls` and switch to `ldaps://` with a trusted cert. |
| `ldap: service bind: …` on login | `bind_dn` / `bind_password` are wrong, or the service account is locked. |
| Login rejected (generic "unauthorized") | Either the user wasn't found by `user_filter` under `base_dn`, or the final re‑bind failed (wrong password). filex deliberately does **not** distinguish the two (no user enumeration). Test your filter with `ldapsearch -b <base_dn> '<filter with a real email>'`. |
| Login rejected even with a correct password, empty password box | An empty password is rejected up front — this guards against directories that treat an empty‑password bind as a successful *anonymous* bind. |
| User can log in but has no admin rights | Expected — LDAP users are always `user`. Elevate them in **admin UI → Users**. There is no `admin_group` for LDAP. |

> **Boot vs. login errors.** A *config* problem (`url and base_dn required`) is
> reported once at startup as a warning and the driver is skipped. *Connection*
> problems (dial/StartTLS/bind) happen per login attempt and surface as a failed
> sign‑in; check the server logs for the wrapped `ldap: …` error.

---

## Reverse-proxy header auth

Driver name **`proxy-header`** (the loader also accepts `proxyheader` and
`header_proxy`; the config block is always `auth.header_proxy`). Use it when an
**authenticating reverse proxy** in front of filex — oauth2‑proxy, Authelia,
Cloudflare Access, nginx `auth_request`, etc. — has already logged the user in
and forwards their identity as request headers.

### How it works

Unlike LDAP, this driver has no login form of its own. It runs on **every
request**, reads the identity from headers, and resolves (or provisions) the
user directly — **no session cookie is minted**, because the proxy is the source
of truth on each request.

```
 client ──► [ auth proxy ] ──► filex
                 │                 │  1. is the DIRECT peer IP in trusted_ips?  (no → ignore headers)
   sets headers ─┘                 │  2. read X-Auth-User (required), X-Auth-Email, X-Auth-Roles
   X-Auth-User / -Email / -Roles   │  3. roles ∋ admin_group? → admin, else user
                                    │  4. upsert user (auto-provision on first sight)
```

1. **Source check.** filex compares the **direct peer address** (`RemoteAddr`)
   against `trusted_ips`. If it doesn't match, the headers are **ignored** and
   the request falls through to the next driver (typically unauthenticated).
2. **Identity.** It reads the user header (`X-Auth-User`). If empty →
   unauthorized. The email comes from `email_header`; if that's empty, filex uses
   the user value when it looks like an email, otherwise synthesizes
   `<user>@proxy.local`.
3. **Role.** The `group_header` value is split on commas; if any entry equals
   `admin_group` (case‑insensitive) the user becomes **admin**, otherwise
   **user**. Re‑evaluated on every request.
4. **Provision.** The user is looked up by email and created on first sight
   (auto‑provision is on).

> **Security — trust is by the DIRECT peer IP, and `X-Forwarded-For` is
> deliberately NOT honored.** If filex trusted XFF, any client could send
> `X-Forwarded-For: <trusted>` alongside forged `X-Auth-User: admin@…` headers
> and elevate themselves. So the check is on the actual TCP peer only.
> **This driver is only safe when the proxy is the *sole* ingress to filex** — if
> a client can reach filex directly from an address inside `trusted_ips`, it can
> forge any identity. Bind filex to the proxy's private network / localhost and
> never expose it directly.

`trusted_ips` is **mandatory**: the driver **refuses to initialize** without it
(no unrestricted header trust). The user header name is **fixed to `X-Auth-User`**
— it is not configurable via `config.yaml`. Auto‑provisioning of first‑seen
users is on and likewise not configurable here.

### Configuration — `auth.header_proxy.*`

| Key | Required | Default | Meaning |
|---|---|---|---|
| `trusted_ips` | **yes** | — | CIDR list (bare IPs allowed → treated as `/32` or `/128`) of proxies whose identity headers filex will trust. **Empty ⇒ the driver refuses to start.** |
| `email_header` | no | `X-Auth-Email` | Header carrying the user's email. |
| `group_header` | no | `X-Auth-Roles` | Header carrying comma‑separated roles/groups. |
| `admin_group` | no | `admin` | The value within `group_header` that elevates the user to admin. |

> The **user identifier header is `X-Auth-User`** (fixed). A name header is
> accepted but unused (filex's users table has no name field today).

### Example `config.yaml`

```yaml
auth:
  drivers: [proxy-header]       # or FILEX_AUTH_DRIVERS=proxy-header
  header_proxy:                 # file-only — no env vars
    email_header: X-Auth-Email
    group_header: X-Auth-Roles
    admin_group: filex-admins
    trusted_ips:
      - "10.0.0.0/8"            # the proxy's private network
      - "172.18.0.0/16"         # e.g. the Docker bridge the proxy sits on
```

Your proxy must set, at minimum, `X-Auth-User`. Typical oauth2‑proxy config:

```
--set-xauthrequest                 # emits X-Auth-Request-User/-Email/-Groups
# (rename to X-Auth-User / X-Auth-Email / X-Auth-Roles at the proxy, or point
#  email_header/group_header at whatever names your proxy actually sends)
```

> Only list drivers you actually front with the proxy. Running `proxy-header`
> **and** `local` together is fine, but remember the header check applies to
> every request — a request from outside `trusted_ips` simply falls through to
> local password auth.

### Failure modes & troubleshooting

| Symptom / log | Cause & fix |
|---|---|
| `proxyheader: trusted_proxies is required (CIDR list); refusing to start …` (warning at boot) | `trusted_ips` is empty/missing. The driver is **skipped**; if it's your only login path, **nobody can authenticate** (yet filex still boots — easy to miss). Add at least one CIDR. |
| `proxyheader: invalid trusted_proxy entry "…"` / `parse CIDR "…"` | A malformed `trusted_ips` entry. Use valid CIDRs (`10.0.0.0/8`) or bare IPs (`10.1.2.3`). Same skip behavior as above. |
| Every request is unauthorized even though the proxy sets headers | The **direct peer** isn't in `trusted_ips`. Something between the proxy and filex (a load balancer, the Docker userland proxy, a service mesh) changed the source IP, so `RemoteAddr` isn't your proxy's address. Add the *actual* direct‑peer CIDR — **only** if that hop is itself trusted (XFF is not consulted). |
| Unauthorized despite a trusted source | The user header is empty. Confirm the proxy sets **`X-Auth-User`** (the fixed name), not just an email/roles header. |
| User logs in but never gets admin | The role value doesn't match. Check the exact string in `group_header` (e.g. `X-Auth-Roles`) and that `admin_group` equals one of the comma‑separated values (comparison is case‑insensitive). |
| Any client can impersonate anyone | filex is reachable directly from within a trusted CIDR. Lock filex behind the proxy (private network / localhost bind); the header trust model assumes the proxy is the *only* way in. |

---

## See also

- [SSO.md](SSO.md) — OIDC/OAuth2 single sign‑on (has env vars; role → admin mapping)
- [CONFIGURATION.md](CONFIGURATION.md#authentication) — full config/env reference, the `config.yaml` schema, and the file‑only note
- [RBAC.md](RBAC.md) — per‑storage and per‑file access control (applies to LDAP/proxy users too)
- [STORAGE.md](STORAGE.md) — mounting the backends these users will browse
- [INSTALLATION.md](INSTALLATION.md) — running filex / first‑run `admin@local`
