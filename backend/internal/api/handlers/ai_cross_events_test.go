package handlers_test

// What the rest of the system is told when the agent surface moves a file
// BETWEEN two storages.
//
// A cross-storage move is two facts, not one: bytes arrived over there, and
// the original is gone over here. `moveAcross` cannot use `file.moved` for it
// — that event carries ONE storage id, and these two ends live in different
// depots — so it emits the destination's ordinary write event (the same
// `file.uploaded` / `file.updated` every other write path emits, through the
// same writehook gate) plus a `file.deleted` for the source.
//
// ⚠⚠ Why this file exists. The destination half is emitted INDIRECTLY: the
// transfer's per-file hook calls `cacheUpsertFile`, which goes through
// `protocolsync.Syncer.Write`, which is what calls `writehook.OnFileWritten`.
// Nothing in `moveAcross` says "emit" — its only visible writehook line is the
// source's delete, under a comment that merely claims the far side is covered
// ("written on the far side, gone on this one"). Reading it, the far-side
// event looks missing; it is not. A refactor that swapped the hook for a
// direct row write — or dropped `OnFile` because "the cache is already
// upserted" — would make the claim false and nothing else would notice: the
// bytes still land, the listing is still right, and only a webhook subscriber
// waiting for the file that was promised sits there forever.

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// collectEvents drains the sink until `want` events have arrived (writehook
// emits on a goroutine, so this is a bounded wait, not a sleep) and fails with
// what it did get.
func collectEvents(t *testing.T, sink *whFakeSink, want int) []notify.Event {
	t.Helper()
	var got []notify.Event
	deadline := time.After(5 * time.Second)
	for len(got) < want {
		select {
		case e := <-sink.ch:
			got = append(got, e)
		case <-deadline:
			names := make([]string, 0, len(got))
			for _, e := range got {
				names = append(names, string(e.Event))
			}
			t.Fatalf("wanted %d events, got %d: %v", want, len(got), names)
		}
	}
	// Nothing else may be on its way: an extra event is as wrong as a missing
	// one (a second write, a stray delete on the far side).
	select {
	case e := <-sink.ch:
		t.Fatalf("one event too many: %s about %v", e.Event, e.Node)
	case <-time.After(250 * time.Millisecond):
	}
	return got
}

// onlyEvent picks the single event of a kind, failing when there is not
// exactly one.
func onlyEvent(t *testing.T, events []notify.Event, kind notify.EventType) notify.Event {
	t.Helper()
	var found []notify.Event
	for _, e := range events {
		if e.Event == kind {
			found = append(found, e)
		}
	}
	require.Len(t, found, 1, "expected exactly one %s", kind)
	return found[0]
}

func installWriteSink(t *testing.T) *whFakeSink {
	t.Helper()
	sink := &whFakeSink{ch: make(chan notify.Event, 32)}
	writehook.Configure(nil, sink)
	t.Cleanup(func() { writehook.Configure(nil, nil) })
	return sink
}

// TestAI_MoveAcrossStorages_TellsBothDepots — one file, carried from `hot` to
// `cold`. The far side must hear about the bytes that landed, on ITS OWN
// storage, with a target that opens the file where it now is; the near side
// keeps its permanent delete, whose target is the folder the file left
// (there is no row to select any more).
func TestAI_MoveAcrossStorages_TellsBothDepots(t *testing.T) {
	srv, client, tok, _, _ := aiTwoStorageFixture(t, false)
	sink := installWriteSink(t)

	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/upload", tok, map[string]any{
		"path": "hot://rapor.txt", "content": "veri",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	// The upload's own event names the SOURCE depot — that is how this test
	// knows which storage id is which without reaching into the fixture.
	seed := collectEvents(t, sink, 1)[0]
	require.Equal(t, notify.EventFileUploaded, seed.Event)
	require.NotNil(t, seed.Node)
	hotID := seed.Node.StorageID
	require.NotZero(t, hotID)

	resp = aiReq(t, client, "POST", srv.URL+"/api/ai/move", tok, map[string]any{
		"src": "hot://rapor.txt", "dst": "cold://arsiv/rapor.txt",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode, readBody(t, resp))

	events := collectEvents(t, sink, 2)

	landed := onlyEvent(t, events, notify.EventFileUploaded)
	require.NotNil(t, landed.Node, "an event about a file names the file")
	assert.NotEqual(t, hotID, landed.Node.StorageID,
		"the bytes are in the OTHER depot; an event on the source storage would tell the wrong depot about a file it never received")
	assert.Equal(t, "/arsiv/rapor.txt", landed.Node.Path)
	assert.Equal(t, "rapor.txt", landed.Node.Name)
	assert.Equal(t, int64(4), landed.Node.Size)
	assert.Equal(t, writehook.OriginAI, landed.Meta["origin"],
		"the same origin stamp every other AI-surface write carries")
	// ⚠ The target's storage NAME is filled in centrally by notify.Service.Send
	// (internal/notify/target_test.go); at the emitter it is the kind and the
	// storage-relative path that have to be right.
	require.NotNil(t, landed.Target, "a write nobody can click through to is half an event")
	assert.Equal(t, notify.TargetFile, landed.Target.Kind)
	assert.Equal(t, "arsiv/rapor.txt", landed.Target.Path)

	gone := onlyEvent(t, events, notify.EventFileDeleted)
	require.NotNil(t, gone.Node)
	assert.Equal(t, hotID, gone.Node.StorageID, "the source is the depot the file left")
	assert.Equal(t, "/rapor.txt", gone.Node.Path)
	require.NotNil(t, gone.Target)
	assert.Equal(t, notify.TargetDir, gone.Target.Kind,
		"a permanent removal leaves no row to select, so the click goes to the folder")
	assert.Equal(t, "", gone.Target.Path, "the file sat at the storage root")
}

// TestAI_MoveAcrossStorages_TellsTheDestinationAboutEveryFile — a whole tree.
// The transfer rebuilds it file by file on the far side, so the far side hears
// about every one of them; the source is one delete, for the folder.
func TestAI_MoveAcrossStorages_TellsTheDestinationAboutEveryFile(t *testing.T) {
	srv, client, tok, _, _ := aiTwoStorageFixture(t, false)
	sink := installWriteSink(t)

	for _, f := range []struct{ path, body string }{
		{"hot://proje/README.md", "# proje"},
		{"hot://proje/src/main.go", "package main"},
	} {
		resp := aiReq(t, client, "POST", srv.URL+"/api/ai/upload", tok, map[string]any{
			"path": f.path, "content": f.body,
		})
		require.Equal(t, http.StatusOK, resp.StatusCode, readBody(t, resp))
	}
	seeds := collectEvents(t, sink, 2)
	hotID := seeds[0].Node.StorageID

	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/move", tok, map[string]any{
		"src": "hot://proje", "dst": "cold://arsiv/proje",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode, readBody(t, resp))

	events := collectEvents(t, sink, 3)

	landed := map[string]notify.Event{}
	for _, e := range events {
		if e.Event == notify.EventFileUploaded {
			require.NotNil(t, e.Node)
			landed[e.Node.Path] = e
		}
	}
	require.Len(t, landed, 2, "every file that arrived is a file somebody was promised an event about")
	for _, p := range []string{"/arsiv/proje/README.md", "/arsiv/proje/src/main.go"} {
		e, ok := landed[p]
		require.True(t, ok, "no write event for %s", p)
		assert.NotEqual(t, hotID, e.Node.StorageID, "%s landed in the OTHER depot", p)
		require.NotNil(t, e.Target)
		assert.Equal(t, notify.TargetFile, e.Target.Kind)
		assert.Equal(t, p[1:], e.Target.Path)
	}

	gone := onlyEvent(t, events, notify.EventFileDeleted)
	require.NotNil(t, gone.Node)
	assert.Equal(t, hotID, gone.Node.StorageID)
	assert.Equal(t, "/proje", gone.Node.Path, "one delete, for the tree that left")
}

// TestAI_MoveAcrossStorages_TellsTheFarSideTheFinalPath — the same one-file
// move, into a name the destination already holds. `moveAcross` de-collides
// before `ops.Transfer` runs, so the write event the far side hears is about
// `rapor-copy.txt`: the path the bytes are at.
//
// ⚠⚠ This is the half of the fix that rots silently. De-collide the path but
// leave the event on the requested one and everything still LOOKS right — the
// bytes land beside the resident file, the listing shows both — while every
// `file.uploaded` subscriber, and the AV scan and the search index that ride
// the same hook, are handed the path of the file nobody wrote. The resident
// file would be rescanned and reindexed as if it were new, and the one that
// actually arrived would be known to nothing.
func TestAI_MoveAcrossStorages_TellsTheFarSideTheFinalPath(t *testing.T) {
	srv, client, tok, _, _ := aiTwoStorageFixture(t, false)
	sink := installWriteSink(t)

	// Seed the collision first, so its own upload event names the COLD depot.
	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/upload", tok, map[string]any{
		"path": "cold://arsiv/rapor.txt", "content": "eski",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode, readBody(t, resp))
	resident := collectEvents(t, sink, 1)[0]
	require.NotNil(t, resident.Node)
	coldID := resident.Node.StorageID
	require.NotZero(t, coldID)

	resp = aiReq(t, client, "POST", srv.URL+"/api/ai/upload", tok, map[string]any{
		"path": "hot://rapor.txt", "content": "yeni",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode, readBody(t, resp))
	seed := collectEvents(t, sink, 1)[0]
	require.NotNil(t, seed.Node)
	hotID := seed.Node.StorageID
	require.NotEqual(t, hotID, coldID, "the fixture's two depots must be distinct or this test proves nothing")

	resp = aiReq(t, client, "POST", srv.URL+"/api/ai/move", tok, map[string]any{
		"src": "hot://rapor.txt", "dst": "cold://arsiv/rapor.txt",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode, readBody(t, resp))

	events := collectEvents(t, sink, 2)

	landed := onlyEvent(t, events, notify.EventFileUploaded)
	require.NotNil(t, landed.Node)
	assert.Equal(t, coldID, landed.Node.StorageID)
	assert.Equal(t, "/arsiv/rapor-copy.txt", landed.Node.Path,
		"the event names the path the bytes landed on, never the one that was asked for")
	assert.Equal(t, "rapor-copy.txt", landed.Node.Name)
	assert.Equal(t, int64(4), landed.Node.Size)
	require.NotNil(t, landed.Target, "a write nobody can click through to is half an event")
	assert.Equal(t, notify.TargetFile, landed.Target.Kind)
	assert.Equal(t, "arsiv/rapor-copy.txt", landed.Target.Path)

	gone := onlyEvent(t, events, notify.EventFileDeleted)
	require.NotNil(t, gone.Node)
	assert.Equal(t, hotID, gone.Node.StorageID, "the source depot is still the one that lost the file")
	assert.Equal(t, "/rapor.txt", gone.Node.Path)
}
