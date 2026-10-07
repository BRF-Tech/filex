# Security Policy

## Supported versions

filex is pre-1.0 and ships from the latest tagged release. A fix for a hole that
can be exploited in a released version ships at once, as a patch release of its
own; hardening with no live hole behind it rides the next daily release
([Release process](docs/CONTRIBUTING.md#release-process)). Please run a recent
version.

## Reporting a vulnerability

**Please do not open a public issue for security problems.**

Report privately instead - use this repository's **private vulnerability
reporting**: the **Security** tab → **Report a vulnerability** (GitHub Security
Advisories). This opens a channel visible only to the maintainers.
Alternatively, email **security@brf.sh**.

Include, where possible:

- affected version / commit,
- a description and impact,
- reproduction steps or a proof of concept,
- any suggested fix.

We aim to acknowledge a report within a few days and to ship a fix or mitigation
as soon as practical, coordinating a disclosure timeline with you.

## Scope notes

filex is a self-hosted application; the operator controls storage backends,
auth providers, network exposure and secrets. Reports about the software itself
(auth bypass, path traversal / confinement escape, injection, SSRF, privilege
escalation, secret leakage, RBAC bypass, a way around the sign-in attempt
limit, a forwarded client address believed from a sender that is not a trusted
proxy (`FILEX_TRUSTED_PROXIES`), a sign-in reaching another tenant's account,
and an app-plugin sandbox escape or permission bypass) are in scope.
Misconfiguration of a specific deployment is not, though we welcome hardening
suggestions.

Some URLs are credential-free **by design**, and each is bound to exactly what
it names: a share link (`/s/…`) or file request (`/d/…`) to its item and its
settings; an upload ticket (`/u/…`) to one destination and one write; a
download ticket (`/z/…`) to one archive or - for the web app's drag-out - one
file, for at most a minute, and re-checked against its owner's current
permissions and tenant when it is used; a sign-in handoff ticket (carried in
the URL fragment, redeemed at `POST /api/auth/handoff`) to one account on one
tenant's address, once, within 60 seconds. That anyone holding such a URL can
use it is the design; a URL reaching **beyond** what it names, outliving its
limits, or being minted by someone who could not read the target is in scope.

Plugin and app downloads reach public addresses only: a download filex makes
for an install, an install request or the update check that reaches this
machine, the private network or a cloud metadata service (other than loopback
under the development setting `FILEX_PLUGIN_LOOPBACK_SOURCES`), or follows a
redirect from https to plain http, is in scope. A storage plugin runs with
filex's own rights and is handed its storages' credentials, so it is trusted
code, not a sandbox; in scope is filex running a binary other than the one it
verified, sending a plugin's token or a storage's configuration anywhere but
that plugin's own socket, loopback port or registered address, or handing one
plugin's storages to another. A certificate filex issues for signing is for
documents only; one that is usable for email (S/MIME), TLS or code is in scope.

A change sent through a signed-in person's browser from another origin is
refused: any request but `GET`, `HEAD` and `OPTIONS`, and a WebSocket upgrade,
that carries only the ambient session (the cookie, or a trusted proxy's
header) must come from filex's own pages, filex's own address, or an origin
the operator put in `FILEX_CORS_ALLOWED_ORIGINS`; the sign-in form is held to
the same rule without a session. In scope: such a request getting through
from any other origin, including a sibling subdomain, `Origin: null`, the
app-interface host and the ONLYOFFICE frame origin - those two are never
trusted, whatever the operator listed. Not in scope: an origin the operator listed (a
`https://*.example.com` pattern trusts every other subdomain by design), a request
carrying a key, and a browser that sends none of `Sec-Fetch-Site`, `Origin`
or `Referer` (every current browser sends them). How it decides:
[CONFIGURATION.md → Requests from other origins](docs/CONFIGURATION.md#requests-from-other-origins).

An API token and the AI surface (REST `/api/ai`, the MCP tools) act for an
account and may do no more than that account could in the file manager
([MCP.md](docs/MCP.md#security)): a tool that asks less than its file-manager
twin is in scope. So is anything that undoes end-to-end encryption's promise
([E2E-ENCRYPTION.md](docs/E2E-ENCRYPTION.md)): the server holds no key, and a
surface that hands out an encrypted file as if it were readable, mints a public
link into an encrypted folder, writes plaintext into one without the caller's
explicit `allow_plaintext`, or changes its key file is a bug. Plaintext written
into an encrypted folder over WebDAV or the CLI is a documented gap, not a
report.

An app's own interface (`/_appui/<app>/<version>/…`) is credential-free by
design too: it serves the app's approved package, and what makes running it
safe is the sandbox every answer carries, not who asked. In scope: an
interface reaching filex's session, storage or API; loading code from outside
its package; a policy wider than the administrator's grant; the bridge
accepting a message from any frame but the one filex drew, or letting an
interface read or write files it was not opened with. Known and documented,
not in scope: an interface sending out what it can see through WebRTC in a
browser that ignores the measures filex takes against it
([APP-PLUGINS.md](docs/APP-PLUGINS.md#an-apps-own-interface)).

The ONLYOFFICE editor's frame (`/filex-frame/editor` on the Document Server's
own origin, `FILEX_ONLYOFFICE_FRAME_ORIGIN`) is the one page filex answers on
that host. In scope: filex answering anything else there, the page running on
filex's own origin, the frame reaching the signed-in person's session, or the
signed editor configuration reaching any frame but the one filex's page drew
([ONLYOFFICE.md](docs/ONLYOFFICE.md#the-editor-in-a-frame-of-its-own)).
