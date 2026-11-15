// Package syncadvice judges a storage's scan interval against what its last
// full scan measured (task #353): how much of the time a scan at that
// interval keeps the storage busy, a rounder and longer interval when that is
// too much, and whether the scan took the process close to the memory it was
// given.
//
// It is the ONE place the thresholds and the formula live. The storage form
// asks the server while a person types an interval
// (GET /api/admin/storages/{id}/interval-advice) and words the answer; it
// never computes it. Judge is a pure function: the handler gathers the
// measurement, the interval and the memory budget and hands them over.
//
// A person may still save any interval (the server's 5-second floor aside):
// this advises, it does not refuse.
package syncadvice

import (
	"math"
	"time"
)

// The thresholds.
const (
	// TargetBusy is the share of the time a suggested interval keeps the
	// storage scanning at most: a 4-minute scan suggests 50 minutes (8 %),
	// the first round step at or under 10 %.
	TargetBusy = 0.10
	// WarnBusy is the share above which an interval is called too short. A
	// 4-minute scan every 5 minutes keeps the disk busy 80 % of the time.
	WarnBusy = 0.25
	// MemWarn: a scan whose peak reaches this share of the memory the
	// process was given is one more allocation away from being killed.
	MemWarn = 0.80
)

// StepsMin are the round intervals, in minutes, a suggestion is chosen from.
var StepsMin = []int{5, 10, 15, 30, 50, 100, 200, 500, 1000, 2000, 5000, 10000}

// Modes, as model.SyncMode spells them (this package imports no model).
const (
	modePoll     = "poll"
	modeFSNotify = "fsnotify"
	modeOnDemand = "ondemand"
	modeLazy     = "lazy"
)

// floor is the server's: an interval under it is "not set" (sync.loopPoll).
const floor = 5 * time.Second

// Scan is what the storage's last full scan that finished "ok" measured.
type Scan struct {
	Duration     time.Duration
	Entries      int
	FinishedAt   time.Time
	PeakMemBytes uint64
}

// Memory sources (Input.MemSource, Memory.Source).
const (
	// MemContainer: the container's memory limit (cgroup).
	MemContainer = "container"
	// MemHost: the machine's memory - there is no container limit.
	MemHost = "host"
)

// Input is everything Judge needs.
type Input struct {
	// Mode is the sync mode the form shows, already resolved ("" is read as
	// poll, the loop an unset mode runs).
	Mode string
	// Interval is the one the form shows; under 5 s it means "the server's
	// default", DefaultInterval.
	Interval        time.Duration
	DefaultInterval time.Duration
	// Last is the last finished scan; nil when the storage has none yet.
	Last *Scan
	// MemLimit is the memory the process was given (sysmem.Allocated), 0 when
	// unknown; MemSource says whose limit it is (MemContainer, MemHost).
	MemLimit  uint64
	MemSource string
	// WatchUntrusted: the storage's folder is on a filesystem a watch cannot
	// be trusted on (a network filesystem, a FUSE mount). `fsnotify` polls at
	// the interval there instead of watching (sync.loopFSNotify, task #351),
	// so it is judged like poll.
	WatchUntrusted bool
}

// State is the verdict.
type State string

const (
	// StateUnmeasured: no full scan has finished yet - advice comes after
	// the first one.
	StateUnmeasured State = "unmeasured"
	// StateOK: scans at this interval keep the storage busy less than
	// WarnBusy of the time.
	StateOK State = "ok"
	// StateBusy: more than WarnBusy of the time; a longer interval is
	// suggested.
	StateBusy State = "busy"
	// StateContinuous: a scan takes longer than the interval, so the next one
	// starts as soon as one ends - the storage is scanned all the time.
	StateContinuous State = "continuous"
	// StateLazy: a lazily catalogued storage. Its interval is the age at
	// which a folder nobody watches is read again by a background pass that
	// yields to people using the storage; filex times no storage-wide walk
	// per interval, so there is no busy share to state (see Judge).
	StateLazy State = "lazy"
	// StateUnused: a mode that does not scan on a schedule (fsnotify,
	// ondemand); the interval is not used.
	StateUnused State = "unused"
)

// ScanFacts is the measurement the advice is based on.
type ScanFacts struct {
	DurationS    int       `json:"duration_s"`
	Entries      int       `json:"entries"`
	FinishedAt   time.Time `json:"finished_at"`
	PeakMemBytes uint64    `json:"peak_mem_bytes,omitempty"`
}

// Memory compares the last scan's peak with what the process was given.
type Memory struct {
	PeakBytes  uint64 `json:"peak_bytes"`
	LimitBytes uint64 `json:"limit_bytes"`
	Percent    int    `json:"percent"`
	Source     string `json:"source,omitempty"`
	// High: at or above MemWarn of the limit.
	High bool `json:"high"`
}

// Advice is Judge's answer, as the endpoint sends it.
type Advice struct {
	State State  `json:"state"`
	Mode  string `json:"mode"`
	// IntervalS is the interval judged, in seconds; DefaultInterval says it
	// is the server's default because the form left the field empty.
	IntervalS       int  `json:"interval_s"`
	DefaultInterval bool `json:"default_interval,omitempty"`
	// BusyPercent: the share of the time scans at IntervalS keep the storage
	// busy (100 when a scan outlasts the interval). Set for ok, busy and
	// continuous.
	BusyPercent int `json:"busy_percent"`
	// SuggestedIntervalS: the shortest round interval (StepsMin) that keeps
	// scans to TargetBusy of the time. Set for busy and continuous.
	SuggestedIntervalS int `json:"suggested_interval_s,omitempty"`
	// TargetBusyPercent is TargetBusy, for the sentence that explains the
	// suggestion.
	TargetBusyPercent int `json:"target_busy_percent"`
	// PollsInstead: an `fsnotify` storage on a filesystem a watch cannot be
	// trusted on, scanned at the interval instead (Input.WatchUntrusted).
	PollsInstead bool       `json:"polls_instead,omitempty"`
	LastScan     *ScanFacts `json:"last_scan,omitempty"`
	// Memory is set when the last scan's peak and the limit are both known,
	// whatever the mode.
	Memory *Memory `json:"memory,omitempty"`
}

// Judge is the advice for in. Pure: same input, same answer.
//
// Lazy: the decision (#353) is to state no busy share and no suggestion. The
// lazy catalogue never walks the storage per interval: after it has converged
// it re-reads, oldest first, the folders nobody watches whose last reading is
// older than the interval, with a pause between folders that grows while
// people use the storage, and every open ahead of it. Nothing times that as
// one scan, so a "busy N % of the time" would be a figure filex did not
// measure. The answer says what the interval means there instead, and still
// carries the memory of the last full scan (Sync now) when there was one.
func Judge(in Input) Advice {
	interval, isDefault := in.Interval, false
	if interval < floor {
		interval, isDefault = in.DefaultInterval, true
	}
	mode := in.Mode
	if mode == "" {
		mode = modePoll
	}
	a := Advice{
		Mode:              mode,
		IntervalS:         int(interval / time.Second),
		DefaultInterval:   isDefault,
		TargetBusyPercent: int(math.Round(TargetBusy * 100)),
	}
	if in.Last != nil {
		a.LastScan = &ScanFacts{
			DurationS:    int(math.Round(in.Last.Duration.Seconds())),
			Entries:      in.Last.Entries,
			FinishedAt:   in.Last.FinishedAt,
			PeakMemBytes: in.Last.PeakMemBytes,
		}
		a.Memory = memoryOf(in)
	}
	switch mode {
	case modeLazy:
		a.State = StateLazy
		return a
	case modeFSNotify:
		if !in.WatchUntrusted {
			a.State = StateUnused
			return a
		}
		a.PollsInstead = true
	case modeOnDemand:
		a.State = StateUnused
		return a
	}
	if in.Last == nil || interval <= 0 {
		a.State = StateUnmeasured
		return a
	}
	busy := in.Last.Duration.Seconds() / interval.Seconds()
	switch {
	case busy >= 1:
		a.State, a.BusyPercent = StateContinuous, 100
	case busy >= WarnBusy:
		a.State, a.BusyPercent = StateBusy, int(math.Round(busy*100))
	default:
		a.State, a.BusyPercent = StateOK, int(math.Round(busy*100))
		return a
	}
	a.SuggestedIntervalS = Suggest(in.Last.Duration, interval)
	return a
}

// Suggest is the shortest round interval (StepsMin) longer than current at
// which a scan of duration keeps the storage busy at most TargetBusy of the
// time; the longest step when none is enough, and 0 when even that is not
// longer than current.
func Suggest(duration, current time.Duration) int {
	need := duration.Seconds() / TargetBusy
	last := 0
	for _, m := range StepsMin {
		s := m * 60
		if float64(s) <= current.Seconds() {
			continue
		}
		last = s
		if float64(s) >= need {
			return s
		}
	}
	return last
}

func memoryOf(in Input) *Memory {
	if in.Last == nil || in.Last.PeakMemBytes == 0 || in.MemLimit == 0 {
		return nil
	}
	ratio := float64(in.Last.PeakMemBytes) / float64(in.MemLimit)
	return &Memory{
		PeakBytes:  in.Last.PeakMemBytes,
		LimitBytes: in.MemLimit,
		Percent:    int(math.Round(ratio * 100)),
		Source:     in.MemSource,
		High:       ratio >= MemWarn,
	}
}
