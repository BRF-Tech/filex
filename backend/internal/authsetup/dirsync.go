package authsetup

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/brf-tech/filex/backend/internal/auth"
)

// Directory sync (auth.DirectorySyncer — docs/LDAP.md → Directory sync):
// run on a provider's interval by RunDirectorySync, and at once by an
// administrator (SyncNow, Admin → Identity providers → Sync now). Each LDAP
// instance syncs its own directory, named by its slug ("ldap", "ldap-2"…).
// One run per instance at a time; the last report is kept in the settings
// table so the page can show it, and so the schedule survives a restart.

// ErrNoDirectorySync is a provider that is not running or cannot sync.
var ErrNoDirectorySync = errors.New("this provider is not running or has no directory to sync")

// ErrSyncRunning is a second run asked for while one is going.
var ErrSyncRunning = errors.New("a directory sync is already running")

// syncTimeout bounds one run.
const syncTimeout = 30 * time.Minute

// syncTick is how often the schedule looks whether a run is due.
const syncTick = time.Minute

// syncKey is where a provider's last report is kept. ⚠ Not under "auth.":
// that prefix is the page's provider fields (LoadStored).
func syncKey(name string) string { return SyncKeyPrefix(name) + "last" }

// SyncKeyPrefix is the prefix of every settings row an instance's directory
// sync keeps — what removing the instance removes.
func SyncKeyPrefix(name string) string { return "authsync." + Canonical(name) + "." }

// DirectoryFor names the running LDAP instance that owns an address (its
// email_domains) — whose people arrive by sign-in and sync, so an account
// made by hand for that address would be a local one the directory could
// never sign in. ok is false when no directory claims it.
func (l *Live) DirectoryFor(email string) (slug, label string, ok bool) {
	type claimer interface {
		ClaimsEmail(string) bool
	}
	for _, e := range l.Current().Entries {
		if e.Name != "ldap" || e.Driver == nil {
			continue
		}
		if c, is := e.Driver.(claimer); is && c.ClaimsEmail(email) {
			label = e.Label
			if label == "" {
				label = e.Slug
			}
			return e.Slug, label, true
		}
	}
	return "", "", false
}

// syncerOf returns a running provider's directory sync.
func (l *Live) syncerOf(name string) (auth.DirectorySyncer, bool) {
	e, ok := l.Current().Entry(name)
	if !ok || e.Driver == nil {
		return nil, false
	}
	s, ok := e.Driver.(auth.DirectorySyncer)
	return s, ok
}

// CanSync reports whether a provider is running and can sync its directory.
func (l *Live) CanSync(name string) bool {
	_, ok := l.syncerOf(name)
	return ok
}

// Syncing reports whether a provider's sync is running now.
func (l *Live) Syncing(name string) bool {
	l.syncMu.Lock()
	defer l.syncMu.Unlock()
	return l.syncing[Canonical(name)]
}

// LastSync returns a provider's last report, or nil when it never ran.
func (l *Live) LastSync(ctx context.Context, name string) (*auth.DirectorySyncReport, error) {
	// No row, or one that cannot be read, is "never ran".
	var rep auth.DirectorySyncReport
	if raw, err := l.opts.Store.GetSetting(ctx, syncKey(name)); err != nil || raw == "" || json.Unmarshal([]byte(raw), &rep) != nil {
		return nil, nil
	}
	return &rep, nil
}

// SyncNow runs a provider's directory sync and keeps its report. trigger is
// "manual" or "schedule". The run is detached from ctx's cancellation (a
// closed browser tab must not stop it half way) but bounded by syncTimeout.
func (l *Live) SyncNow(ctx context.Context, name, trigger string) (*auth.DirectorySyncReport, error) {
	name = Canonical(name)
	syncer, err := l.claim(name)
	if err != nil {
		return nil, err
	}
	defer l.release(name)
	return l.run(ctx, name, trigger, syncer)
}

// StartSync claims a provider's sync and runs it in the background: the
// error is that it could not start (ErrNoDirectorySync, ErrSyncRunning);
// the run's own outcome is its report (LastSync).
func (l *Live) StartSync(ctx context.Context, name, trigger string) error {
	name = Canonical(name)
	syncer, err := l.claim(name)
	if err != nil {
		return err
	}
	go func() {
		defer l.release(name)
		_, _ = l.run(ctx, name, trigger, syncer)
	}()
	return nil
}

// claim marks a provider's sync running, or says why it cannot run.
func (l *Live) claim(name string) (auth.DirectorySyncer, error) {
	syncer, ok := l.syncerOf(name)
	if !ok {
		return nil, ErrNoDirectorySync
	}
	l.syncMu.Lock()
	defer l.syncMu.Unlock()
	if l.syncing == nil {
		l.syncing = map[string]bool{}
	}
	if l.syncing[name] {
		return nil, ErrSyncRunning
	}
	l.syncing[name] = true
	return syncer, nil
}

func (l *Live) release(name string) {
	l.syncMu.Lock()
	delete(l.syncing, name)
	l.syncMu.Unlock()
}

// SyncInterval is a running provider's sync interval (0: none).
func (l *Live) SyncInterval(name string) time.Duration {
	if s, ok := l.syncerOf(name); ok {
		return s.SyncInterval()
	}
	return 0
}

// run is one sync, already claimed.
func (l *Live) run(ctx context.Context, name, trigger string, syncer auth.DirectorySyncer) (*auth.DirectorySyncReport, error) {
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), syncTimeout)
	defer cancel()
	rep, err := syncer.SyncDirectory(runCtx)
	if rep == nil {
		rep = &auth.DirectorySyncReport{StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC()}
		if err != nil {
			rep.Error = err.Error()
		}
	}
	rep.Provider, rep.Trigger = name, trigger
	if raw, merr := json.Marshal(rep); merr == nil {
		if serr := l.opts.Store.UpsertSetting(context.WithoutCancel(ctx), syncKey(name), string(raw)); serr != nil {
			l.opts.Log.Warn("auth: could not keep the directory sync report", slog.String("provider", name), slog.Any("err", serr))
		}
	}
	attrs := []any{
		slog.String("provider", name), slog.String("trigger", trigger),
		slog.Int("found", rep.Found), slog.Int("created", rep.Created), slog.Int("updated", rep.Updated),
		slog.Int("skipped", rep.Skipped), slog.Int("missing", rep.Missing), slog.Int("disabled", rep.Disabled),
		slog.Duration("took", rep.FinishedAt.Sub(rep.StartedAt)),
	}
	if err != nil {
		l.opts.Log.Warn("auth: directory sync stopped", append(attrs, slog.Any("err", err))...)
	} else {
		l.opts.Log.Info("auth: directory sync", attrs...)
	}
	return rep, err
}

// RunDirectorySync runs every provider's directory sync on its interval
// until ctx ends: at each tick, a provider whose last run started an
// interval ago or more (or never ran) runs. Blocks — start it with go.
func (l *Live) RunDirectorySync(ctx context.Context) {
	t := time.NewTicker(syncTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for _, e := range l.Current().Entries {
			s, ok := e.Driver.(auth.DirectorySyncer)
			if !ok || e.Driver == nil || s.SyncInterval() <= 0 || l.Syncing(e.Slug) {
				continue
			}
			last, _ := l.LastSync(ctx, e.Slug)
			if last != nil && time.Since(last.StartedAt) < s.SyncInterval() {
				continue
			}
			if _, err := l.SyncNow(ctx, e.Slug, "schedule"); err != nil && !errors.Is(err, ErrSyncRunning) {
				continue // logged by SyncNow
			}
		}
	}
}
