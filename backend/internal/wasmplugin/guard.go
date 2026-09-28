package wasmplugin

import (
	"context"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/model"
)

// ── The person behind a call, asked again where the call reaches further ─
//
// A job is authorised at submit for what it READS (viewer on every input) or
// WRITES (editor). Three host functions reach further than that — a public
// link hands a file to strangers, a lock freezes a file for everyone, an
// addressed notice lands in somebody's bell — so they ask the person's rights
// again, here, at the moment they act.

// actorHoldsEditor answers whether the person a call runs for may hand rel to
// the outside or freeze it: editor on it — the bar the Share dialog sets for a
// public link ("an outbound-access grant", handlers/share.go) — with the lock
// of THIS app waived, because the app that froze a document is the one that
// finishes it (the signing app's delivery link).
//
// ⚠ A call with no person (the hourly wake-up's job, actor id 0) is answered
// by its caller, never here: there is no ACL to ask. See keepsStateOn.
func (r *Registry) actorHoldsEditor(ctx context.Context, actor *model.User, storageID int64, rel string, pluginID int64) bool {
	if actor == nil || actor.ID <= 0 || r.opts.Store == nil {
		return false
	}
	st, err := r.opts.Store.GetStorage(ctx, storageID)
	if err != nil || st == nil {
		return false
	}
	set, err := acl.New(r.opts.Store).LoadSet(ctx, actor, st)
	if err != nil || set == nil {
		return false
	}
	rel = strings.Trim(rel, "/")
	if set.Effective(rel) >= acl.LevelEditor {
		return true
	}
	if l := set.Lock(rel); l != nil && l.PluginID == pluginID {
		return set.EffectiveIgnoringLocks(rel) >= acl.LevelEditor
	}
	return false
}

// mayFreeze answers whether this call may lock rel (hfFileLock).
func (s *Scope) mayFreeze(ctx context.Context, rel string) bool {
	if s.actor != nil && s.actor.ID > 0 {
		return s.reg.actorHoldsEditor(ctx, s.actor, s.storageID, rel, s.plugin.Row.ID)
	}
	return s.hasInput(rel)
}

// callQueueWait is how long a screen call waits for a free slot before it is
// answered "busy".
const callQueueWait = 3 * time.Second

// enterCall takes one of the plugin's screen-call slots (view and public page
// events), waiting at most callQueueWait or until ctx ends, and returns the
// release. The answer when none frees up is CodeBusy — 503 at the door.
//
// ⚠⚠ Every event instantiates the module afresh with up to its memory ceiling
// (256 MiB) for up to its call budget (60 s), and a public page event needs
// nothing but the link, so screens get a ceiling as jobs do (PerPluginJobs).
func (p *Installed) enterCall(ctx context.Context) (func(), error) {
	if p.calls == nil {
		return func() {}, nil
	}
	t := time.NewTimer(callQueueWait)
	defer t.Stop()
	select {
	case p.calls <- struct{}{}:
		return func() { <-p.calls }, nil
	case <-t.C:
	case <-ctx.Done():
	}
	return nil, &CallError{Code: CodeBusy, Message: "the app is busy; try again in a moment"}
}
