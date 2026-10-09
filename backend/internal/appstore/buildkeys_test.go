package appstore_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/appstore"
	"github.com/brf-tech/filex/backend/internal/plugin"
)

// sec055 S5: a build a STORE signed is taken through that store's own link,
// or - any other way (an upload, an address, a source, another store's link)
// - only while the store still lists that version and has not revoked the
// plugin. The store's artifact keys are learnt from the keys.json filex reads
// when it trusts the store, and remembered for good.

// gateFix is a trusted store with an artifact key and a signed index: myfs
// 1.3.0 listed, myfs 1.2.0 yanked, gonefs revoked.
func gateFix(t *testing.T) (*fix, ed25519.PublicKey) {
	t.Helper()
	f := newFix(t, nil)
	f.st.AddKey("idx-1", appstore.UseIndex, appstore.KeyActive)
	art := f.st.AddKey("art-1", appstore.UseArtifact, appstore.KeyActive)
	f.trust(t)
	doc := f.st.IndexDoc(
		map[string]any{"name": "myfs", "kind": "storage", "version": "1.3.0"},
		map[string]any{"name": "gonefs", "kind": "storage", "version": "1.0.0", "revoked": true},
	)
	for _, a := range doc["apps"].([]any) {
		app := a.(map[string]any)
		versions := app["versions"].([]any)
		v := versions[0].(map[string]any)
		v["binaries"] = map[string]any{"linux/amd64": map[string]any{"url": "https://x/b", "sha256": strings.Repeat("1", 64), "sig": "00"}}
		if app["name"] == "myfs" {
			old := map[string]any{}
			for k, x := range v {
				old[k] = x
			}
			old["version"], old["ref"] = "1.2.0", "v1.2.0"
			old["yanked"] = map[string]any{"reason": "broken", "at": "2026-10-02T00:00:00Z"}
			app["versions"] = append(versions, old)
		}
	}
	f.st.SetIndex("idx-1", doc, false)
	return f, art
}

func claim(name, version string) plugin.BuildClaim {
	return plugin.BuildClaim{Name: name, Version: version, Platform: "linux/amd64", SHA256: strings.Repeat("1", 64)}
}

func TestStorageBuildGate_AStoresBuildOnlyThroughItsLinkOrWhileListed(t *testing.T) {
	f, art := gateFix(t)
	ctx := context.Background()
	origin := f.st.Origin()

	owners, err := appstore.BuildKeyStores(ctx, f.mem, art)
	if err != nil || len(owners) != 1 || owners[0] != origin {
		t.Fatalf("the store's artifact key was not remembered as the store's: %v %v", owners, err)
	}

	gate := appstore.StorageBuildGate(f.mem, f.svc)
	publisher, _, _ := ed25519.GenerateKey(rand.Reader)
	if err := gate(ctx, "", claim("myfs", "1.2.0"), publisher, false); err != nil {
		t.Fatalf("a publisher's key is not the gate's business: %v", err)
	}
	if err := gate(ctx, origin, claim("myfs", "1.2.0"), art, false); err != nil {
		t.Fatalf("through the store's own link: %v", err)
	}
	if err := gate(ctx, "", claim("myfs", "1.3.0"), art, false); err != nil {
		t.Fatalf("a version the store lists, by address: %v", err)
	}
	for name, c := range map[string]plugin.BuildClaim{
		"a yanked version":         claim("myfs", "1.2.0"),
		"a revoked plugin":         claim("gonefs", "1.0.0"),
		"a version never listed":   claim("myfs", "9.9.9"),
		"a plugin the store lacks": claim("nofs", "1.0.0"),
	} {
		if err := gate(ctx, "", c, art, false); err == nil {
			t.Errorf("%s signed by the store was taken by address", name)
		}
		if err := gate(ctx, "https://other.example", c, art, false); err == nil {
			t.Errorf("%s signed by the store was taken through another store's link", name)
		}
	}
	if err := gate(ctx, origin, claim("myfs", "1.3.0"), art, true); err == nil {
		t.Fatal("a store's signature over the sha256 alone was taken")
	}
}

func TestStorageBuildGate_WithTheStoreOffAStoresSignatureNeedsItsLink(t *testing.T) {
	f, art := gateFix(t)
	ctx := context.Background()
	gate := appstore.StorageBuildGate(f.mem, nil)
	if err := gate(ctx, "", claim("myfs", "1.3.0"), art, false); err == nil {
		t.Fatal("with nobody to ask, a store's build was taken by address")
	}
	if err := gate(ctx, f.st.Origin(), claim("myfs", "1.3.0"), art, false); err != nil {
		t.Fatalf("through its own link: %v", err)
	}
}

func TestStorageBuildGate_AStoreNoLongerTrustedStaysAStore(t *testing.T) {
	f, art := gateFix(t)
	ctx := context.Background()
	if _, err := f.svc.Remove(ctx, f.st.Origin(), nil); err != nil {
		t.Fatal(err)
	}
	owners, err := appstore.BuildKeyStores(ctx, f.mem, art)
	if err != nil || len(owners) != 1 {
		t.Fatalf("untrusting the store made its key a publisher's: %v %v", owners, err)
	}
	if err := appstore.StorageBuildGate(f.mem, f.svc)(ctx, "", claim("myfs", "1.3.0"), art, false); err == nil {
		t.Fatal("a build of a store no longer trusted was taken without asking it")
	}
}
