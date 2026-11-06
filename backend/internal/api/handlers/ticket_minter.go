package handlers

// Who a credential-free ticket acts for, asked again when it is used.
//
// A ticket URL carries no credential: the /z/ download links and archives
// (archive_download.go, download_link.go) and the /u/ upload tickets
// (upload_ticket.go) were each decided by an authenticated mint, and the
// request that redeems one is anonymous. What the mint decided holds only
// while the account that minted it still stands where it stood, so every
// redeem re-reads that account here, and runs as it with the scope its tenant
// has NOW, before it reads or writes a byte. One function, so a redeem that
// asks less than the others cannot be written by accident.

import (
	"context"
	"time"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/tenant"
)

// ticketMinter re-reads the account a ticket acts for, and the API token it
// was minted with (0 for a session, or for a ticket that does not follow its
// token). reason is "" when both still stand, otherwise the audit word for
// what fell: "account" (gone, or may no longer sign in: disabled, its tenant
// suspended, maintenance mode) or "token" (revoked, expired, or no longer the
// owner's).
func ticketMinter(ctx context.Context, store db.Store, multiTenant bool, ownerID, tokenID int64) (*model.User, *model.APIToken, string) {
	if store == nil || ownerID <= 0 {
		return nil, nil, "account"
	}
	owner, err := store.GetUser(ctx, ownerID)
	if err != nil || owner == nil || !auth.LoginAllowed(ctx, store, multiTenant, owner) {
		return nil, nil, "account"
	}
	var token *model.APIToken
	if tokenID != 0 {
		token, err = store.GetAPITokenByID(ctx, tokenID)
		if err != nil || token == nil || token.UserID != owner.ID ||
			(token.ExpiresAt != nil && !token.ExpiresAt.After(time.Now())) {
			return nil, nil, "token"
		}
	}
	return owner, token, ""
}

// asTicketMinter is ctx as the ticket's minter: their account, the token they
// minted it with (its `root:` folder and verbs) when there is one and, on a
// multi-tenant install, their tenant's scope as it is now - the context the
// explorer's own requests run with. The scope is what confines the storage
// listings (tenantstore) and ownsStorage to the storages the tenant still has.
func asTicketMinter(ctx context.Context, store db.Store, multiTenant bool, owner *model.User, token *model.APIToken) context.Context {
	uctx := auth.WithUser(ctx, owner)
	if token != nil {
		uctx = auth.WithToken(uctx, token)
	}
	if multiTenant {
		uctx = tenant.WithScope(uctx, auth.ScopeForUser(uctx, store, owner))
	}
	return uctx
}
