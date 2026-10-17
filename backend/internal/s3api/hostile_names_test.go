package s3api_test

// The hostile names (task #238, internal/testutil/hostilenames) through the S3
// endpoint: every name a local disk holds is put, listed - with
// encoding-type=url decoded back to the very key, page by page - read back
// and copied; the names no door may create are refused; a name too long for
// the disk is KeyTooLongError, not a 500 carrying the server's path; and a key
// is never a second spelling of another path.

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/testutil/hostilenames"
)

// hnKeyPath escapes a key for the request URL, segment by segment, the way an
// SDK puts it on the wire.
func hnKeyPath(key string) string {
	segs := strings.Split(key, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// hnList walks a whole V2 listing of prefix with encoding-type=url, pages of
// three, and returns every key decoded the way botocore and the Go SDK decode
// them (the form decoder).
func hnList(t *testing.T, hz *harness, key *protocolauth.IssuedKey, prefix string) []string {
	t.Helper()
	var keys []string
	token := ""
	for page := 0; page < 100; page++ {
		u := "https://s3.filex.test/main?list-type=2&max-keys=3&encoding-type=url&prefix=" + url.QueryEscape(prefix)
		if token != "" {
			u += "&continuation-token=" + url.QueryEscape(token)
		}
		rec := hz.do(t, key, http.MethodGet, u)
		if rec.Code != http.StatusOK {
			t.Fatalf("list page %d = %d: %s", page, rec.Code, rec.Body.String())
		}
		var out struct {
			Prefix       string `xml:"Prefix"`
			EncodingType string `xml:"EncodingType"`
			IsTruncated  bool   `xml:"IsTruncated"`
			Next         string `xml:"NextContinuationToken"`
			Contents     []struct {
				Key string `xml:"Key"`
			} `xml:"Contents"`
		}
		if err := xml.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("the listing is not valid XML: %v", err)
		}
		if p, err := url.QueryUnescape(out.Prefix); err != nil || p != prefix {
			t.Fatalf("Prefix came back as %q (%v)", out.Prefix, err)
		}
		for _, c := range out.Contents {
			k, err := url.QueryUnescape(c.Key)
			if err != nil {
				t.Fatalf("key %q is not url-encoded: %v", c.Key, err)
			}
			keys = append(keys, k)
		}
		if !out.IsTruncated {
			return keys
		}
		if out.Next == "" {
			t.Fatal("a truncated page with no continuation token")
		}
		token = out.Next
	}
	t.Fatal("the listing did not end")
	return nil
}

func TestS3_HostileNamesRoundTrip(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})
	names := hostilenames.ForLocalDisk()

	for _, n := range names {
		rec := hz.put(t, key, "https://s3.filex.test/main/hostile/"+hnKeyPath(n.Name), []byte("body of "+n.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: PUT %q = %d: %s", n.ID, n.Name, rec.Code, rec.Body.String())
		}
	}
	listed := map[string]int{}
	for _, k := range hnList(t, hz, key, "hostile/") {
		listed[k]++
	}
	if len(listed) != len(names) {
		t.Errorf("listed %d keys, put %d", len(listed), len(names))
	}
	for _, n := range names {
		t.Run(n.ID, func(t *testing.T) {
			k := "hostile/" + n.Name
			if listed[k] != 1 {
				t.Errorf("%q listed %d times, want once", k, listed[k])
			}
			rec := hz.do(t, key, http.MethodGet, "https://s3.filex.test/main/"+hnKeyPath(k))
			if rec.Code != http.StatusOK || rec.Body.String() != "body of "+n.ID {
				t.Fatalf("GET = %d %q", rec.Code, rec.Body.String())
			}
			if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "hostile", n.Name)); err != nil {
				t.Fatalf("not on disk under its own name: %v", err)
			}
			rec = hz.copy(t, key, "https://s3.filex.test/main/copied/"+hnKeyPath(n.Name), "/main/"+hnKeyPath(k))
			if rec.Code != http.StatusOK {
				t.Fatalf("CopyObject = %d: %s", rec.Code, rec.Body.String())
			}
			got, err := os.ReadFile(filepath.Join(hz.rootOf(t, st), "copied", n.Name))
			if err != nil || string(got) != "body of "+n.ID {
				t.Fatalf("the copy holds %q (%v)", got, err)
			}
		})
	}
}

// The deep path: one PUT makes the 64 folders on the way, and the key reads
// back.
func TestS3_HostileDeepPath(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})
	d := hostilenames.Deep()
	if rec := hz.put(t, key, "https://s3.filex.test/main/"+hnKeyPath(d.Path), []byte("deep")); rec.Code != http.StatusOK {
		t.Fatalf("PUT %d bytes deep = %d: %s", len(d.Path), rec.Code, rec.Body.String())
	}
	if rec := hz.do(t, key, http.MethodGet, "https://s3.filex.test/main/"+hnKeyPath(d.Path)); rec.Code != http.StatusOK || rec.Body.String() != "deep" {
		t.Fatalf("GET = %d %q", rec.Code, rec.Body.String())
	}
}

// The names no door may create (internal/leafname) are refused with 400, as
// an object's name and as a CopyObject destination, and nothing lands.
func TestS3_RefusedNamesAreRefused(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "dir/a.txt", []byte("a"))
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	for _, n := range hostilenames.Refused() {
		t.Run(n.ID, func(t *testing.T) {
			dst := "https://s3.filex.test/main/dir/" + hnKeyPath(n.Name)
			for what, rec := range map[string]interface{ Result() *http.Response }{
				"PUT":        hz.put(t, key, dst, []byte("x")),
				"CopyObject": hz.copy(t, key, dst, "/main/dir/a.txt"),
			} {
				res := rec.Result()
				var body bytes.Buffer
				_, _ = body.ReadFrom(res.Body)
				if res.StatusCode != http.StatusBadRequest || errorCode(t, body.Bytes()) != "InvalidArgument" {
					t.Errorf("%s %q = %d %s, want 400 InvalidArgument", what, n.Name, res.StatusCode, body.String())
				}
			}
		})
	}
	ents, err := os.ReadDir(filepath.Join(hz.rootOf(t, st), "dir"))
	if err != nil || len(ents) != 1 {
		t.Fatalf("dir holds %d entries (%v), want only a.txt", len(ents), err)
	}
	if got, err := os.ReadFile(filepath.Join(hz.rootOf(t, st), "dir", "a.txt")); err != nil || string(got) != "a" {
		t.Fatalf("a.txt is now %q (%v)", got, err)
	}
}

// A name too long for the disk is the client's to fix: 400 KeyTooLongError,
// with no server path in the message - not a 500 InternalError carrying
// `open /srv/<root>/...: file name too long`. A key over 1024 bytes is refused
// up front, as AWS refuses it.
func TestS3_TooLongNameIsKeyTooLong(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})
	root := hz.rootOf(t, st)

	check := func(what string, code int, body []byte) {
		t.Helper()
		if code != http.StatusBadRequest || errorCode(t, body) != "KeyTooLongError" {
			t.Errorf("%s = %d %s, want 400 KeyTooLongError", what, code, body)
		}
		if bytes.Contains(body, []byte(filepath.ToSlash(root))) || bytes.Contains(body, []byte(root)) {
			t.Errorf("%s: the answer names the storage's place on the server: %s", what, body)
		}
	}
	for _, n := range hostilenames.TooLongForLocalDisk() {
		rec := hz.put(t, key, "https://s3.filex.test/main/"+hnKeyPath(n.Name), []byte("x"))
		check("PUT "+n.ID, rec.Code, rec.Body.Bytes())
	}
	over := strings.Repeat("abcdefghi/", 103) + "x.txt"
	rec := hz.put(t, key, "https://s3.filex.test/main/"+over, []byte("x"))
	check(fmt.Sprintf("PUT of a %d-byte key", len(over)), rec.Code, rec.Body.Bytes())
	if _, err := os.Stat(filepath.Join(root, "abcdefghi")); err == nil {
		t.Error("the over-long key made folders before it was refused")
	}
}

// A key is never a second spelling of another path. A credential confined to
// projects/acme cannot climb out of it with `..` - not to read, write, copy
// or list - because the confinement, the grant and the drivers must all read
// one key the same way.
func TestS3_KeyIsNeverASecondSpelling(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "clients/x/secret.txt", []byte("not yours"))
	hz.writeFile(t, st, "projects/acme/ok.txt", []byte("yours"))
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "confined", Bucket: "main", Prefix: "projects/acme"})
	root := hz.rootOf(t, st)

	out := "projects/acme/../../clients/x/"
	if rec := hz.do(t, key, http.MethodGet, "https://s3.filex.test/main/"+out+"secret.txt"); rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "not yours") {
		t.Errorf("GET through .. = %d %q", rec.Code, rec.Body.String())
	}
	if rec := hz.put(t, key, "https://s3.filex.test/main/"+out+"planted.txt", []byte("planted")); rec.Code == http.StatusOK {
		t.Errorf("PUT through .. = %d", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(root, "clients", "x", "planted.txt")); err == nil {
		t.Error("a PUT through .. wrote outside the confinement")
	}
	rec := hz.copy(t, key, "https://s3.filex.test/main/projects/acme/stolen.txt", "/main/"+out+"secret.txt")
	if rec.Code == http.StatusOK {
		t.Errorf("CopyObject from outside through .. = %d", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(root, "projects", "acme", "stolen.txt")); err == nil {
		t.Error("a copy through .. brought a file in from outside the confinement")
	}
	rec = hz.do(t, key, http.MethodGet, "https://s3.filex.test/main?list-type=2&prefix="+url.QueryEscape("projects/acme/../../"))
	if rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "clients") {
		t.Errorf("a listing through .. = %d: %s", rec.Code, rec.Body.String())
	}
	body := []byte(`<Delete><Object><Key>` + out + `secret.txt</Key></Object></Delete>`)
	rec = recorderFor(hz, signedBody(t, key, http.MethodPost, "https://s3.filex.test/main?delete", body, hz.at))
	if _, err := os.Stat(filepath.Join(root, "clients", "x", "secret.txt")); err != nil {
		t.Errorf("a bulk delete through .. removed a file outside the confinement (%d)", rec.Code)
	}
}

// A copy onto another spelling of its own source is the same object: it used
// to pass the same-object test (which compares the keys as written) and the
// local driver truncated the file it was about to read. Now either spelling
// is refused as a key, and the file keeps its bytes.
func TestS3_CopyOntoAnotherSpellingOfItself(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "dir/a.txt", []byte("original"))
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	for _, c := range []struct{ dst, src string }{
		{"https://s3.filex.test/main/dir/a.txt", "/main/dir/./a.txt"},
		{"https://s3.filex.test/main/dir/./a.txt", "/main/dir/a.txt"},
		{"https://s3.filex.test/main/dir//a.txt", "/main/dir/a.txt"},
		{"https://s3.filex.test/main/dir/a.txt", "/main/dir/x/../a.txt"},
	} {
		rec := hz.copy(t, key, c.dst, c.src)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("copy %s -> %s = %d: %s", c.src, c.dst, rec.Code, rec.Body.String())
		}
		got, err := os.ReadFile(filepath.Join(hz.rootOf(t, st), "dir", "a.txt"))
		if err != nil || string(got) != "original" {
			t.Fatalf("after copy %s -> %s the file holds %q (%v)", c.src, c.dst, got, err)
		}
	}
}

// On the dedicated S3 host the path starts with the bucket, or - virtual
// hosted - with the key: a name that begins with "s3" is that name, not the
// /s3 mount prefix of the app's own host.
func TestS3_NamesStartingWithS3OnTheDedicatedHost(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	backup := hz.storage(t, "s3yedek")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	for _, c := range []struct {
		url  string
		st   *model.Storage
		want string
	}{
		{"https://main.s3.filex.test/s3cmd.conf", st, "s3cmd.conf"},
		{"https://main.s3.filex.test/s3/inside.txt", st, "s3/inside.txt"},
		{"https://s3.filex.test/s3yedek/x.txt", backup, "x.txt"},
	} {
		rec := hz.put(t, key, c.url, []byte("x"))
		if rec.Code != http.StatusOK {
			t.Errorf("PUT %s = %d: %s", c.url, rec.Code, rec.Body.String())
			continue
		}
		if _, err := os.Stat(filepath.Join(hz.rootOf(t, c.st), filepath.FromSlash(c.want))); err != nil {
			t.Errorf("PUT %s did not land on %s in %s: %v", c.url, c.want, c.st.Name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "cmd.conf")); err == nil {
		t.Error("s3cmd.conf lost its first two letters")
	}
}

// A completed upload's Location is a URL that names the key: a space, `#` or
// `?` in a name is escaped, not left to end the path.
func TestS3_CompletedUploadLocationIsAURL(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})
	const name = "dir/a b#c?.txt"
	target := "https://s3.filex.test/main/" + hnKeyPath(name)

	uploadID := initiate(t, hz, key, target)
	part := uploadPart(t, hz, key, target, uploadID, 1, []byte("the only part"))
	if part.Code != http.StatusOK {
		t.Fatalf("part = %d: %s", part.Code, part.Body.String())
	}
	rec := complete(t, hz, key, target, uploadID, map[int]string{1: part.Header().Get("ETag")})
	if rec.Code != http.StatusOK {
		t.Fatalf("complete = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Location string `xml:"Location"`
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	loc, err := url.Parse(out.Location)
	if err != nil || loc.Path != "/main/"+name || loc.Fragment != "" || loc.RawQuery != "" {
		t.Fatalf("Location %q reads as path %q (%v)", out.Location, loc.Path, err)
	}
}

// A file that already has a name no door may give it now - a tab, a line
// break, planted on the disk by something else - stays reachable over S3:
// listed (encoding-type=url, the exact key back), read, overwritten and
// deleted. Only a NEW name is refused (the owner's rule for 0.57).
func TestS3_PlantedControlNamesStayReachable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot hold a control character in a file name")
	}
	planted := hostilenames.Filter(func(n hostilenames.Name) bool {
		return n.Has("control") && !strings.ContainsRune(n.Name, 0)
	})
	if len(planted) == 0 {
		t.Fatal("no planted name to try")
	}
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})
	for _, n := range planted {
		hz.writeFile(t, st, "planted/"+n.Name, []byte("planted "+n.ID))
	}
	listed := map[string]bool{}
	for _, k := range hnList(t, hz, key, "planted/") {
		listed[k] = true
	}
	for _, n := range planted {
		t.Run(n.ID, func(t *testing.T) {
			k := "planted/" + n.Name
			if !listed[k] {
				t.Errorf("%q is not in the listing under its own key", k)
			}
			target := "https://s3.filex.test/main/" + hnKeyPath(k)
			if rec := hz.do(t, key, http.MethodGet, target); rec.Code != http.StatusOK || rec.Body.String() != "planted "+n.ID {
				t.Fatalf("GET = %d %q", rec.Code, rec.Body.String())
			}
			if rec := hz.put(t, key, target, []byte("rewritten")); rec.Code != http.StatusOK {
				t.Fatalf("overwriting the file that has the name = %d: %s", rec.Code, rec.Body.String())
			}
			if rec := hz.do(t, key, http.MethodGet, target); rec.Body.String() != "rewritten" {
				t.Fatalf("after the overwrite GET = %q", rec.Body.String())
			}
			if rec := hz.put(t, key, target+"-new", []byte("x")); rec.Code != http.StatusBadRequest {
				t.Errorf("a NEW key with the character = %d, want 400", rec.Code)
			}
			if rec := hz.do(t, key, http.MethodDelete, target); rec.Code != http.StatusNoContent {
				t.Fatalf("DELETE = %d: %s", rec.Code, rec.Body.String())
			}
			if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "planted", n.Name)); !os.IsNotExist(err) {
				t.Errorf("still on the disk after DELETE (%v)", err)
			}
		})
	}
}

// A file planted on the disk with a name of only dots and spaces that does
// not hold `..` (` . . . `, `. .`) - refused as a NEW name since 0.57 -
// stays readable, overwritable and deletable over S3: the read side keeps
// its own rule (leafname.DotSegment), only a new name is asked.
func TestS3_PlantedDotAndSpaceNamesStayReachable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows trims trailing dots and spaces: it cannot hold these names")
	}
	planted := []string{hostilenames.Get("dots-spaced").Name, hostilenames.Get("dot-space-dot").Name}
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})
	for _, name := range planted {
		hz.writeFile(t, st, "planted/"+name, []byte("planted"))
	}
	for _, name := range planted {
		t.Run(name, func(t *testing.T) {
			target := "https://s3.filex.test/main/" + hnKeyPath("planted/"+name)
			if rec := hz.do(t, key, http.MethodGet, target); rec.Code != http.StatusOK || rec.Body.String() != "planted" {
				t.Fatalf("GET = %d %q", rec.Code, rec.Body.String())
			}
			if rec := hz.put(t, key, target, []byte("rewritten")); rec.Code != http.StatusOK {
				t.Fatalf("overwriting the file that has the name = %d: %s", rec.Code, rec.Body.String())
			}
			fresh := "https://s3.filex.test/main/" + hnKeyPath("fresh/"+name)
			if rec := hz.put(t, key, fresh, []byte("x")); rec.Code != http.StatusBadRequest {
				t.Errorf("a NEW key with the name = %d, want 400", rec.Code)
			}
			if rec := hz.do(t, key, http.MethodDelete, target); rec.Code != http.StatusNoContent {
				t.Fatalf("DELETE = %d: %s", rec.Code, rec.Body.String())
			}
			if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "planted", name)); !os.IsNotExist(err) {
				t.Errorf("still on the disk after DELETE (%v)", err)
			}
		})
	}
}

// S3 deletes AT MOST THE ONE OBJECT NAMED, and a DELETE is idempotent.
// `folder/` that still holds anything stays as it is (on S3 the marker would
// go and the prefix would stay listed, because its children are there);
// `folder/` that holds nothing goes; `folder` without the slash names no
// object at all. The same for the entries of a bulk delete, each reported
// Deleted. Every one used to trash the whole folder with what was in it.
func TestS3_DeleteRemovesAtMostTheObjectNamed(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})
	root := hz.rootOf(t, st)
	hz.writeFile(t, st, "full/a.txt", []byte("a"))
	hz.writeFile(t, st, "full/sub/b.txt", []byte("b"))
	for _, d := range []string{"empty", "empty-noslash", "empty-bulk"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	there := func(rel string) bool {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		return err == nil
	}
	full := func(what string) {
		t.Helper()
		for _, rel := range []string{"full/a.txt", "full/sub/b.txt"} {
			if !there(rel) {
				t.Fatalf("%s removed %s", what, rel)
			}
		}
	}

	for _, k := range []string{"full/", "full", "full/sub/", "empty-noslash"} {
		rec := hz.do(t, key, http.MethodDelete, "https://s3.filex.test/main/"+k)
		if rec.Code != http.StatusNoContent {
			t.Errorf("DELETE %s = %d, want 204: %s", k, rec.Code, rec.Body.String())
		}
		full("DELETE " + k)
	}
	if !there("empty-noslash") {
		t.Error("DELETE of a directory without its slash removed it")
	}
	if rec := hz.do(t, key, http.MethodDelete, "https://s3.filex.test/main/empty/"); rec.Code != http.StatusNoContent {
		t.Errorf("DELETE empty/ = %d", rec.Code)
	}
	if there("empty") {
		t.Error("DELETE of an empty folder's marker left the folder")
	}

	body := []byte(`<Delete><Object><Key>full/</Key></Object><Object><Key>full</Key></Object><Object><Key>empty-bulk/</Key></Object></Delete>`)
	rec := recorderFor(hz, signedBody(t, key, http.MethodPost, "https://s3.filex.test/main?delete", body, hz.at))
	if rec.Code != http.StatusOK {
		t.Fatalf("bulk delete = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Deleted []struct {
			Key string `xml:"Key"`
		} `xml:"Deleted"`
		Errors []struct {
			Key string `xml:"Key"`
		} `xml:"Error"`
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal %s: %v", rec.Body.String(), err)
	}
	if len(out.Deleted) != 3 || len(out.Errors) != 0 {
		t.Errorf("bulk delete reported %d deleted, %d errors, want 3 and 0: %s", len(out.Deleted), len(out.Errors), rec.Body.String())
	}
	full("the bulk delete")
	if there("empty-bulk") {
		t.Error("the bulk delete of an empty folder's marker left the folder")
	}
}
