# Demo mode

`FILEX_DEMO_MODE=true` turns an ordinary filex install into a **public
playground**: the login page shows an "Open the demo" button that signs a
visitor in with credentials printed on the page itself.

That single decision changes what "admin-only" means. On a demo the admin
account is whoever read the landing page, so every admin-only door is a public
door — and the guard described below is what keeps a public playground from
being a public control panel.

The reference deployment is <https://demo.filex.sh>.

---

## What demo mode changes

| | Ordinary install | Demo |
|---|---|---|
| Login page | sign-in form | feature tour + "Open the demo" CTA with the credentials |
| `/api/capabilities` | — | additionally carries `demo_mode`, `demo_user`, `demo_pass` |
| Storage plugins | on unless disabled | **off** unless the operator says otherwise in so many words |
| Adding a storage | any driver | refused: `local` reaches the server's own filesystem, the remote drivers make the server connect where a visitor points it |
| Admin writes | allowed | **refused, 403** (below) |
| Own account's password / e-mail / TOTP | allowed | **refused, 403** — the account is shared |
| Audit log, dashboard activity | shows client IPs | IPs replaced with `hidden on the demo` |
| Auth-provider config read-back | full | values that carry a credential are masked |

Everything else is the product, and works: browsing, uploading, renaming,
deleting, sharing, tagging, starring, comments, versions, the file explorer's
own API keys. A visitor is meant to use those — the nightly restore is what
makes that safe.

---

## The read-only guard

`backend/internal/api/demo_guard.go` refuses every state-changing method
(anything that is not `GET` / `HEAD` / `OPTIONS`) under these prefixes:

| Prefix | Why |
|---|---|
| `/api/admin/…` | the operator surface: settings, users, storages, webhooks, external services, auth providers, self-update, quotas, trash purge |
| `/api/ai/admin/…` | **the same surface behind an admin-scoped API token.** Guard one and the other is the bypass |
| `/api/auth/password` | changes the *published* password — one visitor locks out every other reader |
| `/api/auth/profile` | changes the e-mail those credentials sign in with |
| `/api/auth/totp/…` | puts a second factor on the shared account |

Refusals answer `403` with a sentence a visitor can act on:

```json
{
  "demo": "read-only",
  "error": "this is a public demo: the admin surface is read-only here, and the shared demo account cannot be changed — run your own filex to try this"
}
```

**Reads still work.** A demo exists to show the product *including* the
operator surfaces, so the admin panel renders — the dashboard, the users list,
settings, storages, the audit page. Hiding them would have been the cheaper
fix and a worse demo.

### Why the guard is on the mount point, not on handlers

The admin route tree grows constantly (60 → 101 routes in three months). A
guard that enumerates handlers is one merge away from a hole; a guard on the
prefix covers a route that does not exist yet. It is installed as router-level
middleware in `BuildRouter`, above every auth chain, and is a pass-through
with a single boolean test when demo mode is off.

### What is still writable on a demo, on purpose

- everything under `/api/files/…` — the product itself
- `POST /api/tokens` — a personal token, whose scopes are capped so it can
  never carry `admin` (`cappedScopes`)
- `/api/auth/s3-keys`, `/api/auth/ssh-keys`, `/api/auth/nfs-exports` —
  per-account protocol credentials for a sandbox that resets nightly

None of these can lock another visitor out, reconfigure the instance, or make
the server connect somewhere a stranger chose.

---

## What the demo publishes

- **The credentials.** `demo_user` / `demo_pass` in `/api/capabilities`, so the
  CTA can submit them. Deliberate.
- **Not the operator's hosts.** `external.<service>.url` is dropped for
  anonymous callers on every install — see
  [BACKEND.md § Capabilities](BACKEND.md#capabilities).
- **Not visitors' IP addresses.** The audit page and the dashboard's
  `recent_activity` replace them. On an ordinary install the operator keeps
  seeing them: they are entitled to, and nothing in this file runs unless
  `FILEX_DEMO_MODE` is on.

---

## ⚠ The demo tree is not in this repo

The files, folders, tags and accounts a visitor sees on demo.filex.sh are **not
seeded from code**. They are a byte copy on the host — `/root/filex/demo-golden`
— restored by cron every night, and refreshed by hand from a running instance
(`demo-reset.sh --refresh-golden`). The reset script and its cron entry live
with the deployment, not here — `docs/DEPLOY_BRF.md` in the repo has the paths
for this project's own instance.

Two consequences worth knowing before debugging anything about the demo:

1. **A fix that lives only in the golden copy is one restore away from gone.**
   Everything on this page is in code, which is why it survives the restore.
2. **Anything the demo *says* about its own content is a claim about that
   golden copy.** The login splash advertises example searches; they are pinned
   to the recorded corpus by
   `backend/internal/api/handlers/search_demo_suggestion_test.go`, so a
   suggestion that stops being true fails a test instead of greeting visitors.
   Measured on 2026-09-07, before that test existed: the two queries the
   product suggested — `invoice 2026` and `tag:report` — were the only two that
   returned nothing.

---

## Running one locally

```sh
FILEX_DEMO_MODE=1 \
FILEX_LISTEN=127.0.0.1:5877 \
FILEX_DATA_DIR=$PWD/demo-data \
FILEX_ADMIN_EMAIL=demo@demo.com \
FILEX_ADMIN_PASSWORD=demo \
filex serve
```

⚠ `FILEX_DEMO_USER` / `FILEX_DEMO_PASS` only decide what the CTA submits and
prints. Neither creates the account — seed it (`FILEX_ADMIN_*`, or
`filex admin`) and keep the two in step yourself.

See also: [CONFIGURATION.md § Demo mode](CONFIGURATION.md#demo-mode) ·
[BACKEND.md § Capabilities](BACKEND.md#capabilities) · [PLUGINS.md](PLUGINS.md)
