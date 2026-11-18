// Package opswait holds the one budget a test gives the operations queue to
// finish what it was handed, and the words its failure ends with.
//
// ⚠ Why one budget (v0.61.2 release round, task #360): the tests that hand
// the queue a job and wait for it to end each carried a deadline of their own
// - 3, 5, 10, 15 or 20 seconds - sized on a quiet machine. Under -race, with
// the eight internal/api/handlers shards side by side, a job that is fine
// takes longer than that, and five release rounds in a row went red on a
// different one of them each time ("queue never drained", "op never
// finished"), never on a fault. The budget is generous on purpose: a test
// that passes waits only as long as the job takes, and one that fails says
// how long it waited, so a real stall still shows as one.
package opswait

import "time"

// Budget is how long a test waits for queued operations to end.
const Budget = 60 * time.Second

// Waited is the tail a "never finished" failure ends with: how long the test
// waited since start, so a slow machine and a stuck job read differently.
func Waited(start time.Time) string {
	return "(waited " + time.Since(start).Round(time.Millisecond).String() + ")"
}
