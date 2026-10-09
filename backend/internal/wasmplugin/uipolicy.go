package wasmplugin

import (
	"crypto/sha256"
	"encoding/base64"
	"sort"
	"strings"
)

// ── The policy an interface is served under ──────────────────────────────
//
// ⚠⚠ ONE function builds it (UIPolicy), from the GRANT — never from anything
// in the package, and never from the manifest's wishes: an address the
// administrator did not approve is in no directive, whatever the bundle's own
// HTML says (a <meta> policy can only narrow this one). Every line below is a
// measurement (docs/APP-PLUGINS-API.md → "Serving", and the design report's
// harness, Chrome 153 / Firefox 150 / WebKit 26.4, 2026-09-27):
//
//   - `<P>` — the explicit origin and version path — and never 'self'. WebKit
//     reads 'self' in a sandboxed frame as the frame's OPAQUE origin and
//     refuses the app's own scripts (lesson #633). The bare filex origin is
//     never a source either: WebKit sends the session cookie with a sandboxed
//     frame's requests to its own site (lesson #634), so a source wider than
//     the package would let an interface fetch /api/… with that cookie.
//   - connect-src 'none' closes fetch, XHR, WebSocket, EventSource, beacons
//     and <a ping> in all three browsers. It does NOT close WebRTC (lesson
//     #636) — hence Connection-Allowlist (Chrome) and the bootstrap (Firefox).
//   - `sandbox allow-scripts` in the policy itself, so an interface address
//     opened directly in a tab is still an opaque origin with no storage and
//     no cookie (measured: without it, filex's localStorage and session cookie
//     were reachable in all three).
//   - frame-src/child-src 'none': the interface opens no frame of its own —
//     or, with `ui:frame-package`, frames of this version's own path and
//     nothing else. Never data: or blob: (a document of the page's own
//     making is a fresh realm the bootstrap never ran in), never another
//     version, another app, a page of filex or the network. Each such page
//     is a page of the package, so it is served HERE under the same policy:
//     the same `sandbox allow-scripts` (an opaque origin of its own, distinct
//     from the frame that opened it), the bootstrap first, connect-src as
//     granted. The frame's own sandbox attribute is inherited by every frame
//     inside it, whatever the app writes on the inner element.
//   - worker-src blob: only (a URL worker cannot start from an opaque origin
//     anyway, measured).
//   - connect-src gains `blob:` with `ui:connect-blob`: an address the page
//     made itself, in memory (an editor that only loads a document from an
//     address). It names no server. A blob: address is bound to the origin
//     that made it, and a framed page is an origin of its own: the page that
//     reads one makes it (docs/PLUGIN-KIT.md says so to authors).
//   - NO frame-ancestors, on purpose: any page may frame an interface (the
//     explorer is embedded in sites filex cannot list), and with
//     `ui:frame-package` the page that frames one of its pages is the
//     interface itself, an opaque origin that no source expression matches
//     - Chromium refused the framed page under `frame-ancestors *`
//     (2026-10-08, filex-office-editor's e2e). The page holds nothing for
//     whoever frames it: the sandbox and the bridge protect it. uiserve.go
//     tells the security-headers middleware (secheaders.OpenFraming) so it
//     does not add filex's own `frame-ancestors 'self'` either.

// uiBootstrap is the script filex puts first in every HTML file of an
// interface, before the app's own code: it takes WebRTC away — from the page
// and from every frame the page could make. In Firefox, together with the
// explorer page's frame-src, it is the one measure that stopped STUN/TURN
// traffic in the measurement; Chrome's is Connection-Allowlist. A seat belt,
// not a wall: a page that embeds the explorer without a frame-src of its own
// lets the interface navigate to a data:/blob: document it made, where this
// never ran (docs/INTEGRATION.md says what such a page should send).
const uiBootstrap = `(()=>{"use strict";` +
	`const N=["RTCPeerConnection","webkitRTCPeerConnection","mozRTCPeerConnection","RTCDataChannel","RTCSessionDescription","RTCIceCandidate","RTCRtpSender","RTCRtpReceiver","RTCRtpTransceiver","RTCDtlsTransport","RTCIceTransport","RTCSctpTransport","RTCCertificate","RTCPeerConnectionIceEvent","RTCDataChannelEvent","RTCTrackEvent","RTCDTMFSender","RTCStatsReport","RTCError","RTCErrorEvent","RTCEncodedAudioFrame","RTCEncodedVideoFrame","RTCRtpScriptTransform"];` +
	`const K=w=>{if(!w)return;for(const n of N){try{Object.defineProperty(w,n,{value:undefined,writable:false,configurable:false})}catch(e){try{delete w[n]}catch(_){}}}};` +
	`K(window);` +
	`const W=Object.getOwnPropertyDescriptor(HTMLIFrameElement.prototype,"contentWindow"),D=Object.getOwnPropertyDescriptor(HTMLIFrameElement.prototype,"contentDocument");` +
	`const G=r=>{if(!r||!r.nodeType)return;const l=r.querySelectorAll?Array.from(r.querySelectorAll("iframe,frame")):[];if(r.tagName==="IFRAME"||r.tagName==="FRAME")l.unshift(r);for(const f of l){try{K(W.get.call(f))}catch(e){}}};` +
	`Object.defineProperty(HTMLIFrameElement.prototype,"contentWindow",{get(){const w=W.get.call(this);K(w);return w},configurable:false});` +
	`Object.defineProperty(HTMLIFrameElement.prototype,"contentDocument",{get(){const d=D.get.call(this);if(d)K(d.defaultView);return d},configurable:false});` +
	`const P=(p,ns)=>{for(const n of ns){const o=p[n];if(typeof o!=="function")continue;Object.defineProperty(p,n,{value:function(...a){const r=o.apply(this,a);for(const x of a)G(x);G(this);return r},configurable:false,writable:false})}};` +
	`P(Node.prototype,["appendChild","insertBefore","replaceChild"]);` +
	`P(Element.prototype,["append","prepend","after","before","replaceWith","insertAdjacentElement","insertAdjacentHTML","replaceChildren"]);` +
	`for(const q of["innerHTML","outerHTML"]){const d=Object.getOwnPropertyDescriptor(Element.prototype,q);Object.defineProperty(Element.prototype,q,{get(){return d.get.call(this)},set(v){const p=this.parentNode;d.set.call(this,v);G(this);if(p)G(p)},configurable:false})}` +
	`window.addEventListener("load",e=>{const t=e.target;if(t&&(t.tagName==="IFRAME"||t.tagName==="FRAME")){try{K(W.get.call(t))}catch(_){}}},true);` +
	`})();`

// uiBootstrapTag is the element put into every HTML answer.
var uiBootstrapTag = "<script>" + uiBootstrap + "</script>"

// uiBootstrapHash is the CSP source that allows exactly that script.
var uiBootstrapHash = func() string {
	h := sha256.Sum256([]byte(uiBootstrap))
	return "'sha256-" + base64.StdEncoding.EncodeToString(h[:]) + "'"
}()

// uiPermissionsPolicy switches off every powerful feature the frame could
// otherwise ask the browser for (the frame has no `allow` either) — the
// devices, the sensors, the credentials, the screen, the ad and tracking
// APIs (security review UI-16). Two stay on on purpose: `sync-xhr` (an editor
// reads its own package with it — draw.io does) and `autoplay` (a media app
// plays what the person opened); neither reaches anything the CSP does not
// already hold.
//
// ⚠ Only features the browser KNOWS: Chrome logs "Error with
// Permissions-Policy header: Unrecognized feature" in every app author's
// console for each one it does not (measured, Chrome 153: ambient-light-
// sensor, attribution-reporting, direct-sockets, join-ad-interest-group,
// private-aggregation, run-ad-auction, shared-storage, shared-storage-select-
// url, smart-card, speaker-selection — none of them usable in a sandboxed
// opaque frame anyway).
const uiPermissionsPolicy = "accelerometer=(), bluetooth=(), browsing-topics=(), camera=(), " +
	"captured-surface-control=(), clipboard-read=(), clipboard-write=(), compute-pressure=(), " +
	"digital-credentials-get=(), display-capture=(), encrypted-media=(), fullscreen=(), gamepad=(), " +
	"geolocation=(), gyroscope=(), hid=(), identity-credentials-get=(), idle-detection=(), keyboard-map=(), " +
	"local-fonts=(), magnetometer=(), microphone=(), midi=(), otp-credentials=(), payment=(), " +
	"picture-in-picture=(), private-state-token-issuance=(), private-state-token-redemption=(), " +
	"publickey-credentials-create=(), publickey-credentials-get=(), screen-wake-lock=(), serial=(), " +
	"storage-access=(), usb=(), web-share=(), window-management=(), xr-spatial-tracking=()"

// UIPolicy is the Content-Security-Policy and the Connection-Allowlist an
// interface's HTML is served with. `pkg` is <P>: the explicit origin and
// version path the interface is served from, ending in "/"
// (https://files.example.com/_appui/drawio/3f9a0c1e5b2d4f60/). `grants` is
// what the administrator approved — the ONLY source of exceptions.
func UIPolicy(grants Grants, pkg string) (csp, allowlist string) {
	live := map[string][]string{}
	var liveURLs []string
	for p := range grants {
		if as, u, ok := uiNetOf(p); ok {
			live[as] = append(live[as], u)
			liveURLs = append(liveURLs, u)
		}
	}
	for k := range live {
		sort.Strings(live[k])
	}
	sort.Strings(liveURLs)
	src := func(base []string, as string) string {
		return strings.Join(append(base, live[as]...), " ")
	}
	script := []string{pkg, uiBootstrapHash}
	if grants.Has(PermUIEval) {
		script = append(script, "'unsafe-eval'")
	}
	if grants.Has(PermUIWasmEval) {
		script = append(script, "'wasm-unsafe-eval'")
	}
	// connect-src: nothing — or, with `ui:package-fetch`, this version's own
	// path and nothing else: not another version, not another app, not
	// filex's API or pages, not the network; with `ui:connect-blob`, the
	// page's own blob: addresses too.
	var connect []string
	if grants.Has(PermUIPackageFetch) {
		connect = append(connect, pkg)
	}
	if grants.Has(PermUIConnectBlob) {
		connect = append(connect, "blob:")
	}
	if len(connect) == 0 {
		connect = []string{"'none'"}
	}
	// frame-src and child-src: no frame — or, with `ui:frame-package`, the
	// pages of this version's own path, and only them.
	frame := "'none'"
	if grants.Has(PermUIFramePackage) {
		frame = pkg
	}
	csp = strings.Join([]string{
		"default-src 'none'",
		"script-src " + strings.Join(script, " "),
		"style-src " + src([]string{pkg, "'unsafe-inline'"}, "style"),
		"img-src " + src([]string{pkg, "data:", "blob:"}, "img"),
		"font-src " + src([]string{pkg, "data:"}, "font"),
		"media-src " + src([]string{pkg, "blob:"}, "media"),
		"connect-src " + strings.Join(connect, " "),
		"worker-src blob:",
		"frame-src " + frame,
		"child-src " + frame,
		"object-src 'none'",
		"manifest-src 'none'",
		"form-action 'none'",
		"base-uri 'none'",
		"sandbox allow-scripts",
	}, "; ")
	// Connection-Allowlist (Chrome): what the document may connect to at all.
	// The live addresses must be named here too, or Chrome refuses them as
	// well (measured: 0 hits for a CDN missing from it).
	// The package's own path, not `response-origin` (which is filex's whole
	// origin): everything the interface loads of its own — its files, its
	// mirrors (ext/…), what `ui:package-fetch` reads, the pages
	// `ui:frame-package` frames — is under it. `blob:` is named nowhere here:
	// it is no network address, and e2e 228 measures in Chromium that a page
	// with `ui:connect-blob` reads its own blob: under this allowlist.
	patterns := []string{`"` + pkg + `*"`}
	for _, u := range liveURLs {
		if strings.HasSuffix(u, "/") {
			u += "*"
		}
		patterns = append(patterns, `"`+u+`"`)
	}
	allowlist = "(" + strings.Join(patterns, " ") + ")"
	return csp, allowlist
}

// uiInjectBootstrap puts the bootstrap first in an HTML document: after a
// doctype and a byte-order mark, before everything else — before any <head>,
// any <meta> and any script of the app's own, so no code of the app ever runs
// before it. (The answer names its charset in Content-Type, so a <meta
// charset> pushed past the first kilobyte by it changes nothing.)
func uiInjectBootstrap(doc []byte) []byte {
	i := 0
	if len(doc) >= 3 && doc[0] == 0xEF && doc[1] == 0xBB && doc[2] == 0xBF {
		i = 3
	}
	j := i
	for j < len(doc) && (doc[j] == ' ' || doc[j] == '\t' || doc[j] == '\r' || doc[j] == '\n') {
		j++
	}
	if len(doc)-j >= 9 && strings.EqualFold(string(doc[j:j+9]), "<!doctype") {
		if k := strings.IndexByte(string(doc[j:]), '>'); k >= 0 {
			i = j + k + 1
		}
	}
	out := make([]byte, 0, len(doc)+len(uiBootstrapTag))
	out = append(out, doc[:i]...)
	out = append(out, uiBootstrapTag...)
	out = append(out, doc[i:]...)
	return out
}
