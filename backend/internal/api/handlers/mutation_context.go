package handlers

import (
	"context"
	"time"
)

// mutationCeiling bounds a change that runs on after its client has gone (see
// detachedMutation). Generous on purpose: every request the driver sends has
// a timeout of its own, so this only has to stop a walk that never ends.
const mutationCeiling = 2 * time.Hour

// detachedMutation is the context a change to the storage runs under once
// every check has passed: the values of ctx — the request's or the MCP call's:
// the caller, the tenant, the audit record — and none of its cancellation.
//
// ⚠⚠ net/http cancels r.Context() the moment the client's connection closes: a
// tab closed, or a proxy that stopped waiting (nginx after 60 s by default,
// Cloudflare after 100 s, the admin SPA after 30 s), or an agent that gave up
// on a slow tool call. On an object store a folder is renamed, moved, trashed,
// restored or purged one object at a time, and the SDK refuses every request
// after that point. The folder was left in two places, with the catalogue
// still describing the old one, and a retry was refused because the half that
// had arrived already held the name. A restore could then never be finished
// from the UI.
//
// Finishing is the only way the storage and the catalogue end up agreeing.
// Nobody may be waiting for the response any more, but the next listing is.
func detachedMutation(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), mutationCeiling)
}
