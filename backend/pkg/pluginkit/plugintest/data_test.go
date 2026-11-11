package plugintest

import (
	"testing"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

func dataManifest(perms ...string) wire.Manifest {
	m := wire.Manifest{ManifestVersion: 1, Name: "kv", Version: "1.0.0", Label: wire.Text{"en": "KV"}}
	m.Permissions = perms
	return m
}

// The fake host keeps the real host's rules: the grant, the tenant scope the
// app cannot leave, conditional writes, and a route visit that only reads
// and counts.
func TestData_TheHostsRules(t *testing.T) {
	h := NewHost(dataManifest())
	if _, _, _, err := h.DataGet(wire.DataTenant, "k"); !IsCode(err, wire.ErrPermissionDenied) {
		t.Fatalf("no grant: %v", err)
	}
	h = NewHost(dataManifest("data"))
	h.Tenant = 1
	if _, err := h.DataPut(wire.DataTenant, "c/abc", "one", pluginkit.DataPutOptions{IfAbsent: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.DataPut(wire.DataTenant, "c/abc", "two", pluginkit.DataPutOptions{IfAbsent: true}); !IsCode(err, wire.ErrBusy) {
		t.Fatalf("second claim: %v", err)
	}
	// Another tenant does not see it.
	h.Tenant = 2
	if _, _, found, _ := h.DataGet(wire.DataTenant, "c/abc"); found {
		t.Fatal("tenant 2 reads tenant 1's key")
	}
	if _, err := h.DataPut(wire.DataApp, "config", "{}", pluginkit.DataPutOptions{}); err != nil {
		t.Fatal(err)
	}
	h.Tenant = 1
	v, rev, found, _ := h.DataGet(wire.DataTenant, "c/abc")
	if !found || v != "one" {
		t.Fatalf("got %q %v", v, found)
	}
	if _, err := h.DataPut(wire.DataTenant, "c/abc", "x", pluginkit.DataPutOptions{IfRev: rev + 1}); !IsCode(err, wire.ErrBusy) {
		t.Fatalf("stale rev: %v", err)
	}
	if _, _, found, _ := h.DataGet(wire.DataApp, "config"); !found {
		t.Fatal("the app scope is every tenant's")
	}
	// A route visit reads and counts, and writes nothing else.
	h.EnterRoute()
	if _, err := h.DataPut(wire.DataTenant, "c/new", "x", pluginkit.DataPutOptions{}); !IsCode(err, wire.ErrPermissionDenied) {
		t.Fatalf("write on a visit: %v", err)
	}
	if n, err := h.DataIncr(wire.DataTenant, "n/abc", 1); err != nil || n != 1 {
		t.Fatalf("count on a visit: %d %v", n, err)
	}
	if n, _ := h.DataIncr(wire.DataTenant, "n/abc", 2); n != 3 {
		t.Fatalf("count %d", n)
	}
	h.EnterScreen()
	if _, err := h.DataPut(wire.DataTenant, "n/abc", "9", pluginkit.DataPutOptions{}); !IsCode(err, wire.ErrInvalid) {
		t.Fatalf("overwriting a counter: %v", err)
	}
	if _, err := h.DataIncr(wire.DataTenant, "c/abc", 1); !IsCode(err, wire.ErrInvalid) {
		t.Fatalf("counting a value: %v", err)
	}
	if _, err := h.DataPut(wire.DataTenant, "big", string(make([]byte, wire.DataMaxValueBytes+1)), pluginkit.DataPutOptions{}); !IsCode(err, wire.ErrTooLarge) {
		t.Fatalf("too large: %v", err)
	}
}

func TestData_ListInOrderByPages(t *testing.T) {
	h := NewHost(dataManifest("data"))
	for _, k := range []string{"u/1/c", "u/1/a", "u/2/a", "u/1/b", "x"} {
		if _, err := h.DataPut(wire.DataTenant, k, k, pluginkit.DataPutOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	items, next, err := h.DataList(wire.DataTenant, "u/1/", "", 2)
	if err != nil || len(items) != 2 || items[0].Key != "u/1/a" || items[1].Key != "u/1/b" || next != "u/1/b" {
		t.Fatalf("page 1: %v %q %v", items, next, err)
	}
	items, next, _ = h.DataList(wire.DataTenant, "u/1/", next, 2)
	if len(items) != 1 || items[0].Key != "u/1/c" || next != "" {
		t.Fatalf("page 2: %v %q", items, next)
	}
}

func TestLinkLive_NeedsTheRole(t *testing.T) {
	h := NewHost(dataManifest("data"))
	if _, err := h.LinkLive("tok"); !IsCode(err, wire.ErrPermissionDenied) {
		t.Fatalf("no role: %v", err)
	}
	h = NewHost(dataManifest("link_shortener"))
	h.SetLinkLive("tok", true)
	if live, _ := h.LinkLive("tok"); !live {
		t.Fatal("live link")
	}
	if live, _ := h.LinkLive("other"); live {
		t.Fatal("unknown link live")
	}
}

func TestRouteOutput_Rules(t *testing.T) {
	for _, c := range []struct {
		out wire.RouteOutput
		ok  bool
	}{
		{wire.RouteOutput{Redirect: "https://example.com/a"}, true},
		{wire.RouteOutput{Miss: true}, true},
		{wire.RouteOutput{Page: &wire.RoutePage{Title: wire.Text{"en": "x"}}}, true},
		{wire.RouteOutput{}, false},
		{wire.RouteOutput{Redirect: "https://example.com/a", Miss: true}, false},
		{wire.RouteOutput{Redirect: "javascript:alert(1)"}, false},
		{wire.RouteOutput{Redirect: "/relative"}, false},
		{wire.RouteOutput{Redirect: "https://u:p@example.com/"}, false},
		{wire.RouteOutput{Page: &wire.RoutePage{Title: wire.Text{"en": "x"}, Link: "data:text/html,x"}}, false},
	} {
		if err := CheckRouteOutput(&c.out); (err == nil) != c.ok {
			t.Fatalf("%+v: %v", c.out, err)
		}
	}
}

func TestShorten_TheAnswerLiesOnABase(t *testing.T) {
	m := dataManifest("data", "public_routes", "link_shortener")
	m.Routes = []wire.Route{{ID: "go", Label: wire.Text{"en": "Go"}}}
	m.LinkShortener = &wire.LinkShortenerSpec{Route: "go"}
	answer := "https://l.example.com/abc"
	h := New(&pluginkit.Plugin{Manifest: m, Shorten: func(*wire.ShortenInput) (*wire.ShortenOutput, error) {
		return &wire.ShortenOutput{URL: answer}, nil
	}})
	in := &wire.ShortenInput{Bases: []wire.RouteBase{{ID: 1, URL: "https://l.example.com"}}}
	if out, err := h.Shorten(in); err != nil || out.URL != answer {
		t.Fatalf("%v %v", out, err)
	}
	answer = "https://elsewhere.example.com/abc"
	if _, err := h.Shorten(in); err == nil {
		t.Fatal("an address on no base accepted")
	}
}
