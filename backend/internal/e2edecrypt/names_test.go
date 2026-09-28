package e2edecrypt

import (
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/text/unicode/norm"

	"github.com/brf-tech/filex/backend/internal/e2e"
)

func vectorKey(t *testing.T) (*NameKey, nameVectors) {
	t.Helper()
	v := loadVectors(t)
	root, ok := b64urlDecode(v.RootID)
	require.True(t, ok)
	k, err := NewNameKey(unhex(t, v.KeyHex), v.Long, root)
	require.NoError(t, err)
	return k, v
}

func mustID(t *testing.T, s string) []byte {
	t.Helper()
	id := dirID(s)
	require.NotNil(t, id, s)
	return id
}

func TestNames_DerivesEveryFolderIDLikeTheReference(t *testing.T) {
	k, v := vectorKey(t)
	require.Equal(t, dirIDLabel, v.DirIDLabel)
	for _, vec := range v.Vectors {
		if !vec.Dir {
			continue
		}
		got := k.DeriveDirID(mustID(t, vec.ParentID), vec.Name)
		require.Equal(t, vec.DirID, b64urlEncode(got), vec.Name)
	}
}

func TestNames_SpellsEveryVectorLikeTheReference(t *testing.T) {
	k, v := vectorKey(t)
	for _, vec := range v.Vectors {
		var id []byte
		if vec.Dir {
			id = mustID(t, vec.DirID)
		}
		got, err := k.EncryptName(vec.Name, mustID(t, vec.ParentID), id)
		require.NoError(t, err, vec.Name)
		require.Equal(t, vec.Encoded, got.Encoded, vec.Name)
		require.Equal(t, vec.Stored, got.Stored, vec.Name)
		require.Equal(t, vec.Sidecar, got.SidecarName, vec.Name)
	}
}

func TestNames_ReadsEveryVectorBackInItsFolder(t *testing.T) {
	k, v := vectorKey(t)
	for _, vec := range v.Vectors {
		vec := vec
		plain, state := k.DecryptStoredName(vec.Stored, mustID(t, vec.ParentID), func(name string) ([]byte, bool) {
			if name == vec.Sidecar {
				return []byte(vec.Encoded + "\n"), true
			}
			return nil, false
		})
		require.Equal(t, NameDecrypted, state, vec.Name)
		require.Equal(t, vec.Name, plain)
		if vec.Dir {
			require.Equal(t, vec.DirID, b64urlEncode(DirIDOf(vec.Stored)), vec.Name)
		} else {
			require.Nil(t, DirIDOf(vec.Stored), vec.Name)
		}
	}
}

func TestNames_SameNameInTwoFoldersIsTwoStoredNames(t *testing.T) {
	k, v := vectorKey(t)
	var faturas []string
	var parents []string
	for _, vec := range v.Vectors {
		if vec.Name == "fatura.pdf" {
			faturas = append(faturas, vec.Stored)
			parents = append(parents, vec.ParentID)
		}
	}
	require.Len(t, faturas, 2)
	require.NotEqual(t, faturas[0], faturas[1])
	// Found in the other folder, it does not open there — and it is a file
	// name, so it reads as a plaintext name that looks like base64url…
	p, st := k.DecryptStoredName(faturas[0], mustID(t, parents[1]), nil)
	require.Equal(t, NamePlain, st)
	require.Equal(t, faturas[0], p)
	// …which the walk then recovers, knowing every folder id.
	p, ok := k.RecoverMoved(faturas[0], mustID(t, parents[1]), [][]byte{mustID(t, parents[0]), mustID(t, parents[1])}, nil)
	require.True(t, ok)
	require.Equal(t, "fatura.pdf", p)
}

func TestNames_AFolderNameIsOursByItsSpelling(t *testing.T) {
	k, v := vectorKey(t)
	for _, vec := range v.Vectors {
		if vec.Name != "2024" {
			continue
		}
		// In the root instead of in Sözleşmeler: unreadable, not plain.
		_, st := k.DecryptStoredName(vec.Stored, k.RootID, nil)
		require.Equal(t, NameUnreadable, st)
		return
	}
	t.Fatal("no 2024 vector")
}

func TestNames_EffectiveDirIDIsTheSameBeforeAndAfterTheRename(t *testing.T) {
	k, v := vectorKey(t)
	for _, vec := range v.Vectors {
		if vec.Name != "Sözleşmeler" {
			continue
		}
		require.Equal(t, vec.DirID, b64urlEncode(k.EffectiveDirID(k.RootID, "Sözleşmeler")))
		require.Equal(t, vec.DirID, b64urlEncode(k.EffectiveDirID(k.RootID, vec.Stored)))
		return
	}
	t.Fatal("no Sözleşmeler vector")
}

func TestNames_NFDInputIsTheSameName(t *testing.T) {
	k, v := vectorKey(t)
	for _, vec := range v.Vectors {
		var id []byte
		if vec.Dir {
			id = mustID(t, vec.DirID)
		}
		got, err := k.EncryptName(norm.NFD.String(vec.Name), mustID(t, vec.ParentID), id)
		require.NoError(t, err)
		require.Equal(t, vec.Stored, got.Stored, vec.Name)
	}
}

func TestNames_ClassifiesEverySpelling(t *testing.T) {
	_, v := vectorKey(t)
	for _, vec := range v.Vectors {
		c := ClassifyStoredName(vec.Stored)
		switch {
		case vec.Dir && vec.Sidecar != "":
			require.Equal(t, KindLongDir, c.Kind, vec.Name)
		case vec.Dir:
			require.Equal(t, KindDir, c.Kind, vec.Name)
		case vec.Sidecar != "":
			require.Equal(t, KindLong, c.Kind, vec.Name)
		default:
			require.Equal(t, KindFile, c.Kind, vec.Name)
		}
		if vec.Sidecar != "" {
			side, ok := SidecarNameFor(vec.Stored)
			require.True(t, ok)
			require.Equal(t, vec.Sidecar, side)
			require.Equal(t, KindSidecar, ClassifyStoredName(vec.Sidecar).Kind)
		}
	}
	require.Equal(t, KindPlain, ClassifyStoredName("notes.txt").Kind)
	// A dot and 22 characters that are not a canonical 16-byte id: not a folder name.
	require.NotEqual(t, KindDir, ClassifyStoredName("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAA.BBBBBBBBBBBBBBBBBBBBBB").Kind)
}

func TestNames_PlainAndUnreadable(t *testing.T) {
	k, v := vectorKey(t)

	// Valid base64url, long enough to be a candidate, not ours → plaintext.
	plain, state := k.DecryptStoredName("ReadMe_2024-final-version", k.RootID, nil)
	require.Equal(t, NamePlain, state)
	require.Equal(t, "ReadMe_2024-final-version", plain)
	plain, state = k.DecryptStoredName("notes.txt", k.RootID, nil)
	require.Equal(t, NamePlain, state)
	require.Equal(t, "notes.txt", plain)

	var long, other struct{ Stored, Encoded, Sidecar, Parent string }
	for _, vec := range v.Vectors {
		if vec.Sidecar == "" || vec.Dir {
			continue
		}
		if long.Stored == "" {
			long = struct{ Stored, Encoded, Sidecar, Parent string }{vec.Stored, vec.Encoded, vec.Sidecar, vec.ParentID}
		} else {
			other = struct{ Stored, Encoded, Sidecar, Parent string }{vec.Stored, vec.Encoded, vec.Sidecar, vec.ParentID}
		}
	}
	require.NotEmpty(t, other.Stored)
	parent := mustID(t, long.Parent)

	_, state = k.DecryptStoredName(long.Stored, parent, func(string) ([]byte, bool) { return nil, false })
	require.Equal(t, NameUnreadable, state, "missing sidecar")
	_, state = k.DecryptStoredName(long.Stored, parent, func(string) ([]byte, bool) { return []byte(other.Encoded), true })
	require.Equal(t, NameUnreadable, state, "another item's sidecar")
	_, state = k.DecryptStoredName(long.Sidecar, parent, nil)
	require.Equal(t, NameSidecar, state)
}

func TestNames_AnotherFoldersKeyReadsPlaintext(t *testing.T) {
	k, v := vectorKey(t)
	other, err := NewNameKey(make([]byte, 64), v.Long, k.RootID)
	require.NoError(t, err)
	plain, state := other.DecryptStoredName(v.Vectors[1].Stored, k.RootID, nil)
	require.Equal(t, NamePlain, state)
	require.Equal(t, v.Vectors[1].Stored, plain)
}

func TestNames_StrictBase64url(t *testing.T) {
	b, ok := b64urlDecode("-_8B")
	require.True(t, ok)
	require.Equal(t, []byte{0xfb, 0xff, 0x01}, b)
	for _, bad := range []string{"", "-_8B=", "+/8B", "AB", "A"} {
		_, ok := b64urlDecode(bad)
		require.False(t, ok, bad)
	}
	b, ok = b64urlDecode("AA")
	require.True(t, ok)
	require.Equal(t, []byte{0}, b)
}

func TestNames_ProblemMatchesTheBrowser(t *testing.T) {
	cases := map[string]string{
		"":       "empty",
		"..":     "dot",
		".":      "dot",
		"a/b":    "slash",
		`a\b`:    "slash",
		"a\x01":  "control",
		"a\x7f":  "control",
		"ok.txt": "",
	}
	for in, want := range cases {
		require.Equal(t, want, NameProblem(in), "%q", in)
	}
	long := ""
	for i := 0; i < 128; i++ {
		long += "ş"
	}
	require.Equal(t, "too_long", NameProblem(long))
	require.Equal(t, "", NameProblem(long[:254]))
}

func TestNames_ConstantsMatchTheServer(t *testing.T) {
	// The server recognises the same two artifacts; if either ever moves,
	// the CLI must move with it.
	require.Equal(t, e2e.MarkerName, MarkerName)
	require.Equal(t, e2e.MagicPrefix, MagicPrefix)
	// …and every spelling of an encrypted name.
	_, v := vectorKey(t)
	for _, vec := range v.Vectors {
		require.True(t, e2e.LooksEncryptedName(vec.Stored), vec.Stored)
		if vec.Sidecar != "" {
			require.True(t, e2e.LooksEncryptedName(vec.Sidecar), vec.Sidecar)
		}
	}
}
