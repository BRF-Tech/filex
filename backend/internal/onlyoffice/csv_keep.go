package onlyoffice

// The cells nobody touched keep their text when ONLYOFFICE saves a CSV
// (docs/ONLYOFFICE.md → Cells nobody changed keep their text).
//
// ONLYOFFICE reads a CSV the way a spreadsheet does: a cell that looks like a
// number or a date becomes one, and a save writes every cell back as the
// spreadsheet shows it, edited or not. Measured on filex 0.51.0 with Docs
// 9.4.0-129, one cell edited in a semicolon file, the editor's language
// English (filex then passed the request's `lang`, else "en", and no region;
// since 0.54 the server chooses both for the person, lang.go): in
// cells nobody had touched `05320000001` came back as `5320000001`, `007` as
// `7`, `000` as `0`, `01.02.2026` as `1/2/2026` (`15.03.2026` stayed: month
// first, and 15 is no month), and every data row had an empty cell more at
// its end. 11- and 16-digit numbers and decimal commas (text to ONLYOFFICE)
// came back as they were. RewriteCSV wrote that.
//
// KeepCSV reads the file the save is about to replace, lines its records up
// with the saved ones, and writes the file's own bytes back wherever the saved
// text is the same value written ONLYOFFICE's way. Three principles, and every
// rule below is held to them:
//
//   - P1, one way only. csvSame(o, s) is true when o == s, or when s is
//     exactly what ONLYOFFICE writes for an untouched cell whose text is o.
//     Never "equal after normalising both": that would call `007` typed over
//     a `7` the same value, and put the `7` back.
//     ⚠ A false "same" silently undoes a person's edit; a false "different"
//     is only what 0.51.0 did. When in doubt: different.
//
//   - P2. The written file, read back in the file's dialect, is the table
//     ONLYOFFICE saved, cell for cell, up to P1. It is checked before
//     anything is returned (csvKeepCheck), and it is why the file on storage
//     need not be the very version the editing session loaded: whatever the
//     records were lined up against, a cell is written from the file only
//     when the saved cell is that text written ONLYOFFICE's way.
//
//   - P3. A save is never failed, delayed or refused over this. Anything
//     unexpected - a file that is not UTF-8, one too large, a result that does
//     not pass the check, a panic - is written as 0.51.0 wrote it: RewriteCSV.
//
// The rules a cell's text is compared by (csvSame) are what a real Docs
// 9.4.0-129 was measured writing, the editor in English, Turkish, German and
// French; the saved bytes are in csv_keep_measured_test.go. ⚠ A rule read from the
// document server's sources is not one until it is measured: of the ones the
// first version read there, three were wrong (an exponent, a line end in a
// quoted cell, a date's spelling), and the date rule put a date a person had
// typed back to the file's spelling.

import (
	"bytes"
	"fmt"
	"math"
	"runtime/debug"
	"strconv"
	"unicode/utf8"
)

// CSVKept says what KeepCSV did, for the log and the tests.
type CSVKept struct {
	// Applied: the cells were kept. False: the bytes are RewriteCSV's.
	Applied bool
	// Why they were not: "empty" (the file on storage has no record),
	// "not_utf8", "too_large", "too_many_records", "too_many_fields" (in one
	// record), "check_failed" (the result did not read back as the save),
	// "panic".
	Why string
	// Panic, when Why is "panic": what panicked and where, for the log. A
	// runtime panic names indexes, never a cell's text.
	Panic string
	// Records written.
	Records int
	// Unchanged records, written from the file's own bytes.
	Unchanged int
	// Restored cells: in a record somebody changed, the cells whose own text
	// was put back over ONLYOFFICE's way of writing it.
	Restored int
	// New records: saved ones with no record of the file behind them.
	New int
	// Deleted records: the file's, with no saved record.
	Deleted int
}

// csvKeepMaxBytes bounds both the file and the save KeepCSV compares: each is
// held whole, with an index beside it. A variable so that a test can lower it.
var csvKeepMaxBytes int64 = 64 << 20

// csvKeepMaxRecords bounds the records of each (the index is 24 bytes a
// record). A variable for the same reason.
var csvKeepMaxRecords = 2000000

// csvKeepMaxFields bounds the fields of one record on either side. A record is
// split into its fields to be compared, 48 bytes a field, and nothing else
// bounds them: a file may be one line of 64 MiB of delimiters. 16384 is how
// many columns an ONLYOFFICE sheet has; a wider record cannot line up with
// anything it saved. A variable for the same reason.
var csvKeepMaxFields = 16384

// csvKeepPairWindow is how many of the file's records a changed saved record
// is compared with before it counts as a new one (and how many saved records
// back a row that reads like several of the file's is looked for).
const csvKeepPairWindow = 8

// csvKeepPairWork bounds what the pairing may read of the file, in multiples
// of the two files' sizes: a saved record that pairs with none of its
// csvKeepPairWindow leaves them for the next one to read again, and a few
// huge records there would otherwise be read once for every saved record.
// Past it nothing more is paired, and what is left is written as new records.
const csvKeepPairWork = 16

// csvCellMaxUnits is the longest text ONLYOFFICE keeps in a cell, in UTF-16
// code units; its reader cuts there.
const csvCellMaxUnits = 32767

// csvDecimalMaxDigits bounds the digits after the point of a number csvSame
// renders; a longer one is compared as text.
const csvDecimalMaxDigits = 64

// KeepCSV turns the CSV the document server saved into the bytes to write,
// with the text of every cell nobody changed as it is in original, the file
// on storage: a record in which nothing changed byte for byte, the unchanged
// cells of a changed one, new records in the file's dialect. d is how the
// original is written (SniffCSV). When the cells cannot be kept the bytes are
// RewriteCSV(saved, d) and CSVKept says why.
func KeepCSV(original, saved []byte, d CSVDialect) ([]byte, CSVKept) {
	return keepOrRewrite(original, saved, d, keepCSV)
}

// keepOrRewrite is P3: whatever keep does - gives up with a reason, or
// panics - the save is written, RewriteCSV's way.
func keepOrRewrite(original, saved []byte, d CSVDialect, keep func(original, saved []byte, d CSVDialect) ([]byte, CSVKept)) (out []byte, kept CSVKept) {
	defer func() {
		if r := recover(); r != nil {
			// Carried out and not logged here: the caller knows which file.
			out, kept = RewriteCSV(saved, d), CSVKept{Why: "panic", Panic: fmt.Sprint(r) + "\n" + string(debug.Stack())}
		}
	}()
	out, kept = keep(original, saved, d)
	if !kept.Applied {
		return RewriteCSV(saved, d), CSVKept{Why: kept.Why}
	}
	return out, kept
}

func keepCSV(original, saved []byte, d CSVDialect) ([]byte, CSVKept) {
	if int64(len(original)) > csvKeepMaxBytes || int64(len(saved)) > csvKeepMaxBytes {
		return nil, CSVKept{Why: "too_large"}
	}
	body := bytes.TrimPrefix(original, utf8BOM)
	// The whole file, not the head the dialect was read from: a legacy code
	// page after 64 KiB of ASCII cannot be compared with UTF-8 either.
	if !d.UTF8 || !utf8.Valid(body) {
		return nil, CSVKept{Why: "not_utf8"}
	}
	comma := d.Comma
	if comma == 0 {
		comma = ','
	}
	tabs := comma != '\t'
	var o, s csvSide
	if why := o.index(body, comma, tabs); why != "" {
		return nil, CSVKept{Why: why}
	}
	if len(o.recs) == 0 {
		return nil, CSVKept{Why: "empty"}
	}
	if why := s.index(bytes.TrimPrefix(saved, utf8BOM), ',', tabs); why != "" {
		return nil, CSVKept{Why: why}
	}
	// A `sep=` line is the file's and never a row of the save: it is written
	// back first and lined up with nothing.
	first := 0
	if csvSepLine(&o, &s, tabs) {
		first = 1
	}
	from, taken := csvLineUp(&o, &s, first, tabs)

	kept := CSVKept{Applied: true}
	w := csvOut{eol: []byte("\n"), comma: comma}
	if d.CRLF {
		w.eol = []byte("\r\n")
	}
	w.buf.Grow(len(utf8BOM) + len(original) + len(saved)/8)
	if d.KeepsBOM() {
		w.buf.Write(utf8BOM)
	}
	head := w.buf.Len()
	if first == 1 {
		w.own(&o, 0)
	}
	lastSaved, lastOwn := -1, int32(first-1)
	for i := range s.recs {
		m := from[i]
		if m < 0 {
			kept.New++
			w.fresh(s.fields(i), o.width)
			continue
		}
		lastSaved, lastOwn = i, max(lastOwn, m)
		of, sf := o.fields(int(m)), s.fields(i)
		if csvSameRecord(of, sf, tabs) {
			kept.Unchanged++
			w.own(&o, int(m))
			continue
		}
		kept.Restored += w.changed(&o, int(m), of, sf, tabs)
	}
	kept.Deleted = len(o.recs) - first - (len(s.recs) - kept.New)
	// Empty lines at the end of the file. ONLYOFFICE stops writing at the
	// last row that has a cell, so nobody deleted them: they are kept, unless
	// rows were added where they stood.
	trail := 0
	if lastSaved == len(s.recs)-1 {
		for i := int(lastOwn) + 1; i < len(o.recs); i++ {
			if !taken[i] && o.recs[i].empty {
				w.own(&o, i)
				trail++
			}
		}
	}
	kept.Deleted -= trail
	kept.Records = w.n
	// The file ends with a line end if and only if it did (ONLYOFFICE always
	// writes one, which says nothing).
	w.finish(o.recs[len(o.recs)-1].term > 0)

	out := w.buf.Bytes()
	if !csvKeepCheck(out[head:], s.in, comma, first, trail) {
		return nil, CSVKept{Why: "check_failed"}
	}
	// A first cell that starts with the three bytes of a byte order mark, at
	// the start of a file that has none: read back, they would be the file's
	// mark and no longer the cell's text.
	if head == 0 && bytes.HasPrefix(out, utf8BOM) {
		return nil, CSVKept{Why: "check_failed"}
	}
	return out, kept
}

// csvKeepCheck is P2, read from the bytes about to be written (both without
// a byte order mark): out, in the file's dialect, has the saved records in
// their order, every cell the saved one or a text ONLYOFFICE writes that way -
// after lead records of the file's own in front (a `sep=` line) and before
// trail empty ones at the end. Both are read a field at a time: what is
// written is checked whatever its records hold.
func csvKeepCheck(out, saved []byte, comma byte, lead, trail int) bool {
	tabs := comma != '\t'
	w := csvReader{in: out, comma: comma}
	s := csvReader{in: saved, comma: ','}
	for ; lead > 0; lead-- {
		if !w.more() {
			return false
		}
		csvSkipRecord(&w)
	}
	for s.more() {
		if !w.more() {
			return false
		}
		// A cell one of the two records does not have is an empty one.
		for wl, sl := false, false; !wl || !sl; {
			var wf, sf csvField
			if !wl {
				wf, wl, _, _ = w.field()
			}
			if !sl {
				sf, sl, _, _ = s.field()
			}
			if !csvSame(wf.val, sf.val, tabs) {
				return false
			}
		}
	}
	for ; trail > 0; trail-- {
		if !w.more() || csvSkipRecord(&w) {
			return false
		}
	}
	return !w.more()
}

// csvSkipRecord reads the record r stands at to its end. filled: a cell of it
// holds something.
func csvSkipRecord(r *csvReader) (filled bool) {
	for {
		f, last, _, _ := r.field()
		filled = filled || len(f.val) > 0
		if last {
			return filled
		}
	}
}

// csvOut writes the records of the kept file. A record's line end is written
// when the next record starts, or at the end if the file has a final one.
type csvOut struct {
	buf   bytes.Buffer
	eol   []byte
	comma byte
	// n records started; at is where the last one's bytes start, term its own
	// line end (none: the dialect's).
	n    int
	at   int
	term []byte
}

func (w *csvOut) begin() {
	if w.n > 0 {
		w.end()
	}
	w.n++
	w.at, w.term = w.buf.Len(), nil
}

func (w *csvOut) end() {
	if len(w.term) > 0 {
		w.buf.Write(w.term)
		return
	}
	w.buf.Write(w.eol)
}

// finish ends the file: with a line end when it had one, and when its last
// record is an empty line, which is no record without one.
func (w *csvOut) finish(final bool) {
	if w.n > 0 && (final || w.buf.Len() == w.at) {
		w.end()
	}
}

// own writes record i of the file as it is: its bytes and its own line end.
func (w *csvOut) own(o *csvSide, i int) {
	w.begin()
	r := o.recs[i]
	w.buf.Write(o.in[r.start:r.end])
	w.term = o.in[r.end : r.end+uint32(r.term)]
}

// changed writes a record somebody changed, field by field: the file's own
// bytes where the saved cell is the same value, the saved value elsewhere. A
// cell the saved record does not have is an empty one (a cleared cell stays
// cleared). It returns how many cells got their own text back over another
// way of writing it.
func (w *csvOut) changed(o *csvSide, i int, of, sf []csvField, tabs bool) (restored int) {
	w.begin()
	n := max(len(of), csvFilled(sf))
	for j := 0; j < n; j++ {
		if j > 0 {
			w.buf.WriteByte(w.comma)
		}
		var sv []byte
		if j < len(sf) {
			sv = sf[j].val
		}
		if j < len(of) && csvSame(of[j].val, sv, tabs) {
			w.buf.Write(of[j].raw)
			if !bytes.Equal(of[j].val, sv) {
				restored++
			}
			continue
		}
		writeCSVField(&w.buf, sv, w.comma)
	}
	r := o.recs[i]
	w.term = o.in[r.end : r.end+uint32(r.term)]
	return restored
}

// fresh writes a saved record with none of the file's behind it. ONLYOFFICE
// pads a row with empty cells up to the widest row it has written so far, so
// the empty ones at its end are dropped down to the file's own width, and no
// further; a record with no cell at all is an empty line.
func (w *csvOut) fresh(sf []csvField, width int) {
	w.begin()
	filled := csvFilled(sf)
	if filled == 0 {
		return
	}
	n := len(sf)
	for n > filled && n > width {
		n--
	}
	for j := 0; j < n; j++ {
		if j > 0 {
			w.buf.WriteByte(w.comma)
		}
		writeCSVField(&w.buf, sf[j].val, w.comma)
	}
}

// csvFilled is the number of fields up to the last one that is not empty.
func csvFilled(f []csvField) int {
	n := len(f)
	for n > 0 && len(f[n-1].val) == 0 {
		n--
	}
	return n
}

// csvSameRecord: every cell of the saved record sf is the cell of of, or what
// ONLYOFFICE writes for it. A cell one of them does not have is an empty one,
// so empty cells at a record's end count for nothing.
func csvSameRecord(of, sf []csvField, tabs bool) bool {
	for j := 0; j < len(of) || j < len(sf); j++ {
		var ov, sv []byte
		if j < len(of) {
			ov = of[j].val
		}
		if j < len(sf) {
			sv = sf[j].val
		}
		if !csvSame(ov, sv, tabs) {
			return false
		}
	}
	return true
}

// ----------------------------------------------------------------- lining up

// csvRec is one record in the index of a CSV.
type csvRec struct {
	// start and end bound its bytes, without the line end. The files compared
	// are at most csvKeepMaxBytes long.
	start, end uint32
	// key is a hash of its cells in their canonical form (csvCanon), the
	// empty ones at its end left out.
	key uint64
	// term is the length of its line end: 0 for a last record without one.
	term uint8
	// empty: no cell holds anything.
	empty bool
}

// csvSide is one of the two files compared: its bytes, an index of its
// records, and a reader that splits one record into fields when asked. The
// fields of a large file are never held all at once.
type csvSide struct {
	in   []byte
	recs []csvRec
	// width is the number of fields of its widest record.
	width int
	rd    csvReader
}

const (
	fnvOffset = 14695981039346656037
	fnvPrime  = 1099511628211
)

// index reads every record once, a field at a time. It answers why it could
// not: "too_many_records", or "too_many_fields" in one of them.
func (c *csvSide) index(in []byte, comma byte, tabs bool) (why string) {
	c.in = in
	c.rd = csvReader{in: in, comma: comma}
	c.recs = make([]csvRec, 0, min(bytes.Count(in, []byte{'\n'})+1, csvKeepMaxRecords))
	scratch := make([]byte, 0, 64)
	r := &c.rd
	for r.more() {
		if len(c.recs) >= csvKeepMaxRecords {
			return "too_many_records"
		}
		start := r.pos
		h, key, empty := uint64(fnvOffset), uint64(fnvOffset), true
		for n := 1; ; n++ {
			if n > csvKeepMaxFields {
				return "too_many_fields"
			}
			f, last, end, term := r.field()
			canon := csvCanon(scratch, f.val, tabs)
			for _, b := range canon {
				h = (h ^ uint64(b)) * fnvPrime
			}
			// The field's end: no byte of UTF-8.
			h = (h ^ 0xFF) * fnvPrime
			if len(canon) > 0 {
				key = h
			}
			if len(f.val) > 0 {
				empty = false
			}
			if last {
				c.width = max(c.width, n)
				c.recs = append(c.recs, csvRec{start: uint32(start), end: uint32(end), key: key, term: uint8(term), empty: empty})
				break
			}
		}
	}
	return ""
}

// fields splits record i (at most csvKeepMaxFields: index saw to it). Valid
// until the next call on this side.
func (c *csvSide) fields(i int) []csvField {
	c.rd.pos = int(c.recs[i].start)
	f, _, _ := c.rd.next()
	return f
}

func (c *csvSide) raw(i int32) []byte { return c.in[c.recs[i].start:c.recs[i].end] }

// csvSepLine reports whether the file's first record is a delimiter hint
// ONLYOFFICE consumed: exactly `sep=` and one character, in a file longer than
// 7 characters (read from its sources). It never writes the line back. A save
// that starts with the line itself had it as a row, and it is lined up like
// any other.
func csvSepLine(o, s *csvSide, tabs bool) bool {
	if len(o.recs) < 2 {
		return false
	}
	line := o.raw(0)
	if len(line) != 5 || string(line[:4]) != "sep=" {
		return false
	}
	if len(o.in) < 32 && utf8.RuneCount(o.in) <= 7 {
		return false
	}
	return len(s.recs) == 0 || !csvSameRecord(o.fields(0), s.fields(0), tabs)
}

// csvTwins are the file's records that share one key.
type csvTwins struct {
	// head is the first of them. at is where they start in csvLineUp's order,
	// the list of the file's records key by key; there they stand in the
	// file's order.
	head, at int32
	// own of them in the file, saved records with the key.
	own, saved int32
	// mixed: they are not all the same bytes. filled: a cell of one of them
	// holds something.
	mixed, filled bool
}

// alike: whichever of them a saved record takes, the same values are written -
// the same bytes, or an empty row for an empty row.
func (g *csvTwins) alike() bool { return !g.mixed || !g.filled }

// csvLineUp finds, for every saved record, the record of the file it came
// from: from[i] is its index, or -1 for a new record; taken marks the file's
// records that have a saved one. Records before first are left out.
//
// Two passes, both linear in the size of the files (but for a binary search):
//
//   - By key, whatever the order (a sort, rows added, rows deleted): a saved
//     record takes a record of the file with its key, the first no other took.
//     Which one matters only when the two sides do not have as many of them,
//     and those keys come after the others, whose pairs they stand between:
//
//     ⚠ Not all the same bytes (`A;007` and `A;7`, one saved `A,7`): none of
//     them is matched. Which look-alike was deleted cannot be told.
//
//     More in the file (one of several rows that read the same was deleted,
//     or edited): the saved record takes the one at its very place when no
//     row was added or deleted between its matched neighbours. Else the first
//     one after the record the matched saved record before it came from - and
//     one further on for every saved record in between that nothing matched
//     and that reads like them (csvShared): that is one of them, edited. The
//     one it leaves behind then stands where the edited row is, and the
//     second pass pairs the two; the first one free would leave the edited
//     row nothing to be paired with.
//
//     More saved (a row was added, or edited, that reads like a row of the
//     file): the file's record goes to the saved one that stands where it
//     does, between the records its matched neighbours came from - at its
//     very place when no row was added or deleted between the two. ⚠ Given
//     to the first saved one in order, a cell somebody typed would be written
//     with the text of a row further down, and that row with the typed text
//     (`6` typed over as `5` above a row that says `05`). Only one that
//     stands between no such neighbours (the rows were moved as well) goes
//     to the first saved record still without one.
//
//   - What is left is paired only on evidence. A saved record nothing matched
//     is compared with the unmatched records of the file that stand between
//     the file's records its matched neighbours came from (only when those
//     two are in the file's order): with the one at its very place first,
//     when no row was added or deleted between the two, then with at most
//     csvKeepPairWindow of them. It is paired with the one that shares
//     strictly more than half of its filled cells, and among several only
//     when no other shares as many (one that is the same bytes aside). No
//     pair: a new record. The reading this costs is bounded (csvKeepPairWork).
//
// A pair is only a guess at which record to take a cell's text from: what is
// written is decided cell by cell (csvSame) and checked (csvKeepCheck).
func csvLineUp(o, s *csvSide, first int, tabs bool) (from []int32, taken []bool) {
	// total is the file's records without the empty lines at its end.
	// ONLYOFFICE never writes those, so they are no row a saved record came
	// from, and counted they would make the end of every file look like a
	// place where rows were deleted.
	total := int32(len(o.recs))
	for int(total) > first && o.recs[total-1].empty {
		total--
	}
	groups := make([]csvTwins, 0, len(o.recs))
	byKey := make(map[uint64]int32, len(o.recs))
	// twin[i] is the group of the file's record i.
	twin := make([]int32, len(o.recs))
	for i := 0; i < len(o.recs); i++ {
		twin[i] = -1
		if i < first {
			continue
		}
		at, rec := int32(i), &o.recs[i]
		gi, ok := byKey[rec.key]
		if !ok {
			gi = int32(len(groups))
			byKey[rec.key] = gi
			groups = append(groups, csvTwins{head: at})
		}
		g := &groups[gi]
		if !g.mixed && !bytes.Equal(o.raw(at), o.raw(g.head)) {
			g.mixed = true
		}
		g.filled = g.filled || !rec.empty
		g.own++
		twin[i] = gi
	}
	// order lists the file's records group after group, each group's in the
	// file's order.
	at := int32(0)
	for gi := range groups {
		groups[gi].at = at
		at += groups[gi].own
	}
	order := make([]int32, at)
	for i := first; i < len(o.recs); i++ {
		g := &groups[twin[i]]
		order[g.at] = int32(i)
		g.at++
	}
	for gi := range groups {
		groups[gi].at -= groups[gi].own
	}
	// skip[k] leads to the first place of order at or after k whose record no
	// saved one took yet (a union-find: stepping over taken ones stays linear).
	skip := make([]int32, len(order)+1)
	for k := range skip {
		skip[k] = int32(k)
	}
	free := func(k int32) int32 { return csvFind(skip, k) }

	group := make([]int32, len(s.recs))
	for i := range s.recs {
		group[i] = -1
		if gi, ok := byKey[s.recs[i].key]; ok {
			group[i] = gi
			groups[gi].saved++
		}
	}
	from = make([]int32, len(s.recs))
	taken = make([]bool, len(o.recs))
	for i := 0; i < first; i++ {
		taken[i] = true
	}
	// take gives saved record i the record at place k of order, when k is one
	// of g's.
	take := func(i int, g *csvTwins, k int32) bool {
		if k < g.at || k >= g.at+g.own {
			return false
		}
		from[i], taken[order[k]], skip[k] = order[k], true, k+1
		return true
	}

	// The keys both sides have as many records of: in order. more and fewer:
	// some key has more of them in the file, or fewer, than were saved.
	more, fewer := false, false
	for i := range s.recs {
		from[i] = -1
		if group[i] < 0 {
			continue
		}
		switch g := &groups[group[i]]; {
		case g.own == g.saved:
			take(i, g, free(g.at))
		case g.alike():
			more, fewer = more || g.own > g.saved, fewer || g.own < g.saved
		}
	}
	// waits answers the group of saved record i when nothing matched it yet
	// and its key has more records in the file than were saved (surplus), or
	// fewer (not surplus).
	waits := func(i int, surplus bool) *csvTwins {
		if from[i] >= 0 || group[i] < 0 {
			return nil
		}
		if g := &groups[group[i]]; g.alike() && g.own != g.saved && (g.own > g.saved) == surplus {
			return g
		}
		return nil
	}
	// inPlace gives the saved records of a run the file's record at their very
	// place, when it has their key: as many records stand between the run's
	// neighbours on both sides, so rows were edited there, none added or
	// deleted.
	inPlace := func(i, end int, before, after int32, surplus bool) {
		if int(after-before) != end-i+1 {
			return
		}
		for j := i; j < end; j++ {
			if p := before + 1 + int32(j-i); twin[p] == group[j] && !taken[p] {
				if g := waits(j, surplus); g != nil {
					take(j, g, g.at+csvRank(order[g.at:g.at+g.own], p))
				}
			}
		}
	}
	if more {
		csvRuns(from, first, total, func(i, end int, before, after int32) bool {
			inPlace(i, end, before, after, true)
			// since: how many saved records nothing matched since the one
			// that came from before.
			for since := 0; i < end; i++ {
				if from[i] >= 0 {
					before, since = from[i], 0
					continue
				}
				g := waits(i, true)
				if g == nil {
					since++
					continue
				}
				last := g.at + g.own
				k := free(g.at + csvRank(order[g.at:last], before+1))
				if k >= last {
					k = free(g.at)
				}
				// An empty row is told from nothing: only rows that hold
				// something are looked for among the saved ones before it, the
				// nearest csvKeepPairWindow of them.
				if g.filled && since > 0 {
					of := o.fields(int(g.head))
					for j := i - 1; j >= i-min(since, csvKeepPairWindow); j-- {
						next := free(k + 1)
						if next >= last {
							break
						}
						if sf := s.fields(j); csvShared(of, sf, csvCells(sf), tabs) > 0 {
							k = next
						}
					}
				}
				if !take(i, g, k) {
					since++
					continue
				}
				before, since = from[i], 0
			}
			return true
		})
	}
	if fewer {
		csvRuns(from, first, total, func(i, end int, before, after int32) bool {
			inPlace(i, end, before, after, false)
			for ; i < end && before < after; i++ {
				if g := waits(i, false); g != nil {
					if k := free(g.at + csvRank(order[g.at:g.at+g.own], before+1)); k < g.at+g.own && order[k] < after {
						take(i, g, k)
					}
				}
			}
			return true
		})
		for i := range s.recs {
			if g := waits(i, false); g != nil {
				take(i, g, free(g.at))
			}
		}
	}

	// The second pass. First the record at a saved record's very place.
	work := csvKeepPairWork*(int64(len(o.in))+int64(len(s.in))) + 1<<20
	csvRuns(from, first, total, func(i, end int, before, after int32) bool {
		if int(after-before) != end-i+1 {
			return true
		}
		for j := i; j < end && work >= 0; j++ {
			p := before + 1 + int32(j-i)
			if taken[p] {
				continue
			}
			work -= int64(len(o.raw(p)))
			if sf := s.fields(j); csvShared(o.fields(int(p)), sf, csvCells(sf), tabs) > 0 {
				from[j], taken[p] = p, true
			}
		}
		return work >= 0
	})
	// gap[i] leads to the first record of the file at or after i that has no
	// saved record yet (the same union-find, over the file's order: the gaps
	// may overlap).
	gap := make([]int32, len(o.recs)+1)
	for i := range gap {
		gap[i] = int32(i)
		if i < len(taken) && taken[i] {
			gap[i] = int32(i + 1)
		}
	}
	csvRuns(from, first, total, func(i, end int, before, after int32) bool {
		// A run of saved records nothing matched, and the gap in the file
		// they may have come from.
		for cur := before + 1; i < end && before < after && work >= 0; i++ {
			sf := s.fields(i)
			filled := csvCells(sf)
			if filled == 0 {
				// A cleared row: nothing to tell it by.
				continue
			}
			best, most, tie := int32(-1), 0, false
			c := csvFind(gap, cur)
			for n := 0; n < csvKeepPairWindow && c < after && work >= 0; n++ {
				work -= int64(len(o.raw(c)))
				switch shared := csvShared(o.fields(int(c)), sf, filled, tabs); {
				case shared > most:
					best, most, tie = c, shared, false
				case shared == most && shared > 0 && !bytes.Equal(o.raw(c), o.raw(best)):
					// As many as another, unless the two are the same bytes:
					// then the first of them is as good as the second.
					tie = true
				}
				c = csvFind(gap, c+1)
			}
			if best < 0 || tie {
				continue
			}
			from[i], taken[best], gap[best] = best, true, best+1
			cur = best + 1
		}
		return work >= 0
	})
	return from, taken
}

// csvRuns calls visit for every run of saved records nothing matched: the
// records [i, end), and before and after, the file's records the matched saved
// records around the run came from (first-1 when none is in front of it, total
// when it ends the save). The file's records the run may have come from stand
// between the two, when the two are in the file's order. visit may match
// records of its run, and stops the walk by answering false.
func csvRuns(from []int32, first int, total int32, visit func(i, end int, before, after int32) bool) {
	for i, before := 0, int32(first-1); i < len(from); {
		if from[i] >= 0 {
			before = from[i]
			i++
			continue
		}
		end := i
		for end < len(from) && from[end] < 0 {
			end++
		}
		after := total
		if end < len(from) {
			after = from[end]
		}
		if !visit(i, end, before, after) {
			return
		}
		i = end
	}
}

// csvFind is the union-find behind csvLineUp's skip and gap: the first place
// at or after i that leads to itself, with the path there shortened.
func csvFind(next []int32, i int32) int32 {
	root := i
	for next[root] != root {
		root = next[root]
	}
	for next[i] != root {
		next[i], i = root, next[i]
	}
	return root
}

// csvRank is how many of the ascending list are below x.
func csvRank(list []int32, x int32) int32 {
	lo, hi := 0, len(list)
	for lo < hi {
		if mid := (lo + hi) / 2; list[mid] < x {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return int32(lo)
}

// csvCells is how many cells of a record hold something.
func csvCells(f []csvField) (filled int) {
	for i := range f {
		if len(f[i].val) > 0 {
			filled++
		}
	}
	return filled
}

// csvShared counts the filled cells of the saved record sf whose cell at the
// same place in of is the same value. 0 when it is not strictly more than
// half of them (filled): no pair is made on less.
func csvShared(of, sf []csvField, filled int, tabs bool) int {
	shared, left := 0, filled
	for j := 0; j < len(sf) && 2*(shared+left) > filled; j++ {
		if len(sf[j].val) == 0 {
			continue
		}
		left--
		if j < len(of) && csvSame(of[j].val, sf[j].val, tabs) {
			shared++
		}
	}
	if 2*shared <= filled {
		return 0
	}
	return shared
}

// ------------------------------------------- what ONLYOFFICE writes for a cell

// csvSame reports whether the saved cell s is the file's cell o: the same
// text, or exactly what ONLYOFFICE writes for an untouched cell whose text is
// o. One way only (P1): csvSame("007", "7") and never csvSame("7", "007").
// tabs: the file's delimiter is not a tab.
//
// What ONLYOFFICE writes another way, and nothing else (measured on Docs
// 9.4.0-129, csv_keep_measured_test.go):
//
//   - R1, a whole number: `007` as `7`, `05320000001` as `5320000001`, `+5`
//     as `5`, ` 42` and `42 ` as `42`, `0x10` as `16`; past 2^53 the double
//     with 17 significant digits, `9007199254740993` as `9007199254740992`,
//     `123456789012345678` as `1.2345678901234568e+17`; from 2^63 up, either
//     way, `-9.2233720368547758e+18` (csvWholeOverflow). No exponent: `1e3`
//     comes back as `1000.00000`.
//   - R2, a number with a point, kept to six decimals and written with as
//     many as the text had: `03.50` as `3.50`, `0.1234567` as `0.1234570`;
//     with nothing before the point, `.5` as `.0` and `-.5` as `-0.5`.
//     Never a comma: `3,5` is text to ONLYOFFICE, in English and in Turkish.
//   - R3, a date with a four-digit year from 1900 on, as the editor's
//     language writes it (csvDateWritten): `01.02.2026` as `1/2/2026` in
//     English and as `1.02.2026` in Turkish, `2026-02-01` as `2/1/2026` and
//     `1.02.2026`.
//   - R4: `true` as `TRUE`, `false` as `FALSE`; exactly lower case (`True`
//     comes back as it was).
//   - R5, R6, text: every tab gone, unless tabs are the delimiter; a text
//     longer than 32767 UTF-16 code units cut there (`x` and `ş` 40000 times
//     came back 32767 times).
//
// Everything else - a time, a percent, an exponent, a date with a time or a
// two-digit year, a formula, a line end in a quoted cell (it comes back as it
// was) - is compared as text, so ONLYOFFICE's value is written.
func csvSame(o, s []byte, tabs bool) bool {
	if bytes.Equal(o, s) {
		return true
	}
	if len(o) == 0 {
		return false
	}
	var b [48]byte
	switch c := o[0]; {
	case c >= '0' && c <= '9', c == '+', c == '-', c == ' ':
		v, ok, huge := csvWhole(o)
		if ok && bytes.Equal(appendCSVWhole(b[:0], v), s) {
			return true
		}
		if huge && string(s) == csvWholeOverflow {
			return true
		}
		if dec, ok := appendCSVDecimal(b[:0], o); ok && bytes.Equal(dec, s) {
			return true
		}
		if c == '-' && csvPointFirst(o[1:]) && len(s) == len(o)+1 && string(s[:2]) == "-0" && bytes.Equal(s[2:], o[1:]) {
			return true
		}
		if od, ok := csvReadDate(o); ok && csvDateWritten(b[:0], od, s) {
			return true
		}
	case c == '.':
		if csvPointFirst(o) && bytes.Equal(appendCSVPointZeros(b[:0], len(o)-1), s) {
			return true
		}
	case c == 't':
		if string(o) == "true" && string(s) == "TRUE" {
			return true
		}
	case c == 'f':
		if string(o) == "false" && string(s) == "FALSE" {
			return true
		}
	}
	t, ok := csvText(o, tabs)
	return ok && bytes.Equal(t, s)
}

// csvCanon is a cell's canonical form, the same for a text and for what
// ONLYOFFICE writes for it, and for more than that: it is symmetric, so it
// only ever says which record of the file a saved record may have come from
// (the key of csvRec), never what is written. It is v itself, or bytes
// appended to dst.
func csvCanon(dst, v []byte, tabs bool) []byte {
	if len(v) == 0 {
		return v
	}
	switch c := v[0]; {
	case c >= '0' && c <= '9', c == '+', c == '-', c == ' ':
		n, ok, huge := csvWhole(v)
		if huge || (ok && math.Abs(float64(n)) >= 0x1p63) {
			// The text ONLYOFFICE writes for all of them, which is also
			// what the saved side comes to.
			return append(dst, csvWholeOverflow...)
		}
		if ok {
			return appendCSVWhole(append(dst, 'n', ':'), n)
		}
		if dec, ok := appendCSVDecimal(append(dst, 'n', ':'), v); ok {
			return dec
		}
		if c == '-' && csvPointFirst(v[1:]) {
			if dec, ok := appendCSVDecimal(append(dst, 'n', ':'), append([]byte("-0"), v[1:]...)); ok {
				return dec
			}
		}
		// A date, its day and month in either order: a text written year
		// first and the way the editor's language writes it have them in
		// different orders (csvDateWritten).
		if d, ok := csvReadDate(v); ok && d.real() {
			dst = strconv.AppendInt(append(dst, 'd', ':'), int64(min(d.a, d.b)), 10)
			dst = strconv.AppendInt(append(dst, ','), int64(max(d.a, d.b)), 10)
			return strconv.AppendInt(append(dst, ','), int64(d.y), 10)
		}
		if f, ok := csvWholeDouble(v); ok {
			return strconv.AppendFloat(append(dst, 'n', ':'), f, 'g', 17, 64)
		}
	case c == '.':
		if csvPointFirst(v) {
			return appendCSVPointZeros(dst, len(v)-1)
		}
	case len(v) == 4 && bytes.EqualFold(v, []byte("true")):
		return append(dst, "TRUE"...)
	case len(v) == 5 && bytes.EqualFold(v, []byte("false")):
		return append(dst, "FALSE"...)
	}
	if (!tabs || bytes.IndexByte(v, '\t') < 0) && bytes.IndexByte(v, '\r') < 0 {
		return v
	}
	for _, c := range v {
		if c == '\t' && tabs {
			continue
		}
		if c == '\n' && len(dst) > 0 && dst[len(dst)-1] == '\r' {
			dst = dst[:len(dst)-1]
		}
		dst = append(dst, c)
	}
	return dst
}

func csvDigit(c byte) bool { return c >= '0' && c <= '9' }

// csvTrimNumber takes off what the reader's number test skips: the spaces in
// front, and one after.
func csvTrimNumber(o []byte) []byte {
	for len(o) > 0 && o[0] == ' ' {
		o = o[1:]
	}
	if n := len(o); n > 0 && o[n-1] == ' ' {
		o = o[:n-1]
	}
	return o
}

// csvSign takes a sign off the front of t.
func csvSign(t []byte) (neg bool, rest []byte) {
	if len(t) > 0 && (t[0] == '+' || t[0] == '-') {
		return t[0] == '-', t[1:]
	}
	return false, t
}

// csvWhole reads o as ONLYOFFICE's reader reads a whole number (R1): digits
// or `0x` digits, with a sign, after the spaces in front and before one
// after. ok only when its value is below 2^63; huge when it is decimal digits
// whose value is 2^63 or more either way (csvWholeOverflow). ⚠ No exponent:
// `1e3` comes back as `1000.00000` (measured), a value no rule writes.
func csvWhole(o []byte) (v int64, ok, huge bool) {
	neg, t := csvSign(csvTrimNumber(o))
	if len(t) == 0 {
		return 0, false, false
	}
	var mag uint64
	if len(t) > 2 && t[0] == '0' && (t[1] == 'x' || t[1] == 'X') {
		mag, ok = csvHex(t[2:])
	} else {
		d := 0
		for d < len(t) && csvDigit(t[d]) {
			d++
		}
		if d == 0 || d != len(t) {
			return 0, false, false
		}
		if mag, ok = csvDecimalValue(t); !ok || mag > math.MaxInt64 {
			return 0, false, true
		}
	}
	if !ok || mag > math.MaxInt64 {
		return 0, false, false
	}
	if neg {
		return -int64(mag), true, false
	}
	return int64(mag), true, false
}

// csvWholeOverflow is what ONLYOFFICE writes for a whole number whose double
// is 2^63 or more either way, in every editor language measured: the smallest
// int64 (`12345678901234567890`, `-12345678901234567890`, a 24-digit number,
// `000012345678901234567890` and `9223372036854775807` itself all came back
// as this). A long account or reference number is lost to it.
const csvWholeOverflow = "-9.2233720368547758e+18"

// csvPointFirst reports whether v is a point and digits, `.5`: ONLYOFFICE
// writes it as a point and as many zeros (measured: `.5` as `.0`, `.25` as
// `.00`), and after a minus sign with a zero in front (`-.5` as `-0.5`).
func csvPointFirst(v []byte) bool {
	if len(v) < 2 || v[0] != '.' {
		return false
	}
	for _, c := range v[1:] {
		if !csvDigit(c) {
			return false
		}
	}
	return true
}

// appendCSVPointZeros appends a point and n zeros.
func appendCSVPointZeros(dst []byte, n int) []byte {
	dst = append(dst, '.')
	for ; n > 0; n-- {
		dst = append(dst, '0')
	}
	return dst
}

// csvDecimalValue is the value of decimal digits, when it has at most 19
// digits after its leading zeros.
func csvDecimalValue(digits []byte) (uint64, bool) {
	for len(digits) > 0 && digits[0] == '0' {
		digits = digits[1:]
	}
	if len(digits) > 19 {
		return 0, false
	}
	var v uint64
	for _, c := range digits {
		v = v*10 + uint64(c-'0')
	}
	return v, true
}

func csvHex(h []byte) (uint64, bool) {
	if len(h) == 0 {
		return 0, false
	}
	for len(h) > 1 && h[0] == '0' {
		h = h[1:]
	}
	if len(h) > 16 {
		return 0, false
	}
	var v uint64
	for _, c := range h {
		switch {
		case csvDigit(c):
			v = v<<4 | uint64(c-'0')
		case c >= 'a' && c <= 'f':
			v = v<<4 | uint64(c-'a'+10)
		case c >= 'A' && c <= 'F':
			v = v<<4 | uint64(c-'A'+10)
		default:
			return 0, false
		}
	}
	return v, true
}

// appendCSVWhole writes v as ONLYOFFICE's writer does: the double nearest to
// it with 17 significant digits (C's %.17g). Up to 2^53 that is its digits.
func appendCSVWhole(dst []byte, v int64) []byte {
	if v >= -1<<53 && v <= 1<<53 {
		return strconv.AppendInt(dst, v, 10)
	}
	if f := float64(v); f >= 0x1p63 {
		// Rounded up to 2^63, past the integer the writer converts it to.
		return append(dst, csvWholeOverflow...)
	}
	return strconv.AppendFloat(dst, float64(v), 'g', 17, 64)
}

// csvWholeDouble reads v as the writer prints a whole number too large for
// its digits, `1.2345678901234568e+17`. For csvCanon only: the saved side of
// R1 above 2^53.
func csvWholeDouble(v []byte) (float64, bool) {
	_, t := csvSign(v)
	if len(t) < 6 || !csvDigit(t[0]) || t[1] != '.' {
		return 0, false
	}
	i := 2
	for i < len(t) && csvDigit(t[i]) {
		i++
	}
	if i == 2 || i+2 >= len(t) || t[i] != 'e' || t[i+1] != '+' {
		return 0, false
	}
	for _, c := range t[i+2:] {
		if !csvDigit(c) {
			return 0, false
		}
	}
	f, err := strconv.ParseFloat(string(v), 64)
	if err != nil || f != math.Trunc(f) || math.Abs(f) >= 1<<63 {
		return 0, false
	}
	return f, true
}

// appendCSVDecimal reads o as a number with a point (R2: digits, a point,
// digits, with a sign) and appends what ONLYOFFICE writes for it: the value
// kept to six decimals (std::to_wstring), written with as many decimals as
// the text had.
func appendCSVDecimal(dst, o []byte) ([]byte, bool) {
	t := csvTrimNumber(o)
	neg, body := csvSign(t)
	p := 0
	for p < len(body) && csvDigit(body[p]) {
		p++
	}
	if p == 0 || p == len(body) || body[p] != '.' {
		return dst, false
	}
	whole, frac := body[:p], body[p+1:]
	if len(frac) == 0 || len(frac) > csvDecimalMaxDigits {
		return dst, false
	}
	for _, c := range frac {
		if !csvDigit(c) {
			return dst, false
		}
	}
	for len(whole) > 1 && whole[0] == '0' {
		whole = whole[1:]
	}
	if len(frac) <= 6 && len(whole) <= 9 {
		// Six decimals and a double hold it exactly: its own digits.
		if neg {
			dst = append(dst, '-')
		}
		dst = append(append(dst, whole...), '.')
		return append(dst, frac...), true
	}
	f, err := strconv.ParseFloat(string(t), 64)
	if err != nil {
		return dst, false
	}
	var b [32]byte
	six, err := strconv.ParseFloat(string(strconv.AppendFloat(b[:0], f, 'f', 6, 64)), 64)
	if err != nil {
		return dst, false
	}
	return strconv.AppendFloat(dst, six, 'f', len(frac), 64), true
}

// csvDate is a text of the shape p1 SEP p2 SEP yyyy, or yyyy-mm-dd.
type csvDate struct {
	a, b, y int
	// iso: the text was yyyy-mm-dd, a is its month and b its day.
	iso bool
}

// csvReadDate reads v as two numbers of one or two digits and a four-digit
// year, with `.`, `/` or `-` between them, the same one twice; or as an ISO
// 8601 date, yyyy-mm-dd.
func csvReadDate(v []byte) (d csvDate, ok bool) {
	n := len(v)
	if n == 10 && v[4] == '-' && v[7] == '-' {
		for i, c := range v {
			if i != 4 && i != 7 && !csvDigit(c) {
				return d, false
			}
		}
		num := func(b []byte) (x int) {
			for _, c := range b {
				x = x*10 + int(c-'0')
			}
			return x
		}
		return csvDate{a: num(v[5:7]), b: num(v[8:10]), y: num(v[:4]), iso: true}, true
	}
	if n < 8 || n > 10 {
		return d, false
	}
	i := 0
	for i < 2 && csvDigit(v[i]) {
		d.a = d.a*10 + int(v[i]-'0')
		i++
	}
	sep := v[i]
	if i == 0 || (sep != '.' && sep != '/' && sep != '-') {
		return d, false
	}
	i++
	lb := 0
	for lb < 2 && csvDigit(v[i]) {
		d.b = d.b*10 + int(v[i]-'0')
		lb++
		i++
	}
	if lb == 0 || v[i] != sep || n-i-1 != 4 {
		return d, false
	}
	for _, c := range v[i+1:] {
		if !csvDigit(c) {
			return d, false
		}
		d.y = d.y*10 + int(c-'0')
	}
	return d, true
}

// real reports whether ONLYOFFICE reads the text as a date at all: one of the
// two numbers is a month and the other a day of it, in a year a spreadsheet
// has (from 1900). Anything else stays text, and comes back as it was.
func (d csvDate) real() bool {
	return d.y >= 1900 && (csvDayOf(d.a, d.b, d.y) || csvDayOf(d.b, d.a, d.y))
}

// csvDateWritten reports whether s is the date d as ONLYOFFICE's writer
// prints it, with the same three numbers in the same order (R3). Measured on
// Docs 9.4.0-129, a file of dates with `.`, `/` and `-` between their
// numbers, one unrelated cell edited:
//
//   - the editor in English reads the first number as the month and writes
//     month/day/year without leading zeros: `01.02.2026`, `01/02/2026` and
//     `1-2-2026` as `1/2/2026`; `15.03.2026` stays as it was (no 15th month);
//   - the editor in Turkish reads the first number as the day and writes
//     day.month.year, the month in two digits: `01.02.2026`, `1/2/2026` and
//     `1-2-2026` as `1.02.2026`, `15/3/2026` as `15.03.2026`;
//   - both read an ISO 8601 date as one: `2026-02-01` as `2/1/2026` and as
//     `1.02.2026`.
//
// ⚠ Nothing else is the date written ONLYOFFICE's way, however close: a text
// ONLYOFFICE did not read as a date comes back as it was, and a person who
// types `15/3/2026` over it in the English editor saves that text. Called the
// same, it would put `15.03.2026` back over their edit (measured: the English
// editor keeps a typed `15/3/2026`, `13/1/2026`, `01.02.2026` or `1.2.2026`
// as typed).
func csvDateWritten(buf []byte, d csvDate, s []byte) bool {
	if d.y < 1900 {
		// The writer's arithmetic starts at 1900: `01.02.1850` came back as
		// `1/2/3750` (measured). No rule.
		return false
	}
	// The two languages read p1 SEP p2 SEP yyyy in two orders, and
	// yyyy-mm-dd in one.
	enMonth, enDay, trDay, trMonth := d.a, d.b, d.a, d.b
	if d.iso {
		trDay, trMonth = d.b, d.a
	}
	// ⚠ An empty rendering is no date: never the same as a cleared cell.
	if w := appendCSVDateEN(buf[:0], enMonth, enDay, d.y); len(w) > 0 && bytes.Equal(w, s) {
		return true
	}
	w := appendCSVDateTR(buf[:0], trDay, trMonth, d.y)
	return len(w) > 0 && bytes.Equal(w, s)
}

// appendCSVDateEN appends the date as the editor in English writes it,
// month/day/year without leading zeros, or nothing when it is no date.
func appendCSVDateEN(dst []byte, month, day, year int) []byte {
	if !csvDayOf(month, day, year) {
		return dst
	}
	dst = strconv.AppendInt(dst, int64(month), 10)
	dst = strconv.AppendInt(append(dst, '/'), int64(day), 10)
	return strconv.AppendInt(append(dst, '/'), int64(year), 10)
}

// appendCSVDateTR appends the date as the editor in Turkish writes it,
// day.month.year with the month in two digits, or nothing when it is no date.
func appendCSVDateTR(dst []byte, day, month, year int) []byte {
	if !csvDayOf(month, day, year) {
		return dst
	}
	dst = append(strconv.AppendInt(dst, int64(day), 10), '.')
	if month < 10 {
		dst = append(dst, '0')
	}
	dst = strconv.AppendInt(dst, int64(month), 10)
	return strconv.AppendInt(append(dst, '.'), int64(year), 10)
}

// csvDayOf reports whether day is a day of month in year.
func csvDayOf(month, day, year int) bool {
	if month < 1 || month > 12 || day < 1 {
		return false
	}
	switch month {
	case 2:
		if year%4 == 0 && (year%100 != 0 || year%400 == 0) {
			return day <= 29
		}
		return day <= 28
	case 4, 6, 9, 11:
		return day <= 30
	}
	return day <= 31
}

// csvText is what ONLYOFFICE writes for o as text (R5, R6): its tabs gone
// (unless they are the delimiter), cut at 32767 UTF-16 code units (measured).
// ok is false when that is o itself, or when the cut would fall inside a
// character. A "\r\n" inside a quoted cell comes back as it was (measured).
func csvText(o []byte, tabs bool) (t []byte, ok bool) {
	tabs = tabs && bytes.IndexByte(o, '\t') >= 0
	if !tabs && len(o) <= csvCellMaxUnits {
		return nil, false
	}
	t = o
	if tabs {
		t = bytes.ReplaceAll(t, []byte{'\t'}, nil)
	}
	if len(t) > csvCellMaxUnits {
		units := 0
		for i := 0; i < len(t); {
			r, size := utf8.DecodeRune(t[i:])
			u := 1
			if r >= 0x10000 {
				u = 2
			}
			if units+u > csvCellMaxUnits {
				if units < csvCellMaxUnits {
					return nil, false
				}
				t = t[:i]
				break
			}
			units += u
			i += size
		}
	}
	return t, !bytes.Equal(t, o)
}
