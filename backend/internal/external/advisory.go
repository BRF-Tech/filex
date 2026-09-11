package external

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Advisories — the half of an external service's configuration that a probe
// from the filex process cannot see.
//
// # Why this exists
//
// Three different machines have to reach three different addresses before the
// Office editor works, and only one of them was ever checked:
//
//	address                     who must reach it              who checked it
//	──────────────────────────  ─────────────────────────────  ──────────────
//	the Document Server URL     the BROWSER (loads editor JS)  nobody
//	the Document Server URL     the filex process              the Test button
//	FILEX_PUBLIC_URL            the DOCUMENT SERVER container  nobody
//	                            (fetch + callback)
//
// So an operator on podman types `http://onlyoffice` — the container name —
// filex reaches it happily, Test goes green, and the browser cannot resolve
// that name at all. The editor then fails with the same message as a missing
// configuration. That is issue #17's second round: the check was narrower than
// the badge implied.
//
// The browser half is now genuinely probed, from the admin page itself
// (packages/core/src/lib/externalReach.ts). The document-server-to-filex half
// cannot be: filex has no way to make another container issue a request on
// demand. So for that leg we do the only honest thing — warn on the shapes
// that are certainly wrong, and say that is what we are doing.
//
// # The rule for adding one
//
// ⚠ A warning that fires on a working setup is worse than none. Every code
// below either describes a shape that cannot work by filex's own documented
// contract, or is a `note` rather than a `warning`. In particular the
// loopback advisory on a Document Server URL is suppressed when filex's own
// public URL is loopback too — that is the developer running everything on one
// machine, and `http://localhost:8080` is right for them.
// ─────────────────────────────────────────────────────────────────────────────

// Advisory severities. `warning` colours the card and withholds the
// "configuration complete" badge; `note` is information the operator may want
// and never blocks anything.
const (
	SeverityWarning = "warning"
	SeverityNote    = "note"
)

// Advisory fields.
const (
	FieldURL       = "url"
	FieldPublicURL = "public_url"
)

// Advisory codes. The admin UI translates by code (EN + TR); Message is the
// English fallback for API and MCP callers, who get no i18n bundle.
const (
	// CodeBrowserBareHost — the Document Server URL's host is a single label
	// with no dot: `http://onlyoffice`, a container name. It resolves only
	// inside the container network. A browser never can.
	CodeBrowserBareHost = "browser_bare_host"
	// CodeBrowserLoopbackHost — the Document Server URL is a loopback address
	// while filex itself is published somewhere else, so the people who reach
	// filex are not on this host and `localhost` means their own machine.
	CodeBrowserLoopbackHost = "browser_loopback_host"
	// CodePublicURLLoopback — FILEX_PUBLIC_URL is loopback. Inside the
	// Document Server's own container, that address is the Document Server.
	CodePublicURLLoopback = "public_url_loopback"
	// CodePublicURLBareHost — FILEX_PUBLIC_URL is a container name. Correct
	// for the callback, wrong for every share link built from it. This is the
	// one setup that genuinely wants two different values; see docs/ONLYOFFICE.md.
	CodePublicURLBareHost = "public_url_bare_host"
	// CodePublicURLUnresolved — FILEX_PUBLIC_URL's hostname does not resolve
	// on this machine. A note, not a warning: split-horizon DNS is a real and
	// working arrangement.
	CodePublicURLUnresolved = "public_url_unresolved"
)

// Advisory is one thing worth saying about a configuration that no probe from
// this process can settle.
type Advisory struct {
	Code     string `json:"code"`
	Field    string `json:"field"`
	Severity string `json:"severity"`
	// Detail is the offending value, so a log line or an MCP caller can see
	// what triggered the advisory without re-reading the row.
	Detail string `json:"detail,omitempty"`
	// Message is English. The admin UI prefers its own translation of Code.
	Message string `json:"message"`
}

// Host classes returned by HostClass.
const (
	ClassEmpty       = ""
	ClassLoopback    = "loopback"
	ClassUnspecified = "unspecified"
	ClassIP          = "ip"
	ClassDotted      = "dotted"
	ClassBare        = "bare"
)

// HostClass buckets a URL's host into the only distinctions that matter for
// "can a machine that is not this one reach it?".
//
//	loopback    localhost, *.localhost, 127.0.0.0/8, ::1
//	unspecified 0.0.0.0, ::
//	ip          any other literal address (LAN or public — both routable)
//	dotted      a name with a dot: office.example.com, files.lan
//	bare        a single-label name: onlyoffice, filex — container-network only
func HostClass(rawURL string) string {
	raw := strings.TrimSpace(rawURL)
	if raw == "" {
		return ClassEmpty
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ClassEmpty
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "" {
		return ClassEmpty
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return ClassLoopback
	}
	if ip := net.ParseIP(host); ip != nil {
		switch {
		case ip.IsLoopback():
			return ClassLoopback
		case ip.IsUnspecified():
			return ClassUnspecified
		default:
			return ClassIP
		}
	}
	if strings.Contains(host, ".") {
		return ClassDotted
	}
	return ClassBare
}

// hostOf returns the lowercased hostname of a URL, or "".
func hostOf(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// browserLoaded names the services the BROWSER fetches directly: OnlyOffice's
// api.js goes into a <script>, drawio goes into an <iframe>. The converter is
// only ever called server-to-server, so a container name is perfectly fine for
// it and must not be warned about.
var browserLoaded = map[string]bool{OnlyOffice: true, Drawio: true}

// callsBack names the services that fetch from filex and POST back to it, and
// therefore care about FILEX_PUBLIC_URL. Only OnlyOffice does.
var callsBack = map[string]bool{OnlyOffice: true}

// LookupFunc reports whether host is DEFINITELY absent from DNS as this
// machine sees it.
//
// ⚠ It must return false for every inconclusive answer — timeout, SERVFAIL,
// no resolver configured. An advisory raised on a slow resolver would fire on
// a working setup, which is the failure mode this whole file exists to avoid.
type LookupFunc func(host string) (definitelyNotFound bool)

// AdvisoryInput is one service's addresses, as they are right now.
type AdvisoryInput struct {
	Service      string
	ServiceURL   string
	PublicURL    string
	PublicURLSet bool
	// CallbackURL is the separate address the service uses to reach filex,
	// when the operator set one. It, not PublicURL, is what the callback
	// advisories must judge: the whole point of the field is that the two can
	// differ.
	CallbackURL string
	Lookup      LookupFunc
}

// Advisories returns everything worth saying about one service's addresses.
//
// serviceURL is the row's URL; publicURL and publicURLSet come from the
// process config (publicURLSet is false when nobody chose one and it defaulted
// to http://localhost:5212 — worth saying out loud, because the operator does
// not know a default is in play). lookup may be nil, which skips the DNS note.
func Advisories(service, serviceURL, publicURL string, publicURLSet bool, lookup LookupFunc) []Advisory {
	return Advise(AdvisoryInput{
		Service: service, ServiceURL: serviceURL,
		PublicURL: publicURL, PublicURLSet: publicURLSet, Lookup: lookup,
	})
}

// Advise is Advisories with the callback address included.
func Advise(in AdvisoryInput) []Advisory {
	service, serviceURL, publicURL, publicURLSet, lookup := in.Service, in.ServiceURL, in.PublicURL, in.PublicURLSet, in.Lookup
	out := []Advisory{}
	if strings.TrimSpace(serviceURL) == "" {
		return out
	}
	svcClass := HostClass(serviceURL)
	pubClass := HostClass(publicURL)

	// The address the SERVICE actually comes back to. With a callback URL set,
	// the public URL is only a browser-facing address and says nothing about
	// this leg — judging it would raise warnings on the very setup the field
	// exists to make work.
	callbackURL := strings.TrimRight(strings.TrimSpace(in.CallbackURL), "/")
	callbackSet := callbackURL != ""
	if callbackSet {
		publicURL = callbackURL
		publicURLSet = true
		pubClass = HostClass(callbackURL)
	}

	if browserLoaded[service] {
		switch svcClass {
		case ClassBare:
			out = append(out, Advisory{
				Code: CodeBrowserBareHost, Field: FieldURL, Severity: SeverityWarning,
				Detail: hostOf(serviceURL),
				Message: fmt.Sprintf(
					"%q is a single-label host name. filex can reach it from inside the container network, but the browser loads this service directly and cannot resolve it. Use an address the browser can open.",
					hostOf(serviceURL)),
			})
		case ClassLoopback:
			// Suppressed when filex itself is reached over loopback: then the
			// browser IS on this host and localhost is the right answer.
			if pubClass != ClassLoopback && pubClass != ClassEmpty {
				out = append(out, Advisory{
					Code: CodeBrowserLoopbackHost, Field: FieldURL, Severity: SeverityWarning,
					Detail: hostOf(serviceURL),
					Message: fmt.Sprintf(
						"This is a loopback address, but filex is published at %s — the people who open it are not on this host, and to their browser %q means their own machine.",
						publicURL, hostOf(serviceURL)),
				})
			}
		}
	}

	if callsBack[service] {
		switch pubClass {
		case ClassLoopback, ClassUnspecified:
			// ⚠ Severity is decided by whether the document server is
			// DEFINITELY somewhere else. If its URL is loopback too, filex and
			// the document server may share one network namespace (host
			// networking, or one pod), and then loopback is genuinely right —
			// so that ambiguous case is a note, never a warning. A warning
			// that fires on a working setup is worse than none.
			severity := SeverityWarning
			msg := fmt.Sprintf(
				"The document server fetches the document from filex and posts the save back to %s. Inside its own container that address is the document server, not filex. Set the callback URL — or FILEX_PUBLIC_URL — to an address the document server can reach.",
				publicURL)
			if !publicURLSet {
				msg = fmt.Sprintf(
					"FILEX_PUBLIC_URL was never set, so it defaulted to %s. The document server fetches the document from filex and posts the save back to that address — inside its own container it is the document server, not filex.",
					publicURL)
			}
			if svcClass == ClassLoopback {
				severity = SeverityNote
				msg = fmt.Sprintf(
					"Both filex (%s) and the document server are on loopback. That works only while they share one network namespace — host networking, or a single pod. If the document server runs in its own container, the save callback will not reach filex.",
					publicURL)
			}
			out = append(out, Advisory{
				Code: CodePublicURLLoopback, Field: FieldPublicURL, Severity: severity,
				Detail: publicURL, Message: msg,
			})
		case ClassBare:
			if callbackSet {
				// A container name is exactly right here: this address is read
				// by the document server alone, and the share links are built
				// from the public URL, which is a separate value now.
				break
			}
			out = append(out, Advisory{
				Code: CodePublicURLBareHost, Field: FieldPublicURL, Severity: SeverityNote,
				Detail: publicURL,
				Message: fmt.Sprintf(
					"FILEX_PUBLIC_URL is %s, a container-network name. The document server can reach it, but every share link and e-mail filex builds from it will not open in a browser outside that network. Put the browser's address here and the container's address in the callback URL.",
					publicURL),
			})
		case ClassDotted:
			if lookup != nil && lookup(hostOf(publicURL)) {
				out = append(out, Advisory{
					Code: CodePublicURLUnresolved, Field: FieldPublicURL, Severity: SeverityNote,
					Detail: hostOf(publicURL),
					Message: fmt.Sprintf(
						"filex itself cannot resolve %q. That is expected if your document server uses a different DNS view; otherwise FILEX_PUBLIC_URL points at a name nothing can look up.",
						hostOf(publicURL)),
				})
			}
		}
	}
	return out
}

// HasWarning reports whether any advisory is severity warning — the signal the
// admin UI uses to withhold a "configuration complete" badge.
func HasWarning(list []Advisory) bool {
	for _, a := range list {
		if a.Severity == SeverityWarning {
			return true
		}
	}
	return false
}

// SystemLookup returns a LookupFunc backed by this machine's resolver.
//
// ⚠ It answers true ONLY for an authoritative "no such host". A timeout, a
// SERVFAIL or a machine with no resolver at all answers false, because an
// advisory raised on those would fire on setups that work.
func SystemLookup(ctx context.Context, timeout time.Duration) LookupFunc {
	return func(host string) bool {
		if host == "" {
			return false
		}
		c, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		_, err := net.DefaultResolver.LookupHost(c, host)
		if err == nil {
			return false
		}
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return true
		}
		return false
	}
}
