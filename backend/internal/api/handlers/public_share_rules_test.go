package handlers_test

// #210 (0.54): the public link's rules and words are the SERVER's.
//
// The audit of "server work done in the browser" found the share surface
// deciding for itself - the PIN's length, the download command, a listed
// link's state, the uploader name's limit, the link-life ceiling, and every
// sentence of the JavaScript public pages. Each test below fails on the code
// before the change: the rule did not exist on the server, or the field it
// answers with was not there.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// postShare mints a link through the API as c and answers status + body.
func postShare(t *testing.T, c *http.Client, base string, body map[string]any) (int, map[string]any) {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	resp, err := c.Post(base+"/api/files/share", "application/json", strings.NewReader(string(b)))
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func getJSON(t *testing.T, c *http.Client, url string) (int, map[string]any) {
	t.Helper()
	resp, err := c.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

// ── B7: one PIN rule, when the link is made ────────────────────────────

func TestShareCreate_APINOutsideTheOneRuleIsRefused(t *testing.T) {
	f, owner, _ := newMineFixture(t, mineSecretKey)
	for _, pin := range []string{"123", "1234567890123", strings.Repeat("9", 40)} {
		status, body := postShare(t, owner, f.srv.URL, map[string]any{"node_id": f.node.ID, "pin": pin})
		require.Equal(t, http.StatusBadRequest, status, "a PIN of %d characters: %v", len(pin), body)
		assert.Equal(t, "pin_length", body["error"])
		assert.EqualValues(t, share.PINMinLen, body["pin_min"])
		assert.EqualValues(t, share.PINMaxLen, body["pin_max"])
		assert.Contains(t, body["message"], strconv.Itoa(share.PINMaxLen), "the sentence names the bound")
	}
	status, body := postShare(t, owner, f.srv.URL, map[string]any{"node_id": f.node.ID, "pin": "123456789012"})
	require.Equal(t, http.StatusOK, status, "twelve characters is inside the rule: %v", body)
}

func TestPublicShare_SaysThePINMaxWithAPIN(t *testing.T) {
	f, owner, _ := newMineFixture(t, mineSecretKey)
	status, body := postShare(t, owner, f.srv.URL, map[string]any{"node_id": f.node.ID, "pin": "4321"})
	require.Equal(t, http.StatusOK, status, "%v", body)
	tok := body["token"].(string)
	status, st := getJSON(t, &http.Client{}, f.srv.URL+"/api/public/s/"+tok)
	require.Equal(t, http.StatusOK, status, "%v", st)
	assert.Equal(t, true, st["needs_pin"])
	assert.EqualValues(t, share.PINMaxLen, st["pin_max"], "the PIN box stops where the rule does")

	status, body = postShare(t, owner, f.srv.URL, map[string]any{"node_id": f.node.ID})
	require.Equal(t, http.StatusOK, status, "%v", body)
	_, st = getJSON(t, &http.Client{}, f.srv.URL+"/api/public/s/"+body["token"].(string))
	_, has := st["pin_max"]
	assert.False(t, has, "a link with no PIN says nothing about one")
}

// ── A10: the server writes the download command ────────────────────────

func TestShareCreate_AnswersTheServersDownloadCommand(t *testing.T) {
	f, owner, _ := newMineFixture(t, mineSecretKey)
	status, body := postShare(t, owner, f.srv.URL, map[string]any{"node_id": f.node.ID, "pin": "4321"})
	require.Equal(t, http.StatusOK, status, "%v", body)
	inner := body["share"].(map[string]any)
	url := inner["url"].(string)
	cmd, ok := inner["download_command"].(map[string]any)
	require.True(t, ok, "the creation answer carries download_command: %v", inner)
	assert.Equal(t, "curl -fSL -o 'teklif.pdf' '"+url+"?pin=4321'", cmd["curl"])
	assert.Equal(t, "Invoke-WebRequest -UseBasicParsing -Uri '"+url+"?pin=4321' -OutFile 'teklif.pdf'", cmd["powershell"])
	// The flat envelope (embed.js) carries it too.
	assert.Equal(t, cmd, body["download_command"])
}

// ── B6: a listed link's state is the server's ──────────────────────────

func TestMyShares_SayWhereEachLinkStands(t *testing.T) {
	f, owner, _ := newMineFixture(t, mineSecretKey)
	status, body := postShare(t, owner, f.srv.URL, map[string]any{"node_id": f.node.ID, "max_downloads": 1})
	require.Equal(t, http.StatusOK, status, "%v", body)
	id := int64(body["id"].(float64))

	stateOf := func() string {
		t.Helper()
		_, list := getJSON(t, owner, f.srv.URL+"/api/shares")
		for _, it := range list["items"].([]any) {
			row := it.(map[string]any)["share"].(map[string]any)
			if int64(row["id"].(float64)) == id {
				s, _ := row["state"].(string)
				return s
			}
		}
		t.Fatalf("link %d is not listed", id)
		return ""
	}
	assert.Equal(t, model.ShareStateActive, stateOf())

	// Its one download spent: the date is still ahead, the link is over.
	require.NoError(t, f.store.IncrementShareDownload(context.Background(), id))
	assert.Equal(t, model.ShareStateExhausted, stateOf(), "a used-up link must not read as active")

	require.NoError(t, f.store.RevokeShare(context.Background(), id))
	assert.Equal(t, model.ShareStateRevoked, stateOf())
}

// ── A7: the public pages' sentences, from the server catalogue ─────────

func TestPublicStrings_AreTheServerCatalogueInOneLanguage(t *testing.T) {
	f, _, _ := newMineFixture(t, mineSecretKey)
	anon := &http.Client{}

	status, body := getJSON(t, anon, f.srv.URL+"/api/public/strings?lang=tr")
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, "tr", body["lang"])
	assert.Equal(t, "ltr", body["dir"])
	strs := body["strings"].(map[string]any)
	assert.Greater(t, len(strs), 80, "every server.public.* key")
	assert.Equal(t, srvtext.Template("tr", "server.public.drop_sub"), strs["drop_sub"])
	assert.Equal(t, srvtext.Template("tr", "server.public.drop_err_too_large"), strs["drop_err_too_large"],
		"the refusal the JavaScript page says before sending is the server's own")
	for k := range strs {
		assert.False(t, strings.HasPrefix(k, "server."), "keys come without the prefix: %s", k)
	}
	// A counted sentence lists its forms, as the no-JavaScript page script reads them.
	_, hasOne := strs["files_left_one"]
	assert.True(t, hasOne, "English and Turkish carry the singular form under its own key")

	// A language the server does not speak is not echoed back: the table is
	// in one it does (the instance default, else English).
	_, body = getJSON(t, anon, f.srv.URL+"/api/public/strings?lang=xx")
	assert.Contains(t, srvtext.BuiltinLanguages(), body["lang"])
}

// ── B14: the caller's own link-life ceiling ────────────────────────────

func TestCapabilities_ShareLinkMaxDaysIsTheCallersCeiling(t *testing.T) {
	pf := newPermFix(t)
	days := 3
	pf.ruleForMember(t, model.PermRuleSettings{ShareLinkMaxDays: &days})

	// The share dialog's reader is a person in a browser: the session, as the
	// explorer asks. ⚠ Not an API token here - this route only annotates its
	// caller (auth.AnnotateUser runs the enabled drivers), and the fixture
	// enables the password driver alone, so a token would read as nobody.
	caps := func(c *http.Client) map[string]any {
		t.Helper()
		status, body := getJSON(t, c, pf.URL+"/api/capabilities")
		require.Equal(t, http.StatusOK, status, "%v", body)
		return body
	}
	member := caps(pf.A)
	assert.EqualValues(t, share.DefaultMaxTTLDays, member["share_max_ttl_days"], "the install's own setting is unchanged")
	assert.EqualValues(t, 3, member["share_link_max_days"], "the rule binding the member is the shorter ceiling")

	admin := caps(pf.AdminA)
	assert.EqualValues(t, share.DefaultMaxTTLDays, admin["share_link_max_days"], "no rule binds the administrator")

	anon := caps(&http.Client{})
	assert.EqualValues(t, share.DefaultMaxTTLDays, anon["share_link_max_days"], "nobody signed in reads the install's ceiling")
}

// expires_in is counted on the server's clock: the dialog sends a length.
func TestShareCreate_ExpiresInIsCountedOnTheServersClock(t *testing.T) {
	pf := newPermFix(t)
	status, body := fxPost(t, pf.URL+"/api/files/share", pf.adminTok, map[string]any{"path": "alpha://report.txt", "expires_in": 2 * 86400})
	require.Less(t, status, 300, body)
	sh := decode(t, body)["share"].(map[string]any)
	assert.Nil(t, sh["expiry_clamped"], "two days is inside every ceiling: not clamped")
	assert.NotEmpty(t, sh["expires_at"])
}

// A10 over the agent doors: an agent that makes a link gets the command the
// server wrote, instead of working out zip=wait, ?pin= and the redirect itself.
func TestAIShare_CarriesTheDownloadCommand(t *testing.T) {
	f := newDoorFix(t)
	f.put(t, f.Tok, "main://raporlar/ozet.txt", "x")

	code, out := f.rest(t, f.Tok, http.MethodPost, "/api/ai/share", map[string]any{"path": "main://raporlar/ozet.txt", "pin": true})
	require.Equal(t, http.StatusOK, code, "%v", out)
	cmd, ok := out["download_command"].(map[string]any)
	require.True(t, ok, "the REST answer carries download_command: %v", out)
	pin, _ := out["pin"].(string)
	require.NotEmpty(t, pin)
	assert.Equal(t, "curl -fSL -o 'ozet.txt' '"+out["url"].(string)+"?pin="+pin+"'", cmd["curl"])
	assert.Contains(t, cmd["powershell"], "-OutFile 'ozet.txt'")

	tl := f.tool(t, f.Tok, "file_share", map[string]any{"path": "main://raporlar/ozet.txt"})
	require.False(t, tl.IsError, tl.Text)
	assert.Contains(t, string(tl.Structured), `"download_command"`, "the MCP result carries it: %s", tl.Raw)
	assert.Contains(t, string(tl.Structured), `curl -fSL -o 'ozet.txt'`, "%s", tl.Raw)
}

// B13: how long a browser keeps its resume bookmark is the server's answer.
// The sweeper takes a staging FILEX_UPLOAD_STAGING_TTL after its LAST write,
// so begin, every chunk and the status answer all say when that is - the
// bookmark used to be kept a fixed 24 hours, wrong both ways the day an
// operator changed the TTL.
func TestStagedUpload_EveryAnswerSaysWhenTheStagingIsSwept(t *testing.T) {
	f := newStagedFixture(t)
	const chunk = 4096
	src := randomBytes(6000)
	total := int64(len(src))

	code, begun := f.begin(t, map[string]any{"path": "main://", "name": "ttl.bin", "size": total, "chunk_size": chunk})
	require.Equal(t, http.StatusOK, code, "%v", begun)
	id, _ := begun["id"].(string)
	parse := func(v any) time.Time {
		t.Helper()
		s, _ := v.(string)
		at, err := time.Parse(time.RFC3339Nano, s)
		require.NoError(t, err, "expires_at %v", v)
		return at
	}
	first := parse(begun["expires_at"])
	assert.True(t, first.After(time.Now()), "a fresh staging is swept in the future")

	code, put := f.putChunk(t, id, 0, chunk, total, src[:chunk])
	require.Equal(t, http.StatusOK, code, "%v", put)
	afterChunk := parse(put["expires_at"])
	assert.False(t, afterChunk.Before(first), "a chunk moves the sweep time forward, never back")

	code, stat := f.status(t, id)
	require.Equal(t, http.StatusOK, code, "%v", stat)
	assert.WithinDuration(t, afterChunk, parse(stat["expires_at"]), 5*time.Second, "status says the same moment")
}
