package ops

import "time"

// SetFinishGrace shortens how long Stop waits for the finishing lane, for the
// tests that stop the queue while a rename, restore or purge is held.
func (s *Service) SetFinishGrace(d time.Duration) { s.finishGrace = d }

// SetTurnFor shortens how long a job put back for a judgement keeps its
// storage's scan back (turnFor), for the tests that watch the announcement run
// out; the returned func puts it back.
func SetTurnFor(d time.Duration) (restore func()) {
	was := turnFor.Swap(int64(d))
	return func() { turnFor.Store(was) }
}
