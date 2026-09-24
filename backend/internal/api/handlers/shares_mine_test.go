package handlers_test

// "Paylaştıklarım" / "My shares" and the audited PIN door.
//
// The product owner, 2026-09-20: *"paylaşımın sahibi ve admin alabilir
// şifreyi. Paylaşım sahibi paylaştıklarını da bir menüde görebilir olsun,
// 'Paylaştıklarım' diye. Admin değilse göremez, kopyalayamaz."*
//
// Two sentences, four properties, and this file measures all four through the
// real router — cookies, middleware chain, tenant wrapper and all — because
// three of them ARE the middleware chain:
//
//  1. a person sees THEIR links here, and only theirs;
//  2. the PIN comes back in plain to the creator and to an administrator;
//  3. to nobody else — a third account gets 403, whatever it knows about ids;
//  4. every read, including the ones that find nothing, leaves an audit row.
//
// ⚠ And the fifth, which is the one a future hand is most likely to undo: the
// LISTING never carries the PIN, the sealed column or the hash. `has_pin` and
// `pin_recoverable` are booleans about a link; everything else about the PIN
// leaves the server through the single-row endpoint or not at all.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

const mineSecretKey = "an-instance-secret-long-enough-for-the-tests"

// mineFixture is one instance with a storage, a file, an admin, an ordinary
// owner and an unrelated third account — the cast the owner's sentence names.
type mineFixture struct {
	srv   *httptest.Server
	store db.Store
	node  *model.Node

	adminEmail, adminPw string
	ownerEmail, ownerPw string
	otherEmail, otherPw string
	ownerID             int64
}

// signedIn returns a fresh client already signed in as one account, so one
// test can act as three people without three servers.
func signedIn(t *testing.T, srv *httptest.Server, email, pw string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	c := &http.Client{Jar: jar}
	testutil.LoginAs(t, srv, c, email, pw)
	return c
}

// newMineFixture builds the instance. `key` empty means an install with no
// FILEX_SECRET_KEY, which is one of the cases that has to degrade honestly.
func newMineFixture(t *testing.T, key string) (*mineFixture, *http.Client, func(email, pw string) *http.Client) {
	t.Helper()
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.SecretKey = key })

	adminEmail, adminPw := testutil.SeedAdmin(t, store)
	const ownerEmail, ownerPw = "sahibi@test.local", "OwnerPass!1"
	const otherEmail, otherPw = "baskasi@test.local", "OtherPass!1"
	testutil.SeedRegularUser(t, store, ownerEmail, ownerPw)
	testutil.SeedRegularUser(t, store, otherEmail, otherPw)

	ctx := context.Background()
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "depo", Driver: "local", MountPath: "/depo",
		ConfigJSON: []byte(`{"path":"` + jsonPath(t.TempDir()) + `"}`),
		SyncMode:   model.SyncModeOnDemand, Enabled: true,
	})
	require.NoError(t, err)
	n, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, Name: "teklif.pdf", Path: "teklif.pdf", Type: model.NodeTypeFile,
		PathHash: pathkey.Hash(st.ID, "teklif.pdf"), SeenAt: time.Now(),
	})
	require.NoError(t, err)

	owner, err := store.GetUserByEmail(ctx, ownerEmail)
	require.NoError(t, err)

	f := &mineFixture{
		srv: srv, store: store, node: n,
		adminEmail: adminEmail, adminPw: adminPw,
		ownerEmail: ownerEmail, ownerPw: ownerPw,
		otherEmail: otherEmail, otherPw: otherPw,
		ownerID: owner.ID,
	}
	as := func(email, pw string) *http.Client { return signedIn(t, srv, email, pw) }
	return f, as(ownerEmail, ownerPw), as
}

// mintShare creates a PIN-protected link THROUGH THE API as the given client,
// so the sealing this file relies on is the sealing the product does.
func mintShare(t *testing.T, f *mineFixture, c *http.Client, pin string) int64 {
	t.Helper()
	body := `{"node_id":` + strconv.FormatInt(f.node.ID, 10) + `,"pin":"` + pin + `"}`
	resp, err := c.Post(f.srv.URL+"/api/files/share", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode, "mint share: %s", raw)
	var out struct {
		Share struct {
			ID int64 `json:"id"`
		} `json:"share"`
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	require.NotZero(t, out.Share.ID)
	return out.Share.ID
}

type pinAnswer struct {
	Pin    *string `json:"pin"`
	Reason string  `json:"reason"`
}

func readPin(t *testing.T, c *http.Client, base string, id int64) (int, pinAnswer, string) {
	t.Helper()
	resp, err := c.Get(base + "/api/shares/" + strconv.FormatInt(id, 10) + "/pin")
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var a pinAnswer
	_ = json.Unmarshal(raw, &a)
	return resp.StatusCode, a, string(raw)
}

// ── 1. the two principals who may read a PIN ───────────────────────────

func TestSharePin_OwnerAndAdminCanRead_AThirdUserCannot(t *testing.T) {
	f, ownerClient, as := newMineFixture(t, mineSecretKey)
	const pin = "834595"
	id := mintShare(t, f, ownerClient, pin)

	t.Run("the creator", func(t *testing.T) {
		code, got, raw := readPin(t, ownerClient, f.srv.URL, id)
		require.Equal(t, http.StatusOK, code, raw)
		require.NotNil(t, got.Pin, "the person who minted the link must be able to be told its PIN again: %s", raw)
		require.Equal(t, pin, *got.Pin)
	})

	t.Run("an administrator", func(t *testing.T) {
		code, got, raw := readPin(t, as(f.adminEmail, f.adminPw), f.srv.URL, id)
		require.Equal(t, http.StatusOK, code, raw)
		require.NotNil(t, got.Pin, "an administrator must be able to read any link's PIN: %s", raw)
		require.Equal(t, pin, *got.Pin)
	})

	t.Run("anybody else", func(t *testing.T) {
		code, got, raw := readPin(t, as(f.otherEmail, f.otherPw), f.srv.URL, id)
		require.Equal(t, http.StatusForbidden, code,
			"a signed-in account that neither created the link nor administers the instance must be refused: %s", raw)
		require.Nil(t, got.Pin)
		require.NotContains(t, raw, pin, "a refusal must not leak what it refused")
	})

	t.Run("a stranger", func(t *testing.T) {
		code, _, raw := readPin(t, &http.Client{}, f.srv.URL, id)
		require.Equal(t, http.StatusUnauthorized, code, raw)
		require.NotContains(t, raw, pin)
	})
}

// ── 2. every read is written down ──────────────────────────────────────

func TestSharePin_EveryReadIsAudited(t *testing.T) {
	f, ownerClient, as := newMineFixture(t, mineSecretKey)
	const pin = "271828"
	id := mintShare(t, f, ownerClient, pin)

	code, _, _ := readPin(t, ownerClient, f.srv.URL, id)
	require.Equal(t, http.StatusOK, code)
	code, _, _ = readPin(t, as(f.adminEmail, f.adminPw), f.srv.URL, id)
	require.Equal(t, http.StatusOK, code)

	rows, total, err := f.store.ListAuditFiltered(context.Background(), nil, handlers.AuditActionPinReveal, nil, nil, 50, 0)
	require.NoError(t, err)
	require.EqualValues(t, 2, total, "two reads, two audit rows — an unlogged PIN read makes the whole rule unverifiable")

	target := strconv.FormatInt(id, 10)
	sawOwner, sawAdmin := false, false
	for _, row := range rows {
		require.Equal(t, "share", row.Entry.TargetType)
		require.Equal(t, target, row.Entry.TargetID, "the row must point at the share, by id")
		require.NotNil(t, row.Entry.UserID, "an audit row with no actor answers nothing")
		require.Equal(t, true, row.Entry.Metadata["revealed"])
		if row.Entry.Metadata["as_admin"] == true {
			sawAdmin = true
		} else {
			sawOwner = true
		}
		// The token is a LIVE public link; an audit table is not where it goes.
		blob, _ := json.Marshal(row.Entry)
		require.NotContains(t, string(blob), pin, "the audit row must not carry the PIN it recorded the reading of")
	}
	require.True(t, sawOwner && sawAdmin,
		"the row must say WHICH principal was used — 'the owner read their own' and 'an admin read somebody else's' are different events")
}

// A read that finds nothing is still a read, and still an attempt worth
// recording.
func TestSharePin_ARefusedReadIsAuditedToo(t *testing.T) {
	f, ownerClient, _ := newMineFixture(t, mineSecretKey)
	// No PIN on this link at all.
	body := `{"node_id":` + strconv.FormatInt(f.node.ID, 10) + `}`
	resp, err := ownerClient.Post(f.srv.URL+"/api/files/share", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var out struct {
		Share struct {
			ID int64 `json:"id"`
		} `json:"share"`
	}
	require.NoError(t, json.Unmarshal(raw, &out))

	code, got, body2 := readPin(t, ownerClient, f.srv.URL, out.Share.ID)
	require.Equal(t, http.StatusOK, code, body2)
	require.Nil(t, got.Pin)
	require.Equal(t, "no_pin", got.Reason, "a link with no PIN must say so in a word the screen can act on")

	rows, total, err := f.store.ListAuditFiltered(context.Background(), nil, handlers.AuditActionPinReveal, nil, nil, 50, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, false, rows[0].Entry.Metadata["revealed"])
	require.Equal(t, "no_pin", rows[0].Entry.Metadata["reason"])
}

// ── 3. an instance with no key degrades honestly ───────────────────────

func TestSharePin_NoSecretKeySaysSoInsteadOfAnEmptyBox(t *testing.T) {
	f, ownerClient, _ := newMineFixture(t, "") // no FILEX_SECRET_KEY
	const pin = "112358"
	id := mintShare(t, f, ownerClient, pin) // still mints — sharing must keep working

	code, got, raw := readPin(t, ownerClient, f.srv.URL, id)
	require.Equal(t, http.StatusOK, code, raw)
	require.Nil(t, got.Pin, "with no key there is nothing to show, and inventing an empty string would be a lie the UI would print")
	require.Equal(t, "no_secret_key", got.Reason,
		"the reason must name the INSTANCE-level cause, which is the one an operator can actually fix")
	require.NotContains(t, raw, pin)

	// …and the link itself is unharmed: it still asks the visitor for the PIN.
	sh, err := f.store.GetShareByID(context.Background(), id)
	require.NoError(t, err)
	require.True(t, sh.HasPin, "the link must still be PIN-protected")
	require.False(t, sh.PinRecoverable, "and must be advertised as un-showable rather than showable-and-empty")
	require.Empty(t, sh.PinEnc, "no key means nothing sealed — and never a plaintext column")
}

// A link minted before migration 00049: a hash, no sealed value, on an
// instance that HAS a key. Those links keep working; their PIN is gone.
func TestSharePin_ALinkFromBeforeThisFeatureSaysItCannotBeShown(t *testing.T) {
	f, ownerClient, _ := newMineFixture(t, mineSecretKey)
	ctx := context.Background()
	legacy, err := f.store.CreateShare(ctx, &model.Share{
		NodeID: f.node.ID, Token: "eski-baglanti", Kind: model.ShareKindDownload,
		PinHash: "$2a$10$0123456789012345678901uXQ3F0ZPqkiCWQcPNQm5m2i2h1dZ.a", CreatedBy: &f.ownerID,
	})
	require.NoError(t, err)
	_ = ownerClient

	code, got, raw := readPin(t, ownerClient, f.srv.URL, legacy.ID)
	require.Equal(t, http.StatusOK, code, raw)
	require.Nil(t, got.Pin)
	require.Equal(t, "not_recoverable", got.Reason,
		"an old link must be told apart from a misconfigured instance — the operator's next move differs")
}

// ── 4. the listing ─────────────────────────────────────────────────────

func TestMyShares_ListsOnlyTheCallersLinks_AndNeverThePIN(t *testing.T) {
	f, ownerClient, as := newMineFixture(t, mineSecretKey)
	const ownerPIN, otherPIN = "834595", "999111"
	mintShare(t, f, ownerClient, ownerPIN)
	otherClient := as(f.otherEmail, f.otherPw)
	mintShare(t, f, otherClient, otherPIN)

	resp, err := ownerClient.Get(f.srv.URL + "/api/shares")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	raw, _ := io.ReadAll(resp.Body)
	text := string(raw)

	var body struct {
		Items []struct {
			Share struct {
				ID             int64  `json:"id"`
				Token          string `json:"token"`
				HasPin         bool   `json:"has_pin"`
				PinRecoverable bool   `json:"pin_recoverable"`
			} `json:"share"`
			URL          string `json:"url"`
			NodePath     string `json:"node_path"`
			StorageName  string `json:"storage_name"`
			CreatorEmail string `json:"creator_email"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))

	require.Len(t, body.Items, 1, "a person's own listing must hold their own link and nobody else's")
	require.EqualValues(t, 1, body.Total)
	row := body.Items[0]
	require.True(t, row.Share.HasPin, "the row must say the link asks for a PIN")
	require.True(t, row.Share.PinRecoverable, "…and whether that PIN can still be shown, so the screen can offer the verb or say it cannot")
	require.Equal(t, "/teklif.pdf", "/"+strings.TrimPrefix(row.NodePath, "/"), "the row must say WHAT is shared")
	require.Equal(t, "depo", row.StorageName)
	require.Equal(t, "http://test.local/s/"+row.Share.Token, row.URL,
		"the link must come from the configured public origin, never from the browser's address (issue #32)")
	require.Empty(t, row.CreatorEmail,
		"the creator is always the caller here; naming an account on a user-scoped listing is how a listing becomes a directory")

	// The five things that must never be in a listing, at any depth.
	require.NotContains(t, text, ownerPIN, "the listing must never carry a PIN")
	require.NotContains(t, text, otherPIN)
	require.NotContains(t, text, "pin_hash", "the listing must never carry the hash column, under any name")
	require.NotContains(t, text, "pin_enc", "the listing must never carry the SEALED column either — a ciphertext travels where the plaintext must not")
	require.NotContains(t, text, "enc:v1:", "…nor its contents under another key")
	require.False(t, strings.Contains(strings.ToLower(text), `"pin":`), "no field named pin, however spelled")
}

// The admin listing beside it: same rule, and the one that already had a test
// saying the PIN can never appear there. Making the PIN recoverable must not
// have quietly changed that.
func TestAdminShares_StillNeverCarriesThePINAfterSealing(t *testing.T) {
	f, ownerClient, as := newMineFixture(t, mineSecretKey)
	const pin = "834595"
	mintShare(t, f, ownerClient, pin)

	resp, err := as(f.adminEmail, f.adminPw).Get(f.srv.URL + "/api/admin/shares?limit=10")
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	text := string(raw)
	require.NotContains(t, text, pin)
	require.NotContains(t, text, "pin_enc")
	require.NotContains(t, text, "enc:v1:")
	require.NotContains(t, text, "pin_hash")
	require.Contains(t, text, `"pin_recoverable":true`,
		"the admin row may say a PIN CAN be shown — that is a boolean about the link, and it is what lets the page offer the verb")
}

// A PIN is a secret arriving over a GET. Nothing along the way may keep it.
func TestSharePin_IsNeverCached(t *testing.T) {
	f, ownerClient, _ := newMineFixture(t, mineSecretKey)
	id := mintShare(t, f, ownerClient, "141592")

	resp, err := ownerClient.Get(f.srv.URL + "/api/shares/" + strconv.FormatInt(id, 10) + "/pin")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Contains(t, resp.Header.Get("Cache-Control"), "no-store",
		"a browser, a proxy and a service worker all feel entitled to keep a GET — this one they may not")
}
