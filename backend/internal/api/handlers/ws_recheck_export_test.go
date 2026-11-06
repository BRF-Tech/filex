package handlers

import "time"

// SetWSRecheckEvery shortens how often an open realtime socket asks again
// whether its account may still act (ws.go holdAccount) and returns the
// restore. A test that waits for a socket to close calls it before the socket
// opens.
func SetWSRecheckEvery(d time.Duration) (restore func()) {
	old := wsRecheckEvery.Load()
	wsRecheckEvery.Store(int64(d))
	return func() { wsRecheckEvery.Store(old) }
}
