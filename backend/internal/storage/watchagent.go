package storage

// WatchAgentApplies reports whether a filex-watch agent (docs/FILEX-WATCH.md)
// can serve a storage on d: the storage is a tree of real folders on a disk
// some machine owns, which the agent watches there.
//
// Not when d is an object store - it walks a tree in one pass (TreeWalker:
// the S3 driver, directly or as a replicated storage's primary) and has no
// folders on a disk to watch - and not when d streams its own changes
// (Watcher: a storage plugin that declares `watch`), which tell filex
// themselves. A plugin declares nothing more about what it is backed by
// (internal/plugin Capabilities), so one that does neither is offered.
//
// The one answer for the server's refusal (handlers/storage_watch.go,
// watch_unsupported_driver) and the storage page, which reads it from the
// driver descriptor (`watch_agent`, Descriptors) rather than a list of names.
func WatchAgentApplies(d Driver) bool {
	if d == nil {
		return false
	}
	if r, ok := d.(*ReplicatedDriver); ok {
		d = r.Primary()
	}
	if _, ok := d.(TreeWalker); ok {
		return false
	}
	if _, ok := d.(Watcher); ok {
		return false
	}
	return true
}
