package filesync

import (
	"os"
	"strings"
	"testing"
)

// preparingJSON is, byte for byte in shape, what 45 files on a real deployment
// held under their own names after a v0.20–v0.42 server answered a download of
// a big file on a slow storage with 202 and the client wrote the answer to disk.
const preparingJSON = `{"name":"poster v02.psd","percent":0,"ready":false,"size":151983227,"state":"preparing"}`

// leftovers lists the engine's temporary download files in the pair folder.
func (r *rig) leftovers() []string {
	r.t.Helper()
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		r.t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".filex-part-") {
			out = append(out, e.Name())
		}
	}
	return out
}

// H1: the listing is the contract. A body of any other length is never
// installed under the file's name — so a later run cannot mistake it for a
// local edit and upload it over the real file.
func TestABodyThatIsNotTheListedFileIsNeverInstalled(t *testing.T) {
	r := newRig(t)
	real := strings.Repeat("8BPS", 250)
	r.writeRemote("poster.psd", real)
	r.srv.served = map[string][]byte{"poster.psd": []byte(preparingJSON)}

	res := r.run()

	if r.localExists("poster.psd") {
		t.Fatalf("a %d-byte body was installed for a file listed at %d bytes", len(preparingJSON), len(real))
	}
	if len(res.Errors) == 0 {
		t.Fatal("the refused download must be reported, not swallowed")
	}
	if left := r.leftovers(); len(left) != 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}

	// Nothing is left for the next run to upload over the server's copy.
	res = r.run()
	if res.Uploaded != 0 {
		t.Fatalf("uploaded %d file(s) after a refused download", res.Uploaded)
	}
	if got := string(r.srv.files["poster.psd"]); got != real {
		t.Fatalf("the server's copy changed: %q", got)
	}

	// Once the server sends the file, it arrives.
	delete(r.srv.served, "poster.psd")
	r.run()
	if got := r.readLocal("poster.psd"); got != real {
		t.Fatalf("after the server recovered: got %d bytes, want %d", len(got), len(real))
	}
}

// H1, the conflict half of the chain: a conflict downloads the server's copy
// first. If that body is wrong, the local file must NOT go up over the server's.
func TestAConflictWhoseServerCopyIsWrongUploadsNothing(t *testing.T) {
	r := newRig(t)
	r.writeLocal("report.txt", "v1")
	r.run()

	r.writeLocal("report.txt", "my edit")
	r.writeRemote("report.txt", "their edit")
	r.srv.served = map[string][]byte{"report.txt": []byte(preparingJSON)}
	res := r.run()

	if got := string(r.srv.files["report.txt"]); got != "their edit" {
		t.Fatalf("the server's copy was overwritten although its download failed: %q", got)
	}
	if res.Uploaded != 0 {
		t.Fatalf("uploaded %d file(s)", res.Uploaded)
	}
	if got := r.readLocal("report.txt"); got != "my edit" {
		t.Fatalf("the local file changed: %q", got)
	}
}
