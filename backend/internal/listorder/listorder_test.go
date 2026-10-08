package listorder

import (
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

func names(nodes []*model.Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.Name
	}
	return out
}

func same(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func node(name string, dir bool, size int64, mod time.Time) *model.Node {
	t := model.NodeTypeFile
	if dir {
		t = model.NodeTypeDirectory
	}
	m := mod
	return &model.Node{Name: name, Path: "/" + name, Type: t, Size: size, BackendMtime: &m}
}

// TestSort_FoldersFirstInBothDirections: the catalogue path used to answer
// `ORDER BY type DESC, name` - files first - while the merged path put folders
// first (audit Y3). One rule now, and the direction never regroups.
func TestSort_FoldersFirstInBothDirections(t *testing.T) {
	now := time.Now()
	mk := func() []*model.Node {
		return []*model.Node{
			node("b.txt", false, 1, now), node("Zeta", true, 0, now),
			node("a.txt", false, 1, now), node("alpha", true, 0, now),
		}
	}
	asc := mk()
	SortNodes(asc, Default)
	if want := []string{"alpha", "Zeta", "a.txt", "b.txt"}; !same(names(asc), want) {
		t.Fatalf("name ascending: got %v want %v", names(asc), want)
	}
	desc := mk()
	SortNodes(desc, Order{Key: KeyName, Desc: true})
	if want := []string{"Zeta", "alpha", "b.txt", "a.txt"}; !same(names(desc), want) {
		t.Fatalf("name descending keeps folders first: got %v want %v", names(desc), want)
	}
}

func TestSort_NumbersAsNumbersAndTheFourIs(t *testing.T) {
	nodes := []*model.Node{
		node("Disk 10", false, 1, time.Time{}), node("Disk 2", false, 1, time.Time{}),
		node("ılık", false, 1, time.Time{}), node("Ilgaz", false, 1, time.Time{}),
	}
	SortNodes(nodes, Default)
	if want := []string{"Disk 2", "Disk 10", "Ilgaz", "ılık"}; !same(names(nodes), want) {
		t.Fatalf("got %v want %v", names(nodes), want)
	}
}

func TestSort_ModifiedAndSize(t *testing.T) {
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nodes := []*model.Node{node("old.txt", false, 10, old), node("new.txt", false, 5, newer)}
	SortNodes(nodes, Order{Key: KeyModified, Desc: true})
	if names(nodes)[0] != "new.txt" {
		t.Fatalf("-modified is newest first, got %v", names(nodes))
	}
	SortNodes(nodes, Order{Key: KeySize, Desc: true})
	if names(nodes)[0] != "old.txt" {
		t.Fatalf("-size is largest first, got %v", names(nodes))
	}
}

// TestSort_WhenIsOneTimeline: the person's own time (opened, starred) does
// not split folders from files.
func TestSort_WhenIsOneTimeline(t *testing.T) {
	a := Fields{Name: "file.txt", When: 200}
	b := Fields{Name: "Folder", Dir: true, When: 100}
	if !Less(a, b, Newest) {
		t.Fatal("the later opening comes first, folder or not")
	}
}

func TestParse(t *testing.T) {
	for in, want := range map[string]Order{
		"":          Default,
		"name":      {Key: KeyName},
		"-modified": {Key: KeyModified, Desc: true},
		"-opened":   {Key: KeyWhen, Desc: true},
		"SIZE":      {Key: KeySize},
	} {
		got, ok := Parse(in, Default)
		if !ok || got != want {
			t.Fatalf("Parse(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	if _, ok := Parse("color", Default); ok {
		t.Fatal("an unknown key is refused, not ignored")
	}
}

func TestSortEntries_ReadsTheListingShape(t *testing.T) {
	entries := []map[string]any{
		{"basename": "b.txt", "type": "file", "size": int64(1)},
		{"basename": "docs", "type": "dir", "size": int64(0)},
		{"basename": "a.txt", "type": "file", "size": int64(9)},
	}
	SortEntries(entries, Default, "")
	got := []string{entries[0]["basename"].(string), entries[1]["basename"].(string), entries[2]["basename"].(string)}
	if want := []string{"docs", "a.txt", "b.txt"}; !same(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
