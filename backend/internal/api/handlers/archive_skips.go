package handlers

import (
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/brf-tech/filex/backend/internal/archivecli"
	"github.com/brf-tech/filex/backend/internal/leafname"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// Why an extraction left a member out for its NAME (task #238). Said one by
// one in the job's answer - `skipped: [{name, reason}]` - so "8 of 10
// extracted" comes with the two names and why.
const (
	// skipUnsafePath: the member's path leaves the destination (`../x`, an
	// absolute path, a drive letter) - archivecli.CleanMemberPath refused it.
	skipUnsafePath = "unsafe_path"
	// skipNameNotAllowed: a name no door of filex lets a file be given
	// (leafname: a NUL, a control character, `...`).
	skipNameNotAllowed = "name_not_allowed"
	// skipNameTooLong: longer than the storage (or the extraction's own
	// workspace disk) holds - over 255 bytes in one name on a Linux disk.
	skipNameTooLong = "name_too_long"
)

// memberSkipsShown bounds the list an answer carries; the rest are counted in
// `skipped_more`. An archive of ten thousand bad names must not become a
// ten-thousand-row answer.
const memberSkipsShown = 100

// skippedMember is one member left out: its name inside the archive, and why.
type skippedMember struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// memberSkips is what one extraction left out for its names.
type memberSkips struct {
	List []skippedMember
	More int
}

func (s *memberSkips) add(name, reason string) {
	if len(s.List) < memberSkipsShown {
		s.List = append(s.List, skippedMember{Name: name, Reason: reason})
		return
	}
	s.More++
}

// count is every member left out, listed or not.
func (s memberSkips) count() int { return len(s.List) + s.More }

// fields is the list as an answer carries it: `skipped` (never null) and
// `skipped_more`.
func (s memberSkips) fields(into map[string]any) map[string]any {
	list := s.List
	if list == nil {
		list = []skippedMember{}
	}
	into["skipped"] = list
	into["skipped_more"] = s.More
	return into
}

// skipsFromResult reads back what fields wrote.
func skipsFromResult(res map[string]any) memberSkips {
	list, _ := res["skipped"].([]skippedMember)
	more, _ := res["skipped_more"].(int)
	return memberSkips{List: list, More: more}
}

// reasonOfWrite is the skip reason of a member whose WRITE failed for its
// name, "" when the failure is something else.
func reasonOfWrite(err error) string {
	switch {
	case storage.IsNameTooLong(err):
		return skipNameTooLong
	case err != nil && leafname.Reason(err) != "":
		return skipNameNotAllowed
	}
	return ""
}

// archiveSkipsError is a queued extraction that FINISHED and left members out
// for their names: the members it could write are written. It is an error
// only so the job queue can tell it from a clean success - the op row ends
// `partial` with this text and the count (ops.Service.executeJob reads
// Skips()), which is where the tray and the toast say what was left out.
type archiveSkipsError struct{ skips memberSkips }

// archiveSkipsNamed is how many names the text spells out; the op row's
// error column is a toast, not a report.
const archiveSkipsNamed = 5

func (e *archiveSkipsError) Error() string {
	n := e.skips.count()
	var b strings.Builder
	if n == 1 {
		b.WriteString("extracted, but 1 member was left out: ")
	} else {
		fmt.Fprintf(&b, "extracted, but %d members were left out: ", n)
	}
	for i, s := range e.skips.List {
		if i == archiveSkipsNamed {
			fmt.Fprintf(&b, ", and %d more", n-archiveSkipsNamed)
			break
		}
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q (%s)", s.Name, skipReasonWords(s.Reason))
	}
	return b.String()
}

// Skips is how many members were left out - what the op row counts.
func (e *archiveSkipsError) Skips() int { return e.skips.count() }

func skipReasonWords(reason string) string {
	switch reason {
	case skipUnsafePath:
		return "its path leaves the destination"
	case skipNameTooLong:
		return "the name is too long for this storage"
	default:
		return "a name filex does not allow"
	}
}

// longerThanHostHolds reports a member path one of whose names this server's
// own disk cannot hold - the extraction workspace 7-Zip and the TAR reader
// write into: over 255 bytes on Linux and macOS, over 255 UTF-16 units on
// Windows. Such a member is left out of the extraction and listed, instead
// of failing the whole job.
func longerThanHostHolds(rel string, windows bool) bool {
	for _, seg := range strings.Split(rel, "/") {
		n := len(seg)
		if windows {
			n = len(utf16.Encode([]rune(seg)))
		}
		if n > 255 {
			return true
		}
	}
	return false
}

// externalMembers is what 7-Zip (or the TAR reader) is asked to extract, and
// the members left out because longerThanHostHolds. With nothing too long it
// is the caller's own selection, unchanged (nil: everything). Otherwise it is
// every FILE of the selection (a selected folder is its files) but the long
// ones, named one by one: the extractor fails a whole run on one name its disk
// cannot hold.
func externalMembers(entries []archivecli.Entry, wanted []string, windows bool) (members, long []string) {
	selected := func(name string) bool {
		if len(wanted) == 0 {
			return true
		}
		for _, w := range wanted {
			w = strings.TrimSuffix(w, "/")
			if name == w || strings.HasPrefix(name, w+"/") {
				return true
			}
		}
		return false
	}
	var files []string
	for _, e := range entries {
		if !selected(e.Name) {
			continue
		}
		if longerThanHostHolds(e.Name, windows) {
			long = append(long, e.Name)
			continue
		}
		if !e.IsDir {
			files = append(files, e.Name)
		}
	}
	if len(long) == 0 {
		return wanted, nil
	}
	return files, long
}

// memberBatchBytes bounds one extractor command line's member names: Windows
// refuses a command line over 32767 characters.
const memberBatchBytes = 24 << 10

// memberBatches splits members into runs of at most limit bytes of names (a
// name longer than limit is a run of its own). nil members is one run of nil:
// extract everything.
func memberBatches(members []string, limit int) [][]string {
	if len(members) == 0 {
		return [][]string{nil}
	}
	var out [][]string
	var cur []string
	size := 0
	for _, m := range members {
		if len(cur) > 0 && size+len(m)+1 > limit {
			out = append(out, cur)
			cur, size = nil, 0
		}
		cur = append(cur, m)
		size += len(m) + 1
	}
	return append(out, cur)
}
