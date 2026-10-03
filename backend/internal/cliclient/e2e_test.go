package cliclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// What `filex encrypt` asks of the server (e2e.go). The conversion flag has to
// reach the server where the server reads it - the multipart form and the
// staged COMMIT - or the server keeps the plaintext it replaces as a version.

func TestConversionWrite_RidesOnTheMultipartForm(t *testing.T) {
	var mu sync.Mutex
	got := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(1<<20))
		mu.Lock()
		got[r.FormValue("path")] = r.FormValue("e2e_convert") + "|" + r.FormValue("expect")
		mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	local := filepath.Join(t.TempDir(), "x.bin")
	require.NoError(t, os.WriteFile(local, []byte("filexe2e..."), 0o644))
	c := &Client{BaseURL: srv.URL, Token: "t", HTTP: srv.Client()}

	conv, _ := ParseRemotePath("docs://Kasa")
	plain, _ := ParseRemotePath("docs://Other")
	_, err := c.UploadTo(ConversionWrite(context.Background()), local, conv, "x.bin", "11:1700")
	require.NoError(t, err)
	_, err = c.UploadTo(context.Background(), local, plain, "x.bin", "11:1700")
	require.NoError(t, err)
	require.Equal(t, "1|11:1700", got["docs://Kasa"])
	require.Equal(t, "|11:1700", got["docs://Other"], "an ordinary upload is never a conversion write")
}

func TestConversionWrite_RidesOnTheStagedCommit(t *testing.T) {
	f := newStagedFake(t)
	local, _ := stagedTestFile(t, stagedTestChunk*2+5)
	c := f.client(t.TempDir())
	dir, _ := ParseRemotePath("docs://Kasa")
	_, err := c.UploadTo(ConversionWrite(context.Background()), local, dir, "big.bin", "8197:1700")
	require.NoError(t, err)
	_, err = c.UploadTo(context.Background(), local, dir, "big2.bin", "")
	require.NoError(t, err)

	f.mu.Lock()
	defer f.mu.Unlock()
	require.Len(t, f.commitQueries, 2)
	require.Equal(t, "1", f.commitQueries[0].Get("e2e_convert"))
	require.Equal(t, "8197:1700", f.commitQueries[0].Get("expect"))
	require.Empty(t, f.commitQueries[1].Get("e2e_convert"))
	require.False(t, f.commitQueries[1].Has("expect"))
}

func TestE2EEscrowKey_ReadsWhatTheBrowserReads(t *testing.T) {
	answer := `{"e2e_escrow":{"enabled":true,"kid":"0123456789abcdef","alg":"RSA-OAEP-256","public_key":"MIIB"}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/capabilities", r.URL.Path)
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(srv.Close)
	c := &Client{BaseURL: srv.URL, Token: "t", HTTP: srv.Client()}
	k, err := c.E2EEscrowKey(context.Background())
	require.NoError(t, err)
	require.Equal(t, "MIIB", k)

	answer = `{"e2e_escrow":{"enabled":false}}`
	k, err = c.E2EEscrowKey(context.Background())
	require.NoError(t, err)
	require.Empty(t, k)

	answer = `{"e2e_escrow":{"enabled":true}}`
	_, err = c.E2EEscrowKey(context.Background())
	require.Error(t, err, "escrow on with no key is not taken as escrow off")
}

func TestE2ECleanup_SendsTheFolderAndTheChoice(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/files/e2e/cleanup", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		_, _ = w.Write([]byte(`{"versions_deleted":3,"trash_purged":1,"thumbnails_dropped":4,"index_cleared":4}`))
	}))
	t.Cleanup(srv.Close)
	c := &Client{BaseURL: srv.URL, Token: "t", HTTP: srv.Client()}
	out, err := c.E2ECleanup(context.Background(), "docs://Kasa", true, false)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"path": "docs://Kasa", "versions": true, "trash": false}, body)
	require.Equal(t, &E2ECleanupResult{VersionsDeleted: 3, TrashPurged: 1, ThumbnailsDropped: 4, IndexCleared: 4}, out)
}

func TestList_SaysWhichFolderIsEncrypted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"adapter":"docs","e2e":true,"e2e_root":"docs://Kasa","files":[
			{"path":"docs://Kasa/Alt","basename":"Alt","type":"dir","e2e":true},
			{"path":"docs://Kasa/a.txt","basename":"a.txt","type":"file","size":3}]}`))
	}))
	t.Cleanup(srv.Close)
	c := &Client{BaseURL: srv.URL, Token: "t", HTTP: srv.Client()}
	l, err := c.List(context.Background(), "docs://Kasa")
	require.NoError(t, err)
	require.Equal(t, "docs://Kasa", l.E2ERoot)
	require.True(t, l.Files[0].E2E)
	require.False(t, l.Files[1].E2E)
}

func TestReadSmall_RefusesMoreThanItWasPromised(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, 2048))
	}))
	t.Cleanup(srv.Close)
	c := &Client{BaseURL: srv.URL, Token: "t", HTTP: srv.Client()}
	_, err := c.ReadSmall(context.Background(), "docs://Kasa/.filex-e2e.json", 1024)
	require.Error(t, err)
	b, err := c.ReadSmall(context.Background(), "docs://Kasa/.filex-e2e.json", 4096)
	require.NoError(t, err)
	require.Len(t, b, 2048)
}
