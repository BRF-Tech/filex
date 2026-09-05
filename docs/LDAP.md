# LDAP / Active Directory & reverse-proxy header auth

Besides local passwords and [OIDC/SSO](SSO.md), filex ships two more auth
drivers for enterprise directories and gateway‑fronted deployments:

- **`ldap`** — simple‑bind against an LDAP or Active Directory server.
- **`proxy-header`** — trust identity headers set by an authenticating reverse
  proxy (oauth2‑proxy, Authelia, Cloudflare Access, …).

> **Both can be configured either way.** `auth.ldap.*` / `auth.header_proxy.*`
> in `config.yaml`, or the `FILEX_LDAP_*` / `FILEX_HEADER_*` environment
> variables — env wins where both are set, and a container-only deployment never
> needs a config file. (Older releases were file-only; that restriction is gone.)
> You pick which drivers are *enabled* the usual way (`FILEX_AUTH_DRIVERS` or
> `auth.drivers`). See
> [CONFIGURATION.md → Authentication](CONFIGURATION.md#authentication).

Both drivers **upsert the user into filex's local users table** on success, so
[RBAC](RBAC.md) grants, shares and the rest of filex treat them like any other
account.

- [LDAP / Active Directory](#ldap--active-directory)
  - [Directory accounts on the file protocols](#directory-accounts-on-the-file-protocols)
- [Reverse-proxy header auth](#reverse-proxy-header-auth)
- [See also](#see-also)

---

## LDAP / Active Directory

### How it works

LDAP plugs into the **normal password login form**. When a user submits their
e‑mail (or username) + password, filex tries each enabled login driver **in the
order they appear in `auth.drivers` / `FILEX_AUTH_DRIVERS`** and the first one
that accepts wins. Keep `local` first: it is a hash comparison against a row
filex already holds, so `admin@local` and every break‑glass password stay
answerable even while the directory is unreachable.

The LDAP driver performs a classic *search‑then‑bind*:

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

**TLS note.** `ldaps://` is selected purely by the URL scheme. With no `ca_file`
both `ldaps://` and StartTLS verify against the **system trust roots**, so a
certificate signed by an internal CA fails the handshake. Point `ca_file`
(`FILEX_LDAP_CA_FILE`) at the PEM bundle holding that CA and it is **appended**
to the system pool — the public roots keep working, and you no longer have to
rebuild the container's `/etc/ssl/certs/ca-certificates.crt` to reach your own
directory. The file is read and validated **at boot**: a wrong path is a startup
error, not a login that fails hours later with a TLS message.

### Configuration — `auth.ldap.*`

| Key | Required | Default | Meaning |
|---|---|---|---|
| `url` | **yes** | — | Directory URL. `ldap://host:389` or `ldaps://host:636`. Scheme = transport. |
| `base_dn` | **yes** | — | Search base for the user subtree, e.g. `ou=people,dc=example,dc=com`. |
| `bind_dn` | no | — | Service‑account DN for the search bind. **Omit → anonymous search.** |
| `bind_password` | no | — | Password for `bind_dn`. |
| `user_filter` | no | `(mail=%s)` | LDAP filter. **Every** `%s` is substituted with the escaped, lower‑cased identifier, so a filter may use it more than once. For AD, `(userPrincipalName=%s)` or `(sAMAccountName=%s)` are common. |
| `email_attr` | no | `mail` | Attribute read back as the account's canonical email. |
| `start_tls` | no | `false` | Upgrade a plain `ldap://` connection via StartTLS. Ignored for `ldaps://`. |
| `ca_file` | no | — | PEM bundle holding a private/internal CA, **appended** to the system trust store. Applies to `ldaps://` and StartTLS alike. Validated at boot. |
| `protocol_login` | no | `true` | Let directory accounts sign in over WebDAV, SFTP, FTPS, S3 and NFS with their directory password. See [the protocols section](#directory-accounts-on-the-file-protocols). |

`url` and `base_dn` are the only hard requirements; everything else has a working
default.

#### Matching more than one attribute

A filter may repeat the placeholder, which is how you let people sign in with
either their mail address or their UPN:

```yaml
user_filter: "(&(objectCategory=person)(objectClass=user)(|(mail=%s)(userPrincipalName=%s)))"
```

> Filters are also searched with a size limit of **2** rather than 1, because
> Active Directory answers a subtree search from the domain root with
> continuation references (`DomainDnsZones`, `ForestDnsZones`, `Configuration`)
> alongside the match. If the filter genuinely matches **two accounts**, the
> login is refused and a warning names the filter — filex will not pick one.

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
    # ca_file: /etc/filex/ldap-ca.pem   # only for a private/internal CA
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
    user_filter: "(&(objectCategory=person)(objectClass=user)(|(mail=%s)(userPrincipalName=%s)))"
    email_attr: mail
    start_tls: true
    ca_file: /etc/filex/ad-root-ca.pem   # internal CA — the usual AD case
```

The same install from environment variables only (no `config.yaml`):

```
FILEX_AUTH_DRIVERS=local,ldap
FILEX_LDAP_URL=ldap://ad.example.com
FILEX_LDAP_BIND_DN=CN=filex svc,CN=Users,DC=example,DC=com
FILEX_LDAP_BIND_PASSWORD=s3cr3t
FILEX_LDAP_BASE_DN=DC=example,DC=com
FILEX_LDAP_USER_FILTER=(&(objectCategory=person)(objectClass=user)(|(mail=%s)(userPrincipalName=%s)))
FILEX_LDAP_EMAIL_ATTR=mail
FILEX_LDAP_START_TLS=true
FILEX_LDAP_CA_FILE=/etc/filex/ad-root-ca.pem
```

> Keep `local` in the driver list if you still want the built‑in `admin@local`
> account (and any other password users) to work alongside LDAP.

### Directory accounts on the file protocols

A directory account is upserted into filex's users table with **no password
hash** — filex never learns the password, the directory keeps it. So the file
protocols (WebDAV, SFTP, FTPS, S3, NFS) cannot check it the way they check a
local one; they ask the directory instead, and `protocol_login` is what allows
that. It is **on by default**, so an account that can sign in to the web UI can
also mount `/dav` with the same credentials.

- **Order.** The local hash is compared first (no network), the directory only
  when that cannot answer. A directory outage therefore never blocks
  `admin@local`.
- **Caching.** A successful verification is remembered for 5 minutes, exactly
  like a local password — Basic‑auth protocols present the credential on every
  request, and without a cache a PROPFIND storm would be one LDAPS bind per
  request. ⚠ That TTL is also how long a password revoked **at the directory**
  keeps working on these protocols.
- **Usernames.** SFTP and FTPS carry only a username field. Once the account
  exists in filex, its canonical e‑mail (from `email_attr`) is what gets sent to
  the directory, so signing in by filex username works too.
- **2FA.** An account with TOTP enabled is refused on these protocols, whether
  its password is local or in the directory — none of them can carry a second
  factor. Such an account must use an API token (file explorer → navigation panel →
  **Connections → API keys**; an embed proxied with a shared *app* token does
  not show that entry — see [MCP.md](MCP.md#token-kinds--user-vs-app)).

Set `protocol_login: false` (or `FILEX_LDAP_PROTOCOL_LOGIN=false`) to keep
directory passwords on the login form only and require an API token everywhere
else.

### Failure modes & troubleshooting

| Symptom / log | Cause & fix |
|---|---|
| `ldap: url and base_dn required` (warning at boot) | `url` or `base_dn` missing. The driver is **skipped** and filex boots without it — LDAP logins silently won't work. Fill both keys. |
| `ldap: dial: …` on login | Can't reach the server — wrong host/port/scheme or a firewall. Confirm the `url` and that filex's network can reach it. |
| `ldap: starttls: …` on login | StartTLS negotiation failed: the server doesn't offer it, or the cert isn't trusted by system roots (custom/self‑signed CAs are **not** supported yet). Use a trusted cert, or drop `start_tls` and switch to `ldaps://` with a trusted cert. |
| `ldap: service bind: …` on login | `bind_dn` / `bind_password` are wrong, or the service account is locked. |
| Login rejected (generic "unauthorized") | Either the user wasn't found by `user_filter` under `base_dn`, or the final re‑bind failed (wrong password). filex deliberately does **not** distinguish the two to the caller (no user enumeration) — but it **does** log the difference: run with `FILEX_LOG_LEVEL=debug` and look for `ldap: no directory entry matched` (filter/base problem) versus `ldap: user bind refused` (password/account problem). Test your filter with `ldapsearch -b <base_dn> '<filter with a real email>'`. |
| `ldap: search: …` on login | The directory could not be *asked* — connection reset, an expired service account, a base DN the account may not read. This is logged and reported separately from a wrong password on purpose: those two used to be indistinguishable. |
| `ldap: user_filter matched more than one entry` (warning) | The filter is ambiguous under `base_dn` (a duplicate or a stale account in another OU). filex refuses rather than picking one. Narrow the filter or the base DN. |
| Web login works, WebDAV/SFTP/S3 answers 401 | `protocol_login` is off (or the account has TOTP enabled — see [above](#directory-accounts-on-the-file-protocols)). With TOTP on, mint an API token and use that as the password. |
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
