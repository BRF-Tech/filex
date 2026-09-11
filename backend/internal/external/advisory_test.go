package external_test

// Advisories are the half of the configuration nothing used to check, and the
// entire risk in them is the opposite failure: a warning that fires on a setup
// that works. So this file is written the other way round from most — the
// "must NOT fire" table is the important one, and it carries the exact
// addresses from the report that produced these checks.

import (
	"context"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/external"
)

func codes(list []external.Advisory) map[string]string {
	out := map[string]string{}
	for _, a := range list {
		out[a.Code] = a.Severity
	}
	return out
}

func TestHostClass(t *testing.T) {
	cases := map[string]string{
		"":                           external.ClassEmpty,
		"not a url at all":           external.ClassBare, // url.Parse is lenient; "not" is the host-less path — see below
		"http://localhost:5212":      external.ClassLoopback,
		"http://LOCALHOST":           external.ClassLoopback,
		"http://foo.localhost":       external.ClassLoopback,
		"http://127.0.0.1:8080":      external.ClassLoopback,
		"http://127.13.9.4":          external.ClassLoopback,
		"http://[::1]:8080":          external.ClassLoopback,
		"http://0.0.0.0:5212":        external.ClassUnspecified,
		"http://[::]:5212":           external.ClassUnspecified,
		"http://192.168.1.10:8080":   external.ClassIP,
		"https://93.184.216.34":      external.ClassIP,
		"https://office.example.com": external.ClassDotted,
		"http://files.lan":           external.ClassDotted,
		"http://onlyoffice":          external.ClassBare,
		"http://onlyoffice:80":       external.ClassBare,
	}
	for in, want := range cases {
		if in == "not a url at all" {
			// url.Parse accepts it as an opaque path with no host.
			if got := external.HostClass(in); got != external.ClassEmpty {
				t.Fatalf("HostClass(%q) = %q, want empty (no host)", in, got)
			}
			continue
		}
		if got := external.HostClass(in); got != want {
			t.Fatalf("HostClass(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestAdvisories_DoesNotFireOnWorkingSetups is the red proof for the rule that
// matters most. Each row is a configuration that works; the assertion is that
// NOTHING of severity warning is raised for it.
func TestAdvisories_DoesNotFireOnWorkingSetups(t *testing.T) {
	cases := []struct {
		name       string
		svc        string
		serviceURL string
		publicURL  string
		set        bool
	}{
		{
			name:       "public DNS names both sides",
			svc:        external.OnlyOffice,
			serviceURL: "https://office.example.com",
			publicURL:  "https://files.example.com", set: true,
		},
		{
			name:       "LAN literals both sides",
			svc:        external.OnlyOffice,
			serviceURL: "http://192.168.1.10:8080",
			publicURL:  "http://192.168.1.5:5212", set: true,
		},
		{
			// The developer running everything on one machine. filex is
			// reached over localhost, so the browser IS on this host and a
			// loopback document server is the right answer.
			name:       "everything on loopback",
			svc:        external.OnlyOffice,
			serviceURL: "http://localhost:8080",
			publicURL:  "http://localhost:5212", set: false,
		},
		{
			// A container name is perfectly correct for the converter: only
			// filex ever calls it, never a browser.
			name:       "container name on a server-only service",
			svc:        external.Convert,
			serviceURL: "http://convert:8080",
			publicURL:  "https://files.example.com", set: true,
		},
		{
			name:       "drawio on a real name",
			svc:        external.Drawio,
			serviceURL: "https://draw.example.com",
			publicURL:  "https://files.example.com", set: true,
		},
		{
			name:       "no URL configured at all yields nothing to say",
			svc:        external.OnlyOffice,
			serviceURL: "",
			publicURL:  "http://localhost:5212", set: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := external.Advisories(tc.svc, tc.serviceURL, tc.publicURL, tc.set, nil)
			if external.HasWarning(got) {
				t.Fatalf("warning raised on a working setup: %+v", got)
			}
		})
	}
}

// TestAdvisories_BrowserUnreachableHost is the reporter's exact setup: podman,
// the container name typed into the field, filex reaching it happily.
func TestAdvisories_BrowserUnreachableHost(t *testing.T) {
	got := external.Advisories(external.OnlyOffice, "http://onlyoffice", "https://files.example.com", true, nil)
	c := codes(got)
	if c[external.CodeBrowserBareHost] != external.SeverityWarning {
		t.Fatalf("bare container name did not raise a warning: %+v", got)
	}
	// It must name the host, not just gesture at it.
	for _, a := range got {
		if a.Code == external.CodeBrowserBareHost && a.Detail != "onlyoffice" {
			t.Fatalf("advisory detail = %q, want the offending host", a.Detail)
		}
	}
}

// TestAdvisories_LoopbackDocumentServerOnAPublishedInstall — the other browser
// shape: localhost is fine for the operator's own machine and wrong for
// everybody who reaches filex at its published address.
func TestAdvisories_LoopbackDocumentServerOnAPublishedInstall(t *testing.T) {
	got := external.Advisories(external.OnlyOffice, "http://localhost:8080", "https://files.example.com", true, nil)
	if codes(got)[external.CodeBrowserLoopbackHost] != external.SeverityWarning {
		t.Fatalf("loopback document server on a published install did not warn: %+v", got)
	}
	// …and the same URL with filex itself on loopback must stay silent. This
	// is the pair, asserted together so neither half can drift.
	quiet := external.Advisories(external.OnlyOffice, "http://localhost:8080", "http://localhost:5212", false, nil)
	if _, ok := codes(quiet)[external.CodeBrowserLoopbackHost]; ok {
		t.Fatalf("loopback document server warned while filex is also on loopback: %+v", quiet)
	}
}

// TestAdvisories_PublicURLReversePath covers the leg nothing can probe.
func TestAdvisories_PublicURLReversePath(t *testing.T) {
	t.Run("loopback public URL with a remote document server warns", func(t *testing.T) {
		got := external.Advisories(external.OnlyOffice, "https://office.example.com", "http://localhost:5212", false, nil)
		if codes(got)[external.CodePublicURLLoopback] != external.SeverityWarning {
			t.Fatalf("want warning, got %+v", got)
		}
		// The default is worth naming: an operator who never set
		// FILEX_PUBLIC_URL does not know one is in play.
		for _, a := range got {
			if a.Code == external.CodePublicURLLoopback && !contains(a.Message, "never set") {
				t.Fatalf("unset public URL not called out: %q", a.Message)
			}
		}
	})
	t.Run("0.0.0.0 is never a client address", func(t *testing.T) {
		got := external.Advisories(external.OnlyOffice, "https://office.example.com", "http://0.0.0.0:5212", true, nil)
		if codes(got)[external.CodePublicURLLoopback] != external.SeverityWarning {
			t.Fatalf("want warning, got %+v", got)
		}
	})
	t.Run("both on loopback is a note, not a warning", func(t *testing.T) {
		got := external.Advisories(external.OnlyOffice, "http://localhost:8080", "http://localhost:5212", false, nil)
		if codes(got)[external.CodePublicURLLoopback] != external.SeverityNote {
			t.Fatalf("ambiguous same-host setup must not warn: %+v", got)
		}
	})
	t.Run("container-name public URL is a note about share links", func(t *testing.T) {
		got := external.Advisories(external.OnlyOffice, "https://office.example.com", "http://filex:5212", true, nil)
		if codes(got)[external.CodePublicURLBareHost] != external.SeverityNote {
			t.Fatalf("want note, got %+v", got)
		}
		if external.HasWarning(got) {
			t.Fatalf("a container-name public URL reaches the document server; it must not warn: %+v", got)
		}
	})
	t.Run("unresolvable public hostname is a note", func(t *testing.T) {
		never := func(string) bool { return true }
		got := external.Advisories(external.OnlyOffice, "https://office.example.com", "https://files.invalid", true, never)
		if codes(got)[external.CodePublicURLUnresolved] != external.SeverityNote {
			t.Fatalf("want note, got %+v", got)
		}
		if external.HasWarning(got) {
			t.Fatalf("split-horizon DNS is a working arrangement; it must not warn: %+v", got)
		}
	})
	t.Run("a resolvable public hostname says nothing", func(t *testing.T) {
		always := func(string) bool { return false }
		got := external.Advisories(external.OnlyOffice, "https://office.example.com", "https://files.example.com", true, always)
		if len(got) != 0 {
			t.Fatalf("nothing to say, got %+v", got)
		}
	})
	t.Run("drawio never speaks about the callback: it has none", func(t *testing.T) {
		got := external.Advisories(external.Drawio, "https://draw.example.com", "http://localhost:5212", false, nil)
		if len(got) != 0 {
			t.Fatalf("drawio does not call back; got %+v", got)
		}
	})
}

// TestSystemLookup_OnlyAnswersNotFound is the guard on the one advisory that
// depends on the network: an inconclusive resolver must never raise it.
func TestSystemLookup_OnlyAnswersNotFound(t *testing.T) {
	lookup := external.SystemLookup(context.Background(), 3*time.Second)
	// RFC 2606 reserves .invalid; no resolver may answer it.
	if !lookup("filex-advisory-probe.invalid") {
		t.Skip("resolver hijacks NXDOMAIN (captive portal / wildcard DNS); nothing to assert")
	}
	if lookup("localhost") {
		t.Fatal("localhost reported as not found")
	}
	if lookup("") {
		t.Fatal("empty host reported as not found")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

// TestAdvisories_JudgeTheCallbackAddress: once a callback URL is configured,
// the public URL says nothing about the document server's route home, and
// warning about it would fire on the very setup the field exists to make work.
func TestAdvisories_JudgeTheCallbackAddress(t *testing.T) {
	// The shape issue #17 ended on: a browser-facing public URL the document
	// server cannot resolve, and a container address for the callback.
	adv := external.Advise(external.AdvisoryInput{
		Service:      external.OnlyOffice,
		ServiceURL:   "https://office.example.com",
		PublicURL:    "https://files.example.com",
		PublicURLSet: true,
		CallbackURL:  "http://filex:5212",
	})
	if external.HasWarning(adv) {
		t.Errorf("a container-name callback is correct, not a warning: %+v", adv)
	}
	for _, a := range adv {
		if a.Code == external.CodePublicURLBareHost {
			t.Errorf("the share-link note belongs to the public URL, which is a real hostname here: %+v", a)
		}
	}

	// And the opposite: a loopback CALLBACK address is judged, even though the
	// public URL is fine — that is the address the document server dials.
	adv = external.Advise(external.AdvisoryInput{
		Service:      external.OnlyOffice,
		ServiceURL:   "https://office.example.com",
		PublicURL:    "https://files.example.com",
		PublicURLSet: true,
		CallbackURL:  "http://127.0.0.1:5212",
	})
	if !external.HasWarning(adv) {
		t.Errorf("a loopback callback cannot reach filex from another container, got %+v", adv)
	}
}
