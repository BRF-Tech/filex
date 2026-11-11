package shortlink

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckPrefix(t *testing.T) {
	reserved := func(s string) bool { return s == "s" || s == "api" || s == "d" }
	for _, c := range []struct {
		in   string
		own  bool
		want string
		err  error
	}{
		{"l", true, "l", nil},
		{"/L/", true, "l", nil},
		{"go/x", true, "go/x", nil},
		{"", false, "", nil},
		{"", true, "", ErrPrefixNeeded},
		{"s", true, "", ErrPrefixInUse},
		{"api/l", true, "", ErrPrefixInUse},
		{"s", false, "s", nil}, // a host of its own has no routes of filex's
		{"a/b/c/d", true, "", ErrPrefix},
		{"l_x", true, "", ErrPrefix},
		{"-l", true, "", ErrPrefix},
		{"ç", true, "", ErrPrefix},
	} {
		got, err := CheckPrefix(c.in, c.own, reserved)
		if !errors.Is(err, c.err) || got != c.want {
			t.Fatalf("%q own=%v: %q %v, want %q %v", c.in, c.own, got, err, c.want, c.err)
		}
	}
}

func TestCheckHost(t *testing.T) {
	if h, err := CheckHost("L.Example.COM."); err != nil || h != "l.example.com" {
		t.Fatalf("%q %v", h, err)
	}
	if h, err := CheckHost("kısa.example.com"); err != nil || h != "xn--ksa-jua.example.com" {
		t.Fatalf("IDN: %q %v", h, err)
	}
	for _, h := range []string{"", "localhost", "203.0.113.7", "l.example.com:8443", "a/b.com", "user@l.example.com"} {
		if _, err := CheckHost(h); err == nil {
			t.Fatalf("%q accepted", h)
		}
	}
}

type served struct {
	hit  bool
	v    Visit
	next bool
}

func harness(t *testing.T, bases []Base) (http.Handler, *served) {
	t.Helper()
	var snap Snapshot
	snap.Store(NewRoutes(bases))
	got := &served{}
	serve := func(w http.ResponseWriter, _ *http.Request, v Visit) {
		got.hit, got.v = true, v
		w.WriteHeader(http.StatusFound)
	}
	app := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		got.next = true
		w.WriteHeader(http.StatusTeapot)
	})
	return HostSplit(&snap, serve)(OwnAddress(&snap, serve)(app)), got
}

// A host of its own answers its short links and /healthz, and nothing else
// of filex: no sign-in page, no share, no API.
func TestHostSplit_AHostOfItsOwnOpensNothingElse(t *testing.T) {
	h, got := harness(t, []Base{
		{ID: 1, Host: "l.example.com", Enabled: true},
		{ID: 2, Host: "files.example.com", Prefix: "l", Enabled: true},
		{ID: 3, Host: "off.example.com", Enabled: false},
	})
	for _, c := range []struct {
		host, path string
		want       int
		hit, next  bool
		code       string
		preview    bool
	}{
		{"l.example.com", "/aB3x9", http.StatusFound, true, false, "aB3x9", false},
		{"l.example.com", "/aB3x9+", http.StatusFound, true, false, "aB3x9", true},
		{"l.example.com", "/aB3x9%2B", http.StatusFound, true, false, "aB3x9", true},
		{"L.EXAMPLE.COM:443", "/%F0%9F%98%80", http.StatusFound, true, false, "%F0%9F%98%80", false},
		{"l.example.com", "/healthz", http.StatusTeapot, false, true, "", false},
		{"l.example.com", "/", http.StatusNotFound, false, false, "", false},
		{"l.example.com", "/api/auth/me", http.StatusNotFound, false, false, "", false},
		{"l.example.com", "/s/token", http.StatusNotFound, false, false, "", false},
		{"l.example.com", "/admin/", http.StatusNotFound, false, false, "", false},
		{"l.example.com", "/+", http.StatusNotFound, false, false, "", false},
		// A switched-off base is not a host of its own any more: filex as usual.
		{"off.example.com", "/abc", http.StatusTeapot, false, true, "", false},
		// filex's own host is untouched.
		{"fm.example.com", "/abc", http.StatusTeapot, false, true, "", false},
	} {
		*got = served{}
		r := httptest.NewRequest(http.MethodGet, "http://"+c.host+c.path, nil)
		r.Host = c.host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want || got.hit != c.hit || got.next != c.next {
			t.Fatalf("%s%s: %d hit=%v next=%v, want %d hit=%v next=%v", c.host, c.path, w.Code, got.hit, got.next, c.want, c.hit, c.next)
		}
		if c.hit && (got.v.Code != c.code || got.v.Preview != c.preview) {
			t.Fatalf("%s%s: code %q preview %v", c.host, c.path, got.v.Code, got.v.Preview)
		}
	}
	// Only GET and HEAD reach a short link.
	r := httptest.NewRequest(http.MethodPost, "http://l.example.com/abc", nil)
	r.Host = "l.example.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", w.Code)
	}
}

// On filex's own address the links live under their prefix and everything
// else is filex's.
func TestOwnAddress_PrefixOnly(t *testing.T) {
	h, got := harness(t, []Base{{ID: 1, Prefix: "l", Enabled: true}, {ID: 2, Prefix: "go/x", Enabled: true}})
	for _, c := range []struct {
		path string
		hit  bool
		base int64
	}{
		{"/l/abc", true, 1},
		{"/go/x/abc", true, 2},
		{"/l/", false, 0},
		{"/l/abc/def", false, 0},
		{"/la/abc", false, 0},
		{"/s/abc", false, 0},
		{"/go/abc", false, 0},
	} {
		*got = served{}
		r := httptest.NewRequest(http.MethodGet, "http://fm.example.com"+c.path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if got.hit != c.hit || got.next == c.hit || (c.hit && got.v.Base.ID != c.base) {
			t.Fatalf("%s: hit=%v next=%v base=%d", c.path, got.hit, got.next, got.v.Base.ID)
		}
	}
	// A POST to a link's address is filex's (the router answers it), never a visit.
	*got = served{}
	r := httptest.NewRequest(http.MethodPost, "http://fm.example.com/l/abc", nil)
	h.ServeHTTP(httptest.NewRecorder(), r)
	if got.hit || !got.next {
		t.Fatal("POST served as a visit")
	}
}

func TestRoutes_Serves(t *testing.T) {
	rt := NewRoutes([]Base{{Host: "l.example.com", Enabled: true}, {Host: "x.example.com", Enabled: false}, {Prefix: "l", Enabled: true}})
	if !rt.Serves("L.example.com.") || rt.Serves("x.example.com") || rt.Serves("") {
		t.Fatal("Serves")
	}
	if len(rt.Hosts()) != 1 {
		t.Fatalf("hosts %v", rt.Hosts())
	}
	var nilRoutes *Routes
	if nilRoutes.Serves("l.example.com") {
		t.Fatal("nil routes serve")
	}
}
