package notify_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/leafname"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/testutil/hostilenames"
)

var whiteRun = regexp.MustCompile(`\s{2,}`)

// Every hostile name (task #238) as the file a notification is about: the
// sentence names it, and no bidi control from a name reaches the bell, a push,
// an email or the desktop's toast - `invoice<RLO>fdp.exe` cannot reverse the
// sentence and show itself as `invoiceexe.pdf`. The isolates a right-to-left
// sentence adds around a value are the composer's own and stay.
func TestSay_AHostileNameNeverTurnsTheSentence(t *testing.T) {
	for _, n := range hostilenames.Creatable() {
		n := n
		t.Run(n.ID, func(t *testing.T) {
			row := sayRow("file.uploaded", "file.uploaded", "Gelen/"+n.Name, map[string]any{
				"node": map[string]any{"path": "Gelen/" + n.Name, "name": n.Name},
			})
			for _, lang := range []string{"en", "tr"} {
				s := notify.Say(lang, row)
				for _, r := range s.Title + s.Body {
					if leafname.IsBidiControl(r) && r != 0x2068 && r != 0x2069 {
						t.Errorf("%s: U+%04X reached %q / %q", lang, r, s.Title, s.Body)
					}
				}
				// A sentence is one line of words: a run of white space in it,
				// the name's own included, is one space (that is how a comment's
				// line breaks become a line), and it has no space at either end.
				want := strings.TrimSpace(whiteRun.ReplaceAllString(leafname.Visible(n.Name), " "))
				if !strings.Contains(s.Title, want) {
					t.Errorf("%s: the title %q does not name %q", lang, s.Title, want)
				}
			}
		})
	}
}
