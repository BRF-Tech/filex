# API errors

How filex answers when it says no, and how a client should show it.

## The envelope

A refusal is JSON with three fields:

```json
{ "error": "read_only", "message": "This storage is read-only.", "params": { "storage": "archive" } }
```

| Field | What it is | What to do with it |
|-------|------------|--------------------|
| `error` | A **code**: lower-case ASCII (`[a-z0-9_]`), stable across releases. | Branch on it. Never show it to a person. |
| `message` | The **sentence**, written by the server in the reader's language. | Show it as it is. Do not translate it or rebuild it from the code. |
| `params` | The values the sentence was filled with (a name, a limit), when it has any. | Optional; for a program that wants them. |

Some refusals carry more fields beside these (a lock's `plugin` and `until`, a
draft's `limit`, the queue's upper-case `code`): they stay for the clients that
read them.

**The sentence is the server's.** The file manager, the admin panel, the
desktop app, the command line (`filex client …` prints `message`) and an MCP
agent all show the same words for the same refusal. No client keeps a table of
codes or of the server's English to say it in its own words - those copies
drifted from the server and are gone since 0.54. A client uses its own words
only for a failure that never reached the server (the network) and for an
answer with no `message` (it says the status: "Not found", "Too many
requests").

### Which language

`message` is written in the first of:

1. the language of the account making the request (Settings → Language);
2. the request's `Accept-Language`;
3. the instance default (`FILEX_DEFAULT_LOCALE`);
4. English.

A running language pack's language counts: its `server.*` keys translate the
sentences, and a key it lacks falls back to English, never to a raw code. A
right-to-left reader's client isolates the machine runs inside the sentence (a
path, a name) for the paragraph's direction; the words are not changed.

### Codes

The codes the server says with a catalogue sentence (`server.error.<code>` in
`backend/internal/srvtext/locales`):

| Code | When |
|------|------|
| `read_only` | A write to a storage marked read-only. (The manager's verbs answer 403, the app and public doors 409, as before.) |
| `quota_exceeded` | A write over the owner's quota (413 with `code: QUOTA_EXCEEDED`, 507 on the app doors). |
| `name_taken` | Something already has the name (a rename, a restore). |
| `locked` | An app holds the file ([423](#app-locks)). |
| `reserved_name` | A name filex keeps for itself (`.filex-trash`, `.versions` …); `code: RESERVED_NAME`, `name`. |
| `not_cancellable`, `finished` | A queued operation that cannot be stopped once it runs / is already over (`code: NOT_CANCELLABLE`, `FINISHED`). |
| `bad_kind` | The queue does not take that kind of job on that door (`code: BAD_KIND`). |
| `not_applicable` | An app's action does not fit the selected files (422; its `applies` rule names other kinds). |
| `too_many` | A restore of more entries than one request may name (`code: TOO_MANY`, `max`). |
| `drafts_unavailable`, `draft_limit`, `draft_folder_gone` | Drafts ([BACKEND.md → Drafts](BACKEND.md#drafts)); `code` as before. |
| `entry_unavailable` | The storage could not say whether the entry still exists (`code: ENTRY_UNAVAILABLE`, `path`, `reason`). |
| `no_secret_key` | The server has no encryption key, so it cannot issue an access key (`admin_hint` for an administrator). |
| `e2e_policy_undecided` | The encryption policy could not be checked (500). |
| `kind_mismatch`, `not_requestable`, `path_missing`, `too_many_pending` | An encryption request that no longer fits the folder ([BACKEND.md → Encryption policy](BACKEND.md#encryption-policy)). `not_requestable` with a `reason` says the rule's own sentence. |
| `not_in_trash`, `restarted` | A queued job's failure: the entry left the trash meanwhile / the server restarted before an archive job finished. |
| `timeout`, `out_of_memory`, `crashed`, `app_removed`, `action_removed`, `engine_missing`, `office_unconfigured` | Why an app's job failed. An administrator reads the form that says what to fix. |
| `storage_unavailable` | The storage cannot be reached right now (503, an app's doors). |
| `handler_off` | An administrator turned the app off for that file type (403; `params.ext`). |
| `not_granted` | The app was not given the right the call needs when it was installed (403; `params.permission`, e.g. `files:write`). |
| `folder_not_offered`, `bad_folder` | A job's result was sent to a chosen folder by an action that writes beside its file / a folder not named as `storage://path` (400). |
| `save_failed` | An app's interface could not save the file (500; the reason goes to the log, never to the app). |
| `store_screen_hidden` | The store screen is not shown to this account (404). |
| `internal_error` | A fault on the server (500); the error's own words are in `detail`, for the log. |
| `bad_json`, `bad_body`, `bad_multipart`, `bad_id`, `bad_path`, `bad_name` | A request filex could not read: a body that is not JSON (a `field` names the part, when one does), a form that is not multipart, an id that is not a number, a path or a name an app's save does not take. |
| `paths_required`, `too_many_paths`, `root_not_input`, `bad_output_mode` | An app's run or screen with no file, more files than one job takes (`params.max`, `max`), a whole storage as an input, a result kind filex does not know (`params.mode`). |
| `storage_required`, `unknown_storage`, `mixed_storages` | An app's files named without a storage, with one that is not there (`params.name`), or across storages. |
| `params_too_large`, `too_large`, `offset` | What one job may carry (`params.max`); a save larger than the app may write (`params.max`); a chunked save that continues from another byte (`received`). |
| `queue_unavailable`, `spool_unavailable`, `app_plugins_disabled`, `app_store_disabled`, `signing_unavailable` | Something this server is not running (503, or 500 for the save spool); `detail` may say why. |
| `unauthenticated`, `unauthorized` | No signed-in person (401). |
| `lock_place_required`, `ca_pair_required`, `ca_invalid` | An administrator's unlock that names no file; a signing CA import without its certificate and key, or one that cannot be used (`detail`). |
| `invalid_rule` | A Default apps rule refused: the reason is `server.error.rule_*` (`rule_capability`, `rule_kind`, `rule_not_handler`, `rule_cannot`, `rule_unknown`, `rule_placement`, `rule_place`), filled with `params`; the English is `detail`. |
| `out_of_range` | An app's thumbnail limit outside what it takes (`field`, `params.range`). |
| `supertenant_only` | A tenant's administrator at a door the platform operator keeps (403); what was refused is `detail`. |
| `plugins_disabled`, `file_required`, `plugin_source_missing`, `from_source_required`, `plugin_patch_shape`, `request_too_large` | The storage plugins' admin routes: plugins off, an upload with no file, an install that names no source, an upgrade that sends neither a file nor `from_source`, a change that names neither `enabled` nor `source`, a body over the limit (`params.max`). |
| `install_failed`, `upgrade_failed`, `change_failed` | A storage plugin that could not be installed, upgraded (the version that ran before stays; `plugin` is what runs now) or changed: the sentence carries the plugin layer's own English (`params.detail`, also `detail`). |

### One code, the sentence that says why

Some codes have always been answered for several reasons, and a program
branches on the code alone. The code stays; `message` is the sentence of the
reason, from the same catalogue (`server.error.<reason>`):

| Code | Reasons said (`server.error.*`) |
|------|---------------------------------|
| `permission_denied` | `outside_root` (a key limited to one folder, on every door), `app_input_denied` (the person's access to a selected file, `params.name`), `app_folder_denied` (the folder chosen for a result), `app_save_denied` (an app's save), `app_no_lookup`, `action_admin_only`, `permission_denied` (no more to say). |
| `encrypted` | `app_encrypted_read` (`params.name`), `app_encrypted_write`: the server has no key to an encrypted folder, so an app neither reads nor writes there. `app_encrypted_file_read` / `app_encrypted_file_write` (`params.name`, 0.55): the same for a single encrypted file (`.fxe`) - an app neither reads it nor saves over it. |
| `not_found` | `action_unavailable` (the app was removed, stopped or turned off, or the action is gone; Go's words in `detail`), `action_from_app`, `path_missing` (`path` beside it), `app_screen_missing`, `save_gone`, `store_unknown` / `store_app_unknown` (the store screen), `lock_none` (an administrator's unlock), `app_missing` (an app that is not installed), `store_nothing_to_remove`, `storage_plugin_missing`. |
| `demo` | `demo_store`: a public demo connects to no app store and trusts none. |
| `session_required` | `plugin_session_required` (installing, upgrading or changing a plugin with an API key; `request_endpoint` and `params.endpoint` name where to leave a request), `admin_session_required` (the app store's administrator routes), `store_screen_person` (the store screen with an API key). What was refused is `detail`. |
| `bad_request` | `fingerprints_required`, `bad_tenant`, `bad_group` (`params.id`), `enabled_required`, `auto_update_removed`, `open_or_thumbnail`. |
| `bad_json` | `rule_shape`: a Default apps rule that is not `{order, off}` (`params.capability`). |
| `bad_kind`, `demo_refused`, `app_plugins_disabled` | On the Default apps page: `bad_file_kind`, `default_apps_demo`, `default_apps_off`. |
| `too_large` | `chunk_too_large`: one part of a chunked save (`params.max`). |
| `busy`, `timeout`, `plugin_oom`, `plugin_trap`, `unsupported`, `refused` | The host's own refusals in an app call: `app_busy`, `timeout`, `out_of_memory`, `crashed`, `app_unavailable`, `app_refused` (which carries the host's reason, `params.detail`); the host's English is `detail`. A `plugin_error` is the app's own words and passes as the app wrote them. |
| `unsupported` | `app_not_running`. |
| `not_applicable` | `app_kind_unsaved`: an app's interface saves only the kinds of file it opens. |

Refusals that always had a code and a sentence keep them:
`permission_denied` ([PERMISSIONS.md](PERMISSIONS.md)), `blocked_file_type`,
`e2e_not_allowed` with its `reason`, the vault's `VAULT_*` codes, the account
fields' codes (below), the sign-in refusals.

⚠ Not every refusal is in the envelope yet: some older ones still put an
English sentence in `error` and send no `message`. Branch on a code where the
route documents one; print `message` when there is one, and fall back to the
status otherwise.

## A failed queue operation

A row of `GET /api/files/ops` (and MCP `op_get` / `ops_list`) that failed
carries:

| Field | What it is |
|-------|------------|
| `error_code` | The code, from the table above. |
| `error_params` | The values its sentence takes. |
| `error_text` | The sentence, in the language of the person **reading** the row - made when the row is read, so a Turkish and an English reader of the same failure each read their own. |
| `error` | The English detail, for an administrator's second line and the log. |

A row a server before 0.54 wrote has only `error`.

## App locks

A rename, move, delete or overwrite of a file an app holds (the signing app,
while signatures are collected) answers `423`:

```json
{ "error": "locked",
  "message": "e-Signature locked this file until 2026-10-03 14:00: signatures are being collected",
  "plugin": "sign", "plugin_label": { "en": "e-Signature" }, "path": "contracts/nda.pdf",
  "reason": "signatures are being collected", "until": "2026-10-03T14:00:00Z" }
```

The sentence names the app by its label in the reader's language, gives the
app's own reason in that language when it wrote one, and the end of the lock on
the reader's clock (the account's time zone; `UTC` when it has none).

## Sign-in refusals

A wrong password answers `401` with `message` (the tries left) and a lock
answers `429` with `message` and `countdown` - the same sentence with `{wait}`
left open for a form that counts the lock down ([BACKEND.md →
`POST /api/auth/login`](BACKEND.md#post-apiauthlogin-)).

## Account fields

`POST /api/auth/account/check` says, while a form is typed, whether an e-mail
address or a username would be accepted, with the save's codes
(`email_required`, `email_invalid`, `email_taken`, `username_invalid`,
`username_taken`) and sentences ([BACKEND.md →
`POST /api/auth/account/check`](BACKEND.md#post-apiauthaccountcheck-)).

## App store refusals

The app store routes (`/api/admin/app-plugins/stores`, `store-intent`,
`store-intent/install`, the store screen, the licenses) answer a refusal with
a `detail` object beside the code:

```json
{ "error": "intent_version_rollback",
  "message": "1.1.0 is installed; the link is for 1.0.0, which is not newer. A store link installs or upgrades, never goes back.",
  "detail": { "kind": "app", "installed": "1.1.0", "link": "1.0.0",
              "reason": "lang-eo 1.1.0 is installed; the link is for 1.0.0, which is not newer. ..." } }
```

`message` is the server's sentence for the code (`server.store.<code>`) in the
reader's language, filled from `detail` (both sources of a
`store_source_changed`, the pins of an `intent_pin_mismatch`, both filex
addresses of an `intent_wrong_instance`). `detail.kind` says what the link was
for (`app`, `language-pack`, `storage`), and `detail.reason` keeps the
server's English detail for a log. Every client prints `message`; since 0.55
the admin panel keeps no sentence of its own for a store code.

## Plugin request refusals

`POST /api/admin/plugin-requests`, the store screen's request
(`POST /api/app-store/requests`) and a request's approve / reject answer a
refusal with its code in `error` (`reason_required`, `already_installed`,
`incompatible`, `not_found`, `not_pending`, `busy`, `store_request`,
`too_many_requests`, `sha256_mismatch`, `superseded`, `plugins_disabled`,
`app_plugins_disabled`), the server's sentence in `message`
(`server.plugin_request.*`, filled from `params` - a name, a version), and
the English detail in `detail`. A `bad_request` (a body that names its fields
wrongly) says which field it wants (`server.plugin_request.bad_op`,
`app_source_missing`, `bad_status` …); a storage plugin the server refused
(`refused`) or an install that did not finish (`failed`) is said too, with the
plugin layer's English as `detail`.

## App install refusals

An app's install, upgrade or "upgrade from its source" refused, and the
reason an update check could not read an app's source, carry the install
refusal's shape (`wasmplugin.InstallRefusal`):

```json
{ "error": "fetch_failed", "reason": "manifest_not_found", "where": "BRF-Tech/x",
  "refs": ["main", "master"], "status": 404,
  "message": "No filex-app.json was found in BRF-Tech/x (tried: main, master). Check that ...",
  "detail": "filex-app.json not found in BRF-Tech/x: http 404 from raw.githubusercontent.com" }
```

`message` is `server.install.<code>` (a failed download's
`server.install.fetch.<reason>`) in the reader's language, filled from the
fields; `detail` is the server's English. An update check stores the refusal
with its English and the list (`GET /api/admin/app-plugins`, "Check now", an
app's detail) says it again for whoever reads it (`update.refusal.message`).
The fields (`missing`, `reason`, `where`, `refs`, `status`, `requires`,
`filex`) stay for a program. Since 0.55 the install wizard and the Apps list
print `message` and keep no sentence of their own. An install's
`association_errors` (the File types choices it could not write) are
sentences in the reader's language too (`server.error.place_not_own`,
`place_not_new`, a refused rule's `rule_*`, `place_failed`).
