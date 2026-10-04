package dbtest

import (
	"context"
	"fmt"
	"math/rand"
	"testing"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// SeedCatalogue catalogues a tree the shape of a real one on storageID - root,
// tops folders in it ("Müşteri-NN"), subs folders in each ("Sözleşmeler-NN")
// and files files in each of those ("belge-NNN.pdf") - and returns the number
// of rows written. It is for the tests that measure the catalogue at the size
// of an install (`-tags measure`): rows as wide as a sync writes them, 2,500
// to a transaction.
//
// The rows are written in path order, as a first sync walks a storage, unless
// shuffled: then in an order that has nothing to do with their paths (always
// the same one), as a catalogue ends up after years of uploads and moves. The
// two differ for every question that reads many rows through an index on
// path: in path order the rows of a folder sit side by side in the table.
func SeedCatalogue(t testing.TB, store db.Store, storageID int64, root string, tops, subs, files int, shuffled bool) int {
	t.Helper()
	type row struct {
		path, name string
		typ        model.NodeType
	}
	rows := []row{{root, root[1:], model.NodeTypeDirectory}}
	for a := 0; a < tops; a++ {
		top := fmt.Sprintf("Müşteri-%02d", a)
		rows = append(rows, row{root + "/" + top, top, model.NodeTypeDirectory})
		for b := 0; b < subs; b++ {
			sub := fmt.Sprintf("Sözleşmeler-%02d", b)
			dir := root + "/" + top + "/" + sub
			rows = append(rows, row{dir, sub, model.NodeTypeDirectory})
			for c := 0; c < files; c++ {
				name := fmt.Sprintf("belge-%03d.pdf", c)
				rows = append(rows, row{dir + "/" + name, name, model.NodeTypeFile})
			}
		}
	}
	if shuffled {
		rand.New(rand.NewSource(1)).Shuffle(len(rows), func(i, j int) { rows[i], rows[j] = rows[j], rows[i] })
	}
	const batch = 2500
	for from := 0; from < len(rows); from += batch {
		to := min(from+batch, len(rows))
		err := store.WithTx(context.Background(), func(ctx context.Context) error {
			for _, r := range rows[from:to] {
				if _, err := store.CreateNode(ctx, &model.Node{
					StorageID: storageID, Name: r.name, Path: r.path, PathHash: pathkey.Hash(storageID, r.path), StorageKey: r.path,
					Type: r.typ, Size: 4096, Mime: "application/pdf", Etag: "9e107d9d372bb6826bd81d3542a419d6",
				}); err != nil {
					return fmt.Errorf("%s: %w", r.path, err)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("dbtest: seed: %v", err)
		}
	}
	return len(rows)
}

// SeedEmptyFolders catalogues root and n folders in it ("klasör-NNNN") with
// nothing below them, and returns the number of rows written.
func SeedEmptyFolders(t testing.TB, store db.Store, storageID int64, root string, n int) int {
	t.Helper()
	err := store.WithTx(context.Background(), func(ctx context.Context) error {
		for i := -1; i < n; i++ {
			p, name := root, root[1:]
			if i >= 0 {
				name = fmt.Sprintf("klasör-%04d", i)
				p = root + "/" + name
			}
			if _, err := store.CreateNode(ctx, &model.Node{
				StorageID: storageID, Name: name, Path: p, PathHash: pathkey.Hash(storageID, p), StorageKey: p,
				Type: model.NodeTypeDirectory,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("dbtest: seed %s: %v", root, err)
	}
	return n + 1
}
