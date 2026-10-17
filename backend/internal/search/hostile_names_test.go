package search

// Every hostile name (task #238) is found by its own name, a decomposed name
// by the composed spelling a keyboard types, and a name holding an invisible
// format character by the spelling a person sees.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/testutil/hostilenames"
)

func TestSearch_HostileNamesAreFoundByTheirOwnName(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)
	names := hostilenames.Creatable()
	ids := map[string]int64{}
	for i, n := range names {
		id := int64(i + 1)
		ids[n.ID] = id
		if err := idx.IndexNode(ctx, fileNode(id, n.Name, "/"+n.Name, fmt.Sprintf("e%d", id))); err != nil {
			t.Fatalf("%s: index: %v", n.ID, err)
		}
	}
	for _, n := range names {
		n := n
		if Normalize(n.Name) == "" {
			continue // nothing in it a search could ask for
		}
		t.Run(n.ID, func(t *testing.T) {
			hits, err := idx.SearchScoped(ctx, n.Name, 50, ScopeName)
			if err != nil {
				t.Fatal(err)
			}
			if len(hits) == 0 {
				t.Fatalf("%q found nothing", n.Name)
			}
			want := ids[n.ID]
			for _, h := range hits {
				if h.NodeID == want {
					// First, or tied with the first: two hostile names may
					// share their words (`a;b.txt` and `a..b.txt` are both
					// `a b txt`), and a twin is the same name.
					if h.Tier != hits[0].Tier {
						t.Errorf("%q is a hit of tier %s behind a %s hit (node %d)", n.Name, h.Tier, hits[0].Tier, hits[0].NodeID)
					}
					return
				}
			}
			t.Errorf("%q is not among its own %d hits", n.Name, len(hits))
		})
	}

	// The composed spelling finds the decomposed name; the spelling a person
	// sees finds the name with an invisible character in it.
	for q, id := range map[string]string{
		hostilenames.Get("nfc-butce").Name: "nfd-butce",
		"zerowidth.txt":                    "zero-width-space",
		"zerowidth":                        "zero-width-space",
		"invoicefdp":                       "rlo-extension",
	} {
		hits, err := idx.SearchScoped(ctx, q, 50, ScopeName)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, h := range hits {
			found = found || h.NodeID == ids[id]
		}
		if !found {
			t.Errorf("query %q does not find %s", q, id)
		}
	}
}

// A format character is not a separator (task #238): it is invisible, and
// the word around it is one word.
func TestNormalize_FormatCharactersDoNotSplitAWord(t *testing.T) {
	for _, cp := range []rune{0x200B, 0x00AD, 0x2060, 0x200C, 0x200D, 0xFEFF, 0x202E, 0x2066} {
		name := "zero" + string(cp) + "width.txt"
		if got := Normalize(name); got != "zerowidth txt" {
			t.Errorf("Normalize(zero U+%04X width.txt) = %q, want %q", cp, got, "zerowidth txt")
		}
	}
	if got := Normalize(hostilenames.Get("zero-width-space").Name); strings.Contains(got, " width") {
		t.Errorf("the zero-width space still splits the word: %q", got)
	}
}

// The database fallback still asks for the words AROUND a format character:
// the stored name holds it, and the folded text a database compares keeps it,
// so `zerowidth` is in no stored row while `zero` and `width` are.
func TestPlanFallback_AFormatCharacterSplitsTheDatabaseWords(t *testing.T) {
	got := PlanFallback(hostilenames.Get("zero-width-space").Name).Words
	if want := []string{"zero", "width", "txt"}; !equalStrings(got, want) {
		t.Errorf("fallback words %q, want %q", got, want)
	}
}

// An index written with schema 3 split a word at a format character; it must
// ask for the rebuild that indexes those names whole.
func TestIndexSchemaVersion_FormatCharactersNeedRebuild(t *testing.T) {
	dir := t.TempDir() + "/idx.bleve"
	idx, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.bleve.SetInternal([]byte(indexVersionKey), []byte("3")); err != nil {
		t.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	if !reopened.NeedsRebuild() {
		t.Error("an index written with schema 3 split names at a format character; it must ask for a rebuild")
	}
}
