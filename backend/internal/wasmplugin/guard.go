package wasmplugin

import (
	"context"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protoperm"
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
	set := r.actorSet(ctx, actor, storageID)
	if set == nil {
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

// actorMayDownload answers whether the person a call runs for may download
// rel: the question every download door asks (protoperm.CanRead - the level
// and files.download there, a role's part for some folders or names
// included). A public link that hands over a file - the document itself, or
// copies the app exposes - asks it of every input when it is opened
// (hfShareCreate); the link's door asks it again at every visit (handlers
// linkCreatorAllows, appLinkCopiesServed).
func (r *Registry) actorMayDownload(ctx context.Context, actor *model.User, storageID int64, rel string) bool {
	set := r.actorSet(ctx, actor, storageID)
	return set != nil && protoperm.CanRead(set, strings.Trim(rel, "/"))
}

// actorSet loads the person's ACL set on one storage, or nil (no person, no
// store, a lookup that failed: every question asked of it says no).
func (r *Registry) actorSet(ctx context.Context, actor *model.User, storageID int64) *acl.Set {
	if actor == nil || actor.ID <= 0 || r.opts.Store == nil {
		return nil
	}
	st, err := r.opts.Store.GetStorage(ctx, storageID)
	if err != nil || st == nil {
		return nil
	}
	set, err := acl.New(r.opts.Store).LoadSet(ctx, actor, st)
	if err != nil {
		return nil
	}
	return set
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
