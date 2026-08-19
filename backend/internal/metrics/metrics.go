// Package metrics is filex's Prometheus surface.
//
// There was none before this: `grep -r prometheus` over the whole repo found
// only the plan that asked for it. So this package defines the exposition
// rather than matching an existing one, and everything it publishes is named
// `filex_*`.
//
// # Where it is served
//
// `GET /metrics`, behind the SAME admin gate as every other operator endpoint
// (`auth.Middleware(true)` + `auth.RequireAdmin`). filex is a file server that
// is routinely exposed to the internet, and an open /metrics leaks storage
// names, user counts and traffic shape. A Prometheus job therefore scrapes it
// with an admin session/token; `docs/METRICS.md` shows the scrape config.
//
// # What is instrumented, and why exactly this
//
// The list is the shortest one that answers the questions an operator actually
// asks when uploads on slow storage go wrong:
//
//   - "is anything stuck?"      → staged uploads in flight, and their age
//   - "did it get worse?"       → commits vs. failures vs. retries
//   - "is the backend slow?"    → transfer duration + rolling bytes/sec
//   - "why was I refused?"      → guard counters, one per guard
//   - "is the disk filling?"    → staged bytes, sweeper removals
//   - "who is using the space?" → total quota usage, refusals
//
// Nothing here is on a hot loop: counters are incremented once per upload,
// per commit or per refusal, and the two derived gauges are computed at scrape
// time from in-memory state.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/brf-tech/filex/backend/internal/throughput"
)

// Registry is filex's own registry rather than the default one: a library that
// registers into prometheus.DefaultRegisterer (a dependency we do not control)
// cannot then silently add series to our exposition.
var Registry = prometheus.NewRegistry()

// ── staged uploads ──────────────────────────────────────────────────────────

var (
	// StagedInFlight is the number of staging directories currently open
	// (begun, not yet committed, aborted or swept). Gauge, not counter: the
	// question is "how many right now".
	StagedInFlight = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "filex_staged_uploads_in_flight",
		Help: "Staged uploads that have begun and not yet committed, aborted or been swept.",
	})

	// StagedBytes is the number of bytes currently sitting in the staging
	// area. This is the disk-incident early warning.
	StagedBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "filex_staging_bytes",
		Help: "Bytes currently held in the upload staging area.",
	})

	// StagedBegun counts uploads accepted at `begin`.
	StagedBegun = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "filex_staged_uploads_begun_total",
		Help: "Staged uploads accepted at begin.",
	})

	// StagedBytesStaged counts bytes written into staging (chunk PUTs).
	StagedBytesStaged = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "filex_staged_upload_bytes_staged_total",
		Help: "Bytes accepted into the staging area.",
	})

	// StagedCommitted counts staged uploads whose bytes reached the driver.
	StagedCommitted = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "filex_staged_uploads_committed_total",
		Help: "Staged uploads successfully transferred to their storage driver.",
	})

	// StagedFailed counts transfers that failed (the staging bytes are kept
	// for retry, so this rising while committed does not is the signal that a
	// backend is down).
	StagedFailed = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "filex_staged_uploads_failed_total",
		Help: "Staged upload transfers that failed.",
	})

	// StagedChunkRetries counts chunk PUTs that overwrote a part already
	// present — i.e. a client resuming or re-sending. A rising line here with
	// no failures means flaky links, not a broken server.
	StagedChunkRetries = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "filex_staged_upload_chunk_retries_total",
		Help: "Chunk uploads that re-sent a part filex already held.",
	})

	// StagedAborted counts client-requested aborts.
	StagedAborted = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "filex_staged_uploads_aborted_total",
		Help: "Staged uploads aborted by the client.",
	})
)

// ── the staging GC ──────────────────────────────────────────────────────────

var (
	// SweepRuns counts sweeper passes — a flat line means the GC stopped.
	SweepRuns = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "filex_staging_sweeps_total",
		Help: "Staging sweeper passes.",
	})

	// Swept counts what the sweeper removed. kind="row" is an expired upload
	// the DB knew about; kind="orphan" is a directory with no row at all
	// (debris from a crash between mkdir and INSERT).
	Swept = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "filex_staging_swept_total",
		Help: "Staging entries removed by the sweeper, by kind.",
	}, []string{"kind"})
)

// ── guards ──────────────────────────────────────────────────────────────────

// GuardRefusals counts every refusal by a guard, labelled with which one.
// guard="disk" is the staging free-space guard; guard="quota" is the per-user
// ceiling. One metric with a label rather than two metrics, so an alert can
// say "any guard is firing" without being edited every time one is added.
var GuardRefusals = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "filex_guard_refusals_total",
	Help: "Requests refused by a guard, by guard name.",
}, []string{"guard"})

// Guard label values.
const (
	GuardDisk  = "disk"
	GuardQuota = "quota"
)

// ── transfers ───────────────────────────────────────────────────────────────

var (
	// TransferDuration is how long one transfer op took, by storage and
	// direction. Buckets run from a tenth of a second to twenty minutes,
	// because the whole point of this work is the slow tail.
	TransferDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "filex_transfer_duration_seconds",
		Help:    "Duration of one storage transfer operation.",
		Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60, 180, 600, 1200},
	}, []string{"storage", "direction"})

	// TransferBytes counts bytes moved to/from a storage driver.
	TransferBytes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "filex_transfer_bytes_total",
		Help: "Bytes transferred to or from a storage driver.",
	}, []string{"storage", "direction"})
)

// ── cache (internal/filecache, chunk 3) ─────────────────────────────────────

// CacheEvents counts cache outcomes. Declared here — with the counters already
// registered — so filecache reports through the same exposition instead of
// growing its own registry. result="hit"|"miss"|"evict"|"store".
var CacheEvents = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "filex_cache_events_total",
	Help: "Download cache outcomes, by result.",
}, []string{"result"})

// CacheBytes is how much the download cache currently holds. filecache calls
// CacheBytes.Set; zero until it exists.
var CacheBytes = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "filex_cache_bytes",
	Help: "Bytes currently held by the download cache.",
})

// ── quota ───────────────────────────────────────────────────────────────────

var (
	// QuotaUsageBytes is the sum of every user's usage_bytes. Deliberately
	// UNLABELLED: a per-user gauge is a cardinality bomb on an instance with
	// a few thousand accounts, and the per-user number is already available
	// through the admin API. Seeded at boot from the users table and moved by
	// the accounting decorator afterwards.
	QuotaUsageBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "filex_quota_usage_bytes",
		Help: "Total storage attributed to users (sum of users.usage_bytes).",
	})

	// QuotaAccountedBytes counts absolute movement, so a stuck accounting
	// path is visible even when adds and subs cancel out.
	QuotaAccountedBytes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "filex_quota_accounted_bytes_total",
		Help: "Bytes accounted to users, by direction (added/released).",
	}, []string{"direction"})
)

// Storage plugins. A plugin is somebody else's program in filex's request
// path, so "is it slow, is it failing, is it saturated" has to be answerable
// from outside — the alternative is an operator watching a spinner.
var (
	// PluginOps counts operations by plugin, operation and outcome
	// (ok | error | busy). `busy` is its own outcome on purpose: it means the
	// plugin hit its concurrency ceiling, which is a sizing problem, not a
	// fault to chase.
	PluginOps = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "filex_plugin_ops_total",
		Help: "Storage-plugin operations, by plugin, operation and outcome.",
	}, []string{"plugin", "op", "outcome"})

	// PluginOpDuration is how long the plugin took to answer.
	PluginOpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "filex_plugin_op_duration_seconds",
		Help:    "How long a storage plugin takes to answer, by plugin and operation.",
		Buckets: []float64{0.005, 0.025, 0.1, 0.5, 1, 5, 30},
	}, []string{"plugin", "op"})

	// PluginInFlight is the live count of operations inside a plugin.
	PluginInFlight = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "filex_plugin_in_flight",
		Help: "Operations currently inside a storage plugin.",
	}, []string{"plugin"})

	// PluginRestarts counts supervisor restarts. A plugin that restarts in a
	// loop still "works" for single requests, and this is how that shows up.
	PluginRestarts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "filex_plugin_restarts_total",
		Help: "Times filex has restarted a storage plugin after it exited.",
	}, []string{"plugin"})

	// PluginUp is 1 while a plugin is running and registered.
	PluginUp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "filex_plugin_up",
		Help: "1 when a storage plugin is running and its driver is registered.",
	}, []string{"plugin"})
)

func init() {
	Registry.MustRegister(
		StagedInFlight, StagedBytes, StagedBegun, StagedBytesStaged,
		StagedCommitted, StagedFailed, StagedChunkRetries, StagedAborted,
		SweepRuns, Swept,
		GuardRefusals,
		TransferDuration, TransferBytes,
		CacheEvents, CacheBytes,
		QuotaUsageBytes, QuotaAccountedBytes,
		PluginOps, PluginOpDuration, PluginInFlight, PluginRestarts, PluginUp,
		throughputCollector{},
		// Go runtime + process metrics: goroutines, heap, GC pause, open FDs.
		// Free, and the first thing anyone wants when "filex is slow".
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	// Materialise the label sets that alerts are written against, so a rule
	// like `rate(filex_guard_refusals_total{guard="disk"}[5m])` returns 0
	// instead of "no data" before the first refusal ever happens.
	GuardRefusals.WithLabelValues(GuardDisk)
	GuardRefusals.WithLabelValues(GuardQuota)
	Swept.WithLabelValues("row")
	Swept.WithLabelValues("orphan")

	// One observation at the transfer site feeds BOTH the rolling rate and
	// these counters. The alternative — asking every call site to report
	// twice — ends with one site reporting once, and then the dashboard and
	// internal/filecache are measuring different things.
	throughput.Subscribe(func(storageID int64, dir throughput.Direction, bytes int64, d time.Duration) {
		lbl := strconv.FormatInt(storageID, 10)
		TransferDuration.WithLabelValues(lbl, string(dir)).Observe(d.Seconds())
		TransferBytes.WithLabelValues(lbl, string(dir)).Add(float64(bytes))
	})
}

// ── the throughput bridge ───────────────────────────────────────────────────

var throughputDesc = prometheus.NewDesc(
	"filex_storage_throughput_bytes_per_second",
	"Rolling transfer rate per storage, from internal/throughput (the same signal internal/filecache uses to call a storage slow).",
	[]string{"storage", "direction"}, nil,
)

// throughputCollector publishes internal/throughput at scrape time rather than
// mirroring it into a gauge on every read: the meter is the single source of
// truth, and a mirror is a second copy to keep in step.
type throughputCollector struct{}

func (throughputCollector) Describe(ch chan<- *prometheus.Desc) { ch <- throughputDesc }

func (throughputCollector) Collect(ch chan<- prometheus.Metric) {
	for _, s := range throughput.Snapshot() {
		ch <- prometheus.MustNewConstMetric(
			throughputDesc, prometheus.GaugeValue, s.BytesPerSec,
			strconv.FormatInt(s.StorageID, 10), string(s.Direction),
		)
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

// StorageLabel renders a storage id for a metric label.
func StorageLabel(id int64) string { return strconv.FormatInt(id, 10) }

// Handler serves the exposition. Mount it behind the admin gate.
func Handler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{
		ErrorHandling: promhttp.ContinueOnError,
	})
}
