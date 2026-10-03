# App plugins - wire contract

> The exact HTTP routes, JSON shapes and frontend conventions the explorer,
> the admin panel and the public shell are built against. Operators read
> [APP-PLUGINS.md](APP-PLUGINS.md); plugin authors read
> [PLUGIN-KIT.md](PLUGIN-KIT.md); this page is the reference both link to.
> The Go shapes live in `backend/pkg/pluginkit/wire/wire.go` and are the
> source of truth.

## Vocabulary

- **App plugin** (admin UI: "Apps"): a WebAssembly module + `filex-app.json`
  manifest, run inside filex by wazero. Not a storage plugin.
- **Action**: a row the plugin adds to the file menu; running one is an **ops
  job** (`kind: plugin-action`).
- **Wake-up** (`tick`): an app holding the `schedule` permission is called
  once an hour and answers with the work it wants run and WHEN; the host runs
  each answer at its own minute, as an ordinary `plugin-action` job.
- **View**: a declarative screen (`Surface`) the plugin returns and filex draws
  with its own components. Placement `modal` (opened by an action), `page` (a
  full page in a new tab), `inspector` (a section in the details panel), `home`
  (a row under "Apps" in the side nav).
- **Public page**: a screen an outside participant reaches without an account.
  Since v3 it is a **real share**, at `/s/<token>`: the plugin declares the page
  in its manifest and opens the link with `share_create`, and the token, PIN,
  expiry, visit ceiling and revoke belong to the share machine rather than to
  the plugin. `/p/*` is retired and answers a 301
  ([BACKEND.md](BACKEND.md#retired-p-and-apip)).

## Capabilities flag

`GET /api/files/capabilities` → `"app_plugins": {"enabled": true|false}`.
When false the explorer makes no plugin calls at all.

## User surface (`/api/files/plugins/*`, authenticated, confinement + ACL apply)

### `GET /api/files/plugins/actions`
What applies to the caller. Cached client-side for 5 min.
```json
{
  "actions": [
    {"plugin": "sign", "id": "sign", "key": "plugin:sign/sign",
     "label": {"en": "Sign…", "tr": "İmzala…"}, "icon": "sign",
     "applies": {"kind": "file", "ext": ["pdf"], "mime": [], "multi": false, "min": 0, "max": 0,
                 "state": ["pending"], "no_state": []},
     "view": "sign-wizard", "view_placement": "page", "confirm": null, "min_role": "editor", "danger": false,
     "output_mode": "sibling"}
  ],
  "views": [
    {"plugin": "sign", "id": "sign-status", "placement": "inspector",
     "label": {"en": "Signatures"}, "applies": {"kind": "file", "ext": ["pdf"]}},
    {"plugin": "sign", "id": "envelopes", "placement": "home", "label": {"en": "Signatures"}, "icon": "sign"}
  ]
}
```
`applies` is the manifest rule merged with the admin override. The client
mirrors `Matches` (kind/ext/mime/multi/min/max/state/no_state) to decide
which rows to show; the server re-checks on every run.

**Open rules (0.50).** `"open_rules": {"drawio": {"order": ["builtin"], "off":
["app:zeta/viewer"]}}`, by kind (a file name's extension, lower-case, no dot):
the administrator's order for the handlers that open the kind
([Default apps](#default-apps-050)). The client applies it to the `viewer`
views that match a file and filex's own viewer (`builtin`), exactly as the
server does: the handlers the rule names first, in its order; the ones it
switches off left out; the rest after, in the default order (the views in
this list's order, then `builtin`). Absent when no kind has a rule.

**App user permissions (0.49.0).** An action or a view whose manifest entry
`requires` one of the app's `user_permissions` carries it as `"requires":
"app.<app>.<id>"`, and is **absent** from this answer for a caller who does
not hold it - decided per caller by `perm.Result.AppAllowed`: an
administrator, else the person's exception, else their custom role
(`settings.apps`), else the built-in role's decision
(`permissions.app_defaults`), else the manifest's `default`
([PERMISSIONS.md → App permissions](PERMISSIONS.md#app-permissions)). The same
question is asked again by `run`, the view `GET` and every `…/event` (so a job
a surface queues), and the interface's `save` and `call`; a refusal is

```json
{"error": "permission_denied", "permission": "app.sign.request",
 "source": {"kind": "rule", "rule_id": 3, "rule_name": "Contractor"},
 "message": "You do not have the “Request signatures” permission of e-Signature."}
```

before the module is called. A manifest names them as
`"user_permissions": [{"id", "label", "description"?, "default": "viewer" |
"user" | "admin"}]` (default `user`) and `"requires": "<id>"` on an action or
a view; an unknown id, a dotted id, a duplicate, another default or a label
missing a declared language is refused at install (`manifest_invalid`). filex
before 0.49.0 refuses both fields as unknown.

The module is told the answer too: `action_run`'s `actor`, and the `context.actor`
of a view event and of a `ui_call`, carry `"permissions": ["request"]` - the
ids (not the keys) of the app's `user_permissions` that person holds, each
decided by the same question the door asks (`handlers.appPermHeld`; an
administrator holds every one). Absent when the person holds none, on a
public page's `page_event` (no actor) and on work nobody started (a
wake-up's job). It is what the app uses to leave out a hint to an action the
reader cannot run; the doors above still decide.

**Engine-gated extensions (v3.1).** A manifest rule may add
`"engine_ext": {"office": ["docx", "odt", …]}` to a non-empty
`ext`/`mime` list: those extensions apply only while that engine is
installed AND granted to the app (the office engine: while an ONLYOFFICE
Document Server is connected; `libreoffice`, its name before 0.50, is
accepted as the same key). The host folds them into `ext` before this
listing and before the run check (`wasmplugin.withEngineExt`), so the rule a
client receives never carries `engine_ext`. An empty `ext`/`mime` list means
"any file" and cannot be added to - the manifest is refused.

**What a missing engine would add (v0.43, administrators only).** The owner's
rule for anything that depends on how the server is set up: *disabled with the
reason for an administrator, not shown to anybody else*. So for an
administrator a row carries `gated: [{"ext": ["heic"], "needs":
{"kind": "engine", "id": "imagemagick", "name": "ImageMagick"}}]` - the
extensions the rule would ALSO take once that engine (granted to the app, not
installed) is there, the admin's override honoured. The explorer draws the
action on such a file as a greyed row whose tooltip says what is missing
("ImageMagick is not installed on this server - install it and restart
filex."); everybody else gets no `gated` and sees no row. `needs.kind` is
`engine` for a program to install and, since 0.50, `office` for the office
engine - a document server to connect, no restart (`{"kind": "office", "id":
"office", "name": "ONLYOFFICE"}`, "Office documents need ONLYOFFICE, which is
not connected to this server - connect it under External services."); a
client shows a generic sentence for a kind it does not know.
The run check never accepts a gated extension.

**A flow that ends in a write (v0.43).** `applies.writable: true` (needs
`files:write`) says the action can only be completed where its file can be
written although its own output is `none` - "Request signatures" writes
nothing now, the signed document at the end. It is not offered on a
read-only storage and `run` answers `409 read_only` there, exactly as for an
action whose output writes; it also needs editor, like a write.

**A result that may go elsewhere (v0.43).** A manifest action may declare
`"output": {"mode": "sibling", "elsewhere": true}`: where the source's
storage is read-only, the action is still offered (the row carries
`output_elsewhere: true`) and its screen asks the person WHERE the result
should go - a `file-chooser` with `kind: "dir"`, starting at
`context.home` - then answers the job with `output: {"mode": "folder",
"dir": "<storage>://<folder>"}`. See *Per-job output* below for the checks.

**Personal state keys (v0.43).** A key an app writes as `<key>@<user id>`
(`wire.PersonalState(key, id)`) belongs to that one person: their listings
show it as `<plugin>:<key>@me`, nobody else's show it at all, and a rule names
it `"state": ["todo@me"]`. The signing app keeps `todo@<id>` for each signer
with an account who has something to sign now, so "Sign / Fill" is offered to
those people only. The run check reads the keys the same way, so the server
stays the authority; a key written as `…@me` is refused (`state_set`), and a
rule naming one person's number is refused at install.

**State-aware rows (v2).** `applies.state` lists state keys THIS plugin must
have set on the file (`state_set`; any one of them), `applies.no_state` keys
it must not have. Listings carry the keys as `app_state: ["sign:pending"]`
(`<plugin>:<key>`, see below), so the client matches `state` against the
row's keys prefixed with the row's plugin name. A signing app offers
"Sign / Fill" only where `pending` is set and "Request signatures" only
where it is not - both in the same right-click menu, next to the built-in
actions.

**Hidden actions.** An action with `hidden: true` is never listed here and
`run` answers `404` for it; it exists only as the second half of a flow a
surface starts (`{job: {action_id}}`) or a public page queues.

**`view_placement`** says how the action's `view` opens: `modal` (dialog
over the explorer) or `page` (full page in a new tab -
`{base}apps/{plugin}/{view}?path=…`, drawn by the same surface renderer
without the dialog's inner scroll). Views placed `page` are not repeated in
`views[]`.

⚠ `{base}` is the prefix the SPA was served from (`/admin/`, `/drive/`), not
the site root: only those prefixes fall back to index.html (`routes.go` →
`wireStatic`), so a bare `/apps/…` is a 404. See *Frontend needs (v2)* §1.

**Listing fields (v2).** Every file row of `GET /api/files/manager` (index,
search) and of the meta lists (recent/starred/tag) may carry:
- `locked: true` + `lock: {plugin, reason?, reason_text?, until?}` - an app
  holds the file read-only (see *File locks*); the row's `perm` is capped to
  `viewer` for everyone, administrators included. `reason_text` is the reason
  in every language the app wrote it in (a manifest message); show the
  reader's, `reason` is the plain (English) fallback.
- `app_state: ["<plugin>:<key>", …]` - the state keys apps keep on the file
  (≤ 32 per file; values never travel).

### `POST /api/files/plugins/actions/{plugin}/{action}/run`
```json
{"paths": ["docs://reports/nda.pdf"], "params": {"quality": 85}}
```
`paths` are adapter-qualified (`<storage name>://<rel>`, the explorer's own
form) and must all name ONE storage; the older `{"storage_id": 3, "paths":
["reports/nda.pdf"]}` spelling is accepted too. Checks: storage ownership,
ACL ≥ viewer on each path (≥ editor when the action's output mode writes;
`min_role` raises it further), read-only storage refuses writing actions,
E2E-encrypted folders refused, `applies` re-checked against the real files
(state keys included). A file locked by THIS plugin passes the ACL check at
the level the caller would have without the lock - the app that froze the
document is the one that must still write the signature into it.
Answers:
- `202 {"op": {…ops row…}, "job_id": "…"}` - queued.
- `200 {"surface": {…}}` - the action declares a `view` and no `params` were
  sent; open the surface first. The surface's `submit` comes back through
  the view event route, which queues when the answer carries `job`.
- `400` bad body / mixed adapters, `403 permission_denied | encrypted`
  (`permission_denied` also when the action `requires` an app user permission
  the caller does not hold - above), `404 not_found`, `409 read_only`,
  `422 not_applicable`.

### `GET /api/files/plugins/views/{plugin}/{view}?path=docs://x.pdf`
Initial surface (event `open`). `POST …/event`:
```json
{"paths": ["docs://x.pdf"], "state": {…echoed state…}, "event": "submit", "action_id": "next", "data": {"values": {…}}}
```
Footer buttons post `event: "submit"` when the button is `primary`, else
`"action"`, with `action_id` = the button id. → `200 {"surface": {…}}` or,
when the surface carries `job`, the handler enqueues it with the same checks
as `run` and answers `202 {"op": …, "job_id"}`. Views may read the named
files but never write; `state_set` is refused outside a job.

⚠⚠ A primary button arrives as `submit`, every other one as `action` - the
same in the full page, the dialog and an embedded explorer's popup. A plugin
that listens for one event only has a dead button the day the button changes
weight: the signing app's "Convert to PDF" was primary and listened for
`action`, so a click redrew the same screen (fixed in filex-sign 0.1.1). Ask
for the button id on both events.

**`?lang=` - the language on the screen (0.45.2).** The explorer names the
language it is drawn in on every app call (`…/run`, the view `GET`, every
`…/event`, and the ops list it polls), and the app is told THAT language
(`context.locale`, and the job's `locale`), before the account's and before
Accept-Language. An embedded explorer draws the language its host page chose
(`config.locale`) whatever the account says; an app that picks its plain
strings by `context.locale` answered a Turkish popup in English
("Identity"). Without `?lang=`, or with a language the server does not speak,
nothing changes: the account's language, then Accept-Language. A field whose
label, help or placeholder is a `{lang: …}` map (wire.Field `I18n`, an
option's `LabelI18n`) is drawn in the screen's own language whatever the app
was told.

**`paths` on every event (#64).** A screen opened on a selection
names the WHOLE selection on every event - `change`, `action` and `submit`,
not only the opening `run` - and the event's `context.inputs` are built from
it. The explorer sends `paths` beside the older single-row `path`; when both
come, `paths` wins, and `path` alone (an older client, a deep link) still
means that one file. Each path is judged again for the person asking (viewer
ACL, encrypted folders → `403` naming the path, before the plugin is asked);
a submit that queues a job re-checks the action's `applies` on every file
(`422 not_applicable`). At most 500 paths, as for `run` (`400 too many
paths`). Up to 0.44.2 the explorer echoed only `path`, so from the second
event on a multi-file screen was answered about its first file and its job
ran on that file alone.

**v3.1:** `?section=<id>` on the opening `GET` hands the plugin
`data.section` (a home page's menu - see *Placements*), and every view
event's `context.actor` carries `ip`: the address the person's request
came from, read the way a public page's `data.page.visitor_ip` is (and,
since 0.49.0, `permissions` - see *App user permissions* above). ⚠ It is
personal data. The signing app prints it under a signature only when the
requester chose that line, and only after the signer has seen it on the
step where they approve what will be printed.

**v3.1:** every file reference a view, a job or a page is handed carries
`read_only: true` when its storage takes no writes. The host refuses a job
that WRITES on such a storage (`409 read_only`), but a flow can end in a write
that its first job does not make - a signing request writes nothing until the
last signer answers - so an app whose flow ends in a write reads this and
refuses at its first screen and in its first job, before anything is frozen or
anybody is told.

**Per-job output (v2).** `job` may carry `output: {mode, name}` - the
surface's "same file as a new version / new file beside it / custom name"
choice - which replaces the action's manifest output for that one job.
`mode` is `sibling | version | none | folder` (anything else → `400`); `name` takes
the manifest pattern (`{stem}`, `{ext}`, `{name}`) or a literal.

**v0.43 - `folder`:** `output: {"mode": "folder", "dir": "depo://reports"}`
writes the result as a new file into the folder the person chose - only for
an action whose manifest output says `elsewhere: true` (else `400
folder_not_offered`), and never from a public page. The folder is judged as
any write there: an adapter-qualified folder (`400`), a storage that exists,
is enabled and is the caller's (`404`), that is not read-only (`409
read_only`), a folder that exists (`404`), the caller at least **editor** on
it (`403 permission_denied`), and no lock or filex-own folder in the way
(writegate: `403`/`423`). The source then only has to be readable. The job's
output is reported adapter-qualified on ITS storage (`reports://x-upper.txt`),
and the file is judged by writegate again when it is written; a storage made
read-only in between refuses it. `context.home` on a view is the default
folder: the root of the first storage, in the administrator's order, the
person may write (enabled, not read-only, not a replica, their tenant, editor
at the root), empty when there is none. The ACL
level required is computed from the EFFECTIVE mode, so a surface cannot turn
a read-only action into a write for a viewer. The override rides in the job
params under `__output` and is stripped before the guest sees them; the
guest sees the effective `output` in `ActionRunInput.Output`.

### Ops rows
`GET /api/files/ops` rows for plugin jobs carry `kind: "plugin-action"`
(the queue's own field; there is no separate `op_type`), plus `plugin`,
`action`, `label` (the action label), `message` (the
last `job_progress` message, or the plugin's final message) and, once
committed, `outputs: [{"path": "docs://reports/nda-signed.pdf"}]` -
adapter-qualified so the tray can navigate. Progress rides on the existing
`bytes_done` / `bytes_total` (from `job_progress` done/total). Statuses:
`pending | running | ok | failed | cancelled`. New: `POST /api/files/ops/{id}/cancel`
→ `200 {"op"}`; `409 already finished`; `403` when the op is somebody else's
and the caller is not an administrator.

**Where a finished job sends its person (0.50).** A row whose job has
FINISHED (`ok`) may carry `open: {"path": "docs://reports/nda.pdf", "view":
"request"}` (or `action`, or neither): the job result's `surface.open`,
resolved to one of the job's own outputs, adapter-qualified. Never while the
job runs or after it failed, and never a file the job did not produce. Only
the screen that queued the job follows it - the app's full page goes there in
its own tab (a `page` screen on its address, anything else through the
explorer's deep link), and an explorer opens the screen on the file for a job
its own dialog queued, unless another dialog is open by then. An
administrator's list carries other people's rows, with their `open`; a client
does not follow those. See [PLUGIN-KIT.md](PLUGIN-KIT.md) → "A job that sends
its person on".

**The reader's language (0.45.2).** `label`, `message` and - for a job the app
itself failed (`error_code: "app"`) - `error` are said when the row is READ,
in the reader's language (`?lang=`, then the account's, then
Accept-Language): the job row keeps every language the app wrote (the action
label, the app's final message) instead of flattening them to the
submitter's at creation. A progress line (`job_progress`) is one string and
stays as it was sent. Rows written before 0.45.2 read as they were.

## The scheduled wake-up (`tick`) - v3

The one path on which plugin code runs with **nobody present**. It exists
because an app cannot close its own deadlines: a signing app's `opExpire`
runs only when somebody opens the status screen, so a request that lapses at
03:00 notifies nobody until a human happens to look.

### The shape

Host → guest, export `tick`, once an hour per app:

```json
{ "now": "2026-09-20T03:00:00Z",
  "window_start": "2026-09-20T03:00:00Z",
  "window_end": "2026-09-20T04:00:00Z",
  "max_items": 64, "max_paths": 16,
  "settings": {"…": "…"}, "engines": {"ffmpeg": true} }
```

Guest → host:

```json
{ "items": [
    { "key": "expire:7f3a",
      "due_at": "2026-09-20T03:00:00Z",
      "action_id": "expire",
      "paths": ["docs://reports/nda.pdf"],
      "params": {"reason": "lapsed"} } ],
  "note": {"en": "2 closing tonight"} }
```

`window_end` is the next hourly boundary. `due_at` **after** it is not
scheduled and is not an error (the next wake-up asks again); `due_at` in the
past runs at once. `key` is the app's own idempotency key - 1-64 characters
of `[A-Za-z0-9]` plus `_.:@-` - and the host keeps at most one item per
(app, key), so naming it again MOVES the item rather than adding another.
`paths` are adapter-qualified, exactly as `state_list` answers them, at least
one and all on one storage. `note` goes to the app's log ring.
⚠ Every path must be a file **this app keeps state on** - one a person's job
handed it earlier and the app recorded (the rows `state_list` returns). Any
other path refuses the item (the reason is on the wake-up row), and an item
whose state is gone by its due time is `skipped`, not run. The host's own
parameter names (`__output`, `page_token_hash`, `share_id`) are dropped from
an item's `params`.

### Manifest

One permission, `schedule`, in the closed set like every other. It appears in
the install review with a plain-language label in both languages ("Wakes up
once an hour on its own and runs its own work at the minute it chooses, with
nobody present"), and `GET /api/admin/app-plugins` carries
**`"scheduled": true`** on the row so the list can mark it. An app without
the permission is never woken and `tick` is never called; an app WITH it that
exports no `tick` is **refused at install** (state `refused`) rather than
failing silently an hour later.

### What the call may touch

`tick` runs with a **read-only** scope - the same one a view gets, with no
storage, no actor and no inputs. Allowed: `settings_get`, `state_get`,
`state_list`, `users_lookup`, `notify_send`, `mail_send`, `http_request`
(each still gated by its own permission). ⚠ `state_list` is the one a tick
cannot do without - with no inputs there is nothing for `state_get` to key on -
so it answers the app's own rows in full, unnarrowed by anybody's ACL,
because there is nobody to narrow it by (see `state_list` below). Refused: `file_create` /
`file_write`, `state_set`, `file_lock` / `file_unlock`, `share_create`, the
`sign` family. The writing happens in the action the wake-up schedules, which
is a normal job with a writable scope.

### What the host does with the answer

Each accepted item becomes a row in `app_plugin_schedule` (migration 00050).
When it comes due the host mints an `app_plugin_jobs` row and submits a
`plugin-action` op, exactly as `POST …/run` does, with **`actor_id` null**
(SYSTEM). From there it is an ordinary job: the ops tray row, `plugin`,
`action`, `label`, `message`, `outputs`, progress and cancel are unchanged.

Timing: the scheduler sleeps until the next row is actually due (capped at
30 s so a row another process wrote is noticed), so an item due at 03:00:00
runs at 03:00:00 - never before it.

### Bounds

| | |
|---|---|
| Wake-up interval | 1 h, re-armed to the hour boundary (a restart at 03:37 does not move later wake-ups to :37) |
| First wake-up | 30 s after a load, so a restart rebuilds the schedule |
| Call budget | manifest `limits.call_timeout_s`, capped at **30 s** |
| Items per wake-up | 64; extras dropped and counted in the app's log |
| Files per item | 16, one storage, ≥ 1 |
| `params` | ≤ 16 KiB |
| Rows per pass | 100 |
| Retention | finished rows swept after 7 days |

### Failure, and why nothing loops

A `tick` that traps, times out or errors is logged, written onto the wake-up
row and **re-armed for the next hour**. An item that cannot be queued is
`failed` and is **never** retried - the next wake-up is the retry. An item
whose app is stopped, uninstalled, or whose action the administrator switched
off is `skipped` with the reason on the row. Statuses: `due → running →
queued | failed | skipped`.

### Two processes, and a restart

`status` is the lease. Claiming is one conditional UPDATE from `due` to
`running`, so of two filex processes on one database only the one whose
UPDATE matched a row runs it; `claimed_by` names the node. The schedule is
written when the wake-up ANSWERS, not when the work runs, so a process that
is down at 03:00 finds the row still `due` when it comes back and runs it
then - once, late rather than never.

⚠ A scheduled job has **no person behind it**, so no ACL is applied: it runs
with the app's own grants on the files the app named - and it may name only
files it keeps state on, i.e. files people handed it before. Granting
`schedule` is granting unattended execution on those files, not on every file
of the instance.

### Switches

`FILEX_APP_PLUGINS_DISABLED=1` leaves no registry at all, so nothing is armed
and nothing runs. **Demo mode** stops the scheduler from the inside as well:
no wake-up is armed, no pass looks at a row, and `tick` answers `refused`.

## Admin surface (`/api/admin/app-plugins`, supertenant admin, demo-refused)

⚠⚠ Install and upgrade (except `?dry_run=1`), `PATCH`, `POST …/rollback`,
`DELETE` and `PUT …/overrides` need an administrator **signed in to the
panel**: an API key gets `403 {"error": "session_required",
"request_endpoint": "/api/admin/plugin-requests"}`, whatever its scopes. A key
leaves an install request there instead - the shapes are in
[BACKEND.md → Admin: plugin requests](BACKEND.md#admin-plugin-requests), the
model in [APP-PLUGINS.md → Install requests](APP-PLUGINS.md#install-requests).
A request freezes the `wasm_sha256` (or `manifest_sha256`) and the
`permissions` ids of the dry run below, and approval installs with exactly
those as the pin and the grant.

### `GET /api/admin/app-plugins`
```json
{
  "runtime": {"enabled": true, "arch_ok": true, "disabled_reason": "", "requires_signature": false,
              "engines": {"ffmpeg": true, "imagemagick": true, "office": false, "ghostscript": true, "poppler": true, "rsvg": true},
              "filex_version": "0.47.0", "compat_enforced": true,
              "update_check": true, "updates_checked_at": "2026-09-26T03:00:00Z"},
  "plugins": [
    {"id": 1, "name": "sign", "version": "1.0.0", "label": {"en": "e-Signature", "tr": "e-İmza"},
     "enabled": true, "state": "running" | "disabled" | "refused" | "failed", "state_error": "",
     "source": "upload" | "url" | "github" | "bundle", "source_url": "", "sha256": "…", "signed": false,
     "permissions": ["files:read", …], "actions": 2, "views": 2, "public_pages": 1,
     "scheduled": true, "kind": "app" | "language-pack", "languages": [],
     "manifest_url": "", "compat": {"requires": ">=0.47.0", "ok": true, "filex": "0.47.0"},
     "update_source": "github" | "url",
     "update": {"checked_at": "…", "status": "current" | "available" | "needs_approval" | "incompatible" | "check_failed",
                "version": "1.3.0", "ref": "v1.3.0", "added": ["mail:send"], "adds_module": false,
                "requires": ">=0.48.0", "refusal": {"error": "…", "…": "…"}, "notes": "…"},
     "previous": {"version": "1.1.0", "replaced_at": "…", "ui": true},
     "engine": true, "ui": {"sha256": "…", "files": 12, "bytes": 81920, "unpacked": 250000, "csp": [], "external": []},
     "created_at": "…", "updated_at": "…"}
  ]
}
```

`filex_version` is the filex app ranges are judged against; `compat_enforced`
is false on a development build (no range is checked). `update.status:
"incompatible"` names the newest newer `version` this filex cannot run and
what it `requires`: the range it declares, or `">"` this filex when its
manifest carries a field (or a `manifest_version`) this filex does not know -
the check reads such a manifest leniently for its name, version and range
only (0.49.0; before, it answered `check_failed`), and install and upgrade
still refuse it. `compat` is absent
when the manifest declares no range; `ok: false` on an installed app is a
warning - it keeps running. `update_source` is absent for an app installed
from a file (nothing to check). `update` is what the last update check found
(absent before the first): `refusal` is an install refusal's body
(`error`, `message`, `reason`, `where`, …), so the panel says it with the
install wizard's sentences; `notes` are the source's release notes for
`version` (a GitHub release's body as text - Markdown - up to 8 KiB). ⚠⚠ Nothing
updates itself since 0.48: the check only records what it found. A row
filex 0.47 wrote may still carry `status: "failed"` and `auto` (its
automatic update); nothing writes them any more. `previous` is the version
the last approval replaced, kept to go back to (`POST …/{id}/rollback`);
absent when none is kept. `engine` is false for an app without a module;
`ui` describes its own interface (absent without one). The exact bytes:
`backend/internal/api/handlers/testdata/wire/app-plugin-update-check.json`.

### `POST /api/admin/app-plugins` - install
Three bodies:
1. multipart: `wasm` (file), `manifest` (file, filex-app.json), optional
   `ui` (the interface bundle, a zip - [An app's own interface](#an-apps-own-interface-v4)),
   optional `signature` (hex/base64 ed25519 over the wasm's sha256 hex), plus
   a JSON field `grant` = `{"permissions": [...]}`.
2. JSON `{"github_repo": "BRF-Tech/filex-sign", "ref": "v1.0.0", "permissions": [...]}` -
   filex fetches `filex-app.json` from the repo at `ref` (default branch when
   empty), then `wasm.url` (`{tag}` expands to `ref`), verifies `wasm.sha256`.
3. JSON `{"url": "https://…/plugin.wasm", "manifest_url": "https://…/filex-app.json", "sha256": "…", "permissions": [...]}`.

A **language pack** takes each body without its module: no `wasm` part, no
`wasm.url` in the repository's manifest, an empty `url` (see *Language packs
(no module)* below). The manifest part is read up to 16 MiB and a larger one
is refused `413 too_large` rather than truncated.

`?dry_run=1` answers `200 {"manifest": {…}, "permissions": [{"id": "files:read", "label": "…", "reason": {"en": "…"}}], "wasm_sha256": "…", "wasm_bytes": N, "signed": bool, "kind": "app"|"language_pack", "manifest_sha256": "…", "languages": [{"code", "keys", "translated", "unknown", "total", "percent", "rtl"}], "compat": {"requires", "ok", "filex"}}` without installing - the wizard's permission-review step. `reason` is the manifest's `permission_reasons[id]` (may be absent). `compat` (absent without a range) with `ok: false` means the install will be refused `incompatible`. `engines_missing: [{"id", "name", "kind"?}]` lists the engines the manifest asks for that this server lacks; `kind: "office"` (0.50) marks the office engine, a document server to connect rather than a program to install. `…/{id}/upgrade?dry_run=1` answers the same shape plus `"upgrade"` - the version it leaves, how the grant changes (`added` is what the administrator approves), and what else the version changes:

```json
"upgrade": {"from": "1.1.0", "added": ["public_pages"], "removed": ["mail:send"], "adds_module": false,
            "module_from": "5f1c…", "module_to": "a995…",
            "ui_from": "", "ui_to": "c0ff…",
            "ui_files": {"added": ["index.html", "app.js"], "removed": [], "changed": [],
                         "added_count": 2, "removed_count": 0, "changed_count": 0},
            "filex_from": ">=0.45.0", "filex_to": ">=0.48.0",
            "signed_from": true, "signed_to": false,
            "notes": "Signers can now be reminded."}
```

`module_*` / `ui_*` are the SHA-256 of the module and of the interface bundle
before and after (absent where there is none); `ui_files` compares the two
bundles file by file (by CRC-32 and size), each list cut at 200 names with
the counts whole; `filex_*` are the ranges as the review reads them; `notes`
are the source's release notes (the `Review update` path), the release's
Markdown as text: the panel draws it through the explorer preview's Markdown
pipeline and the document sanitizer (`markdownToSafeHtml`), never as markup of
its own. The exact bytes: `testdata/wire/app-plugin-upgrade-review.json`.

**File types (0.50).** An install's dry run also answers `file_types` when
the app opens or draws kinds of file: one row per kind and capability,
`{"capability": "open"|"thumbnail", "ext": "jar", "mime": "application/java-archive",
"handler": {"id": "app:pkglist", "app": "pkglist", "version": "0.1.0", "label": {…}},
"current": [{"id": "builtin"}, …], "default": "first"|"last"}` - who handles the
kind now, in order, and where the app lands when nothing is chosen (the
kinds of a media type are the known extensions of that type). The install
takes the choices as `associations` (JSON field; a multipart form field of
that name holding the JSON list): `[{"capability", "ext", "handler", "place":
"first"|"last"|"off"}]`. They are written once the app runs, only for the
new app's own handlers; the answer carries `association_errors` (a list of
sentences) for any that could not be. An upgrade's dry run answers
`file_types` for the kinds the new version ADDS and only those (absent when it
adds none), and the upgrade takes `associations` the same way: a choice for a
kind the installed version already handled is refused into
`association_errors` (*not a kind this version adds*) and its order is left
as it is. The wire shape: `testdata/wire/app-plugin-file-types.json`.

**Install requests.** `GET /api/admin/plugin-requests/{id}` answers, for a
pending app request, `request.file_types`: the same rows, worked out when it
is read (an upgrade request: the kinds it adds). `POST …/{id}/approve` takes
`{"associations": [...]}` (optional), places them as an install or an upgrade
does, answers `request.association_errors`, and writes them into the
`plugin_request.approve` audit row (`file_types`).

`permissions` (granted) must equal the manifest's set exactly → else
`400 {"error": "permissions_incomplete", "missing": [...]}`. Success `201` with
the row as in the list. Errors: `400 manifest_invalid`, `400 sha256_mismatch`,
`400 signature_required|signature_invalid`, `409 name_taken`, `409 describe_mismatch`,
`409 incompatible` (the manifest's `filex` range leaves this filex out:
`{"error": "incompatible", "requires": ">=0.48.0", "filex": "0.47.0"}`).

Nothing an install starts updates itself (0.48): there is no per-app switch.

⚠ The server COMPILES the module before it answers - tens of seconds for a
large one (the 20 MB signing module: 23-33 s measured, 2026-09-21). The admin
page waits up to 180 s for an install or an upgrade (`INSTALL_TIMEOUT_MS`, not
the client's 30 s default), and the server finishes - or undoes - an install
or an upgrade whatever the client does: a closed tab cannot cut the compile
half-way or leave a row behind that no list shows and every later install
calls `name_taken`.

### `GET /api/admin/app-plugins/{id}`

An envelope: the app's own row - the list's shape - one level down, under
`plugin`, and everything about it beside that:

```json
{ "plugin": { "id": 7, "name": "sign", "version": "1.2.0",
              "label": {"en": "e-Signature", "tr": "e-İmza"}, "signed": true, "permissions": ["files:read", "…"] },
  "manifest": { "…": "…" },
  "granted": ["files:read", "…"],
  "permissions": [ {"id": "files:read", "label": "Reads the contents of the files it is opened or run on, and of the files it keeps records about",
                    "reason": {"en": "To read the document you sign or send.", "tr": "İmzaladığınız ya da gönderdiğiniz belgeyi okumak için."}} ],
  "settings": {"tsa_url": "…"}, "setting_fields": [ … ], "overrides": [ … ], "schedule": [ … ] }
```

⚠ `permissions` at the top is NOT the id list: it is the reviewed rows, the
install review's shape - `label` is filex's own words already rendered in the
caller's language, `reason` is the app's words in every language it wrote them
in. The ids are `granted` (and `plugin.permissions`). Every localised field
here - `label`, `description`, `reason` - is an object of languages, never a
string; read it in the viewer's language.

⚠ **Where filex has words of its own for the same thing** - a date box's
`order` / `separator` / `example` captions, a list's empty line, a progress
line - the order is: the app's text **for this reader**, then **filex's own
string**, and only then the app's other languages. An app that ships
en/tr/es/de/fr does not put English in front of a reader whose language filex
itself translates (v0.43.0; `pluginTextOr` in the client package). Where filex
has no words for it, the app's other language is shown, as before.

The exact bytes, written and checked by the server's own test, are in
`backend/internal/api/handlers/testdata/wire/app-plugin-detail.json`.

Also carries **`schedule`** - the app's rows from `app_plugin_schedule`,
soonest first, empty for an app that is not woken:

```json
"schedule": [
  {"plugin_id": 3, "key": "", "due_at": "2026-09-20T04:00:00Z", "status": "due",
   "attempts": 19, "error": "2 scheduled"},
  {"plugin_id": 3, "key": "expire:7f3a", "due_at": "2026-09-20T03:40:00Z",
   "action_id": "expire", "storage_id": 1, "status": "due", "attempts": 0}
]
```

The row with the empty `key` is the **wake-up itself**: `due_at` is when the
next one is, `error` is what the last one decided (the app's own note plus
"N scheduled, N beyond this window, N refused (why)"), `attempts` counts how
many times it has been woken, and `claimed_by` names the node that took it
while it was running. Every other row is one piece of work.
Row + `manifest` + `granted` + `overrides` + `settings` (secret values masked
as `"***"`) + `describe` (last answer).

### `PATCH …/{id}` `{"enabled": bool}` · `DELETE …/{id}` · `POST …/{id}/upgrade` · `POST …/{id}/rollback`
`PATCH` with `auto_update` answers `400 bad_request` ("automatic updates were
removed: every newer version waits for an administrator's approval").
`POST …/{id}/rollback` puts back the version the last approval replaced
(`previous` on the row) under the permissions it ran under - no new approval,
and the version it replaces becomes `previous` in its turn. `404 not_found`
when none is kept; `400 sha256_mismatch` when the kept files changed on disk
since (the running version stays). Audited `app_plugin.rollback`; an upgrade
is audited `app_plugin.upgrade` - both with the administrator, `from`, `to`,
the permissions added and dropped, and the interface's hashes when they
changed. Every approved change is also sent to every open explorer as the
realtime frame `{"type": "app.updated", "app": "…", "version": "…"}` (an
interface open on the old version offers to reload).

`POST …/{id}/upgrade`
(same bodies as install; a manifest that asks for permissions not yet granted
answers `409 {"error": "permissions_changed", "missing": [...]}` until the body
grants them). An upgrade may also take `{"from_source": true, "permissions":
[...]}`: the newer version the app's own source has, found and fetched
exactly as the update check does - `409 up_to_date` when there is nothing
newer, `409 incompatible` when the only newer version needs another filex,
`400 manifest_invalid` for an app installed from a file (no source).

### `POST /api/admin/app-plugins/updates/check`
Asks every app's source for a newer version now and records what it found -
it installs nothing (0.48: every newer version waits for an administrator).
Answers `200 {"report": {"checked_at", "checked": N, "updated": [] (always empty since 0.48),
"available": [names], "needs_approval": [names], "failed": [names]},
"runtime": {…}, "plugins": [...]}` - the list redrawn. One check runs at a
time: a second caller waits for the one in flight and gets its answer. `403
demo_refused` on a demo. The daily check (`FILEX_APP_PLUGIN_UPDATE_CHECK`)
runs the same code; the rules are in [APP-PLUGINS.md → Updates](APP-PLUGINS.md#updates).

### `GET/PUT …/{id}/settings`
`{"values": {"tsa_url": "https://…", "api_key": "***"}}`; a `"***"` value on PUT
leaves the secret unchanged; secret fields are sealed at rest.

### `GET/PUT …/{id}/overrides`
`{"actions": [{"id": "sign", "enabled": true, "applies": {…} | null, "admin_only": false}]}`
`applies: null` = manifest default.

### `GET …/{id}/logs?after=N` → `{"lines": [{"seq": 12, "ts": "…", "level": "info", "msg": "…"}], "next": 13}`
Ring buffer of the last 500 lines (guest `log` + host warnings).

### `GET/PUT …/{id}/thumbnails` (0.50)
An app with a `thumbnails` block: its thumbnail limits.
`{"values": {"max_input_mb": 32, "timeout_s": 10, "memory_mb": 64, "concurrency": 2},
"stored": {…0 = the default…}, "defaults": {…}, "min": {…}, "max": {…},
"ext": ["jar"], "mime": []}`. PUT takes `{max_input_mb, timeout_s, memory_mb,
concurrency}` (each 0 = the default) and answers the same; a value outside
its range is `400 {"error": "out_of_range", "field": "timeout_s"}`. Ranges:
1 to `FILEX_APP_PLUGIN_MAX_INPUT_MB`, 1 to 60 s, 16 to 256 MB, 1 to 8. PUT
needs a signed-in administrator (`403 session_required` for an API key), is
refused on a demo, and is audited `app_plugin.thumbnail_limits` (before and
after). `404` for an app that draws no thumbnails.

### Default apps (0.50)

`GET /api/admin/file-types` → `{"kinds": [{"ext": "drawio", "mime": "…",
"open": {"on": [Handler…], "off": [Handler…], "custom": bool, "rule": {"order",
"off"}?}, "thumbnail": {…}}], "enabled": bool, "editable": bool}` - every kind
an app handles (its extensions, and the known extensions of its media
types) and every kind with a rule. A `Handler` is `{"id", "app"?, "view"?,
"version"?, "label"?}`; `id` is `builtin`, `app:<app>/<view>` (open) or
`app:<app>` (thumbnail). `editable` is false for an API key and on a demo.

`PUT /api/admin/file-types/{ext}` `{"open"?: {"order": [...], "off": [...]} | null,
"thumbnail"?: {…} | null}` - a capability left out is unchanged, `null` puts
it back to the default. Every id must be a handler the kind has here (`400
invalid_rule` otherwise, nothing stored). `DELETE /api/admin/file-types/{ext}`
puts both back. Supertenant only (`403 supertenant_only`), a signed-in
administrator (`403 session_required` for an API key), refused on a demo,
audited `file_association.update` / `file_association.reset` with `ext`,
`before` and `after`. Removing an app takes its handlers out of every rule.

**The server's own line.** An interface switched off for a kind is refused on
a file of it - its save (`PUT …/ui/{plugin}/{view}/save`, the file's name or
a save-as name) and its module (`POST …/ui/{plugin}/{view}/call`, each path):
`403 {"error": "handler_off"}`.

**A person's own choices** ("always open this kind with this app"), one
record per account for every surface: `GET /api/me/open-with` →
`{"choices": {"drawio": "app:drawio/editor", …}}`; `PUT /api/me/open-with/{ext}`
`{"handler": "builtin" | "app:<app>/<view>"}` (`400 bad_kind` / `bad_handler`,
at most 512 kinds) and `DELETE /api/me/open-with/{ext}` answer the new set;
`DELETE /api/me/open-with` forgets them all. `GET /api/me/prefs` carries them
as `openWith` (a JSON string, for every surface) and `PUT /api/me/prefs`
drops that key. A choice is not checked against what is installed: the
explorer uses it only where it is on.

### Errors
`{"error": "<code>", "message": "…", "missing": [...]}`. Codes: `manifest_invalid`
(400), `sha256_mismatch` (400), `sha256_required` (400), `signature_required` /
`signature_invalid` (400), `permissions_incomplete` (400, `missing`), `name_taken`
(409), `describe_mismatch` (409), `permissions_changed` (409, upgrade, `missing`),
`too_large` (413), `fetch_failed` (502, `reason` one of `bad_repo`,
`manifest_not_found`, `module_not_found`, `unreachable`, `http_status`,
`bad_url`, `missing_url`, `too_large`, `changed`), `incompatible` (409,
`requires`, `filex`), `up_to_date` (409), `demo_refused` (403), `not_found` (404),
`app_plugins_disabled` (503, admin) - the user routes answer 404 when the
runtime is off. `GET /api/admin/app-plugins` answers 200 even then, with
`runtime.enabled=false` and `runtime.disabled_reason`.

`bad_url` (0.50.0) also covers an address filex does not download from: every
app download - an install, an install request, the update check, the module
and interface bundle a manifest names - goes through the one guarded client
the storage plugins use. Public addresses only, judged after DNS and on every
redirect hop (at most five), and never a redirect from `https://` to plain
`http://`. Plain `http://` is accepted for `127.0.0.1`/`localhost`/`::1`
only, and those are reachable only with `FILEX_PLUGIN_LOOPBACK_SOURCES=1`
(development and tests, [CONFIGURATION.md](CONFIGURATION.md#storage-plugins)).

## The thumbnail call (`thumbnail`) - 0.50

The manifest's `thumbnails` block and what the call may do are in
[PLUGIN-KIT.md → Drawing thumbnails](PLUGIN-KIT.md#drawing-thumbnails); the
pipeline around it in [thumbnails.md → Thumbnails drawn by apps](thumbnails.md#thumbnails-drawn-by-apps-one-chain-per-kind).
The contract:

- **Manifest**: `"thumbnails": {"applies": {"kind": "file", "ext": [...], "mime":
  [...]}}` - nothing else in the rule. Each kind derives a permission,
  `thumbnail:.<ext>` / `thumbnail:<type>` (a line of the review; never in
  `permissions`). A module whose manifest has the block and no `thumbnail`
  export is refused at load (`refused`).
- **Input** (`wire.ThumbnailInput`): `{"file": {"ref": "in:0", "name", "size",
  "mime"}, "ext", "max_width": 320, "max_height": 320, "max_output_bytes":
  4194304, "locale", "settings"}` - no path, no storage, no actor.
- **Output** (`wire.ThumbnailOutput`): `{"image": "<base64 PNG or JPEG>"}`; at
  most 4 MiB and 4096 x 4096 pixels (checked from the header first). An
  error answer, a crash, a timeout, an empty or refused image: the host asks
  the next handler.
- **Host functions in the call**: `file_open` / `file_read` / `file_close` on
  `in:0` (and on assets), `settings_get` (`settings`), `asset_fetch`,
  `engine_available`, `http_request` (`http:<host>`). Every other one answers
  `{"error": {"code": "permission_denied", "message": "a thumbnail call reads the
  file it was given and nothing else"}}`. A call that reached the network
  writes the audit row `app_plugin.thumbnail_sent` (`plugin`, `version`,
  `hosts`, `storage_id`, `path`, `node_id`, `size`).
- **Reasons recorded** on the thumbnail row (`error`, and each entry of
  `attempts`): `app_failed:<app>`, `app_timeout:<app>:<ms>`,
  `app_too_large:<app>:<bytes>`; `no_handler` when every handler of the kind
  is switched off.

## Host functions (guest side: `pkg/pluginkit/host.go`)
`file_open/file_read/file_close` (files:read - except on an `asset:N` ref,
the app's own download, below), `file_create/file_write`
(files:write), `job_progress`, `settings_get` (settings),
`state_get/state_set/state_list` (state; jobs only for set),
`engine_available/engine_run` (`engines:<name>`;
jobs only). Every failure is in band: `{"error": {"code", "message"}}` with
codes `permission_denied | not_found | too_large | timeout | unavailable |
invalid | busy | internal`. Engine arguments are bare tokens: anything with a
path separator, `..`, `@list` or a `file:`/`http:`-style scheme is refused
before the engine is even looked up.

**The office engine (0.50).** `engines:office` - and `engines:libreoffice`,
its name before 0.50, which is the same engine and the same grant - is the
connected ONLYOFFICE Document Server, not a program: filex runs no
LibreOffice anywhere. `engine_run` reads the soffice line an app sent
LibreOffice (`--convert-to <format>[:<filter>[:<options>]] <file>`, the
desktop-session switches ignored, anything else `invalid`), hands each file
to the document server's conversion API and writes the result into the run
directory as `<stem>.<format>`. The document server downloads the input from
filex's own fetch door (`/api/files/onlyoffice/fetch?o=<token>&exp&p=convert&sig`)
for the length of that one conversion. A refusal is a failed run (`exit 1`,
the reason in `stderr_tail`); no document server connected is `unavailable`
and classifies the job as `office_unconfigured`; the document server's own
clock and size limit are `timeout` and `too_large`. The call's `engines` map
carries both names with the same answer, read live. The kit:
[PLUGIN-KIT → The office engine](PLUGIN-KIT.md#the-office-engine).

**`state_list` (v3, permission `state`).** `{key, limit}` → `{items: [{path,
name, key, value}]}`, `path` adapter-qualified, at most 500 (100 when `limit`
is absent or out of range), an empty `key` meaning every key this plugin
keeps. It answers the one question the per-file store could not: *which files
am I keeping this on?* Without it a home screen was empty until somebody
navigated to a document, because a plugin only ever sees the file it was
opened on. Three properties are the whole of it, and each one is deliberate:

- a **deleted** file is not in the answer - the state row is joined back to a
  live node, so a document that was removed stops appearing in the list that
  says it is waiting for something;
- an **un-indexed** file IS in the answer - the row carries the path itself
  (migration **00048**) rather than only its hash, so a document recorded
  seconds after upload is not missing from the very list it belongs in;
- every row is filtered through the **asking person's** permissions. A listing
  is not a way around the ACL: the same call from two people answers two
  different lists.
  - ⚠⚠ …with one exception, and only one: the hourly **wake-up**
    (`tick`). The host starts it on its own clock, so there is no person to
    narrow it by, and running it through an ACL anyway answers *nobody may see
    anything* - which made a scheduled app look out on an empty world every
    hour while the same rows were on a screen in front of somebody. A `tick`
    is told about the app's OWN rows, which are the app's, and nothing else.
  - ⚠ A **public page** call is equally person-less and is **not** the
    exception: that is a stranger who found a link, and they are told nothing.
    The host keys this on the call the host itself began, never on "there is no
    actor".

Outbound (M3, shipped): `users_lookup {q}` → `{users: [{user_id, email, name}]}`
(users:lookup; tenant-scoped, ≤ 20) · `notify_send {title: Text, body: Text,
severity: info|warning|error, meta: {…≤8 small facts}, to_user_id?, target?:
{ref | path, action?, view?}}` → `{id}` raises a `plugin.notice` notification
whose meta carries `plugin`, `plugin_label_<lang>`, `title_<lang>`,
`body_<lang>` - one per language the app wrote (`_en`/`_tr` always, at most 16
more) - and `job` (notify:send). A reader sees the notice in their own
language when the app wrote it in that language, else in English.
⚠ **Whose people (multi-tenant).** A call reads the directory of the person it
speaks for: the person who started it, or on a public page the person who sent
the link. The wake-up and the work it schedules speak for nobody, so on a
multi-tenant instance their `users_lookup` answers no one. `to_user_id` must
name somebody whose tenant reaches the storage the call is about (for a call
about no storage: somebody in the tenant of the person it speaks for); anyone
else is `not_found`, the same answer as an id that does not exist. A
single-tenant instance is unaffected.
**v2:** `to_user_id` addresses ONE person (their bell, their push, their mail
if they enabled it; `404` for an unknown id) instead of the instance feed;
`target` makes the row clickable - `ref` (an input) or `path` (adapter-
qualified, on the job's storage) names the file, and `action`/`view` (this
plugin's, validated) say what to open on it. The stored target is
`{kind: "file", storage, path, open: {plugin, action|view}}`; a client that
receives `open` navigates to the file and dispatches
`plugin:<plugin>/<action>` (or opens the view) - a "please sign" lands in
the signing screen, not on the notifications page. Without `target` the row
stays non-clickable. **v3.1:** a target with NO `ref`/`path` and a `view`
this app places `home` (plus an optional `section`) opens that home PAGE -
a notice about a list, not a file: stored as `{kind: "app", open: {plugin,
view, section?}}`, opened by the web at `{base}app/{plugin}/{view}?section=`
(the desktop app, which has no such page, brings its window forward). A
file-less target naming any other view is refused. · `mail_send {to, subject, body, lang?}` (mail:send;
plain text ≤ 64 KiB, 60 per hour per plugin, filex appends "Sent by the `<app>`
app on filex" in `lang` - the language the app wrote the mail in, when the
server speaks it - else in the language the call runs in, and labels the mail
with it; unavailable until SMTP
is configured and verified) ·
`http_request {method, url, headers, body_b64, timeout_s}` → `{status, headers,
body_b64}` (host must match an `http:<host|*.domain>` grant - a host name, an IP
literal, or `*.` and a name of at least two labels; no port, no `*` alone, no
wildcard over a whole top-level domain; loopback/private/link-local/shared
(100.64.0.0/10)/reserved addresses, and NAT64/6to4 spellings of them, are
refused even when a granted name resolves to one;
8 MiB each way, 30 s, 5 redirects that must stay inside the grant; `Cookie`
never sent, `Set-Cookie` never returned; Extism's own HTTP import is closed so
this is the only network path). 

**`asset_fetch {url, sha256, max_bytes}` → `{ref, size, cached}`** - a file
the app needs, downloaded ONCE by the host and read with the ordinary file
ABI (`file_open` on `ref`, which needs no `files:read`: the file is the
app's own download, not a person's). Built for the signing app's fonts: a
name in Japanese or a box filled in Arabic needs a Noto face of 0.1-10 MB,
and bundling every script's would add tens of megabytes to the module and
to the compile every install pays - fetched, they add nothing until used.

- **Guarded like `http_request`.** `https` only; the host must match one of
  the app's `http:<host>` grants (so the connection is on the install
  review, never hidden); private and loopback addresses refused after DNS;
  redirects only to granted `https` hosts. A refused host is never
  contacted (`permission_denied`).
- **Pinned.** `sha256` (64 hex) is required: the bytes are hashed as they
  stream to a temporary file and renamed into place only when the hash
  matches. A mismatch is `integrity` and nothing is kept, not even a
  partial file, and it is logged as a `warn` in the app's log - a font file
  is a parser's attack surface, and a compromised server must not be able to
  hand an app bytes it did not pin.
- **Bounded.** `max_bytes` 1…32 MiB (`too_large` past it or past the app's
  own figure). 32 MiB because the largest file any app pins today is Noto
  Sans SC Regular (10.5 MB), and its variable release - the one a future
  pin may move to - is 17.8 MB; room for that, not for arbitrary files.
  One app's cache is kept under 256 MiB, least recently used first.
- **Cached.** `<plugins dir>/assets/<app>/<sha256>`, kept across upgrades and
  removed with the app. A second call - any call, any screen - is served
  from disk with no network (`cached: true`). `assets` (with `cache`,
  `spool`, `public`) is a reserved app name for that reason.
- **Detached from the call.** A download runs on its own budget (120 s),
  shared by every call that wants the same file. A call waits for it only
  while its own deadline allows (keeping 3 s to answer) and then gets
  `timeout` ("still downloading"); a later call finds the file on disk. A
  screen's 30 s would otherwise cut a slow download, and the next keystroke
  would start it again from zero, forever.
- **Offline is an answer, not a storm.** A failed download is `unavailable`
  (or `integrity`); the same asset is not tried again for a minute - the
  calls in between get the same answer at once - and the outage is logged
  ONCE in the app's log, not once per call, until the file arrives
  (measured 2026-09-21: an app asking at every pause in typing wrote
  fifteen identical lines for three pauses before this). What the app does
  with it is the app's call; the signing app tells the person which
  characters will not print.
- The app log says `asset fetched: <url> (<n> bytes, sha256 verified)` for
  every download - the one moment an installation talks to the network on
  an app's behalf.

Signing (M3, shipped; permission `sign`, needs `FILEX_SECRET_KEY`): the host
runs a CA per tenant - ECDSA P-256, generated on first use, or one the
operator imported (v3, below); sealed at rest either way -
`host_sign_info {}` → `{available, reason?, ca_cert_pem, ca_certs_pem,
algorithm}` ·
`cert_issue {common_name, email?, days?}` (jobs only; default and ceiling
3650 days) →
`{key_ref, cert_pem, chain_pem, not_after}`, a leaf for documents only - its
extendedKeyUsage is id-kp-documentSigning (1.3.6.1.5.5.7.3.36, RFC 9336) and
Adobe's Authentic Documents Trust (1.2.840.113583.1.1.5, which Acrobat accepts),
and nothing else; OU = the plugin's name ·
`cert_issue {purpose: "platform"}` → the same shape for the **platform seal**:
one key per tenant and app, kept by the host (never destroyed - `key_destroy`
answers `invalid`), CN *filex document seal*, O *filex*, OU the app's name,
issued by the live authority and re-issued by the next one after a rotation;
it signs through `host_sign` from jobs only (a screen is refused). It is how an
app closes a document on the installation's behalf - the signing app seals
every completed request with it ·
`host_sign {key_ref, hash: "sha256", digest_b64}` → `{signature_b64}` (DER
ECDSA over the 32-byte digest; keys are the plugin's own and the caller's
tenant's, 20 calls/min) · `key_destroy {key_ref}` (the certificate stays; the
key is gone). The guest SDK wraps this as `pluginkit.HostSigner`
(crypto.Signer) so pdfsign/pkcs7 take it unchanged.

⚠ **`ca_certs_pem`, not `ca_cert_pem`, is what a verifier's root pool is
built from** (v3). It is every authority this tenant has ever signed with,
retired ones included, as one PEM bundle; `ca_cert_pem` is only the live one.
A verify screen that trusts the live authority alone reports every signature
made before the last rotation as untrusted - which is the opposite of what
retiring an authority is for.

⚠ **No `emailProtection` (0.50.0).** Leaves and seals issued before 0.50.0 also
carried emailProtection - with the name and the address the app chose, that
made them S/MIME certificates under the tenant's authority. They no longer do,
and a seal that still does is replaced the next time it is asked for. A
signature made with an older certificate still verifies: the chain is the same,
and a verifier that requires document signing (filex-sign's does) finds it on
both.

⚠ **Leaf lifetime.** Leaves are issued for ten years by default, because a
verifier asks "is this certificate valid *now*": a 30-day leaf meant every
signature started reading "certificate expired" on its 31st day. The private
key is destroyed seconds after the signature either way, so a long leaf
carries no key risk.

Admin routes:

| Route | |
|---|---|
| `GET /api/admin/app-plugins/signing/ca.pem` | the live certificate readers import |
| `GET …/signing/cas` | `{authorities: [{id, subject, issuer, fingerprint, imported, not_before, not_after, created_at, retired_at?, live}]}` - every authority, live and retired |
| `POST …/signing/ca/rotate` | retires the current authority and starts a fresh one |
| `POST …/signing/ca/import` | take over signing with an authority the operator already has |

**Importing an authority (v3).** `POST …/signing/ca/import`, either multipart
with `cert` and `key` or JSON `{cert_pem, key_pem}`. `cert` may be a bundle
(the signing certificate first, its issuers after it); the key is PKCS#8,
PKCS#1 or SEC1 PEM and **not encrypted** - a `.p12`/`.pfx` is converted first
(`openssl pkcs12 -in ca.p12 -nodes -out ca.pem`), because the encrypted
containers in the wild are more varied than any one library reads and a
half-working import is worse than a clear instruction. The key is sealed at
rest with the instance key exactly like a generated one, and an RSA authority
is accepted (the leaves it issues are still ECDSA P-256, because that is what
the host signs with). Answers `200 {imported, subject, authorities,
warnings}`; `400 {"error": "ca_invalid", "message"}` otherwise. Audited as
`app_plugin.signing_ca_import`. `warnings` (0.50.0) is a list of
`{code, message}`, empty when there is nothing to say; it never refuses the
import. `ca_not_limited_to_document_signing`: the authority's certificate has
no extendedKeyUsage, or names purposes beside document signing - so whatever
trusts it for email, websites or code now trusts a key this filex holds, and
filex issues document-signing certificates under it in any name an app with
`sign` asks for. An intermediate made for filex alone (extendedKeyUsage
1.3.6.1.5.5.7.3.36 and 1.2.840.113583.1.1.5, name constraints for your own
domains) is imported without one. The same sentence goes to the log.

⚠ **Authorities are never deleted.** Importing or rotating *retires* the
current one. A signature made two authorities ago must still verify, so the
retired certificates stay - in the listing, and in the `ca_certs_pem` bundle
every guest is handed.

### File locks (v2, permission `files:lock`)

**v0.43:** `ttl_days: -1` is a lock **until lifted** (`until: null`) - by the
app, or by an administrator on the app's page (audited). And `file_lock` on
one of the job's **own outputs** (`ref` from `file_create`) is *promised*:
answered at once with `promised: true`, taken when `runJob` commits that output
at its final path, never when the job fails - how an app locks the file it has
just produced (the signing app's *lock the signed file when every signature is
in*).

`file_lock {ref | path, ttl_days (0 = 30, ≤ 365), reason | reason_key + reason_args}` → `{until}` and
`file_unlock {ref | path}` → `{was_locked}`, jobs only, FILES only (a folder
is refused as `invalid`). ⚠ A `path` must name one of the job's inputs or a
file this app keeps state on - the same rule for a notice's `target.path` and
a page link's document - and anything else is `permission_denied` with one
sentence whether or not the file exists or is locked; an app may always lift
its OWN lock by path. The person the job runs for must also hold **editor** on
the file (this app's own lock is waived); a job with nobody behind it (the
wake-up's) may lock only its own inputs. A lock freezes one
file for EVERYONE - owner and administrators included - until the plugin
lifts it or `until` passes: every caller's effective level on that path is
capped at viewer (uploads over it, saves, versions restore, sharing edits
all refuse), and rename/move/delete of the file OR of any folder above it
answer `423 {"error": "locked", "plugin", "plugin_label?", "path", "reason?", "until?"}`,
as does an upload (browser or staged) that would overwrite the locked file
(uploads of other names INTO such a folder stay open - the lock is the file,
not the folder). The locking plugin's own jobs still write - and ONLY they: an
output of another plugin landing on the file is refused. It holds at **every** door, not only the explorer's: the document editor's save (a document opened before the freeze and saved after it is refused), WebDAV (423 Locked), SFTP, FTPS and NFS (their permission error - also for a session that was already open when the freeze was taken), the S3 gateway (AccessDenied), the AI/MCP write tools, archive extraction (a member that would land on the file is skipped and counted as `locked`), trash and version restores onto the path, and another app's output. One check does it for all of them (`backend/internal/writegate`), the same one that refuses filex's own folder names. One lock per file; a
second plugin gets `busy`; lifting another plugin's lock is
`permission_denied`. Removing the plugin removes its locks; expired locks
are swept hourly. Listings show `locked` + `lock`. Admin override:
`GET /api/admin/app-plugins/locks[?storage_id=]` → `{locks: [{storage_id,
path, plugin, reason, until, created_by, created_at}]}` and
`DELETE /api/admin/app-plugins/locks {storage_id, path}` (audited as
`app_plugin.unlock`; `404` when nothing is locked there).

**Who the lock names (v0.43).** Both payloads carry `plugin`, the app's
manifest **name** - that is what addresses it - and, beside it,
`plugin_label`, the app's own label in every language its manifest wrote it
in (`{"en": "e-Signature", "tr": "e-İmza"}`). Show the label; fall back to
the name only when `plugin_label` is absent, which is what a server that no
longer has the app installed (or has no app runtime at all) sends. filex's
own details panel used to print the name, so a person reading a frozen file
was told "sign locked this file" while every other screen - the Apps list,
the install review, the app's page - called it "e-Signature".

**Lock reasons in the reader's language (v0.43).** `reason` is one string in
whatever language the job ran in, so a German administrator read the Turkish
requester's words. `reason_key` names one of the manifest's `messages`
(`{"messages": {"lock.collecting": {"en": "signatures are being collected",
"tr": "imzalar toplanıyor", …}}}`, every declared language required, `{name}`
placeholders filled from `reason_args`, ≤ 8); the lock keeps the key, and
every place that shows the reason - the admin lock list, the listing's `lock`,
a refused write's `423` - carries `reason_text` in every language plus the
English `reason`. `pluginkit.FileLockMessage(ref, ttl, key, args)`. A key the
manifest does not declare is refused; key and arguments must fit 190 bytes.

### `GET /api/files/plugins/users?plugin=<name>&q=`
The people-picker's directory search: `{users: [{user_id, email, name}]}`
through
the caller's tenant scope; `403 permission_denied` unless the named plugin is
running and holds `users:lookup`, `404` for an unknown plugin.

## Configuration
`FILEX_APP_PLUGINS_DISABLED=1` turns the runtime off (demo mode flips the
default to off), which also means no app is ever woken (see *The scheduled
wake-up*); demo mode stops the schedule even where the runtime is on. `FILEX_APP_PLUGIN_MAX_INPUT_MB` (256), `_MAX_OUTPUT_MB` (512),
`_MAX_WASM_MB` (64). `FILEX_APP_PLUGIN_UPDATE_CHECK=0` stops the daily update
check (the admin's "Check now" still runs; a demo never checks). Signatures use
the same `FILEX_PLUGIN_TRUSTED_KEYS` as storage plugins. Modules live under `<data-dir>/app-plugins/<name>/`, the
compilation cache and the per-call spool next to them.

## An app's public page IS a share (v3)

A plugin running an ACTION JOB may open a link for an outside participant
(`public_pages` permission; manifest `public_pages[]` declares each page's
`pin` policy `optional|required|none` and TTL ceiling). Until v3 that link was
the plugin's own construction at `/p/<token>`, with its own token, PIN,
expiry, visit ceiling and revoke. It is now a **real share**: `share_create`
opens a row in the same table as every other public link, so the
administrator sees and revokes a signature request in **Shares** like a
download, and the security properties are the ones that surface already has -
one revoke list, one expiry policy, one PIN implementation to get right, one
visit counter, one set of audit rows.

```
share_create {
  ref | path,          // the document the outsider sees; empty = the job's first input
                       // ref may name one of THIS JOB'S OUTPUTS - see below
  page_id,             // the manifest page this share renders
  subject,             // the line the public shell puts in its header
  pin: "auto" | "<4-12>" | "",
  ttl_days, max_visits,
  state: {…},          // the plugin's durable record for this link (≤ 64 KiB)
  files: [{ref, name}],// extra files exposed to the visitor (copies)
  purpose: {label, revoke?, section?}  // what THIS link is, in a list of links (v3.1)
} -> {token, url, pin?, expires_at}
```

**What a link IS, in a list of links (v3.1).** A `public_pages[]` entry may
declare `"purpose": {"label": {…}, "revoke": {…}, "section": "<home section>"}`,
and `share_create` may carry one for the link it opens - the link's own wins
over its page's, and it is the ONLY way a page-less link (a plain share of a
file, e.g. a finished document handed to everybody) can say what it is. Both
are checked the same way at the door (`wasmplugin.checkPurpose`) and stored on
the row (`shares.purpose_json`, migration 00052).
My shares (`GET /api/shares`) and the admin's Shares then carry, on every
link of that page, `app: {plugin, page, label, revoke, view, section}` - the
app's own name for the link, what revoking it does (said before the revoke),
and the app's `home` view and section that shows it. `label` is required and,
like `revoke`, speaks every language the app declares.

**Visits are not downloads (v3.1, migration 00052).** Opening an app page
counts on the share's `visit_count` - what `max_visits` caps and what the app
reads as `page.visits`. `download_count` counts only an exposed file TAKEN:
`GET …/file/<ref>?download=1` (the page's Download button, the no-JS page's
link), once per download (a `Range` that does not start at 0 is the same
download). The page's own viewer fetches the file without `download=1` and is
not counted.

- `share_revoke {token}` · `share_state {token?, state?}` - read or replace
  the plugin's record for one link; inside a public-page call `token` may be
  empty. Jobs only for `share_create`. A read answers
  `{state, page: {page, subject, visits, expires_at, revoked}}`; the SDK's
  `ShareInfo(token)` returns the `page` half. `revoked` means the link no
  longer opens (it ran out, hit its visit ceiling, or somebody revoked it -
  a revoke sets `expires_at` to that moment), and a link DELETED outright
  answers `not_found` (`pluginkit.IsNotFound`). ⚠ Inside a page call the
  answer is always about the link being visited, whatever `token` says.
- **An app learns that a person ended one of its links by asking.** filex
  sends no event when a link is revoked or deleted from **My shares** or
  **Shares**; it brings the app's hourly wake-up forward to a few seconds
  from now (only for an app holding `schedule`), and the app's `tick` reads
  its links' facts as above. A link that stopped EARLIER than the
  `expires_at` share_create handed back was ended by somebody. The signing
  app closes the request that link belonged to.
- `share_max_ttl_days` on every call input is the installation's share
  ceiling; `share_create` clamps to it (and to the page's `max_ttl_days` and
  365). Offer no longer a life than it allows.
- ⚠ **`public_page_create` / `_revoke` / `_state` are the older spelling of
  exactly these three** and are still bound, so a v2 module keeps working.
  New code uses the `share_*` names; the guest SDK's `PublicPage*` wrappers
  are marked deprecated and call the same imports.
- Files are COPIED out of the call for the visitor (≤ 16 files / 64 MiB each
  / 128 MiB total, under `<data-dir>/app-plugins/public/<share id>/`); the PIN
  is returned to the app once and is never stored in clear - like every share
  PIN it is kept as a bcrypt hash plus a copy sealed under `FILEX_SECRET_KEY`,
  which the link's creator or an administrator can read back from **My
  shares** / **Shares** (`GET /api/shares/{id}/pin`, audited).
- The share row carries `plugin_id`, `page_id`, `subject`, `state_json` and
  `files_json` (migration **00046**); a share without them is an ordinary
  download link and behaves exactly as before. `app_plugin_pages`, the table
  v2 used, is **dropped** - see below.
- The link's document is the job's first input unless `ref`/`path` names
  another, and `state_get`/`state_set` on it work from public-page calls too,
  so a plugin can keep per-document facts beside the per-link record.
- ⚠ A `ref` that is neither an input of this call nor an output of it is
  `not_found`, by name. It does **not** fall back to the first input: a plugin
  that believes in a ref the host does not has a bug, and quietly delivering a
  different file to a stranger is how it stays one.

### Sharing the file the job is still writing

The document an app most wants to share is usually the one it has just made -
the signed PDF at the end of a signature round. That file has no catalogue
node while `action_run` is running: the host commits outputs to the storage
*after* the plugin returns, and a share points at a node.

So `share_create` accepts a `ref` naming one of **this job's own outputs**
(`out:N`, or an engine artefact `eng:N` - anything the call may name in
`ActionRunOutput.Outputs`). Everything about the link is settled at the ask
and returned as usual, so the answer can go straight into the mail being
composed:

```go
out, _ := pluginkit.WriteOutput("contract-signed.pdf", signed)
link, err := pluginkit.ShareCreate(pluginkit.PageCreate{
    Ref: out.Ref, Subject: "contract.pdf", PIN: "auto", TTLDays: 30,
})
// link.URL / link.Token / link.PIN / link.ExpiresAt are final here.
return &wire.ActionRunOutput{OK: true, Outputs: []wire.OutputRef{out}, …}, nil
```

- The **row** is written when that output is committed, pointing at the node
  the bytes landed on. From then it is an ordinary share: in **Shares**, under
  the administrator's revoke, behind the same PIN gate, spending the same
  visit counter, naming the file the recipient actually gets. The audit row
  is written then too, and carries `output` so it is clear why a link exists
  for a file nobody uploaded.
- It holds for **both output modes**: `sibling` (a new file beside the
  original - the share points at the new node, under its final, uniquified
  name) and `version` (a new version of the original - the share points at
  that same node, serving the new bytes).
- ⚠ **A promise the job does not keep leaves nothing behind.** If the job
  fails, or the plugin never names the output in `outputs`, no row is ever
  written and the token answers nothing. That is the reason the row waits
  instead of being written early and bound later: in `version` mode the target
  node already exists, so an early row would be a *live* link handing the
  visitor the original, unsigned document the moment the job fell over.
- ⚠ An action whose output mode is `none` keeps nothing, so a link against
  one of its outputs is refused **at the ask** rather than silently never
  opening.
- A page-less link exposes no copies, so `files: [{ref: <output>}]` - a single
  copy and nothing else - is read as "this is the document" and honoured as
  `ref`. Two copies, or one naming something this call does not know, are
  refused.

⚠ **No v2 page could be carried over, and none can be.** `app_plugin_pages`
stored only `sha256(token)` while a share stores the token itself, so an
existing `/p/<token>` cannot be turned into a `/s/<token>` without the token
nobody kept. The table shipped in no release, so this reaches development
instances only.

### The visitor's routes

They are [the public surface](BACKEND.md#the-public-surface) - the same JSON a
download share and a file request answer, because a stranger meets one shell
whichever kind of link they were sent. In short, and with the shapes in
BACKEND.md:

| Route | |
|---|---|
| `GET /api/public/branding` | who this instance says it is: name, logo, accent, footer, theme, locales. Unauthenticated and **revalidated** - a strong ETag over the body with `public, no-cache`, so a language pack installed a moment ago is offered on the next page load and an unchanged answer costs a `304` - whitelabel means a signature request from a renamed instance does not say "filex" |
| `GET /api/public/s/{token}` | the link's state. `kind: "app"` carries `app: {plugin, page, title, files: [{ref, name, size, mime}]}`; `files` only once unlocked |
| `POST …/s/{token}/pin` | `{"pin"}` → the same object plus the unlock cookie · `401 pin_wrong` · `429 locked` |
| `POST …/s/{token}/event` | `{state, event, action_id, data}` → `{surface}`, or `202 {accepted, job_id}` when the surface asks for a job. An empty body means `{"event": "open"}` and counts a visit. A job the submit-time gate turns away answers `403`/`409`/`413` (below) |
| `GET …/s/{token}/file/{ref}` | one exposed copy (`pub:N`), Range-capable, `inline`, behind the same gate. Always carries a `Content-Type` (the app's, else the one the name implies, else `application/octet-stream`). Every kind a browser does not simply show - HTML, SVG, XML, anything unknown - also carries `Content-Security-Policy: sandbox; default-src 'none'…`, so it can never run as a page of this filex; a PDF, a picture (not SVG), sound, video and plain text are shown as themselves |

- ⚠ `expired` and `revoked` are different words for different things.
  `expired` is the clock - and an administrator's **Revoke** moves the expiry
  to the moment it happened, so the visitor's screen lands there too: the
  link is stopped by the clock, so the clock is the word a stranger reads.
  (The listings do tell the two apart. A revoke also writes `revoked_at` -
  migration 00053 - which is what **My shares** and **Shares** read to say
  *Revoked*; a link revoked before v0.43.0 has no such record and still reads
  as expired there.)
  `revoked` is dead for a reason that is *not* the clock: the visit ceiling is
  spent, the file is gone, or the app that answers the link was stopped or
  removed. `locked` is neither - the PIN gate shut after five wrong answers
  and lifts by itself in ten minutes.
- The plugin's `page_event` export answers the surface; `data.page` carries
  `{subject, state, visits, visitor_ip}` and `context.inputs` lists the link's
  document (an unreadable anchor) plus the exposed copies `pub:N`.
- A surface carrying `job` is queued on that document **as the link's
  creator** (their ACL, their storage), with `params.page_token_hash` added;
  the visitor gets `202 {"accepted": true, "job_id"}` and never sees the ops
  row.
- ⚠⚠ **That submit passes the same gate an authenticated one does.** The
  action is resolved through the *registry*, not through your manifest, so an
  action the administrator **disabled** or reserved to **administrators**, and
  any action of a **stopped** plugin, is refused at the door - a page cannot
  reach past the panel by naming an id. The creator's ACL on the anchor is
  **re-read at submit** (viewer, editor once the job writes, higher if the
  action declares `min_role`), the encrypted-folder refusal applies, a writing
  mode on a read-only storage is refused, and `params` are capped at the same
  **64 KiB** the authenticated door uses (`413`).
- ⭐ `is_admin` for that resolution is the **creator's**, never the visitor's.
  The job spends the creator's rights, so a link an administrator minted into
  an admin-only action keeps working, and a link minted by somebody else does
  not become one by being forwarded to a stranger.
- ⚠ **A link does not outlive its creator's access, or their account.** Two
  ways it ends, both by design, because the job would otherwise run under
  rights its owner no longer has:
  - the person who opened the link **loses their grant** on the document (they
    change teams, the folder's sharing is tidied, the account is demoted) -
    the submit is refused with `403 no_access`, in one sentence and with no
    detail about the instance: the link can no longer be used, ask whoever
    sent it for a new one. (`409 link_unavailable` when the action itself is
    gone; `permission_denied` when it is reserved.) A plugin should treat any
    of these as "this envelope is finished", not as a transient error to retry.
  - their **account is switched off or deleted** - and this one stops the
    link at the DOOR, not at the submit: `GET …/s/{token}` reports
    `revoked: true` and every `…/event` answers **410**, so the visitor meets
    the ordinary dead-link screen instead of filling in a document that was
    never going to be accepted. ⭐ It also means `page_event` does not run, so
    a dead link cannot record a signature it will never finalise.
    ⚠ Disabling is a **pause, not a demolition**: nothing about the share is
    rewritten, and re-enabling the account makes every outstanding link work
    again.
    ⚠ A link that names **nobody** is not affected: a page your `tick`
    minted runs with no actor, so there is no account to have left. It serves
    its document as before - it simply cannot queue a job, which was already
    true (a job needs an ACL to run under).
- ⚠ A public surface may not carry `open` (below): there is no explorer behind
  a share link to send anybody into.
- The PIN gate is one implementation for every public link now. Five wrong
  answers shut it for ten minutes, counted on the share row so it survives a
  restart and holds across two instances behind one address, and the *right*
  PIN during a lock is refused too - a lock the correct answer lifts is no
  lock at all. Before v3 this existed only on app pages; a PIN on a `/s/` link
  could be walked through at the speed of HTTP.
- Links revoked or expired for more than 7 days are swept hourly (the exposed
  copies with them).

### Retired: `/p/*` and `/api/p/*`

`GET /p/{token}` **301**s to `/s/{token}`, and each `/api/p/…` leaf 301s to
the same leaf under `/api/public/s/{token}` (`/view` to the bare route - the
opening surface is an ordinary `open` event now). A link already printed,
mailed or pasted somewhere keeps working; nothing new should be built against
the old prefix.

### `GET /api/admin/app-plugins/shares`

The links apps opened: `?plugin=<name>`, `?active=true`, `?limit=`,
`?offset=`. Same rows, envelope and tenant filter as `GET /api/admin/shares`,
which carries `plugin_name` and `page_id` on every row so the Shares table can
show a plugin/page column without a lookup per row. An app that is not
installed answers an empty page rather than a 404, so a panel polling one app
keeps working through an uninstall.

## Surface component catalogue (v1)
`text`, `form` (fields = storage.Field), `steps`, `list`, `progress`,
`people-picker`, `pin-input`, `file-chooser`, `pdf-fields`, `signature-pad`,
`preview`, `divider`, `row`. The host validates every surface against this
catalogue before it reaches a browser and drops unknown node types (and caps
500 nodes / depth 8 / 8 footer actions). `pdf-fields` and `signature-pad`
arrive with the sign track (M3); everything else is M2.

### `pdf-fields`: define the boxes, then place them (v3 §3.3)

The node has four modes, and the first two are the author's two **jobs**:

| `mode` | What is on the screen | What comes back |
|---|---|---|
| `define` | the boxes as cards - name, whose it is, required, a text box's rule and face. **No document.** | the whole `fields[]`; a box with no place yet carries `"placed": false` |
| `place` | the document, and the boxes that still need a place. Choose one, tap the page. Nothing to type into. | the same list, with `placed` gone from whatever was put down |
| `edit` | both at once - the one-screen form, for a plugin that wants it | the whole `fields[]` |
| `fill` | the signer's own boxes | `{fields: [{id, value, …}]}` |

⚠⚠ Why they are separate: naming a box and finding a place for it are two
different decisions, and a screen that asks both at once (a palette of types,
a rectangle under the pointer, a strip of properties for whichever box is
selected) is the one the owner sent back twice. A plugin that splits them
gets a step that asks *what is wanted of whom* and a step that asks *where*.

A host that receives `"placed": false` must not draw that box on a page - it
is not anywhere yet - and a plugin must refuse to send a request while any of
them is still waiting.

**v3.1, the owner's second pass (2026-09-21):**

- A `signature`/`initials` box carries `style: "typed"` when it is a name
  typed in `font` rather than a drawing (absent = drawn). The editor asks
  *how it is signed* (Drawn / Typed) and offers a face only for a typed one;
  a drawn signature's face was a control that changed nothing.
- The node may offer `stamp_lines: [{id, label: Text, examples?: {<signer
  id>|"*": Text}, default?: bool}]` - what the plugin can print under a
  signature. Each signature/initials box then carries its own `lines` (ids,
  in catalogue order; absent = the `default` ones, `[]` = nothing), and
  the card previews them in the plugin's own words (`examples`). ⚠ The
  values are the plugin's to fill from its signing record - nothing typed on
  this screen reaches the paper.
- A date / text / checkbox box's owner is labelled **Filled by** (tr
  **Dolduran**), a signature's **Signer**; "Anyone" is shown chosen on a box
  that has no assignee.
- In `place` the hint and the selected box's controls share ONE strip of
  constant height, so choosing a box no longer re-fits (and redraws) the
  document, and a new surface carrying the same `src` no longer reloads it -
  the two causes of the flicker, and of a dragged box jumping to the
  page's edge, that the owner reported.

### A surface may send the person to a file (v3)

```json
{"open": {"path": "docs://reports/nda.pdf", "action": "fill"}}
```

The client goes to that file and starts the named screen on it - `action` or
`view`, never both; naming neither just opens the file. This is what lets a
home screen be a list of *documents* rather than a list of names: a row is
clicked and the person lands where the work is, instead of reading a label
and then hunting for the file themselves.

Two checks, in two places, because they know different things. The **host**
verifies that the screen belongs to the plugin that answered (a surface
naming somebody else's action is a plugin error). The **handler** checks the
path against the ASKING person's permissions, because that is where a
caller's rights are known - a screen must never become a way to look at a
file the person could not have opened themselves. A path that does not pass
drops the link rather than failing the screen: the person still gets their
surface, just without a door they were never allowed through.

⚠ A public page's surface may not carry `open`; an anonymous visitor has no
explorer to be sent into, so the host strips it.

### Node props (M2, fields revised in v3)
Every node may carry `id`; a node with `id` that holds a value contributes
`data.values[id]` to the next event. `Text` = `{en, tr}` map or a plain string.
- `text` - `{text: Text, tone?: "muted"|"danger"|"info", heading?: bool}`.
- `divider` - no props. `row` - `children` only (drawn side by side).
- `form` - `{fields: Field[], values?: {key: any}}`. Field = the storage
  descriptor field (`key, type, label, help, required, secret, default,
  placeholder, options, min, max`, plus the v3 three below; `label`/`help`
  may also be Text). `type` is `string | password | int | bool | select |
  text | date`. Edits post
  `event: "change"` (debounced 300 ms) with `data.values`; `submit` carries
  the current values too. `errors[key]` on the surface marks a field invalid.
- `steps` - `{items: [{id, label: Text, state: "done"|"active"|"todo"}]}`.
- `list` - `{columns: [{key, label: Text, width?, sortable?, align?,
  format?}], rows: [{id, cells: {key: Text}, actions?: [{id, label: Text,
  danger?}], sort?: {key: string|number}}], empty?: Text}`. A row action posts
  `event: "action"`, `action_id`, `data.row_id`. **v0.43:** `format: "date"`
  (cells `YYYY-MM-DD`) or `"datetime"` (RFC 3339) hands the host the machine's
  value; it is printed the way the explorer prints dates (the reader's
  language and clock; a calendar day is never moved by a time zone) and the
  column sorts by the value. Anything else in such a cell (a dash) is shown
  as sent. An older host shows the value itself.
  ⚠ Drawn by filex's ONE table - the explorer's own (v0.43) - so a person can
  resize, sort, hide and move the columns, and the arrangement is remembered on
  their account per app and node (`app.<plugin>.<node id>`; give the node an
  `id` if two lists on different screens should not share one). The fields
  after `label`/`cells` are optional and additive; a plugin written against the
  first contract draws the same rows:
  - `width` - opening width in px, clamped to 60-900 (what a person could drag
    it to).
  - `sortable` - default **true**: the node carries every row, so sorting what
    is on screen is sorting the list. Say `false` for a column whose order is
    meaningful as sent.
  - `align` - `left` (default), `right` (numbers) or `center`.
  - `rows[].sort` - the raw value to sort a column by where the cell is
    formatted for people: `{"size": 1048576}` beside `"1 MB"`, an ISO
    timestamp beside "3 days ago". Without it the cell's text sorts in the
    viewer's collation, digits read as numbers.
  The first column is the lead: never hidden or moved, and frozen on the
  leading edge while the rest scroll under it. Row actions are ONE labelled
  Actions control on the trailing edge, frozen the same way; a row with no
  actions draws none. ⚠ A column is frozen only while it leaves the columns
  sliding beneath it room to be read - in a pane too narrow for that (an
  app's list in the details panel is ~265px) nothing is pinned and the row
  scrolls as one piece, and the lead gives way to its own minimum rather than
  taking the whole pane. Nothing is ever hidden for want of room: a table
  narrower than its columns scrolls. `plugintest` flags an out-of-range
  width, an unknown `align` and a `sort` key with no column.
- `progress` - `{value: 0..100 | null, label?: Text}` (null = indeterminate).
- `people-picker` - `{id, value: [{email, user_id?, name?}], multi?: bool,
  allow_external?: bool}`. Internal users are searched through
  `GET /api/files/plugins/users?plugin=<name>&q=` (answers only when the
  plugin holds `users:lookup`; 403 otherwise → the picker offers free email
  entry only). Value goes into `data.values[id]`.
- `pin-input` - `{id, length?: 4..8}` → `data.values[id]` string.
- `file-chooser` - `{id, kind: "file"|"dir", value?: "adapter://rel"}` → the
  explorer's own destination picker; `data.values[id]` = adapter-qualified path.
- `preview` - `{path: "adapter://rel"}` - the explorer's existing preview of
  a storage file (thumbnail/viewer); call-scoped refs are not previewable.

### Theme tokens, node by node

An app never styles its screen: it describes it, and filex draws every node
with its own components. So whatever paints filex paints the app - the
built-in palettes, an operator's own theme (its colours, corner radius and
face), light and dark, right to left - with nothing for the app to do. An app
cannot opt out and cannot bring colours of its own; a signer's `color` in
`pdf-fields` is the one exception, below.

The table is what the host guarantees. `web/tests/quality/surfaceTokens.test.ts`
fails the build when a node's CSS carries a fixed colour, a fixed corner or a
fixed face, and it finds a NEW node's rules by itself (every CSS block a
plugin component uses that no other screen does).

| Node | Reads | Fixed on purpose |
|---|---|---|
| `text` | `--fe-text` (inherited); `--fe-text-muted`, `--fe-danger`, `--fe-primary` for the three tones | - |
| `divider` | `--fe-border` | - |
| `form` | filex's own field components, the same as the storage and settings forms: `--fe-bg`, `--fe-border-strong`, `--fe-primary` (focus, a chosen button), `--fe-bg-selected`, `--fe-danger`, `--fe-radius` | - |
| `steps` | `--fe-text`, `--fe-text-muted`, `--fe-border-strong`; the current step's disc is `--fe-primary` with `--fe-text-on-primary` on it, a finished step's tick `--fe-primary` | - |
| `list` | the explorer's one table (DataTable), so every token it reads | - |
| `progress` | track `--fe-border`, bar `--fe-primary`, pill corners | - |
| `people-picker` | chips `--fe-bg-elev` + `--fe-border-strong`; the avatar `--fe-primary` with `--fe-text-on-primary` on it | - |
| `pin-input` | the form input's tokens; `--fe-danger` when refused | - |
| `file-chooser` | `--fe-bg`, `--fe-border-strong`, `--fe-radius`, `--fe-text-muted` while empty | - |
| `preview` | `--fe-bg-elev`, `--fe-radius`, `--fe-text-muted` | the file itself |
| `pdf-fields` | the chrome: `--fe-bg`, `--fe-bg-elev`, `--fe-border*`, `--fe-primary` (the selected box), `--fe-radius*`, `--fe-shadow-sm`, `--fe-text*` | **Document space**: the page is white and the ink on it dark in every theme, because it is the PDF. **Signer colours** (`SIGNER_PALETTE`, or the plugin's own `signers[].color`) are identity colours, like a file type's: the same in every theme, so a signer is one colour on the page and on the card. The number on a card takes its ink from that colour (`inkOn`: white or near-black, whichever reads), never from `--fe-text-on-primary`, which is dark in dark mode |
| `signature-pad` | the frame `--fe-border-strong` (`--fe-danger` when invalid), `--fe-radius`; the hint `--fe-text-muted`; "Signed" `--fe-ok`; the mode and face buttons are filex's buttons | **Paper**: the pad is white and the ink `#111111` in every theme - see below |
| sections, footer | tabs `--fe-text`, `--fe-text-muted`, `--fe-primary`; the footer is filex's buttons | - |

**Why the pad stays white in dark mode.** A signature is a picture bound for a
PDF, and the pad exports exactly what it shows. A dark pad would need light
ink on the screen and dark ink in the file - two renderings of one gesture -
and the other two ways in could not follow at all: an uploaded picture of a
signature is usually dark on white, and a typed one is rendered into the same
PNG. Showing the paper keeps what the signer sees identical to what is
stamped, and it sits beside the PDF page, which is white for the same reason.
Nothing on it comes from the theme, so it needs no redrawing when the theme
changes; its frame is CSS and turns with the rest.

**Light and dark while the screen is open.** An app's page in the admin panel,
its full-page view and its public page turn with the window: the panel hands
the page the mode as it is painted (`web/src/lib/theme.ts` → `liveTheme`), and
the public page follows the operating system (`composables/useSystemDark.ts`)
unless its host forced a mode.

**Not themed, for anyone.** Font sizes, spacing and control heights are
metrics, not a palette (`packages/core/src/lib/themes.ts`), so they are the
same on an app's screen as on filex's own. The compact density is a setting
of the file list, so it tightens the explorer's rows only, as it does for
filex's own tables.

### Fields, and what a step may ask (v3)

Three rules the renderer enforces, so no plugin can break them. They come
from a wizard step that showed eight fields, four buttons, a dropdown whose
options nobody could read without clicking, an "advanced" block hiding the
rest - and two defaults that contradicted each other ("signed document goes:
a new version of this file" above a box asking for the new file's name).

1. **No dropdowns anywhere in a surface.** A `select` renders as a row of
   choice buttons - one selectable, or several with `multi: true`. Every
   option is readable without a click, which is the entire reason. A `select`
   with no options cannot render at all. `multi` means nothing on any other
   type; a `select`'s value stays a string (single) or a list of strings
   (multi).
2. **No hidden sections.** `Field.Advanced` is **gone** from the contract and
   ignored where an older manifest still carries it. If a field matters it is
   on the step; if it does not, it is not in the manifest.
3. **One step asks one thing** - at most one primary button, plus Back. The
   `steps` node is the spine of a multi-step surface.

**Texts in every language (v3.1).** A manifest setting's `label`, `help` and
`placeholder`, and an option's `label`, may be a string or a
`{"en": …, "tr": …}` map (`wire.Field.I18n`); the admin panel reads the map
in the reader's language. The wake-up line (`tick`'s `note`) is stored in
every language the app gave, the host's tally beside it in the same one.

```go
type Field struct {
    …
    ShowWhen     *Condition `json:"show_when,omitempty"`
    RequiredWhen *Condition `json:"required_when,omitempty"`
}
type Condition struct {
    Key    string   `json:"key"`     // another field in the same form
    Equals []string `json:"equals"`
}
```

`show_when` hides a field until another field in the same form holds one of
the given values; `required_when` makes it required only then. So "the name
of the new file" appears only when the output is a new file, and is required
once it does. ⚠ Both are checked on the client **and re-checked by the host
at submit**: a value belonging to a hidden field is **dropped before the job
runs**, so it cannot arrive as a surprise, and a `required_when` field left
empty refuses the job. A field that is `show_when`-hidden and unconditionally
`required` cannot be filled - use `required_when`.

- `bool` renders as a two-button Yes / No when it decides something the
  person must read, and stays a plain toggle for an on/off switch. **`style`
  says which**: `"choice"` for the two buttons, `"switch"` for the toggle.
  Left empty the client guesses - `options` or `required` mean a decision,
  anything else a switch - so say it whenever the answer matters ("should
  this installation add time stamps?" is a question, not a tickbox). ⚠ A
  `select` ignores `style`: it is choice buttons either way, because rule 1
  leaves it no dropdown to fall back to.
- `date` is a field **type**. The `date` rule on text fields is gone (below).

⚠ These rules hold on **every** screen that draws a plugin's fields - the
surface a plugin opens AND the admin's own settings screen for the installed
app (Plugins → Apps → the app's **Actions** menu → Details → Settings), which maps the manifest's
`settings` through the same one mapper. A `multi` setting is stored
comma-separated, because `GET/PUT …/settings` carries `map[string]string`.

### Placements (M2, `page` added in v2)
- `modal` - opened by an action (`run` answers `{surface}`) or directly.
- `page` - like `modal` on the wire (same view routes, same events) but the
  client opens it as a full page in a NEW TAB (`/apps/{plugin}/{view}?path=`)
  instead of a dialog; for wizards with a document beside them. The action
  row says `view_placement: "page"`. ⚠ In this placement a `pdf-fields` node
  takes the whole viewport minus the step header and footer and fits the page
  to it (the zoom controls stay) - a signature page that needed scrolling in
  two directions is the complaint the placement was added for.
- `inspector` - listed in `GET /api/files/plugins/actions` → `views[]`; the
  details panel shows a collapsible section per matching view (applies rule
  against the selected item) and loads `GET …/views/{p}/{v}?path=`.
- `home` - a row under "Apps" in the side navigation; opens the view with no
  path (`GET …/views/{p}/{v}`), drawn full-size. That is the explorer's side
  bar for everybody the view is offered to, and - for an administrator - also
  an **Apps** section in the admin panel's own side navigation, one row per
  running app's home view (`/admin/apps/{plugin}/home/{view}`), read from the
  same `views[]` answer; with no such view the section is not drawn at all.
  **v3.1: a page of its own, in the same tab.** The explorer's row opens
  `{base}app/{plugin}/{view}` (the SPA's `app-home` route, laid out like
  **My shares**: a way back to the files, the title, the screen) when its
  host sets `config.appHomePage` and handles `open-app-home`; a host that
  does not keeps the dialog. ⚠ `/app/`, not `/apps/` - that is the new-tab
  `page` view and the admin panel's copy.
- **Sections (v3.1)** - a `home` surface may carry `sections: [{id, label:
  Text, count?}]` and `section` (which one it is). The FRAME draws them as a
  menu (the product's tab strip, a count beside each label) and keeps the
  open one in its address, `?section=<id>`, pushed as history: Back walks the
  sections, a link - a notification's - names one. Opening a view with
  `?section=` hands the plugin `data.section` on the `open` event; a plugin
  answers any unknown section with its default and says which it drew. The
  signing app's Signatures page is the first: *Waiting for my signature · I
  asked for these · I have signed these · Every request* (administrators) ·
  *How it works*, one table each.

## Languages (v3)

A `Text` is `{en, tr, …}`, so a plugin could always answer in any language it
liked - and end up half in one, a pad saying "Çiz / Yaz / Yükle" under an
English heading, because nothing checked. Two manifest keys close that:

- **`languages: ["en", "tr"]`** - what the plugin promises to speak (empty
  means `["en"]`, and `en` must be in the list because `en` is the fallback
  everywhere a `Text` appears). ⚠ **Every string the plugin shows must carry
  every language it declares**, and the host **refuses the install** when the
  `describe` answer has a `Text` missing one. An author should see a
  half-translated screen before a person does; `pluginkit/plugintest` checks
  the surfaces too, at `go test` time.
- **`ui_locales: {"es": {"ctx.download": "Descargar", …}}`** - languages for
  **filex itself**, by filex's own string keys: ONE flat dotted namespace over
  both catalogues (the explorer's `packages/core/src/locales/en.ts` and the
  admin panel's `web/src/locales/en.json`, addressed by dotted path). The host
  lists them in the language list every picker offers (the public shell
  included), marks them with the app, and drops them when the app is removed
  or switched off. A missing or empty key falls back to English; an unknown
  key is ignored. The limits are **bytes** - 1 MiB per language, 4 MiB per
  manifest, 4 KiB per string, 128-byte keys of dotted `[A-Za-z0-9_-]`
  segments (`__proto__`, `constructor`, `prototype` refused), a 16 MiB
  manifest document - sized for a complete translation
  (`backend/pkg/pluginkit/wire/langpack.go`). The format, grammar per
  catalogue and tooling: [PLUGIN-KIT.md → Writing a language pack](PLUGIN-KIT.md#writing-a-language-pack).

### Language packs (no module)

A manifest with `ui_locales` and no `actions`, `views`, `public_pages`,
`settings`, `permissions` or `wasm` is a **language pack**
(`wire.Manifest.IsLanguagePack`):

- **Install** takes the manifest alone: multipart without the `wasm` part; a
  GitHub install fetches `filex-app.json` and nothing else; a URL install
  takes `manifest_url` with `url` empty (a given `sha256` pins the manifest).
  A module sent with a pack is refused (`manifest_invalid`), and a manifest
  that is NOT a pack still needs its module (`no module supplied`).
- **Integrity** moves to the manifest: `sha256` on the row is the manifest's,
  and on an instance with `FILEX_PLUGIN_TRUSTED_KEYS` the detached signature
  is over the manifest's sha256.
- **Runtime**: none. `running` means *serving its languages*; enabling,
  disabling, upgrading (either way across the pack/app line) and a restart
  never compile anything.
- **Status** (`GET /api/admin/app-plugins`, and the `plugin` of the detail)
  carries `kind` (`app` | `language_pack`) and `languages: [{code, keys,
  translated, unknown, total, percent, rtl}]` - coverage of the catalogue the
  running binary embeds (`admin/i18n/filex-catalogue-en.json`); `total: 0`
  means the binary has none and coverage is unknown. `percent` is floored.
- **The dry run** (`?dry_run=1`) adds `kind`, `manifest_sha256` (a pack) and
  the same `languages` rows, so the review can say *language pack* and how
  much it covers.
- **Updates**: installed from GitHub (a branch) or an address, a pack follows
  it and moves to a higher `version` by itself - it asks for no permission,
  so there is nothing to approve. A pack whose new version brings a module
  waits for approval ([APP-PLUGINS.md → Updates](APP-PLUGINS.md#updates)). `rtl: true` marks a right-to-left language - the interface
  is laid out right to left in it ([RTL.md](RTL.md)).

### The two public reads

- `GET /api/public/branding` → `locales` (codes) and `ui_locales`: one row
  per added language, **without strings** -
  `[{"code": "es", "source": "plugin", "plugin": "lang-es", "rtl": false}]`.
  ⚠ It was a map of every string of every language until v0.43.0, while the
  browser read a list - no pack's string ever reached a screen.
- `GET /api/public/ui-locales/{code}` → `{"code": "es", "strings": {…}}`, one
  language merged over every running app that ships it (first app by name
  wins a contested key; empty values are left out). `404` when no running app
  ships it. Both are unauthenticated and revalidated - `Cache-Control:
  public, no-cache` with a strong ETag over the body, so a pack installed,
  upgraded or removed a moment ago reaches the next page load and an
  unchanged answer is a `304` with no body (the strings table is ~300 KB for
  a complete language, so that matters). They were `max-age=60` until
  v0.43.0, which is how an installed language could take a minute to appear.
  The browser fetches the strings of the one language somebody picks, once
  (`packages/core/src/lib/uiLocales.ts`).

## An app's own interface (v4)

An app may bring its **own interface** - HTML, CSS and JavaScript it wrote -
instead of (or beside) filex-drawn surfaces. filex serves it from the app's
package and runs it in a sandboxed frame; the only way out of that frame is
the bridge below, and filex decides every call. Operators read
[APP-PLUGINS.md → An app's own interface](APP-PLUGINS.md#an-apps-own-interface);
authors read [PLUGIN-KIT.md → Writing an interface](PLUGIN-KIT.md#writing-an-interface).
Every rule below was measured in Chrome 153, Firefox 150 and WebKit 26.4
(2026-09-27) before it was written.

There are still exactly **two kinds of plugin**: storage plugins and apps. An
app is a module (the wasm engine), an interface, or both:

| The app has | Its manifest carries | Example |
|---|---|---|
| a module only | `wasm` | the converter, the signing app |
| an interface only | `ui` | draw.io: the editor is JavaScript, there is nothing for a module to do |
| both | `wasm` + `ui` | an interface that asks its module to do the heavy part (`engine.call`) |

### Manifest

```json
{
  "name": "drawio",
  "version": "1.0.0",
  "permissions": ["files:read", "files:write"],
  "ui": {
    "bundle": { "url": "https://github.com/<o>/<r>/releases/download/{tag}/ui.zip", "sha256": "<64 hex>" },
    "csp": ["wasm-unsafe-eval"],
    "external": [
      { "url": "https://fonts.example.net/inter/", "as": "font", "reason": { "en": "The Inter typeface" } },
      { "url": "https://cdn.example.net/katex@0.16.9/katex.min.css", "as": "style", "sha256": "<64 hex>", "reason": { "en": "Maths" } }
    ]
  },
  "views": [
    { "id": "editor", "placement": "viewer", "ui": "index.html",
      "applies": { "ext": ["drawio", "dio"] }, "label": { "en": "draw.io" } }
  ]
}
```

- **`ui.bundle`** - a zip of the interface's files. `url` takes `{tag}` like
  `wasm.url` (a relative address is read from the repository at that ref);
  `sha256` pins the zip and is **required** for a GitHub or URL install. An
  upload sends the zip as the multipart part `ui`.
  - Checked at install, never at serving time: at most
    `FILEX_APP_PLUGIN_MAX_UI_MB` (default 128) MiB zipped, 512 MiB unzipped,
    20 000 files, 64 MiB per file; names are forward-slash relative paths with
    no `..`, no `.` segment, no backslash, no drive or leading slash, no
    duplicate (case-insensitively), no symbolic link; every file has an
    extension on the served list (HTML, scripts, styles, JSON, source maps,
    wasm, images, fonts, audio, video, text, XML) or none at all (`LICENSE`).
  - The zip is kept as it is and served **from its own index**: nothing is
    unpacked, so no request path is ever joined onto a directory.
- **`ui.package_fetch`** - `true` lets the interface **read its own
  package** with `fetch` / `XMLHttpRequest` (draw.io loads its stencils,
  shapes and translations that way). Only THIS version's files: the page's
  `connect-src` and Chrome's `Connection-Allowlist` name the package's own
  path (`<P>`) and nothing else - not another version, not another app, not
  filex's API or pages, not the network. A permission (`ui:package-fetch`),
  on the review as "reads its own package". Without it, `connect-src 'none'`.
- **`ui.download`** - `true` lets the interface hand the person a file to
  keep on their own disk (`ui.download` in the bridge, `fx.download` in the
  SDK): an export, a PNG of the drawing. filex does it - a sandboxed frame
  cannot download - on a gesture in the frame or the person's yes, never
  over 256 MiB. A permission (`ui:download`), on the review as "can save
  files to your computer - each time you allow it".
- **`ui.csp`** - exceptions to the interface's script policy:
  `"unsafe-eval"` (`eval`, `new Function`) and `"wasm-unsafe-eval"`
  (WebAssembly compiled in the page). Each is a permission (below). They open
  no network channel (measured); they make a mistake inside the interface
  easier to exploit.
- **`ui.external`** - addresses outside the package the interface loads.
  - `as` is `style`, `font`, `img` or `media`. **`script` is refused**: a
    script from anywhere but the approved package would make the package's
    sha256 meaningless. **`connect` is refused**: an interface that needs data
    asks its module, which holds an `http:<host>` permission the
    administrator approved, and every such request goes through the server's
    guarded client.
  - `url` is `https://`, a whole host (no wildcard), no query or fragment; a
    full file, or a path prefix ending in `/`. It is written in ONE plain
    form, because it goes as it is into the page's policy and into Chrome's
    `Connection-Allowlist` (a URLPattern): a lower-case public host name (not
    an IP address, not `.local`, `.internal`, `.corp`, `.home.arpa`, `.test`
    and the like), an optional port, and a path of letters, digits,
    `. _ ~ @ - /` and `%HH` escapes (never an escaped `/`, `\`, `.` or NUL).
    Anything else - a quote, a backslash, a non-ASCII letter, `( ) { } + :` -
    is refused at install, and a grant row holding one is not read.
  - **With `sha256`, the file is mirrored**: filex downloads it once at
    install, checks the hash and serves it from the package's own address as
    `ext/<host>/<path>` - the interface refers to
    `ext/cdn.example.net/katex@0.16.9/katex.min.css`, and no browser ever asks
    `cdn.example.net`. Not a permission: nothing leaves the reader's browser.
    Only a full file can be mirrored.
  - **Without `sha256`, the address is live**: the reader's browser fetches
    from it. That is a permission (`ui-net:<as>:<url>`), and the review says
    what it means: the address's owner sees who uses the app and when, and
    the interface can put data from the open file into those requests'
    addresses. ⚠ A policy cannot stop that: measured, `lib.css?leak=<64 KB>`
    reached an address allowed for exactly one file, in all three browsers.
  - `reason` is shown beside the address at review, in every language the app
    declares.
- **`views[].ui`** - the file (in the bundle) a view opens. A view is drawn
  EITHER by the app's surfaces (no `ui`) OR by its interface (`ui`), never
  both. `placement` gains **`viewer`**: the interface opens a file the way
  filex's own viewers do - clicking the file, *Open with* - and takes the
  preview's place. ⚠ A viewer NAMES what it opens: its `applies` has a
  non-empty `ext` or `mime` list (a family like `image/*` is fine, `*/*` is
  not), `kind` is `file`, and each kind is a line of the review
  (`ui-viewer:`, below). A viewer with no rule would have been the default
  viewer of every file the person opened, so it is refused at install.
- **`new_documents`** - rows the app adds to filex's **New document** menu
  (below).
- **No module.** A manifest with `ui` and no `wasm` is an app with an
  interface and no engine. It may declare only views that have a `ui`,
  actions that open such a view, settings, and the permissions the bridge
  uses (`files:read`, `files:write`, `settings`). ⚠ Not `state`: that is the
  module's per-file state (`state_set`); the interface's own small store
  (`state.get` / `state.set`) needs no permission - it is the person's, kept
  in their account preferences, 16 KiB per app. An action that
  would run a module, a public page, a wake-up or a permission only a module
  can use is refused at install, with the reason.

### New documents

```json
"new_documents": [
  { "ext": "drawio", "label": { "en": "draw.io diagram", "tr": "draw.io diyagramı" },
    "view": "editor", "template": "filex/blank.xml" }
]
```

A row of the **New document** dialog, under **Apps**, in the app's own
words: the person names the file and picks the folder as for any new
document, the file is made - a copy of `template` (a file of the interface's
package), or empty - and opens in `view`. Where the server keeps drafts it is
a draft until its first save (issue #71), like any other new document.

- `ext` is the kind, lower-case, no dot; the file always carries it.
- `view` is one of the app's `viewer` views, and it must open that kind.
- `template` is a path inside the bundle, on the bundle's served list (a
  blank diagram as `filex/blank.xml`, not `.drawio`), at most 16 MiB; a
  bundle that does not hold it refuses the install.
- `label` in every language the app declares. At most 8 rows, each kind
  once. The app needs `files:write`: a new document is saved by it.
- Each kind is a permission, `ui-new:.<ext>` (below): an update that adds a
  kind asks again.
- The explorer is told the rows in `capabilities.newdoc_types`
  (`group` / `requires` `"app"`, `key` = `app:<plugin>:<ext>`, `app: {plugin,
  view, label}`), a signed-in person only, and only while the app runs with
  the kind granted; the create call names the row by its `key`
  (`POST /api/files/manager?action=newfile` or `POST /api/files/drafts`,
  `{path, name, type: "app:drawio:drawio", exact_name}`).

### Permissions an interface adds

Derived from the `ui` block - never written into `permissions` (listing one
there is refused, so the manifest has ONE place that says it) - and part of
the grant exactly like the others: the review lists them, the install must
grant them, an upgrade that adds one answers `permissions_changed`.

| Permission | Meaning at review |
|---|---|
| `ui` | Runs its own interface in your browser, in a sandbox |
| `ui:eval` | Its interface may run code it builds while it runs (`unsafe-eval`) |
| `ui:wasm-eval` | Its interface may compile WebAssembly in the page (`wasm-unsafe-eval`) |
| `ui:package-fetch` | Its interface reads its own package - the files of this version, nothing else (`ui.package_fetch`) |
| `ui:download` | Its interface can save files to your computer - each time you allow it (`ui.download`) |
| `ui-net:<as>:<url>` | Your browser loads `<as>` from `<url>` while you use it (a live address) |
| `ui-new:.<ext>` | Adds a new `.<ext>` file to the New menu; it opens in this app's interface (`new_documents`) |
| `ui-viewer:.<ext>` / `ui-viewer:<type/subtype>` | Opens those files in its own interface, in place of filex's preview - one line per kind a `viewer` view names, so an update that makes the app the viewer of one more kind asks again |

The bridge also answers to the ordinary permissions: `files:read` for
`file.read`, `files:write` for `file.save` / `file.saveAs`, `settings` for the
administrator's settings in the session.

### Serving: `GET <base>/_appui/<app>/<bundle-sha[:16]>/<path>`

Outside the SPA, unauthenticated and cookieless: it reads no session and sets
no cookie, and a request carrying one is answered exactly like one that does
not. The version is in the address, so a package file is `Cache-Control:
public, max-age=31536000, immutable`; an upgrade opens a new address, and the
previous version stays served for open tabs and a roll-back. ⚠ Except the
**pages** (`.html`): their policy is built from the grant, so they are
`no-cache` with an `ETag` over the page and its policy (`304` when it
matches) and `Vary: Host` - a narrowed grant takes effect at the next
opening. An error is `no-store`.

- The path is decoded once and then checked: `..`, `.`, an empty segment, a
  backslash, an encoded slash, a NUL - `404`. It is then looked up in the
  zip's own index; it is never joined onto a directory.
- `ext/<host>/<path>` is a mirrored external file (above).
- **HTML** answers carry the policy **built from the grant** at the moment of
  serving (`wasmplugin.UIPolicy`, one function), never from anything in the
  package:

  ```
  Content-Security-Policy: default-src 'none'; script-src <P> 'sha256-<bootstrap>' [eval];
    style-src <P> 'unsafe-inline' [live style]; img-src <P> data: blob: [live img];
    font-src <P> [live font]; media-src <P> blob: [live media]; connect-src 'none' | <P>;
    worker-src blob:; frame-src 'none'; child-src 'none'; object-src 'none';
    form-action 'none'; base-uri 'none'; frame-ancestors *; sandbox allow-scripts
  Connection-Allowlist: ("<P>*" [live addresses])
  X-DNS-Prefetch-Control: off
  Referrer-Policy: no-referrer
  Permissions-Policy: every feature ()
  ```

  - `<P>` is the explicit origin and version path
    (`https://files.example.com/_appui/drawio/3f9a…/`). ⚠ Never `'self'`:
    WebKit reads `'self'` as the frame's opaque origin and refuses the app's
    own scripts. Never the bare filex origin either: WebKit sends the session
    cookie with a sandboxed frame's requests to its own site.
  - `sandbox allow-scripts` is in the answer, not only on the frame: an
    interface address opened directly in a tab would otherwise run with
    filex's own origin (measured in all three browsers: localStorage and the
    session cookie reachable).
  - `Connection-Allowlist` makes Chrome refuse WebRTC and the frame's own
    navigation away - the only measure that stops WebRTC there. It names the
    package's own path (`<P>*`), not filex's whole origin; live addresses are
    named in it too, or Chrome refuses them as well.
  - `connect-src` is `'none'`, or `<P>` with `ui:package-fetch`.
  - `X-DNS-Prefetch-Control: off`: Firefox resolves `<link rel=dns-prefetch>`
    names despite the CSP.
- filex puts its **bootstrap** first in every HTML file, before any of the
  app's own code: a small inline script (allowed by its hash) that removes
  the WebRTC constructors from the page and from every frame the page could
  make. In Firefox it is the one measure against WebRTC, together with the
  explorer page's own `frame-src`. It is a seat belt, not a wall: an embed on
  a page with no CSP of its own does not have that `frame-src`. filex's own
  pages name themselves in it BY PATH (`<host>/_appui/`, `<host>/z/`), never
  `'self'`: an interface cannot navigate its frame to another page of filex
  either.
- **Other files** are served with their type from the extension (never
  sniffed), `X-Content-Type-Options: nosniff` and
  `Content-Security-Policy: default-src 'none'; sandbox` - an SVG or a JSON
  file opened as a document runs nothing and bypasses no bootstrap.
- `Access-Control-Allow-Origin: *` on every answer: a sandboxed frame's
  origin is opaque, so a module script or a font it loads is a cross-origin
  request with `Origin: null`. The files are public; no credentials are
  involved.

### How an interface is opened

`GET /api/files/plugins/actions` → each `views[]` row, and each action row
whose view has one, carries

```json
"ui": { "url": "/_appui/drawio/3f9a0c1e5b2d4f60/index.html", "grants": ["files:read", "files:write", "ui"] }
```

`url` is relative to the server's root (the client joins it with its API
base, like every other relative address filex answers); it is absolute when
the interfaces have an origin of their own (`FILEX_APP_UI_ORIGIN`). A `viewer`
view is offered for the files its `applies` rule matches: the explorer opens
it in the preview's place, and *Open with* lists it. ONE rule decides which
app opens a file (`pickAppViewer`, packages/core `lib/appViewer`): *Open
with*'s choice, else the first view in the list that matches. The standalone
editor tab (`/files/edit`) uses it too, and *Open in new tab* carries the
choice as `app=<plugin>/<view>` (`app=builtin` for filex's own viewer).

The explorer draws the frame with ONE component (`AppFrame`,
packages/core), the same in the web app, the desktop app and every embed:

- `sandbox="allow-scripts"` is set **before** `src`, and before the element
  is in the document - a sandbox added afterwards applies from the next
  navigation, and the first document runs unsandboxed (measured: it read the
  session token);
- `referrerpolicy="no-referrer"`, no `allow` attribute (no camera, no
  microphone, no clipboard, no fullscreen);
- ONE bridge per element: when the frame loads a second time the port is
  closed and no new hello is taken. To reload an interface (a new version was
  approved), the explorer replaces the element.

### The interface bridge

The SDK is **`@brftech/filex-app-ui`** (`packages/app-ui`, a few KB, no
dependency; an ES module and a classic-script build, `window.FilexAppUI`).
The protocol is its `src/protocol.ts`, which filex's own frame imports too.

```js
import { connect } from '@brftech/filex-app-ui';

const fx = await connect();
const file = await fx.open();          // FileInfo + text() / bytes() / stream() / save()
editor.load(await file.text());
editor.on('change', () => fx.dirty(true));
fx.onSave(() => editor.serialize());   // filex's Save, a draft's Save to disk, Ctrl+S
```

**Handshake.** The app posts `{type: "filex:hello", v: 1}` to
`window.parent`. The host takes it only when `event.source` is the frame it
drew - the origin is `"null"` for every sandboxed frame, so it cannot tell two
of them apart (measured: a naive listener took a sibling frame's forged
`save`) - and answers once with `{type: "filex:port", v: 1}` and a transferred
`MessagePort`. The app takes the port only when `event.source` is its parent
(and, where the browser offers `location.ancestorOrigins`, when the origin is
the parent's). The trusted origin is never read from the frame's address.
Everything after that goes through the port.

**Requests** are `{id, method, params}`, answered `{id, result}` or
`{id, error: {code, message}}`. `code` is one of `not_granted`, `not_found`,
`read_only`, `invalid`, `too_large`, `failed`, `cancelled`, `unavailable`,
`unknown_method`. ⚠ When the server refused, `message` is at most its short
code (`quota_exceeded`, `locked`, `encrypted`, `not_applicable`), never its
sentence; anything else that failed is `failed` with the word `failed`. An
interface is told what went wrong, not a path on the server's disk or a
driver's words.

| Method | Params → result | Needs |
|---|---|---|
| `session.get` | → `{v, app: {name, version}, view: {id, placement}, locale, dir, theme: {mode, tokens}, user: {name}, files: [{index, name, ext, size, mime, readOnly}], grants, settings?}` | - |
| `file.read` | `{index?, as: stream / bytes / text}` → `{name, size, mime, stream / bytes / text}`. A stream is transferred (`ReadableStream<Uint8Array>`); text is UTF-8 and refused over 32 MiB. | `files:read` |
| `file.save` | `{index?, data: ReadableStream / ArrayBuffer / string, mime?}` → `{saved: true, size}`. Only over a file the interface was opened with - a new version of it, or the draft it is. | `files:write` |
| `file.saveAs` | `{name, data, mime?}` → `{saved: true, name, size}`. A NEW file, in a folder the person picks in filex's own dialog (`cancelled` when they close it). | `files:write` |
| `ui.dirty` | `{dirty: bool}` | - |
| `ui.title` | `{text}` - the frame's title | - |
| `ui.toast` | `{text, tone?: info / success / warning / error}` - drawn by filex, prefixed with the app's name | - |
| `ui.confirm` | `{text, title?, confirm?, cancel?, danger?}` → `bool` - drawn by filex, its title prefixed with the app's name | - |
| `ui.close` | - filex asks first when there are unsaved changes | - |
| `ui.download` | `{name, data: ReadableStream / ArrayBuffer / string, mime?}` → `{saved: true, size}` - a file for the person's own disk. `name` is a file name (no folder). **The person's call** (below). Chromium: File System Access - the person picks where, and a stream is written as it is read, never whole in the page; elsewhere a Blob the browser downloads. At most `LIMITS.maxDownloadBytes` (256 MiB), streamed or not: past it `too_large` and nothing is kept (a picked file is aborted). `cancelled` when the person says no or closes the picker | `ui:download` |
| `clipboard.write` | `{text}` - the host writes it, the same in every browser (Chrome refuses a sandboxed frame; Firefox and WebKit let it). **The person's call:** on a gesture in the frame, or filex asks (`cancelled` on "no") | - |
| `engine.call` | `{method, params}` → the module's answer | a module with a `ui_call` export |
| `job.submit` | `{action, params?}` → `{op}` - one of the app's actions on the opened files, through the ordinary submit checks. **The person's call**, like `clipboard.write` | a module |
| `state.get` / `state.set` | `{key}` → value / `{key, value}` - this person's small store for this app (JSON, 8 KiB a value, 16 KiB an app), kept in the account's preferences | - |

There is no `fetch`, no storage and no cookie in the frame: an interface that
needs to keep something asks `state.set`; one that needs data from elsewhere
asks its module.

**The person's call.** A call marked so above goes ahead only on a
**gesture** - the person clicked or typed in the frame a moment ago, which
the browser tells filex's page too (`navigator.userActivation`, about five
seconds) - and ONE gesture stands for one such call. A click, a key or a
touch on filex's OWN page does not count for five seconds (the page sees
those, never the frame's): not the double-click that opened the app, not a
click on filex's Save. Otherwise filex asks,
above the frame, naming the app ("Sketch wants to copy text to your
clipboard", *Allow* / *Don't allow*); "no" answers `cancelled`. An interface
cannot start a job, fill the clipboard or hand over a download on its own.
*Allow* answers only once the question has been on screen a moment (0.6 s; *Don't allow* at once): the app decides when it
asks, so it could ask right under a click it invited. Where the browser
refuses filex's page the clipboard on a gesture in the frame (WebKit), the
person is asked and their click on *Allow* is the gesture.

**Who is speaking.** Everything filex draws for an interface carries the
app's name: the view's label, and the app's installed name too when the label
does not already say it (a label cannot pass itself off as filex).

**How much.** One frame has at most 32 calls unanswered at once and 400
requests in any 10 seconds; past either a request is answered `unavailable`
with the message `busy`.

**Events** (host → app, `{event, data}`): `theme` (`{mode, tokens}` - the
SDK paints `--fe-*` onto `<html>` by itself), `locale` (`{locale, dir}`),
`file.changed`, `close.request`, `app.updated` (an administrator approved a
new version; reload to use it).

**The host's own requests** (`{hid, request, params}`, answered
`{hid, result}` or `{hid, error}`): `save` - filex's Save button and a
draft's *Save to disk* ask the app for its document; the SDK calls the
`onSave` handler and saves what it returns. A draft (issue #71) is invisible
to the app: `file.save` writes into the draft, and the host's close question
takes it from there.

**Keys are not forwarded.** Ctrl+S inside the frame is handled by the SDK and
runs the app's own save handler; nothing the frame does can press filex's
shortcuts.

### `PUT /api/files/plugins/ui/{plugin}/{view}/save`

The server half of `file.save` and `file.saveAs`, with the person's session
(the frame calls it; the interface cannot). Every check is made again here:
the app is running, the view is its interface, the app holds `files:write`,
the file is of a type the view `applies` to, and the person may write it -
confinement, ACL editor (or the file is the caller's OWN draft, issue #71),
the write gate (locks, filex's own folders, somebody else's draft refused),
not a read-only storage, not in an encrypted folder, and the person's quota
(`507 quota_exceeded`, before a byte is written). A NEW file ("save as") is
held to the same rules: its name must be of a kind the view applies to
(`422 not_applicable` - a diagram editor never saves an `.html`), and never
into an encrypted folder (`403 encrypted`). A failed write answers
`500 save_failed` with a sentence of filex's own; the error itself is in the
server's log.

- `?path=<qualified>` - a new version of that file (versioning keeps the
  previous one) → `200 {saved, path, name, size}`; audit `app_plugin.ui_save`.
- `?dir=<qualified folder>&name=<file name>` - a NEW file there, under a free
  name beside anything that is there → `201`.
- The body is the content, as it is. Up to `FILEX_APP_PLUGIN_MAX_OUTPUT_MB`.
- **Chunked**, for anything over 8 MiB (the explorer does it by itself): the
  first chunk with `&chunk=start` → `202 {session, received}`; the next ones
  with `&session=<id>&offset=<received>`; the last one adds `&final=1` and is
  answered like a one-shot save. Each chunk is at most 8 MiB, so a reverse
  proxy's body limit never trips; the file is written once, at the end. A
  chunk at the wrong offset answers `409 {received}`; a session is the
  person's, for that app, view and file - any other request naming it is
  `404`; an abandoned one is removed after 30 minutes.

### `POST /api/files/plugins/ui/{plugin}/{view}/call`

`{method, params, paths}` → the module's `ui_call` answer (`200 {result}`).
The paths are the files the interface was opened with; each is checked for
the person asking exactly like a view's (confinement, ACL viewer, not in an
encrypted folder) and handed to the module as `context.inputs` (file refs,
never paths). A module without `ui_call` answers `404 unsupported`.

## Interface preferences

Not an app-plugin route, but the one a surface's theming rides on:
`GET|PUT /api/me/prefs?surface=web|desktop` keeps theme, palette, density and
language **per person per surface** in the database
(migration 00047) rather than in `localStorage`, which is per browser and
never per person. Shapes in [BACKEND.md](BACKEND.md#interface-preferences).

## Frontend needs (M1 - added by the UI side; the backend implements)

Things the explorer and the admin SPA code against that the sections above
did not pin down. Each is what the frontend sends or expects today.

1. **Paths, not storage ids.** The explorer holds adapter-qualified wire
   paths (`docs://reports/nda.pdf`) and no id→name map, so `POST …/run`,
   `GET …/views/{plugin}/{view}` and `POST …/event` are sent with `paths`
   (`?path=` on the opening `GET`; a modal's events carry `path` AND, when
   it was opened on a selection, `paths` - see the view section above) in
   that form - the same form `/api/files/copy` and
   `/api/files/move` already accept. `storage_id` is optional and omitted;
   the server resolves the storage from the path's first segment (name or
   uid, as `storageref` does).
2. **Ops row `outputs[].path` is adapter-qualified too** (`docs://…`), so the
   tray's "Open" can load the folder and select the file without guessing
   a storage.
3. **View events from a footer button:** `event: "submit"` when the button
   is the surface's `primary` one, `event: "action"` otherwise; `action_id`
   is the button's id in both cases; `data` is `{}` in M1 (no `form` node
   yet) and the surface's `state` is echoed unchanged.
4. **`text` node props:** `{"text": Text | string, "tone"?: "muted" | "danger" |
   "info", "heading"?: bool}` (`value` is accepted as an alias of `text`).
   `divider` has no props; `row` only has `children`. Anything else renders
   as "unsupported" in M1.
5. **Upgrade dry run:** `POST …/{id}/upgrade?dry_run=1` answers exactly like
   the install dry run (`{manifest, permissions, wasm_sha256}`) so the same
   review step can precede an upgrade.
6. **Dry-run `permissions[].reason`** is the manifest's text in every language
   (`{"en": …, "tr": …}`) and may be absent. The wizard reads it - or, when it
   is absent, `manifest.permission_reasons[id]` - in the caller's locale, and
   says "no reason given" when neither has one. ⚠ Printed as it arrives, it
   shows as raw JSON (it did, until v0.43.0 was about to ship).
7. **Client cache invalidation:** the explorer forgets its cached
   `/api/files/plugins/actions` answer when the admin installs, removes,
   enables/disables or saves overrides for an app (same tab) - a server that
   changes the set out of band is picked up on the next 5-minute refresh.
8. **Ops `cancel` on a finished job** should answer `409` (or `204` as a
   no-op); the tray only offers Cancel on `pending`/`running` rows and polls
   right after.

## Frontend needs (M2 - added by the UI side; the backend implements)

What the surface components and the two placements code against beyond
the sections above.

1. **Users lookup - exact shape.** `GET /api/files/plugins/users?plugin=<name>&q=<text>`
   answers `200 {"users": [{"user_id": 3, "email": "ayse@example.com", "name": "Ayşe Yılmaz"}]}` -
   the same object a `people-picker` value holds, so a picked row goes
   into `data.values[id]` unchanged. `q` is matched against name and email,
   case-insensitive; at most 20 rows; an empty `q` answers `[]`. `403
   permission_denied` when the plugin does not hold `users:lookup` (and a
   404 from an older server) turn the picker into free email entry for the
   life of the node - it never asks again. `name` may be absent.
2. **`form` values are flat.** A `form` node's fields land in `data.values`
   under their FIELD KEYS (`values.quality`, not `values.<form id>.quality`),
   because that is how the plugin declared them; the form node's own `id`
   holds no value. Every other value-holding node (`people-picker`,
   `pin-input`, `file-chooser`) contributes `data.values[<node id>]`.
   `submit`, `action` and `change` all carry the whole map; a `change`
   answer that arrives after a later event was posted is discarded by the
   client, and after a `change` answer the person's current entries win
   over the echoed `values`.
3. **`change` cadence.** Only `form` edits post `event: "change"` (300 ms
   after the last keystroke). Choosing a person, a file or typing a PIN
   does not - those ride on the next `submit`/`action`.
4. **`surface.errors` for form fields** is keyed by field key (`errors.quality`),
   for other nodes by node id. The field is drawn invalid with those words;
   an unknown key is ignored.
5. **`list` row actions** (drawn as the row's **Actions** menu) post `{"event": "action", "action_id": "&lt;action id&gt;",
   "data": {"values": {…}, "row_id": "&lt;row id&gt;"}}` - `values` included, so a
   row action inside a form screen sees the form.
6. **`file-chooser` answers adapter-qualified paths** (`docs://reports/nda.pdf`),
   chosen through the explorer's own picker; `kind: "dir"` requires a folder
   the caller may write into, `kind: "file"` any readable file. An empty
   string means "nothing chosen".
7. **`preview` reads through the manager's preview route** (the same
   authenticated fetch the viewers use), so the path must be one the caller
   may read; a refusal renders as "the preview could not be loaded", not as
   an error of the surface.
8. **`inspector` views load lazily.** The details panel lists the section for
   every `views[]` row whose `applies` accepts the selected single item
   (client mirror of `Matches`; storage rows and trashed rows never match)
   and asks `GET …/views/{p}/{v}?path=` only when the section is opened.
   Footer buttons post through `…/event` exactly as the modal does; `{op}`
   registers the job in the tray, `done` reloads the section.
9. **`home` views open with no `path`** (`GET …/views/{p}/{v}`). A host that
   wires `config.appHomePage` opens one as a **page of its own in the same
   tab** (`{base}app/{plugin}/{view}`, the open section in `?section=`); a
   host that does not keeps the older view dialog at size `xl` regardless of
   `surface.size`. Their `views[]`
   row's `icon` is a name from the explorer's icon set (`sign`, `convert`,
   …) and falls back to the generic plugin glyph.

## Frontend needs (M3 - added by the UI side; the backend implements)

What the public shell (`packages/core/src/components/public/`, over
`usePublicPage.ts`) and the two sign-track components code against beyond the
sections above. ⚠ The addresses below are the v3 ones: the shell is the SPA
served at `/s/` and `/d/`, and it talks to `/api/public/*`. Where a rule says
`/api/public/s/{token}`, v2 said `/api/p/{token}`.

1. **The visitor's language.** The shell reads the browser's language and
   sends it, and the plugin's `page_event` answers in `context.locale`, which
   the server derives from the same header. The shell also **offers a
   picker** - the languages `GET /api/public/branding` lists, including one an
   installed app added through `ui_locales` - because a stranger's browser
   language is a guess and the person reading a contract should be able to
   correct it. ⚠⚠ Choosing one **re-asks** the surface (`POST …/event` with
   `event: "change"`, the state and the values already on screen), it does not
   re-open the link: somebody three steps into signing, title typed and
   signature drawn, must not lose it by pressing the button next to the
   wizard.
2. **Errors carry `error` and may carry `message`.** `401 {"error":
   "pin_wrong"}`, `429 {"error": "locked", "message": "…"}`, `401 {"error":
   "pin_required"}` (event/file before an unlock), `404 not_found`,
   `410 gone`. The shell shows its own words for each code and appends the
   server's `message` for a lock; `message` is never shown on its own. ⚠ A
   `410` carries the **same object shape** as a live link, so the shell
   renders it from what it already parses.
   ⚠⚠ **`expired`, used up and `revoked` are ONE sentence on the page**, and
   that is deliberate rather than an omission. The three are distinguishable
   on the wire because the owner and the audit log need them; the *visitor*
   is told only "this link is not available - ask the person who sent it for
   a new one", because saying which one confirms that the token EXISTED, and
   somebody guessing tokens should not learn that from a page anybody can
   load. A shut PIN gate is a different thing again and says so
   (`pin_locked`, with the server's `message` if it sent one). This paragraph
   used to promise three sentences; the code has said one since v0.43.0
   (`PublicShell.vue` → `public-page-unavailable`), and
   `web/tests/components/publicShell.test.ts` asserts the three are
   indistinguishable - corrected here 2026-09-23 rather than left as a
   contradiction for the next reader. The same reasoning hides the link's
   TITLE until it opens: a locked gate does not name the document behind it.
3. **The unlock cookie is the server's whole session.** The shell keeps
   nothing: no token copy, no PIN, nothing in `localStorage`. An event that
   answers `401 pin_required` (the cookie expires after 12 h) returns the
   visitor to the PIN form, not to an error.
4. **`POST /api/public/s/{token}/event` is the view event without
   `path`/`storage_id`:** `{state, event, action_id, data}`, where `state` is
   the last surface's `state` echoed unchanged and `data.values` is the whole
   value map (M2 rules: form fields flat, other nodes under their id). An
   empty body means `{"event": "open"}`. The visitor's
   footer buttons post `submit` (primary) / `action` exactly as the modal
   does; a `202 {"accepted": true, "job_id"}` ends the conversation on the
   "received, thank you" screen and no further event is posted.
5. **Exposed copies are plain URLs.** `app.files[]` from the state answer are
   offered as links to `GET /api/public/s/{token}/file/{ref}` (`ref`
   URL-encoded, `pub%3A0`), opened in a new tab; the response's `inline`
   disposition and
   `Content-Type` are what the browser draws. A `preview` node may name an
   exposed copy with `{"ref": "pub:0"}` instead of `path`; the shell draws it
   from that same URL (`<img>` for a picture by `files[].mime`, `<embed>`
   otherwise) - no bytes are buffered in the page. The no-JS fallback lists
   the same URLs as plain links, so a signer on a locked-down browser can
   still read the document they were sent.
6. **`signature-pad` node:** `{id, modes?: ["draw","type","upload"],
   width?, height?, label?: Text, required?: bool, font?, fonts?: [key]}`
   (**v3.1**: `label` is drawn as a form field's label and `required` puts
   the same `*` on it - a required signature used to be the one required box
   with no star; `fonts` narrows the faces a typed signature may use, one
   face meaning no picker, and `font` is the one it starts in); value
   `{png_b64, mode, font?}` under `data.values[id]`
   (`font` on a `type` signature only - see *Faces*, below)
   (transparent PNG, at most 600×200 CSS px, drawn at the device pixel
   ratio; an upload is PNG/JPEG ≤ 200 KB fitted into that box). `null`
   when cleared or never signed. The value rides on the next
   `submit`/`action`, never on `change`.
7. **`pdf-fields` node:** `{id, src: {ref}|{path}|{url}, mode:
   "define"|"place"|"edit"|"fill", stamp_lines?,
   fields: [{id, type: signature|initials|date|text|checkbox, label?, page,
   x, y, w, h, assignee?, required?, value?, rule?, font?, style?, lines?,
   format?, placed?}], signers?: [{id,
   label: Text, color?}], signer?, types?}`. Coordinates are FRACTIONS (0..1) of the
   page's rendered viewport, origin top-left, with CropBox and /Rotate
   already applied by pdf.js; `lib/pdfFieldsGeom.fracToPdf` is the
   conversion to PDF user space (points, origin bottom-left of the
   unrotated page) a stamping plugin needs, for /Rotate 0/90/180/270.
   `page` is 1-based.
   - **`label` (v3)** is what the field is CALLED ("Ad soyad", "İmza",
     "Tarih") - shown in the box, in the fill form and in the audit trail.
     The placer names it (a default is offered per type); absent, the type's
     own name stands in, which is what every field placed before v3 has.
     Its reason: a signer used to be shown a whole document and told to find
     their boxes, and three boxes of the same type on one page were
     indistinguishable. A named field can be listed, asked for and reported.
   - **edit** → `data.values[id]` = the whole `fields[]` array, posted as
     `change` (300 ms debounce, like a form) and again on `submit`. Field
     ids are minted `sig-1`, `ini-1`, `date-1`, `text-1`, `chk-1`, never
     reused within the array. A field without `assignee` is anybody's;
     a single-signer surface assigns new fields to that signer.
   - **fill** → `data.values[id]` = `{"fields": [{id, value, font?, rule?}]}`
     for the fields the signer may act on (their own AND the unassigned
     ones) that hold a value; other signers' fields are drawn grey with that
     signer's label and never post. Values: signature/initials `png_b64`
     string, text string, checkbox `true`, date `YYYY-MM-DD` (today when
     tapped). An empty text, an unticked box and a cleared signature are
     absent, not `""`/`false`. The node seeds its values from
     `fields[].value` (and `font`) on arrival, so a re-opened screen keeps
     what was filled. ⚠ `font` and `rule` ride WITH the value because the
     stamping plugin reads this array and never sees the screen - a
     signature typed in Caveat that arrives as a bare string is stamped in
     whatever the plugin happens to default to.
   - `src.ref` resolves through the page's `fileUrl` (rule 5); `src.path`
     through the manager's authenticated preview route (M2 rule 7);
     `src.url` is **dropped by the host** (a screen may not make the viewer's
     browser fetch an address of the app's choosing). A document
     that cannot be loaded renders as "the document could not be loaded"
     inside the surface - never a blank, never a failed surface.
8. **`surface.errors[id]`** on a `signature-pad` / `pdf-fields` node draws
   the node invalid with those words, like any other node.
9. **The public shell boots with no session call at all**: no
   `/api/auth/me`, no `/api/capabilities`, no login redirect. It asks
   `GET /api/public/branding` and `GET /api/public/s|d/{token}`, and nothing
   else; a token that is not one renders "this link is not available".

## Frontend needs (v2 - added by the UI side; the backend implements)

What the state-aware menu, the lock badge, the `page` placement, the output
chip and the addressed notification code against beyond the sections above.

1. **A `page` view's address is relative to the SPA's MOUNT BASE.** The same
   bundle is served from `/admin/` and `/drive/` and only those
   prefixes fall back to index.html, so the client opens
   `{base}apps/{plugin}/{view}?path=…` with the base it was itself served
   from (`ExplorerConfig.pluginPageBase`; `lib/pluginPage` builds it). A
   host that wires no base keeps the modal - a `page` action still works,
   in a dialog. A shell with no tabs to open (the desktop app) takes the
   address through `ExplorerConfig.openPluginPage` instead. **0.50:** the
   same holds for a `page` screen an app asks for (a notification's deep
   link, a screen's or a finished job's `open`): when its tab does not open -
   no base, or a browser that blocks a tab nobody clicked for - it opens in
   the dialog, as the "new tab was blocked" message says. Up to 0.49 that
   path stopped at the message.

2. **The page is the SAME conversation as the modal.** It asks
   `GET …/views/{p}/{v}?path=` itself (event `open`) and posts to
   `…/event`; the action is NOT run through `…/run` first. So a `page`
   action's job is always born from the surface's own `job`, which is why
   `view_placement: "page"` on an action with no `view` is meaningless.
   **0.50:** the page FOLLOWS the job it queued (the ops list, as the tray
   reads it) and says how it ended - done in the app's own words, failed in
   the person's - and when the job's row carries `open` (above, Ops rows) it
   goes there in the same tab. Up to 0.49 it stopped at "the job is queued"
   and nothing said when it ended: the explorer in the other tab announces
   only the jobs it queued itself.

3. **`applies.state` is matched against the row's OWN plugin's keys.** The
   listing carries `app_state: ["<plugin>:<key>"]`; the client strips
   `"<the asking action's plugin>:"` and compares the remainder against the
   bare keys in `state` / `no_state`. An action can only ever ask about its
   own plugin's keys, and a client that compares without stripping shows no
   state-aware row at all - which reads as "the app is broken".

4. **A locked row arrives with `perm` already capped at `viewer`.** The
   client draws the badge from `locked` + `lock` and otherwise changes
   nothing: the write verbs disappear through the ordinary permission gate.
   A rename / move / delete that the server refuses answers
   `423 {"error": "locked", plugin, path, reason?, until?}` - a body the
   client turns into "«app» locked this file: «reason» - until «date»".
   ⚠ The STATUS is what identifies it; a 423 with an unreadable body still
   reads as a lock, never as "you are not allowed to do this".

5. **The ops row does NOT carry the job's output mode.** The tray's
   "new file" / "new version" chip is resolved client-side from the cached
   actions list (`plugin` + `action` → `output_mode`), so it is absent for a
   hidden action and for a surface that overrode the output for that one job
   (`job.output`, which the browser never sees). A wrong chip is worse than
   none - it is the difference between "your file was replaced" and "a copy
   appeared" - so `outputs[].path` staying adapter-qualified is what "Open"
   actually relies on. ⚠ In `version` mode that path IS the input, which is
   why the chip is worth drawing at all.
   *If the backend later puts `output_mode` on the ops row, the client
   prefers it: the manifest default is a guess, the row is the fact.*

6. **A notification without a target is not clickable.** `target` absent or
   `{"kind": "none"}` renders as plain text - no cursor, no hover, no click.
   It used to navigate to the notifications page, which is admin-gated, so
   for everybody else the click was a guard bounce that threw away the
   folder they were standing in. Events that CAN carry an address should
   carry one (`docs/NOTIFICATIONS.md` → Click target).

7. **`target.open` is a deep link, and the client trusts it.** The server
   validates that `open.action` / `open.view` belongs to `open.plugin`
   before storing it, so a client navigates to the file and dispatches
   `plugin:<plugin>/<action>` (or opens the view - as a page when that
   view's placement is `page`) without re-checking. On the web it travels
   as `?select=…&app=…&appAction=|appView=…` so the link survives being
   pasted, reloaded or opened in a new tab.

### Faces and rules (the sign track's two additions)

- **Five faces, a closed set**: `caveat`, `dancing-script`,
  `homemade-apple` (handwriting), `inter`, `source-serif` (formal). A
  `signature-pad` in `type` mode and a `pdf-fields` `text`/`date`/
  `signature`/`initials` field both offer them, and the KEY travels with
  the value (`font`). ⚠ A key, not a CSS family: the faces are shipped with
  the frontend (`packages/core/src/assets/fonts/`, OFL / Apache 2.0) so
  every signer gets the one they picked rather than whatever the machine
  happens to have - and a plugin re-rendering the name at print resolution
  knows which one that was. An unknown key resolves to `caveat`.
- **`rule` on a `text` field** - `{kind: "any"|"number"|"email", min?, max?}`.
  The browser shapes the keystrokes (digits only, no spaces in an email, the
  length bounds) and says what is wrong under the box; the rule then rides
  back with the value so the stamper formats what it is handed instead of
  parsing an ambiguous string. ⚠ It is a convenience, not a boundary: only
  the browser runs it, so a plugin that cares re-checks - the same
  relationship `applies` has with the server's re-check.
- **`format` on a `date` field** - which layout the date is written in, as
  one of the ids the node's `formats` prop offered (`[{id, label, example}]`;
  the signing app offers `DD.MM.YYYY`, `MM/DD/YYYY`, `YYYY-MM-DD`). The
  editor draws them as buttons carrying each layout's EXAMPLE, and the id
  travels back on the field, so the plugin stamps the layout the author
  chose instead of its own default. A field's `format` has to survive the
  round trip like `rule` does: dropped on the way through, the box comes
  back as "no layout" and a form that asked for `12/31/2000` quietly starts
  printing `31.12.2000`. With no `formats` catalogue there is no control and
  the plugin's default stands.
  ⚠⚠ **There is no `date` rule any more** (v3). `date` is a field type, and
  two ways to ask for the same thing is how you get two answers: a date
  placed as a `date` field arrived as `YYYY-MM-DD` while a date typed into a
  text field under the old rule arrived as `DD/MM/YYYY`, so a stamping plugin
  had to guess which of its own boxes it was looking at. An older manifest's
  `kind: "date"` is read as `any` rather than refused - the box keeps taking
  what the person types, it just stops pretending to be a second date
  control.
