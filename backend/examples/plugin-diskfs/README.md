# `diskfs` — a filex storage plugin in Python, without the SDK

filex's plugin protocol says a plugin can be written in **any language**. This
is that claim, executed: one file, standard library only, no Go, no SDK — and
filex's own acceptance run installs it and drives every capability through it.

It stores files in a real directory (the `root` config field) and implements
the whole of protocol 1, including the parts the Go example
([`../plugin-memfs`](../plugin-memfs/main.go)) does not: native ranged reads,
server-side move and copy, directories, mtimes and the change stream.

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

> ⚠ Step 11 is the one worth understanding. filex decides what a storage can do
> by type-asserting `storage.Writer` at forty-odd call sites, so a read-only
> plugin is handed to filex as a value that **has no write methods at all**. A
> driver that merely returned an error would be offered an upload button, a
> trash move and a version snapshot that each fail at the last moment.

> ⚠ The script kills processes matching `plugin.py` and wipes `/tmp/plugacc*`.
> It is a test harness, not something to point at a live install.
