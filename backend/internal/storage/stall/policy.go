// Package stall bounds how long a storage driver waits for a backend that
// does not answer, without ever cutting a transfer that keeps moving.
//
// It started as the S3 driver's own answer to issue #44 (a drop into a dead
// bucket took 85.6 s to say so, a store that accepted the connection and never
// answered did not return at all) and moved here for issue #73, when the same
// gaps turned up in the other network drivers: WebDAV cut every transfer at 60
// s, whether it was moving or not, and FTP, SFTP and SMB waited forever on a
// server that stopped answering once connected. One set of pieces now serves
// them all:
//
//   - Policy: a storage's three settings (attempt timeout, attempts, total
//     budget), read from its config, and the descriptor fields that draw them
//     on the admin form (Defaults.Fields).
//   - Conn, Reader, AnswerWatch: the silence limits. Each renews on every sign
//     of life, so it bounds a stall and never the length of a transfer.
//   - Clock and Policy.Do: the retry budget.
//   - Policy.Reason and UnavailableError: "the store is down" in words, as
//     storage.ErrUnavailable.
//
// ⚠ The limits are on SILENCE. A 2 GB upload on a slow line takes as long as
// it takes; what is bounded is how long any one wait for a sign of life may
// last. A test that only proves "the dead store is reported in time" proves
// half of it — every driver here also has a test that a transfer many times
// the attempt timeout long, against a store that reads or writes slowly,
// arrives whole.
package stall

import (
	"time"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// Bounds of the three settings, the same for every driver. The descriptor's
// Min/Max read these constants, so the form and the driver cannot disagree.
const (
	MaxAttemptsLimit     = 20
	AttemptTimeoutLimitS = 600
	TotalTimeoutLimitS   = 3600
)

// MaxBackoff caps the pause between two attempts.
const MaxBackoff = 10 * time.Second

// Defaults are one driver's defaults for the three settings.
type Defaults struct {
	AttemptTimeoutS int
	MaxAttempts     int
	TotalTimeoutS   int
}

// Policy is one storage's retry and timeout settings.
//
//   - AttemptTimeout: how long one attempt may wait for a sign of life —
//     connecting, the answer to a request, each next piece of a transfer.
//     Never a limit on a transfer that keeps moving.
//   - MaxAttempts: how many times a request is tried in all when the failure
//     can pass. A refusal is never retried.
//   - TotalTimeout: no new attempt starts unless a store that is still down
//     could show it within this long of the first (see Clock.Room). An
//     attempt under way is not cut.
type Policy struct {
	MaxAttempts    int
	AttemptTimeout time.Duration
	TotalTimeout   time.Duration
}

// Settings are the three raw values from a storage's config.
//
// ⚠ The driver reads them itself — `Settings{AttemptTimeout:
// cfg["attempt_timeout_s"], …}` — rather than handing its config map here:
// the descriptor test (storage/descriptor_drivers_test.go) parses each
// driver's source for the keys it reads, and a read hidden in this package
// would look like a declared key nothing reads.
type Settings struct {
	AttemptTimeout any
	MaxAttempts    any
	TotalTimeout   any
}

// Policy reads the settings. A missing, empty, zero or unreadable value means
// the default; a value past the upper bound is brought down to it (the form
// enforces the same bounds, the CLI and the API do not).
func (s Settings) Policy(def Defaults) Policy {
	return Policy{
		MaxAttempts:    IntSetting(s.MaxAttempts, def.MaxAttempts, MaxAttemptsLimit),
		AttemptTimeout: time.Duration(IntSetting(s.AttemptTimeout, def.AttemptTimeoutS, AttemptTimeoutLimitS)) * time.Second,
		TotalTimeout:   time.Duration(IntSetting(s.TotalTimeout, def.TotalTimeoutS, TotalTimeoutLimitS)) * time.Second,
	}
}

// IntSetting reads one positive integer setting.
func IntSetting(v any, def, hi int) int {
	n, ok := storage.ConfigInt(v)
	if !ok || n <= 0 {
		return def
	}
	return min(n, hi)
}

// SendStallFloor is the least time a send may go without moving before it is
// cut, whatever the attempt timeout.
//
// ⚠ A send does not move smoothly. The kernel wakes a writer blocked on a full
// send buffer only when about half of what it holds has drained, so against a
// store that reads slowly a healthy upload sits still for (queued/2)/rate at a
// time — measured 1-2 s at 1 MB/s with Linux's 4 MB send buffer, which a 1 s
// attempt timeout cut in half the runs. A variable so a test can lower it.
var SendStallFloor = 60 * time.Second

// SendStall is how long a send may go without moving.
func (p Policy) SendStall() time.Duration { return max(p.AttemptTimeout, SendStallFloor) }

// An upload's last megabytes may still sit in the sockets' buffers when the
// request counts as sent: measured 2.3-3.4 MB against a store that reads at
// 0.5-2 MB/s, which it needed 1.65 s (at 2 MB/s) to 4.65 s (at 512 KB/s) to
// read. The answer wait is lengthened by the time that tail needs at
// sendTailRate, for at most sendTailBytes of it: up to 16 s.
const (
	sendTailBytes = 4 << 20
	sendTailRate  = 256 << 10 // bytes per second
)

// SendTail is the extra answer wait for a request body of n bytes.
func SendTail(n int64) time.Duration {
	n = max(min(n, sendTailBytes), 0)
	return time.Duration(n) * time.Second / sendTailRate
}
