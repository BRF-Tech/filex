package local

import "time"

// retryWhileLocked runs op, retrying while the filesystem says somebody else
// has the file open.
//
// ⚠ The budget is short on purpose. Every holder this is written for — a
// thumbnail being generated, a file being indexed, a real-time scanner reading
// a freshly written file — lets go within milliseconds. If it is still held
// after a second, waiting longer will not help and the caller deserves the
// error rather than a request that hangs.
//
// ⚠⚠ It retries ONLY on a sharing violation (see locked_windows.go). Retrying
// everything would turn "no such file" and "permission denied" — both of which
// are answers — into a second of dead time before the same answer.
func retryWhileLocked(op func() error) error {
	const budget = time.Second

	err := op()
	if err == nil || !sharingViolation(err) {
		return err
	}
	// Back off gently: the first retry usually wins, and a tight loop would
	// keep the file busy competing with the very handle it is waiting on.
	deadline := time.Now().Add(budget)
	for wait := 10 * time.Millisecond; time.Now().Before(deadline); wait *= 2 {
		time.Sleep(wait)
		err = op()
		if err == nil || !sharingViolation(err) {
			return err
		}
	}
	return err
}
