package shortlink

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestCountable(t *testing.T) {
	person := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	for _, c := range []struct {
		method, ua string
		header     [2]string
		want       bool
	}{
		{"GET", person, [2]string{}, true},
		{"GET", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1", [2]string{}, true},
		{"GET", "curl/8.5.0", [2]string{}, true},
		{"HEAD", person, [2]string{}, false},
		{"GET", person, [2]string{"Sec-Purpose", "prefetch;prerender"}, false},
		{"GET", person, [2]string{"Purpose", "prefetch"}, false},
		{"GET", "", [2]string{}, false},
		{"GET", "Slackbot-LinkExpanding 1.0 (+https://api.slack.com/robots)", [2]string{}, false},
		{"GET", "facebookexternalhit/1.1 (+http://www.facebook.com/externalhit_uatext.php)", [2]string{}, false},
		{"GET", "WhatsApp/2.23.20.0", [2]string{}, false},
		{"GET", "TelegramBot (like TwitterBot)", [2]string{}, false},
		{"GET", "Mozilla/5.0 (compatible; Discordbot/2.0; +https://discordapp.com)", [2]string{}, false},
		{"GET", "Mozilla/5.0 (Windows NT 6.1; WOW64) SkypeUriPreview Preview/0.5 skype-url-preview@microsoft.com", [2]string{}, false},
		{"GET", "LinkedInBot/1.0 (compatible; Mozilla/5.0; Apache-HttpClient +http://www.linkedin.com)", [2]string{}, false},
		{"GET", "Twitterbot/1.0", [2]string{}, false},
		{"GET", "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)", [2]string{}, false},
		{"GET", "Synapse (bot; +https://github.com/matrix-org/synapse)", [2]string{}, false},
	} {
		r := httptest.NewRequest(c.method, "/l/abc", nil)
		r.Header.Set("User-Agent", c.ua)
		if c.header[0] != "" {
			r.Header.Set(c.header[0], c.header[1])
		}
		if got := Countable(r); got != c.want {
			t.Fatalf("%s %q %v: counted=%v, want %v", c.method, c.ua, c.header, got, c.want)
		}
	}
}

func TestDedupe(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)}
	d := &Dedupe{Now: clk.now}
	if !d.First(1, "a") || d.First(1, "a") {
		t.Fatal("second opening within the minute counted")
	}
	if !d.First(2, "a") || !d.First(1, "b") {
		t.Fatal("another link or another client not counted")
	}
	// The next minute still remembers it; the one after does not.
	clk.t = clk.t.Add(ClickDedupe)
	if d.First(1, "a") {
		t.Fatal("counted again in the next minute")
	}
	clk.t = clk.t.Add(2 * ClickDedupe)
	if !d.First(1, "a") {
		t.Fatal("not counted two minutes later")
	}
}
