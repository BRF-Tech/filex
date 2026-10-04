package db_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// The folders the subtree questions are asked about below, and the rows that
// must and must not answer them. Every look-alike here is a way a prefix match
// goes wrong on some engine: a collation that ignores case or accents, a
// wildcard read out of a name, a sibling that sorts right beside the folder, a
// bound counted in bytes, an index that only holds the first 512 characters.
var (
	subtreeDeepDir = subtreeDeepFolder()

	subtreeSeed = []struct {
		path    string
		typ     model.NodeType
		trashed bool
	}{
		{path: "/Rapor", typ: model.NodeTypeDirectory},
		{path: "/Rapor/a.txt", typ: model.NodeTypeFile},
		{path: "/Rapor/alt", typ: model.NodeTypeDirectory},
		{path: "/Rapor/alt/b.txt", typ: model.NodeTypeFile},
		{path: "/Rapor/\x01denetim.txt", typ: model.NodeTypeFile}, // a control character sorts below everything
		{path: "Rapor/çıplak.txt", typ: model.NodeTypeFile},       // the bare spelling
		{path: "/Rapor/silinmiş.txt", typ: model.NodeTypeFile, trashed: true},
		// What only ONE arm of a statement sees, so that no arm can lose its
		// spelling or its `deleted_at IS NULL` unnoticed: a folder whose only
		// row below is in the bare spelling; one that holds nothing live, in
		// either spelling; a folder's own row in the bare spelling, live and
		// trashed; a trashed folder that still holds a live row.
		{path: "/Yalnız", typ: model.NodeTypeDirectory},
		{path: "Yalnız/çıplak.txt", typ: model.NodeTypeFile},
		{path: "/Çöp", typ: model.NodeTypeDirectory},
		{path: "/Çöp/silinmiş.txt", typ: model.NodeTypeFile, trashed: true},
		{path: "Çöp/çıplak-silinmiş.txt", typ: model.NodeTypeFile, trashed: true},
		{path: "Rapor/çıplak-silinmiş.txt", typ: model.NodeTypeFile, trashed: true},
		{path: "Çıplak", typ: model.NodeTypeDirectory},
		{path: "ÇıplakEski", typ: model.NodeTypeDirectory, trashed: true},
		{path: "/Silik", typ: model.NodeTypeDirectory, trashed: true},
		{path: "/Silik/kalan.txt", typ: model.NodeTypeFile},
		// Siblings that only start with the folder's name. '/' is 0x2F and '0'
		// is 0x30: these sit on both sides of every bound a range can have.
		{path: "/Rapor /boşluk.txt", typ: model.NodeTypeFile},
		{path: "/Rapor!/x.txt", typ: model.NodeTypeFile},
		{path: "/Rapor-eski/x.txt", typ: model.NodeTypeFile},
		{path: "/Rapor./x.txt", typ: model.NodeTypeFile},
		{path: "/Rapor.txt", typ: model.NodeTypeFile},
		{path: "/Rapor0", typ: model.NodeTypeDirectory},
		{path: "/Rapor0/x.txt", typ: model.NodeTypeFile},
		{path: "/Rapor1/x.txt", typ: model.NodeTypeFile},
		{path: "/RaporA/x.txt", typ: model.NodeTypeFile},
		{path: "/Rapor_x/x.txt", typ: model.NodeTypeFile},
		// The same name in another case is another folder.
		{path: "/rapor/küçük.txt", typ: model.NodeTypeFile},
		{path: "/RAPOR/büyük.txt", typ: model.NodeTypeFile},
		// The same name deeper in the tree is another folder.
		{path: "/Arşiv/Rapor/eski.txt", typ: model.NodeTypeFile},
		// `_` and `%` are names, not wildcards.
		{path: "/a_b", typ: model.NodeTypeDirectory},
		{path: "/a_b/içinde.txt", typ: model.NodeTypeFile},
		{path: "/axb/dışında.txt", typ: model.NodeTypeFile},
		{path: "/a%b/içinde.txt", typ: model.NodeTypeFile},
		{path: "/aXYb/dışında.txt", typ: model.NodeTypeFile},
		// Not ASCII: composed, decomposed (as macOS writes it) and other cases
		// are four different folders.
		{path: "/Müşteri", typ: model.NodeTypeDirectory},
		{path: "/Müşteri/sözleşme.pdf", typ: model.NodeTypeFile},
		{path: "/Müşteri2/ek.pdf", typ: model.NodeTypeFile},
		{path: "/müşteri/küçük.pdf", typ: model.NodeTypeFile},
		{path: "/MÜŞTERİ/büyük.pdf", typ: model.NodeTypeFile},
		{path: "/Müşteri/ayrışık.pdf", typ: model.NodeTypeFile},
		// Outside the Basic Multilingual Plane: four bytes, one character.
		{path: "/📁/dosya.txt", typ: model.NodeTypeFile},
		{path: "/📁2/dosya.txt", typ: model.NodeTypeFile},
		// Longer than any index key: the folder's own row, what is below it,
		// siblings that differ from it only past the key - on both sides of
		// its range - and a folder past the key in the bare spelling.
		{path: subtreeDeepDir, typ: model.NodeTypeDirectory},
		{path: subtreeDeepDir + "/derin.txt", typ: model.NodeTypeFile},
		{path: subtreeDeepDir + "x/komşu.txt", typ: model.NodeTypeFile},
		{path: subtreeDeepDir + "-eski/x.txt", typ: model.NodeTypeFile},
		{path: subtreeDeepDir + ".txt", typ: model.NodeTypeFile},
		{path: subtreeDeepDir[1:] + "ç", typ: model.NodeTypeDirectory},
		// And the same name in another case below it: their keys are the same
		// 512 characters, so only path itself, compared byte for byte, tells
		// the two apart.
		{path: subtreeDeepDir + "/Müşteri/derin.pdf", typ: model.NodeTypeFile},
		{path: subtreeDeepDir + "/müşteri/derin.pdf", typ: model.NodeTypeFile},
		// A row that IS the lower bound of its folder's range: it starts with
		// "Tek/", so it is below Tek.
		{path: "/Tek/", typ: model.NodeTypeDirectory},
	}

	subtreeAsked = []string{
		"Rapor", "/Rapor", "/Rapor/", "Rapor/../Rapor", "Rapor/alt", "/Rapor/alt/b.txt",
		"rapor", "RAPOR", "Rapor ", "Rapor0", "Rapor.", "Arşiv/Rapor", "Arşiv",
		"a_b", "a%b", "Müşteri", "müşteri", "Müşteri", "📁",
		subtreeDeepDir, subtreeDeepDir[1:] + "ç", subtreeDeepDir + "/Müşteri", "Derin", "Tek",
		"Yalnız", "Çöp", "Çıplak", "ÇıplakEski", "Silik", "yok", "", "/", ".",
	}
)

// subtreeDeepFolder is a folder nine names down whose path is 1,815 characters
// and 5,415 bytes: past the 512 characters an index on nodes.path holds on
// MySQL and PostgreSQL, and past the 2704 bytes a PostgreSQL B-tree row can be
// - by enough that compression does not bring it back under, because nothing
// in it repeats (the names are drawn from a hash chain, not strings.Repeat).
// An index on the whole path refuses this row on PostgreSQL.
func subtreeDeepFolder() string {
	var b strings.Builder
	b.WriteString("/Derin")
	sum := sha256.Sum256([]byte("filex"))
	for name := 0; name < 9; name++ {
		b.WriteByte('/')
		for i := 0; i < 200; i++ {
			if i%16 == 0 {
				sum = sha256.Sum256(sum[:])
			}
			// A CJK ideograph, three bytes of UTF-8.
			b.WriteRune(rune(0x4E00 + (int(sum[i%16*2])<<8|int(sum[i%16*2+1]))%0x5000))
		}
	}
	return b.String()
}

// subtreeOracle is what "the row at dir" and "the rows below dir" mean, said
// in Go: the two spellings a stored path can have for the cleaned dir, compared
// byte for byte. The storage root is never a subtree.
func subtreeOracle(dir string) (self, below func(p string) bool) {
	bare := strings.Trim(path.Clean("/"+strings.Trim(dir, "/")), "/")
	if bare == "" {
		none := func(string) bool { return false }
		return none, none
	}
	slashed := "/" + bare
	self = func(p string) bool { return p == slashed || p == bare }
	below = func(p string) bool { return strings.HasPrefix(p, slashed+"/") || strings.HasPrefix(p, bare+"/") }
	return self, below
}

// TestSubtreeQuestionsMatchTheFolderExactlyOnEveryEngine holds every question
// the store answers about what is below a folder to one meaning on every
// engine: the rows whose path starts with the folder's own, byte for byte, in
// both spellings - and nothing that only looks like it.
//
// These answers decide what a rescan moves to the trash (ListStaleNodesUnder,
// CountLiveNodesUnder), which rows the sync drops (ListNodesUnder) and whether
// a folder is new to the encryption rule (HasLiveNodesUnder), so "nearly the
// same rows" on one engine is a data-loss bug on that engine.
func TestSubtreeQuestionsMatchTheFolderExactlyOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			st := createEngineStorage(t, store)

			var live, all []string // what was seeded, in id order
			for _, row := range subtreeSeed {
				n := seedTreeRow(t, store, st.ID, row.path, row.typ)
				all = append(all, row.path)
				if row.trashed {
					require.NoError(t, store.SoftDeleteNode(ctx, n.ID))
					continue
				}
				live = append(live, row.path)
			}
			// Another storage holding the same paths answers nothing here.
			other, err := store.CreateStorage(ctx, &model.Storage{
				Name: "other", Driver: "local", MountPath: "/data/other", ConfigJSON: []byte(`{"root":"/data/other"}`),
				SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
			})
			require.NoError(t, err)
			seedTreeRow(t, store, other.ID, "/Rapor/başka-depo.txt", model.NodeTypeFile)
			seedTreeRow(t, store, other.ID, "/yok/başka-depo.txt", model.NodeTypeFile)

			pick := func(from []string, keep func(string) bool) []string {
				out := []string{}
				for _, p := range from {
					if keep(p) {
						out = append(out, p)
					}
				}
				return out
			}
			future, past := time.Now().Add(time.Hour), time.Now().Add(-time.Hour)

			// The cut-off is strict, and an instant: a row seen AT it is not
			// stale. A rescan's cut-off is the second it started in, and the
			// rows it walks are seen in that same second.
			inAlt, err := store.ListNodesUnder(ctx, st.ID, "Rapor/alt/b.txt", false)
			require.NoError(t, err)
			require.Len(t, inAlt, 1)
			seen := inAlt[0].SeenAt
			stale, err := store.ListStaleNodesUnder(ctx, st.ID, "Rapor/alt", seen)
			require.NoError(t, err)
			require.Empty(t, stale, "a row seen at the cut-off (%s) is not stale", seen)
			stale, err = store.ListStaleNodesUnder(ctx, st.ID, "Rapor/alt", seen.Add(time.Second))
			require.NoError(t, err)
			require.Equal(t, []string{"/Rapor/alt/b.txt"}, pathsInOrder(stale), "a second later it is")

			// A catalogue that cannot be read is an error, never an answer:
			// "nothing below" would make a full folder a new one.
			gone, cancel := context.WithCancel(ctx)
			cancel()
			_, err = store.HasLiveNodesUnder(gone, st.ID, "Rapor")
			require.Error(t, err, "HasLiveNodesUnder on a store that cannot answer")
			_, err = store.CountLiveNodesUnder(gone, st.ID, "Rapor")
			require.Error(t, err, "CountLiveNodesUnder on a store that cannot answer")
			_, err = store.ListNodesUnder(gone, st.ID, "Rapor", false)
			require.Error(t, err, "ListNodesUnder on a store that cannot answer")
			_, err = store.ListStaleNodesUnder(gone, st.ID, "Rapor", future)
			require.Error(t, err, "ListStaleNodesUnder on a store that cannot answer")

			for _, dir := range subtreeAsked {
				name := subtreeLabel(dir)
				self, below := subtreeOracle(dir)
				wantBelow := pick(live, below)

				got, err := store.ListNodesUnder(ctx, st.ID, dir, false)
				require.NoError(t, err, "ListNodesUnder(%s)", name)
				require.Equal(t, pick(live, func(p string) bool { return self(p) || below(p) }), pathsInOrder(got),
					"ListNodesUnder(%s): the live rows at and below it, in id order", name)
				require.True(t, sort.SliceIsSorted(got, func(i, j int) bool { return got[i].ID < got[j].ID }),
					"ListNodesUnder(%s) is in id order", name)

				got, err = store.ListNodesUnder(ctx, st.ID, dir, true)
				require.NoError(t, err, "ListNodesUnder(%s, deleted too)", name)
				require.Equal(t, pick(all, func(p string) bool { return self(p) || below(p) }), pathsInOrder(got),
					"ListNodesUnder(%s, deleted too): trashed rows as well", name)

				n, err := store.CountLiveNodesUnder(ctx, st.ID, dir)
				require.NoError(t, err, "CountLiveNodesUnder(%s)", name)
				require.EqualValues(t, len(wantBelow), n, "CountLiveNodesUnder(%s): live rows strictly below it", name)

				has, err := store.HasLiveNodesUnder(ctx, st.ID, dir)
				require.NoError(t, err, "HasLiveNodesUnder(%s)", name)
				require.Equal(t, len(wantBelow) > 0, has, "HasLiveNodesUnder(%s): whether a live row is strictly below it", name)

				stale, err := store.ListStaleNodesUnder(ctx, st.ID, dir, future)
				require.NoError(t, err, "ListStaleNodesUnder(%s)", name)
				require.ElementsMatch(t, wantBelow, pathsInOrder(stale),
					"ListStaleNodesUnder(%s): every live row strictly below it was last seen before the cut-off", name)

				stale, err = store.ListStaleNodesUnder(ctx, st.ID, dir, past)
				require.NoError(t, err, "ListStaleNodesUnder(%s, past)", name)
				require.Empty(t, stale, "ListStaleNodesUnder(%s): rows seen after the cut-off are not stale", name)
			}
		})
	}
}

// subtreeLabel is a folder as a failure message names it: quoted, and cut
// where it is the 5 KB one.
func subtreeLabel(dir string) string {
	if len(dir) > 120 {
		return fmt.Sprintf("%q… (%d bytes)", string([]rune(dir)[:16]), len(dir))
	}
	return strconv.Quote(dir)
}

// pathsInOrder is the rows' paths in the order the store returned them.
func pathsInOrder(nodes []*model.Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.Path)
	}
	return out
}

// A name that is not valid UTF-8 is a name: a disk written in another encoding
// (cp1254, ISO 8859-9) has them, and SQLite stores the bytes as they come. The
// folder's rows are the ones that start with its bytes.
//
// Until 00081 the match was SUBSTR(path,1,n) with n counted in Go: Go counts
// the two bytes of "\xff\x80" as two characters and SQLite as one, so the
// bound overshot and such a folder had no rows below it for any of these
// questions - an empty folder to the encryption rule, nothing to tombstone to
// a rescan. A name holding a NUL went the same way: SQLite's SUBSTR stops at
// it. PostgreSQL refuses both when the row is written, and MySQL the first, so
// the question does not arise there.
func TestSubtreeQuestionsTakeANameAsItsBytesOnSQLite(t *testing.T) {
	sqlDB, drv := openMigrated(t, sqliteEngine(t))
	store := drv.NewStore(sqlDB)
	ctx := context.Background()
	st := createEngineStorage(t, store)

	for _, dir := range []string{"z\xff\x80", "z\xfc\xb0", "z\xe0\x80", "z\x00b"} {
		seedTreeRow(t, store, st.ID, "/"+dir, model.NodeTypeDirectory)
		seedTreeRow(t, store, st.ID, "/"+dir+"/belge.txt", model.NodeTypeFile)

		n, err := store.CountLiveNodesUnder(ctx, st.ID, dir)
		require.NoError(t, err)
		require.EqualValues(t, 1, n, "%q holds one file", dir)
		has, err := store.HasLiveNodesUnder(ctx, st.ID, dir)
		require.NoError(t, err)
		require.True(t, has, "%q holds one file", dir)
		got, err := store.ListNodesUnder(ctx, st.ID, dir, false)
		require.NoError(t, err)
		require.Equal(t, []string{"/" + dir, "/" + dir + "/belge.txt"}, pathsInOrder(got))
	}
}
