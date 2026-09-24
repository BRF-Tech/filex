package plugintest_test

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/plugintest"
)

// The kit's asset_fetch keeps the host's promises: once from the network,
// then from the cache; the pinned hash or nothing; the app's http grant;
// offline fails what is not cached. An app tests its "no font, say so"
// path against it.
func TestKit_AssetFetch(t *testing.T) {
	m := manifest()
	m.Permissions = []string{"http:fonts.example.test"} // no files:read
	h := plugintest.NewHost(m)
	url := "https://fonts.example.test/noto.ttf"
	body := []byte("glyphs")
	sum := fmt.Sprintf("%x", sha256.Sum256(body))
	h.Network[url] = body

	a, err := h.AssetFetch(url, sum, 1<<20)
	if err != nil || a.Cached {
		t.Fatalf("first fetch: %+v %v", a, err)
	}
	got, err := h.ReadInput(a.Ref)
	if err != nil || string(got) != "glyphs" {
		t.Fatalf("an asset reads without files:read: %q %v", got, err)
	}
	h.Offline = true
	a, err = h.AssetFetch(url, sum, 1<<20)
	if err != nil || !a.Cached {
		t.Fatalf("second fetch comes from the cache, offline too: %+v %v", a, err)
	}
	if len(h.Downloads) != 1 {
		t.Errorf("downloaded %d times", len(h.Downloads))
	}
	if _, err := h.AssetFetch("https://fonts.example.test/other.ttf", fmt.Sprintf("%x", sha256.Sum256([]byte("x"))), 10); !plugintest.IsCode(err, "unavailable") {
		t.Errorf("offline and not cached: %v", err)
	}
	h.Offline = false
	// (A hash already in the cache is served whatever the URL says — the
	// cache is keyed by what the bytes ARE.)
	h.Network["https://fonts.example.test/bad.ttf"] = []byte("evil")
	want := fmt.Sprintf("%x", sha256.Sum256([]byte("the real font")))
	if _, err := h.AssetFetch("https://fonts.example.test/bad.ttf", want, 1<<20); !plugintest.IsCode(err, "integrity") {
		t.Errorf("a file that is not the pinned one: %v", err)
	}
	if _, err := h.AssetFetch("https://elsewhere.test/x.ttf", sum, 1<<20); !plugintest.IsCode(err, "permission_denied") {
		t.Errorf("a host the app was not granted: %v", err)
	}
	var _ *pluginkit.Asset = a
}
