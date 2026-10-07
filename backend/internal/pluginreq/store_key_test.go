package pluginreq

import (
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
)

// A store request's dedup key (#162) is its store, app and version: one
// pending request per version of an app of a store. The keys of every other
// source are what they were before store requests existed - a pending
// request is found again by its key - and a store's fields never reach one.
func TestSourceKey_StoreRequestsHaveTheirOwnAndChangeNoOther(t *testing.T) {
	app := model.PluginRequestKindApp
	a := sourceKey(app, "install", 0, SourceStore, Source{Store: "https://apps.filex.sh", StoreApp: "lang-eo", StoreVersion: "1.0.0"})
	b := sourceKey(app, "install", 0, SourceStore, Source{Store: "https://APPS.filex.sh", StoreApp: "lang-eo", StoreVersion: "1.0.0"})
	c := sourceKey(app, "install", 0, SourceStore, Source{Store: "https://apps.filex.sh", StoreApp: "lang-eo", StoreVersion: "1.0.1"})
	d := sourceKey(app, "install", 0, SourceStore, Source{Store: "https://other.example", StoreApp: "lang-eo", StoreVersion: "1.0.0"})
	if a != b {
		t.Fatal("the store's spelling changed the key")
	}
	if a == c || a == d {
		t.Fatal("another version or another store gave the same key")
	}
	// A GitHub source: the store fields are cleared by appSource, so a
	// request that carried them keys the same as one that did not.
	src := Source{GitHubRepo: "Owner/app", Store: "https://apps.filex.sh", StoreApp: "x"}
	kind, err := appSource(&src, false)
	if err != nil || kind != SourceGitHub || src.Store != "" || src.StoreApp != "" {
		t.Fatalf("appSource kept the store fields: %+v %v", src, err)
	}
	plain := Source{GitHubRepo: "Owner/app"}
	_, _ = appSource(&plain, false)
	if sourceKey(app, "install", 0, kind, src) != sourceKey(app, "install", 0, SourceGitHub, plain) {
		t.Fatal("a store field changed a GitHub request's key")
	}
}
