package plugin_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// Storage plugins from an app store (#215, store.go): a license that does not
// hold HOLDS the plugin, a store's build is installed held to its pin with
// the signature that verifies, and the feed the store reviewed is read only
// when its bytes are the ones pinned.

func TestSetLicenseHold_HoldsARunningPluginAndLetsItGo(t *testing.T) {
	f := newFakePlugin("heldfs", fullCaps())
	defer f.Close()
	m, _, _ := newManager(t)
	ctx := context.Background()
	st, err := m.InstallRemote(ctx, "heldfs", f.URL(), "test-token")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	waitState(t, m, st.ID, plugin.StateRunning)

	m.SetLicenseHold("heldfs", "license: revoked")
	got := waitState(t, m, st.ID, plugin.StateHeld)
	if got.StateError != "license: revoked" {
		t.Fatalf("a held plugin says why: %q", got.StateError)
	}
	if _, err := storage.Get("plugin:heldfs"); err == nil {
		t.Fatal("a held plugin's driver is still registered: its storages would open")
	}
	// A restart does not get round the hold.
	if _, err := m.Restart(ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, st.ID, plugin.StateHeld)

	m.SetLicenseHold("heldfs", "")
	waitState(t, m, st.ID, plugin.StateRunning)
	if _, err := storage.Get("plugin:heldfs"); err != nil {
		t.Fatalf("released, the driver is back: %v", err)
	}
}

func TestSetLicenseHold_APluginInstalledUnderAHeldNameStartsHeld(t *testing.T) {
	f := newFakePlugin("paidfs", fullCaps())
	defer f.Close()
	m, _, _ := newManager(t)
	// A paid install: the license is required BEFORE the plugin lands.
	m.SetLicenseHold("paidfs", "license: unverified")
	st, err := m.InstallRemote(context.Background(), "paidfs", f.URL(), "test-token")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if got := waitState(t, m, st.ID, plugin.StateHeld); got.StateError != "license: unverified" {
		t.Fatalf("state error %q", got.StateError)
	}
	m.SetLicenseHold("paidfs", "")
	waitState(t, m, st.ID, plugin.StateRunning)
}

// buildServer serves body at /build over TLS, and a manager whose download
// client trusts it.
func buildServer(t *testing.T, body []byte, opts ...func(*plugin.Options)) (*plugin.Manager, string) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/build" && r.URL.Path != "/filex-storage.json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	m, _, _ := newManagerWith(t, func(o *plugin.Options) {
		o.HTTP = srv.Client()
		for _, opt := range opts {
			opt(o)
		}
	})
	return m, srv.URL
}

func TestInstallBuild_TheSignatureThatVerifiesIsKept(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	content := []byte("#!/bin/sh\nexit 0\n")
	sum := shaOf(content)
	store := hex.EncodeToString(ed25519.Sign(priv, []byte(sum)))
	publisher := hex.EncodeToString(ed25519.Sign(otherPriv, []byte(sum)))
	trusted := func(o *plugin.Options) { o.TrustedKeys = []string{hex.EncodeToString(pub)} }

	t.Run("the store's signature, trusted here, is the one kept", func(t *testing.T) {
		m, base := buildServer(t, content, trusted)
		st, err := m.InstallBuild(context.Background(), "storefs", "acme/filex-storefs",
			plugin.FeedBinary{URL: base + "/build", SHA256: sum}, plugin.StoreLink{}, []string{publisher, store})
		if err != nil {
			t.Fatalf("install: %v", err)
		}
		if st.Source != "acme/filex-storefs" {
			t.Fatalf("the source is kept for the daily check: %q", st.Source)
		}
		b, err := os.ReadFile(filepath.Join(m.Dir(), "storefs", st.Binary+".sig"))
		if err != nil || strings.TrimSpace(string(b)) != store {
			t.Fatalf("kept %q (%v), want the store's signature", b, err)
		}
	})

	t.Run("no signature that verifies: refused before anything lands", func(t *testing.T) {
		m, base := buildServer(t, content, trusted)
		_, err := m.InstallBuild(context.Background(), "storefs", "acme/filex-storefs",
			plugin.FeedBinary{URL: base + "/build", SHA256: sum}, plugin.StoreLink{}, []string{publisher})
		var rej plugin.RejectedError
		if !errors.As(err, &rej) || !strings.Contains(err.Error(), "signature") {
			t.Fatalf("want a signature refusal, got %v", err)
		}
		if _, statErr := os.Stat(filepath.Join(m.Dir(), "storefs")); !os.IsNotExist(statErr) {
			t.Fatal("a refused build left files behind")
		}
	})

	t.Run("bytes that are not the pinned build: refused", func(t *testing.T) {
		m, base := buildServer(t, content)
		_, err := m.InstallBuild(context.Background(), "storefs", "",
			plugin.FeedBinary{URL: base + "/build", SHA256: strings.Repeat("0", 64)}, plugin.StoreLink{}, nil)
		if !errors.Is(err, plugin.ErrSHA256Mismatch) {
			t.Fatalf("want ErrSHA256Mismatch, got %v", err)
		}
	})

	t.Run("an instance without trusted keys keeps the store's signature as the record", func(t *testing.T) {
		m, base := buildServer(t, content)
		required, verifies := m.SignatureVerifies(context.Background(), "recfs", plugin.FeedBinary{SHA256: sum}, plugin.StoreLink{}, []string{store})
		if required || verifies {
			t.Fatalf("no keys: nothing is required (%v %v)", required, verifies)
		}
		st, err := m.InstallBuild(context.Background(), "recfs", "", plugin.FeedBinary{URL: base + "/build", SHA256: sum}, plugin.StoreLink{}, []string{store})
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(filepath.Join(m.Dir(), "recfs", st.Binary+".sig")); strings.TrimSpace(string(b)) != store {
			t.Fatalf("kept %q", b)
		}
	})
}

func TestReadPinnedFeed_OnlyThePinnedBytes(t *testing.T) {
	feed := []byte(`{"name":"myfs","version":"1.3.0","binaries":{"linux/amd64":{"url":"https://x/y","sha256":"` + strings.Repeat("A", 64) + `"}}}`)
	m, base := buildServer(t, feed)
	got, err := m.ReadPinnedFeed(context.Background(), base+"/filex-storage.json", shaOf(feed))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "myfs" || got.Binaries["linux/amd64"].SHA256 != strings.Repeat("a", 64) {
		t.Fatalf("feed %+v", got)
	}
	if _, err := m.ReadPinnedFeed(context.Background(), base+"/filex-storage.json", strings.Repeat("1", 64)); !errors.Is(err, plugin.ErrFeedChanged) {
		t.Fatalf("other bytes: want ErrFeedChanged, got %v", err)
	}
	if _, err := m.ReadPinnedFeed(context.Background(), "http://example.com/filex-storage.json", shaOf(feed)); err == nil {
		t.Fatal("a feed over plain http was read")
	}
}
