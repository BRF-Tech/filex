# `diskfs` — a filex storage plugin in Python, without the SDK

filex's plugin protocol says a plugin can be written in **any language**. This
is that claim, executed: one file, standard library only, no Go, no SDK — and
filex's own acceptance run installs it and drives every capability through it.

It stores files in a real directory (the `root` config field) and implements
the whole of protocol 1, including the parts the Go example
([`../plugin-memfs`](../plugin-memfs/main.go)) does not: native ranged reads,
server-side move and copy, directories, mtimes, the change stream, resumable
**multipart** uploads assembled from parts on disk, and `POST /v1/selftest` —
the throwaway area filex runs its [conformance
probes](../../../docs/PLUGINS.md#conformance-a-plugin-has-to-prove-its-claims)
against, without which a plugin is installed *unverified*.

```bash
chmod +x plugin.py
# Admin → Plugins → Install a plugin → upload plugin.py
# Then: Connections → Add a storage → "Disk (Python example)"
```

The file is executed directly, so the shebang decides the interpreter: filex
does not care that it is Python.

---

## Running the acceptance

`acceptance.sh` boots a throwaway filex, installs this plugin **through the
admin API**, and measures the subsystem end to end. Every line it prints is a
measurement, not a claim:

```bash
go build -o /tmp/filex-lin ./backend/cmd/filex
FILEX_BIN=/tmp/filex-lin bash backend/examples/plugin-diskfs/acceptance.sh
```

What it proves, in order:

| # | Measured |
|---|---|
| 1 | A non-Go plugin is launched by filex and reaches `running`; its describe reports the full capability set |
| 2 | Its driver appears in `/api/admin/storage-drivers` and the config **form comes from the plugin** |
| 3–4 | Files land on the real disk; a **25 MB** upload (past the 8 MB buffered path) round-trips with a matching sha256 |
| 5 | A ranged read reaches the plugin as a real `Range` request (`206`), not the host's emulation |
| 6 | `mkdir`, `move` and `copy` are served by the plugin's own implementations |
| 7 | Delete puts the object in the **trash** and restore brings it back |
| 8 | The plugin is **killed with `-9`** mid-life: the next listing still answers 200, the files are still there, a new process is up, and the admin list reports `restarts=1` |
| 9 | The **remote** kind: a plugin the operator runs, registered by address + token, with the token **sealed** in the database (`enc:v1:…`, never the plaintext) |
| 10 | Protocol level: `set-mtime` changes the file's real mtime, and an SSE event arrives on `/watch` |
| 11 | A **read-only** plugin: capabilities show `range,watch` only, the driver reports `write=false`, reads work, and a write is refused with `driver does not support write` — with nothing written to disk |
| 12 | **Conformance ran at install**: the admin list carries a report with `verified=true`, `scratch=selftest`, and the probes that passed — `write`, `range` and `multipart` among them |
| 13 | A plugin that **lies** is refused. The script edits this one so its write deletes the file it just wrote, installs it, and measures the consequence: state `refused`, `state_error` saying *fails its own claims*, the driver **absent** from `/api/admin/storage-drivers`, and a storage on it impossible to create |
| 14 | `multipart` is declared *and* its probe passed — a part uploaded, completed, read back and compared |
| 15 | **Upgrade in place**: a v2 file is POSTed to `/upgrade`, the reported version changes, and the storage keeps answering 200 throughout — the row and the storages survive |
| 16 | **Upgrade rollback**: a deliberately broken binary is refused, the previous one is restored and started, and the storage still works. A failed upgrade costs an error, not a plugin |
| 17 | The **load** the plugin is under is reported: `max_in_flight=10`, plus in-flight and rejected counts |
| 18 | **Signed plugins**: filex is restarted with `FILEX_PLUGIN_TRUSTED_KEYS` set, and from then on an unsigned upload is refused (400, naming the setting), a bogus signature is refused as a *client* error, and the plugins installed before the key was set keep running |

> ⚠ Step 11 is the one worth understanding. filex decides what a storage can do
> by type-asserting `storage.Writer` at forty-odd call sites, so a read-only
> plugin is handed to filex as a value that **has no write methods at all**. A
> driver that merely returned an error would be offered an upload button, a
> trash move and a version snapshot that each fail at the last moment.

> ⚠ Step 18 restarts the server on purpose. The bug it guards against was
> never in the signature check — it was that the parsed keys were computed and
> never stored on the manager, so enforcement could not be switched on by
> anyone, by any means. Only the real process, with the real environment
> variable, measures that.

> ⚠ Step 13 is the point of conformance in one measurement. The lying plugin is
> **accepted by the install call** (`201`) and only then refused — describe and
> the probes happen after the row exists. Anything driving the admin API has to
> read the *state*, not the status code.

> ⚠ The script kills processes matching `plugin.py` and wipes `/tmp/plugacc*`.
> It is a test harness, not something to point at a live install.

> ⚠ It is Linux-shaped: it wants `pkill`, `pgrep`, `sha256sum`, `python3` and a
> filex binary built for the host (`FILEX_BIN`). Nothing in it is portable to
> Windows, and it is meant to be read as much as run — every line it prints is
> the API's own answer.
