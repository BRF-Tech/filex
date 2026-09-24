package sqlite

import (
	"database/sql/driver"
	"fmt"
	"strings"

	"golang.org/x/text/unicode/norm"
	sqlitedriver "modernc.org/sqlite"

	"github.com/brf-tech/filex/backend/internal/namefold"
)

// ⚠⚠ SQLite's LIKE compares BYTES, folding case for ASCII only. On a product
// whose first language is Turkish that failed twice over in the one search
// path an install without the Bleve index has:
//
//   - `%şubat%` found nothing in `ŞUBAT.pdf`: `Ş` and `ş` are two unrelated
//     characters to it (v0.43.0, PR #35's integration).
//   - `%gürel%` found nothing in a name a Mac uploaded: macOS writes `ü` as
//     `u` + U+0308, filex keeps the bytes it is given, and 71 388 of 169 471
//     names on the catalogue that reported it were stored that way (PR #46).
//
// So on SQLite a name is not compared with LIKE at all. Two functions of our
// own put the stored name through internal/namefold — the rule the query's
// words were folded by, and the one the index and the scorer compare with —
// and compare in Go:
//
//	fx_match(name, word, …)  1 when the folded name holds every word
//	fx_rank(name, word)      0 the name IS the word, or the word plus an
//	                         extension; 1 it starts with the word; 2 neither
//
// PostgreSQL and MySQL spell the same rule in SQL (their SearchNodes), and
// TestSearchNodesOnEveryEngine holds the three to one answer.
//
// ⚠ ONE call per row for all the words, not one fold per word: a Go function
// costs a few microseconds a row here (measured: a 170 000-row scan went from
// 0.2 s with the native LIKE to 0.77 s with a fold per row), so fx_match folds
// the name once, only for the words that need it, and runs last — after the
// native LIKEs on model.NameMatch.Runs and on the all-plain words (`plan`,
// `2026`: stored as they are in every spelling) have turned most rows away in
// C.
const (
	matchFunc = "fx_match"
	rankFunc  = "fx_rank"
)

// wordForms is a folded word as MySQL has to be asked for it: composed, and
// decomposed when that differs. MySQL compares code point by code point — a
// decomposed `Gu`+U+0308+`rel` answers neither `%gürel%` nor `%gurel%`
// (measured on 8.4) — and has nothing to compose the stored name with. Its
// collation folds the case of both.
func wordForms(w string) []string {
	if d := norm.NFD.String(w); d != w {
		return []string{w, d}
	}
	return []string{w}
}

// mysqlNameLike is one MySQL condition on the name the way namefold compares
// it, as far as SQL can spell that: utf8mb4_0900_ai_ci folds case (and
// accents, which only widens what the scorer re-checks) and already treats
// I, İ and i as one letter; the REPLACEs add the fourth, ı, and drop the dot
// namefold.Canonical drops. Each pattern is one form from wordForms.
var mysqlNameLike = func() string {
	dot := string(rune(0x0307))
	e := "name"
	for _, r := range [][2]string{
		{"ı", "i"}, // first, so a dotless i with a dot above is dropped below too
		{"i" + dot, "i"}, {"I" + dot, "i"}, {"İ" + dot, "i"},
		{"j" + dot, "j"}, {"J" + dot, "j"},
	} {
		e = "REPLACE(" + e + ", '" + r[0] + "', '" + r[1] + "')"
	}
	return e + " COLLATE utf8mb4_0900_ai_ci LIKE ?"
}()

// textArg reads a TEXT (or BLOB) argument. A NULL is "".
func textArg(fn string, v driver.Value) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case []byte:
		return string(x), nil
	case nil:
		return "", nil
	default:
		return "", fmt.Errorf("%s: cannot read %T", fn, v)
	}
}

// rankName is fx_rank — model.NameMatch.Prefer's three tiers.
func rankName(name, prefer string) int64 {
	f := namefold.String(name)
	switch {
	case f == prefer || strings.HasPrefix(f, prefer+"."):
		return 0
	case strings.HasPrefix(f, prefer):
		return 1
	}
	return 2
}

func init() {
	// ⚠ Before any connection is opened (the driver hands its functions to new
	// connections only), and once per process: a second registration under the
	// same name is an error, not a replacement.
	must := func(name string, nArg int32, fn func(*sqlitedriver.FunctionContext, []driver.Value) (driver.Value, error)) {
		if err := sqlitedriver.RegisterDeterministicScalarFunction(name, nArg, fn); err != nil {
			panic(fmt.Sprintf("sqlite: registering %s: %v", name, err))
		}
	}
	must(matchFunc, -1, func(_ *sqlitedriver.FunctionContext, args []driver.Value) (driver.Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("%s: want a name and at least one word", matchFunc)
		}
		name, err := textArg(matchFunc, args[0])
		if err != nil {
			return nil, err
		}
		// The words arrive folded (SearchNodes folds them once per query);
		// the name is folded once per row, here.
		f := namefold.String(name)
		for _, a := range args[1:] {
			w, err := textArg(matchFunc, a)
			if err != nil {
				return nil, err
			}
			if !strings.Contains(f, w) {
				return int64(0), nil
			}
		}
		return int64(1), nil
	})
	must(rankFunc, 2, func(_ *sqlitedriver.FunctionContext, args []driver.Value) (driver.Value, error) {
		name, err := textArg(rankFunc, args[0])
		if err != nil {
			return nil, err
		}
		prefer, err := textArg(rankFunc, args[1])
		if err != nil {
			return nil, err
		}
		return rankName(name, prefer), nil
	})
}
