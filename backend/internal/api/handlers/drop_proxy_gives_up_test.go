package handlers_test

// A drop that has fully arrived is written to the end, even when the client
// stops waiting.
//
// The drop endpoint writes the files to the storage one after another after
// the whole request has arrived. For a large drop into an object store that
// outlasts a proxy's wait (Cloudflare gives up at 100 s, nginx at 60 s), and
// the proxy's hang-up cancelled the request: the write loop stopped between two
// files, half the drop landed, the visitor was told none of it had, and a retry
// made a second submission. Here the "proxy" gives up the moment the last byte
// is in — the request's context is cancelled as the last byte of its body is read.

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
)

// hangUpAtLastByte cancels the request the moment the last byte of its body
// has been read. (The multipart reader stops at the closing boundary and
// never asks for the EOF, so the bytes are counted.)
type hangUpAtLastByte struct {
	r      io.Reader
	left   int
	hangUp context.CancelFunc
}

func (h *hangUpAtLastByte) Read(p []byte) (int, error) {
	n, err := h.r.Read(p)
	h.left -= n
	if h.left <= 0 || err == io.EOF {
		h.hangUp()
	}
	return n, err
}

func TestDrop_AFullyArrivedDropIsWrittenWhenTheProxyGivesUp(t *testing.T) {
	r, svc, store, st, root := newDropFixture(t)
	folder := mkdirNode(t, store, st, root, "inbox")
	sh, err := svc.Create(context.Background(), share.CreateOpts{NodeID: folder.ID, Kind: model.ShareKindDrop})
	require.NoError(t, err)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	names := []string{"a.txt", "b.txt", "c.txt"}
	for _, n := range names {
		part, err := mw.CreateFormFile("file[]", n)
		require.NoError(t, err)
		_, _ = io.WriteString(part, "contents of "+n)
	}
	require.NoError(t, mw.Close())

	ctx, hangUp := context.WithCancel(context.Background())
	defer hangUp()
	req := httptest.NewRequest(http.MethodPost, "/d/"+sh.Token, &hangUpAtLastByte{r: &body, left: body.Len(), hangUp: hangUp}).WithContext(ctx)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Error(t, ctx.Err(), "the fixture must hang up before the writes")
	for _, n := range names {
		assert.NotEmpty(t, findUnder(root, "inbox", n), "%s was not written after the proxy gave up", n)
	}
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}
