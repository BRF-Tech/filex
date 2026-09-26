package storage

import (
	"context"
	"time"
)

// MutationCeiling bounds a change that runs on after its caller has gone (see
// DetachMutation). Generous on purpose: every request a driver sends has a
// timeout of its own, so this only has to stop a walk that never ends.
const MutationCeiling = 2 * time.Hour

// DetachMutation is the context a change to the storage runs under once every
// check has passed: the values of ctx — the caller, the tenant, the audit
// record — and none of its cancellation. Every surface that renames, moves,
// trashes, restores or purges a folder asks it here: the HTTP handlers (the
// explorer, the trash, the agent surface) and WebDAV alike.
//
// ⚠⚠ net/http cancels r.Context() the moment the client's connection closes: a
// tab closed, a proxy that stopped waiting (nginx after 60 s by default,
// Cloudflare after 100 s, the admin SPA after 30 s), an agent that gave up on a
// slow tool call, a WebDAV client that timed out a DELETE or a MOVE. On an
// object store a folder is changed one object at a time, and the SDK refuses
// every request after that point. The folder was left in two places, with the
// catalogue still describing the old one, and a retry was refused because the
// half that had arrived already held the name.
//
// Finishing is the only way the storage and the catalogue end up agreeing.
// Nobody may be waiting for the response any more, but the next listing is.
func DetachMutation(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), MutationCeiling)
}
