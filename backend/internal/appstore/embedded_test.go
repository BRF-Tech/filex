package appstore_test

// The embedded store (#162): a connection made with a store's one-time code
// and held to this filex's own address and key; requests signed the way the
// store checks them (the shared vector); the catalog verified with the
// trusted index key, kept 10 minutes, served stale while the store is down
// and never served when it does not verify; icons held to their names.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/appstore"
	"github.com/brf-tech/filex/backend/internal/appstore/storetest"
)

const testCode = "fxc_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func embFix(t *testing.T) *fix {
	t.Helper()
	f := newFix(t, nil)
	f.st.AddKey("idx-1", appstore.UseIndex, appstore.KeyActive)
	f.st.AddKey("lic-1", appstore.UseLicense, appstore.KeyActive)
	f.trust(t)
	return f
}

func (f *fix) connect(t *testing.T) {
	t.Helper()
	f.st.AddConnectCode(testCode)
	if _, err := f.svc.Connect(context.Background(), f.st.Origin(), testCode, testInstance, nil, "admin@test"); err != nil {
		t.Fatalf("connect: %v", err)
	}
}

// The request text and its signature are the store's: the vector fapps made
// with Node's crypto (fapps backend/internal/install/testdata).
func TestInstanceRequest_MatchesTheStoresVector(t *testing.T) {
	raw, err := os.ReadFile("testdata/instance-request-vector.json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	seed, _ := hex.DecodeString(m["seed_hex"])
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	if hex.EncodeToString(pub) != m["public_key"] || (appstore.Key{Ed25519: m["public_key"]}).Fingerprint() != m["key_fingerprint"] {
		t.Fatalf("key %x", pub)
	}
	text := appstore.RequestText(m["method"], m["path"], m["instance_id"], m["key_fingerprint"], m["timestamp"], m["nonce"], []byte(m["body"]))
	sum := sha256.Sum256([]byte(text))
	if hex.EncodeToString(sum[:]) != m["text_sha256"] {
		t.Fatalf("text sha256 %x, want %s\n%s", sum, m["text_sha256"], text)
	}
	if got := hex.EncodeToString(ed25519.Sign(priv, []byte(m["text_sha256"]))); got != m["signature"] {
		t.Fatalf("signature %s, want %s", got, m["signature"])
	}
}

func TestConnect_BindsANewKeyToThisFilexOnly(t *testing.T) {
	f := embFix(t)
	ctx := context.Background()
	// The code's shape is checked before anything is sent.
	if _, err := f.svc.Connect(ctx, f.st.Origin(), "not-a-code", testInstance, nil, ""); code(err) != appstore.CodeConnectCode {
		t.Fatalf("a malformed code: %v", err)
	}
	// An unknown code: the store's 404.
	if _, err := f.svc.Connect(ctx, f.st.Origin(), testCode, testInstance, nil, ""); code(err) != appstore.CodeConnectCode {
		t.Fatalf("an unknown code: %v", err)
	}
	// This filex is not the one the store's instance is: the store refuses.
	f.st.AddConnectCode(testCode)
	if _, err := f.svc.Connect(ctx, f.st.Origin(), testCode, "https://other.example", nil, ""); code(err) != appstore.CodeConnectCode {
		t.Fatalf("another filex: %v", err)
	}
	v, err := f.svc.Connect(ctx, f.st.Origin(), testCode, testInstance, nil, "admin@test")
	if err != nil {
		t.Fatal(err)
	}
	pub := f.st.ConnectedKey()
	if !v.Connected || v.InstanceID != storetest.DefaultInstanceID || v.KeyFingerprint != (appstore.Key{Ed25519: hex.EncodeToString(pub)}).Fingerprint() {
		t.Fatalf("connection %+v", v)
	}
	if f.st.ConnectedOrigin() != testInstance {
		t.Fatalf("the store was told %q", f.st.ConnectedOrigin())
	}
	// The private key is sealed; the row holds neither its seed nor the code.
	for k, row := range f.mem.rows {
		if strings.HasPrefix(k, "conn:") && (strings.Contains(row, testCode) || !strings.Contains(row, "key_sealed")) {
			t.Fatalf("connection row %s", row)
		}
	}
	if acts := strings.Join(f.mem.actions(), ","); !strings.Contains(acts, "app_store.connect") {
		t.Fatalf("audit %s", acts)
	}
}

func TestConnect_AnUntrustedStoreIsNeverAsked(t *testing.T) {
	f := newFix(t, nil)
	f.st.AddKey("idx-1", appstore.UseIndex, appstore.KeyActive)
	f.st.AddConnectCode(testCode)
	if _, err := f.svc.Connect(context.Background(), f.st.Origin(), testCode, testInstance, nil, ""); code(err) != appstore.CodeTrustRequired {
		t.Fatalf("untrusted: %v", err)
	}
	if f.st.ConnectedKey() != nil {
		t.Fatal("an untrusted store was sent a key")
	}
}

func TestRequestIntent_IsSignedAndHeldToTheStoresRules(t *testing.T) {
	f := embFix(t)
	ctx := context.Background()
	// Not connected: nothing is sent.
	if _, err := f.svc.RequestIntent(ctx, f.st.Origin(), "lang-eo", "1.0.0", "", nil); code(err) != appstore.CodeNotConnected {
		t.Fatalf("not connected: %v", err)
	}
	f.connect(t)
	f.st.OnIntent(func(ask storetest.IntentAsk) (string, string, int) {
		return "tok-" + strings.Repeat("x", 20), "0123456789abcdef", 0
	})
	got, err := f.svc.RequestIntent(ctx, f.st.Origin(), "lang-eo", "1.0.0", "FXL-KEY1", nil)
	if err != nil {
		t.Fatalf("request: %v (store refused %v)", err, f.st.Refused())
	}
	if got.Token != "tok-"+strings.Repeat("x", 20) || got.TokenID != "0123456789abcdef" || got.App != "lang-eo" || got.ExpiresAt.IsZero() {
		t.Fatalf("intent %+v", got)
	}
	asks := f.st.IntentAsks()
	if len(asks) != 1 || asks[0].App != "lang-eo" || asks[0].Version != "1.0.0" || asks[0].LicenseKey != "FXL-KEY1" {
		t.Fatalf("asks %+v", asks)
	}
	// Two requests are two nonces: neither is a replay of the other.
	if _, err := f.svc.RequestIntent(ctx, f.st.Origin(), "lang-eo", "", "", nil); err != nil {
		t.Fatalf("second request: %v (store refused %v)", err, f.st.Refused())
	}
	if len(f.st.Refused()) != 0 {
		t.Fatalf("the store refused %v", f.st.Refused())
	}
	// The store refusing what was asked is said as such.
	f.st.OnIntent(func(storetest.IntentAsk) (string, string, int) { return "", "", http.StatusConflict })
	if _, err := f.svc.RequestIntent(ctx, f.st.Origin(), "lang-eo", "", "", nil); code(err) != appstore.CodeStoreRefusal {
		t.Fatalf("a refusal: %v", err)
	}
	if ok, err := f.svc.Disconnect(ctx, f.st.Origin(), nil); err != nil || !ok {
		t.Fatalf("disconnect: %v %v", ok, err)
	}
	if f.st.Disconnects() != 1 || f.svc.Connected(ctx, f.st.Origin()) {
		t.Fatal("the store was not told, or the key stayed")
	}
	if _, err := f.svc.RequestIntent(ctx, f.st.Origin(), "lang-eo", "", "", nil); code(err) != appstore.CodeNotConnected {
		t.Fatalf("after disconnect: %v", err)
	}
}

func TestRequestIntent_AKeyThatNoLongerOpensIsNotSent(t *testing.T) {
	f := embFix(t)
	f.connect(t)
	// Another FILEX_SECRET_KEY: the sealed key does not open.
	for k, row := range f.mem.rows {
		if strings.HasPrefix(k, "conn:") {
			var c map[string]any
			_ = json.Unmarshal([]byte(row), &c)
			c["key_sealed"] = "v1:not-sealed"
			b, _ := json.Marshal(c)
			f.mem.rows[k] = string(b)
		}
	}
	if _, err := f.svc.RequestIntent(context.Background(), f.st.Origin(), "lang-eo", "", "", nil); code(err) != appstore.CodeNotConnected {
		t.Fatalf("an unopenable key: %v", err)
	}
	if len(f.st.IntentAsks()) != 0 {
		t.Fatal("a request went out without a key")
	}
}

func TestRequestFor_RemembersTheRequestALinkWasAskedFor(t *testing.T) {
	f := embFix(t)
	ctx := context.Background()
	if f.svc.RequestFor(ctx, f.st.Origin(), "0123456789abcdef") != 0 {
		t.Fatal("a link nobody asked for names a request")
	}
	if err := f.svc.RememberRequest(ctx, f.st.Origin(), "0123456789abcdef", 42, time.Now().Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := f.svc.RequestFor(ctx, f.st.Origin(), "0123456789abcdef"); got != 42 {
		t.Fatalf("request %d", got)
	}
}

// ── The catalog ────────────────────────────────────────────────────────

var pngIcon = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 32)...)

func iconFile() string {
	sum := sha256.Sum256(pngIcon)
	return hex.EncodeToString(sum[:]) + ".png"
}

func TestCatalog_VerifiedProjectedAndCached(t *testing.T) {
	f := embFix(t)
	ctx := context.Background()
	f.st.PutMedia(iconFile(), pngIcon)
	f.st.SetIndex("idx-1", f.st.IndexDoc(
		map[string]any{"name": "lang-eo", "kind": "language_pack", "version": "1.0.0", "icon": iconFile()},
		map[string]any{"name": "pdfx", "version": "2.0.0", "permissions": []string{"files:read"}},
		map[string]any{"name": "gone", "version": "1.0.0", "revoked": true},
		map[string]any{"name": "yanked", "version": "1.0.0", "yanked": true},
	), false)
	c, err := f.svc.Catalog(ctx, f.st.Origin())
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Apps) != 2 || c.Apps[0].Name != "lang-eo" || c.Apps[1].Name != "pdfx" || c.Stale || c.Serial != 7 {
		t.Fatalf("catalog %+v", c)
	}
	if a := c.Apps[1]; a.Version != "2.0.0" || strings.Join(a.Permissions, ",") != "files:read" || a.Publisher != "Acme" || !a.PublisherVerified {
		t.Fatalf("pdfx %+v", a)
	}
	if c.Apps[0].Icon != iconFile() {
		t.Fatalf("icon %q", c.Apps[0].Icon)
	}
	// Kept: a second read does not ask the store.
	hits := f.st.IndexReads()
	if _, err := f.svc.Catalog(ctx, f.st.Origin()); err != nil || f.st.IndexReads() != hits {
		t.Fatalf("not cached: %v, %d reads", err, f.st.IndexReads()-hits)
	}
	// Ten minutes later it is read again.
	f.clock.pass(11 * time.Minute)
	if _, err := f.svc.Catalog(ctx, f.st.Origin()); err != nil || f.st.IndexReads() != hits+1 {
		t.Fatalf("not read again: %v", err)
	}
	// The icon: only one the catalog names, only its own bytes.
	b, ctype, err := f.svc.Media(ctx, f.st.Origin(), iconFile())
	if err != nil || ctype != "image/png" || !bytes.Equal(b, pngIcon) {
		t.Fatalf("icon: %v %s", err, ctype)
	}
	other := strings.Repeat("c", 64) + ".png"
	f.st.PutMedia(other, pngIcon)
	if _, _, err := f.svc.Media(ctx, f.st.Origin(), other); code(err) != appstore.CodeMediaInvalid {
		t.Fatalf("an icon the catalog does not name: %v", err)
	}
	if _, _, err := f.svc.Media(ctx, f.st.Origin(), "../keys.json"); code(err) != appstore.CodeMediaInvalid {
		t.Fatalf("a path: %v", err)
	}
}

func TestCatalog_AnIconWhoseBytesAreNotItsNameIsRefused(t *testing.T) {
	f := embFix(t)
	f.st.PutMedia(iconFile(), append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{2}, 32)...))
	f.st.SetIndex("idx-1", f.st.IndexDoc(map[string]any{"name": "lang-eo", "version": "1.0.0", "icon": iconFile()}), false)
	if _, _, err := f.svc.Media(context.Background(), f.st.Origin(), iconFile()); code(err) != appstore.CodeMediaInvalid {
		t.Fatalf("swapped bytes: %v", err)
	}
}

func TestCatalog_AnIndexThatDoesNotVerifyIsNeverServed(t *testing.T) {
	f := embFix(t)
	ctx := context.Background()
	f.st.SetIndex("idx-1", f.st.IndexDoc(map[string]any{"name": "lang-eo", "version": "1.0.0"}), true)
	if _, err := f.svc.Catalog(ctx, f.st.Origin()); code(err) != appstore.CodeIndexInvalid {
		t.Fatalf("tampered: %v", err)
	}
	// Signed by a key the store is not trusted with (a license key).
	f.st.SetIndex("lic-1", f.st.IndexDoc(map[string]any{"name": "lang-eo", "version": "1.0.0"}), false)
	if _, err := f.svc.Catalog(ctx, f.st.Origin()); code(err) != appstore.CodeIndexInvalid {
		t.Fatalf("another use's key: %v", err)
	}
	// Expired.
	doc := f.st.IndexDoc(map[string]any{"name": "lang-eo", "version": "1.0.0"})
	doc["expires_at"] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	f.st.SetIndex("idx-1", doc, false)
	if _, err := f.svc.Catalog(ctx, f.st.Origin()); code(err) != appstore.CodeIndexInvalid {
		t.Fatalf("expired: %v", err)
	}
	// Another schema.
	doc = f.st.IndexDoc(map[string]any{"name": "lang-eo", "version": "1.0.0"})
	doc["schema"] = 2
	f.st.SetIndex("idx-1", doc, false)
	if _, err := f.svc.Catalog(ctx, f.st.Origin()); code(err) != appstore.CodeIndexInvalid {
		t.Fatalf("schema 2: %v", err)
	}
}

func TestCatalog_WhileTheStoreIsDownTheLastGoodOneIsServedStale(t *testing.T) {
	f := embFix(t)
	ctx := context.Background()
	f.st.SetIndex("idx-1", f.st.IndexDoc(map[string]any{"name": "lang-eo", "version": "1.0.0"}), false)
	if _, err := f.svc.Catalog(ctx, f.st.Origin()); err != nil {
		t.Fatal(err)
	}
	f.clock.pass(11 * time.Minute)
	f.st.SetDown(true)
	c, err := f.svc.Catalog(ctx, f.st.Origin())
	if err != nil || !c.Stale || len(c.Apps) != 1 {
		t.Fatalf("down, with a catalog before: %v %+v", err, c)
	}
	// A store never read before, down: the error.
	g := embFix(t)
	g.st.SetDown(true)
	if _, err := g.svc.Catalog(ctx, g.st.Origin()); err == nil {
		t.Fatal("a store never read, down, answered a catalog")
	}
}

func TestView_WhoSeesTheScreen(t *testing.T) {
	f := embFix(t)
	ctx := context.Background()
	origin := f.st.Origin()
	if v, err := f.svc.View(ctx, appstore.ScopeDefault); err != nil || v != nil {
		t.Fatalf("never set: %+v %v", v, err)
	}
	// A store that is not trusted cannot be shown.
	if _, err := f.svc.SetView(ctx, "t5", appstore.ViewSettings{Enabled: true, Stores: []string{"https://evil.example"}}, nil, ""); code(err) != appstore.CodeTrustRequired {
		t.Fatalf("untrusted store: %v", err)
	}
	if _, err := f.svc.SetView(ctx, "t5", appstore.ViewSettings{Enabled: true, Stores: []string{origin}, Audience: "roles", Roles: []string{"root"}}, nil, ""); err == nil {
		t.Fatal("an unknown role was taken")
	}
	v, err := f.svc.SetView(ctx, "t5", appstore.ViewSettings{Enabled: true, Stores: []string{origin, origin}, Audience: "groups", Groups: []int64{9, 3, 9}}, nil, "admin@test")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Stores) != 1 || len(v.Groups) != 2 || v.Groups[0] != 3 {
		t.Fatalf("normalized %+v", v)
	}
	got, _ := f.svc.View(ctx, "t5")
	for _, c := range []struct {
		role   string
		groups []int64
		want   bool
	}{
		{"user", nil, false}, {"user", []int64{3}, true}, {"admin", []int64{4}, false}, {"viewer", []int64{9}, true},
	} {
		if got.Allows(c.role, c.groups) != c.want {
			t.Errorf("%s %v: %v", c.role, c.groups, !c.want)
		}
	}
	// Another tenant's scope sees nothing.
	if other, _ := f.svc.View(ctx, "t6"); other.Allows("admin", []int64{3}) {
		t.Fatal("tenant 6 sees tenant 5's screen")
	}
	// The store no longer trusted: the screen shows nothing.
	if _, err := f.svc.Remove(ctx, origin, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = f.svc.View(ctx, "t5")
	if got.Allows("user", []int64{3}) || len(got.Stores) != 0 {
		t.Fatalf("after untrust %+v", got)
	}
}
