# Security Policy

## Supported versions

filex is pre-1.0 and ships from the latest tagged release. Security fixes land
on `main` and in the next tag. Please run a recent version.

## Reporting a vulnerability

**Please do not open a public issue for security problems.**

Report privately instead:

- Preferred: use the repository host's private vulnerability reporting
  ("Report a vulnerability" / private security advisory).
- Or email: `security@example.com` *(replace with the project's real address
  before publishing)*.

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
escalation, secret leakage, RBAC bypass) are in scope. Misconfiguration of a
specific deployment is not, though we welcome hardening suggestions.
