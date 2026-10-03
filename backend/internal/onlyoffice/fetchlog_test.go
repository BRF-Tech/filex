package onlyoffice

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// TestDiagnose_ThreeVerdicts: what the editor's "Download failed" is told,
// from what the fetch endpoint saw (issue #80).
func TestDiagnose_ThreeVerdicts(t *testing.T) {
	svc := &Service{}

	d := svc.Diagnose(7)
	require.Equal(t, VerdictNotRequested, d.Verdict, "a document nobody opened was never asked for")
	require.Equal(t, ScopeThisProcess, d.Scope)
	require.Nil(t, d.Fetch)

	svc.noteOpened(7)
	d = svc.Diagnose(7)
	require.Equal(t, VerdictNotRequested, d.Verdict)
	require.NotNil(t, d.OpenedAt)

	svc.NoteFetch(7, http.StatusOK, "", "", true)
	d = svc.Diagnose(7)
	require.Equal(t, VerdictServed, d.Verdict, "the document server got it: the failure is the browser's leg")
	require.Equal(t, http.StatusOK, d.Fetch.Status)

	svc.NoteFetch(7, http.StatusUnauthorized, FetchSignatureBad, "signature refused", false)
	d = svc.Diagnose(7)
	require.Equal(t, VerdictRefused, d.Verdict)
	require.Equal(t, FetchSignatureBad, d.Fetch.Code)

	// Opened again and not fetched since: the earlier answer belongs to the
	// earlier opening.
	time.Sleep(2 * time.Millisecond)
	svc.noteOpened(7)
	d = svc.Diagnose(7)
	require.Equal(t, VerdictNotRequested, d.Verdict)
	require.NotNil(t, d.Fetch, "the earlier answer is still reported, for the record")
}

// TestFetchLog_OnlySignedRequestsAddDocuments: the fetch endpoint is public, so
// a request that is not the document server's must not create entries.
func TestFetchLog_OnlySignedRequestsAddDocuments(t *testing.T) {
	svc := &Service{}
	svc.NoteFetch(99, http.StatusUnauthorized, FetchSignatureBad, "signature refused", false)
	require.Nil(t, svc.Diagnose(99).Fetch, "an unsigned refusal for a document filex never opened leaves nothing")

	svc.NoteFetch(99, http.StatusNotFound, FetchObjectMissing, "the object is not on the storage", true)
	d := svc.Diagnose(99)
	require.Equal(t, VerdictRefused, d.Verdict)
	require.Equal(t, FetchObjectMissing, d.Fetch.Code)
}

// TestFetchLog_IsBounded: one process's memory, of a fixed size.
func TestFetchLog_IsBounded(t *testing.T) {
	svc := &Service{}
	for id := int64(1); id <= fetchLogSize+20; id++ {
		svc.NoteFetch(id, http.StatusOK, "", "", true)
	}
	l := svc.fetches()
	l.mu.Lock()
	n := len(l.entries)
	l.mu.Unlock()
	require.LessOrEqual(t, n, fetchLogSize)
	require.Equal(t, VerdictServed, svc.Diagnose(fetchLogSize+20).Verdict, "the newest is kept")
}

// TestBuildConfigForNode_RecordsTheOpening: the editor configuration is the
// opening a later fetch is compared with.
func TestBuildConfigForNode_RecordsTheOpening(t *testing.T) {
	svc := &Service{DocumentServerURL: "https://docs.example", JWTSecret: "s3cret", PublicURL: "https://files.example"}
	node := &model.Node{ID: 12, Name: "a.docx", PathHash: "h"}
	_, err := svc.BuildConfigForNode(context.Background(), node, nil, "en", "view")
	require.NoError(t, err)
	require.NotNil(t, svc.Diagnose(12).OpenedAt)
}
