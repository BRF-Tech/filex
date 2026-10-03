package authsetup

import (
	"context"
	"errors"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
)

// directoryOf arranges the running directories (LDAP, the operating-system
// providers) into the one Directory the file protocols consult: none, the one,
// or a chain that asks each in the order the providers were added.
func directoryOf(dirs []Directory) Directory {
	switch len(dirs) {
	case 0:
		return nil
	case 1:
		return dirs[0]
	}
	return directoryChain(dirs)
}

// directoryChain asks every directory in turn and returns the first account
// that one of them vouches for.
//
// A directory that answers ErrUnauthorized has judged and said no; the next
// one is asked. Any other error means that directory could not judge at all —
// remembered and returned only if no later one succeeds, so the protocol layer
// (which logs anything that is not ErrUnauthorized) shows the operator that a
// directory is down rather than that a password was wrong.
type directoryChain []Directory

func (c directoryChain) VerifyPassword(ctx context.Context, identifier, password string) (*model.User, error) {
	var last error = auth.ErrUnauthorized
	for _, d := range c {
		u, err := d.VerifyPassword(ctx, identifier, password)
		if err == nil && u != nil {
			return u, nil
		}
		if err != nil && !errors.Is(err, auth.ErrUnauthorized) {
			last = err
		}
	}
	return nil, last
}
