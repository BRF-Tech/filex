package handlers_test

// Upload tickets: the credential-free path for a file that is already on the
// agent's disk. Every assertion here is about the property that made the
// feature necessary — the transfer must work with NO token, must land exactly
// where the authorized half said, and must not become a general write key.

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// mintTicket asks for an upload URL for dest and returns the decoded reply.
func mintTicket(t *testing.T, client *http.Client, srvURL, tok string, body map[string]any) (map[string]any, int) {
	t.Helper()
	resp := aiReq(t, client, "POST", srvURL+"/api/ai/upload/ticket", tok, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode
	}
	var out map[string]any
	testutil.ReadJSON(t, resp, &out)
	return out, resp.StatusCode
}

// putNoAuth streams body to url with NO credentials of any kind — the exact
// shape `curl -T file url` puts on the wire.
func putNoAuth(t *testing.T, client *http.Client, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(body))
	require.NoError(t, err)
	req.ContentLength = int64(len(body))
	resp, err := client.Do(req)
	require.NoError(t, err)
	return resp
}

func TestUploadTicket_RedeemNeedsNoToken(t *testing.T) {
	srv, client, _, tok := aiFixture(t)

	info, code := mintTicket(t, client, srv.URL, tok, map[string]any{
		"path": "main://reports/quarterly.xlsx",
	})
	require.Equal(t, http.StatusOK, code)
	url, _ := info["url"].(string)
	require.NotEmpty(t, url)
	assert.Contains(t, info["curl"], "curl -T")
	assert.Equal(t, "main://reports/quarterly.xlsx", info["path"])

	// The ticket URL is published with the configured public base, which is
	// not the test server's address — redeem against the same path on srv.
	redeem := srv.URL + url[strings.Index(url, "/u/"):]

	payload := strings.Repeat("spreadsheet-bytes;", 4096)
	resp := putNoAuth(t, client, redeem, payload)
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"the redeem URL must work with no token at all — that is the whole feature")
	var out map[string]any
	testutil.ReadJSON(t, resp, &out)
	resp.Body.Close()
	entry, _ := out["entry"].(map[string]any)
	require.NotNil(t, entry)
	assert.Equal(t, "quarterly.xlsx", entry["name"])
	assert.Equal(t, float64(len(payload)), entry["size"])

	// The bytes really landed at the minted destination, unchanged.
	got := aiReq(t, client, "GET", srv.URL+"/api/ai/download?path=main://reports/quarterly.xlsx", tok, nil)
	require.Equal(t, http.StatusOK, got.StatusCode)
	b, _ := io.ReadAll(got.Body)
	got.Body.Close()
	assert.Equal(t, payload, string(b))
}

func TestUploadTicket_MultipartRedeem(t *testing.T) {
	srv, client, _, tok := aiFixture(t)

	info, code := mintTicket(t, client, srv.URL, tok, map[string]any{"path": "main://in/photo.bin"})
	require.Equal(t, http.StatusOK, code)
	redeem := srv.URL + info["url"].(string)[strings.Index(info["url"].(string), "/u/"):]

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "whatever-the-client-calls-it.bin")
	require.NoError(t, err)
	_, err = part.Write([]byte("binary-payload"))
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	req, err := http.NewRequest(http.MethodPost, redeem, &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// The filename in the multipart part is IGNORED: the destination is the
	// one pinned at mint time, so a redeemer cannot redirect the write.
	got := aiReq(t, client, "GET", srv.URL+"/api/ai/download?path=main://in/photo.bin", tok, nil)
	require.Equal(t, http.StatusOK, got.StatusCode)
	b, _ := io.ReadAll(got.Body)
	got.Body.Close()
	assert.Equal(t, "binary-payload", string(b))
}

func TestUploadTicket_SingleUse(t *testing.T) {
	srv, client, _, tok := aiFixture(t)

	info, _ := mintTicket(t, client, srv.URL, tok, map[string]any{"path": "main://once/a.txt"})
	redeem := srv.URL + info["url"].(string)[strings.Index(info["url"].(string), "/u/"):]

	resp := putNoAuth(t, client, redeem, "first")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp = putNoAuth(t, client, redeem, "second")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode,
		"a redeemed ticket must be gone, and indistinguishable from one that never existed")

	// The second attempt must not have touched the file.
	got := aiReq(t, client, "GET", srv.URL+"/api/ai/download?path=main://once/a.txt", tok, nil)
	b, _ := io.ReadAll(got.Body)
	got.Body.Close()
	assert.Equal(t, "first", string(b))
}

func TestUploadTicket_UnknownAndExpired(t *testing.T) {
	srv, client, _, tok := aiFixture(t)

	resp := putNoAuth(t, client, srv.URL+"/u/definitely-not-a-ticket", "x")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	// A one-second ticket, redeemed after it lapses.
	info, code := mintTicket(t, client, srv.URL, tok, map[string]any{
		"path": "main://late/late.txt", "expires_in_seconds": 1,
	})
	require.Equal(t, http.StatusOK, code)
	redeem := srv.URL + info["url"].(string)[strings.Index(info["url"].(string), "/u/"):]
	time.Sleep(1100 * time.Millisecond)

	resp = putNoAuth(t, client, redeem, "too late")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusGone, resp.StatusCode,
		"an expired ticket is 410 — a distinct, non-retryable answer")
}

func TestUploadTicket_RespectsCeilingAndLength(t *testing.T) {
	srv, client, _, tok := aiFixture(t)

	info, code := mintTicket(t, client, srv.URL, tok, map[string]any{
		"path": "main://big/capped.bin", "max_bytes": 16,
	})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, float64(16), info["max_bytes"])
	redeem := srv.URL + info["url"].(string)[strings.Index(info["url"].(string), "/u/"):]

	resp := putNoAuth(t, client, redeem, strings.Repeat("x", 64))
	require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
	resp.Body.Close()

	// Over-size was refused, but the ticket survives so a corrected upload
	// still works without a second mint.
	resp = putNoAuth(t, client, redeem, "small")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

func TestUploadTicket_ChunkedBodyRefused(t *testing.T) {
	srv, client, _, tok := aiFixture(t)

	info, _ := mintTicket(t, client, srv.URL, tok, map[string]any{"path": "main://len/x.txt"})
	redeem := srv.URL + info["url"].(string)[strings.Index(info["url"].(string), "/u/"):]

	// No ContentLength set → Go sends it chunked. The write needs an exact
	// size, so this must be a clear 411, not a 500 half way through.
	req, err := http.NewRequest(http.MethodPut, redeem, io.NopCloser(strings.NewReader("body")))
	require.NoError(t, err)
	req.ContentLength = -1
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusLengthRequired, resp.StatusCode)
}

func TestUploadTicket_CannotMintOutsideConfinedRoot(t *testing.T) {
	srv, client, store, adminTok := aiFixture(t)

	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/upload", adminTok, map[string]any{
		"path": "main://tenant/in.txt", "content": "x",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	u, err := store.CreateUser(context.Background(), "ticket-confined@test.local", "x", model.RoleAdmin, "en", "UTC")
	require.NoError(t, err)
	conf := issueToken(t, store, u.ID, "read,write,root:main://tenant", nil)

	// Control: inside the root the confined token mints fine.
	_, code := mintTicket(t, client, srv.URL, conf, map[string]any{"path": "ok.bin"})
	require.Equal(t, http.StatusOK, code)

	// Outside it, minting is refused — otherwise a ticket would be a way to
	// launder a confined token into a write anywhere (lesson #3).
	_, code = mintTicket(t, client, srv.URL, conf, map[string]any{"path": "main://outside/evil.bin"})
	assert.Equal(t, http.StatusForbidden, code,
		"a confined token must not mint a ticket for a path it could not write itself")
}

func TestUploadTicket_FolderPathRefusedAtMint(t *testing.T) {
	srv, client, _, tok := aiFixture(t)

	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/mkdir", tok, map[string]any{"path": "main://adir"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Catching this at mint time is the point: the agent learns before it
	// spends minutes transferring, not after.
	_, code := mintTicket(t, client, srv.URL, tok, map[string]any{"path": "main://adir"})
	assert.NotEqual(t, http.StatusOK, code,
		"a folder as the destination must be refused when the ticket is minted")
}
