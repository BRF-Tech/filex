package handlers

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// errWrongDoor: the token names a live link of the other kind - a file
// request at a read door, a download link or an app's page at an upload
// door. A door answers it like a token that does not exist, unless it has a
// reason of its own to say more (the /d/ door names a download link: whoever
// holds one can open it at /s/ anyway).
var errWrongDoor = errors.New("this link does not open here")

// linkAt resolves token to the link it names, for one public door, and is
// the only way a public door looks a token up (TestLinkAt_IsTheOnlyPublicLookup).
//
// ⚠⚠ The kind is decided HERE, by model.Share.Opens, and not by each door:
// every door used to ask its own question, the /s/ page asked only
// "is this an app's page?", and a file request's token opened at /s/ listed
// the folder it collects into, zipped it and streamed its files
// (GHSA-rcrp-r83c-g8hv). A door that resolves a token by any other way is a
// door that may forget.
//
// The token is matched as the client meant it (lower case, no surrounding
// space): tokens are minted lower-case hex, and the doors used to disagree
// about that too.
//
// Returns the link, or an error: the lookup's own (an unknown token, a link
// whose storage left its tenant: db.ShareStands) or errWrongDoor. On an error
// the link is nil, so no door can go on with a link of the wrong kind.
func linkAt(ctx context.Context, store db.Store, token string, door model.LinkDoor) (*model.Share, error) {
	if store == nil {
		return nil, sql.ErrNoRows
	}
	sh, err := store.GetShareByToken(ctx, strings.ToLower(strings.TrimSpace(token)))
	switch {
	case err != nil:
		return nil, err
	case sh == nil:
		return nil, sql.ErrNoRows
	case !sh.Opens(door):
		return nil, errWrongDoor
	}
	return sh, nil
}
