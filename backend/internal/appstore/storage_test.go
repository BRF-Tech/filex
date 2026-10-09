package appstore_test

// Storage plugins from a store (#215): a storage plugin's link is read and
// checked like an app's, with its own fields (the release's feed, a pinned
// build per platform); a catalog carries storage plugins with their builds;
// a paid storage plugin's license is its own row ("storage:<name>") and the
// store is asked about it by its name.

import (
	"context"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/appstore"
)

func storagePayload(p map[string]any) {
	p["app"], p["kind"], p["version"], p["repo"], p["ref"] = "myfs", "storage", "1.3.0", "acme/filex-myfs", "v1.3.0"
	p["manifest_sha256"] = strings.Repeat("cd", 32)
	p["feed_url"] = "https://github.com/acme/filex-myfs/releases/download/v1.3.0/filex-storage.json"
	p["binaries"] = map[string]any{
		"linux/amd64": map[string]any{"url": "https://github.com/acme/filex-myfs/releases/download/v1.3.0/myfs-linux-amd64", "sha256": strings.Repeat("1", 64), "size": 10, "sig": "aa"},
		"linux/arm64": map[string]any{"url": "https://github.com/acme/filex-myfs/releases/download/v1.3.0/myfs-linux-arm64", "sha256": strings.Repeat("2", 64), "size": 11, "sig": "bb"},
	}
	p["conformance"] = map[string]any{"platform": "linux/amd64", "filex": "0.55.0", "verified": true, "passed": 9, "failed": 0, "skipped": 7,
		"driver": "myfs", "capabilities": []string{"delete", "write"}}
}

func TestIntent_AStoragePluginsLinkIsReadWithItsBuilds(t *testing.T) {
	f := embFix(t)
	f.intent("tokenstore1", storagePayload)
	in, err := f.svc.ReadIntent(context.Background(), f.st.Origin(), "tokenstore1", testInstance)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !in.IsStorage() || in.LicenseID() != "storage:myfs" || in.FeedURL == "" || len(in.Binaries) != 2 {
		t.Fatalf("intent %+v", in)
	}
	if b := in.Binaries["linux/amd64"]; b.SHA256 != strings.Repeat("1", 64) || b.Sig != "aa" || b.Size != 10 {
		t.Fatalf("build %+v", b)
	}
	if c := in.Conformance; c == nil || !c.Verified || c.Passed != 9 || c.Driver != "myfs" {
		t.Fatalf("conformance %+v", in.Conformance)
	}
}

func TestIntent_AStorageLinkThatPinsNoBuildIsRefused(t *testing.T) {
	f := embFix(t)
	ctx := context.Background()
	for name, mut := range map[string]func(p map[string]any){
		"no feed":      func(p map[string]any) { delete(p, "feed_url") },
		"a plain feed": func(p map[string]any) { p["feed_url"] = "http://example.com/filex-storage.json" },
		"no build":     func(p map[string]any) { p["binaries"] = map[string]any{} },
		"an unpinned build": func(p map[string]any) {
			p["binaries"] = map[string]any{"linux/amd64": map[string]any{"url": "https://x/y", "sha256": "abc", "sig": "aa"}}
		},
		"a plain build": func(p map[string]any) {
			p["binaries"] = map[string]any{"linux/amd64": map[string]any{"url": "http://x/y", "sha256": strings.Repeat("1", 64), "sig": "aa"}}
		},
	} {
		tok := "tok" + strings.ReplaceAll(name, " ", "")
		f.intent(tok, func(p map[string]any) {
			storagePayload(p)
			mut(p)
		})
		if _, err := f.svc.ReadIntent(ctx, f.st.Origin(), tok, testInstance); code(err) != appstore.CodeIntentInvalid {
			t.Errorf("%s: want %s, got %v", name, appstore.CodeIntentInvalid, err)
		}
	}
}

func TestLicenseID_AStoragePluginNeverSharesAnAppsRow(t *testing.T) {
	if appstore.LicenseID(appstore.KindStorage, "myfs") != "storage:myfs" || appstore.LicenseID(appstore.KindApp, "myfs") != "myfs" {
		t.Fatal("license ids")
	}
}

func TestCatalog_StoragePluginsComeWithTheirBuilds(t *testing.T) {
	f := embFix(t)
	ctx := context.Background()
	doc := f.st.IndexDoc(
		map[string]any{"name": "myfs", "kind": "storage", "version": "1.3.0"},
		map[string]any{"name": "emptyfs", "kind": "storage", "version": "1.0.0"},
		map[string]any{"name": "pdfx", "version": "2.0.0"},
	)
	for _, a := range doc["apps"].([]any) {
		app := a.(map[string]any)
		if app["name"] != "myfs" {
			continue
		}
		v := app["versions"].([]any)[0].(map[string]any)
		v["binaries"] = map[string]any{
			"linux/arm64": map[string]any{"url": "https://x/arm", "sha256": strings.Repeat("2", 64), "sig": "bb"},
			"linux/amd64": map[string]any{"url": "https://x/amd", "sha256": strings.Repeat("1", 64), "sig": "aa"},
		}
		v["conformance"] = map[string]any{"platform": "linux/amd64", "filex": "0.55.0", "verified": true, "passed": 9, "skipped": 7, "capabilities": []string{"write"}}
	}
	f.st.SetIndex("idx-1", doc, false)
	c, err := f.svc.Catalog(ctx, f.st.Origin())
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Apps) != 2 || c.Apps[0].Name != "myfs" || c.Apps[1].Name != "pdfx" {
		t.Fatalf("a storage plugin without a build is nothing to install; catalog %+v", c.Apps)
	}
	fs := c.Apps[0]
	if fs.Kind != appstore.KindStorage || strings.Join(fs.Platforms, ",") != "linux/amd64,linux/arm64" ||
		fs.Builds["linux/amd64"] != strings.Repeat("1", 64) || fs.Conformance == nil || fs.Conformance.Passed != 9 || len(fs.Permissions) != 0 {
		t.Fatalf("storage plugin %+v", fs)
	}
}

func TestLicense_AStoragePluginIsAskedAboutByItsName(t *testing.T) {
	ctx := context.Background()
	f, _ := licFix(t)
	if _, err := f.svc.RequireUndoableAs(ctx, "storage:myfs", "myfs", f.st.Origin(), licKey, nil); err != nil {
		t.Fatal(err)
	}
	if f.hold.of("storage:myfs") == "" {
		t.Fatal("a paid storage plugin is held under its own row before its license is confirmed")
	}
	v, err := f.svc.Check(ctx, "storage:myfs", nil)
	if err != nil || v.Status != appstore.StatusValid || v.Held {
		t.Fatalf("valid answer: %+v %v", v, err)
	}
	if v.Name != "myfs" || v.Kind != appstore.KindStorage || v.App != "storage:myfs" {
		t.Fatalf("view %+v", v)
	}
	var asked []string
	for _, r := range f.st.Verifies() {
		asked = append(asked, r.App)
	}
	if strings.Join(asked, ",") != "myfs" {
		t.Fatalf("the store is asked about the entry it knows, by its own name: %v", asked)
	}
	if f.hold.of("storage:myfs") != "" || f.hold.of("myfs") != "" {
		t.Fatal("released, and never under an app's name")
	}
}
