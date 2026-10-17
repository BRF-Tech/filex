package mountfs

import (
	"fmt"
	"testing"

	"github.com/brf-tech/filex/backend/internal/cliclient"
)

// A server that refuses a NAME says so with 400 (internal/leafname: a
// backslash, `...`) or with its too-long code; a mounted drive turns those
// into EINVAL and ENAMETOOLONG - what a file manager shows as "invalid
// argument" and "file name too long" - not EIO, which reads as a broken disk
// (task #238).
func TestClassify_NameRefusals(t *testing.T) {
	cases := []struct {
		err  error
		want ErrKind
	}{
		{&cliclient.APIError{Status: 400, Code: "bad new name"}, ErrBadName},
		{fmt.Errorf("rename: %w", &cliclient.APIError{Status: 400}), ErrBadName},
		{&cliclient.APIError{Status: 400, Code: "NAME_TOO_LONG"}, ErrNameTooLong},
		{&cliclient.APIError{Status: 400, Code: "name_too_long"}, ErrNameTooLong},
		{&cliclient.APIError{Status: 414}, ErrNameTooLong},
		// What was there before stays as it was.
		{&cliclient.APIError{Status: 403}, ErrDenied},
		{&cliclient.APIError{Status: 409}, ErrExists},
		{&cliclient.APIError{Status: 507}, ErrNoSpace},
		{&cliclient.APIError{Status: 500}, ErrIO},
	}
	for _, c := range cases {
		if got := Classify(c.err); got != c.want {
			t.Errorf("Classify(%v) = %d, want %d", c.err, got, c.want)
		}
	}
}
