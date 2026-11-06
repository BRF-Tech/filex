package db

import (
	"context"
	"slices"

	"github.com/brf-tech/filex/backend/internal/model"
)

// A credential-free URL that names an item - a public link, a signed
// thumbnail, the document editor's fetch URL - carries the TENANT the item's
// storage was in when the URL was made: its stamp. The platform operator may
// move a storage to another tenant (unlink it from one, link it to another),
// and a URL made for one customer's data must not go on serving, or taking
// writes into, what is another customer's now.
//
// The two halves live here, so every such URL asks the one question:
//
//   - StorageStamp, when the URL is made: the storage's first tenant by
//     provider id (GetProviderIDForStorage), 0 for a storage in none.
//   - StampStands, when the URL is used: does that tenant still have the
//     storage? (ShareStands asks it of a link's row.)
//
// ⚠ Only a CUSTOMER tenant holds a URL to its storage. A stamp of 0 (the
// storage was in no tenant, as on a single-tenant install) or of the
// platform's own tenant (the supertenant) is not held to it: the platform's
// data handed to a customer is the operator's own move, and holding it would
// close every link of an install the day its storages are first given to
// tenants. A storage shared by two tenants keeps its URLs while the stamped
// one still has it, and a URL whose storage comes back answers again.

// StampReader is what the two halves read.
type StampReader interface {
	GetProviderIDForStorage(ctx context.Context, storageID int64) (int64, bool, error)
	GetProvider(ctx context.Context, id int64) (*model.Provider, error)
	ListProviderStorageIDs(ctx context.Context, providerID int64) ([]int64, error)
}

// StorageStamp is the tenant a URL made now for an item on storageID
// carries: the storage's first tenant by provider id, 0 when it is in none.
// An error is the caller's to refuse on: a URL stamped by a failed read would
// be held to nothing.
func StorageStamp(ctx context.Context, st StampReader, storageID int64) (int64, error) {
	pid, ok, err := st.GetProviderIDForStorage(ctx, storageID)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, nil
	}
	return pid, nil
}

// StampStands reports whether a URL stamped with tenant stamp may still be
// served for an item on storageID: no customer stamp, or the stamped tenant
// still has the storage. A tenant that is gone, or cannot be read, has
// nothing any more: the URL is not served.
func StampStands(ctx context.Context, st StampReader, stamp, storageID int64) bool {
	if stamp <= 0 {
		return true
	}
	p, err := st.GetProvider(ctx, stamp)
	if err != nil || p == nil {
		return false
	}
	if p.IsSupertenant {
		return true
	}
	ids, err := st.ListProviderStorageIDs(ctx, p.ID)
	if err != nil {
		return false
	}
	return slices.Contains(ids, storageID)
}

// AccountReaches reports whether account u's tenant, as the rows say NOW,
// reaches storageID: an account of a customer tenant reaches only the
// storages linked to it; one of the platform's own tenant (the supertenant),
// or with no tenant at all (a single-tenant install's), reaches every
// storage. It is the question a door with no request scope asks for the
// account it acts for, at the moment it acts - a queued job when it runs, an
// app's result when it is written, an editor's save when it lands - because
// the platform operator may have moved the storage to another tenant since
// the account was let in. A tenant that cannot be read reaches nothing.
func AccountReaches(ctx context.Context, st StampReader, u *model.User, storageID int64) bool {
	if u == nil {
		return false
	}
	if u.ProviderID == nil || *u.ProviderID <= 0 {
		return true
	}
	p, err := st.GetProvider(ctx, *u.ProviderID)
	if err != nil || p == nil {
		return false
	}
	if p.IsSupertenant {
		return true
	}
	ids, err := st.ListProviderStorageIDs(ctx, p.ID)
	if err != nil {
		return false
	}
	return slices.Contains(ids, storageID)
}

// ShareStampReader is StampReader plus the node a link names.
type ShareStampReader interface {
	StampReader
	GetNode(ctx context.Context, id int64) (*model.Node, error)
}

// ShareStamp is the stamp every engine's CreateShare writes on a new link to
// nodeID: the tenant the node's storage is in now (StorageStamp).
func ShareStamp(ctx context.Context, st ShareStampReader, nodeID int64) (int64, error) {
	n, err := st.GetNode(ctx, nodeID)
	if err != nil {
		return 0, err
	}
	if n == nil {
		return 0, nil
	}
	return StorageStamp(ctx, st, n.StorageID)
}

// ShareStands is StampStands for a link's row (model.Share.TenantID, stamped
// by CreateShare). Every engine's GetShareByToken asks it, so a link whose
// tenant gave its storage away answers like a token that does not exist,
// whichever door looks it up (a link whose item is gone, or cannot be read,
// answers the same).
func ShareStands(ctx context.Context, st ShareStampReader, sh *model.Share) bool {
	if sh == nil || sh.TenantID <= 0 {
		return true
	}
	n, err := st.GetNode(ctx, sh.NodeID)
	if err != nil || n == nil {
		return false
	}
	return StampStands(ctx, st, sh.TenantID, n.StorageID)
}
