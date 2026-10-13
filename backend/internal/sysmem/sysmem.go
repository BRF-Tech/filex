// Package sysmem answers two questions about memory: how much this process
// was given, and how much of it the Go runtime is holding.
//
// "Given" is the container's limit when there is one (cgroup v2 memory.max,
// cgroup v1 memory.limit_in_bytes) and the machine's RAM otherwise: a filex in
// a container with 4 GiB on a 64 GiB host was given 4 GiB, and that is the
// figure a budget has to be a share of. The search index takes its memory
// budget from here (issue #115).
package sysmem

import (
	"runtime/metrics"
)

// Budget is the memory this process was given and where the figure came
// from. Bytes is 0 when nothing could be read.
type Budget struct {
	Bytes  uint64
	Source string
}

// Sources, as Budget.Source names them in the log.
const (
	SourceCgroupV2 = "cgroup v2 memory.max"
	SourceCgroupV1 = "cgroup v1 memory.limit_in_bytes"
	SourceHost     = "host memory"
	SourceUnknown  = "unknown"
)

// Allocated returns the memory this process was given: the container's limit
// when it has one below the machine's RAM, the machine's RAM otherwise.
func Allocated() Budget {
	host := hostTotal()
	if limit, src := cgroupLimit(); limit > 0 && (host == 0 || limit < host) {
		return Budget{Bytes: limit, Source: src}
	}
	if host > 0 {
		return Budget{Bytes: host, Source: SourceHost}
	}
	return Budget{Source: SourceUnknown}
}

// Share is percent of b, with percent clamped to [min, max]. A budget that
// could not be read has no share.
func (b Budget) Share(percent, min, max int) uint64 {
	if b.Bytes == 0 {
		return 0
	}
	percent = Clamp(percent, min, max)
	return b.Bytes / 100 * uint64(percent)
}

// Clamp keeps v inside [min, max].
func Clamp(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// metric names read by InUse.
const (
	metricTotal    = "/memory/classes/total:bytes"
	metricReleased = "/memory/classes/heap/released:bytes"
)

// InUse is the memory the Go runtime holds and has not handed back to the
// operating system: every heap span, stack and runtime structure. It leaves
// out memory-mapped files (the search index's segments), whose pages are the
// kernel's to drop under pressure and are not this process's to ration.
func InUse() uint64 {
	s := []metrics.Sample{{Name: metricTotal}, {Name: metricReleased}}
	metrics.Read(s)
	if s[0].Value.Kind() != metrics.KindUint64 || s[1].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	total, released := s[0].Value.Uint64(), s[1].Value.Uint64()
	if released > total {
		return 0
	}
	return total - released
}
