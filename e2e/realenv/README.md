# realenv - filex against real third-party servers

The main e2e suite runs filex on its own: an identity provider, an ACME
authority, a reverse proxy or a document server, where a spec needs one, is
stubbed or faked. This directory measures the same features against the real
thing, each in a Docker container:

| Stage | Real servers | What is measured | Spec |
|---|---|---|---|
| `tls` | [Pebble](https://github.com/letsencrypt/pebble) (Let's Encrypt's ACME test server) and pebble-challtestsrv (its DNS) | `FILEX_TLS_MODE=acme`: a certificate through TLS-ALPN-01 (no HTTP listener at all), through HTTP-01 (TLS on a port the authority never dials), for a tenant's CNAME'd own domain and its platform subdomain; renewal of a three-minute certificate; a stranger name refused; a served name whose DNS does not lead to filex and an untrusted authority, each with what the log says | `tests/acme.spec.ts` |
| `tls` | Caddy (on-demand TLS from the same Pebble) | `FILEX_TLS_MODE=proxy`: Caddy's `ask` gets yes only for names filex serves, `get_certificate http` hands Caddy a tenant's own certificate, and the hooks answer nobody but the proxy itself (`clientip.ProxyItself`): a request Caddy forwards and one straight to filex both get 404 | `tests/proxy.spec.ts` |
| `sso` | Keycloak (dev mode, realms from `stack/keycloak`), OpenLDAP (ldaps with the run's own CA) | the SSO buttons on the sign-in page in a browser, a realm-less sign-in (the platform's tenant), a sign-in with a realm, a tenant's own OIDC with no address of its own (`/api/auth/oidc/start?instance=&realm=`), the first sign-in rule, group links and the admin group, a tenant's own LDAP through the guarded client and the guard's refusals | `tests/sso.spec.ts` |
| `office` | ONLYOFFICE Document Server | issue #80's S2 (JWT off on the document server) and S5 (a secret that does not match): the "Test now" answer, the admin page, the editor, the document server's own log | `tests/office.spec.ts` |

## Run

Linux with Docker, a filex binary for linux (`pnpm run build:all` makes
`bin/filex`) and the e2e dependencies installed (`pnpm install`):

```bash
e2e/realenv/run.sh                 # every stage, one after another
e2e/realenv/run.sh tls             # one stage
REALENV_PULL=1 e2e/realenv/run.sh  # pull the images it does not find
```

Each stage creates two networks of its own (`fxre-priv`, a private /24, and
`fxre-pub`, a /24 the guarded client treats as public, TEST-NET-3 by
default), starts its servers and a Playwright container on them, runs its
specs in that container and removes everything again. Nothing is published on
the host. Results, logs and screenshots land in `e2e/realenv/.work`
(`logs/specs-<stage>.log`, `logs/<container>.log`, `results/<stage>/`).

**Skipped, and said so.** A stage whose images are not on the machine is
skipped with the image named (`SKIPPED tls: image ghcr.io/letsencrypt/pebble:latest
is not on this machine`), and so is every stage without Docker, without a
filex binary or without the e2e dependencies; the script then exits 0. The
specs also skip themselves, with the stage they need, when they are run
outside `run.sh`.

| Variable | Default | |
|---|---|---|
| `REALENV_BIN` | `bin/filex` | the filex binary under test (linux) |
| `REALENV_WORK` | `e2e/realenv/.work` | certificates, logs, results |
| `REALENV_PULL` | `0` | `1` pulls missing images |
| `REALENV_KEEP` | `0` | `1` leaves the last stage's containers running |
| `REALENV_LOCK` | none | a lock file held while a stage runs (a host shared with other heavy jobs) |
| `REALENV_PREFIX` | `fxre` | container and network names |
| `REALENV_PRIV_NET` / `REALENV_PUB_NET` | `172.30.50` / `203.0.113` | the first three octets of the two /24 networks |
| `REALENV_PEBBLE_IMAGE`, `REALENV_CHALLTESTSRV_IMAGE`, `REALENV_CADDY_IMAGE`, `REALENV_KEYCLOAK_IMAGE`, `REALENV_LDAP_IMAGE`, `REALENV_OO_IMAGE`, `REALENV_BASE_IMAGE`, `REALENV_RUNNER_IMAGE` | see `run.sh` | another tag, or a registry mirror |
| `REALENV_OO_S5_URL`, `REALENV_OO_S5_NETWORK` | none | office S5: a document server that is already running with JWT on (any secret but filex's), and the Docker network it is reached on, instead of starting a second one (a host short of memory). S2 always starts its own, JWT off, and removes it as soon as S2 is measured |

The runner image is `mcr.microsoft.com/playwright:v<version>-noble`, the
version of `@playwright/test` installed in `e2e/node_modules`.

## How the pieces are wired

- **Trusting Pebble.** filex has no setting for the ACME authority's CA: it
  trusts it the way any Go program trusts a private CA, with `SSL_CERT_FILE`
  naming Pebble's root (`/test/certs/pebble.minica.pem` in the Pebble image),
  and `FILEX_TLS_ACME_DIRECTORY=https://pebble:14000/dir`. Caddy takes the same
  root as `acme_ca_root`. The certificates Pebble ISSUES chain to another root,
  made fresh at each start; the specs fetch it from Pebble's management port
  (`/roots/0`) and verify every presented chain against it.
- **DNS.** filex and Pebble ask pebble-challtestsrv, which the specs drive
  through its management API (A records, CNAMEs). filex's containers get it as
  their DNS server (`--dns`), so the own-domain check (`LookupCNAME`) sees the
  same records as the authority.
- **Which challenge.** Pebble validates TLS-ALPN-01 on port 5001 and HTTP-01
  on 5002. The `acme` filex listens on 5001 and has no HTTP listener
  (`FILEX_TLS_HTTP_LISTEN=off`), so its certificates prove TLS-ALPN-01; the
  `h1` filex listens for TLS on 8443, so its certificate proves HTTP-01.
- **Renewal.** A second Pebble (`stack/pebble-short.json`) issues certificates
  that live three minutes; autocert renews at a third of the lifetime left.
- **The guard.** A tenant's own OIDC and LDAP are dialled through
  `internal/netguard`, which refuses private addresses. Keycloak and OpenLDAP
  therefore sit on the "public" network; the same directory's name on the
  private network (`ldap-private.test`) is what the guard must refuse.
- **Certificates of the run** (`stack/certs.sh`, in the runner): a test CA,
  OpenLDAP's certificate, and the certificate a tenant brings for its own
  domain in the proxy spec.

## Adding a case

Put it in the stage's spec, against the servers that stage already starts;
a new kind of server is a new stage in `run.sh` (its images in `need_images`,
so a machine without them skips it by name). Keep every assertion about what
filex does with the real server's answer, not about the server itself.
