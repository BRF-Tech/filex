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
| `too_many` | A restore of more entries than one request may name (`code: TOO_MANY`, `max`). |
| `drafts_unavailable`, `draft_limit`, `draft_folder_gone` | Drafts ([BACKEND.md → Drafts](BACKEND.md#drafts)); `code` as before. |
| `entry_unavailable` | The storage could not say whether the entry still exists (`code: ENTRY_UNAVAILABLE`, `path`, `reason`). |
| `no_secret_key` | The server has no encryption key, so it cannot issue an access key (`admin_hint` for an administrator). |
| `e2e_policy_undecided` | The encryption policy could not be checked (500). |
| `kind_mismatch`, `not_requestable`, `path_missing`, `too_many_pending` | An encryption request that no longer fits the folder ([BACKEND.md → Encryption policy](BACKEND.md#encryption-policy)). `not_requestable` with a `reason` says the rule's own sentence. |
| `not_in_trash`, `restarted` | A queued job's failure: the entry left the trash meanwhile / the server restarted before an archive job finished. |
| `timeout`, `out_of_memory`, `crashed`, `app_removed`, `action_removed`, `engine_missing`, `office_unconfigured` | Why an app's job failed. An administrator reads the form that says what to fix. |

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
