package shortlink

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// ── What counts as a click ─────────────────────────────────────────────────
//
// A short link pasted into a chat is opened by the chat before anybody
// clicks it: Slack, WhatsApp, Telegram, Teams and the rest fetch the address
// to draw a preview, and browsers prefetch links they expect to be followed.
// Those requests still get the redirect (a preview of the target is what the
// sender wanted), but they are not counted. Neither is a HEAD, nor the same
// client opening the same link again within ClickDedupe.

// previewAgents are fragments of the User-Agent of link previewers and
// crawlers, lower case. A fragment, not a name: "bot" alone covers most.
var previewAgents = []string{
	"bot", "crawler", "spider", "slurp", "preview",
	"facebookexternalhit", "facebot", "embedly", "iframely", "skypeuripreview",
	"whatsapp", "telegram", "discord", "slack", "linkedin", "twitter",
	"pinterest", "mastodon", "pleroma", "misskey", "vkshare", "viber",
	"line-poker", "kakaotalk", "snapchat", "zalo", "applebot",
	"google-pagerenderer", "googleother", "google-inspectiontool", "yandex",
	"bitly", "outbrain", "nuzzel", "quora link preview", "redditbot",
	"mattermost", "rocket.chat", "synapse", "element", "signal",
}

// Countable reports whether r is a person opening the link: a GET, not a
// prefetch, from a client that does not call itself a previewer or a crawler.
// A request with no User-Agent at all is not counted either.
func Countable(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	for _, h := range []string{"Purpose", "Sec-Purpose", "X-Purpose", "X-Moz"} {
		v := strings.ToLower(r.Header.Get(h))
		if strings.Contains(v, "prefetch") || strings.Contains(v, "preview") || strings.Contains(v, "prerender") {
			return false
		}
	}
	ua := strings.ToLower(r.UserAgent())
	if strings.TrimSpace(ua) == "" {
		return false
	}
	for _, f := range previewAgents {
		if strings.Contains(ua, f) {
			return false
		}
	}
	return true
}

// ClickDedupe is how long one client's repeated opening of one link counts
// once: the rest of the minute it was first counted in, and the next one.
const ClickDedupe = time.Minute

// Dedupe remembers the (link, client) pairs counted in the last minute or
// two. O(1) a visit: two generations, the older dropped whole when the
// minute turns, each at most MaxClients pairs. Past that a visit is counted
// without the check: more distinct visitors in a minute than the map holds
// are counted, never silently dropped.
type Dedupe struct {
	// Now is the clock; nil = time.Now.
	Now func() time.Time

	mu        sync.Mutex
	cur, prev map[dedupeKey]struct{}
	gen       time.Time
}

type dedupeKey struct {
	link   int64
	client string
}

// First reports whether this is the client's first opening of the link in
// this minute or the last, and remembers it.
func (d *Dedupe) First(link int64, client string) bool {
	now := time.Now()
	if d.Now != nil {
		now = d.Now()
	}
	gen := now.Truncate(ClickDedupe)
	k := dedupeKey{link, client}
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case d.cur == nil:
		d.cur, d.gen = map[dedupeKey]struct{}{}, gen
	case gen.After(d.gen):
		if gen.Sub(d.gen) == ClickDedupe {
			d.prev = d.cur
		} else {
			d.prev = nil
		}
		d.cur, d.gen = map[dedupeKey]struct{}{}, gen
	}
	if _, ok := d.cur[k]; ok {
		return false
	}
	if _, ok := d.prev[k]; ok {
		return false
	}
	if len(d.cur) >= MaxClients {
		return true
	}
	d.cur[k] = struct{}{}
	return true
}
