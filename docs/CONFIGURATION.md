# Configuration

filex reads configuration in this order (highest precedence first):

1. **Environment variables** (`FILEX_*`)
2. **`config.yaml`** (path via `--config`, `FILEX_CONFIG`, or `~/.filex/config.yaml` if present)
3. **Built-in defaults**

For containers, environment variables are easiest. For rich setups (LDAP,
proxy-header auth, custom CORS) a `config.yaml` is handier because a few settings
are **file-only** (noted below). Individual storages are **not** configured here -
they're database records; see [STORAGE.md](STORAGE.md).

- [Install-time settings (`FILEX_INSTALLATION_*`)](#install-time-settings-filex_installation_)
  - [Adopting escrow later](#adopting-escrow-later)
- [Server & networking](#server--networking)
- [Logging](#logging)
- [Database](#database)
- [Authentication](#authentication)
- [Sign-in attempt limits](#sign-in-attempt-limits)
- [Zero-touch seeding](#zero-touch-seeding)
- [External services](#external-services)
- [Protocol endpoints (S3 · SFTP · FTPS · NFS · WebDAV)](#protocol-endpoints-s3--sftp--ftps--nfs--webdav)
- [Storage plugins](#storage-plugins)
- [Storage sync](#storage-sync)
- [Uploads (staged / resumable)](#uploads-staged--resumable)
- [File operations (copy · move · delete)](#file-operations-copy--move--delete)
- [Archives](#archives)
- [Antivirus (ClamAV)](#antivirus-clamav)
- [Versioning on overwrite](#versioning-on-overwrite)
- [End-to-end encryption: the vault](#end-to-end-encryption-the-vault)
- [Downloads from slow storage (prepared copies)](#downloads-from-slow-storage-prepared-copies)
- [Thumbnails](#thumbnails)
- [Search](#search)
- [Usage & cost](#usage--cost)
- [Queue](#queue)
- [Notifications](#notifications)
- [CORS](#cors)
- [Security headers and framing](#security-headers-and-framing)
- [Error reporting (Sentry/GlitchTip)](#error-reporting)
- [Updates](#updates)
- [Demo mode](#demo-mode)
- [config.yaml](#configyaml)
- [Gotchas](#gotchas)

> **Booleans** are true only for `"1"` or (case-insensitive) `"true"`. Any other
> non-empty value is treated as false. Write `1` or `true`. (`FILEX_MULTI_TENANT`
> follows this rule since 0.53 - before, it read only `1` or lower-case `true` -
> and, set to any value at all, it pins Admin → Multi-tenant mode.) The exceptions,
> each of which is **on unless switched off**: the first-sign-in switch of each
> provider - `FILEX_OIDC_AUTO_CREATE`, `FILEX_LDAP_AUTO_CREATE` and
> `FILEX_HEADER_AUTO_CREATE` - and `FILEX_AUTH_RECOVERY_LOGIN` are false only
> for `0`, `false`, `no` or `off` (any other non-empty value is true, and unset
> keeps the default); `FILEX_CACHE` is off only for `0` or `false`. The seeds of
> a stored setting (`FILEX_CLAMAV`, `FILEX_THUMBS_FOLDER_PREVIEWS`) also take
> `yes` / `no` / `on` / `off`, and a value they do not recognise is ignored,
> with a log line naming it, rather than read as false.

---

## Install-time settings (`FILEX_INSTALLATION_*`)

Settings with the **`FILEX_INSTALLATION_`** prefix are decided at the first
boot of an installation and **cannot be edited afterwards**. filex records what
it was given in the `settings` table (key `installation.pinned`) and mirrors it
to `<data-dir>/installation.json`; on every later boot it compares the two and
**refuses to start** if they disagree, printing what changed and what your
options are.

That is not caution for its own sake. These settings change the *shape of data
already written*, so flipping one later does not reconfigure anything - it
produces an installation whose guarantees are true for some of its data and
false for the rest, with nothing on either half to say which. Refusing to start
is the only honest response.

There is exactly one supervised exception, [adoption](#adopting-escrow-later),
and it exists because the alternative was worse: escrow could only ever be
chosen in the first second of an installation's life, and nobody decides
key-escrow policy before they have a single file.

| Env var | Default | Description |
|---|---|---|
| `FILEX_INSTALLATION_E2E_ESCROW_KEY` | *(unset - escrow off)* | Base64 SPKI **public** key (RSA ≥ 2048, PEM armour and whitespace tolerated) enabling [E2E key escrow](E2E-ENCRYPTION.md#key-escrow-optional-operator-recovery). Generate the pair with `filex e2e-escrow keygen`; put the public half here and keep the private half yourself. |
| `FILEX_INSTALLATION_E2E_ESCROW_ADOPT` | `false` | One-time consent to turn escrow **on** for an installation that already exists. Read only when the pinned record has no escrow key and the environment supplies one; ignored on a first boot, and it does **not** authorise changing or removing a key. See [Adopting escrow later](#adopting-escrow-later). |

**Why escrow cannot simply be edited.** An encrypted folder wraps its master
key once per recovery path *when the folder is created*. A folder made while
escrow was off carries no escrow-wrapped key, and nothing can add one without
the folder password - which the server never has. So:

- turning escrow **on** later gives you access to nothing that already exists
  (this is the one you can still choose to do - see below - as long as you
  understand that sentence);
- **changing** the key leaves old folders openable only by the old private key
  and new ones only by the new;
- turning it **off** does not un-escrow anything already created.

**If you did not mean to change it**, the supported paths are: restore the
original value, or start a new installation with a fresh data directory -
keeping the old escrow private key for as long as the folders created under it
exist.

### Adopting escrow later

Turning escrow on for a running installation is allowed, once, and only when
you say so in a second variable:

```bash
FILEX_INSTALLATION_E2E_ESCROW_KEY=MIIBoj...      # the public half
FILEX_INSTALLATION_E2E_ESCROW_ADOPT=1            # "yes, I mean it"
```

Start the server. It logs the adoption at **WARN**, writes it to the record,
and from that moment new encrypted folders carry an escrow slot. You can drop
`_ADOPT` again on the next deploy; it is read only while the pinned record has
no escrow key, so leaving it set does nothing.

⚠⚠ **Adoption is not retroactive, and no future version can make it so - for
you.** Every encrypted folder that existed before the adoption has no
escrow-wrapped key, and nothing you do as operator gives it one: adding a slot
needs the folder password, which the server has never had. Your escrow private
key opens folders created *after* the adoption, and nothing else.

⚠ Each folder's **owner** can grant a slot, from their browser, with the folder
password. filex offers it to them the next time they unlock, states in plain
words that it hands you a permanent second way in, and records a refusal so
they are not asked again - see
[offering an existing folder a slot](E2E-ENCRYPTION.md#offering-an-existing-folder-an-escrow-slot).
You can ask; you cannot take. In the explorer, the unlock dialog says all of
this on the older folders rather than showing an escrow tab that cannot work.

Why a separate variable rather than "the key appeared, so they must have meant
it": an escrow key arrives by being pasted into a compose file, a Helm values
file or a `.env`, usually copied from another deployment. That is the shape of
an accident, and the accident is silent - the installation gains a second key
holder, only new folders get it, and nothing looks wrong until somebody needs
the key on an old folder. One extra line, in the file you are already editing,
is the smallest thing that is still a decision.

After an adoption the record separates the two dates, because "when was this
installed?" and "when did it gain a second key?" are different questions:

```json
{
  "e2e_escrow_kid": "9f2c1a55b4e07d38",
  "e2e_escrow_alg": "RSA-OAEP-256",
  "pinned_at": "2026-09-05T12:02:24Z",
  "pinned_by": "first-boot",
  "e2e_escrow_adopted_at": "2026-11-20T08:30:00Z",
  "e2e_escrow_adopted_by": "env:FILEX_INSTALLATION_E2E_ESCROW_ADOPT",
  "e2e_escrow_adoption_note": "escrow was turned on after this installation already existed; folders created BEFORE e2e_escrow_adopted_at have no escrow-wrapped key, so the escrow key does not open them and no operator action can change that. Each folder's OWNER can grant it from the browser with the folder password"
}
```

`e2e_escrow_adopted_at` is the boundary of **automatic** coverage: folders
older than it get no escrow slot at creation, and no operator action gives them
one. It is not a claim about every folder forever - one whose owner
[granted a slot](E2E-ENCRYPTION.md#offering-an-existing-folder-an-escrow-slot)
is simply no longer described by it, and the marker's `esc` slot is the only
authority on which key opens which folder. A record without these fields means
escrow was present from the first boot (or is off).

⚠ `_ADOPT` is **not** a general override. Pointing the key at a different value,
or removing it, is still a start-time failure with the flag set - those two
really do leave folders behind that the running configuration can no longer
describe.

⚠ An unparseable value is **fatal**, not ignored. Running without escrow while
the operator believes they configured it is the one failure mode worth crashing
over.

⚠ The private half never reaches filex. `filex e2e-escrow keygen` writes
nothing and touches no database; it prints the pair and exits. That is what
makes a stolen filex database worthless to an attacker even with escrow on -
and what makes losing the private key unrecoverable.

---

## Server & networking

| Env var | Default | Description |
|---|---|---|
| `FILEX_LISTEN` | `0.0.0.0:5212` | Bind address. |
| `FILEX_PUBLIC_URL` | `http://localhost:5212` | **The external URL users open.** Baked into share links, the OIDC redirect and OnlyOffice fetch/callback - set it to your real `https://…` domain behind a proxy. ⚠ It is one value serving both audiences: it must be openable by a browser **and** reachable from inside the OnlyOffice container ([three addresses](ONLYOFFICE.md#three-machines-three-addresses)). |
| `FILEX_BASE_PATH` | - (the path of `FILEX_PUBLIC_URL`, usually none) | **Serve filex under a sub-path** behind a reverse proxy, e.g. `/filex` for `https://example.com/filex/`. Unset: the path of `FILEX_PUBLIC_URL` is used, so `FILEX_PUBLIC_URL=https://example.com/filex` alone is enough. Empty/unset with a root public URL = served at the root, exactly as before. Validated at startup; the proxy must pass the **full** path. See [Base path](#base-path) and [DEPLOYMENT.md → Serving filex under a sub-path](DEPLOYMENT.md#serving-filex-under-a-sub-path). |
| `FILEX_DATA_DIR` | `~/.filex` (`/data` in Docker) | Holds the SQLite DB, search index, thumbnail cache, first-run secret. |
| `FILEX_DEFAULT_LOCALE` | - | Pin the initial UI language (`en` / `tr`) for users who haven't chosen one, overriding browser detection. A user's explicit language switch still wins. |
| `FILEX_MULTI_TENANT` | - (the switch) | Native multi-tenancy - one install serves N tenants, each a host-bound auth realm (provider) confined to its own storage(s). **Off = a normal single-tenant install, behaviour unchanged.** Since 0.53 the mode is a switch on **Admin → Multi-tenant mode** (the platform operator's; a change takes effect when filex is restarted). Set, this variable **pins** it both ways - `1`/`true` on, any other value off - and the switch shows it locked; config file `multi_tenant` does the same when the variable is unset. Unset in both, the switch decides, and it is off until somebody turns it on. On, a sign-in names its tenant by the tenant's own address or its **realm** (the sign-in form's Realm field, `realm/name` over SFTP); a bare name is the platform's own tenant. See [MULTI-TENANCY.md](./MULTI-TENANCY.md) and [its Realms section](./MULTI-TENANCY.md#realms-which-tenant-a-sign-in-is-for). |
| `FILEX_COOKIE_DOMAIN` | - (host-only) | `Domain` attribute for the `filex_session` cookie, e.g. `.example.com` - subdomains of that domain then share the session. Applied on **both** set and clear, so logout removes the same cookie it created. Empty = host-only cookie (unchanged behaviour). `Secure`/`SameSite`/`HttpOnly` are unaffected. **Multi-tenant:** this is only the last-resort fallback - the cookie Domain resolves per tenant: the provider's `cookie_domain` field wins, else it is derived from the provider host by dropping its first label (`files.example.com` → `.example.com`), else this global value. ⚠ A tenant served on its bare apex, or whose derivation would land on a public suffix (`tenant.com.tr` → `.com.tr`, which browsers reject), must set `cookie_domain` explicitly. See [MULTI-TENANCY.md](./MULTI-TENANCY.md). |
| `FILEX_TENANT_DOMAIN` | - | **Multi-tenant:** gives every tenant an address of its own, `<realm>.<tenant domain>` (`tenants.files.example` → `acme.tenants.files.example`), and is the name a tenant's own domain points its CNAME at ([TENANT-ADMIN.md](TENANT-ADMIN.md#addresses-and-own-domains)). Needs a wildcard DNS record `*.<tenant domain>` of **address records** (A/AAAA) pointing at the platform: behind a wildcard CNAME every own domain's canonical name is that CNAME's target, and none can be proven. Empty = no platform subdomains and no own domains. On a platform subdomain or an own domain the session cookie is that address's alone. |
| `FILEX_TLS_MODE` | `proxy` | Who issues the certificates of the tenants' addresses. `proxy`: the reverse proxy in front, which may ask filex first (`/api/tls/ask` for Caddy's on-demand TLS, `/api/tls/certificate` for a tenant's own certificate; both answer the proxy itself only). `acme`: filex terminates TLS itself and issues Let's Encrypt certificates (`x/crypto/acme/autocert`, cache in the data directory). A tenant's own certificate is served first either way. Any other value stops the server at start. [TENANT-ADMIN.md, TLS](TENANT-ADMIN.md#tls-an-installation-setting-plus-a-tenants-own-certificate). |
| `FILEX_TLS_LISTEN` | `:443` | `acme` mode: where filex's own TLS server listens. |
| `FILEX_TLS_HTTP_LISTEN` | `:80` | `acme` mode: the plain listener for the HTTP-01 challenge and the redirect to HTTPS; `off` for none (the TLS-ALPN-01 challenge on the TLS listener still works). |
| `FILEX_TLS_ACME_EMAIL` | - | `acme` mode: the contact the ACME account is registered with. |
| `FILEX_TLS_ACME_DIRECTORY` | Let's Encrypt | `acme` mode: another ACME directory URL (a staging directory, a private CA). The directory's own HTTPS certificate must be trusted, and filex has no setting of its own for that: a private CA's root is added the way any Go program takes one, `SSL_CERT_FILE` (replaces the system bundle, so give it a bundle with both) or `SSL_CERT_DIR` (adds a directory). `e2e/realenv` runs filex against Pebble this way. |
| `FILEX_TRUSTED_PROXIES` | `auto` | Reverse proxies whose `X-Forwarded-For` / `X-Real-IP` filex believes: addresses and CIDR networks, comma or space separated, and four words - `auto` (worked out from where filex runs: this machine and, in a container, the container networks it is attached to, never their gateways, never the LAN - see [Trusted proxies](#trusted-proxies)), and one per class of address as Go's standard library defines it (`net/netip`): `loopback` (this machine), `private` (the RFC 1918 / RFC 4193 private ranges, `IsPrivate`) and `link-local` (`IsLinkLocalUnicast`); an IPv4-mapped IPv6 address counts as the IPv4 address it carries. A list **replaces** the default, so keep `auto` to add to it: `auto, 203.0.113.0/24`. `none` trusts no proxy. ⚠ The forwarded header is read **only** when the socket's peer is on this list, and the chain is walked from the trusted end - a header a client writes itself never decides the address the [sign-in limit](#sign-in-attempt-limits) counts by, nor the one the access log, the audit log and the file-request limit record. ⚠ `auto` does not trust a proxy **on the host** that reaches filex's container through a published port (it arrives from the Docker gateway), a proxy **on another machine**, an ingress controller on **another Kubernetes node**, or a CDN's **public** edge in front of your proxy: list each (`auto, <address>`), or every visitor resolves to the proxy's address and shares one counter - the **Sign-in security** page names such a peer and offers to add it. ⚠ `private` is RFC 1918 / RFC 4193 and nothing else, the LAN and every gateway included: a proxy that reaches filex over `100.64.0.0/10` (carrier-grade NAT - Tailscale, Cloudflare WARP, some pod networks) must be listed. Before 0.50 every forwarded header was believed, whoever sent it. The `login.trusted_proxies` setting (the **Sign-in security** page) wins over this variable, which wins over `auto`. A bad entry stops the server at start. |
| `FILEX_CONFIG` | - | Path to `config.yaml` (same as `--config`). |

### Trusted proxies

Which peers filex believes about a client's address (`X-Forwarded-For`,
`X-Real-IP`) - and, since 0.54, about how the client arrived (an
`X-Forwarded-Proto: http`, which turns a tenant's links into `http://`, is
read from these peers only). The list is the `login.trusted_proxies` setting
(the **Sign-in security** page), else `FILEX_TRUSTED_PROXIES`, else **`auto`**. `auto` is worked out
from where filex runs - at start, and again every minute, so a container
joined to another network with `docker network connect` is noticed - and
trusts:

| Where filex runs | `auto` trusts |
|---|---|
| A plain install - Linux, Windows or macOS, not in a container | This machine (`127.0.0.0/8`, `::1`) only. |
| A container on a network of its own - Docker bridge or user-defined networks, Podman rootful (netavark, CNI), a Kubernetes pod | This machine, plus the subnet of each of the container's own `veth` interfaces (all of them when it is on several networks), **minus** every gateway - each next hop in `/proc/net/route` and `/proc/net/ipv6_route`, and each network's first host address, Docker's default gateway `.1` / `::1` - and **minus** the container's own addresses. |
| Host networking - `network_mode: host`, `podman --network host`, a pod with `hostNetwork` | This machine only: the container sees the host's own NICs and bridges. |
| A macvlan or ipvlan network | Never: it puts the container on the LAN. |
| Rootless Podman on its default network - pasta, slirp4netns | This machine only: its interface is a `tun` device, and what it relays arrives from the container's own address (pasta copies the host's; slirp4netns uses `10.0.2.100`) or the gateway (`10.0.2.2`). On a user-defined network it is a container network like the rows above. |
| Kubernetes | The pod's own interface subnet: a node's `/24` with flannel or kindnet, a `/32` - the pod alone - with Calico or Cilium. |
| Anything that cannot be read, or that contradicts itself | This machine only, and a warning: in the log at start, and on the page. |

IPv6 container networks (a ULA such as `fd00:dead:beef::/64`) count like IPv4
ones, minus their gateway. Link-local addresses (`fe80::/10`, `169.254.0.0/16`)
never do - the host bridge's own link-local address sits there. Whether an
interface is a `veth`, a macvlan or an ipvlan is asked of the kernel over
netlink (what `ip -d link` reads); `/sys/class/net` shows the three the same way.

**Why the gateway and filex's own address are carved out** (measured on Docker
29 and Podman 5.8): a connection to a port published on `127.0.0.1` is relayed by
`docker-proxy` and reaches the container from the gateway (`172.x.0.1`), and a
host process that dials the container's address directly arrives from it too;
rootful Podman relays from `10.88.0.1` the same way. Trusting the gateway would
let anything that reaches the published port choose its own address. Rootless
Podman on a user-defined network relays every published-port connection - from
the internet too - from the container's **own** address. A client on the LAN
that reaches a published port arrives with its own address, which `auto` does
not trust either.

**What to list by hand**, next to `auto` - `FILEX_TRUSTED_PROXIES=auto, <address>`,
or the page's address list:

- a proxy **on the host** that reaches filex's container through a published
  port: the gateway, e.g. `172.18.0.1` - only when that port is published on
  `127.0.0.1` or firewalled, so that nothing but the proxy reaches it;
- a proxy **on another machine**: its address;
- an ingress controller on **another Kubernetes node**: the cluster's pod
  network, e.g. `auto, 10.244.0.0/16` (the Helm chart's `trustedProxies`);
- a CDN or any other **public** hop in front of your proxy: its ranges.

A peer that is not trusted and sends `X-Forwarded-For` or `X-Real-IP` is
named - in the log, once per peer and at most five lines a minute, and on the
**Sign-in security** page with how often it was seen and a one-click
**Trust** that saves `auto, <address>` (or the list in force plus the address),
after a question that says what trusting a stranger costs. At most 256 such
peers are remembered, a day each; only the peer's address is kept, never what it
wrote. What `auto` resolved to and why is `trusted_proxies_auto` on
[`GET /api/admin/login-security`](BACKEND.md#admin-sign-in-security).

⚠ Where `auto` cannot see the truth from inside the container, list the proxy
by hand (or write the list without `auto`):

- **Docker Swarm's routing mesh** (a port published in the default `ingress`
  mode) relays every client from an address on the ingress network, which the
  container sees as a container network - publish in `mode: host`, or set the
  list.
- A Docker or CNI **bridge that also carries the host's LAN NIC** (containers
  bridged straight onto the LAN) looks like a container network from inside.
- A network created with a **custom `--gateway`** that is not the container's
  default route: its gateway is known only when it is the first host address
  or a route's next hop.
- **Nested runtimes** (Docker-in-Docker, a kind or k3d node, a container inside
  an LXC system container): the outer network looks like a container network;
  a bridge next to it with no physical NIC is a contradiction, so `auto` falls
  back to this machine and warns.

### Public URL

`FILEX_PUBLIC_URL` is the one address filex hands to other people: every share
link and file-request link, the links inside every email, the OIDC redirect
and the address OnlyOffice fetches documents from. Set it to what a browser
types to reach this instance - `https://files.example.com`, no trailing slash,
the proxy's hostname rather than the container's.

**When it is not set**, filex assumes `http://localhost:5212` and builds every
one of those links on it. The link looks fine on the machine that runs filex
and is dead everywhere else - and the first person to find out is whoever
receives it. So an administrator signed in to the panel sees a banner saying
the variable is unset until it is; `GET /api/files/capabilities` says the same
thing as `public_url_configured: false`.

⚠ The variable is `FILEX_PUBLIC_URL`, exactly. filex reads no
`FILEX_APPLICATION_URL`, `APP_URL`, `BASE_URL` or `SITE_URL`, and a typo in a
compose file is not an error at startup - it is a share link to `localhost`
some time later. The panel banner is there for precisely that case.

In [multi-tenant](./MULTI-TENANCY.md) mode a tenant's links are built on the
tenant's own host; `FILEX_PUBLIC_URL` is the operator's fallback.

### Base path

filex can live under a path of a domain it shares with other things -
`https://example.com/filex/` - instead of on a host of its own. **One setting**
decides it:

- `FILEX_BASE_PATH=/filex` (`base_path: /filex` in `config.yaml`), or
- nothing at all, with `FILEX_PUBLIC_URL=https://example.com/filex`: the path
  of the public URL **is** the base.

| You set | filex serves | Links are built on |
|---|---|---|
| `FILEX_PUBLIC_URL=https://files.example.com` | the root (the default) | `https://files.example.com` |
| `FILEX_PUBLIC_URL=https://example.com/filex` | `/filex/…` | `https://example.com/filex` |
| `FILEX_BASE_PATH=/filex` + `FILEX_PUBLIC_URL=https://example.com` | `/filex/…` | `https://example.com/filex` (the base is added to the public URL) |
| `FILEX_BASE_PATH=/filex` + `FILEX_PUBLIC_URL=https://example.com/filex` | `/filex/…` | `https://example.com/filex` |
| `FILEX_BASE_PATH=/filex` + `FILEX_PUBLIC_URL=https://example.com/files` | **refuses to start** - the two disagree | - |
| `FILEX_BASE_PATH=/` | the root, said out loud (and refused if the public URL has a path) | - |

The value is checked when filex starts, and a bad one stops it with a message
that says what to write instead:

- it starts with `/` and does **not** end with one - `/filex`, not `/filex/`
  or `filex`;
- no empty, `.` or `..` segment;
- letters, digits and `- . _ ~` only (anything else would need
  percent-encoding, and a prefix that can be spelled two ways is a prefix a
  request can be talked past).

The startup log names the base in effect and where it came from:

```text
INFO http: serving under a base path base_path=/filex from=FILEX_PUBLIC_URL public_url=https://example.com/filex
```

(`http: serving at the root of the host (no base path)` otherwise.)

What it changes, all of it at once:

- **Every route answers under the base and nowhere else.** `/filex/api/…`,
  `/filex/admin/`, `/filex/dav/…`, `/filex/s/<token>` - and `/api/…` on the
  host's root is a plain 404, before any sign-in check runs. The one exception
  is `/healthz`, which answers at the root **and** at `/filex/healthz`, because
  the container images' `HEALTHCHECK` and a Kubernetes probe ask for it there.
- **The proxy passes the full path.** filex takes the prefix off itself; a
  proxy that strips it (Caddy `handle_path`, an nginx `proxy_pass` with a URI)
  sends requests filex no longer answers. Examples in
  [DEPLOYMENT.md](DEPLOYMENT.md#serving-filex-under-a-sub-path).
- **Every address filex hands out carries it**: redirects, share and
  file-request links, links in e-mails, the OIDC callback
  (`<public>/api/auth/oidc/callback`), the realtime socket, the WebDAV and S3
  endpoints on the connection pages, the PWA manifest's `id`, `start_url` and
  `scope`.
- **Cookies are scoped to it** (`Path=/filex`), so the other applications on
  the same host do not receive filex's session.
- **The web app is the same build.** The server tells it its base as it serves
  it; nothing is rebuilt per prefix.

⚠ Pick a base that is not one of filex's own route names (`admin`, `drive`,
`api`, `dav`, `s3`, `s`, `d`, `u`, `z`, `p`, `files`, `embed`). The server
works with any of them, but the desktop app, given an address someone pasted
from the browser, cannot tell `https://example.com/drive/` (filex under
`/drive`) from the `/drive/` page of a filex at the root.

⚠ Moving an existing install under a base path, or out of one, changes the
installed web app's identity (the manifest `id` follows the base): browsers
treat it as a new app, and people who installed it install it again. Links
already sent keep pointing at the old address.

---

## Logging

| Env var | Default | Description |
|---|---|---|
| `FILEX_LOG_LEVEL` | `info` | `debug` · `info` · `warn` · `error` |
| `FILEX_LOG_FORMAT` | `text` | `text` · `json` |

Every HTTP request writes one `info` line, `msg=http`:

| Field | Always | Meaning |
|---|---|---|
| `method`, `path`, `status`, `ip`, `dur_us` | yes | the request, its answer, and how long it took (µs) |
| `user_id` | when signed in | the account the request acted as |
| `token_id` | when an API token was used | the token's row id (as listed on the API keys page), never its secret |
| `tenant` | multi-tenant only | the tenant (provider) slug the request was scoped to |
| `action` | `/api/files/manager` only | the file manager's verb - `index`, `search`, `upload`, `rename`, `move`, `delete`… - or `other` for anything it does not have |

```
time=2026-09-22T10:04:12.345Z level=INFO msg=http method=POST path=/api/files/manager status=500 ip=10.0.0.5 dur_us=812 user_id=12 token_id=34 action=upload
```

That line is written when the answer is finished. At `FILEX_LOG_LEVEL=debug`
each request also writes a `debug` line the moment it arrives,
`msg="http start"`, with `method`, `path`, `ip` and `peer` (the socket's own
address, port included, where `ip` is the client as resolved through trusted
proxies).
A request with an arrival line and no `msg=http` line reached filex and has not
been answered; one with neither never reached it.

⚠ The **query string is never logged**. It carries thumbnail and OnlyOffice
signatures, WebSocket tickets, share PINs, OIDC codes, S3 presigned credentials
and people's search text. `action` is the only value read from it, and only as
one of the manager's own verbs. The **path** is logged as sent, and share and
drop links carry their token in it (`/s/…`, `/d/…`), so treat the access log as
you would the database.

---

## Database

| Env var | Default | Description |
|---|---|---|
| `FILEX_DB_DRIVER` | `sqlite` | `sqlite` · `postgres` · `mysql` |
| `FILEX_DB_DSN` | - | Connection string. Empty + sqlite → `<data_dir>/instance.sqlite`. |

DSN examples:
- postgres: `postgres://user:pass@host:5432/dbname?sslmode=require`
- mysql: `user:pass@tcp(host:3306)/dbname?parseTime=true&loc=UTC&charset=utf8mb4`

Migrations **run automatically on startup**; also `filex migrate up|down|status`.
SQLite (pure Go, CGO-free) is a fine default; **PostgreSQL is recommended for
teams/HA**. MySQL needs **8.0.17+** (MariaDB **11.4+**) and filex fills in
`parseTime`, `loc=UTC` and `time_zone='+00:00'` when the DSN omits them.

All three engines run the migrations, a schema comparison and the writes of a
first install in CI on every change - see **[DATABASES.md](DATABASES.md)**,
which also explains why the queue driver follows the database.

---

## Authentication

Pick drivers with `FILEX_AUTH_DRIVERS` (comma list, tried in order, first match
wins). The **API-token driver is always on** regardless.

**Precedence (v0.43.0).** OIDC, LDAP and the proxy header can also be set up on
**Admin → Identity providers**, applied without a restart - and the
operating-system sign-in (`windows` on a Windows server, `pam` on a Linux one,
[OS-LOGIN.md](OS-LOGIN.md)) can be set up **only** there. The environment
wins: a driver listed in `FILEX_AUTH_DRIVERS` - or, when that variable is
unset, in the config file's `auth.drivers`, or else the built-in default
(`local`) - is built from the environment's settings only and shown on the page
read-only with where it is defined; the page's providers are added after the
environment's. `local` and the recovery sign-in are the environment's alone, so
the page can never lock the instance out. See
[SSO.md → Managing providers on the Identity providers page](SSO.md#managing-providers-on-the-identity-providers-page).

| Env var | Default | Description |
|---|---|---|
| `FILEX_AUTH_DRIVERS` | `local` | e.g. `local,oidc`, `local,ldap`, `proxy_header`. The operating-system providers `windows` and `pam` are not accepted here (nor in `auth.drivers`): they are switched on from **Admin → Identity providers**, where their setup is tested, and an entry for one is refused at start with a warning while the others start ([OS-LOGIN.md](OS-LOGIN.md)). |
| `FILEX_AUTH_RECOVERY_LOGIN` | `true` | **Recovery sign-in.** When no `local` driver is enabled - SSO or a directory only - the administrator filex created at installation can still sign in with its password, and nobody else can. The login page offers it behind an *Administrator recovery sign-in* link (`/admin/login?local=1`); two-factor still applies, and every such sign-in is logged at WARN. It exists for the day the identity provider is down. Set `false` if your policy forbids any password sign-in. |

**OIDC / SSO** (see [SSO.md](SSO.md)):

| Env var (legacy `FILEX_AUTH_OIDC_*` also accepted) | Description |
|---|---|
| `FILEX_OIDC_ISSUER` | IdP issuer URL |
| `FILEX_OIDC_CLIENT_ID` | Client ID |
| `FILEX_OIDC_CLIENT_SECRET` | Client secret |
| `FILEX_OIDC_REDIRECT_URL` | `<public>/api/auth/oidc/callback` |
| `FILEX_OIDC_ROLE_CLAIM` | Claim carrying roles/groups |
| `FILEX_OIDC_ADMIN_GROUP` | Value that elevates to admin. Applied at **every** sign-in since 0.41.1 - added to the group → admin, removed → `user`; the setup account and the last admin are never demoted ([SSO.md](SSO.md#roles--admin-access)) |
| `FILEX_OIDC_AUTO_REDIRECT` | **SSO-first login** (default `false`): the login page starts the OIDC flow immediately instead of showing the password form. Local login stays available behind a "Sign in with password" link (`/admin/login?local=1`) for break-glass/`admin@local`. The redirect is skipped on `?local=1`, after a failed IdP round-trip (`?error=oidc`), on `?maintenance=1` and right after signing out (`?signed_out=1`), so a broken IdP can never cause a redirect loop and a sign-out is never undone by the next page. Requires `oidc` in `FILEX_AUTH_DRIVERS`. Multi-tenant: the flag is instance-global; the flow itself already dispatches per request host to the right tenant realm. |
| `FILEX_OIDC_AUTO_CREATE` | `false` stops SSO from opening an account at a person's first sign-in (only existing accounts sign in). Default `true`. See [LDAP.md → the first sign-in rule](LDAP.md#the-first-sign-in-rule-who-gets-an-account), the one rule every provider follows. |
| `FILEX_OIDC_ALLOWED_GROUPS` | Comma list, judged against the `FILEX_OIDC_ROLE_CLAIM` claim: only members of one of these groups get an account on their first sign-in. |
| `FILEX_OIDC_TRUST_EMAIL` | `true` takes every email address the provider sends as verified, `email_verified` or not. Off, an unverified address signs in to no account that is not yet bound to its SSO identity, and opens a new account switched off for an administrator to approve. Unset: the answer the upgrade to 0.50 took once (on when the database had accounts that came through SSO), off on a fresh install. ⚠ Only for a provider whose addresses all belong to their people. See [SSO.md](SSO.md#trust-this-providers-email-addresses). |
| `FILEX_OIDC_LOGOUT` | What **Sign out** ends for an SSO session (default `idp`): `idp` - filex's session **and** the IdP's (RP-initiated logout, when the IdP's discovery has an `end_session_endpoint`); the IdP must allow `https://<host>/admin/login?signed_out=1` and `https://<host>/drive/login?signed_out=1` (or `https://<host>/*`) as post-logout redirect URIs. `local` - filex's session only; the person stays signed in at the IdP. See [SSO.md](SSO.md#signing-out). |

**LDAP** (enable with `FILEX_AUTH_DRIVERS=local,ldap`):

| Env var | Description |
|---|---|
| `FILEX_LDAP_URL` | Directory URL, e.g. `ldaps://ldap.example.com` |
| `FILEX_LDAP_BIND_DN` | Service bind DN |
| `FILEX_LDAP_BIND_PASSWORD` | Service bind password |
| `FILEX_LDAP_BASE_DN` | Search base for users |
| `FILEX_LDAP_USER_FILTER` | User filter, e.g. `(mail=%s)` |
| `FILEX_LDAP_EMAIL_ATTR` | Attribute holding the email (e.g. `mail`) |
| `FILEX_LDAP_START_TLS` | `true` to upgrade a plain connection with StartTLS |
| `FILEX_LDAP_CA_FILE` | PEM bundle holding the CA that signed the directory certificate. Appended to the system trust store, not substituted for it. Needed for an internal/private CA on `ldaps://` **or** StartTLS. |
| `FILEX_LDAP_PROTOCOL_LOGIN` | `false` to stop directory accounts from signing in over WebDAV, SFTP and FTPS with their directory password. Default `true`. (S3 and NFS never take a password: they use the access keys and exports an account mints.) |
| `FILEX_LDAP_PROVIDER` | **Multi-tenant only.** Tenant slug a newly created directory account is homed in when the login names no tenant - neither a [realm](MULTI-TENANCY.md#realms-which-tenant-a-sign-in-is-for) nor a tenant's own address (a bare name over SFTP, FTPS without SNI, NFS, the platform's sign-in page with an empty Realm). Such a login is then that tenant's, and its account's address carries that tenant's realm. Unset ⇒ such a login **refuses to create** the account rather than falling back to the confine-exempt supertenant. See [LDAP.md](LDAP.md#which-tenant-a-new-account-lands-in). |
| `FILEX_LDAP_AUTO_CREATE` | `false` stops the directory from opening an account at a person's first sign-in (only existing accounts sign in). Default `true`. See [LDAP.md](LDAP.md#the-first-sign-in-rule-who-gets-an-account). |
| `FILEX_LDAP_ALLOWED_GROUPS` | Comma list: only members of one of these groups get an account on their first sign-in. |
| `FILEX_LDAP_SHOW_REFUSAL_REASON` | `true` tells a person whose directory password was right why the first sign-in rule still refuses them (403 with a reason code). Default `false`: the wrong-password answer, so the form never confirms a directory password ([why](LDAP.md#the-first-sign-in-rule-who-gets-an-account)). The same switch is `show_refusal_reason` on every LDAP, PAM and Windows provider of the Identity providers page. |
| `FILEX_LDAP_GROUP_ATTR` | Entry attribute listing a person's groups. Default `memberOf`. Unset, groups are read only once `FILEX_LDAP_ALLOWED_GROUPS` is set; set it to feed [groups](GROUPS.md#members-and-sso-links) and starting roles without restricting who gets an account. It is also where a browser sign-in and directory sync read a person's groups for filex groups linked to LDAP groups ([LDAP.md → Groups](LDAP.md#groups)). |
| `FILEX_OS_LOGIN_EMAIL_TOKEN` | What follows the `@` of an account known only by a login name (`alex` → `alex@local`; in a tenant's realm on a multi-tenant install `alex@acme.local`). Default `local`. **Set once at installation and never change it** - a later change makes a second account of the same person. The environment or `config.yaml` (`auth.login_email_token`) only - no page or API changes it - and a value that is not a DNS-like label (`a-z`, `0-9`, `.`, `-`) stops the server at start. It applies to the operating-system providers too ([OS-LOGIN.md](OS-LOGIN.md#the-e-mail-token---choose-it-once)). See [LDAP.md](LDAP.md#e-mail-address-for-an-account-that-has-only-a-login-name). |
| `FILEX_LDAP_GROUP_FILTER` | Find a person's groups for the LDAP links by a search instead: `%s` is their DN, `%u` the name they signed in with, e.g. `(member=%s)` |
| `FILEX_LDAP_GROUP_BASE_DN` | Where that search runs (default: the base DN) |
| `FILEX_LDAP_SYNC_INTERVAL` | Run directory sync on its own this often (e.g. `6h`, at least `5m`); unset = only from **Sync now**. See [LDAP.md → Directory sync](LDAP.md#directory-sync) |
| `FILEX_LDAP_SYNC_FILTER` | The search listing every person for directory sync (default: the user filter with `*`) |
| `FILEX_LDAP_SYNC_DISABLE_MISSING` | `true` to switch off accounts the directory made once it no longer lists them |
| `FILEX_LDAP_SYNC_GROUPS` | `false` to stop directory sync bringing every directory group in as a filex group (on by default) |
| `FILEX_LDAP_SYNC_GROUP_FILTER` | Which directory groups sync brings in (default: every group) |
| `FILEX_LDAP_EMAIL_DOMAINS` | Only these e-mail domains (comma-separated) sign in through this directory or get an account from it. See [LDAP.md → Several directories](LDAP.md#several-directories) |

> `FILEX_LDAP_USER_FILTER` may contain the placeholder more than once - every
> `%s` is filled with the same escaped identifier, so the usual AD filter that
> accepts either address form works as written:
> `(&(objectCategory=person)(objectClass=user)(|(mail=%s)(userPrincipalName=%s)))`

**Proxy-header** - trust an authenticating reverse proxy (enable with
`FILEX_AUTH_DRIVERS=proxy_header`):

| Env var | Description |
|---|---|
| `FILEX_HEADER_EMAIL` | Header carrying the authenticated email (e.g. `X-Auth-Email`) |
| `FILEX_HEADER_GROUP` | Header carrying roles/groups (e.g. `X-Auth-Roles`) - the person's groups, recorded whenever a request carries it; see [LDAP.md → Groups from the roles header](LDAP.md#groups-from-the-roles-header) |
| `FILEX_HEADER_TRUSTED_IPS` | Comma list of proxy CIDRs allowed to set the identity headers - checked against the direct peer, and a separate list from `FILEX_TRUSTED_PROXIES` (whose forwarded client address filex believes) |
| `FILEX_HEADER_ADMIN_GROUP` | Group value that creates a new account as admin (an existing account's role is not changed by the header) |
| `FILEX_HEADER_AUTO_CREATE` | `false` stops the proxy from opening an account for a person it names for the first time. Default `true`. See [LDAP.md](LDAP.md#the-first-sign-in-rule-who-gets-an-account). |
| `FILEX_HEADER_ALLOWED_GROUPS` | Comma list, judged against the roles header: only members of one of these groups get an account. Only a door - the header's groups are recorded with or without it. |
| `FILEX_HEADER_PROVIDER` | **Multi-tenant only.** Tenant slug a newly created account is homed in when the request Host maps to no tenant. Unset ⇒ such a request **refuses to create** the account. See [LDAP.md](LDAP.md#which-tenant-a-new-account-lands-in). |

> LDAP and proxy-header can still be set under `auth.ldap.*` /
> `auth.header_proxy.*` in [config.yaml](#configyaml); the env vars above override
> those. See [SSO.md → other auth drivers](SSO.md#other-auth-drivers).

Local auth uses the `filex_session` cookie (12 h), bcrypt passwords, optional
TOTP 2FA. First boot creates `admin@local` (or seed a known admin - see
[Zero-touch seeding](#zero-touch-seeding) and [INSTALLATION.md](INSTALLATION.md#first-run)).

---

## Sign-in attempt limits

Wrong passwords are counted and, past a limit, the door shuts - for the web form
and for every protocol that takes a password (WebDAV, FTPS, SFTP), because the
limit sits **above** the driver chain (local, LDAP, recovery, whatever comes
next). Nothing to switch on: it is on by default.

| What | Rule |
|---|---|
| **Per account** | 5 wrong attempts within 10 minutes lock the *identifier typed* (trimmed, lower-case) - whether or not an account by that name exists, so the reply to "how many tries are left" cannot be used to find out which names are real. On a [multi-tenant](MULTI-TENANCY.md#realms-which-tenant-a-sign-in-is-for) install the tenant **realm** leads the key - `acme/alex`, whether it was typed in the Realm field, written as `acme/alex` over SFTP, or named by acme's own address - so `acme/alex`, `beta/alex` and the platform's `alex` are three counters. A realm nobody has is counted like a name nobody owns. |
| **Per address** | 10 wrong attempts within 10 minutes lock the client address. |
| **Lock length** | 1 minute, doubling with each lock in a row, up to 15 minutes. The doubling is forgotten after a success or a quiet spell. |
| **While locked** | Even the **right** password is refused (`429` + `Retry-After` on the web and WebDAV; a plain refusal on FTPS / SFTP), and the refused attempt is not counted. A cached protocol credential is refused too - otherwise a lock would be a guessing oracle. |
| **What is told** | Every wrong attempt says how many tries are left and at which failure the lock falls; a lock says when it opens. Same words for a name nobody owns. In the reader's language. |
| **What counts** | A wrong password, and a wrong second-factor code. Not: the form asking for the code, a right password on a disabled account, an API token (not a password). |
| **What resets** | A success resets the *account's* counter - never the address's, so one valid login between guesses cannot launder a spray. A protocol's cached credential and an API token do not reset it either (a busy client would wipe the counter with every request). |

![The sign-in form after a wrong password: how many tries are left](https://filex.sh/shots/loginsecurity/login-remaining-1440.961ed40adfd4.png)

![The sign-in form on a locked account: the lock counted down on its button](https://filex.sh/shots/loginsecurity/login-locked-1440.fafb1136af30.png)

**The IP allow-list** (`login.ip_allowlist`) is the way back in. An address on it is
exempt from the per-address limit, and may sign in to **any** locked account (the
password must still be right - the lock is not applied to *that address*, and it is
not lifted for anybody else). Its wrong attempts are audited and counted against
nobody. **No account is privileged**: the bootstrap administrator is limited like
everyone else, so put your office or VPN address on the list *before* you need it.
`your_ip` on `GET /api/admin/login-security` shows the address filex sees you from.
The one exception is a **public demo's shared account**
([Demo mode](#demo-mode)): its password is printed on the sign-in page, so it is
counted per address only.

Locking somebody out by typing their name wrongly is possible - that is what a lock
is. The escalating length keeps the cost on the attacker's side, and the allow-list
and the admin *Unlock* are the remedy.

**Counters are decided in memory and written through to the database**
(`login_throttle`). At start filex reads the table into memory - a lock placed
before a restart or a deploy is in force after it, so neither hands out fresh
guesses - and from then on every attempt is decided from memory, and every change
is written to the table as it happens. A write the database refuses (a full disk,
a lost connection) does **not** change the decision: the limit goes on from memory -
neither failing open nor locking everybody out - logs the failure, and writes the
counter again with the next write that succeeds or at the next sweep. A table
that cannot be read at start leaves an empty memory and the default settings,
and is read again at the next sweep.

**The sweep runs every minute on its own clock**, from the moment the server
starts until it stops - not on the traffic, so a quiet instance is kept as
tidy as a busy one. Each minute it reads the settings again (below), writes
what the database is owed, releases the locks that ran out (auditing each),
drops counters idle for a day, and prunes the table. The memory holds at most
100 000 counters (past that, idle ones go first, then the oldest - a counter
with no lock in force before a lock). Listing the locks on the page runs the
sweep too.

⚠ **Several instances sharing one database:** the **settings** are shared -
each instance reads them again every minute, so a change saved on one is in
force on all of them **within a minute**. The **counters are per instance**:
each counts in its own memory and reads the table only at start. With N
instances a guesser spread across all of them gets, in the worst case, **N ×
the limit** of wrong attempts per account (and per address); a lock one
instance places is not seen by the others until they restart, and the table
holds whichever instance wrote a counter last. If that matters, run the limit
on one instance (or in front of all of them).

**Audit trail** (Audit page → filter `login.`, or `GET /api/admin/login-security/attempts`,
the page's *Recent sign-in events*):
`login.failed` (every wrong attempt: identifier, address, door, reason, tries left),
`login.locked`, `login.unlocked` (a lock that ran out - noticed at the next attempt
for that account or address, or by the minute's sweep - or an administrator's
unlock), `login.allowlist_pass` (an allow-listed address got into a locked account),
and `login_security.update` (a change to the settings below). An administrator's
unlock and settings change keep their name whichever door they came through -
the panel, an admin API key, an MCP tool, the generic settings API - and the row
says which (`via`); each is in the trail once. Never a password.

Settings, edited through
[`/api/admin/login-security`](BACKEND.md#admin-sign-in-security) (the **Sign-in
security** page), instance-wide - on a multi-tenant install the page and its API
are the platform operator's (`403 supertenant_only` for a tenant's
administrator). They are held in memory like the counters: read
from the database at start and again every minute, and a save writes the
database **and** puts the value in force at once on the instance that saved it -
the next attempt decides with it, no restart - and on every other instance within
a minute. A database read that fails keeps the last value known (the default when
there is none). A `login.*` key written through the generic settings API reaches
the running limit the same way.

![Admin → Sign-in security: the limit, allowed addresses, trusted proxies, the locks and the sign-in trail](https://filex.sh/shots/loginsecurity/login-security-1440.fa0a391a5c19.png)

| Key | Default | Range |
|---|---|---|
| `login.throttle_enabled` | `true` | Off = nothing is counted. |
| `login.account_max_fails` | `5` | 1 - 1000 |
| `login.ip_max_fails` | `10` | 1 - 10000 |
| `login.window_seconds` | `600` | 10 - 86400 |
| `login.lock_base_seconds` | `60` | 1 - 86400 |
| `login.lock_max_seconds` | `900` | 1 - 86400 (never below the base) |
| `login.ip_allowlist` | empty | Addresses and CIDR networks. |
| `login.trusted_proxies` | empty (→ `FILEX_TRUSTED_PROXIES`, then `auto`) | Addresses, CIDR networks, the words `auto` ([worked out from where filex runs](#trusted-proxies)), `loopback`, `private`, `link-local` (one class of address each, as `net/netip` defines it), `none`. On the page `auto` is a switch that shows what it resolved to and why, the three classes are three switches and the addresses a list of their own; a peer that sends forwarded addresses without being trusted is named there, with a one-click **Trust**. |

**Not covered:** S3 (a signed request proves an HMAC over a random secret - there is
no password to guess), NFS (an export is bound to an address, not a password), API
tokens (256-bit), and the *current password* prompts on a signed-in account's
profile.

---

## Zero-touch seeding

These variables **seed the database once, on first boot, only when the target
record is absent.** They let a fresh `docker compose up` / `helm install` come up
fully configured from env alone - no admin-UI clicks. Once a record exists, later
operator edits in the UI **always win**; changing the env afterwards does **not**
re-seed or overwrite. (OIDC/LDAP/header auth are read live from env every boot and
so are configured in [Authentication](#authentication), not here.)

**First admin** - created if the user table is empty:

| Env var | Default | Description |
|---|---|---|
| `FILEX_ADMIN_EMAIL` | `admin@local` | Email of the seeded admin account. Its username is `admin` either way (reserved for this one account). |
| `FILEX_ADMIN_PASSWORD` | *(random, printed once)* | Password for that admin. Omit both to get a random `admin@local` (see [INSTALLATION.md → first run](INSTALLATION.md#first-run)). |

**SMTP** (mailer) - seeded when host, port and from are all set:

| Env var | Description |
|---|---|
| `FILEX_SMTP_HOST` | SMTP server host. |
| `FILEX_SMTP_PORT` | SMTP server port. |
| `FILEX_SMTP_USERNAME` | Auth username (optional). |
| `FILEX_SMTP_PASSWORD` | Auth password (optional). |
| `FILEX_SMTP_FROM` | From address on outbound mail. |
| `FILEX_SMTP_TLS` | `starttls` · `tls` · `none`. |

**Branding, trash & drafts:**

| Env var | Description |
|---|---|
| `FILEX_SITE_NAME` | Instance display name shown in the UI. |
| `FILEX_TRASH_RETENTION_DAYS` | Days to keep trashed items before purge (see [TRASH-VERSIONING.md](TRASH-VERSIONING.md)). |
| `FILEX_SHARE_MAX_TTL` | Longest life a **new** share link may be given - `7`, `7d` or `168h`; `0` = no ceiling. Seeds `share.max_ttl_days` (default 7) once; afterwards the admin **Protection** page owns it. Existing links are never changed (see [SHARING.md](SHARING.md)). |
| `FILEX_DRAFTS_LIMIT` | How many drafts (new documents not saved yet) one person may keep, `1`-`1000`. Seeds `drafts.limit` (default 50) once; afterwards the admin **Protection** page owns it (see [PROTECTION.md → Drafts](PROTECTION.md#drafts)). |

**Default storage** - seeds one initial storage when **no storage exists yet**, so
a fresh install already has a working place for files. Leave
`FILEX_DEFAULT_STORAGE_DRIVER` empty to seed nothing. (See [STORAGE.md](STORAGE.md)
for the storage model.)

| Env var | Applies to | Description |
|---|---|---|
| `FILEX_DEFAULT_STORAGE_DRIVER` | all | Which driver to seed; empty = seed no storage. `local` and `s3` have dedicated variables below; **any other built-in driver** (`sftp`, `webdav`, `ftp`, `smb`) is seeded by supplying its configuration as JSON in `FILEX_DEFAULT_STORAGE_CONFIG`. |
| `FILEX_DEFAULT_STORAGE_CONFIG` | all | The driver's configuration as **raw JSON**, e.g. `{"host":"…","user":"…"}`. Required for a driver that has no dedicated variables, and accepted for `local`/`s3` too, where it **replaces** them rather than merging. Invalid JSON seeds nothing and logs a warning. |
| `FILEX_DEFAULT_STORAGE_NAME` | both | Display name / top-level folder label. |
| `FILEX_DEFAULT_STORAGE_MOUNT` | both | Logical mount point (default `/`). |
| `FILEX_DEFAULT_STORAGE_PATH` | local | On-disk directory to serve. |
| `FILEX_DEFAULT_STORAGE_S3_BUCKET` | s3 | Bucket name. |
| `FILEX_DEFAULT_STORAGE_S3_PREFIX` | s3 | Key prefix = storage root (keep non-empty - root guard). |
| `FILEX_DEFAULT_STORAGE_S3_ENDPOINT` | s3 | Custom endpoint (MinIO/R2/Hetzner …); omit for AWS. |
| `FILEX_DEFAULT_STORAGE_S3_REGION` | s3 | e.g. `us-east-1`; `auto` for R2/MinIO. |
| `FILEX_DEFAULT_STORAGE_S3_ACCESS_KEY` | s3 | Access key. |
| `FILEX_DEFAULT_STORAGE_S3_SECRET_KEY` | s3 | Secret key. |
| `FILEX_DEFAULT_STORAGE_S3_PATH_STYLE` | s3 | `true` for path-style addressing (MinIO/Hetzner/B2/R2). |

---

## External services

Each is optional - an empty URL disables it. Set via env, `external_services.*`,
**or the admin UI** (*Settings → External services*).

The value the running process uses is always the one in the `external_services`
table, read on every request, so a change made in the admin UI takes effect
without a restart. Env and `config.yaml` are declarative configuration **for**
that table: a service they name is re-asserted onto its row at every boot, so
editing `FILEX_ONLYOFFICE_URL` and restarting still works - and an admin-UI edit
to such a service applies now but is reverted at the next start.
`GET /api/admin/external` returns `env_managed: true` for those, and the UI
labels them. Switching such a service off in the admin UI is the same kind of
edit: it is off everywhere at once (editor, office thumbnails, the apps'
office engine) and on again after the next restart; to switch it off for good,
remove `FILEX_ONLYOFFICE_URL` (or `FILEX_DRAWIO_URL`, or its `config.yaml`
key).

| Env var | Description |
|---|---|
| `FILEX_ONLYOFFICE_URL` | OnlyOffice Document Server URL (see [ONLYOFFICE.md](ONLYOFFICE.md)) |
| `FILEX_ONLYOFFICE_JWT` | Shared JWT secret - must match the Document Server |
| `FILEX_ONLYOFFICE_CALLBACK_URL` | Address the Document Server uses to reach filex; empty means `FILEX_PUBLIC_URL`. Only needed when the browser's address and the container's address differ |
| `FILEX_ONLYOFFICE_FRAME_ORIGIN` | The Document Server's own origin (`https://docs.example.com`), whose reverse proxy sends `/filex-frame/*` to filex: the editor's `api.js` then runs in a frame there instead of in filex's page ([ONLYOFFICE.md → The editor in a frame of its own](ONLYOFFICE.md#the-editor-in-a-frame-of-its-own)). The same site as filex is fine. On that host filex answers `/filex-frame/editor` and nothing else. Read at start (not on the admin page); a value that is not an origin, filex's own or `FILEX_APP_UI_ORIGIN` stops the server. YAML: `external_services.onlyoffice.frame_origin` |
| `FILEX_ONLYOFFICE_LANG` | The editor's language: `auto` (each person's own filex language) or a language the editor offers (`de`, `fr`, `tr`, `pt-PT`, `zh-TW`, ...) for everybody. Re-asserted onto the ONLYOFFICE row at every boot like the URL (an admin-UI edit lasts until the next start); unset, *External services → ONLYOFFICE → Editor language* decides, `auto` by default. A value the editor does not offer is logged and ignored. YAML: `external_services.onlyoffice.editor_lang` ([ONLYOFFICE.md → The editor's language](ONLYOFFICE.md#the-editors-language)) |

The same Document Server draws the thumbnails of office documents (0.50,
[thumbnails.md → Office through OnlyOffice](thumbnails.md#office-through-onlyoffice));
its two limits are the `FILEX_THUMBS_OFFICE_*` settings under [Thumbnails](#thumbnails).
| `FILEX_DRAWIO_URL` | Drawio embed URL (diagram editing) |

> **Mermaid needs no service.** Mermaid diagrams render entirely client-side in
> the browser via a bundled `mermaid` library - there is nothing to deploy and no
> URL to set (the former `FILEX_MERMAID_URL` was removed).

---

## Protocol endpoints (S3 · SFTP · FTPS · NFS · WebDAV)

filex can be reached as five protocols besides HTTP. Full picture, including
which credential each one takes and the traps that cost real time:
[PROTOCOLS.md](./PROTOCOLS.md).

⚠ **S3 and `/dav` are ON by default; the other three are OFF.** The two that are
on do not open a port of their own and refuse every unsigned or unauthenticated
request, and a credential still has to be minted before anything can reach them.
The three that open a **listener** stay off until asked for - a port nobody
requested is not something to open for them.

| Env var | Default | Description |
|---|---|---|
| `FILEX_SECRET_KEY` | - | ⚠⚠ **Required once anybody mints an S3 access key.** SigV4 verifies a request by recomputing an HMAC chain from the secret, so unlike a token it cannot be hashed - filex seals it with AES-GCM under this key. With no key configured, minting an access key **fails** rather than storing plaintext. **Changing or losing it stops every existing access key from verifying**, so treat it like the database, not like a password: back it up, do not rotate it casually. Any 32+ random bytes. It also seals the [Web Push](NOTIFICATIONS.md#web-push) key: without it push notifications stay off, and a changed key turns them off until the push key is rotated (Admin → Notifications). |
| `FILEX_S3` | `1` | The S3-compatible endpoint. Set `0` to switch it off. |
| `FILEX_S3_DOMAIN` | - | Dedicated host for the endpoint, e.g. `s3.example.com`, which also enables virtual-hosted addressing (`bucket.s3.example.com`). Empty leaves the endpoint under `/s3`, path-style only. ⚠⚠ **Never point this at the host the app itself serves** - the whole site then answers as S3. ⚠ Setting it needs a wildcard A record **and** a wildcard certificate for `*.<domain>`; without both, current SDKs (which default to virtual-hosted) fail at TLS with nothing that names the cause. |
| `FILEX_SFTP` | `0` | The SFTP endpoint. Its own TCP listener, not a route. |
| `FILEX_SFTP_ADDR` | `:2022` | Listen address. 2022 by convention - sftpgo and `rclone serve sftp` use it, while 2222 reads as "SSH in a container". |
| `FILEX_SFTP_HOST_KEY_DIR` | `<data>/ssh` | Where the server's host keys live. ⚠ **It must survive a rebuild**: regenerating host keys gives every user the "REMOTE HOST IDENTIFICATION HAS CHANGED" warning, which is indistinguishable from an attack. |
| `FILEX_SFTP_BANNER` | - | Text shown before authentication. |
| `FILEX_FTPS` | `0` | The FTPS endpoint. ⚠ Explicit TLS is **mandatory** on both channels and there is no switch to relax it: plain FTP sends the password in the clear and the file after it. |
| `FILEX_FTPS_ADDR` | `:2121` | Control channel. Port 21 needs root. |
| `FILEX_FTPS_PASV_MIN` / `_MAX` | `30000` / `30100` | The passive data-port range. ⚠⚠ **Open it on the firewall too** - a blocked range makes every transfer *hang* with no error on either side, which is the classic FTP failure and impossible to guess at from the client end. |
| `FILEX_FTPS_PUBLIC_HOST` | - | The address to advertise for passive connections, when it differs from what the server sees (NAT, Docker). A host name or an IPv4 address; a name is resolved at startup, because the PASV reply itself can only carry a dotted quad. ⚠ IPv6 has no PASV representation at all - a v6-only deployment must rely on EPSV, which needs no address and so needs no setting. |
| `FILEX_FTPS_CERT` / `_KEY` | - | TLS certificate. Absent, filex generates a self-signed one and the guide says so, so nobody has to discover it from a client warning. **Re-read when the files change** (mtime/size checked on every handshake), so a real, auto-renewing certificate - Caddy's, certbot's - can be mounted read-only and is picked up by the next connection with no restart. A renewal that lands half-written keeps the previous certificate serving and logs a warning. |
| `FILEX_FTPS_BANNER` | - | Greeting line shown on connect. |
| `FILEX_NFS` | `0` | The NFSv3 endpoint. ⚠⚠ **NFSv3 is unencrypted** - anyone who can read the traffic sees the files, and anyone who learns an export path can mount it. LAN or VPN only; for anything off-LAN the answer is `filex mount`. |
| `FILEX_NFS_ADDR` | `:2049` | Listen address. ⚠ There is no portmapper on 111, so clients must be given `port=` and `mountport=` explicitly - and Windows' "Client for NFS" cannot say that at all, so it only works on the standard 2049. |
| `FILEX_DAV` | `1` | The WebDAV endpoint at `/dav`. |

---

## Storage plugins

Drivers that live outside the binary - see [PLUGINS.md](PLUGINS.md).

| Variable | Default | Meaning |
|---|---|---|
| `FILEX_PLUGINS_DISABLED` | `0`, **`1` in demo mode** | Turns the whole subsystem off: nothing under `<data-dir>/plugins` is launched, no remote plugin is contacted, and the admin API answers 503 saying so. The subsystem is on by default, because a plugin is only ever installed by an admin - but an operator hardening a shared instance may not want the admin role to include “run a program on the server”. ⚠⚠ **`FILEX_DEMO_MODE` moves the default to `1`, so plugins are off on a demo.** A demo publishes an admin login - that is what a demo is - and this API is admin-only, so on a demo "admin-only" means anybody; installing a plugin runs an uploaded program on the host. Setting the variable yourself wins in either direction: `FILEX_PLUGINS_DISABLED=0` turns them back on for a demo, deliberately. |
| `FILEX_PLUGIN_CONFORMANCE` | `enforce` | `enforce` · `warn` · `off`. filex **probes every capability a plugin declares** - at install against the plugin's own throwaway area, and again when a storage on it is saved, against that real configuration. `enforce` refuses a plugin that fails its own claims and refuses to save a storage on it. `warn` registers it anyway and keeps the report - for somebody *writing* a plugin, never for a shared instance: the cost of a broken claim is paid by the user, who meets an operation the UI offered and reads the failure as filex being broken. `off` skips both gates. Anything unrecognised falls back to `enforce`. |
| `FILEX_PLUGIN_TRUSTED_KEYS` | - | Comma-separated ed25519 **public** keys (hex or standard base64) allowed to sign a plugin. Set any key and an unsigned or badly signed binary is refused at install *and* at upgrade, the signature is kept beside the binary (`<binary>.sig`) and **verified again at every start** - a plugin installed before the keys were set is refused at its next start until it is reinstalled with a signature - and the admin API reports `requires_signature: true` so the UI asks for the signature up front. Left empty, no signature is asked for and the recorded sha256 is all an install carries. A storage plugin build's signature is over its name, version, platform and sha256 (0.55; the old sha256-only form is taken in 0.55 with a warning and refused from 0.56), and a build a store's `artifact` key signed is taken through that store's link or while the store still lists that version - [PLUGINS.md → What is signed](PLUGINS.md#what-is-signed). See [PLUGINS.md → Signed plugins](PLUGINS.md#signed-plugins). |
| `FILEX_PLUGIN_MAX_INFLIGHT` | `10` | Concurrent operations allowed **per plugin**. A caller that waits 5 s for a slot is refused rather than queued, and counted as `outcome="busy"` in [the metrics](METRICS.md#storage-plugins) - a sizing signal, not a bug. Raise it for a fast local plugin, lower it to keep a slow remote one from occupying the server. `0` or nonsense keeps the default. |
| `FILEX_PLUGIN_LOOPBACK_SOURCES` | `0` | ⚠ **Development and tests only - never on a server.** `1` lets plugin and app **downloads** reach this machine (`127.0.0.0/8`, `::1`) and nothing else that is private: a storage plugin from a URL or a source, an app's manifest, module and interface bundle, an install request, the update check, an app store (its keys, its install links, a license check). By default every one of them reaches public addresses only (see below). The end-to-end tests set it, because they serve plugin sources from a small server on 127.0.0.1. With it on, a manifest that names a loopback address makes filex send GET requests to its own services, so the server says so in its log at start. An app's own requests (`http_request`, `asset_fetch`) never follow it. YAML: `plugin_loopback_sources`. |
| `FILEX_APP_PLUGINS_DISABLED` | `0`, **`1` in demo mode** | Turns the [app plugin](APP-PLUGINS.md) runtime off: nothing under `<data-dir>/app-plugins` is loaded, the file menu shows no app rows, the admin tab explains why. Demo mode moves the default to `1` for the same reason as storage plugins; `FILEX_APP_PLUGINS_DISABLED=0` turns them back on deliberately. |
| `FILEX_APP_PLUGIN_MAX_INPUT_MB` | `256` | Per-file ceiling on what one app job may read. |
| `FILEX_APP_PLUGIN_MAX_OUTPUT_MB` | `512` | Per-file ceiling on what one app job may produce. |
| `FILEX_APP_PLUGIN_MAX_WASM_MB` | `64` | Largest module an app install accepts. `FILEX_PLUGIN_TRUSTED_KEYS` applies to app modules too. |
| `FILEX_APP_UI_ORIGIN` | - | Serves apps' own interfaces from an [origin of their own](APP-PLUGINS.md#an-origin-of-their-own), e.g. `https://apps.example-usercontent.com` (a scheme and a host; another registrable domain than filex's). The proxy sends that host to filex too; filex answers only the interface route there and refuses it on its own host. With it and no `FILEX_ONLYOFFICE_FRAME_ORIGIN`, the ONLYOFFICE editor's frame is served there too ([ONLYOFFICE.md → The editor in a frame of its own](ONLYOFFICE.md#the-editor-in-a-frame-of-its-own)). Empty (the default): interfaces are served from filex's origin, opaque by sandbox. A value that is not an origin, or filex's own, stops the server. YAML: `app_ui_origin`. |
| `FILEX_APP_PLUGIN_MAX_UI_MB` | `128` | Largest [interface package](APP-PLUGINS.md#an-apps-own-interface) (the app's `ui` zip) an install accepts; the files inside it are capped too (512 MiB unpacked, 20 000 files, 64 MiB per file). |
| `FILEX_APP_PLUGIN_UPDATE_CHECK` | `1` | The daily check that asks every installed app's source (its GitHub repository or address) for a newer version the running filex can run, and **tells the administrators** - it installs nothing: every newer version waits for an administrator's approval ([APP-PLUGINS.md → Updates](APP-PLUGINS.md#updates)). The same switch covers the storage plugins' [update sources](PLUGINS.md#updates-from-a-source). `0` = no request leaves the server for it - what an air-gapped install wants; **Check for updates** on the Apps tab still asks when pressed. A demo never checks. The time of the last check is stored, so a restart neither skips a day nor checks at every boot. YAML: `app_plugin_update_check`. |
| `FILEX_APP_STORE_URLS` | - | Comma-separated app store **origins** (`https://store.example`, no path) trusted by configuration: their install links and license answers are accepted without an administrator approving the store first, signed with the keys in `FILEX_APP_STORE_KEYS` and no others. Set, it is an **allow list** (0.52.0): any other store is refused (`403 store_not_allowed`), not even trusted on first use. Unset, a store is trusted only by an administrator who compared its key fingerprints on its first install link ([APP-PLUGINS.md → Trusted stores](APP-PLUGINS.md#trusted-stores)). A configured store has no remove button in the panel. YAML: `app_store_urls`. |
| `FILEX_APP_STORE_KEYS` | - | Comma-separated ed25519 **public** keys (hex or standard base64) of the stores in `FILEX_APP_STORE_URLS`, each optionally prefixed with the one use it may sign for: `index:<key>` (install links) or `license:<key>` (license answers); a bare key may sign either. filex reads the store's `/v1/keys.json` for the key ids and accepts a key only when its material is listed here; a key the store publishes that is not here is refused (`store_key_not_configured`), never put to an administrator. Ignored (with a log line) when `FILEX_APP_STORE_URLS` names no store. Not the same setting as `FILEX_PLUGIN_TRUSTED_KEYS`, which checks modules. YAML: `app_store_keys`. |
| `FILEX_APP_GITHUB_RAW_BASE` | `https://raw.githubusercontent.com` | Where a GitHub install (and a [store link](APP-PLUGINS.md#installing-from-a-store), which names a GitHub repository) reads a repository's files: `<base>/<owner>/<name>/<ref>/filex-app.json`. A mirror of GitHub's raw host for an install that cannot reach it, or the end-to-end tests' fake GitHub. The download guard applies to it like to every address: a loopback mirror needs `FILEX_PLUGIN_LOOPBACK_SOURCES`, a private one is refused. YAML: `app_github_raw_base`. |
| `FILEX_APP_CLOCK` | - | ⚠ **Screenshots and tests only - never on a server.** An RFC 3339 instant (`2026-09-15T10:30:00Z`): the apps' clock reads it when filex starts and runs at real speed from there - the time inside every app module, and the host's own times it hands an app (a lock's end, a link's expiry, a certificate's not-after, a wake-up's window). The screenshot scenes set it so that what an app writes into its own words ("requested on", "frozen until", the date under a signature) is dated on their clock. Everything filex stores and compares stays on the real clock. The server says in its log at start that the clock is moved. No YAML key. |
| `FILEX_SECRET_KEY` | - | Also seals a **remote** plugin's bearer token. Without it, registering a remote plugin is refused rather than stored in plaintext (binary plugins get a token minted per start, which is never stored). It seals a [paid app's license key](APP-PLUGINS.md#paid-apps) too: without it a paid app cannot be given a key (`license_key_invalid`), and changing it leaves the stored keys unreadable (enter them again). |
| `FILEX_PLUGIN_REQUEST_TTL_DAYS` | `14` | How long an [install request](APP-PLUGINS.md#install-requests) - what an API key leaves instead of installing an app or a storage plugin - waits for an administrator before it expires. Checked hourly and whenever the requests are read. YAML: `plugin_request_ttl_days`. |

**App stores: `FILEX_APP_STORE_URLS` is an allow list** (0.52.0). Set, it
is the whole list of app stores this filex takes install links and license
answers from: any other store is refused (`403 store_not_allowed`) before
anything is read from it - its keys, its links, its license checks - and no
administrator can trust it on first use. A store an administrator trusted
before the list was set is refused (and no longer listed) too, and a list
whose entries all fail to parse admits no store at all - so does a value that
names none (`FILEX_APP_STORE_URLS=" , "`): any value given is the list. Unset
(not in the environment, or empty), a store is
trusted by an administrator who compares its key fingerprints on its first
link, as before. A store link also names the filex it was made for
(`filex_origin`): filex holds it to the origin of [`FILEX_PUBLIC_URL`](#public-url), or -
unset - to the origin the request arrived at, so set `FILEX_PUBLIC_URL` when
a proxy in front of filex rewrites the host. A paid app's grace (the store
unreachable) loses an hour at every start after an unclean stop - a crash, a
kill, a power cut - and nothing at a clean one
([APP-PLUGINS.md → Paid apps](APP-PLUGINS.md#paid-apps)).

Installed binaries live in `<data-dir>/plugins/<name>/` (with the detached
signature beside each as `<binary>.sig`, when one was supplied), and a plugin's
socket in `<data-dir>/plugins/<name>/run/` (directory mode 0700, socket 0600).
A launched plugin does **not** inherit filex's environment: it sees `PATH`,
`HOME`, the temp and locale variables (plus the handful Windows needs) and the
`FILEX_PLUGIN_*` variables filex sets for it - never `FILEX_SECRET_KEY`, the
database DSN or anything else filex itself was configured with. In multi-tenant
mode the admin surface is supertenant-only.

Two network rules apply to plugins that are not uploaded: a plugin installed
**from a URL** may only be fetched from a public host (private, loopback and
link-local targets are refused after DNS and on every redirect, at most five
of them, and a redirect from `https://` to plain `http://` is refused), and a
**remote** plugin is spoken to over TLS unless its address is on the private
network - plain `http://` is accepted only for loopback, link-local, RFC 1918
and ULA targets, checked again at every connection, and a remote's redirects
are not followed. See [PLUGINS.md → Install one](PLUGINS.md#install-one).
Apps are downloaded by the same guarded client, under the same address rule
([APP-PLUGINS.md → Install one](APP-PLUGINS.md#install-one)); only
`FILEX_PLUGIN_LOOPBACK_SOURCES` widens it, and only to this machine.

What is **not** configurable from the environment, and is cheaper to read here
than to search for:

- **The waiting and deadline figures around the ceiling**: 5 seconds waiting for
  a slot, 60 seconds on metadata calls. Streaming reads and writes are
  deliberately exempt - a 20 GB upload is legitimately slow, and a deadline
  there turns a working transfer into a failed one.
- **The plugin log rate limit** (50 lines/s, burst 200). Dropped lines are
  reported once per window rather than silently lost.

> ⚠ Signature enforcement is **off until you set `FILEX_PLUGIN_TRUSTED_KEYS`**.
> Until then a plugin is accepted on the strength of its sha256, which proves
> only that the file has not changed since it arrived - never who it came from.
> Once a key is set it applies to what is already installed too: a binary with
> no stored signature is refused at its next start (`signature required …
> reinstall`).

---

## Storage sync

> ⚠ **A single failed sync run is not an error.** A run that cannot list the
> backend (a 503/504 from an object store under load, say) is logged at INFO and
> the catalogue is refreshed on the next tick. Only **three consecutive**
> failures are reported as a warning - that is a storage genuinely not
> answering, not a hiccup. The message carries the streak, and recovery is
> logged too.

Fallback cadence for the [sync worker](STORAGE.md#sync), used by storages that
do not set their own `sync_interval_s`. A storage that does set one wins.

| Env var | Default | Description |
|---|---|---|
| `FILEX_SYNC_INTERVAL` | `15m` | Go duration (`30s`, `15m`, `1h`); `0` or a negative value is "unset". The 5 s floor applies to a storage's own `sync_interval_s` (under 5 s it falls back to this variable), not to this variable. |

> ⚠ The variable is `FILEX_SYNC_INTERVAL`, **not** `FILEX_SYNC_DEFAULT_INTERVAL`.
> An unparseable value logs a warning at boot and keeps the default rather than
> failing silently.

> ⚠ **`FILEX_SYNC_WORKERS` was removed in v0.20.** It was documented here as
> "concurrent storage sync workers" and parsed into a config field that
> **nothing ever read** - there is no pool to size. The sync worker runs one
> goroutine per enabled storage and always has, so concurrency is the number of
> enabled storages and there is no knob to turn. Setting the variable now has
> no effect and produces no error; delete it from your environment.
>
> (`FILEX_SYNC_INTERVAL` was equally dead until v0.20 - parsed, then read by
> nobody, while the real fallback was a hardcoded `15m` that happened to match
> the documented default. It is wired up now.)

### Lazy catalogue settings

A local storage with `sync_mode: lazy` ([STORAGE.md → Lazy
catalogue](STORAGE.md#lazy-catalogue), design in
[LAZY-CATALOGUE.md](LAZY-CATALOGUE.md)) reads three more keys from its own
`config`. They are per storage, not environment variables: the storage form
draws them under **Sync mode** when *Lazy catalog* is chosen, and the admin API
refuses a value outside its bounds (400).

| Key | Default | Bounds | Meaning |
|---|---|---|---|
| `lazy_fill` | `background` | `background` · `on_open` | **Catalog behavior.** `background`: the folder somebody opens is catalogued first and a slow background pass catalogues the rest. `on_open`: only the folders people open are catalogued; search, folder sizes and usage say so. |
| `lazy_max_watches` | `1024` | 1 - 1,000,000 | How many opened folders are watched for changes made outside filex. Past it, the folder opened longest ago stops being watched and is checked again the next time somebody opens it. |
| `lazy_watch_ttl` | `60` | 1 - 10,080 (minutes) | A folder nobody has opened for this long stops being watched. |

The storage's own `sync_interval_s` (**Scan every**) is how old an unwatched
folder's listing may get before the background pass, or a connected desktop
sync pair, checks it again.

> ⚠ **Linux: the kernel's own watch limit.** Each watched folder is one inotify
> watch, and `fs.inotify.max_user_watches` caps them for the whole user (8,192
> on some distributions). When the kernel refuses one, filex logs it once and
> treats the budget as full - nothing breaks, folders are simply checked on
> open instead of watched. Raise the sysctl or lower `lazy_max_watches` if you
> see that line.

---

## Uploads (staged / resumable)

Large uploads land in filex's own staging area first and are transferred to the
storage backend by a background job, so they survive a dropped connection and
work on every driver. See [UPLOADS.md](UPLOADS.md).

| Env var | Default | Description |
|---|---|---|
| `FILEX_UPLOAD_STAGING_DIR` | `<data_dir>/uploads` | Where in-flight upload parts live. |
| `FILEX_UPLOAD_CHUNK_SIZE` | `8388608` (8 MiB) | Default part size when the client does not request one. |
| `FILEX_UPLOAD_STAGING_TTL` | `24h` | Idle time before the sweeper removes an abandoned staging directory. Staging that belongs to a file you delete **permanently** is released at once and does not wait for this. |

> ⚠ The whole object passes through the staging directory - put it on a
> filesystem with room for the largest upload you expect. `begin` refuses when
> less than `size × 1.2` is free.

---

## File operations (copy · move · delete)

Copy, move and delete from the explorer (`POST /api/files/copy`, `/move`,
`/delete`, `/api/files/ops`) are queued jobs, run one at a time in the order
they were submitted.

| Env var | Default | Description |
|---|---|---|
| `FILEX_OPS_DELETE_WORKERS` | `4` | How many items of **one delete job** are put in the trash at the same time. Below 1 means the default. |

Only the items inside a delete job overlap; jobs still run one after another,
so a paste queued behind a delete still waits for it - it just waits much less.
On an object store every trashed file is several round trips, so a delete is
almost all waiting on the network: one item at a time, a job of tens of
thousands of files ran for hours. Raise the value when the backend takes it;
lower it (to `1` for the old behaviour) when it answers with throttling errors.
An item inside another item of the same job (a file and its folder) is left to
the folder, so the folder goes to the trash whole.

---
## Archives

Archive executable paths and the private workspace are process configuration;
the live format and resource policy is managed under **Settings → Archives**.
See [ARCHIVES.md](ARCHIVES.md) for the provider matrix and security model.

| Env var | Default | Description |
|---|---|---|
| `FILEX_ARCHIVE_7Z_BIN` | `7zz` or `7z` on `PATH` | 7-Zip executable used for ZIP/7z/TAR creation, encryption and 7z/XZ extraction, and RAR extraction when that 7-Zip reads RAR (Alpine's package does not; see [ARCHIVES.md](ARCHIVES.md)). Plain ZIP and the TAR family are read by filex itself. Version 25.01 or newer is required. |
| `FILEX_ARCHIVE_WORK_DIR` | `<data-dir>/archive-work` | Private local staging directory for provider input and output. Needs room for one archive's expanded size; fast local storage, preferably not the database's file system. |

```yaml
archive:
  sevenzip_bin: /usr/local/bin/7zz
  work_dir: /var/lib/filex/archive-work
```

## Antivirus (ClamAV)

Optional. filex scans written files with ClamAV, reached either through a local
**binary** or through a **clamd daemon** over TCP or a unix socket; see
[PROTECTION.md](PROTECTION.md#antivirus-clamav) for how to choose between them
and for what a clean, infected or failed scan does.

⚠⚠ **Every setting below now lives in the database**, edited on
**Settings → Protection** in the admin UI. The environment variables still
exist, but only as a **seed**:

> **The env var applies on a boot where the setting has no stored row yet, and
> never again.** Once a row exists - written by that first-boot seeding, or by
> an admin on the Protection page - the variable is inert. Editing it in your
> compose file and restarting the container changes nothing, and no warning is
> printed, because there is nothing unusual about a setting that already has a
> value. Change them on the Protection page.

The seed is applied per setting, not per family: a setting whose row does not
exist yet is seeded from its variable even if its siblings already have rows.
So a deployment upgrading from an older filex keeps exactly the values it had.

| Env var | Where it lives now | Default | Description |
|---|---|---|---|
| `FILEX_CLAMAV` | **seed → database** (`antivirus.enabled`) | on | The on/off switch. `0`/`false` disables scanning. ⚠⚠ It used to be an env-only kill switch and is now a **seed**: an install that had `FILEX_CLAMAV=0` keeps scanning off across the upgrade, because the value is seeded into the row on the first boot - but from then on the switch is on the Protection page and editing the variable does nothing. ⚠ Takes effect at the **next restart**, in both directions (see below). |
| `FILEX_CLAMAV_MODE` | **seed → database** (`antivirus.mode`) | `binary` | How ClamAV is reached: `binary` (run `clamdscan`/`clamscan` from filex's own `$PATH`) or `daemon` (talk to a running clamd). ⚠ Setting `FILEX_CLAMAV_ADDR` without this seeds `daemon`, because a compose file that names a clamd container and comes up in binary mode is a deployment that looks configured and scans nothing. ⚠ Next restart. |
| `FILEX_CLAMAV_ADDR` | **seed → database** (`antivirus.clamd_addr`) | unset | The clamd address for `daemon` mode: `clamav:3310`, `tcp://127.0.0.1:3310`, a bare host (port 3310 is assumed), or a unix socket path such as `/var/run/clamav/clamd.ctl`. The bytes are streamed to the daemon, so **filex and clamd need no shared filesystem** - which is what makes ClamAV-in-its-own-container work. An unparseable address is refused when you save it, not discovered at scan time. ⚠ Next restart. |
| `FILEX_CLAMAV_BIN` | environment | unset | Absolute path to `clamdscan`/`clamscan` for `binary` mode; authoritative when set, and an invalid path disables scanning rather than silently falling back to `$PATH`. ⚠ The one that stays an environment concern **deliberately**: this is a path the server *executes*, so an admin-writable field for it would turn an admin account into arbitrary command execution as the filex process. A clamd *address* is a dial target, never executed, which is why that one is editable. |
| `FILEX_CLAMAV_MAX` | **seed → database** (`antivirus.max_scan_mb`) | 100 MB | Largest file that gets scanned; bigger files are skipped, not failed. ⚠ The variable is in **bytes**, the stored setting is in **megabytes** - the number an admin types. The conversion at seed time rounds **up**, so an upgrade never shrinks an existing ceiling. Range 1-10240 MB. |
| `FILEX_CLAMAV_SAVE_WINDOW_MINUTES` | **seed → database** (`antivirus.save_scan_window_minutes`) | 30 | How long a save from the built-in text editor waits before its scan is queued; further saves to the same file inside that window join it instead of queueing more. Range 2-60 minutes; `0` is refused. See below. |

### ⚠⚠ Three of them take effect at the next restart

The switch, the mode and the clamd address are stored the moment you save them
and are **in force only after filex restarts - in both directions.** Turning
scanning on does not start it and turning it off does not stop it until then,
because the scan pipeline is wired once at boot: the queue handler is
registered and the write paths are handed an enqueue function only when
scanning resolved as available.

This is a deliberate choice, not an oversight. Making "off" immediate while
"on" stayed deferred - the shape a naive implementation falls into, since
suppressing work is easy and creating a worker-pool handler at runtime is not -
would give you a control that is sometimes live and sometimes not, with no way
to tell which from looking at it. The admin page states the deferral at the
moment you change it and keeps a "restart required" band up until the restart
has happened.

The scan-size ceiling and the save-scan window are **not** deferred: they are
read per file, so they apply to the next file scanned. They tune a pipeline
that is already running; the three above decide whether it exists.

### Reaching a clamd container

```yaml
services:
  filex:
    environment:
      FILEX_CLAMAV_ADDR: "clamav:3310"
  clamav:
    image: clamav/clamav:latest
    volumes:
      - clamav-db:/var/lib/clamav
volumes:
  clamav-db:
```

The Protection page shows whether clamd actually answered (`reachable`, with
the error text beside it). ⚠ A daemon filex cannot reach never produces a clean
verdict - the scan fails and retries on the queue - but nothing would *say* so
without that field, so check it after changing an address.

### The editor save-scan window

A file **created** in filex's text editor is scanned immediately, exactly like
an upload. A **save** over a file that already exists schedules one scan
`FILEX_CLAMAV_SAVE_WINDOW_MINUTES` from now, and every further save inside that
window is absorbed into it - so a burst of Ctrl+S costs exactly one scan, and
that scan reads the file as it stands when it runs, not the content of the save
that scheduled it.

The trade is worth stating, because the default looks arbitrary otherwise: one
scan per file per half-hour of editing, paid for by the file sitting unscanned
for up to that long after its **last** save. Shorter means more scans and less
exposure; longer means fewer scans and more.

- **Minimum 2 minutes.** Below that the window stops coalescing anything - a
  save every couple of minutes is still one editing session - while multiplying
  scans.
- **Maximum 1 hour.** The window *is* the time an infected file written through
  the editor stays live. Longer is not a tuning preference, it is a different
  security posture.
- **`0` is not accepted.** It reads equally as "scan every save" and "never
  scan", which are opposite behaviours, and the second would silently re-open
  the gap this window lives inside. To turn scanning off, use the switch on the
  Protection page (`antivirus.enabled`), which is explicit about being a
  separate control and about applying at the next restart.

A value outside the range is **refused** when you save it on the Protection
page, so you find out while you are looking at the field. A value that is
somehow already stored out of range - written by hand, or by an older build -
is **clamped to the nearest bound on read**, with one WARN line naming the
value in force, rather than refusing to boot over a scan interval.

⚠ The delay is a row in filex's operation queue (`ops_queue.not_before`), not a
timer inside the process, so a restart or a deploy mid-window does not lose the
pending scan. Changing the window never rewrites scans already scheduled: they
keep the time they were given, and only scans scheduled afterwards use the new
value.

---

## Versioning on overwrite

Before any destructive-write surface replaces an existing file, it snapshots
the bytes being replaced into version history - and refuses the write rather
than let it through unrecorded when that snapshot cannot be taken. The full
per-surface list, including the two batch surfaces that skip a refused member
instead of failing the whole request, is in
[TRASH-VERSIONING.md](TRASH-VERSIONING.md#what-triggers-a-snapshot).

| Env var | Default | Description |
|---|---|---|
| `FILEX_VERSIONS_ON_OVERWRITE` | `1` | Master switch for the pre-write snapshot guard. `0`/`false` turns it off for a deployment whose storage backend cannot afford the extra write - those writes then proceed unsnapshotted, exactly as they did before the guard existed, and filex logs one WARN at boot naming the state. `1`/`true` turns it back on, including over a `config.yaml` that set `versions_on_overwrite: false`: like every other boolean here, the env var wins in both directions. |
| `FILEX_VERSIONS_FAIL_OPEN` | `0` | Lets a snapshot **failure** fall through to the write instead of refusing it, logging `overwrite proceeding without a snapshot` (naming the storage and path) each time. Default off, because `Service.Snapshot`'s contract is that a caller must not proceed past a failed snapshot. It exists for one specific bind: if the object store fills up, snapshots start failing and the fail-closed default then refuses **every** overwrite on the instance - desktop sync, WebDAV, OnlyOffice saves, the browser. filex logs a WARN at boot while this is on, so the state is visible without reading the config. |

⚠ These are independent. `FILEX_VERSIONS_ON_OVERWRITE=0` skips the snapshot
attempt entirely; `FILEX_VERSIONS_FAIL_OPEN=1` still attempts it and still
records one when it succeeds. Note also that `versions.keep_n = 0` does **not**
disable versioning - `0` means "unlimited", and the snapshot path falls back to
its built-in retention default.

In `config.yaml`: `versions_on_overwrite: false` and `versions_fail_open: true`,
both top-level alongside `upload:` / `cache:`.

---

## End-to-end encryption: the vault

The third encryption level, the **vault** ([format](E2E-VAULT-FORMAT.md)), is
built - the web app, the desktop app, the embeds, `filex decrypt` and
`filex vault mount` open it - and **off** by default: turn it on where people
should be able to make one.

| Env var | Default | Description |
|---|---|---|
| `FILEX_E2E_VAULT` | `0` | Turns the vault on: the API under `/api/files/e2e/vault` ([BACKEND.md](BACKEND.md#vault-encryption-level-3)), `e2e_vault: true` in `GET /api/files/capabilities` (a client offers level 3 only then), and the rule that inside a vault folder only that API writes - every other door (the explorer, the queue, the agent API and MCP, archives, apps, the document server's save, WebDAV, S3, SFTP, FTPS, NFS) is refused there. Off, every vault route answers `404 VAULT_DISABLED`, the capability is `false`, and the doors do not look for vaults: a vault made while it was on is then an ordinary encrypted folder to them, so do not turn it off on an instance whose people use vaults. |

In `config.yaml`: `e2e_vault: true`, top-level. The vault's write lock lives in
the database (`vault_locks`, migration 00096), so every filex process on one
database shares it; nothing else needs configuring.

---

## Downloads from slow storage (prepared copies)

When a **big** file lives on a **slow** backend, filex fetches it to local disk
once, tells the user it is preparing (with a percentage), and then serves it -
and every later request - at local-disk speed, with full `Range` support. See
*Slow storage* in [STORAGE.md](STORAGE.md).

| Env var | Default | Description |
|---|---|---|
| `FILEX_CACHE` | `1` | Master switch. `0` disables prepared copies entirely. |
| `FILEX_CACHE_DIR` | `<data_dir>/cache` | Where prepared copies live. |
| `FILEX_CACHE_MIN_SIZE` | `67108864` (64 MiB) | Smallest file worth preparing. Below it, nothing is ever cached. |
| `FILEX_CACHE_MAX_BYTES` | `21474836480` (20 GiB) | **Global** ceiling on the cache directory, enforced with LRU eviction. |
| `FILEX_CACHE_SLOW_BPS` | `10485760` (10 MiB/s) | Measured throughput below which a storage counts as slow. |

A file is prepared only when it is **at least `MIN_SIZE`** *and* its storage is
slow - either flagged by you (`"slow": true` in the storage config) or measured
below `SLOW_BPS`. Small files, and files on storages that measure fast, are
served exactly as they were before: nothing is prepared and nobody waits.

> ⚠ The cap is not optional and cannot be set to "unlimited". When the cache is
> full of entries that are being read, a new file is simply **not** prepared and
> streams from the backend as before - filex will not exceed the ceiling to make
> room.

**The cache directory also holds folder-share ZIPs** (`<cache_dir>/sharezips`,
moved there from `<data_dir>/sharezips` on first start of v0.19.1+). They are
not covered by `FILEX_CACHE_MAX_BYTES`; they are bounded by their shares and by
two knobs of their own - an archive is deleted as soon as no active share can
serve it, and in any case after a week. See *Folder ZIPs are cached* in
[SHARING.md](SHARING.md).

| Env var | Default | Description |
|---|---|---|
| `FILEX_SHAREZIP_WARM_MAX_BYTES` | `2147483648` (2 GiB) | Largest folder (sum of file sizes) the background warmer pre-builds. Bigger folders are zipped on demand when a visitor clicks, never refused. `0` = no ceiling. |
| `FILEX_SHAREZIP_MAX_AGE` | `7d` | A cached archive older than this is swept even if its share is still live, and rebuilt when next needed. Go durations plus a `d` suffix. `0` = keep for the share's life. |

⚠ **Exclude `<data_dir>/cache` from your backups.** Everything under it is
regenerable, and a single folder-share archive can be tens of gigabytes.

---

## Thumbnails

| Env var | Default | Description |
|---|---|---|
| `FILEX_THUMBS_ENABLED` | `true` | Master switch. |
| `FILEX_THUMB_BACKFILL_ON_BOOT` | - | Set `once` to backfill missing thumbnails on startup. |
| `FILEX_THUMBS_SWEEP_INTERVAL` | `6h` | How often cached thumbnails whose node no longer exists are deleted (also once at boot). Once per boot the same worker also scales down pages cached at full size by a version before 0.41.0. `0` disables both. |
| `FILEX_THUMBS_SVG_MAX_MB` | `5` | **Seed** of the setting `thumbs.svg_max_mb` (1-64): the largest SVG drawn. Consumed on the first boot with no value; after that the setting is edited in **Settings** or **Admin → Tools → Thumbnail repair** and this variable is inert. See [thumbnails.md → SVG limits](thumbnails.md#svg-limits). |
| `FILEX_THUMBS_SVG_TIMEOUT` | `10` | **Seed** of `thumbs.svg_timeout_seconds` (1-120): the longest time one SVG may take to draw. Same seed-once rule. |
| `FILEX_THUMBS_OFFICE_MAX_MB` | `25` | **Seed** of `thumbs.office_max_mb` (1-100): the largest office document sent to OnlyOffice for a thumbnail; larger ones are marked *Too large*. Same seed-once rule. See [thumbnails.md → Office limits](thumbnails.md#office-limits). |
| `FILEX_THUMBS_OFFICE_SLOTS` | `1` | **Seed** of `thumbs.office_slots` (1-4): how many office documents one filex process has OnlyOffice draw at once (a Community Edition document server runs one converter, shared with the editors). Same seed-once rule. |
| `FILEX_THUMBS_FOLDER_PREVIEWS` | `true` | **Seed** of `thumbs.folder_previews`: folder cards show up to three pictures from inside, and a resting pointer lists what a folder holds. Same seed-once rule; switched in **Settings** or on the repair tab. See [thumbnails.md → Folder previews](thumbnails.md#folder-previews). |
| `FILEX_THUMBS_URL_TTL` | `24h` | How long a stamped `thumb_url` (`?exp=&sig=`) stays valid. The stamp is what lets a bare `<img src>` fetch a preview with no header and no cookie; an authenticated caller never needs one. ⚠ `0` means *use the default*, not "never expires". See [thumbnails.md → Serving](thumbnails.md#serving). |

Kinds and their tool requirements (auto-detected on `PATH`; the default Docker
image bundles all of them, `Dockerfile.slim` deliberately none): images =
built-in; SVG = built-in (resvg in WebAssembly, since 0.50; `rsvg-convert`
is only its fallback); HEIC/HEIF/AVIF = ImageMagick with libheif
(`imagemagick-heic` on Alpine), and for HEIC libheif's HEVC decoder
(`libheif-plugin-libde265` on Debian/Ubuntu, which apt does not install on its
own; checked at boot by decoding a sample, `thumbs.heic`); video/audio = `ffmpeg`; PDF = `gs` or
`pdftoppm`; office = no tool here: the OnlyOffice document server draws office
documents (0.50, configured as below; without it they get no thumbnail and the
repair tab says "ONLYOFFICE is not configured"). A missing tool turns
that kind off: its files are recorded as *skipped* with the tool named, show a
plain type tile, and are listed under **Admin → Tools → Thumbnail repair**
until the tool is installed (restart, then the next listing or a *Fix* repair
draws them). The server also says so **once at boot**, at WARN, naming each
unavailable kind and the package that draws it
(`thumbs: some previews will fall back to a plain type tile…`). The cache dir is `config.yaml`
only (`thumbs.cache_dir`); which kinds are drawn follows the tools installed, not a list in the
configuration. See [thumbnails.md](thumbnails.md).

---

## Search

| Env var | Default | Description |
|---|---|---|
| `FILEX_SEARCH_ENABLED` | `true` | Embedded Bleve full-text index. |
| `FILEX_SEARCH_CONTENT` | `true` | Extract text from files into the index (content search). `0` stops enqueueing extraction; text already indexed keeps matching. |
| `FILEX_SEARCH_CONTENT_MAX` | `5242880` | Source files larger than this are never content-extracted. |
| `FILEX_SEARCH_AUTO_REBUILD` | `true` | Rebuild the index in the background at startup when it was written by an older document schema. The replacement is built alongside the live index and swapped in, so search never goes dark. `0` leaves the index alone and reports `needs_rebuild` instead. |
| `FILEX_TESSERACT_BIN` | unset (`tesseract` on `$PATH`) | Path to the `tesseract` binary used to OCR images (png/jpg/webp/tiff) into the content index. **When set it is authoritative:** a value that does not resolve turns OCR *off* rather than falling back to `$PATH` - an explicit path that is wrong is an operator mistake worth surfacing, not something to paper over. Unset, filex looks for `tesseract` on `$PATH`; if there is none, images are skipped silently and the capabilities endpoint reports `ocr: false`. |

Index path is `config.yaml` only (`search.index_path`, default
`<data_dir>/search.bleve`). See [SEARCH.md](SEARCH.md).

---

## Usage & cost

The *Admin → Usage & cost* page reads five rows of the settings table, written
from that page or through `PATCH /api/admin/settings`. Like the
[ClamAV family](#antivirus-clamav), each is **seeded from its variable on first
boot only** - once a row exists the variable is inert and the page's value wins.
There is no `config.yaml` block.

| Variable | Setting | What |
|---|---|---|
| `FILEX_USAGE_PROVIDER` | `usage.provider` | `b2`, or empty for none |
| `FILEX_USAGE_REPORT_STORAGE` | `usage.report_storage` | the filex storage whose root is the provider's report bucket |
| `FILEX_USAGE_ACCOUNT_ID` | `usage.account_id` | the provider account id (optional for B2) |
| `FILEX_USAGE_PREFIX` | `usage.prefix` | folder inside that storage holding the dated reports |
| `FILEX_USAGE_PRICING` | `usage.pricing` | the price table, as JSON |

See [USAGE.md](USAGE.md).

---

## Queue

| Env var | Default | Description |
|---|---|---|
| `FILEX_QUEUE_DRIVER` | *(follows `FILEX_DB_DRIVER`)* | `sqlite` · `postgres` · `mysql` · `redis` |
| `FILEX_QUEUE_DSN` | - | `postgres://…` or `redis://…` (ignored when the queue shares the app DB) |
| `FILEX_QUEUE_WORKERS` | `4` | Worker pool size. |
| `FILEX_QUEUE_ENABLED` | `true` | Disable to run without the persistent queue. |

⚠ Unset means **the queue follows the database**, not "sqlite". It used to mean
sqlite whatever the database was, which on a Postgres install sent SQLite SQL
down the Postgres connection: a syntax error on every poll and no background job
ever run, on a server that looked healthy.

Use **redis** for multi-node deployments, or **postgres** with its own DSN
(postgres and mysql both claim with `SELECT … FOR UPDATE SKIP LOCKED`; redis
keeps its pending set in a sorted set and claims with a Lua script). Sharing the
application database is fine single-node.

All of them serve ops in the same order - `priority DESC`, then oldest first -
so a background sweep never overtakes a person's request whichever one you
run. ⚠ Switching **to** redis on an install that already ran the pre-v0.34.0
redis driver converts its pending list on startup, keeping every queued op;
switching back to that older build afterwards will not work.

---

## Notifications

| Env var | Default | Description |
|---|---|---|
| `FILEX_NOTIFY_ENABLED` | `true` | In-app bell + webhook. |
| `FILEX_WEBHOOK_URL` | - | The **legacy single** webhook: one JSON POST per event, no event filter. Empty = in-app only, plus whatever targets exist. |
| `FILEX_WEBHOOK_TOKEN` | - | Sent as `Authorization: Bearer` to that legacy webhook only. |
| `FILEX_WEBHOOK_LANG` | - | The language the legacy webhook's `title` and `body` are said in (`tr`, `en`, a language pack's). Empty = the instance's (`FILEX_DEFAULT_LOCALE`, else English). A webhook target sets its own in Admin → Webhooks ([Which language](NOTIFICATIONS.md#which-language)). |
| `FILEX_PUSH_ENABLED` | `true` | [Web Push](NOTIFICATIONS.md#web-push): what a person's bell tells them reaches their phone and browsers while filex is closed, once they turn it on for a device. ⚠ Needs `FILEX_SECRET_KEY` - the VAPID key pushes are signed with is made at the first start and stored sealed with it; without the key push stays off and says so. |
| `FILEX_PUSH_SUBJECT` | `FILEX_PUBLIC_URL` (https) | The contact a push service may write to about this server: `mailto:ops@example.com` or an https address. Without an https public address: `mailto:filex@<its host>`. |
| `FILEX_PUSH_HOSTS` | - | Push services accepted besides the browsers' own (Chrome, Firefox, Safari, Edge), comma separated; `*` accepts any https host that is a name, never an address. The sender connects directly, not through `HTTP_PROXY` / `HTTPS_PROXY`. |

⚠ These two are not the whole notification surface, and have not been for
several releases: **webhook v2 targets** are rows managed in *Admin → Webhooks*,
each with its own URL, its own signing secret and its own **per-event
allow-list**. One event produces one POST per destination, not one POST. There
is no environment variable for them.

⚠ From v0.34.0 a write that **replaced** an existing file emits `file.updated`
rather than `file.uploaded` - a behaviour change for anyone already subscribed
to `file.uploaded` in order to see edits.

The **notification digest** - optional, off out of the box (every kind is told
at once) - is not an environment variable either: how long a held kind waits
(1-15 minutes, default 1) and which kinds are held by default: an administrator sets it under **Admin →
Notifications → Notification digest**, per tenant on a multi-tenant install,
and each person chooses their own urgent kinds in their notification settings
([The digest](NOTIFICATIONS.md#the-digest)).

See [NOTIFICATIONS.md](NOTIFICATIONS.md).

---

## CORS

| Env var | Default | Description |
|---|---|---|
| `FILEX_CORS_ALLOWED_ORIGINS` | `*` | Comma list. Restrict when embedding the component from specific origins. It is also the one list of other origins that may **change** things with a visitor's session ([below](#requests-from-other-origins)); `*` grants that to nobody. An entry is an exact origin (`https://work.example.com`) or a pattern with one `*` (`https://*.example.com`). |

`allowed_methods` / `allowed_headers` are `config.yaml` only. Default allowed
headers: `Authorization, Content-Type, X-Filex-Pin, Content-Range, Range,
X-Filex-Accept-Prepare`. If you use API-token root confinement from a browser,
add `X-Filex-Token` / `X-Filex-Root`. `Content-Range` and `Retry-After` are
exposed to the page.

⚠ If you set `allowed_headers` yourself, keep **`Content-Range`** in it: every
chunk of an upload larger than the chunk size (8 MiB by default) is a `PUT`
carrying it, and a preflight that does not allow it makes the browser refuse
the chunk - small uploads keep working, which looks like a size limit rather
than CORS. (It was missing from the default before 0.41.1.) A same-origin
deployment is unaffected.

### Requests from other origins

A browser attaches filex's session cookie to every request it sends to filex,
whichever page sent it. So filex refuses a request that **changes** something
(any method but `GET`, `HEAD` and `OPTIONS`, and a WebSocket upgrade) when a
browser sent it from another origin with nothing but that ambient session:
the answer is `403` with `{"error": "cross_origin_refused", "message": …}`,
and nothing runs. It reads what the browser says about the request:
`Sec-Fetch-Site` first, then `Origin`, then `Referer`.

These pass, with no setting:

- **filex's own pages**: the same origin, which is each tenant's own host on a
  [multi-tenant](MULTI-TENANCY.md) install and the same host under a
  `FILEX_BASE_PATH`. The installed app (PWA) is filex's own page too.
- **filex's own address** (`FILEX_PUBLIC_URL`), so the platform's pages reach a
  tenant's host (the sign-in handoff).
- **A request with a key**: `Authorization: Bearer …` or `X-Filex-Token`. The
  desktop app, an embed with `auth: { kind: 'bearer' }`, an embed whose host
  proxies with a key, ShareX, MCP clients and the CLI all send one; a browser
  cannot put either header on a request to another origin unless the CORS
  answer allows it.
- **Links that carry their own credential**: share and file-request links
  (`/s/…`, `/d/…`, `/api/public/…`), upload tickets (`/u/…`), a WebSocket
  `ticket`, S3 (signed), the document server's callback and the desktop
  sign-in exchange.
- **A client that is not a browser** (no `Sec-Fetch-Site`, `Origin` or
  `Referer` at all): scripts, `curl -b`, WebDAV and S3 clients.
- **An origin in `FILEX_CORS_ALLOWED_ORIGINS`**, exactly or by its `*` pattern.
  This is the only place to name another origin that may write with a
  visitor's session; there is no second list.

These are refused:

- **Another origin on the same site.** `blog.example.com` and
  `files.example.com` share a site, so the browser sends the session cookie
  (`SameSite=Lax` stops only other sites); a page there is refused unless it is
  in `FILEX_CORS_ALLOWED_ORIGINS`. A `https://*.example.com` entry trusts every
  page on every subdomain: list the hosts you mean.
- **`*`**, the default, trusts nobody for this. It lets any page read what
  filex answers without credentials; it says nothing about writing with
  somebody's session.
- **`Origin: null`** (a sandboxed frame, a `file://` page).
- **The sign-in form posted from another origin**, with or without a session:
  a forged sign-in would put the visitor in an account the page chose, and
  what they upload next would land there. The same holds for the tenant
  sign-in handoff.

Three origins filex knows are deliberately **not** trusted:

- `FILEX_ONLYOFFICE_FRAME_ORIGIN`, normally the Document Server's own origin:
  the ONLYOFFICE editor's script runs there, in a frame of its own
  ([ONLYOFFICE.md → The editor in a frame of its own](ONLYOFFICE.md#the-editor-in-a-frame-of-its-own)).
  It is usually the same site as filex, so the browser sends the session
  cookie with its requests - and filex refuses every write that names it
  **even when a `FILEX_CORS_ALLOWED_ORIGINS` entry or wildcard covers it**,
  and sends it no CORS answer, so nothing filex answers is readable there.
  The editor never needs filex's API: the Document Server saves server to
  server.
- `FILEX_APP_UI_ORIGIN`: an app's interface runs sandboxed without
  `allow-same-origin`, so its requests say `Origin: null`, and it reaches filex
  through the explorer's bridge, never with the session. Trusting its host
  would change nothing today and would hand third-party app code the person's
  session the day that sandbox loosened. It is held like the frame origin -
  never trusted, no CORS answer, whatever the CORS list says - because the
  ONLYOFFICE editor's frame is served there when there is no frame origin, and
  that frame keeps its origin.
- `FILEX_FRAME_ANCESTORS`: a dashboard that frames filex needs nothing here.
  The framed page is filex's own, so its requests are same-origin; the
  dashboard's own page writing with the session is exactly what is refused.

A refused request that carried a live session writes an audit row,
`auth.cross_origin_refused`, naming the account (at most one a minute per
account), and a warning in the log.

**Cookies.** Every cookie filex sets is `HttpOnly`, so no script on any host
the browser sends one to can read it - the session (`filex_session`;
`SameSite=Lax`, `Secure` behind TLS, and on a multi-tenant install `Domain`
the parent domain, so every sibling host receives it), a share's PIN unlock,
and an SSO sign-in's state and flow (host-only). filex sets no cookie a script
must read; a sibling host can neither read the session nor, through the rules
above, use it.

---

## Security headers and framing

filex itself sends these on its answers, so every install has them whether or
not a reverse proxy adds anything:

| Header | Value | On |
|---|---|---|
| `X-Content-Type-Options` | `nosniff` | every answer |
| `Referrer-Policy` | `strict-origin-when-cross-origin` | every answer |
| `Content-Security-Policy` | `frame-ancestors 'self'` + `FILEX_FRAME_ANCESTORS`; `frame-src` filex's own `/_appui/`, `/z/` and (0.55) `/_print/` + the editors below | filex's own pages (HTML) |

| Env var | Default | Description |
|---|---|---|
| `FILEX_FRAME_ANCESTORS` | *(empty: filex only)* | The pages, besides filex itself, that may show filex's pages inside a frame - a home dashboard such as Homarr or Organizr - and the sites the web component runs on when its apps print (the print page, below). Origins separated by commas or spaces: `https://home.example.com`, `http://10.0.0.5:7575`, `https://*.example.com` for every subdomain, or `*` for any page (the protection is then off). `frame_ancestors` in `config.yaml`. |

⚠ A value that is not an origin (`home.example.com` with no scheme, a path, a
quote) stops the server at startup with a message saying what to write; the
startup log names the origins in force.

What it does and does not touch:

- **A dashboard that shows filex in an `<iframe>`** now needs its origin in
  `FILEX_FRAME_ANCESTORS`; without it the browser shows its own "refused to
  connect" page in the frame. The same applies to a share link (`/s/…`)
  shown inside another site's frame.
- **The web component is not a frame.** `<filex-explorer>` embedded in another
  site ([INTEGRATION.md](INTEGRATION.md)) runs in the host's page and is not
  affected, and neither is what it shows from filex there - a PDF, an image, a
  download: `frame-ancestors` is sent on filex's pages, not on a file's own
  bytes. One exception, below: **printing from an app** there needs the
  site in `FILEX_FRAME_ANCESTORS`.
- **The desktop app** opens filex in its own window, not in a frame, and is
  not affected; its page may frame the print page without being listed.
- **The print page** (`/_print/`, 0.55: filex prints an app's PDF from it,
  [APP-PLUGINS-API.md → Printing a PDF](APP-PLUGINS-API.md#printing-a-pdf-uiprint-055))
  says who may frame it itself: `frame-ancestors 'self'`, the origins of
  `FILEX_FRAME_ANCESTORS` and the desktop app's page (`app://filex`). The
  explorer frames it to print, so where the web component runs in another
  site, that site must be listed for its apps to print; elsewhere the page
  is refused and the app is told `unavailable` (an app such as the office
  editor then hands the PDF over as a download). Why not any site: the page
  frames a PDF it is handed under filex's address and opens the print
  dialog, so a page every site could frame would let any site show a PDF of
  its own as filex's. It prints only on the person's click on its own
  button, wherever it is framed.
- `Content-Security-Policy` here is `frame-ancestors` and `frame-src`; a
  handler that sets a policy of its own keeps it, and gets `frame-ancestors`
  added when it names none (never `frame-src`: that could only widen it) -
  except an app interface's page, which names none on purpose
  ([APP-PLUGINS-API.md → An app's own interface](APP-PLUGINS-API.md#an-apps-own-interface-v4)),
  and the print page, which names its own (above).

**What filex's pages may frame (`frame-src`).** Three paths of filex itself -
the app interfaces (`<host>/_appui/`, left out when they have an origin of
their own), the frame a download starts in (`<host>/z/`) and the page an
app's PDF is printed from (`<host>/_print/`, 0.55; it frames that PDF as a
`blob:` of its own, which filex's pages never do), named for the
host the page was asked on and for the host of `FILEX_PUBLIC_URL` when it is
set - and the origin of each editor that is switched on under *Admin →
External services* (draw.io, ONLYOFFICE) - read live, so an edit there
applies to the next page without a restart. Not `'self'`: an app's
interface could otherwise navigate its own frame to any page of filex.
An editor served from filex's own origin (a proxy path such as
`/onlyoffice/`) is named by its origin, which frames filex whole again -
give editors a host of their own. Nothing else: not another site, not a
`data:` or `blob:` document. This is what keeps an [app's
interface](APP-PLUGINS.md#an-apps-own-interface) inside its frame: a
sandboxed interface that tries to navigate itself to its author's server, or
to a document of its own making, is refused by the page around it. An editor
served from another address than the one configured (a proxy that rewrites
it) is refused too - configure the address the browser uses.

---

## Error reporting

Optional Sentry-wire reporting (works with self-hosted GlitchTip). Empty DSN =
off.

| Env var | Default | Description |
|---|---|---|
| `FILEX_SENTRY_DSN` | - | Sentry/GlitchTip DSN. |
| `FILEX_SENTRY_ENVIRONMENT` | - | Tag events (e.g. `production`). |

**What is reported.** filex does not sprinkle capture calls through the code;
it forwards its own log. Every `ERROR` record and every `WARN` record that
carries an `err` attribute becomes one event, grouped by the log message
(`thumb generate failed`, `ops: step failed`, …). `INFO` never reaches the
tracker - a listener closed by a shutdown, for instance, is an `INFO` line.

**Log attributes travel with the event.** Everything the log line carries -
`path`, `err`, `driver`, `attempt`, `storage`, `node`, … plus `source`
(`file.go:line` of the call site) - is attached twice:

- as **tags** when the value is short (first line, up to 120 characters), so
  the issue list and the tag filter show *which* file, driver or error;
- in full under the **`log` context** of the event (an ffmpeg transcript in
  `err` is kept whole there, untruncated).

Values whose key names a credential (`token`, `password`, `secret`,
`authorization`, `cookie`, `credential`, `private`, `*_key`, `key`) are
replaced with `[filtered]` before either. Log sites do not log secrets on
purpose, but a wrapped HTTP error or a config dump can carry one, and the key
is the cheapest reliable signal; an S3 object key is caught by the same rule -
the `path` beside it says the same thing.

---

## Updates

Release awareness and - on installs that own their binary - self-upgrade.
Full behaviour, including everything that is checked before anything is applied
automatically, is in [UPDATES.md](./UPDATES.md).

| Env var | Default | Description |
|---|---|---|
| `FILEX_UPDATE_CHECK` | `1` | Master switch for the periodic check. `0` = no outbound request, ever. |
| `FILEX_UPDATE_POLICY` | `manual` | `off` · `manual` · `patch` · `minor` - how far filex may move on its own. The default announces only. |
| `AUTO_UPGRADE` | - | Shorthand for `FILEX_UPDATE_POLICY=patch` (z-moves apply themselves; y and x are announced). An explicit policy set afterwards wins. |
| `FILEX_UPDATE_CHANNEL` | `stable` | Release channel. |
| `FILEX_UPDATE_MANIFEST_URL` | `https://filex.sh/updates/stable.json` | Release index location - point it at your own mirror for air-gapped installs. |
| `FILEX_UPDATE_WINDOW` | - | Daily maintenance window for automatic upgrades, e.g. `03:00-05:00` (server local time). Empty = any time. |
| `FILEX_UPDATE_INTERVAL` | `24h` | Time between checks. Anything under `1h` is raised to `1h`. |
| `FILEX_UPDATE_PRE_COMMAND` | - | Shell command run immediately before a self-upgrade (database dump for postgres/mysql). **A non-zero exit aborts the upgrade.** sqlite is snapshotted by filex itself with `VACUUM INTO`. |
| `FILEX_INSTALL_MODE` | auto-detected | `binary`, `docker` or `package` (or the package manager by name: `homebrew`, `winget`, `snap`), when detection is wrong for your setup. Container installs never self-apply - the image layer is immutable, so a replaced binary reverts at the next `up`. Package-manager installs never self-apply either - the manager owns the binary and upgrades it ([UPDATES.md](./UPDATES.md#package-manager-installs)). |
| `FILEX_SYSTEMD_UNIT` | auto-detected | The unit `systemctl restart` is run against after a self-upgrade, e.g. `filex.service`. Consulted only when the process runs under systemd; left unset, the unit is read from `/proc/self/cgroup`. Set it when that detection names the wrong unit. |

`FILEX_UPDATE_TARGET` is the one variable in this table filex **sets for you**
rather than reads: it is exported into the environment of
`FILEX_UPDATE_PRE_COMMAND` and holds the version about to be installed
(e.g. `v0.34.2`). Setting it in the server's own environment changes nothing -
it is overwritten for the child process. Use it to name your dump after the
version it precedes:

```sh
FILEX_UPDATE_PRE_COMMAND='pg_dump -Fc filex > /backups/filex-pre-$FILEX_UPDATE_TARGET.dump'
```

---

## Demo mode

| Env var | Default | Description |
|---|---|---|
| `FILEX_DEMO_MODE` | `false` | Renders an "Open the demo" CTA on the login page. |
| `FILEX_DEMO_USER` | `demo@demo.com` | The account the CTA logs in as. |
| `FILEX_DEMO_PASS` | `demo` | The password the CTA submits, and the one printed under the button. **These are published credentials** - on a demo instance the server returns them in `/api/capabilities` so the page can use them, which is the whole point of a demo. Neither variable creates or changes the account: keep the DB user in sync yourself. |

⚠⚠ Demo mode is not only a login page. Because the credentials are published,
"admin-only" means "public" on that instance, so the whole admin surface goes
**read-only**: every write under `/api/admin/…` and `/api/ai/admin/…` - and
every `admin_*` MCP tool that writes - is refused with 403, as are changes to
the shared account itself (password, email, TOTP). Reads still work - a demo
exists to show the operator surfaces - and every address in them is masked
(`hidden on the demo`): the visitors' on the audit log, the dashboard and the
Sign-in security page's trail and locks, and the operator's allow-list and
trusted proxies; so is a name typed at a sign-in door that is no account of
the instance (a visitor's own e-mail). Nothing here runs unless `FILEX_DEMO_MODE` is on.
⚠ The [sign-in attempt limit](#sign-in-attempt-limits) counts the shared
account **per address only**: it is exempt from the per-account lock, so
wrong passwords typed for it cannot lock it for everybody, while one address
guessing is still stopped at the per-address limit (and the form counts down
that address's tries, never the account's). The account is the one
`FILEX_DEMO_USER` names, looked up as a sign-in looks it up - by its e-mail and
by its username, in its own tenant's realm on a multi-tenant install
(`acme/demo`, not `beta/demo`) - on every door (the web form, WebDAV, FTPS,
SFTP). An account created after the start is followed within a minute.
Every other account, administrators included, is limited as always.
Full list: [DEMO.md](DEMO.md).

---

## config.yaml

Every field is optional; pass with `--config /path/to/config.yaml`.

```yaml
listen: "0.0.0.0:5212"
public_url: "https://files.example.com"
# base_path: "/filex"   # served under a sub-path - see "Base path"; unset = the public URL's path
data_dir: "/data"
trusted_proxies: ""                # FILEX_TRUSTED_PROXIES - empty = auto (see "Trusted proxies")

log:   { level: info, format: text }
db:    { driver: sqlite, dsn: "" }

auth:
  drivers: [local, oidc]           # local | oidc | ldap | proxy_header (windows / pam: Admin → Identity providers only)
  login_email_token: ""            # FILEX_OS_LOGIN_EMAIL_TOKEN - set once, never change (empty = local)
  oidc:
    issuer: https://id.example.com/realms/main
    client_id: filex
    client_secret: "…"
    redirect_url: https://files.example.com/api/auth/oidc/callback
    role_claim: realm_access.roles
    admin_group: filex-admin
    auto_create: true              # first sign-in opens an account (the first sign-in rule)
    allowed_groups: ""             # only these role_claim groups get one
    # trust_email: false           # unset = the 0.50 upgrade's answer; true = every address counts as verified
  ldap:                            # also overridable via FILEX_LDAP_*
    url: ldaps://ldap.example.com
    bind_dn: "cn=svc,dc=example,dc=com"
    bind_password: "…"
    base_dn: "ou=people,dc=example,dc=com"
    user_filter: "(mail=%s)"
    email_attr: mail
    start_tls: false
    ca_file: ""                    # PEM bundle for a private CA (optional)
    protocol_login: true           # directory passwords on WebDAV/SFTP/FTPS
    provider: ""                   # multi-tenant only - tenant slug for logins that name no tenant
    auto_create: true
    allowed_groups: ""
    group_attr: ""                 # memberOf when empty; read once allowed_groups is set
    show_refusal_reason: false     # tell a refused first sign-in why (confirms the password)
    group_filter: ""               # LDAP links: find a person's groups by a search, e.g. "(member=%s)"
    group_base_dn: ""              # where that search runs (default base_dn)
    sync_interval: ""              # directory sync on its own, e.g. 6h (off by default)
    sync_filter: ""                # who sync lists (default: user_filter with *)
    sync_disable_missing: false    # switch off accounts the directory stopped listing
    sync_groups: true              # bring every directory group in as a filex group
    sync_group_filter: ""          # which ones (default: every group)
    email_domains: ""              # only these e-mail domains sign in through it
  header_proxy:                    # trust an auth proxy - also FILEX_HEADER_*
    email_header: X-Auth-Email
    group_header: X-Auth-Roles
    trusted_ips: ["10.0.0.0/8"]
    admin_group: admin
    provider: ""                   # multi-tenant only - tenant slug when Host matches none
    auto_create: true
    allowed_groups: ""             # judged against group_header (recorded as groups either way)

external_services:
  onlyoffice: { url: https://office.example.com, jwt_secret: "…" }
  drawio:     { url: "" }        # mermaid renders client-side - no service

sync:   { default_interval: 15m }   # ⚠ no `workers` key - see Storage sync
thumbs: { enabled: true, cache_dir: "", url_ttl: 24h }
search: { enabled: true, index_path: "" }
cors:
  allowed_origins: ["*"]           # also the only origins that may write with a visitor's session; `*` trusts none for that
  allowed_methods: [GET, POST, PUT, DELETE, PATCH, OPTIONS]
  allowed_headers: [Authorization, Content-Type, X-Filex-Pin, Content-Range, Range, X-Filex-Accept-Prepare]  # the defaults; drop Content-Range and large uploads break
frame_ancestors: []                # FILEX_FRAME_ANCESTORS - pages that may frame filex's pages
queue:  { driver: "", dsn: "", workers: 4, enabled: true }   # "" follows the database - never pin sqlite on Postgres/MySQL
ops:    { delete_workers: 4 }        # items of one delete job trashed at once
notify: { enabled: true, webhook_url: "", webhook_token: "" }
demo:   { mode: false, user: demo@demo.com, pass: demo }
sentry: { dsn: "", environment: "" }
versions_on_overwrite: true       # pre-write snapshot guard - see Versioning on overwrite
versions_fail_open: false         # let a failed snapshot through instead of refusing the write

seed:                              # first-boot only-if-absent (see Zero-touch seeding)
  admin_email: ""
  admin_password: ""
  site_name: ""
  trash_retention_days: ""
  share_max_ttl_days: ""           # FILEX_SHARE_MAX_TTL - "7", "7d", "0" = no ceiling
  smtp:    { host: "", port: "", username: "", password: "", from: "", tls: starttls }
  storage: { driver: "", name: "", mount_path: "/", path: "",
             config: "",                # raw JSON for drivers with no fields of their own
             bucket: "", prefix: "", endpoint: "", region: "",
             access_key: "", secret_key: "", path_style: false }
```

Some settings (branding, default thumbnail policy) live in the database
`settings` table and are managed from the admin UI, not here.

---

## Gotchas

- `FILEX_SYNC_DEFAULT_INTERVAL` is **not** read - the correct var is
  `FILEX_SYNC_INTERVAL`.
- `FILEX_SYNC_WORKERS` is **not** read either, and was removed in v0.20: there
  is no worker pool to size. See [Storage sync](#storage-sync).
- ⚠⚠ **`FILEX_CLAMAV*` are seeds, not overrides.** Every one of them except
  `FILEX_CLAMAV_BIN` is read on a boot where its setting has no stored row yet,
  and never again - so once filex has booted once, editing the variable and
  restarting **changes nothing, silently**, because a setting that already has a
  value is not unusual and nothing warns. The stored values are edited on
  *Settings → Protection*. See [Antivirus (ClamAV)](#antivirus-clamav).
- `FILEX_DEFAULT_STORAGE_*` only takes effect on a **fresh** install (it seeds a
  default storage when none exists yet); it never edits or replaces an existing
  storage. See [Zero-touch seeding](#zero-touch-seeding).
- Booleans accept only `"1"` / `"true"`; anything else is false - except the
  three `*_AUTO_CREATE` switches, which are false only for `0` / `false` / `no` /
  `off` (see the note at the top of this page).
- LDAP and proxy-header now have env vars (`FILEX_LDAP_*` / `FILEX_HEADER_*`); the
  env value overrides the matching `config.yaml` field.
- `FILEX_INSTALLATION_*` settings are frozen after the first boot and make filex
  refuse to start if they change - that is deliberate, not a bug. The single
  exception is turning escrow **on**, which can be
  [adopted](#adopting-escrow-later) with `FILEX_INSTALLATION_E2E_ESCROW_ADOPT=1`
  and is not retroactive. See
  [Install-time settings](#install-time-settings-filex_installation_).
- `FILEX_TRUSTED_PROXIES` is a **fallback**: once the `login.trusted_proxies`
  setting holds a list (saved on the **Sign-in security** page), editing the
  variable and restarting changes nothing. A bad entry stops the server at
  start. A CDN or any other public hop in front of your proxy must be listed,
  or every visitor shares the edge's address - and its sign-in counter. It is
  a different list from `FILEX_HEADER_TRUSTED_IPS`, which says who may send the
  proxy-header driver its identity headers.
- Unset, the trusted proxies are **`auto`** ([Trusted proxies](#trusted-proxies)):
  this machine and, in a container, the container networks it is attached to -
  never their gateways, never the LAN. A proxy **on the host** reaching filex's
  container through a published port, or **on another machine**, is not
  trusted until it is listed: until then every visitor resolves to the proxy's
  address. The **Sign-in security** page names such a peer and offers to add it.
  A list that should keep the automatic part says so: `auto, 192.0.2.10`.
- `FILEX_OS_LOGIN_EMAIL_TOKEN` is part of the address of every account known
  only by a login name (LDAP without a mail attribute, Windows, Linux PAM):
  changing it later makes a second account of each such person.
