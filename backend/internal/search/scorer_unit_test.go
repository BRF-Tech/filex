package search

// Unit tests for the scorer itself. The behaviour tests in
// scorer_test.go go through the index and prove the reporter's queries;
// these pin the pieces those tests depend on, so a failure says WHICH
// part moved.

import "testing"

// TestPrepareQuery_RawTagTokenWouldDropEverything is the ordering
// contract between ParseQuery and the scorer, and it is a real failure
// mode of this change rather than a hypothetical one: the scorer drops a
// candidate when ANY piece is unanswered, so a `tag:source` token that
// reached it as text would drop every file in the index and a working
// tag search would return nothing.
//
// Both halves are asserted, because only the pair proves the order
// matters: the raw query drops the file, the parsed query keeps it.
func TestPrepareQuery_RawTagTokenWouldDropEverything(t *testing.T) {
	const raw = "main.go tag:source"

	if PrepareQuery(raw).ScoreName("main.go", "/Code/main.go").OK {
		t.Fatal("this test no longer proves anything: the raw tag token was expected to drop the candidate")
	}
	parsed := ParseQuery(raw)
	if parsed.Text != "main.go" {
		t.Fatalf("ParseQuery must strip the tag token, got %q", parsed.Text)
	}
	if !PrepareQuery(parsed.Text).ScoreName("main.go", "/Code/main.go").OK {
		t.Error("after ParseQuery the tag token is gone and the file must score")
	}
}

// TestScoreName_PathIdentity: naming the whole path is the strongest
// possible statement and outranks everything, including a same-named
// file somewhere else.
func TestScoreName_PathIdentity(t *testing.T) {
	q := PrepareQuery("Code/main.go")

	hit := q.ScoreName("main.go", "/Code/main.go")
	if !hit.OK || hit.Score != PathIdentityScore || hit.Tier != TierExact {
		t.Errorf("want identity score %d at tier exact, got %+v", PathIdentityScore, hit)
	}
	if other := q.ScoreName("main.go", "/example/main.go"); other.OK {
		t.Errorf("a different folder must not answer this query, got %+v", other)
	}
}

// TestScoreName_LabelBeatsPath is VS Code's central weighting and the
// answer to "Code/main.go and example/main.go are the same thing": a
// piece answered by the FILENAME is worth an order of magnitude more
// than one answered by a folder above it.
func TestScoreName_LabelBeatsPath(t *testing.T) {
	q := PrepareQuery("report")

	inName := q.ScoreName("report.txt", "/misc/report.txt")
	inFolder := q.ScoreName("summary.txt", "/report/summary.txt")
	if !inName.OK || !inFolder.OK {
		t.Fatalf("both should match: name=%+v folder=%+v", inName, inFolder)
	}
	if inName.Score <= inFolder.Score {
		t.Errorf("a filename match must outscore a folder match: %d vs %d", inName.Score, inFolder.Score)
	}
	if inName.Tier != TierExact {
		t.Errorf("report -> report.txt is an exact match, got %s", inName.Tier)
	}
	if inFolder.Tier != TierPath {
		t.Errorf("only the folder answered, want tier path, got %s", inFolder.Tier)
	}
}

// TestScoreName_SubsequenceNotSubstring is the reporter's "you shipped
// string matching". `mn` is not a substring of `main.go`; it is a
// subsequence, and the scorer has to see it — for the candidates Bleve
// hands over.
func TestScoreName_SubsequenceNotSubstring(t *testing.T) {
	if got := PrepareQuery("mn").ScoreName("main.go", "/Code/main.go"); !got.OK {
		t.Errorf("`mn` is a subsequence of `main.go` and must score, got %+v", got)
	}
	if got := PrepareQuery("gm").ScoreName("main.go", "/Code/main.go"); got.OK {
		t.Errorf("`gm` is not a subsequence of `main.go` (order matters WITHIN a piece), got %+v", got)
	}
}

// TestScoreName_PositionBonuses pins the ported bonus table. The whole
// reason this ranks better than a substring test is that it knows a
// match at the start of a name, or straight after a separator, is worth
// more than one in the middle of a word — so if the constants drift, the
// ranking quietly degrades to "found it somewhere".
func TestScoreName_PositionBonuses(t *testing.T) {
	q := PrepareQuery("go")

	atStart := q.ScoreName("go.mod", "/go.mod")     // index 0
	afterSep := q.ScoreName("main.go", "/main.go")  // straight after '.'
	midWord := q.ScoreName("logo.png", "/logo.png") // inside a word
	if !atStart.OK || !afterSep.OK || !midWord.OK {
		t.Fatalf("all three should match: %+v %+v %+v", atStart, afterSep, midWord)
	}
	if !(atStart.Score > afterSep.Score && afterSep.Score > midWord.Score) {
		t.Errorf("want start > after-separator > mid-word, got %d, %d, %d",
			atStart.Score, afterSep.Score, midWord.Score)
	}
}

// TestScoreName_CamelHump: NPE finds NullPointerException. Ported for
// completeness — filex filenames are less camelCase-heavy than VS Code
// symbols, but source trees are exactly where people search this way.
func TestScoreName_CamelHump(t *testing.T) {
	q := PrepareQuery("npe")
	hump := q.ScoreName("NullPointerException.java", "/NullPointerException.java")
	flat := q.ScoreName("nullpointerexception.java", "/nullpointerexception.java")
	if !hump.OK || !flat.OK {
		t.Fatalf("both should match: %+v %+v", hump, flat)
	}
	if hump.Score <= flat.Score {
		t.Errorf("camelCase humps must score above the same letters mid-word: %d vs %d",
			hump.Score, flat.Score)
	}
}

// TestScoreName_SingleCharacterQuery. A one-character query gets no
// special case: it scores and filters like any other, which means it
// keeps every candidate containing that character and drops the rest.
// Worth a test because the alternative — bailing out early — would make
// the shortest query the only one that behaves differently.
func TestScoreName_SingleCharacterQuery(t *testing.T) {
	q := PrepareQuery("g")
	if got := q.ScoreName("main.go", "/Code/main.go"); !got.OK {
		t.Errorf("`g` occurs in main.go and must match, got %+v", got)
	}
	if got := q.ScoreName("readme.txt", "/readme.txt"); got.OK {
		t.Errorf("`g` occurs nowhere in readme.txt and must not match, got %+v", got)
	}
}

// TestScoreName_EmptyAndUnscorable are the two cases where filtering
// would do more harm than good: nothing to score with, and nothing to
// score against. The second is the un-rebuilt upgrade — an index whose
// documents predate the stored name/path fields must lose ranking
// quality, never results.
func TestScoreName_EmptyAndUnscorable(t *testing.T) {
	if got := PrepareQuery("   ").ScoreName("main.go", "/Code/main.go"); !got.OK {
		t.Errorf("an empty query filters nothing, got %+v", got)
	}
	if got := PrepareQuery("main").ScoreName("", ""); !got.OK {
		t.Errorf("a document with no stored name must survive, got %+v", got)
	}
}

// TestScoreName_UnicodeFilenames: the bonuses are computed per rune, so
// a Turkish filename is scored on its characters and not on the middle
// bytes of one.
func TestScoreName_UnicodeFilenames(t *testing.T) {
	if got := PrepareQuery("şubat").ScoreName("rapor-şubat.txt", "/rapor-şubat.txt"); !got.OK {
		t.Errorf("want a match on the Turkish filename, got %+v", got)
	}
	if got := PrepareQuery("ŞUBAT").ScoreName("rapor-şubat.txt", "/rapor-şubat.txt"); !got.OK {
		t.Errorf("case must not matter, got %+v", got)
	}
}

// TestFallback_AcceptsAgreesWithIndex: the index-less install is a
// different code path, not a different product. Whatever the scorer
// drops on the index side, the LIKE fallback drops too.
func TestFallback_AcceptsAgreesWithIndex(t *testing.T) {
	plan := PlanFallback("Code main")

	if !plan.Accepts("main.go", "/Code/main.go") {
		t.Error("the fallback must accept the file the index ranks first")
	}
	if plan.Accepts("Code", "/Code") {
		t.Error("`/Code` answers `code` and nothing answers `main`; the fallback must drop it too")
	}
	if plan.Accepts("main.go", "/example/main.go") {
		t.Error("nothing in /example/main.go answers `code`; the fallback must drop it too")
	}
	if got := plan.Rank("main.go", "/Code/main.go"); got != TierPath {
		t.Errorf("the folder answered one of the pieces, want tier path, got %s", got)
	}
}
