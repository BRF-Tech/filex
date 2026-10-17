package handlers

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/protoperm"
	"github.com/brf-tech/filex/backend/internal/writegate"
)

// packRefusal is packSourceRefusal's permission refusal, carrying the
// verdict so each door can say WHY in its own wire shape: the explorer's
// structured permission_denied body, the AI surface's errAIForbidden.
type packRefusal struct{ v permVerdict }

func (e *packRefusal) Error() string { return "insufficient permission" }

// packSourceRefusal is THE rule for packing a selected item into an archive,
// whichever door packs it: POST /api/files/archive/create, POST
// /api/files/archive/add (each file it adds), and the AI surface's
// server-side zip (POST /api/ai/zip, the MCP file_zip tool). What is inside a
// selected folder answers memberRule, below.
//
// Packing takes the item's bytes - the archive can be downloaded or shared a
// moment later - so it asks what a download asks: never one of filex's own
// names (writegate.Names), and files.download on the item (acl.NeedLevel:
// viewer and up).
//
// ⚠⚠ One function so there is one rule. The AI zip asked only "may the
// caller see it" (resolveStorage, viewer) until v0.50: an account whose
// files.download was taken away, with create rights in some folder, packed
// what it could not download with file_zip and fetched the archive (task
// #112). A new door that packs files calls this and writes no rule of its
// own.
//
// It returns nil when the item may be packed, a writegate error, or a
// *packRefusal.
func packSourceRefusal(ctx context.Context, resolver *acl.Resolver, store db.Store, storageID int64, rel string) error {
	// An unwired resolver (a handler built by hand in a test) is "nothing
	// locked" to writegate - and must reach it as a nil interface, not as an
	// interface holding a nil pointer.
	var locks writegate.Locks
	if resolver != nil {
		locks = resolver.Locks(ctx, storageID)
	}
	if err := writegate.Check(locks, 0, writegate.Names(rel)); err != nil {
		return err
	}
	if v := aclCanID(ctx, resolver, store, storageID, rel, perm.FilesDownload); !v.ok {
		return &packRefusal{v: v}
	}
	return nil
}

// refusePackSource is packSourceRefusal for an explorer door, with the answer
// written: the gate's (a lock, one of filex's own names), the structured
// permission refusal, or a bare 403 naming raw. It reports true when it
// refused - archive/create for each selected item, archive/add for each file
// it packs.
func refusePackSource(w http.ResponseWriter, r *http.Request, resolver *acl.Resolver, store db.Store, storageID int64, rel, raw string) bool {
	err := packSourceRefusal(r.Context(), resolver, store, storageID, rel)
	if err == nil {
		return false
	}
	var pr *packRefusal
	if !errors.As(err, &pr) {
		answerGate(w, r, err)
		return true
	}
	if !pr.v.WritePerm(w, r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission: " + raw})
	}
	return true
}

// memberRule is THE rule for every FILE whose bytes a door takes from below
// the item the caller (or a public link's creator) named: each member of a
// folder archive - the selection download and its /z/ ticket, archive/create,
// the AI zip and the MCP file_zip tool, a folder link's ZIP - and each file a
// folder link serves.
//
// It asks of the member's own path what a download of that one file asks,
// protoperm.CanRead - the folder level it needs (capped by the account role)
// and files.download there, a custom role's part that differs in some folders
// or for some names included - and that the path is inside the caller's
// tenant and the token's `root:` folder.
//
// ⚠⚠ packSourceRefusal passing on the selected folder says nothing about what
// is in it: a role can say no to files.download on one file, or one
// subfolder, inside a folder the caller may download. A door that walks a
// folder asks this of every file it reads, and writes no rule of its own.
//
// A member it refuses is left out, as a member the caller cannot reach at
// all always was: the folder is still the caller's to download, without what
// the rule keeps. The selected item itself is refused outright, as before
// (packSourceRefusal).
//
// One value serves one request, one ticket redeem or one job: it loads the
// caller's set once per storage. A storage or set that cannot be loaded
// refuses every member there. A nil resolver (a handler built by hand in a
// test) enforces nothing, like every other check here.
type memberRule struct {
	resolver *acl.Resolver
	store    db.Store
	mu       sync.Mutex
	sets     map[int64]memberSet
}

// memberSet is the caller's loaded set on one storage; both nil when it could
// not be loaded.
type memberSet struct {
	st  *model.Storage
	set *acl.Set
}

func newMemberRule(resolver *acl.Resolver, store db.Store) *memberRule {
	return &memberRule{resolver: resolver, store: store, sets: map[int64]memberSet{}}
}

// allows reports whether the caller on ctx may have the bytes of rel on
// storageID.
func (m *memberRule) allows(ctx context.Context, storageID int64, rel string) bool {
	if m == nil || m.resolver == nil {
		return true
	}
	if scope, confined := confinedScope(ctx); confined && !scope.CanAccessStorage(storageID) {
		return false
	}
	ms := m.load(ctx, storageID)
	if ms.st == nil || ms.set == nil {
		return false
	}
	rel = acl.CleanRel(rel)
	if root, confined := callerRoot(ctx); confined && !root.Within(ms.st.Name, rel) {
		return false
	}
	return protoperm.CanRead(ms.set, rel)
}

func (m *memberRule) load(ctx context.Context, storageID int64) memberSet {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ms, ok := m.sets[storageID]; ok {
		return ms
	}
	var ms memberSet
	if m.store != nil {
		if st, err := m.store.GetStorage(ctx, storageID); err == nil && st != nil {
			if set, err := m.resolver.LoadSet(ctx, auth.UserFrom(ctx), st); err == nil && set != nil {
				ms = memberSet{st: st, set: set}
			}
		}
	}
	m.sets[storageID] = ms
	return ms
}
