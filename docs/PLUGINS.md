# Storage plugins

filex speaks local disk, S3, SFTP, FTP, WebDAV and SMB out of the box. A
**plugin** is how it speaks to something it has never heard of: your appliance,
your company's object store, a research archive with its own API — anything
that can list, read and (optionally) write files.

A plugin is **a separate program**. filex starts it and talks to it over a small
HTTP/JSON protocol, so a plugin can be written in any language, ships on its own
schedule, and cannot take filex down when it crashes. Once it is running, its
driver appears in the ordinary storage picker with the config form the plugin
itself describes — nothing about filex's frontend or its release cycle is
involved.

```
Admin → Plugins → Install          Connections → Add a storage
        ┌──────────────┐                  ┌────────────────────┐
        │ your program │◀── HTTP/JSON ────│ filex              │
        │  (any lang)  │   unix socket    │  storage.Driver    │
        └──────────────┘   or loopback    └────────────────────┘
```

---

## Install one

**Admin → Plugins → Install a plugin**, in one of three ways:

| Source | What happens | When to use it |
|---|---|---|
| **Upload a binary** | The file is stored under `<data-dir>/plugins/<name>/`, hashed, and launched. | The normal case. |
| **From a URL** | Downloaded, checked against a **required** SHA256, then as above. | Unattended installs, scripted setups. |
| **Remote service** | Nothing is launched: filex connects to an address you give it with a bearer token you give it. | A sidecar container, a plugin on another host, or a plugin you are developing. |

![The Plugins page with the example plugin running](screenshots/admin-plugins.png)

> ⚠ **A plugin runs with filex's own privileges** and is handed the credentials
> of every storage created on it. Install only plugins you trust — the same
> judgement you would apply to a package you install on the server.

> ⚠ The binary has to be built **for the server**, not for your laptop:
> `GOOS=linux GOARCH=amd64` for the usual Docker image. A binary for the wrong
> platform fails to start, and the Plugins page shows the exec error.

The **name** you choose (`[a-z0-9][a-z0-9_-]{0,31}`) names the plugin's folder
and appears in logs. The **driver** name comes from the plugin itself, and the
storage driver is always `plugin:<driver>` — a plugin can never shadow a
built-in driver, and two plugins cannot claim the same one (the second is
refused, naming the first).

To turn the subsystem off entirely: **`FILEX_PLUGINS_DISABLED=1`**. Nothing is
launched, no remote is contacted, and the admin API answers 503 saying so. In
multi-tenant mode the page is **supertenant-only**: a tenant admin gets a 403
that says whose surface it is.

### States

| State | Meaning |
|---|---|
| **Running** | Described itself, driver registered, storages can open. |
| **Starting…** | Launched; the handshake or describe has not finished. |
| **Failed** | Exited or became unreachable. A binary is restarted with backoff; a remote is re-checked every few seconds. |
| **Refused** | filex will not use it: protocol mismatch, an invalid describe, a driver-name collision, or a binary whose SHA256 no longer matches what was installed. Fix it and press **Restart**. |
| **Off** | Disabled by the toggle. The driver is unregistered, and storages on it stop opening. |

**Removing** a plugin deletes its files and its registration. Storages created
on it are **left alone** — they simply cannot open until the plugin is back.
Deleting somebody's storages is not a decision this button makes; the page shows
how many will be affected before you confirm.

---

## Write one (Go)

The SDK makes a plugin three methods and a `Serve` call. Everything else —
handshake, routing, streaming, error codes, instance bookkeeping — is done for
you.

```go
package main

import (
	"context"
	"io"

	"github.com/brf-tech/filex/backend/pkg/pluginsdk"
)

type myFS struct{ root string }

func (m *myFS) List(ctx context.Context, path string) ([]pluginsdk.Object, error) { /* … */ }
func (m *myFS) Stat(ctx context.Context, path string) (pluginsdk.Object, error)   { /* … */ }
func (m *myFS) Read(ctx context.Context, path string) (io.ReadCloser, error)      { /* … */ }

func main() {
	pluginsdk.Serve(pluginsdk.Plugin[*myFS]{
		Name:    "myfs",
		Version: "1.0.0",
		Label:   "My storage",
		Fields: []pluginsdk.Field{
			{Key: "root", Type: pluginsdk.FieldString, Label: "Root", Required: true, Root: true},
			{Key: "token", Type: pluginsdk.FieldPassword, Label: "API token", Secret: true},
		},
		Open: func(ctx context.Context, cfg map[string]any) (*myFS, error) {
			return &myFS{root: pluginsdk.String(cfg, "root")}, nil
		},
	})
}
```

Two complete, working examples live in the repository, and filex's own tests
install and drive them, so neither can rot:

- [`backend/examples/plugin-memfs`](../backend/examples/plugin-memfs/main.go) —
  the Go SDK, about a hundred lines, in-memory.
- [`backend/examples/plugin-diskfs/plugin.py`](../backend/examples/plugin-diskfs/plugin.py) —
  **Python, standard library only, no SDK**: the same protocol implemented by
  hand, backed by a real directory, with every optional capability (ranged
  reads, move, copy, mkdir, mtimes, the change stream). It exists because
  “any language” is a claim worth proving rather than repeating.

```bash
go build -o myfs ./cmd/myfs      # for the SERVER's platform
# Admin → Plugins → Install → upload `myfs`
```

### Capabilities are your method set

Add a method, gain a capability. There is no list to keep in step:

| Implement | filex gains | If you don't |
|---|---|---|
| `Write` **and** `Delete` | uploads, rename, trash, versions | the storage is read-only everywhere in the UI |
| `RangeReader` | ranged reads (video seek, partial downloads) | filex reads from the start and discards — correct, just slower |
| `Mover` / `Copier` | server-side move / copy | emulated as copy+delete / read+write |
| `Mkdirer` | real directories | treated like an object store: a no-op |
| `Toucher` | a file's own mtime survives a sync | filex does not offer it (better than pretending) |
| `Watcher` | nothing yet — see the note below | filex polls on its sync interval |

> ⚠ `Write` and `Delete` travel **together**. A driver that can create files it
> cannot remove breaks trash and versioning, so filex refuses the pair split at
> registration — and the SDK reports the pair honestly rather than letting you
> discover it later.

> ⚠ Exactly one field should be marked **`Root: true`** — the field that scopes
> a storage inside your backend (a path, a prefix, a bucket). filex refuses to
> mount a backend's root, so a plugin without one has every storage rejected.

> ⚠⚠ **`watch` is declared but not consumed yet.** The protocol carries a
> change stream and the SDK serves it, but nothing in filex subscribes to it
> today: the only event-driven sync mode is `fsnotify`, which works on the
> **local** driver alone and falls back to polling for everything else. No
> built-in driver implements `storage.Watcher` either. Implement it if you
> like — it costs a plugin nothing and the endpoint is part of protocol 1 —
> but until a watch-driven sync mode lands, a storage on your plugin is
> refreshed by the poll interval like any other. Measured 2026-08-19.

> ⚠ Return the SDK's errors — `pluginsdk.ErrNotFound`, `ErrReadOnly`,
> `ErrUnsupported`. They become filex's own `storage.ErrNotFound` and friends, so
> a missing file answers 404 rather than 500.

### Developing against a running filex

Run the plugin yourself and register it as a **remote** plugin, so a rebuild is
a restart of your own process:

```bash
FILEX_PLUGIN_TOKEN=dev-token FILEX_PLUGIN_LISTEN=127.0.0.1:9099 go run ./cmd/myfs
# Admin → Plugins → Install → Remote service
#   Address: http://127.0.0.1:9099   Token: dev-token
```

---

## The protocol (any language)

filex sets two environment variables and reads **one line** on stdout:

```
FILEX_PLUGIN_TOKEN=<32-byte hex>     the bearer token on every request
FILEX_PLUGIN_SOCKET_DIR=<dir>        a private directory you may create a socket in

→ stdout: FILEX-PLUGIN/1 unix:/path/to.sock
      or: FILEX-PLUGIN/1 tcp:127.0.0.1:PORT
```

Everything after that is HTTP with `Authorization: Bearer <token>`:

| Method | Path | Body / notes |
|---|---|---|
| `GET` | `/v1/describe` | `{protocol, name, version, label, fields[], capabilities{}}` |
| `POST` | `/v1/instances` | `{config}` → `{instance}` — one per storage row |
| `DELETE` | `/v1/instances/{id}` | release it |
| `GET` | `/v1/instances/{id}/list?path=` | `{objects: [...]}` |
| `GET` | `/v1/instances/{id}/stat?path=` | one object |
| `GET` | `/v1/instances/{id}/read?path=` | the bytes; honours `Range` when you declare `range` |
| `PUT` | `/v1/instances/{id}/write?path=` | the bytes; `X-Filex-Size` carries the length (`-1` = unknown) |
| `POST` | `/v1/instances/{id}/move` · `/copy` | `{src, dst}` |
| `POST` | `/v1/instances/{id}/delete` · `/mkdir` | `{path}` |
| `POST` | `/v1/instances/{id}/set-mtime` | `{path, mtime}` (RFC 3339) |
| `GET` | `/v1/instances/{id}/watch` | `text/event-stream` of `{op, path, from}` — **not called by filex yet**, see above |

Errors are `{"error": <code>, "message": <text>}`. Codes filex understands:
`not_found`, `read_only`, `unsupported`, `invalid`, and **`no_instance`** —
answer that one for an id you do not recognise and filex re-creates the instance
from the saved config and retries the call once. That is what makes a plugin
restart invisible to the person using the file manager.

> ⚠ If you serve `/watch`, **flush the response headers immediately**. Written
> but unflushed headers leave filex waiting for a response that has already been
> produced — a watch that appears to hang on an idle backend.

> ⚠ A plugin that listens on TCP must bind **loopback**. The token is the only
> thing between the world and the storage credentials filex sends you; filex
> refuses a handshake that advertises a non-loopback address.

---

## Where things live

| What | Where |
|---|---|
| Installed binaries | `<data-dir>/plugins/<name>/` |
| Sockets a plugin creates | `<data-dir>/plugins/<name>/run/` (mode 0600) |
| Registration | the `plugins` table (migration 00029) |
| A remote plugin's token | sealed with `FILEX_SECRET_KEY` — registering one without that key is refused rather than stored in plaintext |
| Host implementation | [`backend/internal/plugin`](../backend/internal/plugin) |
| SDK | [`backend/pkg/pluginsdk`](../backend/pkg/pluginsdk) |

Related: [STORAGE.md](STORAGE.md) for the built-in drivers,
[PROTOCOLS.md](PROTOCOLS.md) for reaching filex *from* other clients (the
opposite direction).
