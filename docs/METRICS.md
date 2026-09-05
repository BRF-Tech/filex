# Metrics

filex exposes Prometheus metrics at **`GET /metrics`**.

## Scraping it

`/metrics` sits **behind the admin gate**, the same one every other operator
endpoint uses. filex is routinely reachable from the internet and its
exposition names storages, counts accounts and shows traffic shape, so it is
not public. The scrape job authenticates as an admin with an API token:

```yaml
scrape_configs:
  - job_name: filex
    metrics_path: /metrics
    scheme: https
    static_configs:
      - targets: ["filex.example.com"]
    authorization:
      type: Bearer
      credentials_file: /etc/prometheus/filex-token   # an admin API token
```

Mint the token in the admin UI (API / MCP), or from the file explorer's
navigation panel under **Connections → API keys** — which an embed proxied with
a shared *app* token does not show
([MCP.md](MCP.md#token-kinds--user-vs-app)) — with an account that has
the `admin` role. A token carrying a `root:` confinement scope will not do —
those are subtree-limited credentials and the admin gate refuses them.

## What is published

Everything is prefixed `filex_`. Go runtime and process metrics
(`go_goroutines`, `go_memstats_*`, `process_open_fds`, …) come along with them,
which is the first thing anyone wants when "filex is slow".

### Staged uploads

| Metric | Type | Meaning |
|---|---|---|
| `filex_staged_uploads_in_flight` | gauge | uploads begun and not yet committed, aborted or swept |
| `filex_staging_bytes` | gauge | bytes currently held in the staging area |
| `filex_staged_uploads_begun_total` | counter | accepted at `begin` |
| `filex_staged_upload_bytes_staged_total` | counter | bytes accepted into staging |
| `filex_staged_uploads_committed_total` | counter | transferred to the storage driver |
| `filex_staged_uploads_failed_total` | counter | transfers that failed (bytes kept for retry) |
| `filex_staged_upload_chunk_retries_total` | counter | chunks re-sent by a client |
| `filex_staged_uploads_aborted_total` | counter | aborted by the client |

The two gauges are moved by events for freshness and **re-measured against the
staging directory on every sweeper pass**, so a restart — or a crash that left
part files behind — cannot leave the dashboard lying about how full the staging
filesystem is.

> Reading them: `failed` rising while `committed` stays flat is a backend that
> is down. `chunk_retries` rising with `failed` flat is a flaky link between the
> client and filex — the protocol is doing its job, and there is nothing to fix
> on the server.

### The staging GC

| Metric | Type | Meaning |
|---|---|---|
| `filex_staging_sweeps_total` | counter | sweeper passes |
| `filex_staging_swept_total{kind="row"}` | counter | expired sessions removed |
| `filex_staging_swept_total{kind="orphan"}` | counter | directories with no DB row removed (crash debris) |

`filex_staging_sweeps_total` not increasing is the alert worth having: an upload
area with no GC is a disk incident waiting, and this project has already lost
29 GB to temp files nobody was watching.

### Guards

| Metric | Type | Meaning |
|---|---|---|
| `filex_guard_refusals_total{guard="disk"}` | counter | staged uploads refused for lack of free space in staging |
| `filex_guard_refusals_total{guard="quota"}` | counter | staged uploads refused by the per-user ceiling |

Both label values are pre-registered, so a rule written against them returns `0`
rather than "no data" before the first refusal ever happens.

### Transfers and per-storage throughput

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `filex_transfer_duration_seconds` | histogram | `storage`, `direction` | one driver transfer, timed |
| `filex_transfer_bytes_total` | counter | `storage`, `direction` | bytes moved to/from a driver |
| `filex_storage_throughput_bytes_per_second` | gauge | `storage`, `direction` | rolling rate over the last 2 minutes |

`direction` is `read` or `write` — a link can be fast one way and slow the
other, and an S3 upload behaves nothing like an S3 download.

The timing covers the **driver call only**, not the DB mirror, the search index
or the post-write hooks: this number has to answer "is the NAS slow", so
anything that is not the backend stays out of it.

The throughput gauge is rendered at scrape time straight from
`internal/throughput`, which is the same signal `internal/filecache` reads to
decide whether a storage is slow enough to be worth caching. One measurement,
two consumers — so the graph and the cache can never disagree.

### Download cache

| Metric | Type | Meaning |
|---|---|---|
| `filex_cache_events_total{result="hit"\|"miss"\|"store"\|"evict"}` | counter | download-cache outcomes |
| `filex_cache_bytes` | gauge | bytes the download cache holds |

Registered here so `internal/filecache` reports through this exposition rather
than growing its own registry. Zero until that cache exists.

### Quota

| Metric | Type | Meaning |
|---|---|---|
| `filex_quota_usage_bytes` | gauge | total storage attributed to accounts (sum of `users.usage_bytes`) |
| `filex_quota_accounted_bytes_total{direction="added"\|"released"}` | counter | absolute movement through the accounting path |

`filex_quota_usage_bytes` is deliberately **unlabelled**. A per-user gauge is a
cardinality bomb on an instance with a few thousand accounts, and the per-user
number is already available through the admin API. It is seeded from the users
table at boot and moved by the same deltas the accounting store writes.

The counter exists so a stalled accounting path is visible even when adds and
releases cancel out — a flat `accounted_bytes_total` while uploads are landing
means the writes are not being counted, which is exactly the bug
[Quotas](QUOTAS.md) documents.

### Storage plugins

A [plugin](PLUGINS.md) is somebody else's program in filex's request path, so
"is it slow, is it failing, is it saturated" has to be answerable from outside.
The alternative is an operator watching a spinner.

| Metric | Type | Meaning |
|---|---|---|
| `filex_plugin_ops_total{plugin,op,outcome="ok"\|"error"\|"busy"}` | counter | every operation filex sends a plugin. `op` is the storage operation (`list`, `stat`, `read`, `read_range`, `write`, `delete`, `mkdir`, `move`, `copy`, `set_mtime`, and `op` for the paths that are not named individually) |
| `filex_plugin_op_duration_seconds{plugin,op}` | histogram | how long the plugin took to answer (buckets 5 ms → 30 s) |
| `filex_plugin_in_flight{plugin}` | gauge | operations inside the plugin right now, out of the ceiling of 10 |
| `filex_plugin_restarts_total{plugin}` | counter | times the supervisor restarted it after it exited |
| `filex_plugin_up{plugin}` | gauge | `1` while it is running **and its driver is registered** — `0` while it is disabled, failed or refused |

⚠ **`busy` is not an error.** It means the plugin hit its concurrency ceiling
and a caller was refused a slot after waiting 5 s. That is a sizing signal — a
storage too popular or a backend too slow for ten parallel operations — not a
fault to chase in the plugin's code. It is a separate outcome precisely so it
cannot hide inside an error rate.

⚠ A plugin that **restarts in a loop still answers single requests**: filex
re-creates the instance and retries once, so a user may see nothing wrong while
the process is dying every few seconds. `filex_plugin_restarts_total` is how
that becomes visible.

⚠ Conformance probes are **not** counted. They run without the limiter and
without metrics, so a probe cannot be refused because users are keeping the
plugin busy, and install-time traffic does not appear as user traffic.

⚠ Neither is the **server-side multipart part push**. It bypasses the slot and
the counter deliberately — a part body can only be read once, so it must not
wait behind a queue or be retried — which means a large staged upload into a
plugin storage moves bytes without moving `filex_plugin_ops_total`. Use the
transfer metrics above for that.

## Alerts worth having

```yaml
groups:
  - name: filex
    rules:
      - alert: FilexStagingGCStopped
        expr: increase(filex_staging_sweeps_total[3h]) == 0
        for: 30m
        annotations:
          summary: "filex staging GC has not run for hours"

      - alert: FilexStagingDiskGuardFiring
        expr: rate(filex_guard_refusals_total{guard="disk"}[15m]) > 0
        annotations:
          summary: "filex is refusing uploads: no room in the staging area"

      - alert: FilexUploadsFailingToTransfer
        expr: rate(filex_staged_uploads_failed_total[15m]) > 0
             and rate(filex_staged_uploads_committed_total[15m]) == 0
        annotations:
          summary: "staged uploads are failing and none are succeeding"

      - alert: FilexStorageSlow
        expr: filex_storage_throughput_bytes_per_second < 1e6
        for: 15m
        annotations:
          summary: "storage {{ $labels.storage }} is under 1 MB/s ({{ $labels.direction }})"

      - alert: FilexPluginDown
        expr: filex_plugin_up == 0
        for: 5m
        annotations:
          summary: "storage plugin {{ $labels.plugin }} is not registered — storages on it cannot open"

      - alert: FilexPluginRestartLoop
        expr: increase(filex_plugin_restarts_total[15m]) > 3
        annotations:
          summary: "storage plugin {{ $labels.plugin }} keeps exiting ({{ $value }} restarts in 15m)"

      - alert: FilexPluginSaturated
        expr: rate(filex_plugin_ops_total{outcome="busy"}[10m]) > 0
        for: 10m
        annotations:
          summary: "storage plugin {{ $labels.plugin }} is refusing work at its concurrency ceiling"
```
