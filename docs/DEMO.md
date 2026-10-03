# Demo mode

`FILEX_DEMO_MODE=true` turns an ordinary filex install into a **public
playground**: the login page shows an "Open the demo" button that signs a
visitor in with credentials printed on the page itself.

That single decision changes what "admin-only" means. On a demo the admin
account is whoever read the landing page, so every admin-only door is a public
door - and the guard described below is what keeps a public playground from
being a public control panel.

The reference deployment is <https://demo.filex.sh>.

---

## What demo mode changes

| | Ordinary install | Demo |
|---|---|---|
| Login page | sign-in form | feature tour + "Open the demo" CTA with the credentials |
| `/api/capabilities` | - | additionally carries `demo_mode`, `demo_user`, `demo_pass` |
| Storage plugins | on unless disabled | **off** unless the operator says otherwise in so many words |
| Apps (app plugins) | on unless disabled | **off** unless the operator says otherwise in so many words (`FILEX_APP_PLUGINS_DISABLED`); install, upgrade and uninstall answer `demo_refused`, and the hourly wake-up never arms |
| Adding a storage | any driver | refused: `local` reaches the server's own filesystem, the remote drivers make the server connect where a visitor points it |
| Admin writes | allowed | **refused, 403** (below) |
| Own account's password / email / TOTP | allowed | **refused, 403** - the account is shared |
| Sign-in attempt limits | apply | apply - except the per-**account** lock on the shared demo account, which is counted per **address** only (below) |
| Audit log, dashboard activity, Sign-in security | show addresses | every visitor's address - and the operator's allow-list and trusted proxies - reads `hidden on the demo` ([below](#what-the-demo-publishes)) |
| Auth-provider config read-back | full | values that carry a credential are masked |

**Nobody can lock the demo account for everybody.** Its password is printed on
the page, so a per-account lock on it would be a switch any stranger flips for
every other visitor - five wrong passwords and the next person typing the right
one would read "this account is locked". On a demo that one account is
**exempt from the per-account lock**; the per-**address** limit still applies to
every attempt on it, so one address guessing is stopped exactly as before
(by default ten wrong attempts within ten minutes lock that address for a
minute, doubling up to 15 minutes), and the sign-in form counts down that
address's tries - it never promises an account lock that will not come. The
exempt account is the one `FILEX_DEMO_USER` names, looked up the way a sign-in
looks it up: by its e-mail **and** its username (and, on a multi-tenant
install, only in its own tenant's realm - `acme/demo`, never `beta/demo`), on
the web form and every file protocol alike. Nothing is written into the code:
an account created after the start is followed within a minute, and every other
account - administrators included - is limited as on any install
([CONFIGURATION.md § Sign-in attempt limits](CONFIGURATION.md#sign-in-attempt-limits)).

Unlocking (`POST /api/admin/login-security/unlock`) and changing the limits are
admin writes, so the demo refuses them like the rest. Set the `login.*` settings
the demo should run with on the **Sign-in security** page with demo mode off,
before turning it on. They are stored in the database, so take the golden copy
(below) after that.

Everything else is the product, and works: browsing, uploading, renaming,
deleting, sharing, tagging, starring, comments, versions, the file explorer's
own API keys. A visitor is meant to use those - the nightly restore is what
makes that safe.

---

## The read-only guard

`backend/internal/api/demo_guard.go` refuses every state-changing method
(anything that is not `GET` / `HEAD` / `OPTIONS`) under these prefixes:

| Prefix | Why |
|---|---|
| `/api/admin/…` | the operator surface: settings, users, roles, groups, storages, webhooks, external services, auth providers, sign-in security, self-update, quotas, trash purge |
| `/api/ai/admin/…` | **the same surface behind an admin-scoped API token.** Guard one and the other is the bypass |
| the `admin_*` MCP tools | the same surface a third time: they run the admin handlers **in-process**, past every route, so the prefix guard never sees them. The tool invoker refuses their writes on a demo itself, with the same answer (`handlers.DemoRefusal`); their reads mask what the routes mask |
| `/api/auth/password` | changes the *published* password - one visitor locks out every other reader |
| `/api/auth/profile` | changes the email those credentials sign in with |
| `/api/auth/totp/…` | puts a second factor on the shared account |
| `/metrics` | the Prometheus exposition, mounted inside the admin group with `r.Handle`, so chi registers it for **every** method. The exposition is read-only and answers the same bytes to any verb, so nothing here was ever exploitable - it is guarded so that "a demo refuses every state-changing method on every operator surface" holds with no exceptions. `GET`/`HEAD`/`OPTIONS` still pass: scrape jobs are untouched (`METRICS.md`) |

Refusals answer `403` with a sentence a visitor can act on:

```json
{
  "demo": "read-only",
  "error": "this is a public demo: the admin surface is read-only here, and the shared demo account cannot be changed - run your own filex to try this"
}
```

**Reads still work.** A demo exists to show the product *including* the
operator surfaces, so the admin panel renders - the dashboard, the users list,
settings, storages, the audit page. Hiding them would have been the cheaper
fix and a worse demo.

### Why the guard is on the mount point, not on handlers

The admin route tree grows constantly (60 → 101 routes in three months). A
guard that enumerates handlers is one merge away from a hole; a guard on the
prefix covers a route that does not exist yet. It is installed as router-level
middleware in `BuildRouter`, above every auth chain, and is a pass-through
with a single boolean test when demo mode is off.

### How the prefix list is kept honest

A prefix list has one failure mode of its own: **the prefix nobody added**.
`/api/admin` and `/api/ai/admin` were the same admin panel behind two front
doors, and the second one was found only because somebody went looking.

`backend/internal/api/shop_window_route_table_test.go` walks the entire chi
route table and classifies every state-changing route by asking the running
server: a route an anonymous caller is refused `401` on and a signed-in
**non-admin** is refused `403` on is an operator surface, one that answers both
identically is not role-gated at all, and everything else is the product. Every
operator surface must be refused here - and, in a second test, no route an
ordinary user is entitled to use may be, so the guard cannot be widened over the
product to make the first test quiet.

Nothing in that test names a route. It found `/metrics` on its first run, and it
is the reason the next front door cannot arrive unnoticed. ⚠ Its one blind spot: the
classification is behavioural, so a route with its own bespoke authorization
that answers an ordinary user exactly as it answers an anonymous one reads as
"not role-gated". Use `auth.RequireAdmin` or `RequireScope("admin")` like the
rest of the tree and it is visible.

### What is still writable on a demo, on purpose

- everything under `/api/files/…` - the product itself
- `POST /api/tokens` - a personal token, whose scopes are capped so it can
  never carry `admin` (`cappedScopes`)
- `/api/auth/s3-keys`, `/api/auth/ssh-keys`, `/api/auth/nfs-exports` -
  per-account protocol credentials for a sandbox that resets nightly

None of these can lock another visitor out, reconfigure the instance, or make
the server connect somewhere a stranger chose.

---

## What the demo publishes

- **The credentials.** `demo_user` / `demo_pass` in `/api/capabilities`, so the
  CTA can submit them. Deliberate.
- **Not the operator's hosts.** `external.<service>.url` is dropped for
  anonymous callers on every install - see
  [BACKEND.md § Capabilities](BACKEND.md#capabilities).
- **Not the visitors' addresses.** Every address a visitor left reads
  `hidden on the demo`, wherever it can be read:
  - the audit page and the dashboard's `recent_activity`: the client address,
    an address a row is about (an address lock's `login.locked` /
    `login.unlocked`, whose target is the address), the target's name, and any
    address inside a row's metadata (an unlock's `subject`, a name typed as an
    address);
  - the **Sign-in security** page: its trail
    (`GET /api/admin/login-security/attempts`) and its list of locks
    (`/locks`: an address lock's `subject`, every counter's `last_ip`). Each
    lock row keeps an `id` of its own, so two masked addresses are still two
    rows, and a masked row offers no *Unlock* - it names nothing to unlock;
  - the same answers through `/api/ai/admin` and the `admin_*` MCP tools.
  The page says it in the reader's language. A wrong attempt's door and its
  reason stay: the trail is still a trail.
- **Not the names visitors typed.** A name typed at a sign-in door that is no
  account of the instance - a visitor's own e-mail, typed instead of the
  demo's - reads `hidden on the demo` on the same surfaces (the trail's account
  column, the locks list, the audit page's sign-in rows). The names of the
  instance's own accounts - the demo account and the golden copy's - stay
  readable, typed by e-mail or by username, in any case, with a realm in front
  (`acme/demo`).
- **Not the operator's sign-in settings addresses.** The allow-list (an
  address on it is exempt from the per-address limit) and the trusted proxies
  read `hidden on the demo` on the Sign-in security page, in
  `/api/admin/settings` and in the audit rows of a settings change; the class
  words (`loopback`, `private`, `link-local`) stay. `your_ip` - the reader's own
  address - is shown.
- **No notification or webhook carries them.** No bell notification, webhook
  payload or realtime frame is built from a sign-in event, so there is nothing
  there to mask.

On an ordinary install the operator keeps seeing every address: they are
entitled to, and the masking runs only when `FILEX_DEMO_MODE` is on.

---

## ⚠ The demo tree is not in this repo

The files, folders, tags and accounts a visitor sees on demo.filex.sh are **not
seeded from code**. They are a byte copy on the host - `/root/filex/demo-golden` -
restored by cron every night, and refreshed by hand from a running instance
(`demo-reset.sh --refresh-golden`). The reset script and its cron entry live
with the deployment, not in this repository.

Two consequences worth knowing before debugging anything about the demo:

1. **A fix that lives only in the golden copy is one restore away from gone.**
   Everything on this page is in code, which is why it survives the restore.
2. **Anything the demo *says* about its own content is a claim about that
   golden copy.** The login splash advertises example searches; they are pinned
   to the recorded corpus by
   `backend/internal/api/handlers/search_demo_suggestion_test.go`, so a
   suggestion that stops being true fails a test instead of greeting visitors.
   Measured on 2026-09-07, before that test existed: the two queries the
   product suggested - `invoice 2026` and `tag:report` - were the only two that
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
prints. Neither creates the account - seed it (`FILEX_ADMIN_*`, or
`filex admin`) and keep the two in step yourself.

See also: [CONFIGURATION.md § Demo mode](CONFIGURATION.md#demo-mode) ·
[BACKEND.md § Capabilities](BACKEND.md#capabilities) · [PLUGINS.md](PLUGINS.md)
