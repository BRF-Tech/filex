package s3api_test

// The S3-compatible endpoint over a PLUGIN-backed storage.
//
// Why this file exists: rclone, s3fs, restic and the desktop sync all reach
// filex through this surface, and it uses storage.Driver differently from
// every other one — a bucket listing is a recursive walk with a delimiter, a
// GET is a RANGED read through filebody, a PUT arrives with a payload hash
// and has to be readable back byte-for-byte afterwards. A plugin answers all
// of that over a wire, and range in particular is the one the adapter may be
// EMULATING (discarding the prefix) rather than forwarding. Nothing outside
// this file measures that a plugin bucket behaves like a bucket.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/plugin/testplugin"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
)

// pluginBucket registers a live plugin and creates a bucket (storage row) on
// it, named by the driver the registry knows: "plugin:<name>".
func (hz *harness) pluginBucket(t *testing.T, p *testplugin.Plugin, name string) *model.Storage {
	t.Helper()
	st, err := hz.store.CreateStorage(t.Context(), &model.Storage{
		Name: name, Driver: p.Register(t), Enabled: true,
		ConfigJSON: json.RawMessage(`{"root":"/data"}`),
	})
	if err != nil {
		t.Fatalf("plugin storage %s: %v", name, err)
	}
	return st
}

// A plugin bucket has to be a bucket: it shows up in ListBuckets, its objects
// list, and a GET returns the bytes the plugin holds. Seeding through the
// plugin rather than through filex is what makes the listing evidence — the
// objects exist only on the backend, so anything filex reports about them
// came over the protocol.
func TestPluginBucketListsAndReads(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	p := testplugin.Start(t)
	hz.pluginBucket(t, p, "eklenti")
	p.Seed("docs/rapor.txt", "plugin bytes over s3")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "rclone"})

	rec := hz.do(t, key, http.MethodGet, "https://s3.filex.test/")
	if rec.Code != http.StatusOK {
		t.Fatalf("ListBuckets = %d: %s", rec.Code, rec.Body.String())
	}
	names := listBuckets(t, rec.Body.Bytes())
	if len(names) != 1 || names[0] != "eklenti" {
		t.Fatalf("buckets = %v, want [eklenti]", names)
	}

	rec = hz.do(t, key, http.MethodGet, "https://s3.filex.test/eklenti?list-type=2&prefix=docs/")
	if rec.Code != http.StatusOK {
		t.Fatalf("ListObjectsV2 = %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "docs/rapor.txt") {
		t.Fatalf("listing does not mention the object: %s", body)
	}

	resp := hz.get(t, key, "https://s3.filex.test/eklenti/docs/rapor.txt", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GetObject = %d", resp.StatusCode)
	}
	if got := readAll(t, resp); got != "plugin bytes over s3" {
		t.Fatalf("GetObject body = %q", got)
	}

	// HEAD has to agree with GET about the size. A client that trusts a wrong
	// Content-Length either truncates the download or hangs waiting for bytes
	// that never come.
	rec = hz.do(t, key, http.MethodHead, "https://s3.filex.test/eklenti/docs/rapor.txt")
	if rec.Code != http.StatusOK {
		t.Fatalf("HeadObject = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Length"); got != "20" {
		t.Fatalf("HEAD Content-Length = %q, want 20", got)
	}
}

// A ranged GET is what every resumed download and every seeking client sends.
// The plugin adapter forwards Range when the plugin declares it and EMULATES
// it otherwise by reading from the start and discarding the prefix, so both
// halves have to return the same window — the emulated path is the one where
// an off-by-one silently corrupts a resumed transfer.
func TestPluginBucketServesRangedGets(t *testing.T) {
	for _, declares := range []bool{true, false} {
		name := "plugin declares range"
		if !declares {
			name = "host emulates range"
		}
		t.Run(name, func(t *testing.T) {
			hz := newHarness(t, false)
			u := hz.user(t, "s3@example.com", model.RoleUser)
			caps := testplugin.FullCaps()
			caps.Range = declares
			p := testplugin.Start(t, testplugin.WithCaps(caps))
			hz.pluginBucket(t, p, "eklenti")
			p.Seed("f.bin", "0123456789")
			key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

			resp := hz.get(t, key, "https://s3.filex.test/eklenti/f.bin",
				map[string]string{"Range": "bytes=3-6"})
			if resp.StatusCode != http.StatusPartialContent {
				t.Fatalf("ranged GET = %d, want 206", resp.StatusCode)
			}
			if got := readAll(t, resp); got != "3456" {
				t.Fatalf("ranged body = %q, want 3456", got)
			}
			if got := resp.Header.Get("Content-Range"); got != "bytes 3-6/10" {
				t.Fatalf("Content-Range = %q", got)
			}
		})
	}
}

// PutObject over a plugin: the bytes must reach the BACKEND, not just filex's
// bookkeeping, and a read-back must return them. Asserting through the
// plugin's own tree is the point — a surface that recorded the node row and
// dropped the body would answer 200 to both the PUT and the GET if the GET
// were served from the index.
func TestPluginBucketAcceptsPutsAndDeletes(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	p := testplugin.Start(t)
	hz.pluginBucket(t, p, "eklenti")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	body := []byte("uploaded into a plugin")
	rec := hz.put(t, key, "https://s3.filex.test/eklenti/docs/new.txt", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("PutObject = %d: %s", rec.Code, rec.Body.String())
	}
	got, ok := p.Data("docs/new.txt")
	if !ok || string(got) != string(body) {
		t.Fatalf("plugin holds %q (present=%v); tree: %v", got, ok, p.Paths())
	}

	resp := hz.get(t, key, "https://s3.filex.test/eklenti/docs/new.txt", nil)
	back := readAll(t, resp)
	if resp.StatusCode != http.StatusOK || back != string(body) {
		t.Fatalf("read-back = %d %q", resp.StatusCode, back)
	}

	rec = hz.do(t, key, http.MethodDelete, "https://s3.filex.test/eklenti/docs/new.txt")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DeleteObject = %d: %s", rec.Code, rec.Body.String())
	}
	if p.Exists("docs/new.txt") {
		t.Fatalf("object survived the delete; tree: %v", p.Paths())
	}
}

// A missing key must be 404 NoSuchKey and not 403 — S3 clients treat the two
// completely differently (one means "upload it", the other means "your
// credentials are wrong"). The plugin returns its own not_found code over the
// wire, so this is the path where a mis-mapped error code turns every missing
// object into an authentication problem.
func TestPluginBucketMapsMissingKeyToNoSuchKey(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	p := testplugin.Start(t)
	hz.pluginBucket(t, p, "eklenti")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	resp := hz.get(t, key, "https://s3.filex.test/eklenti/yok.txt", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET missing = %d, want 404", resp.StatusCode)
	}
}

// readAll drains a response body once and returns it as a string.
func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}
