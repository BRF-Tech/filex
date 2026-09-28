# Security Policy

## Supported versions

filex is pre-1.0 and ships from the latest tagged release. Security fixes land
on `main` and in the next tag. Please run a recent version.

## Reporting a vulnerability

**Please do not open a public issue for security problems.**

Report privately instead — use this repository's **private vulnerability
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
escalation, secret leakage, RBAC bypass, and an app-plugin sandbox escape or
permission bypass) are in scope. Misconfiguration of a
specific deployment is not, though we welcome hardening suggestions.

Some URLs are credential-free **by design**, and each is bound to exactly what
it names: a share link (`/s/…`) or file request (`/d/…`) to its item and its
settings; an upload ticket (`/u/…`) to one destination and one write; a
download ticket (`/z/…`) to one archive or — for the web app's drag-out — one
file, for at most a minute, and re-checked against its owner's current
permissions and tenant when it is used. That anyone holding such a URL can use
it is the design; a URL reaching **beyond** what it names, outliving its
limits, or being minted by someone who could not read the target is in scope.

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
