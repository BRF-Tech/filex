package onlyoffice

// Who a save written beside a document is written for (filex 0.51,
// callback_format.go), and whether they may (the maintainer's decision,
// 2026-10-04: the new file is created only for somebody who may create files
// in that folder).
//
// # Who
//
// The callback has no signed-in person; it names the session's editors in
// `users`. Those ids are filex's own: the document server only knows the user
// ids filex put in the editor configs it SIGNED, one per person it handed an
// editing session (BuildConfigForNode). Two things hold the callback to that:
//
//   - the ids are read from the callback's verified token, never from its
//     body (a body can be changed beside a token it was not built from);
//   - filex records, per document key, every person it handed an EDIT config
//     (noteOpener), and keeps only those of the callback's editors. A person
//     who only viewed it, or an id the session never had, does not count.
//     The record is this process's memory: after a restart it is empty, and
//     the signed ids are taken as they are - they still name only people
//     filex handed a config for this document.
//
// # May they
//
// A save is written beside the document for the FIRST of those editors who
// may create that file there, checked now, at the save, against the file's
// own name: their account is there and on, their tenant reaches the storage,
// and the permissions resolve to files.create on the path (role, folder
// exceptions, grants, a blocked file type, an app's lock; acl.Set.Can - what
// every door that creates a file asks). Nobody may: the save is not written,
// and the editors are told why, in their language
// (server.onlyoffice.refused_no_create). The document's folder is inside the
// root the person opened the document under, so a root an API key is
// confined to holds for the new file as well.

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

// openerTTL is how long a record of who opened a session is kept.
const openerTTL = 48 * time.Hour

// openersMax bounds the record (sessions, not people).
const openersMax = 4096

// noteOpener records that uid was handed an editing session of the document
// with this key.
func (s *Service) noteOpener(key string, uid int64) {
	if key == "" || uid <= 0 {
		return
	}
	now := time.Now()
	s.openersMu.Lock()
	defer s.openersMu.Unlock()
	if s.openers == nil {
		s.openers = map[string]map[int64]time.Time{}
	}
	if len(s.openers) >= openersMax {
		for k, m := range s.openers {
			for id, at := range m {
				if now.Sub(at) > openerTTL {
					delete(m, id)
				}
			}
			if len(m) == 0 {
				delete(s.openers, k)
			}
		}
	}
	m := s.openers[key]
	if m == nil {
		m = map[int64]time.Time{}
		s.openers[key] = m
	}
	m[uid] = now
}

// openersOf is who was handed an editing session of key (in this process),
// and whether there is a record at all.
func (s *Service) openersOf(key string) (map[int64]bool, bool) {
	s.openersMu.Lock()
	defer s.openersMu.Unlock()
	m, ok := s.openers[key]
	if !ok || len(m) == 0 {
		return nil, false
	}
	out := make(map[int64]bool, len(m))
	for id, at := range m {
		if time.Since(at) <= openerTTL {
			out[id] = true
		}
	}
	return out, len(out) > 0
}

// claimUsers is the `users` of a verified callback token: in its payload
// itself (the body token) or wrapped in {"payload": …} (the header token).
func claimUsers(claims map[string]any) ([]string, bool) {
	if claims == nil {
		return nil, false
	}
	if inner, ok := claims["payload"].(map[string]any); ok {
		claims = inner
	}
	raw, ok := claims["users"].([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		switch u := v.(type) {
		case string:
			out = append(out, u)
		case float64:
			out = append(out, strconv.FormatInt(int64(u), 10))
		}
	}
	return out, true
}

// sessionEditors are the callback's editors who were handed an editing
// session of this document (see the header), in the callback's order.
func (s *Service) sessionEditors(key string, users []string) []int64 {
	ids := editorIDs(users)
	opened, recorded := s.openersOf(key)
	if !recorded {
		return ids
	}
	var out []int64
	for _, id := range ids {
		if opened[id] {
			out = append(out, id)
		}
	}
	return out
}

// mayCreate reports whether the person may create the file at rel on the
// document's storage now (see the header).
func (s *Service) mayCreate(ctx context.Context, node *model.Node, uid int64, rel string) bool {
	return s.mayDo(ctx, node, uid, rel, perm.FilesCreate)
}

// mayDo reports whether the person may do p at rel on the document's storage
// now: their account is there and may still sign in (auth.LoginAllowed: on,
// its tenant not suspended - the rule a ticket's redeem and a public link
// follow too), their tenant still reaches the storage (db.AccountReaches: a
// person of a tenant reaches only its storages, a person of the platform's
// own tenant or of a single-tenant install every storage), and their
// permissions resolve to p there.
func (s *Service) mayDo(ctx context.Context, node *model.Node, uid int64, rel string, p perm.Perm) bool {
	if s.Store == nil {
		return false
	}
	u, err := s.Store.GetUser(ctx, uid)
	if err != nil || u == nil || !auth.LoginAllowed(ctx, s.Store, s.multiTenant, u) {
		return false
	}
	st, err := s.Store.GetStorage(ctx, node.StorageID)
	if err != nil || st == nil {
		return false
	}
	if !db.AccountReaches(ctx, s.Store, u, st.ID) {
		return false
	}
	set, err := s.aclResolver().LoadSet(ctx, u, st)
	if err != nil || set == nil {
		return false
	}
	return set.Can(rel, p)
}

// anEditorMayChange reports whether one of the save's editors (sessionEditors)
// may still change the document itself, asked at the save: the callback acts
// for them, and nothing else stands behind it. Before 0.59 a save over the
// document asked nobody, so an editor left open while the platform operator
// moved the storage to another tenant - or while the person's access was
// taken away - went on writing into it.
func (s *Service) anEditorMayChange(ctx context.Context, node *model.Node, editors []int64) bool {
	for _, uid := range editors {
		if s.mayDo(ctx, node, uid, node.Path, perm.FilesModify) {
			return true
		}
	}
	return false
}

// keyOf is the document key a callback names.
func keyOf(p CallbackPayload) string { return strings.TrimSpace(p.Key) }
