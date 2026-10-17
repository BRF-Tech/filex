package httpx

import (
	"mime"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/brf-tech/filex/backend/internal/testutil/hostilenames"
)

// Every hostile name (task #238) as a download: the header parses as a
// Content-Disposition (that it is ASCII, TestContentDisposition_IsAlwaysASCII
// walks the same list), and the name a client saves is the file's own name -
// except the runes no saved name may carry (a path separator, a control
// character, a bidi control), each replaced one-for-one by `_`.
func TestContentDisposition_HostileNames(t *testing.T) {
	for _, n := range hostilenames.All() {
		n := n
		t.Run(n.ID, func(t *testing.T) {
			got := ContentDisposition("attachment", n.Name)
			disp, params, err := mime.ParseMediaType(got)
			if err != nil {
				t.Fatalf("%q does not parse: %v", got, err)
			}
			if disp != "attachment" {
				t.Fatalf("disposition %q", disp)
			}
			saved := params["filename"]
			want := strings.TrimSpace(n.Name)
			if want == "" {
				want = "file"
			}
			if utf8.RuneCountInString(saved) != utf8.RuneCountInString(want) {
				t.Fatalf("saved as %q (%d runes), the name is %q (%d runes)", saved, utf8.RuneCountInString(saved), want, utf8.RuneCountInString(want))
			}
			sr, wr := []rune(saved), []rune(want)
			for i := range wr {
				bad := wr[i] == '/' || wr[i] == '\\' || unicode.IsControl(wr[i]) || unicode.Is(unicode.Bidi_Control, wr[i])
				switch {
				case bad && sr[i] != '_':
					t.Errorf("rune %d (U+%04X) reached the saved name as U+%04X", i, wr[i], sr[i])
				case !bad && sr[i] != wr[i]:
					t.Errorf("rune %d: saved U+%04X, the name has U+%04X", i, sr[i], wr[i])
				}
			}
		})
	}
}

// A plain-ASCII name with a `%` in it travels in filename* too: Chromium
// percent-decodes the plain parameter and saved `100%20off.txt` as
// `100 off.txt`.
func TestContentDisposition_PercentNamesCarryTheExtendedForm(t *testing.T) {
	for _, id := range []string{"percent-20", "percent-alone", "percent-2f"} {
		n := hostilenames.Get(id)
		got := ContentDisposition("attachment", n.Name)
		if !strings.Contains(got, "filename*=UTF-8''"+rfc5987Escape(n.Name)) {
			t.Errorf("%s: %q has no filename* for %q", id, got, n.Name)
		}
	}
	if got := ContentDisposition("attachment", "rapor.txt"); strings.Contains(got, "filename*") {
		t.Errorf("a plain name grew a second copy: %q", got)
	}
}

// The RLO name, spelled out: what a client saves shows its true extension.
func TestContentDisposition_RLONeverReachesTheSavedName(t *testing.T) {
	n := hostilenames.Get("rlo-extension")
	_, params, err := mime.ParseMediaType(ContentDisposition("attachment", n.Name))
	if err != nil {
		t.Fatal(err)
	}
	if got := params["filename"]; got != "invoice_fdp.exe" {
		t.Fatalf("saved as %q, want invoice_fdp.exe", got)
	}
}
