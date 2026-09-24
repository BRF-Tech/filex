package wasmplugin

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/share"
)

// ── A share of a file the job has not finished writing ─────────────────
//
// `share_create` points a link at a NODE, because that is what a share is: a
// row in `shares` whose `node_id` is NOT NULL and references `nodes`. That is
// what puts the link on the file's own Shares list, under the administrator's
// revoke, and inside the one expiry/PIN/visit machine every link obeys.
//
// A job's OUTPUT has no node while the job runs. `action_run` hands the bytes
// back in the call's spool and `runJob` commits them to the storage
// afterwards, so at the moment the plugin asks, the file filex will end up
// with does not exist yet. The one thing an app most wants to share — the
// document it has just produced — was therefore the one thing it could not,
// and the signing app's delivery mail went out with no link in it.
//
// A plugin may now name one of its OWN outputs. Everything about the link is
// decided at the ask — token, PIN, expiry, caps, subject, state, the exposed
// copies — and handed back at once, so the mail being composed carries the
// real address. The ROW is written when that output is committed and its node
// exists.
//
// ⚠⚠ CREATED LATE, NOT BOUND LATE. The alternative was to write the row at
// once and fill the node in later, which `shares.node_id` cannot hold without
// being made nullable in all three dialects — and a nullable node_id is a
// share that every reader in this codebase (the Shares list, the public
// surface, the no-JS page, the sweeper, the file's own share list) would have
// to learn to distrust, for the sake of a window that lasts seconds. It is
// also the shape that leaves something behind when the job fails: a row in
// **Shares** pointing at nothing, or — in `version` mode, where the target
// node already exists and binding would have been "free" — a LIVE link
// handing the visitor the original, unsigned document. Nothing is written
// here until the bytes are in the catalogue, so a promise the job did not
// keep is a token nothing answers.

// promisedShare is one link a job asked for against a file it is still
// writing. `opts` carries every fact the row needs except NodeID, which is
// the one that does not exist yet.
type promisedShare struct {
	// ref is the scope ref of the output this link is of ("out:0", "eng:2").
	ref string
	// opts is the share as it will be created, minus NodeID. Its Token was
	// minted at the ask and has already been handed to the plugin.
	opts share.CreateOpts
	// stage is the directory holding the copies this link exposes, waiting to
	// be renamed to the share's own <public>/<share id>/. It is made for every
	// link, exactly as the immediate path makes one — a page-less link's is
	// simply empty, because such a link exposes no copies.
	stage string
	// hasPIN feeds the audit row; the PIN itself is in opts and never logged.
	hasPIN bool
}

// promise records a link to be written when ref is committed.
func (s *Scope) promise(p *promisedShare) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.promises = append(s.promises, p)
}

// takePromises removes and returns the promises made against ref, so a
// commit keeps each one exactly once.
func (s *Scope) takePromises(ref string) []*promisedShare {
	s.mu.Lock()
	defer s.mu.Unlock()
	var mine, rest []*promisedShare
	for _, p := range s.promises {
		if p.ref == ref {
			mine = append(mine, p)
		} else {
			rest = append(rest, p)
		}
	}
	s.promises = rest
	return mine
}

// dropPromises abandons what is left — the job failed, the output was never
// named in `outputs`, or the action writes none — and removes the staged
// copies of each. Called from Close, so every exit path takes it.
//
// Nothing has to be un-done in the database: a promise IS the absence of a
// row.
func (s *Scope) dropPromises() {
	s.mu.Lock()
	left := s.promises
	s.promises = nil
	s.mu.Unlock()
	for _, p := range left {
		if p.stage != "" {
			_ = os.RemoveAll(p.stage)
		}
	}
}

// keepPromises writes the rows promised against one committed output.
// nodeID is the catalogue node the bytes landed on.
//
// A failure here is LOUD and does not fail the job: the output is already
// written and the plugin has already handed the link out, so the honest thing
// is to say plainly that a promised link could not be opened rather than to
// mark a conversion that happened as a conversion that did not.
func (r *Registry) keepPromises(ctx context.Context, s *Scope, p *Installed, ref string, nodeID int64) {
	for _, pr := range s.takePromises(ref) {
		pr.opts.NodeID = nodeID
		sh, err := r.opts.Share.Create(ctx, pr.opts)
		if err != nil {
			if pr.stage != "" {
				_ = os.RemoveAll(pr.stage)
			}
			p.log("error", "promised link for "+ref+" could not be opened: "+err.Error())
			r.log.Error("app-plugins: promised share was not written", "plugin", p.Row.Name, "ref", ref, "err", err.Error())
			continue
		}
		if pr.stage != "" {
			if err := os.Rename(pr.stage, r.pageDir(sh.ID)); err != nil {
				_ = os.RemoveAll(pr.stage)
				_ = r.opts.Store.DeleteShare(ctx, sh.ID)
				p.log("error", "promised link for "+ref+" could not keep its copies: "+err.Error())
				r.log.Error("app-plugins: promised share lost its exposed copies", "plugin", p.Row.Name, "ref", ref, "err", err.Error())
				continue
			}
		}
		what := "download link"
		if pr.opts.PageID != "" {
			what = "public page " + pr.opts.PageID
		}
		p.log("info", what+" promised for "+ref+" is open (share "+strconv.FormatInt(sh.ID, 10)+")")
		r.auditShareCreate(ctx, p, sh, nodeID, pr.hasPIN, ref)
	}
}

// auditShareCreate writes the row an administrator reads. Shared by the
// immediate path and the promised one so there is one shape, not two.
//
// ⚠ The share's ID and the file, never the TOKEN: an audit row is read by
// administrators and support, and the token IS the link. Same reasoning as
// share.pin_locked in public_api.go, which records TokenHash(…)[:12].
func (r *Registry) auditShareCreate(ctx context.Context, p *Installed, sh *model.Share, nodeID int64, hasPIN bool, outputRef string) {
	meta := map[string]any{
		"plugin": p.Row.Name, "page": sh.PageID, "node": nodeID,
		"has_pin": hasPIN, "expires_at": sh.ExpiresAt,
	}
	if outputRef != "" {
		// Says WHY this link exists for a file nobody uploaded: the job that
		// produced the file opened it.
		meta["output"] = outputRef
	}
	_ = r.opts.Store.InsertAuditEntry(ctx, &model.AuditEntry{
		UserID: sh.CreatedBy, Action: "app_plugin.share_create", TargetType: "share",
		TargetID: strconv.FormatInt(sh.ID, 10),
		Metadata: meta,
	})
}

// keepPromisedShares is what runJob calls once an output has a node: it
// resolves the node the bytes landed on and keeps every promise against ref.
// A missing node is loud for the same reason keepPromises is.
func (r *Registry) keepPromisedShares(ctx context.Context, s *Scope, p *Installed, storageID int64, ref, rel string) {
	if !s.hasPromises() {
		return
	}
	node, err := r.opts.Store.GetNodeByPath(ctx, storageID, pathkey.Hash(storageID, "/"+strings.Trim(rel, "/")))
	if err != nil || node == nil {
		for range s.takePromises(ref) {
			p.log("error", "promised link for "+ref+" has no file to point at: "+rel+" is not in the catalogue")
			r.log.Error("app-plugins: promised share has no node", "plugin", p.Row.Name, "ref", ref, "path", rel)
		}
		return
	}
	r.keepPromises(ctx, s, p, ref, node.ID)
}

// promiseLock records a lock to be taken when ref is committed. A second
// ask for the same output replaces the first.
func (s *Scope) promiseLock(ref string, l *model.AppPluginLock) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.promisedLocks == nil {
		s.promisedLocks = map[string]*model.AppPluginLock{}
	}
	s.promisedLocks[ref] = l
}

// keepPromisedLock takes the lock promised against one committed output, at
// the path the bytes landed on. Loud, and not a job failure, for the same
// reason a promised share is: the file is written already.
func (r *Registry) keepPromisedLock(ctx context.Context, s *Scope, p *Installed, storageID int64, ref, rel string) {
	s.mu.Lock()
	l := s.promisedLocks[ref]
	delete(s.promisedLocks, ref)
	s.mu.Unlock()
	if l == nil {
		return
	}
	l.Rel = strings.Trim(rel, "/")
	l.PathHash = pathkey.Hash(storageID, "/"+l.Rel)
	l.StorageID = storageID
	if cur, err := r.opts.Store.GetAppPluginLock(ctx, storageID, l.PathHash); err == nil && cur != nil && cur.Live(time.Now()) && cur.PluginID != p.Row.ID {
		p.log("error", "promised lock for "+ref+" was not taken: "+l.Rel+" is locked by app "+cur.PluginName)
		return
	}
	if err := r.opts.Store.PutAppPluginLock(ctx, l); err != nil {
		p.log("error", "promised lock for "+ref+" could not be taken: "+err.Error())
		r.log.Error("app-plugins: promised lock was not taken", "plugin", p.Row.Name, "ref", ref, "err", err.Error())
		return
	}
	p.log("info", "locked "+l.Rel+" "+lockWords(l.Until)+" (promised for "+ref+")")
}

func (s *Scope) hasPromises() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.promises) > 0
}
