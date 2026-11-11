# extism go-sdk v1.7.1, with one filex patch

This directory is [github.com/extism/go-sdk](https://github.com/extism/go-sdk)
**v1.7.1** (BSD-3-Clause, [LICENSE](LICENSE)), the files a build needs, with
one change. `backend/go.mod` points the module at it:

```
replace github.com/extism/go-sdk v1.7.1 => ./third_party/extism-go-sdk
```

## The change

[filex.patch](filex.patch) (two files, `extism.go` and `host.go`): a `Plugin`
keeps the wazero functions it calls instead of asking for them again on every
call.

Upstream, every `CallWithContext` asks wazero's `ExportedFunction` for the
export, and for each extism kernel function the call goes through (`reset`,
`alloc`, `input_set`, `output_offset`, `output_length`, `error_get`,
`length`). Every guest memory access through the `extism:host/env` module
(`load_u8`, `store_u64`, ...) does the same, and so do host functions that
allocate. wazero builds a **new** function object each time it is asked, with
a new 10 KiB call stack that the Go guest then grows. The patch keeps them in
two maps on the `Plugin` (`exportFns`, `kernelFns`). A `Plugin` is used by
one caller at a time (upstream's rule already: it shares its input and
output), so the maps take no lock. The env module, shared by every instance
of a runtime, finds the calling instance through the context's `plugin`
value and uses that instance's map. Without one, it does what upstream does.

## Why

Measured 2026-11-11 (filex #313, the short-link door under a flood):

| | upstream v1.7.1 | patched |
|---|---|---|
| bytes allocated by one warm route call (echo fixture) | 262,268 | 5,697 |
| share of the door's CPU in building functions | ~18% | - |

`internal/wasmplugin/pool_reuse_test.go` holds the warm call under 32 KiB.
It goes red if this copy stops being the module in use: an upgrade that
drops the `replace`, or a fresh copy without the patch.

Two other routes were looked at first:

- **A newer release.** v1.7.1 is the latest go-sdk release, and its `main`
  still asks for the function on every call. wazero v1.12.0 still builds a
  new function per `ExportedFunction`.
- **The public API.** `Plugin` exposes no `api.Module` or `api.Function`.
  The call also depends on context values and kernel functions that are
  unexported, and the env module's per-byte forwarding is inside the SDK. So
  there is no way around it from outside without `unsafe` reads of
  unexported fields, which would break silently on any upgrade.

## Upgrading extism

1. Copy the new version's `LICENSE`, `README.md`, `extism-runtime.wasm`,
   `extism-runtime.wasm.version`, `go.mod`, `go.sum` and its non-test `.go`
   files over this directory.
2. Re-apply the change: `git apply -p1 filex.patch` from this directory,
   by hand where it no longer applies.
3. Change the version in the `replace` line, then `go build ./...` and
   `go test ./internal/wasmplugin/`. The reuse test says whether the change
   is still in effect.
4. If upstream keeps the functions itself, delete this directory and the
   `replace` line. The reuse test then shows whether upstream's version is
   as cheap.

The Docker builds copy this directory before `go mod download`
(`docker/Dockerfile*`): the `replace` points at it.
