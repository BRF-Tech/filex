package ops_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/ops"
)

// UniqueDestNumbered is where a draft is saved beside a file that already has
// its name (issue #71): `notes (2).txt`, the numbering the New document dialog
// suggests — never `notes-copy.txt`, which would call a new document a copy of
// the one it happened to share a name with. Same rules as UniqueDest, one
// function underneath (ops.uniqueDest), only the spelling differs.

func TestUniqueDestNumbered_FreeNameIsKept(t *testing.T) {
	f := newOpsFixture(t)
	got, err := ops.UniqueDestNumbered(context.Background(), f.drv, "docs/notes.txt")
	require.NoError(t, err)
	require.Equal(t, "docs/notes.txt", got)
}

func TestUniqueDestNumbered_TakenNameGetsTheNextNumber(t *testing.T) {
	f := newOpsFixture(t)
	f.seedDir(t, "docs")
	f.seedFile(t, "docs/notes.txt", "already here")
	got, err := ops.UniqueDestNumbered(context.Background(), f.drv, "docs/notes.txt")
	require.NoError(t, err)
	require.Equal(t, "docs/notes (2).txt", got)

	f.seedFile(t, "docs/notes (2).txt", "and this one")
	got, err = ops.UniqueDestNumbered(context.Background(), f.drv, "docs/notes.txt")
	require.NoError(t, err)
	require.Equal(t, "docs/notes (3).txt", got)

	// A name with no extension is numbered at its end.
	f.seedFile(t, "docs/LICENSE", "x")
	got, err = ops.UniqueDestNumbered(context.Background(), f.drv, "docs/LICENSE")
	require.NoError(t, err)
	require.Equal(t, "docs/LICENSE (2)", got)
}

func TestUniqueDestNumbered_HonoursTheCatalogue(t *testing.T) {
	// A catalogue row whose bytes went missing still holds its name — the
	// same Taken hook UniqueDest takes.
	f := newOpsFixture(t)
	taken := func(rel string) bool { return strings.Trim(rel, "/") == "notes.txt" }
	got, err := ops.UniqueDestNumbered(context.Background(), f.drv, "notes.txt", taken)
	require.NoError(t, err)
	require.Equal(t, "notes (2).txt", got)
}

func TestUniqueDestNumbered_SaturatedIsAnError(t *testing.T) {
	f := newOpsFixture(t)
	taken := func(rel string) bool { return true }
	_, err := ops.UniqueDestNumbered(context.Background(), f.drv, "notes.txt", taken)
	require.True(t, errors.Is(err, ops.ErrNoFreeName), "got %v", err)
}

func TestUniqueDest_StillSpellsACopy(t *testing.T) {
	// The refactor that gave the numbered spelling a home must not have moved
	// the copy's.
	f := newOpsFixture(t)
	f.seedFile(t, "a.txt", "x")
	for i := 0; i < 2; i++ {
		got, err := ops.UniqueDest(context.Background(), f.drv, "a.txt")
		require.NoError(t, err)
		want := "a-copy.txt"
		if i == 1 {
			want = "a-copy-2.txt"
		}
		require.Equal(t, want, got)
		f.seedFile(t, got, fmt.Sprint(i))
	}
}
