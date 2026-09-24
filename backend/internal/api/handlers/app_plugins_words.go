package handlers

import (
	"context"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// lockReasons turns a lock's stored reason into words — the plain English a
// consumer with no reader gets, and the same words in every language the app
// wrote them in (wasmplugin.Registry.LockReason). Set once when the app
// handlers are built; nil where no app runtime is wired.
//
// ⚠ Package-level because the 423 answer (lockedAnswer) and the listing's
// lock badge (lockView) are free functions used by every write handler, none
// of which holds the registry.
var lockReasons atomic.Pointer[func(*model.AppPluginLock) (string, wire.Text)]

// SetLockReasons wires the registry's LockReason.
func SetLockReasons(f func(*model.AppPluginLock) (string, wire.Text)) {
	if f == nil {
		lockReasons.Store(nil)
		return
	}
	lockReasons.Store(&f)
}

// appLabels resolves an app's manifest name to its own label, in every
// language the manifest wrote it in (wasmplugin.Registry.AppLabel). Set with
// lockReasons, and nil for the same reason: the lock answers are free
// functions with no registry in hand.
var appLabels atomic.Pointer[func(string) wire.Text]

// SetAppLabels wires the registry's AppLabel.
func SetAppLabels(f func(string) wire.Text) {
	if f == nil {
		appLabels.Store(nil)
		return
	}
	appLabels.Store(&f)
}

// lockAppLabel is the label to print for the app holding a lock, or nil when
// nothing can resolve it (no runtime wired, or the app is gone). The client
// falls back to the name, which is what it did before there was a label at
// all — so an app with no label still produces a whole sentence.
func lockAppLabel(name string) wire.Text {
	if name == "" {
		return nil
	}
	if f := appLabels.Load(); f != nil {
		if t := (*f)(name); len(t) > 0 {
			return t
		}
	}
	return nil
}

// lockReasonOf is a lock's reason for a reader: plain words and, when the
// app named a message, every language of it (the client picks the reader's).
//
// ⚠⚠ Why every language (v0.43.0 wave 2): the reason was ONE string in the
// requester's language, so a German administrator read "imzalar toplanıyor"
// under a Turkish requester's lock and a Turkish one read "signatures are
// being collected" under an English one's.
func lockReasonOf(l *model.AppPluginLock) (string, wire.Text) {
	if l == nil {
		return "", nil
	}
	if f := lockReasons.Load(); f != nil {
		return (*f)(l)
	}
	if strings.HasPrefix(l.Reason, "msg:") {
		return "", nil
	}
	return l.Reason, nil
}

// homeOf is CallContext.Home: where this person's results go when they
// cannot go beside their source (output.elsewhere) — the root of the first
// storage, in the administrator's order, that the person may write: enabled,
// not read-only, not a replica, inside their tenant, and they are at least
// editor at its root. "" when there is none; the app then asks without a
// default.
//
// ⚠ filex has no per-person folder; this is the nearest honest thing (the
// owner asked for "the person's own home folder", 2026-09-22).
func (h *AppPlugins) homeOf(ctx context.Context, u *model.User) string {
	if u == nil {
		return ""
	}
	list, err := h.Store.ListStorages(ctx)
	if err != nil {
		return ""
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	scope, confined := confinedScope(ctx)
	for _, st := range list {
		if st == nil || !st.Enabled || st.ReadOnly || st.ReplicaOfID != nil {
			continue
		}
		if confined && !scope.CanAccessStorage(st.ID) {
			continue
		}
		if !aclAllowForPluginAs(ctx, h.ACL, h.Store, u, st.ID, "", acl.LevelEditor, 0) {
			continue
		}
		return st.Name + "://"
	}
	return ""
}
