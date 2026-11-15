package syncadvice

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const gib = uint64(1) << 30

func scan(d time.Duration) *Scan {
	return &Scan{Duration: d, Entries: 120000, FinishedAt: time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)}
}

// The owner's example (#353): a scan that takes 4 minutes, every 5 minutes,
// keeps the disk busy 80 % of the time; the advice is 50 minutes - the first
// round step at which it is busy at most 10 % (8 %).
//
// Break: compare with WarnBusy instead of TargetBusy in Suggest - it answers
// 30 minutes (13 %).
func TestJudge_TheOwnersExample(t *testing.T) {
	a := Judge(Input{Mode: "poll", Interval: 5 * time.Minute, DefaultInterval: 15 * time.Minute, Last: scan(4 * time.Minute)})
	assert.Equal(t, StateBusy, a.State)
	assert.Equal(t, 80, a.BusyPercent)
	assert.Equal(t, 50*60, a.SuggestedIntervalS)
	assert.Equal(t, 300, a.IntervalS)
	assert.False(t, a.DefaultInterval)
	assert.Equal(t, 10, a.TargetBusyPercent)
	require.NotNil(t, a.LastScan)
	assert.Equal(t, 240, a.LastScan.DurationS)
	assert.Equal(t, 120000, a.LastScan.Entries)
}

// A scan that outlasts its interval: the next one starts the moment one ends.
func TestJudge_AScanLongerThanTheIntervalIsContinuous(t *testing.T) {
	a := Judge(Input{Mode: "poll", Interval: 15 * time.Minute, Last: scan(20 * time.Minute)})
	assert.Equal(t, StateContinuous, a.State)
	assert.Equal(t, 100, a.BusyPercent)
	assert.Equal(t, 200*60, a.SuggestedIntervalS, "200 minutes: 20 / 200 = 10 %")
}

func TestJudge_ALightScanIsFine(t *testing.T) {
	a := Judge(Input{Mode: "poll", Interval: 15 * time.Minute, Last: scan(30 * time.Second)})
	assert.Equal(t, StateOK, a.State)
	assert.Equal(t, 3, a.BusyPercent)
	assert.Zero(t, a.SuggestedIntervalS)
}

// WarnBusy is where "too short" begins, inclusive.
func TestJudge_TheWarningStartsAtAQuarterOfTheTime(t *testing.T) {
	a := Judge(Input{Mode: "poll", Interval: 15 * time.Minute, Last: scan(225 * time.Second)})
	assert.Equal(t, StateBusy, a.State)
	assert.Equal(t, 25, a.BusyPercent)
	assert.Equal(t, 50*60, a.SuggestedIntervalS, "225 s needs 37.5 min; the next round step is 50")

	a = Judge(Input{Mode: "poll", Interval: 15 * time.Minute, Last: scan(224 * time.Second)})
	assert.Equal(t, StateOK, a.State)
}

// An empty field (or anything under the server's 5-second floor) is the
// server's default, and the answer says so.
func TestJudge_AnEmptyIntervalIsTheServersDefault(t *testing.T) {
	for _, typed := range []time.Duration{0, 3 * time.Second} {
		a := Judge(Input{Mode: "poll", Interval: typed, DefaultInterval: 15 * time.Minute, Last: scan(time.Minute)})
		assert.Equal(t, 900, a.IntervalS)
		assert.True(t, a.DefaultInterval)
		assert.Equal(t, StateOK, a.State)
		assert.Equal(t, 7, a.BusyPercent)
	}
}

// A new storage has no measurement: no warning, only "after the first scan".
func TestJudge_NothingMeasuredYet(t *testing.T) {
	a := Judge(Input{Mode: "poll", Interval: time.Minute, MemLimit: 4 * gib, MemSource: MemContainer})
	assert.Equal(t, StateUnmeasured, a.State)
	assert.Zero(t, a.BusyPercent)
	assert.Zero(t, a.SuggestedIntervalS)
	assert.Nil(t, a.LastScan)
	assert.Nil(t, a.Memory)
}

// An unset mode runs the poll loop, and is judged like it.
func TestJudge_AnUnsetModeIsPoll(t *testing.T) {
	a := Judge(Input{Interval: 5 * time.Minute, Last: scan(4 * time.Minute)})
	assert.Equal(t, "poll", a.Mode)
	assert.Equal(t, StateBusy, a.State)
}

// Lazy: no busy share, no suggestion - nothing times a walk per interval
// there - but the memory of the last full scan still counts.
//
// Break: judge lazy like poll - an 80 % warning about a walk the lazy
// catalogue never makes.
func TestJudge_LazySaysWhatTheIntervalMeansThere(t *testing.T) {
	last := scan(4 * time.Minute)
	last.PeakMemBytes = 3_700_000_000
	a := Judge(Input{Mode: "lazy", Interval: 5 * time.Minute, Last: last, MemLimit: 4 * gib, MemSource: MemContainer})
	assert.Equal(t, StateLazy, a.State)
	assert.Zero(t, a.BusyPercent)
	assert.Zero(t, a.SuggestedIntervalS)
	require.NotNil(t, a.Memory)
	assert.True(t, a.Memory.High)
}

func TestJudge_ModesWithoutASchedule(t *testing.T) {
	for _, m := range []string{"fsnotify", "ondemand"} {
		a := Judge(Input{Mode: m, Interval: 5 * time.Minute, Last: scan(4 * time.Minute)})
		assert.Equal(t, StateUnused, a.State, m)
		assert.Zero(t, a.SuggestedIntervalS, m)
	}
}

// The memory warning compares the last scan's peak with what the process was
// given (the container's limit, else the machine's RAM): 80 % or more warns.
func TestJudge_MemoryNearTheLimitWarns(t *testing.T) {
	last := scan(time.Minute)
	last.PeakMemBytes = 3_500_000_000
	a := Judge(Input{Mode: "poll", Interval: 15 * time.Minute, Last: last, MemLimit: 4 * gib, MemSource: MemContainer})
	require.NotNil(t, a.Memory)
	assert.True(t, a.Memory.High)
	assert.Equal(t, 81, a.Memory.Percent)
	assert.Equal(t, MemContainer, a.Memory.Source)
	assert.Equal(t, StateOK, a.State, "memory does not change the interval verdict")

	last.PeakMemBytes = gib
	a = Judge(Input{Mode: "poll", Interval: 15 * time.Minute, Last: last, MemLimit: 4 * gib, MemSource: MemHost})
	require.NotNil(t, a.Memory)
	assert.False(t, a.Memory.High)
	assert.Equal(t, 25, a.Memory.Percent)
}

// No figure, no judgement: a run from before the measurement existed, or a
// platform whose memory could not be read.
func TestJudge_UnknownMemoryIsNotJudged(t *testing.T) {
	last := scan(time.Minute)
	a := Judge(Input{Mode: "poll", Interval: 15 * time.Minute, Last: last, MemLimit: 4 * gib})
	assert.Nil(t, a.Memory, "peak not measured")
	last.PeakMemBytes = gib
	a = Judge(Input{Mode: "poll", Interval: 15 * time.Minute, Last: last})
	assert.Nil(t, a.Memory, "limit unknown")
}

func TestSuggest_RoundSteps(t *testing.T) {
	cases := []struct {
		duration, current time.Duration
		want              int
	}{
		{20 * time.Second, time.Minute, 5 * 60},        // needs 200 s: the shortest step
		{90 * time.Second, 5 * time.Minute, 15 * 60},   // needs 900 s exactly
		{91 * time.Second, 5 * time.Minute, 30 * 60},   // a second more: the next step
		{4 * time.Minute, 5 * time.Minute, 50 * 60},    // the owner's example
		{2 * time.Minute, 50 * time.Minute, 100 * 60},  // always longer than what was typed
		{30 * time.Hour, 15 * time.Minute, 10000 * 60}, // nothing is enough: the longest step
		{30 * time.Hour, 10000 * time.Minute, 0},       // already at the longest
	}
	for _, c := range cases {
		assert.Equal(t, c.want, Suggest(c.duration, c.current), "%s at %s", c.duration, c.current)
	}
}

// Task #351: `fsnotify` on a network filesystem scans at the interval instead
// of watching (sync.loopFSNotify), so its interval is judged like poll's and
// the answer says why. On a disk it watches, the interval is not used.
//
// Break: leave fsnotify StateUnused whatever WatchUntrusted says.
func TestJudge_FSNotifyOnANetworkFilesystemIsJudgedLikePoll(t *testing.T) {
	a := Judge(Input{Mode: "fsnotify", Interval: 5 * time.Minute, Last: scan(4 * time.Minute), WatchUntrusted: true})
	assert.Equal(t, StateBusy, a.State)
	assert.True(t, a.PollsInstead)
	assert.Equal(t, 50*60, a.SuggestedIntervalS)

	a = Judge(Input{Mode: "fsnotify", Interval: 5 * time.Minute, Last: scan(4 * time.Minute)})
	assert.Equal(t, StateUnused, a.State)
	assert.False(t, a.PollsInstead)

	a = Judge(Input{Mode: "ondemand", Interval: 5 * time.Minute, Last: scan(4 * time.Minute), WatchUntrusted: true})
	assert.Equal(t, StateUnused, a.State, "ondemand scans on request only, wherever it is")
}
