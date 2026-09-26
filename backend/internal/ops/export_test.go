package ops

import "time"

// SetFinishGrace shortens how long Stop waits for the finishing lane, for the
// tests that stop the queue while a rename, restore or purge is held.
func (s *Service) SetFinishGrace(d time.Duration) { s.finishGrace = d }
