# Writing an app plugin

This is the author's side of [APP-PLUGINS.md](APP-PLUGINS.md): the manifest,
the exports filex calls, the host functions it offers, the screens it can
draw for you, and a complete example. The Go SDK is `pkg/pluginkit` in the
filex repository; any language that compiles to WebAssembly with WASI
preview 1 can speak the same ABI (the tables below are the whole contract).

## The shape of an app

```
my-app/
  filex-app.json      the manifest (what the app is, what it asks for, what it offers)
  main.go             registers actions / views / pages with pluginkit.Run — from init()
  plugin.wasm         the build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared
```

```bash
GOOS=wasip1 GOARCH=wasm go build -trimpath -ldflags="-s -w" -buildmode=c-shared -o plugin.wasm .
sha256sum plugin.wasm     # goes into filex-app.json → wasm.sha256 for URL installs
```

Stock Go 1.25+ is enough — no TinyGo (the SDK lives in filex's own module,
which declares `go 1.25.0`, so an older toolchain refuses to build against
it). `-buildmode=c-shared` turns the
`//go:wasmexport` functions into module exports and makes the module a
*reactor*: filex calls the exports directly and **`main()` never runs**, which
is why registration happens in `init()`.

```go
package main

import (
	"strings"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

var manifest = wire.Manifest{ /* must equal filex-app.json */ }

func main() {}

func init() {
	pluginkit.Run(&pluginkit.Plugin{
		Manifest: manifest,
		Actions: map[string]pluginkit.ActionFunc{
			"upper": func(in *wire.ActionRunInput) (*wire.ActionRunOutput, error) {
				var outs []wire.OutputRef
				for i, f := range in.Inputs {
					data, err := pluginkit.ReadInput(f.Ref)
					if err != nil {
						return nil, err
					}
					ref, err := pluginkit.WriteOutput(strings.TrimSuffix(f.Name, ".txt")+"-upper.txt",
						[]byte(strings.ToUpper(string(data))))
					if err != nil {
						return nil, err
					}
					outs = append(outs, ref)
					pluginkit.Progress(int64(i+1), int64(len(in.Inputs)), "upper-cased "+f.Name)
				}
				return &wire.ActionRunOutput{OK: true, Outputs: outs,
					Message: wire.Text{"en": "done", "tr": "bitti"}}, nil
			},
		},
	})
}
```

The test fixture `backend/internal/wasmplugin/testdata/echo` is a complete
app that exercises every host function; copy it to start.

## The manifest (`filex-app.json`)

```json
{
  "manifest_version": 1,
  "name": "sign",
  "version": "1.0.0",
  "label": { "en": "e-Signature", "tr": "e-İmza" },
  "description": { "en": "Sign PDFs and ask others to sign." },
  "icon": "sign",
  "homepage": "https://github.com/BRF-Tech/filex-sign",
  "min_filex": "0.43.0",
  "permissions": ["files:read", "files:write", "sign", "public_pages", "mail:send", "notify:send", "users:lookup", "state", "engines:libreoffice"],
  "permission_reasons": { "mail:send": { "en": "To invite outside signers by email." } },
  "languages": ["en", "tr"],
  "settings": [
    { "key": "tsa_url", "type": "string", "label": { "en": "Timestamp server", "tr": "Zaman damgası sunucusu" } },
    { "key": "api_key", "type": "password", "label": "API key", "secret": true }
  ],
  "actions": [
    { "id": "sign", "label": { "en": "Sign…", "tr": "İmzala…" }, "icon": "sign",
      "applies": { "kind": "file", "ext": ["pdf"], "engine_ext": { "libreoffice": ["docx", "odt"] } },
      "view": "sign-wizard", "min_role": "editor",
      "output": { "mode": "sibling", "name": "{stem}-signed{ext}" },
      "limits": { "timeout_s": 300 } }
  ],
  "views": [
    { "id": "sign-wizard", "placement": "modal", "label": { "en": "Sign" }, "size": "lg" },
    { "id": "sign-status", "placement": "inspector", "label": { "en": "Signatures" }, "applies": { "kind": "file", "ext": ["pdf"] } },
    { "id": "envelopes", "placement": "home", "label": { "en": "Signatures" } }
  ],
  "public_pages": [
    { "id": "signer", "label": { "en": "Sign the document", "tr": "Belgeyi imzala" }, "pin": "optional", "default_ttl_days": 14, "max_ttl_days": 90,
      "purpose": { "label": { "en": "Signing request", "tr": "İmza isteği" },
                   "revoke": { "en": "Revoking this link cancels the signing request.", "tr": "Bu bağlantıyı iptal etmek imza isteğini iptal eder." },
                   "section": "requested" } }
  ],
  "limits": { "memory_pages": 2048, "call_timeout_s": 30 },
  "wasm": { "url": "https://github.com/BRF-Tech/filex-sign/releases/download/{tag}/plugin.wasm", "sha256": "…" }
}
```

| Field | Meaning |
|---|---|
| `name` | `[a-z0-9][a-z0-9_-]{0,31}`, unique per instance; names the directory and every menu key (`plugin:<name>/<action>`). Not `cache`, `spool`, `public` or `assets` — the host's own directories beside the apps' |
| `label`, `description` | `Text` = `{en, tr, …}`; `en` is required everywhere a Text appears, other languages fall back to it |
| `permissions` | the closed list below; anything else is refused at install |
| `permission_reasons` | shown beside each permission in the install review — say why |
| `languages` | the languages the plugin promises to speak, e.g. `["en", "tr"]` (empty = `["en"]`). **Every `Text` it returns must carry all of them** — the host refuses to install one whose `describe` answer is missing a language, and `pluginkit/plugintest` checks the screens too |
| `ui_locales` | languages for **filex itself**: `{"es": {"ctx.download": "Descargar", …}}` by filex's own keys (both catalogues, one flat namespace). They join the language list every picker offers (public pages included) and leave with the app; a missing key falls back to English. A manifest with `ui_locales` and nothing that runs is a **language pack** and needs no module — see [Writing a language pack](#writing-a-language-pack) |
| `settings` | the storage-driver field shape (`key, type, label, help, required, secret, default, placeholder, options, min, max`, plus `multi` / `show_when` / `required_when` — see *Fields*); `type` is `string \| password \| int \| bool \| select \| text \| date`; `secret` fields are sealed and only readable through `settings_get`. `label`, `help`, `placeholder` and an option's `label` may be **one string or a `Text`** (`{en, tr, …}`); a `Text` must carry every declared language, a plain string is shown as it is (every manifest written before v0.43 keeps installing). ⚠ `advanced` is **gone** — a setting that matters is on the form |
| `actions[]` | menu rows: `applies` (`kind` file/dir/any, `ext` lower-case no dot, `mime` exact or `image/*`, `multi`, `min`, `max`, **`state` / `no_state`** — bare keys you keep with `StateSet`; the row is offered only where one of `state` is set and none of `no_state` is), optional `view` opened first, `confirm` text, `min_role` viewer/editor/owner, `danger`, `output.mode` sibling/version/none with `output.name` pattern (`{stem}`, `{ext}`, `{name}`), `limits.timeout_s`, **`hidden`** (never in a menu — only a surface's `job` or a page starts it: the second half of a flow). **`applies.engine_ext`** `{engine: [ext…]}` adds extensions that apply only while that engine is installed and granted (office documents only where LibreOffice is); it adds to a non-empty `ext`/`mime` list |
| `views[]` | screens: `placement` modal (dialog) / **page** (full page in a new tab — a wizard with the document beside it) / inspector (details panel, needs `applies`) / home (side bar) |
| `public_pages[]` | the screens an outside participant may be sent to: `pin` optional/required/none, TTL default and ceiling in days. Opening one mints a **share** (`/s/<token>`, see *Public links, end to end*); a page you never declare cannot be opened. **`purpose`** `{label, revoke, section}` names what its links are in My shares / Shares ("Signing request"), what revoking one does, and the section of your `home` view that shows it. `ShareCreate` takes the same `Purpose` for one link — it wins over the page's, and a page-less link (a plain share of a file) has no other way to say what it is |
| `limits` | `memory_pages` (default 1024 = 64 MiB, ceiling 4096), `call_timeout_s` for screens (default 15, ceiling 60); jobs default 300 s, ceiling 900 |
| `messages` | texts filex says on your behalf long after the call is over, in each reader's language: `{"lock.collecting": {"en": …, "tr": …}}`. A file lock's reason is kept as a key plus arguments (`pluginkit.FileLockMessage`) and read back on the admin page and in the `423` a refused write gets. Every declared language is required, and a key the manifest does not declare is refused |
| `wasm` | where an installer fetches the module (`{tag}` = the ref) and its sha256 — required for GitHub/URL installs of an app with a module; absent for a language pack |

Unknown fields are refused: a typo in `permisions` would otherwise install an
app with no grants and every host call refused, which is a confusing failure.

### Permissions

| Permission | Unlocks |
|---|---|
| `files:read` | `file_open` / `file_read` / `file_close` on the call's inputs |
| `files:write` | `file_create` / `file_write` — outputs a job may return |
| `files:lock` | `file_lock` / `file_unlock` — freeze a file read-only for everyone (administrators included) until you lift it or the TTL passes; your own jobs still write into it |
| `state` | `state_get` / `state_set` / `state_list` — small per-file records (≤ 64 KiB) keyed by the file; they move and vanish with it, and `state_list` finds them again |
| `settings` | `settings_get` — the admin's values, secrets opened here only |
| `engines:<name>` | `engine_run` / `engine_available` for `ffmpeg`, `imagemagick`, `libreoffice`, `ghostscript`, `poppler`, `rsvg` |
| `users:lookup` | `users_lookup` — the caller's directory (tenant-scoped, ≤ 20 rows); also lets the `people-picker` screen part search |
| `notify:send` | `notify_send` — a `plugin.notice` notification |
| `mail:send` | `mail_send` — plain text through the server's SMTP, 60/hour |
| `http:<host>` / `http:*.domain` | `http_request` to that host (private addresses refused after DNS), and `asset_fetch` of a pinned file from it |
| `public_pages` | `share_create` / `share_revoke` / `share_state` — open a real share link for an outside participant. (`public_page_create` / `_revoke` / `_state` are the older names for exactly these and still work.) |
| `sign` | `host_sign_info` / `cert_issue` / `host_sign` / `key_destroy` |
| `schedule` | the `tick` export — filex wakes your app once an hour and runs the work you ask for at the minute you name. The only permission that makes your code run with nobody present; see *A scheduled wake-up* |

The administrator grants the list exactly; there is no partial grant in v1.

## The six exports

filex calls these on a **fresh instance per call**: nothing survives between
calls except what you write through host functions. Input and output are
JSON in Extism's buffers (`pluginkit` does the plumbing).

| Export | Input | Output | When |
|---|---|---|---|
| `describe` | `{host_version, locale}` | the manifest | at install and every load; must match the installed manifest's `name`, `version`, `manifest_version`, and its permissions must be a subset — else the app is *refused* |
| `action_run` | `ActionRunInput` | `ActionRunOutput` | a queued job; the only export that may write files, create pages, issue certificates |
| `view_event` | `ViewEventInput` | `Surface` | a screen event: `open`, `change`, `submit`, `action` |
| `page_event` | `ViewEventInput` | `Surface` | the same for an outside participant on a public link (`data.page` carries `{subject, state, visits, visitor_ip}`) |
| `tick` | `TickInput` | `TickOutput` | once an hour, if you asked for `schedule`. Nobody is waiting: the budget is your `call_timeout_s` capped at 30 s, and the call is READ-ONLY |
| `on_event` | — | — | reserved — nothing calls it yet, and the `events:<name>` permission that would gate it is **refused at install** until something does (a grant that does nothing is not a grant) |

`ActionRunInput`: `job_id`, `action_id`, `params` (from the screen or the
caller), `inputs[]` (`{ref, name, size, mime, path_rel, path, read_only}` — `ref` is an opaque,
call-scoped handle like `in:0`; `path` is the adapter-qualified spelling a
`pdf-fields`/`preview` node's `src.path` takes; `path_rel` is storage-relative;
`read_only` is true when the file's storage takes no writes — refuse there, at
your first screen and in your first job, a flow that ENDS in a write: filex
refuses a job that writes, not one that only leads to a write later), `output` (the action's
mode and name pattern), `actor` (`{id, email, name, role}`), `locale`,
`settings` (non-secret values), `engines` (which are present *and* granted),
`share_max_ttl_days` (below).

`share_max_ttl_days` — on `ActionRunInput` and on every view and page call's
`context` — is the longest life, in days, this installation gives ANY new
share link (Admin → Protection, `share.max_ttl_days`; 7 unless changed; `0` =
no ceiling; absent for an app without `public_pages`). ⚠ `share_create`
clamps a link to the lowest of that, the page's own `max_ttl_days` and 365,
**silently**. A screen that offers a longer life promises something the link
will not have — the signing app's wizard said "links valid 14 days" while its
links lived 7 — so read it and offer no more.

`ActionRunOutput`: `ok`, `outputs[]` (`{ref, name}` — refs you created with
`file_create`, or artefacts an engine produced), `message` (Text; shown in the
tray and as the job's result), optional `surface`.

`Surface`: `title`, `size` (sm/md/lg/xl), `state` (opaque, echoed back on the
next event), `nodes[]`, `actions[]` (footer buttons: `{id, label, primary,
danger, disabled}` — **at most one `primary`**), `toast`, `done`, `job`
(`{action_id, params}` — ask the host to queue an action; from a public link
it runs as the link's creator), `errors` (`{field: Text}`), `open`, and — on a
`home` screen — `sections` (`[{id, label, count?}]`) with `section`, the one
this surface drew. filex draws them as the page's tab strip and keeps the open
one in the address (`?section=`), which arrives back as `data.section` on the
next `open`.

`Surface.open` — `{path, action|view}` — sends the person to a FILE and starts
one of your screens on it (naming neither just opens the file). It is what
makes a `home` screen a list of *documents* rather than a list of names: the
row is clicked and the person lands where the work is. The host checks the
screen is yours; the handler checks the path against the **asking person's**
permissions, and drops the link rather than refusing the screen when it does
not pass — you can offer a door, you cannot open one that was not theirs.
⚠ A public link's surface may not carry `open`: there is no explorer behind
it.

## A scheduled wake-up

Everything above happens because somebody clicked. Some work has no click in
it: a signature request that lapses at 03:00 has to close at 03:00 and tell
both sides, and "the next time a human opens the status screen" is not 03:00.

Ask for the `schedule` permission and filex wakes your app **once an hour**
with one question: *what do you want done, and when?* You answer with items,
each carrying a due time, and filex runs each one **at that time**. So the
wake-up is hourly and the work is to the minute — not "some time this hour".

```go
pluginkit.Run(&pluginkit.Plugin{
    Manifest: manifest, // permissions: […, "schedule"]
    Actions: map[string]pluginkit.ActionFunc{
        // The work itself. `hidden: true` in the manifest, because nobody
        // picks it from a menu — the schedule starts it.
        "expire": func(in *wire.ActionRunInput) (*wire.ActionRunOutput, error) {
            _ = pluginkit.StateSet(in.Inputs[0].Ref, "pending", "")   // a job MAY write
            _, _ = pluginkit.NotifySend(pluginkit.Notice{ /* … */ })
            return &wire.ActionRunOutput{OK: true}, nil
        },
    },
    Tick: func(in *wire.TickInput) (*wire.TickOutput, error) {
        // Which of my documents are waiting, and when does each lapse?
        // This app writes "<envelope>@<RFC3339 deadline>" into its own
        // `pending` state when the request goes out.
        waiting, err := pluginkit.StateList("pending", 200)
        if err != nil {
            return nil, err
        }
        var items []wire.ScheduleItem
        for _, f := range waiting {
            envelope, when, ok := strings.Cut(f.Value, "@")
            if !ok {
                continue
            }
            deadline, err := time.Parse(time.RFC3339, when)
            if err != nil || deadline.After(in.WindowEnd) {
                continue // not this hour's business; you will be asked again
            }
            items = append(items, wire.ScheduleItem{
                // ⚠ Your name for this work, and it must match the key
                // pattern — a PATH is not a key, it has slashes in it.
                Key:      "expire:" + envelope,
                DueAt:    deadline,          // to the minute
                ActionID: "expire",
                Paths:    []string{f.Path},  // adapter-qualified, as StateList answers
                Params:   map[string]any{"reason": "lapsed"},
            })
        }
        return &wire.TickOutput{Items: items}, nil
    },
})
```

**The window.** `WindowStart` is now, `WindowEnd` is the next wake-up (the
next hourly boundary). An item due inside it is scheduled; one due **after**
it is quietly not — say it again at the wake-up whose window contains it, by
which time you may have changed your mind. An item due in the **past** runs
at once, because a wake-up that ran late should still close what has lapsed.
After a restart at 03:37 the window is 03:37 to 04:00, shorter than an hour,
and `WindowEnd` says so.

**The key is an idempotency key.** filex keeps at most one item per (app,
key), so naming the same key again **moves** that item rather than adding a
second one — a deadline extended by an hour is the same envelope closing
later, not two closures. It is also why a restart cannot run something twice.
It must be 1–64 characters of `[A-Za-z0-9]` plus `_.:@-`; an item whose key
does not match is refused and counted in your log. ⚠ A file path is **not** a
key — it has slashes in it. Key by something of your own (an envelope id, a
request id), which is also what makes the same work recognisable next hour.

**What a wake-up may and may not do.** `tick` is **read-only**: the same
scope a screen gets. You may read settings, read your own state
(`StateGet` / `StateList`), look people up, notify, send mail and make
granted HTTP calls. You may **not** write files, write state, take locks,
sign or open public links. Those belong in the action you schedule, which
runs as a full job with a writable scope — which means a wake-up is also how
an app finally gets to write state *on its own schedule*, just not in the
deciding call. Decide in `tick`, act in the action.

**What is bounded**

| | |
|---|---|
| Wake-up | once an hour, per app |
| Call budget | your `limits.call_timeout_s`, capped at **30 s** (tighter than a screen's 60: nobody is waiting, and a hang would hold up every other app) |
| Items per wake-up | `in.MaxItems` (64). Extras are dropped — put the urgent ones first |
| Files per item | `in.MaxPaths` (16), all on **one** storage, each adapter-qualified, **at least one** (a job runs on files) |
| `params` | JSON, ≤ 16 KiB |
| Due time | inside `[WindowStart, WindowEnd]` |
| Action | one of **yours**, and not one the administrator switched off — re-checked when it runs, not only when you asked |

**When it goes wrong.** A `tick` that traps, times out or returns an error
costs your app that hour and nothing else: it is logged where the
administrator reads it, and the next wake-up asks again. An item that cannot
be queued is marked failed and is **not** retried — the next wake-up is the
retry. Nothing loops inside the hour.

**Where to watch it.** A scheduled item becomes an ordinary job: an ops row
with `kind: plugin-action`, your action, your message, your outputs. The
actor is SYSTEM, because nobody asked for it. What each wake-up decided is in
your app's log in the admin panel — including a `note` you can put there
yourself.

## Host functions

Every host function is JSON in / JSON out and answers a failure **in band**:
`{"error": {"code", "message"}}` with `permission_denied`, `not_found`,
`too_large`, `timeout`, `unavailable`, `invalid`, `busy`, `internal`. A
refused call is an error you can read, not a trap — degrade, tell the user.
The Go wrappers return `*pluginkit.HostError`; `pluginkit.IsPermissionDenied`
tells the one case apart.

| Function | Go wrapper | Notes |
|---|---|---|
| `file_open {ref}` → `{handle, size}` · `file_read` (framed, ≤ 1 MiB) · `file_close` | `OpenInput(ref)` (an `io.ReadCloser`), `ReadInput(ref)` | inputs, engine artefacts, on a page the exposed copies (`pub:N`), and an `asset_fetch` ref (`asset:N`) — which needs no `files:read` |
| `file_create {name}` → `{handle, ref}` · `file_write` (framed, ≤ 1 MiB) · `file_close` | `CreateOutput(name)` (an `io.WriteCloser` with `.Ref()`), `WriteOutput(name, bytes)` | jobs only; the name is a single path element |
| `job_progress {done, total, message}` | `Progress(done, total, msg)` | draws the tray bar; the message is shown |
| `settings_get {key}` → `{value, found}` | `Setting(key)` | secrets are opened here only |
| `state_get {ref, key}` · `state_set {ref, key, value|null}` | `StateGet`, `StateSet`, `StateDelete` | per storage file; ≤ 64 KiB; `set` from jobs and public-link calls; the KEYS reach listings as `<app>:<key>` so `applies.state` can switch menu rows on them (keep marker keys short and value-free: `pending`, `done`) |
| `state_list {key, limit}` → `{items[{path, name, key, value}]}` | `StateList(key, limit)` | **which files do I keep this on?** — the question a home screen is. Empty `key` = every key of yours; ≤ 500 rows (100 by default); `path` is adapter-qualified, ready for `Surface.open`. A deleted file drops out by itself, a file not yet indexed is still there, and every row passes the ASKING person's permissions — two people get two lists. ⚠ One exception: inside a `tick` there IS no asking person, so the app is told its own rows in full; a public-page call is person-less too and is NOT excepted — a visitor is told nothing |
| `file_lock {ref|path, ttl_days, reason}` → `{until}` · `file_unlock {ref|path}` | `FileLock`, `FileLockPath`, `FileUnlock`, `FileUnlockPath` | jobs only; freezes the file read-only for everyone (admins too) and refuses renames/moves/deletes of it and of the folders above it; 0 = 30 days, at most 365, `LockUntilLifted` (-1) = no end; another app's lock → `busy`; your own jobs still write into the file; lift it when your flow ends — an administrator can force it, and a dated lock expires anyway. `ref` may be one of the job's OWN outputs: that lock is taken when the output is committed |
| `engine_available {engine}` · `engine_run {engine, args[], inputs{name: ref}, outputs[], timeout_s}` → `{exit, stdout_tail, stderr_tail, outputs[{ref,name}], duration_ms}` | `EngineAvailable`, `EngineRun(EngineRequest)` | inputs are copied into a private run directory under the names you give; args are bare tokens (`-i in.mp4 -c:v libx264 out.mp4`), anything naming a path elsewhere is refused before the engine is looked up; new files in the directory come back as refs |
| `users_lookup {q}` → `{users[{user_id, email, name}]}` | `UsersLookup(q)` | empty `q` lists nobody |
| `notify_send {title: Text, body: Text, severity, meta, to_user_id?, target?: {ref|path, action?, view?}}` → `{id}` | `NotifySend(Notice{…, ToUserID, Target: &NoticeTarget{…}})` | the bell; both languages are kept. `ToUserID` addresses one person (bell, push, and mail if they turned it on — so an internal signer needs no `mail_send`); `Target` makes the row clickable: the file plus, optionally, your action or view to open on it — "please sign" lands in the signing screen |
| `mail_send {to, subject, body, lang?}` | `MailSend`, `MailSendIn(lang, …)` | plain text ≤ 64 KiB; filex appends "Sent by the *App* app on filex" — in `lang`, the language you wrote the mail in (`MailSendIn`), when this server speaks it (English, Turkish or a language pack's), else in the language the call runs in (empty on a scheduled wake-up: say it, or a reminder in the requester's language gets the instance's footer) |
| `http_request {method, url, headers, body_b64, timeout_s}` → `{status, headers, body_b64}` | `HTTPDo(HTTPRequest)` | GET/POST/PUT/PATCH/DELETE/HEAD; 8 MiB each way, 30 s; no cookies either way |
| `asset_fetch {url, sha256, max_bytes}` → `{ref, size, cached}` | `AssetFetch(url, sha256, maxBytes)` → `*Asset{Ref, Size, Cached}`, then `OpenInput(a.Ref)` | a file your app needs (a font, a model, a dictionary) downloaded ONCE by the host into your app's cache and read like an input. `https` on a granted `http:` host; `sha256` is required and checked before you see a byte (`integrity`, nothing kept); `max_bytes` ≤ 32 MiB, and one app's cache is held under **256 MiB, least recently used first**, so a file fetched long ago may have to be fetched again; later calls are served from disk (`Cached`). Offline is `unavailable` — tried again after a minute, logged once per outage; a download slower than your call goes on without it and your call gets `timeout` ("still downloading"). Read it into a buffer of exactly `Size` — a growing one holds a large file twice. See APP-PLUGINS-API.md → `asset_fetch` |
| `share_create {page_id, ref\|path, subject, pin, ttl_days, max_visits, state, files[{ref,name}], purpose{label, revoke?, section?}}` → `{token, url, pin?, expires_at}` · `share_revoke {token}` · `share_state {token?, state?}` | `ShareCreate`, `ShareRevoke`, `ShareState`, `ShareStateSet`, `ShareInfo` | jobs only for create. Opens a **real share** at `/s/<token>`, so the administrator revokes it in **Shares** like any link; the PIN comes back once — show it to the requester or send it by a second channel, never in the same mail as the link. `ref`/`path` names the document the link is about (empty = the job's first input), and `ref` may name one of **this job's own outputs** — the link is answered at once and the row is written when that output is committed, which is how an app shares the file it has just made (a job that fails writes no row, so the token answers nothing). A ref that is neither an input nor an output is `not_found` by name, never the first input. `purpose` wins over the page's and is the **only** way a page-less link — the finished document handed to everybody — says what it is. ⚠ `public_page_create` / `_revoke` / `_state` and the `PublicPage*` wrappers are the older spelling of these three, still bound, now deprecated |
| `host_sign_info {}` → `{available, reason?, ca_cert_pem, ca_certs_pem, algorithm}` · `cert_issue {common_name, email, days}` → `{key_ref, cert_pem, chain_pem, not_after}` · `cert_issue {purpose: "platform"}` (the seal) · `host_sign {key_ref, hash: "sha256", digest_b64}` → `{signature_b64}` · `key_destroy {key_ref}` | `HostSignInfo`, `CertIssue`, `PlatformSeal`, `HostSign`, `KeyDestroy`, and `NewHostSigner(issued)` → a `crypto.Signer` | ECDSA P-256 over a 32-byte sha256 digest, DER-encoded; the certificate carries the document-signing EKU and the app's name as OU, and runs ten years by default; issue → sign → destroy per signer. ⚠ Build a verifier's root pool from **`ca_certs_pem`** — every authority the tenant ever signed with, retired ones included — not from `ca_cert_pem`, which is only the live one |

### Signing a PDF with the host key

```go
issued, err := pluginkit.CertIssue(signerName, signerEmail, 0)  // 0 = the host's default, ten years
signer, err := pluginkit.NewHostSigner(issued)          // crypto.Signer; Public() from the cert
// hand `signer` + signer.Cert to your PDF library (digitorus/pdfsign takes a crypto.Signer)
// … it hashes the byte ranges and calls signer.Sign(nil, digest, crypto.SHA256) …
_ = pluginkit.KeyDestroy(issued.KeyRef)                  // the certificate stays, the key is gone
```

⚠ **Ask for a long certificate, or none at all.** A verifier asks whether the
certificate is valid *now*, so a 30-day one makes every signature start
reading "certificate expired" on its 31st day. The key is destroyed seconds
after the signature either way, so the length carries no key risk. Passing 0
takes the host's default.

The live CA certificate (`HostSignInfo().CACertPEM`) is what readers import;
an administrator fetches the same file from
`GET /api/admin/app-plugins/signing/ca.pem` (there is no button for it in the
panel yet).

⚠ **Verify against `CACertsPEM`, not `CACertPEM`.** The bundle carries every
authority this tenant has signed with, retired ones included, because an
operator may rotate or import their own and nothing is ever deleted. A
verifier whose root pool holds only the live authority reports every
signature made before the last rotation as untrusted — which is precisely
what retiring rather than deleting was meant to avoid.

## Screens (surfaces)

A screen is data: a tree of nodes from filex's catalogue, drawn by filex's
own components. Node = `{id?, type, props, children?}`. A node with an `id`
that holds a value contributes `data.values[id]` to the next event; a `form`
contributes its fields *flat* under their keys.

| Type | Props | Value |
|---|---|---|
| `text` | `{text: Text, tone?: muted|danger|info, heading?: bool}` | — |
| `divider` · `row` | — · children side by side | — |
| `form` | `{fields: Field[], values?}` | each field under its key; edits post `change` (debounced 300 ms). See *Fields*, below |
| `steps` | `{items: [{id, label, state: done|active|todo}]}` | — |
| `list` | `{columns: [{key, label, width?, sortable?, align?, format?}], rows: [{id, cells, actions?, sort?}], empty?}` | a row action posts `event: "action"`, `action_id`, `data.row_id`. It is drawn by filex's one table, the explorer's, so a person resizes, sorts, hides and moves the columns; `format: "date"` / `"datetime"` prints an ISO value the way the explorer prints dates, and sorts by the value rather than the printed text |
| `progress` | `{value: 0..100|null, label?}` | — |
| `people-picker` | `{id, value: [{user_id?, email, name?}], multi?, allow_external?}` | the list; internal search needs `users:lookup`, otherwise free email only |
| `pin-input` | `{id, length?: 4..8}` | the string |
| `file-chooser` | `{id, kind: file|dir, value?}` | an adapter-qualified path `docs://a/b.pdf` |
| `preview` | `{path}` or, on a public link, `{ref}` | — |
| `pdf-fields` | `{id, src: {ref\|path}, mode: define\|place\|edit\|fill, fields[], signers?, signer?, types?, stamp_lines?}` | **define**: the boxes as cards, with no document (what is asked of whom); **place**: the document, and the boxes still waiting to be put on it; **edit**: the older one-screen form — each answers the whole `fields[]` (fractions of the page, origin top-left); **fill**: `{fields: [{id, value}]}`. Each field carries a **`label`** — what it is called, shown in the box, in the fill form and in the audit trail; without one a signer hunts for three identical boxes. A signature field says how it is signed (`style`) and which lines are printed under it (`lines`, from the surface's `stamp_lines`). In a `page` view the node fills the viewport |
| `signature-pad` | `{id, modes?, width?, height?, label?, required?, font?, fonts?}` | `{png_b64, mode, font?}` — `label` and `required` draw it like any other form field, with the same `*`; `fonts` narrows the faces a typed signature may use (one face: no picker) and `font` is the one it opens in |

Footer buttons post `event: "submit"` when `primary`, else `"action"` with
the button's id. Answer `{done: true}` to close, `{toast}` to say something,
`{job}` to queue an action with the values you collected — and, when the
person chose where the result goes, `job.output: {mode, name}` (sibling /
version / none, with the manifest's name pattern or a literal) to replace
the action's manifest output for that one job. `Hidden` actions are queued
this way only. `{open: {path, action|view}}` sends the person to a file and
starts one of your screens there.

### Asking for boxes on a document

`pdf-fields` in `define` mode draws the boxes as cards and no document at
all; `place` draws the document and hands out the boxes that still need a
place. Ask them as two steps:

```go
// step 1 — what is wanted, of whom
wire.Node{ID: "fields", Type: "pdf-fields", Props: map[string]any{
    "src": doc, "mode": "define", "fields": fs, "signers": people}}

// step 2 — where it goes
wire.Node{ID: "fields", Type: "pdf-fields", Props: map[string]any{
    "src": doc, "mode": "place", "fields": fs, "signers": people}}
```

A box that has been defined and not yet placed comes back with
`"placed": false`. Refuse the final submit while any of them is waiting —
the wizard says so on the placing step, and the ACTION checks it again,
because the job is the boundary and a box with nowhere to go would be
stamped wherever it was born.

⚠ `edit` is the old one-screen form and still works. Prefer the two steps:
one screen that asks two different questions is the complaint this split was
made to answer.

### What language a call is answered in

Every call carries a language, and the plugin answers in it: `context.locale`
on a view or page event, `locale` on an action run. The host works it out from

1. the caller's account language (`users.locale`), which the interface's own
   language switcher keeps up to date, and
2. failing that, the request's `Accept-Language` — which filex's clients send
   as **the language on screen**, not the browser's install language;
3. then the instance default, then `en`.

Each only when filex offers that language — English, Turkish, or one a
[language pack](#writing-a-language-pack) adds — so the call's language is
the language the screen is in: `es`, `de`, `pt-br` (a region is kept). It is
the same rule the interface and the server's own text follow. A page opened
from a public link is told the visitor's language the same way (the page
sends the one it shows). Answer in it where you can; `Text.Get(lang)` falls
back per string to the base language (`pt` for `pt-br`), then to English —
so an app that ships Spanish is read in Spanish, and one that does not is
read in English, never in another language it happens to carry.

⚠ Until v0.43.0 every language but Turkish reached an app as `en`.

⚠ A `Text` (`{en, tr}`) is resolved in the BROWSER, so both languages travel
and a switch needs no round trip. A `Field.Label`, by contrast, is a plain
string that YOU resolve with the call's language — which is why a language
chosen mid-screen re-asks the surface (`change`) rather than re-opening it:
the answer comes back in the new language with the values still in it.

### Where the result may go, and who a file is waiting for

Four manifest words, each saying something filex would otherwise have to
guess (v0.43):

- **`applies.writable: true`** — your flow ends in writing the file although
  the action's own output is `none` (a signing request: nothing now, the
  signed document at the end). filex does not offer the action on a
  read-only storage — your first screen would only be able to refuse.
- **`output.elsewhere: true`** (with `mode: "sibling"`) — the result may go
  into a folder the person chooses. On a read-only storage the action is
  still offered; your screen reads `input.read_only`, asks with a
  `file-chooser` (`kind: "dir"`, `value: context.home`), and answers the job
  with `Output{Mode: "folder", Dir: <the chosen folder>}`. filex checks the
  folder (the person must be able to write there) before the job is queued.
- **Personal state** — `pluginkit.StateSet(ref, wire.PersonalState("todo",
  userID), "1")` marks the file for ONE person; `applies.state:
  ["todo@me"]` offers the action to that person only. Nobody else's listing
  shows the key.
- **`messages`** — texts filex says for you later, in each reader's
  language: `pluginkit.FileLockMessage(ref, ttl, "lock.collecting", nil)`
  instead of a plain reason string.

`plugintest` checks all four: a manifest that uses one wrongly fails
`CheckManifest`, and the fake host refuses a `@me` key and an undeclared
message the way filex does.

### Dates in your own words

filex prints every date for a person one way — the explorer's: *22 Eyl 2026*,
*Sep 22, 2026*, and with the time *22 Eyl 2026, 15:38* / *Sep 22, 2026,
3:38 PM*. A table column gets that for nothing: send the ISO value with
`format: "date"` (or `"datetime"`) on the `list` column and the host prints it.
A date inside your OWN sentence — "the link is valid until …", a mail, a
notice — is yours to write, and `pkg/pluginkit/humandate` writes it the same
way in `en`, `tr`, `de`, `es` and `fr` (anything else in English):

```go
humandate.Day(lang, t)         // "22 Eyl 2026" — t's UTC calendar day
humandate.DayTime(lang, t)     // "22 Eyl 2026, 15:38" — UTC; say so beside it
humandate.Stamp(lang, stored)  // an RFC 3339 / YYYY-MM-DD value's day, else unchanged
```

Use it; do not write a month table of your own. A server has no reader's
clock, so a time is UTC. Keep ISO 8601 where a value must stay exact and
machine-readable — an audit trail, a form field's value.

### Fields

A `form` field is the storage-driver descriptor — `key`, `type`, `label`,
`help`, `required`, `secret`, `default`, `placeholder`, `options`, `min`,
`max` — with `type` one of `string`, `password`, `int`, `bool`, `select`,
`text`, `date`. Three rules the renderer enforces, so they are not yours to
get wrong:

1. **A `select` is a row of choice buttons, never a dropdown.** Every option
   is readable without a click, which is the point; a `select` with no
   options cannot render at all. `multi: true` lets several be chosen and
   makes the value a list of option values instead of one string. `multi`
   means nothing on any other type.
2. **There is no "advanced".** `Field.Advanced` is gone from the contract. A
   setting that matters belongs on the step; one that does not belongs
   nowhere.
3. **One step asks one thing** — at most one `primary` footer button, plus
   Back. `steps` is the spine of a multi-step screen.
4. **`text` is the LONG field** and draws a text area; `string` is the
   one-line box. Ask for `text` whenever the answer can run to more than a
   line — the signing app's list of signers is a `text` field, because its
   help line says "one signer per line" and a one-line box would make that a
   lie.

A form may not present a contradiction, so a field can depend on another:

```go
wire.Field{Key: "new_name", Type: "string", Label: "Name of the new file",
    ShowWhen:     &wire.Condition{Key: "output", Equals: []string{"new"}},
    RequiredWhen: &wire.Condition{Key: "output", Equals: []string{"new"}}}
```

⚠ Both conditions are checked in the browser **and re-checked by the host at
submit**: a value belonging to a hidden field is **dropped before your job
runs**, so it cannot arrive as a surprise, and an empty `required_when` field
refuses the job. A field that is `show_when`-hidden and unconditionally
`required` can never be filled — use `required_when`.

⚠ `date` is a field **type**. The `date` *rule* on text fields is gone: two
ways to ask for the same thing is how you get two answers, and a stamping
plugin was left guessing whether `01/02/2026` came from a date control or a
text box. Text rules keep `any`, `number`, `email` and the length bounds.

## Public links, end to end

An app's public page is a **share**: the token, the PIN, the expiry, the
visit ceiling and the revoke are the share machine's, which is why an
administrator can see and stop a signature request in **Shares** beside the
downloads, and why there is one PIN implementation rather than two.

1. A job (the "Send for signature" action) exposes the document and opens
   the link: `ShareCreate{PageID: "signer", Subject: …, PIN: "auto",
   TTLDays: 14, State: envelope, Files: [{Ref: in.Inputs[0].Ref, Name: …}]}`.
   It gets `{token, url, pin}` — the URL is `/s/<token>`; mail it
   (`MailSend`), show the PIN to the requester (the job's message, a
   notification), and keep what you need with
   `StateSet(in.Inputs[0].Ref, …)` or in the link's own state.
   ⚠ Never put the PIN in the same mail as the link.
2. The visitor opens `/s/<token>` in the public shell — your instance's
   branding, the PIN gate, the expiry — enters the PIN, and filex calls your
   `page_event` with `open`: read the document copy (`ReadInput("pub:0")`),
   your record (`ShareState("", &st)`), and answer a surface — a
   `pdf-fields` in fill mode plus a `signature-pad`, and a **Sign** button.
3. On `submit`, record what was signed (`ShareStateSet`, `StateSet`) and
   answer `{job: {action_id: "apply", params: {...}}}`. filex queues that
   action **as the link's creator** on the original document, with
   `params.page_token_hash` added; your job issues a certificate, signs,
   writes the signed PDF as a sibling, and revokes or advances the link.
   - ⚠ That submit is gated exactly as a run from inside filex is: the action
     must be one this instance has enabled (and not reserved to
     administrators, unless the person who opened the link is one), and the
     link's creator must still hold the ACL the action needs on the document.
     So a link stops working when its creator's access to the document goes
     away — by design, because the job would otherwise run under rights its
     owner no longer has. Treat a `403`/`409` on the visitor's submit as *this
     envelope is over*, not as something to retry, and let the surface say so.
   - ⚠ If the creator's ACCOUNT is switched off or deleted, the link dies one
     step earlier: `page_event` is not called at all and the visitor gets the
     dead-link screen (**410**). Your app therefore never sees a visit on such
     a link — do not treat the silence as abandonment; a disabled account is a
     pause, and every one of its links resumes when the account comes back.
     ⚠ A page your `tick` opened has no account behind it at all (a wake-up
     runs with no actor) and is never stopped this way.

⚠ A public surface may not carry `open`, and a public call may not write
files: an anonymous visitor has no explorer and no storage. Ask for a job.

## Writing a language pack

A **language pack** is an app that adds a language to filex itself and does
nothing else. It is a manifest and nothing more: **no module, no Go, no
build** — a translator writes JSON. It translates the whole interface — the
file explorer, the admin panel, the settings dialog and the public pages a
share link opens — and the text the server writes: emails, notifications,
the no-JavaScript pages behind a link and the install review's permission
sentences. The fastest start is the template repository,
[BRF-Tech/filex-lang-template](https://github.com/BRF-Tech/filex-lang-template),
which carries the catalogue, the validator and a step-by-step README.

### What makes a manifest a language pack

A manifest that has `ui_locales` and **none** of `actions`, `views`,
`public_pages`, `settings`, `permissions` or `wasm` is a language pack
(`wire.Manifest.IsLanguagePack`). filex installs it from the manifest alone,
never starts a runtime for it, lists it in **Plugins → Apps** as a *Language
pack*, and refuses a module uploaded with it. Anything more — one permission,
one action — and it is an ordinary app that needs its module.

```json
{
  "manifest_version": 1,
  "name": "lang-es",
  "version": "1.0.0",
  "label": { "en": "Spanish language pack" },
  "description": { "en": "The whole filex interface in Spanish." },
  "homepage": "https://github.com/you/filex-lang-es",
  "permissions": [],
  "ui_locales": {
    "es": {
      "ctx.download": "Descargar",
      "appPlugins.title": "Aplicaciones",
      "toast.restored": "{n} elementos restaurados",
      "toast.restored_one": "{n} elemento restaurado",
      "dashboard.fileCount": "{n} archivo | {n} archivos"
    }
  }
}
```

A pack may carry several languages (`"es": {…}, "pt-br": {…}`) and may also
**overlay** a language filex ships (`"tr": {…}` changes only the keys it names).

### Keys: one flat namespace over three catalogues

filex draws its words from three tables, and a pack addresses all of them with
ONE flat object of dotted keys:

| Table | Source | Key | Drawn by |
|---|---|---|---|
| **explorer** | `packages/core/src/locales/en.ts` | as written: `ctx.download` | the explorer, its dialogs, the public pages |
| **admin** | `web/src/locales/en.json` (nested) | the dotted path: `appPlugins.wizard.title` | the admin panel and the settings dialog |
| **both** | 55 keys in both (`storages.driver.*`, `storages.fields.*`, `storages.fieldHelp.*`, `home.title`) | the same key | both, with the same English — one translation serves both |
| **server** | `backend/internal/srvtext/locales/en.json` and the notification phrases of `web/src/lib/notificationText.ts` | everything under `server.`: `server.mail.greeting` | the server: emails, notifications, the no-JavaScript pages, the install review ([Text the server writes](#text-the-server-writes)) |

It is one namespace because no key of one table is a dotted prefix of a key
of another, a shared key has identical English in both interface tables, and
only the server's keys start with `server.`
(`web/tests/i18n/langPackCatalogue.test.ts` fails the build otherwise). One
file per language holds all of it: a translator never has to know which
program prints a string, only which grammar its table follows.

**Get the catalogue** — every key, its English, and which table it belongs to:

- `filex-catalogue-en.json` + `filex-catalogue-context.json` attached to every
  [release](https://github.com/BRF-Tech/filex/releases);
- from any running filex: `https://<your-filex>/admin/i18n/filex-catalogue-en.json`
  (the exact catalogue of that version);
- from a checkout: `node scripts/i18n-export.mjs --out catalogue`.

`filex-catalogue-en.json` is **exactly** the shape of `ui_locales["<lang>"]`:
copy it and translate the values. The context file says per key `in`
(explorer / admin / both / server), `syntax`, the Turkish reference (`tr`),
the source files that use it (`where`) — for a server key instead, which
email or page shows it (`about`) and what each placeholder holds (`vars`) —
and `plural: true` on a sentence about a count. Its top level carries
`plural_categories`: the [plural categories](#plural-forms) of ~60 languages;
`node scripts/i18n-export.mjs --lang <tag>` adds any other. `filex` names the
version: a running server writes its own there.

### One word per concept

filex uses one term for each thing on screen — "API key", never also "token";
"PIN", never also "code"; "storage", never also "disk" — and writes labels in
sentence case. A pack keeps that property in its own language: pick one word
for each row of the glossary in
[CONTRIBUTING.md → Words: one term per concept](CONTRIBUTING.md#words-one-term-per-concept)
and use it in every string, and address the reader the same way throughout
(Turkish uses the polite "siz"). A person reading two names for one thing
assumes two things.

### The grammar depends on the table

| | explorer (`plain`) | admin (`vue-i18n`) | both (`shared`) | server (`server`) |
|---|---|---|---|---|
| a value | `{name}` — the same names as the English | `{name}` (and `{0}`) — the same names as the English | `{name}` | `{name}` — **exactly** the names of the English: one left out is an error, not a warning |
| a plural | separate keys by category: `<key>_zero` … `<key>_many`, the plain `<key>` is `other` ([Plural forms](#plural-forms)) | forms in one string split by a bar, one per category in CLDR order ([Plural forms](#plural-forms)) | — | as the explorer, the count is `{count}` |
| `@` | an ordinary character | starts a linked message — write `{'@'}` | not allowed | an ordinary character |
| the bar `\|` | an ordinary character | splits plural forms — write `{'\|'}` | not allowed | an ordinary character |
| `{` `}` | only around a placeholder | write `{'{'}` / `{'}'}` | only around a placeholder | only around a placeholder |
| `%` right before `{` | an ordinary character | ⚠ `%{x}` is vue-i18n's old *modulo* form and **eats the `%`** (`%{percent}` renders `97`, not `%97`) — write `{'%'}{percent}` | not allowed | an ordinary character (`%s` is **not** a placeholder: it prints as written) |
| `{'…'}` | printed **as written** — never use it | the literal: `{'@'}` and friends | not allowed | printed as written — never use it |

Placeholders may move and repeat; every form of a plural is handed the same
values. A placeholder the English does not have prints wrong (literally in the
explorer and the server's text, empty in the panel) — the validator refuses
it.

### Plural forms

filex picks a plural form by the **CLDR category** of the count in the
reader's language — `Intl.PluralRules` in the browser, the same Unicode rules
on the server: `zero`, `one`, `two`, `few`, `many`, `other`. A language has
the categories its counts fall into: English and Spanish `one`, `other`;
Russian, Ukrainian and Polish `one`, `few`, `many`, `other`; Arabic and Welsh
all six; Japanese and Chinese only `other`. `plural_categories` in the
catalogue's context lists them for ~60 languages; the validator prints yours.

⚠ Only the categories that **whole numbers** fall into count. Modern CLDR also
lists `many` for Spanish, French, Italian, Portuguese and Catalan, but it only
means an exact million ("1 millón de archivos"); filex treats those languages
as `one` / `other`.

**Explorer and server keys** — each form is its own key. The plain key is the
`other` form and the fallback; there is no `_other`:

```json
"toast.restored_zero": "لم تتم استعادة أي عنصر",
"toast.restored_one":  "تمت استعادة عنصر واحد",
"toast.restored_two":  "تمت استعادة عنصرين",
"toast.restored_few":  "تمت استعادة {n} عناصر",
"toast.restored_many": "تمت استعادة {n} عنصرًا",
"toast.restored":      "تمت استعادة {n} عنصر"
```

A form your language lacks shows **your plain form** — never the English
singular inside your sentence; only a key you have not translated at all shows
the English. A form for a category your language does not have is never read
(the validator says `UNUSED`). The count is the call's `{count}`, `{n}` or
`{days}` (the server: always `{count}`).

**Admin-panel strings** — the forms are written inside one string, split by
`|`, in CLDR order of **your language's** categories:

| Your language has | Write |
|---|---|
| all six (Arabic) | `zero \| one \| two \| few \| many \| other` |
| four (Russian) | `one \| few \| many \| other` |
| three (Latvian `zero, one, other`; Romanian `one, few, other`) | them, in that order |
| any number | or the classic 1 form (no inflection), 2 (`one \| other`) or 3 (`zero \| one \| other`) |

(English and Turkish keep vue-i18n's classic rule.) Any other number of forms
is refused.

**The number as a word.** In a category that holds exactly one number in your
language — English and Spanish `one`; Arabic `zero`, `one`, `two` — the form
may leave the count out and say the number as a word (*يوم واحد*, "one day").
Where a category holds several numbers — Russian `one` is also 21, 31, 101 —
the count must stay, or 21 would read "one"; the validator warns (explorer)
or refuses (server).

### Text the server writes

Everything under `server.` is written by the server, not drawn by a screen:

| Keys | What | In whose language |
|---|---|---|
| `server.mail.share.*`, `server.mail.label.*`, `server.mail.valid_days`, `server.mail.no_expiry`, `server.mail.greeting` | the email with a share link (Share dialog → *Send by email*, or an invite to an address with no account) | the **sender's** — the language their screen is in |
| `server.mail.drop_invite.*` | the email asking somebody to upload files (a file-request link) | the sender's |
| `server.mail.grant.*` | an existing account was given access | the **recipient's** account language, else the sender's |
| `server.mail.account.*` | an administrator created the recipient's account while inviting them | the inviting administrator's — the new account starts in it too |
| `server.mail.drop_received.*`, `server.drop.note_from` | files arrived through a file-request link: the owner's email, the notification's stored title, and the first line of `NOT.txt` beside the files | the folder **owner's** account language |
| `server.mail.smtp_test.*` | the mail settings' *Send test* | that administrator's |
| `server.mail.cloud_verify.*` | filex cloud sign-up verification | the visitor's browser language |
| `server.mail.app_footer` | the line under every mail an installed app sends | the language the app wrote the mail in (`MailSendIn`) when the server speaks it, else the language its call runs in |
| `server.notify.*` | the notification phrases — the bell, the browser pop-up | the **reader's** screen language (each reader's own: two people read one notification) |
| `server.public.*` | the pages a share or file-request link opens without JavaScript, the PIN gate, the error pages, the sign-in hop | the visitor's browser language (`?lang=` wins) |
| `server.perm.*` | Plugins → Apps → the install review: what a permission lets an app do | the administrator's |

In every case the language is one the server *speaks* — English, Turkish, or
one an installed pack adds — else the instance default
(`FILEX_DEFAULT_LOCALE`), else English; per key, a string your pack lacks is
English. ⚠ At run time the server re-checks each translation it uses: one
whose placeholders differ from the English is **not used** — the recipient
gets the English line with the link or the PIN in it, rather than your line
without it. Run the validator and it will not come to that.

Emails are plain text, one key per line or paragraph (a mail is assembled
from the facts it has — a PIN or none, a size or none), so every key is a
whole sentence. A subject is one line. filex labels each mail with its
language (`Content-Language`) and encodes a non-ASCII subject for the mail
system; a right-to-left language needs nothing more — plain text carries no
direction, and mail clients lay each paragraph out by its own letters.

The public pages escape your text; the one piece of markup is the footer's
`{filex}`, which becomes the link to filex.sh — keep it exactly once.

### Right-to-left languages

A pack in Arabic, Hebrew, Persian, Urdu … needs nothing extra: the interface
is laid out right to left in its language by itself (the server's one list,
`wire.IsRTL`). What a translator should know about mixed-direction text —
names, paths and numbers inside a sentence — is in [RTL.md](RTL.md).

### What is not in the catalogue yet

- An app's **own** screens speak the languages the app declares (`languages`);
  a language pack does not translate another app.

### Limits

The limits are bytes, sized from the catalogue. Measured on v0.43.0: **3 593
keys**, 190 KiB of keys + values in English (226 KiB as the exported
`filex-catalogue-en.json`). The packs written for this release came out at
**223–286 KB**, and a right-to-left language whose letters are two bytes each
measured about **300 KB**. Every ceiling below therefore has room: the longest English string
is 606 bytes against the 4 KiB per-string limit, the longest key 52 bytes
against 128, and a complete two-byte-script language is about 300 KiB against
the 1 MiB per-language limit.

| | Limit |
|---|---|
| one language (keys + values, UTF-8) | 1 MiB |
| all languages of one manifest | 4 MiB |
| one string | 4 KiB |
| one key | 128 bytes: dotted segments of letters, digits, `_` and `-` (`__proto__`, `constructor`, `prototype` are refused) |
| the manifest document | 16 MiB (room for `\uXXXX`-escaped JSON) |

### Coverage and fallback

Every key a pack lacks shows in **English** — never as a raw key. An empty
value is *untranslated*, not "translate as nothing". A key filex does not have
(a typo, or a string a newer filex removed) is ignored; a plural form of a key
filex has (`toast.restored_few`) is not a typo, and counts neither way.
**Plugins → Apps** shows each language's coverage of the catalogue of the
filex that is running — the server's `server.*` keys included —
*Español — 97% translated · the rest shows in English* — floor-rounded, so
only a complete language reads 100%. Coverage is measured against the running
version, so a pack written for an older filex honestly reports what it misses
after an upgrade.

### Language tags

A tag is `es`, `pt-br`, `zh-hant`… (lower-case in the manifest). Name the
region when it matters (`pt-br` and `pt-pt` are different tables; `es-mx`
also gives Mexican date and number formats). A browser that asks for `es-MX`
gets an `es` pack, and one that asks for `pt` gets `pt-br` when that is the
only Portuguese on offer. The picker shows each language in its own words.

### Validate

```bash
node scripts/i18n-validate.mjs translations/es.json             # in a filex checkout
node scripts/validate.mjs translations/es.json                  # in the template
node scripts/i18n-validate.mjs filex-app.json --complete --missing
```

It checks every rule above against the catalogue — key shape and byte limits,
unknown and missing keys (coverage), placeholders (exact for `server.*`),
plural forms against your language's categories (`UNUSED` for a category it
does not have, the count kept where a category holds several numbers), the
`@`, bar, `{'…'}` and `%{` rules per table, one-line subjects — and exits 1
on an error. It compiles admin-panel strings with vue-i18n's own parser when
that is installed and falls back to a built-in checker that the filex tests
hold to the same answers. `--complete` makes a missing key an error;
`--missing` lists them; `--plurals` lists the plural keys that still lack a
form for one of your categories.

### Install

- **GitHub** — push the manifest as `filex-app.json` at the repository root;
  **Install → GitHub** with `owner/name` (and a tag or branch). No release and
  no `wasm` block: the manifest is the whole distribution.
- **Files** — **Install → Files**, the manifest alone.
- **URL** — the manifest URL, the module URL left empty; a SHA-256, if given,
  pins the **manifest**.
- On an instance that only accepts signed apps (`FILEX_PLUGIN_TRUSTED_KEYS`),
  sign the manifest's sha256 (hex, lower-case) the way you would a module's.

To publish a new version, bump `version` and use **Upgrade** on the app's row.
Removing the pack removes its languages at once; a person who had chosen one
falls back to their next choice (and gets it back if the pack returns).

## Testing with plugintest

`pkg/pluginkit/plugintest` is the test kit that ships with the SDK. It runs
your plugin's own Go functions in-process, behind a fake filex, so `go test`
answers the questions that matter **before `plugin.wasm` exists**:

- does the action do the right thing, and degrade when a permission is
  missing or an engine is absent?
- is every screen drawable — known node types, a readable choice instead of
  a dropdown, one primary button per step, conditions pointing at fields
  that exist?
- does every string carry every language the manifest promises?
- did a screen change shape without anybody noticing?

### Take your host as a parameter

The host functions in `pluginkit` only answer inside wasm; off-wasm they
return `ErrNotWasm`. So a plugin that calls `pluginkit.ReadInput` directly
can only be tested by building the module and installing it. Take the calls
you use as a small interface instead — `plugintest.Host` satisfies it
method-for-method, with the same names and signatures as `pluginkit`'s own
functions:

```go
// internal/job/host.go
type Host interface {
	ReadInput(ref string) ([]byte, error)
	InputSize(ref string) int64
	WriteOutput(name string, data []byte) (wire.OutputRef, error)
	EngineRun(req pluginkit.EngineRequest) (*pluginkit.EngineResult, error)
	Progress(done, total int64, message string)
	Log(level, msg string)
}

// SDKHost is the production one.
type SDKHost struct{}

func (SDKHost) ReadInput(ref string) ([]byte, error) { return pluginkit.ReadInput(ref) }
// … one line per call
```

Assemble the plugin over that host once, and both `main()` and the tests use
the same assembly:

```go
// internal/app/app.go
func Plugin(h job.Host) *pluginkit.Plugin {
	return &pluginkit.Plugin{
		Manifest: myapp.Manifest(),
		Actions: map[string]pluginkit.ActionFunc{
			"convert": func(in *wire.ActionRunInput) (*wire.ActionRunOutput, error) { return job.Run(h, in) },
		},
		Views: map[string]pluginkit.ViewFunc{"options": view.Handle},
	}
}

// cmd/plugin/main.go
func init() { pluginkit.Run(app.Plugin(job.SDKHost{})) }
```

### A complete test

```go
package app_test

import (
	"bytes"
	"testing"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/plugintest"

	myapp "github.com/you/filex-myapp"
	"github.com/you/filex-myapp/internal/app"
)

// The fake host is built from the manifest, so the manifest's permissions
// ARE the administrator's grants: a call the plugin forgot to declare is
// refused here exactly as filex refuses it.
func harness(engines ...string) *plugintest.Harness {
	h := plugintest.NewFor(myapp.Manifest(), func(host *plugintest.Host) *pluginkit.Plugin {
		return app.Plugin(host)
	})
	h.Host.InstallEngine(engines...) // what THIS fake server has
	return h
}

func TestOptionsScreen(t *testing.T) {
	h := harness()
	s, err := h.Open("options", plugintest.File{Name: "photo.png", Data: pngBytes()})
	if err != nil {
		t.Fatal(err)
	}

	plugintest.CheckManifest(t, h.Manifest())     // what filex refuses at install
	plugintest.CheckRegistered(t, h.Plugin)       // every advertised id has a function
	plugintest.CheckSurface(t, h.Manifest(), s)   // the renderer's rules
	plugintest.CheckLanguages(t, h.Manifest(), s) // every declared language
	plugintest.Golden(t, "options", s)            // the stored shape

	// A select renders as a row of buttons, so the test reads it as one.
	if len(plugintest.ChoiceValues(s, "target")) < 5 {
		t.Fatal("a PNG reaches more formats than that")
	}
	if a, ok := plugintest.PrimaryAction(s); !ok || !a.Disabled {
		t.Fatal("with nothing chosen yet, the primary button is disabled")
	}
}

// The same screen in every declared language, compared side by side. This
// is what catches a signature pad saying "Çiz / Yaz / Yükle" under an
// English heading: the plugin picked those strings itself, so only drawing
// the screen twice shows it.
func TestBothLanguages(t *testing.T) {
	h := harness()
	byLocale, err := h.OpenInLocales("options", plugintest.File{Name: "photo.png", Data: pngBytes()})
	if err != nil {
		t.Fatal(err)
	}
	plugintest.CheckLocaleParityOpts(t, byLocale, plugintest.LangOpts{
		SameAllowed: []string{"PNG", "JPEG"}, // a format name is a name
	})
}

func TestTheWholeFlow(t *testing.T) {
	h := harness()
	h.Select(plugintest.File{Name: "photo.png", Data: pngBytes()})

	s, err := h.Submit("options", nil, map[string]any{"target": "jpg"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := h.Queue(s) // run the job the screen asked for
	if err != nil {
		t.Fatal(err)
	}
	body, _ := h.Host.Bytes(out.Outputs[0].Ref)
	if !bytes.HasPrefix(body, []byte{0xff, 0xd8}) {
		t.Fatal("that is not a JPEG")
	}
}
```

### The fake host

`plugintest.Host` is filex's host side in memory — files, settings, per-file
state, locks, engines, signing, notifications, mail, HTTP and shares — and it
answers with the **same codes the real host returns** (`permission_denied`,
`not_found`, `too_large`, `timeout`, `unavailable`, `invalid`, `busy`,
`internal`). It refuses a call the manifest never asked for, and refuses from
a screen what only an action job may do; a kit that says yes to everything is
a kit that lies.

| Wiring | |
|---|---|
| `NewFor(manifest, build)` / `New(plugin)` | build the harness; `h.Host` is the fake instance |
| `h.Host.InstallEngine("ffmpeg")` | what this server *has* (the grant is the manifest's) |
| `h.Host.SetSetting`, `AddUser`, `MailPerHour`, `SignBudget`, `MaxInputBytes`, `SignUnavailable` | the rest of the instance |
| `h.Host.EngineFn` / `HTTPFn` | script an engine's or an endpoint's answer, including failures |
| `h.Host.Network[url] = bytes`, `h.Host.Offline`, `h.Host.Downloads` | what `asset_fetch` can download, an installation with no internet (the cache still answers), and every URL actually downloaded — a cached call adds nothing, so "fetched once" is one line to assert |
| `h.Select(files…)`, `h.Open` / `Change` / `Submit` / `Act`, `h.Do`, `h.Queue`, `h.Page` | drive the plugin |
| `h.Host.Bytes(ref)`, `Outputs()`, `ProgressLog`, `Notices`, `Mails`, `Engines`, `Shares()`, `State()`, `Locked()` | read back what it did |
| `h.Host.CertIssue` → `h.Host.NewSigner(issued)` · `h.Host.PlatformSeal` | a real P-256 CA, so a signature a test makes actually verifies; the seal is the host's own key, is never destroyed, and comes back from the same fake |

### What the assertions catch

| Call | |
|---|---|
| `CheckSurface` | unknown node type · a `select` with no readable options · more than one primary button · `show_when` / `required_when` naming a field that does not exist · a hidden field whose value is still being sent · duplicate node ids or field keys · a `steps` node without exactly one active step |
| `CheckManifest` | the closed permission set · an action whose view is not declared · a placement filex cannot draw · a page without `public_pages` · an output mode without `files:write` |
| `CheckRegistered` | a menu row that opens a screen nobody wrote, and the reverse |
| `CheckLanguages` | a `Text` missing a language the manifest promised, or carrying one it did not declare; the same words under two languages (a missing translation reads exactly like that) |
| `CheckLocaleParity` | the screen drawn once per language: a section only one language has, a string blank in one, a string identical in both |
| `Golden` | the stored shape of a screen; `-update` rewrites it, `git diff` is the review |

Findings come back as a `Report` when you want to look rather than fail
(`InspectSurface`, `InspectLanguages`, `InspectLocaleParity`, …): errors fail
the test, warnings are logged. `LangOpts{SameAllowed: […]}` is for the strings
that are the same in every language on purpose — format names, brands, units.

### Testing a wake-up

The kit runs `tick` too, which matters more than it sounds: a wake-up is the
one call nobody watches, and a mistake in it looks like work that silently
never happened.

```go
h := plugintest.NewFor(manifest, myapp.Plugin)
h.Host.SetSetting("grace_hours", "24")

now := time.Date(2026, 9, 20, 3, 0, 0, 0, time.UTC)
in := h.TickInput(now)          // the window filex would hand you at 03:00
out, err := h.Tick(in)          // or h.Wake(now), which builds the input for you
require.NoError(t, err)
require.NoError(t, plugintest.CheckSchedule(h.Manifest(), in, out))
require.Equal(t, "expire:7f3a", out.Items[0].Key)
```

`Wake`/`Tick` run with the **read-only** scope the host uses, so a `StateSet`
from your tick fails here exactly as it would at 03:00 — the kit refuses what
the host refuses, or it would be teaching you the wrong thing.

`CheckSchedule` applies filex's published bounds (`wire.Schedule*`, the same
numbers `TickInput` carries) and reports **every** complaint at once: a key
that does not match, an action your manifest does not declare, a due time
past `WindowEnd` that will quietly not be scheduled, an item with no files,
a path that is not adapter-qualified, two storages in one item, more items
than will be kept. It is a warning, not the enforcement — the host is what
refuses — but it is the difference between finding out in `go test` and
finding out an hour later in a log.

Both refuse to run at all if the manifest does not ask for `schedule`, or if
it asks and you registered no `Tick`.

### The build gate

Run the suite **before** the wasm build, and produce no module when it fails.
`scripts/build.sh` in both example repositories does exactly this:

```bash
if ! go test ./...; then
  echo "build refused: the tests do not pass, so plugin.wasm is not produced" >&2
  exit 1
fi
GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm ./cmd/plugin
```

A module with a broken screen or a half-translated one is worse than no
module: filex installs it, and the person finds out.

### Against filex itself

`go test` in the filex repository also runs every host function against a
built fixture (`scripts/build-wasm-fixture.sh`), and `e2e/tests/` walks a
browser through install → run → output. That is the end-to-end pass;
`plugintest` is the one you run on every save.

## Other languages

The ABI is the tables above: six exports named `describe`, `action_run`,
`view_event`, `page_event`, `tick`, `on_event`; Extism's input/output/error buffers;
host imports in the `extism:host/user` namespace taking and returning one
Extism memory pointer each (JSON, except the two framed chunk functions:
`file_read` answers `[status u8][bytes]` with status 0 data / 1 EOF / 2 error
JSON, `file_write` takes `[handle u64 LE][bytes]`). Any Extism PDK (Rust,
JavaScript, Zig, C, …) can implement it; the Go SDK is simply the one filex
ships and tests.
