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

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/srvtext"
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
	if s.Store == nil {
		return false
	}
	u, err := s.Store.GetUser(ctx, uid)
	if err != nil || u == nil || !u.Enabled {
		return false
	}
	st, err := s.Store.GetStorage(ctx, node.StorageID)
	if err != nil || st == nil {
		return false
	}
	// The tenant boundary: a person of a tenant reaches only its storages; a
	// person of the platform's own tenant (or of a single-tenant install)
	// reaches every storage.
	if u.ProviderID != nil && *u.ProviderID > 0 {
		p, err := s.Store.GetProvider(ctx, *u.ProviderID)
		if err != nil || p == nil {
			return false
		}
		if !p.IsSupertenant {
			ids, err := s.Store.ListProviderStorageIDs(ctx, p.ID)
			if err != nil {
				return false
			}
			found := false
			for _, id := range ids {
				if id == st.ID {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	set, err := s.aclResolver().LoadSet(ctx, u, st)
	if err != nil || set == nil {
		return false
	}
	return set.Can(rel, perm.FilesCreate)
}

// creatorOf is the session's editors, the first of them who may create the
// file at rel leading; why says why nobody may (no editor the session had, or
// none of them may create files there).
func (s *Service) creatorOf(ctx context.Context, node *model.Node, key string, users []string, rel string, got string) (editors []int64, why *errNotWritten) {
	editors = s.sessionEditors(key, users)
	vars := srvtext.Vars{"ext": docExt(node.Name), "format": formatName(got)}
	if len(editors) == 0 {
		return nil, notWritten(refusedNoEditor, nil)
	}
	for i, id := range editors {
		if s.mayCreate(ctx, node, id, rel) {
			if i > 0 {
				// The one it is written for leads: the audit row names them.
				ordered := append([]int64{id}, editors[:i]...)
				editors = append(ordered, editors[i+1:]...)
			}
			return editors, nil
		}
	}
	return editors, notWritten(refusedNoCreate, vars)
}

// keyOf is the document key a callback names.
func keyOf(p CallbackPayload) string { return strings.TrimSpace(p.Key) }
