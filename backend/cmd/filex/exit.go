package main

import "errors"

// Exit statuses other than 1, for callers that act on WHY the CLI stopped
// rather than on the fact that it did. The desktop app supervises
// `filex sync run --watch` and needs to tell "the server no longer accepts
// this account's token" (ask the person to reconnect; never restart the
// watcher with the same token) from any other failure (restart, retry).
const (
	exitSignedOut = 3
)

// exitError carries a process exit status alongside the error main prints.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

// errSignedOut is what a 401 turns into once the CLI stops on it.
var errSignedOut = errors.New("signed out: the server no longer accepts this token (HTTP 401)")

// exitCode is the process status for err: 0 for nil, the carried status for
// an *exitError anywhere in the chain, 1 otherwise.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	return 1
}
