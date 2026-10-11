package coedit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/secretbox"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// clock is a settable time source.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type rig struct {
	store db.Store
	clock *clock
	dir   string
	box   *secretbox.Box
	left  []Departure
	mu    sync.Mutex
}

func newRig(t *testing.T) *rig {
	t.Helper()
	_, store := dbtest.NewTestDB(t)
	box, err := secretbox.New("coedit-test-secret-key")
	if err != nil {
		t.Fatal(err)
	}
	return &rig{store: store, clock: &clock{t: time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)}, dir: t.TempDir(), box: box}
}

// relay is one filex process on the rig's database (two relays = two
// processes on one database).
func (g *rig) relay(mod ...func(*Options)) *Relay {
	o := Options{
		Store: g.store, BlobDir: g.dir, Keys: g.box, Now: g.clock.now,
		OnDeparture: func(_ context.Context, d Departure) {
			g.mu.Lock()
			g.left = append(g.left, d)
			g.mu.Unlock()
		},
	}
	for _, m := range mod {
		m(&o)
	}
	return New(o)
}

var ctx = context.Background()

func docReq(doc string) OpenRequest {
	return OpenRequest{Doc: DocKey(3, doc), StorageID: 3, Path: doc, App: "office-editor", Build: "0.3.0+3f9a", FileSig: "100:1", KeyMode: KeyServer}
}

func person(user int64, canEdit bool) Identity {
	return Identity{User: user, Name: fmt.Sprintf("user %d", user), CanEdit: canEdit}
}

func open(t *testing.T, r *Relay, doc string, who Identity) Hello {
	t.Helper()
	h, err := r.Open(ctx, docReq(doc), who)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return h
}

// started opens a session for doc as its creator and stores the base, the way
// the creator's page does before anybody else may join.
func started(t *testing.T, r *Relay, doc string, who Identity) Hello {
	t.Helper()
	h := open(t, r, doc, who)
	if !h.Created {
		t.Fatalf("expected to start the session: %+v", h)
	}
	if _, err := r.PutBlob(ctx, h.SID, h.Me.Client, BaseBlob, bytes.NewReader([]byte("sealed-base"))); err != nil {
		t.Fatal(err)
	}
	return h
}

func head(t *testing.T, r *Relay, sid string) uint64 {
	t.Helper()
	s, err := r.Session(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	return uint64(s.Head)
}

func lease(t *testing.T, r *Relay, sid, client string, seen uint64) {
	t.Helper()
	ok, err := r.Lease(ctx, sid, client, LeaseAcquire, seen)
	if err != nil || !ok {
		t.Fatalf("lease: %v %v", ok, err)
	}
}

func TestOpen_TheFirstOpenerStartsTheSessionAndEverybodyElseWaitsForTheBase(t *testing.T) {
	g := newRig(t)
	r := g.relay()
	a := open(t, r, "Rapor.docx", person(7, true))
	if !a.Created || a.Me.Client != "c1" || a.Me.IndexUser != 1 || a.Head != 1 || len(a.Key) != KeyBytes {
		t.Fatalf("creator's hello: %+v", a)
	}
	if _, err := r.Open(ctx, docReq("Rapor.docx"), person(8, true)); !errors.Is(err, ErrNotReady) {
		t.Fatalf("joining before the base: %v, want ErrNotReady", err)
	}
	if _, err := r.PutBlob(ctx, a.SID, a.Me.Client, BaseBlob, bytes.NewReader([]byte("sealed-base"))); err != nil {
		t.Fatal(err)
	}
	b := open(t, r, "Rapor.docx", person(8, true))
	if b.Created || b.SID != a.SID || b.Me.IndexUser != 2 || !bytes.Equal(b.Key, a.Key) {
		t.Fatalf("second member: %+v", b)
	}
	if b.Me.User == a.Me.User || b.Me.User == "8" || len(b.Me.User) != 16 {
		t.Fatalf("a member's user is an opaque id of the person, never the account: %q / %q", a.Me.User, b.Me.User)
	}
	again := open(t, r, "Rapor.docx", person(7, true))
	if again.Me.User != a.Me.User || again.Me.Client == a.Me.Client {
		t.Fatalf("the same person in another tab: the same user, another client: %+v vs %+v", again.Me, a.Me)
	}
}

func TestOpen_TwoProcessesOnOneDatabaseServeOneSession(t *testing.T) {
	g := newRig(t)
	p1, p2 := g.relay(), g.relay()
	a := started(t, p1, "Rapor.docx", person(7, true))
	b := open(t, p2, "Rapor.docx", person(8, true))
	if b.SID != a.SID || b.Me.IndexUser != 2 {
		t.Fatalf("the other process joined %+v, want session %s", b, a.SID)
	}
	lease(t, p2, b.SID, b.Me.Client, 0)
	seq, err := p2.Append(ctx, b.SID, b.Me.Client, AppendRequest{Kind: KindChanges, Base: head(t, p2, b.SID), Ctr: 1, CT: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	es, err := p1.Entries(ctx, a.SID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) == 0 || es[len(es)-1].Seq != seq || es[len(es)-1].Client != b.Me.Client {
		t.Fatalf("process 1 reads what process 2 appended: %+v", es)
	}
}

func TestAppend_LandsRightAfterTheHeadItWasWrittenAgainstOrNotAtAll(t *testing.T) {
	g := newRig(t)
	r := g.relay()
	a := started(t, r, "d.docx", person(7, true))
	b := open(t, r, "d.docx", person(8, true))
	h := head(t, r, a.SID)
	seq, err := r.Append(ctx, a.SID, a.Me.Client, AppendRequest{Kind: KindLock, Base: h, Ctr: 1, CT: []byte("lock-a")})
	if err != nil || seq != h+1 {
		t.Fatalf("first: %d %v", seq, err)
	}
	_, err = r.Append(ctx, a.SID, b.Me.Client, AppendRequest{Kind: KindLock, Base: h, Ctr: 1, CT: []byte("lock-b")})
	var conflict *ConflictError
	if !errors.As(err, &conflict) || conflict.Head != h+1 {
		t.Fatalf("written against an old head: %v, want a conflict at %d", err, h+1)
	}
	if seq, err := r.Append(ctx, a.SID, b.Me.Client, AppendRequest{Kind: KindLock, Base: h + 1, Ctr: 1, CT: []byte("lock-b")}); err != nil || seq != h+2 {
		t.Fatalf("after catching up: %d %v", seq, err)
	}
}

func TestAppend_TheCounterOnlyGrowsAndARefusedAttemptDoesNotSpendIt(t *testing.T) {
	g := newRig(t)
	r := g.relay()
	a := started(t, r, "d.docx", person(7, true))
	h := head(t, r, a.SID)
	if _, err := r.Append(ctx, a.SID, a.Me.Client, AppendRequest{Kind: KindLock, Base: h + 5, Ctr: 4, CT: []byte("x")}); err == nil {
		t.Fatal("a conflict was taken")
	}
	if _, err := r.Append(ctx, a.SID, a.Me.Client, AppendRequest{Kind: KindLock, Base: h, Ctr: 4, CT: []byte("x")}); err != nil {
		t.Fatalf("the refused attempt spent the counter: %v", err)
	}
	if _, err := r.Append(ctx, a.SID, a.Me.Client, AppendRequest{Kind: KindLock, Base: h + 1, Ctr: 4, CT: []byte("x")}); !errors.Is(err, ErrCounter) {
		t.Fatalf("a repeated counter: %v", err)
	}
}

func TestAppend_MembersWriteOnlyTheirKindsAndOnlyWithSomethingSealed(t *testing.T) {
	g := newRig(t)
	r := g.relay()
	a := started(t, r, "d.docx", person(7, true))
	for _, k := range []Kind{KindJoin, KindLeave, KindSaved, "other"} {
		if _, err := r.Append(ctx, a.SID, a.Me.Client, AppendRequest{Kind: k, Base: head(t, r, a.SID), Ctr: 1, CT: []byte("x")}); !errors.Is(err, ErrKind) {
			t.Fatalf("kind %q: %v", k, err)
		}
	}
	if _, err := r.Append(ctx, a.SID, a.Me.Client, AppendRequest{Kind: KindLock, Base: head(t, r, a.SID), Ctr: 1}); !errors.Is(err, ErrEmpty) {
		t.Fatalf("nothing sealed: %v", err)
	}
	big := make([]byte, DefaultMaxEntryBytes+1)
	if _, err := r.Append(ctx, a.SID, a.Me.Client, AppendRequest{Kind: KindLock, Base: head(t, r, a.SID), Ctr: 1, CT: big}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("too large: %v", err)
	}
}

func TestReadOnlyMember_ReadsAlongButWritesNothing(t *testing.T) {
	g := newRig(t)
	r := g.relay()
	a := started(t, r, "d.docx", person(7, true))
	v := open(t, r, "d.docx", person(9, false))
	if v.Me.CanEdit {
		t.Fatal("the server said the member may not write")
	}
	if _, err := r.Append(ctx, a.SID, v.Me.Client, AppendRequest{Kind: KindLock, Base: head(t, r, a.SID), Ctr: 1, CT: []byte("x")}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("append: %v", err)
	}
	if _, err := r.Lease(ctx, a.SID, v.Me.Client, LeaseAcquire, 0); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("lease: %v", err)
	}
	if _, err := r.Saved(ctx, a.SID, v.Me.Client, 1, "1:1"); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("saved: %v", err)
	}
	if _, err := r.PutBlob(ctx, a.SID, v.Me.Client, "m.image_fx0123456789abcdef.png", bytes.NewReader([]byte("img"))); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("an image: %v", err)
	}
	body, _, err := r.Blob(ctx, a.SID, v.Me.Client, BaseBlob)
	if err != nil {
		t.Fatalf("it reads along: %v", err)
	}
	got, _ := io.ReadAll(body)
	body.Close()
	if string(got) != "sealed-base" {
		t.Fatalf("base: %q", got)
	}
}

func TestChanges_NeedTheLeaseAndTheLeaseGoesOnlyToAWriterThatHasSeenEveryChange(t *testing.T) {
	g := newRig(t)
	r := g.relay()
	a := started(t, r, "d.docx", person(7, true))
	b := open(t, r, "d.docx", person(8, true))
	if _, err := r.Append(ctx, a.SID, a.Me.Client, AppendRequest{Kind: KindChanges, Base: head(t, r, a.SID), Ctr: 1, CT: []byte("c")}); !errors.Is(err, ErrNoLease) {
		t.Fatalf("changes without the lease: %v", err)
	}
	lease(t, r, a.SID, a.Me.Client, 0)
	if ok, _ := r.Lease(ctx, a.SID, b.Me.Client, LeaseAcquire, 0); ok {
		t.Fatal("two holders at once")
	}
	seq, err := r.Append(ctx, a.SID, a.Me.Client, AppendRequest{Kind: KindChanges, Base: head(t, r, a.SID), Ctr: 1, CT: []byte("c")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Lease(ctx, a.SID, a.Me.Client, LeaseRelease, seq); err != nil {
		t.Fatal(err)
	}
	if ok, _ := r.Lease(ctx, a.SID, b.Me.Client, LeaseAcquire, seq-1); ok {
		t.Fatal("a writer that has not seen the last change got the lease")
	}
	lease(t, r, a.SID, b.Me.Client, seq)
}

func TestLease_ExpiresIsRenewedByEveryChangeAndGoesWithItsHolder(t *testing.T) {
	g := newRig(t)
	r := g.relay()
	a := started(t, r, "d.docx", person(7, true))
	b := open(t, r, "d.docx", person(8, true))
	lease(t, r, a.SID, a.Me.Client, 0)
	g.clock.add(50 * time.Second)
	seq, err := r.Append(ctx, a.SID, a.Me.Client, AppendRequest{Kind: KindChanges, Base: head(t, r, a.SID), Ctr: 1, CT: []byte("c")})
	if err != nil {
		t.Fatal(err)
	}
	g.clock.add(50 * time.Second)
	if ok, _ := r.Lease(ctx, a.SID, b.Me.Client, LeaseAcquire, seq); ok {
		t.Fatal("a change renews the lease for 60 s")
	}
	g.clock.add(11 * time.Second)
	lease(t, r, a.SID, b.Me.Client, seq)

	if err := r.Leave(ctx, a.SID, b.Me.Client, "left"); err != nil {
		t.Fatal(err)
	}
	lease(t, r, a.SID, a.Me.Client, seq)
}

func TestJoin_IndexUserIsNeverGivenTwice(t *testing.T) {
	g := newRig(t)
	r := g.relay()
	a := started(t, r, "d.docx", person(7, true))
	b := open(t, r, "d.docx", person(8, true))
	if err := r.Leave(ctx, a.SID, b.Me.Client, "left"); err != nil {
		t.Fatal(err)
	}
	c := open(t, r, "d.docx", person(8, true))
	if c.Me.IndexUser != 3 || c.Me.Client != "c3" {
		t.Fatalf("after a leave: %+v", c.Me)
	}
}

func TestOpen_AnotherBuildOrAChangedFileIsRefusedWhileWorkIsUnsaved(t *testing.T) {
	g := newRig(t)
	r := g.relay()
	a := started(t, r, "d.docx", person(7, true))
	lease(t, r, a.SID, a.Me.Client, 0)
	if _, err := r.Append(ctx, a.SID, a.Me.Client, AppendRequest{Kind: KindChanges, Base: head(t, r, a.SID), Ctr: 1, CT: []byte("c")}); err != nil {
		t.Fatal(err)
	}
	other := docReq("d.docx")
	other.Build = "0.4.0+aaaa"
	if _, err := r.Open(ctx, other, person(8, true)); !errors.Is(err, ErrBuild) {
		t.Fatalf("another build: %v", err)
	}
	changed := docReq("d.docx")
	changed.FileSig = "120:2"
	if _, err := r.Open(ctx, changed, person(8, true)); !errors.Is(err, ErrFileChanged) {
		t.Fatalf("a changed file: %v", err)
	}
	if err := r.Leave(ctx, a.SID, a.Me.Client, "left"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Open(ctx, changed, person(8, true)); !errors.Is(err, ErrFileChanged) {
		t.Fatalf("nobody in it, but its changes are not saved: %v", err)
	}
	h := open(t, r, "d.docx", person(8, true))
	if h.SID != a.SID || h.Created {
		t.Fatalf("the next opener of the same file joins the session with its unsaved work: %+v", h)
	}
}

func TestOpen_ASavedEmptySessionMakesWayForANewFileOrBuild(t *testing.T) {
	g := newRig(t)
	r := g.relay()
	a := started(t, r, "d.docx", person(7, true))
	lease(t, r, a.SID, a.Me.Client, 0)
	seq, err := r.Append(ctx, a.SID, a.Me.Client, AppendRequest{Kind: KindChanges, Base: head(t, r, a.SID), Ctr: 1, CT: []byte("c")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Saved(ctx, a.SID, a.Me.Client, seq, "130:3"); err != nil {
		t.Fatal(err)
	}
	if err := r.Leave(ctx, a.SID, a.Me.Client, "left"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(g.dir, a.SID)); err != nil {
		t.Fatalf("the session's blobs are there: %v", err)
	}
	other := docReq("d.docx")
	other.Build = "0.4.0+aaaa"
	other.FileSig = "130:3"
	same := docReq("d.docx")
	same.FileSig = "130:3"
	n, err := r.Open(ctx, same, person(8, true))
	if err != nil || n.SID != a.SID {
		t.Fatalf("the saved file's signature is the session's now: joined %+v", n)
	}
	if err := r.Leave(ctx, n.SID, n.Me.Client, "left"); err != nil {
		t.Fatal(err)
	}
	fresh, err := r.Open(ctx, other, person(8, true))
	if err != nil || !fresh.Created || fresh.SID == a.SID {
		t.Fatalf("a new build of a saved, empty session starts afresh: %+v %v", fresh, err)
	}
	if _, err := os.Stat(filepath.Join(g.dir, a.SID)); !os.IsNotExist(err) {
		t.Fatalf("the old session's blobs went with it: %v", err)
	}
}

func TestSaved_MovesForwardAndNeverPastTheLog(t *testing.T) {
	g := newRig(t)
	r := g.relay()
	a := started(t, r, "d.docx", person(7, true))
	h := head(t, r, a.SID)
	if _, err := r.Saved(ctx, a.SID, a.Me.Client, h+3, "1:1"); !errors.Is(err, ErrSaveAhead) {
		t.Fatalf("ahead: %v", err)
	}
	if _, err := r.Saved(ctx, a.SID, a.Me.Client, h, "1:1"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Saved(ctx, a.SID, a.Me.Client, h-1, "1:1"); !errors.Is(err, ErrSaveBehind) {
		t.Fatalf("behind: %v", err)
	}
	es, _ := r.Entries(ctx, a.SID, h, 10)
	if len(es) != 1 || es[0].Kind != KindSaved || es[0].Through != h {
		t.Fatalf("the save is in the log: %+v", es)
	}
}

func TestOpen_AWatcherStartsNoSession(t *testing.T) {
	g := newRig(t)
	r := g.relay()
	// Nobody is editing: nothing to follow, and no session is made (its
	// starter stores the base every member builds on).
	if _, err := r.Open(ctx, docReq("d.docx"), person(9, false)); !errors.Is(err, ErrNothingToWatch) {
		t.Fatalf("a watcher first: %v", err)
	}
	if s, err := g.store.GetCoEditSessionByDoc(ctx, "d.docx"); err != nil || s != nil {
		t.Fatalf("no session made: %+v %v", s, err)
	}
	// A session somebody who may write started: the watcher joins it.
	a := started(t, r, "d.docx", person(7, true))
	v := open(t, r, "d.docx", person(9, false))
	if v.SID != a.SID || v.Created || v.Me.CanEdit {
		t.Fatalf("joined as a watcher: %+v", v)
	}
	// Everybody left: the session and its base are still there (the file is
	// what it holds), and a watcher joins it as it is.
	if err := r.Leave(ctx, a.SID, a.Me.Client, "left"); err != nil {
		t.Fatal(err)
	}
	if err := r.Leave(ctx, a.SID, v.Me.Client, "left"); err != nil {
		t.Fatal(err)
	}
	if again := open(t, r, "d.docx", person(9, false)); again.SID != a.SID || again.Created {
		t.Fatalf("a watcher after everybody left: %+v", again)
	}
}

func TestEntries_EveryMemberReadsTheSameLogInOrder(t *testing.T) {
	g := newRig(t)
	r := g.relay()
	a := started(t, r, "d.docx", person(7, true))
	b := open(t, r, "d.docx", person(8, false))
	if _, err := r.Append(ctx, a.SID, a.Me.Client, AppendRequest{Kind: KindLock, Base: head(t, r, a.SID), Ctr: 1, CT: []byte{0, 1, 2}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Leave(ctx, a.SID, b.Me.Client, "left"); err != nil {
		t.Fatal(err)
	}
	es, err := r.Entries(ctx, a.SID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	kinds := []Kind{}
	for i, e := range es {
		if e.Seq != uint64(i+1) {
			t.Fatalf("entry %d has seq %d", i, e.Seq)
		}
		kinds = append(kinds, e.Kind)
	}
	want := []Kind{KindJoin, KindJoin, KindLock, KindLeave}
	if fmt.Sprint(kinds) != fmt.Sprint(want) {
		t.Fatalf("kinds %v, want %v", kinds, want)
	}
	if es[1].Member == nil || es[1].Member.Client != b.Me.Client || es[1].Member.CanEdit || es[1].Member.User != b.Me.User {
		t.Fatalf("a join entry says who joined: %+v", es[1].Member)
	}
	if !bytes.Equal(es[2].CT, []byte{0, 1, 2}) || es[2].Ctr != 1 {
		t.Fatalf("the sealed payload comes back as it was: %+v", es[2])
	}
}

func TestWatch_ASocketIsWokenByAnEntryGetsTheOthersCursorsAndIsToldItsMembershipEnded(t *testing.T) {
	g := newRig(t)
	r := g.relay()
	a := started(t, r, "d.docx", person(7, true))
	b := open(t, r, "d.docx", person(8, true))
	wa, wb := r.Watch(a.SID, a.Me.Client), r.Watch(a.SID, b.Me.Client)
	defer wa.Close()
	defer wb.Close()
	if _, err := r.Append(ctx, a.SID, a.Me.Client, AppendRequest{Kind: KindLock, Base: head(t, r, a.SID), Ctr: 1, CT: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-wb.Notify:
	default:
		t.Fatal("b was not woken")
	}
	if err := r.SendEph(a.SID, Eph{Client: a.Me.Client, Ctr: 1, CT: []byte("cursor")}); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-wb.Eph:
		if e.Client != a.Me.Client {
			t.Fatalf("eph from %s", e.Client)
		}
	default:
		t.Fatal("b did not get a's cursor")
	}
	select {
	case <-wa.Eph:
		t.Fatal("a got its own cursor back")
	default:
	}
	if err := r.Leave(ctx, a.SID, b.Me.Client, "timeout"); err != nil {
		t.Fatal(err)
	}
	select {
	case why := <-wb.Ended:
		if why != "timeout" {
			t.Fatalf("why: %q", why)
		}
	default:
		t.Fatal("b was not told its membership ended")
	}
	if err := r.Touch(ctx, a.SID, b.Me.Client); !errors.Is(err, ErrNotMember) {
		t.Fatalf("a touch after the end: %v", err)
	}
}

func TestLog_HasACeiling(t *testing.T) {
	g := newRig(t)
	r := g.relay(func(o *Options) { o.MaxLogBytes = 10 })
	a := started(t, r, "d.docx", person(7, true))
	if _, err := r.Append(ctx, a.SID, a.Me.Client, AppendRequest{Kind: KindLock, Base: head(t, r, a.SID), Ctr: 1, CT: make([]byte, 8)}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Append(ctx, a.SID, a.Me.Client, AppendRequest{Kind: KindLock, Base: head(t, r, a.SID), Ctr: 2, CT: make([]byte, 8)}); !errors.Is(err, ErrLogFull) {
		t.Fatalf("past the ceiling: %v", err)
	}
}

func TestBlobs_AreWrittenOnceWithinTheirBudgetAndReadOnlyByMembers(t *testing.T) {
	g := newRig(t)
	r := g.relay(func(o *Options) { o.MaxBlobBytes = 20 })
	a := started(t, r, "d.docx", person(7, true))
	if _, err := r.PutBlob(ctx, a.SID, a.Me.Client, BaseBlob, bytes.NewReader([]byte("again"))); !errors.Is(err, ErrBlobSet) {
		t.Fatalf("a second base: %v", err)
	}
	if _, err := r.PutBlob(ctx, a.SID, a.Me.Client, "../x", bytes.NewReader([]byte("x"))); !errors.Is(err, ErrBlobName) {
		t.Fatalf("a bad name: %v", err)
	}
	if _, err := r.PutBlob(ctx, a.SID, a.Me.Client, "m.a.png", bytes.NewReader(make([]byte, 10))); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("past the budget (11 of 20 already): %v", err)
	}
	if n, err := r.PutBlob(ctx, a.SID, a.Me.Client, "m.a.png", bytes.NewReader(make([]byte, 9))); err != nil || n != 9 {
		t.Fatalf("within the budget: %d %v", n, err)
	}
	if _, _, err := r.Blob(ctx, a.SID, "c99", BaseBlob); !errors.Is(err, ErrNotMember) {
		t.Fatalf("a stranger: %v", err)
	}
	if _, _, err := r.Blob(ctx, a.SID, a.Me.Client, "m.none.png"); !errors.Is(err, ErrNoBlob) {
		t.Fatalf("no such blob: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(g.dir, a.SID))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".put-") {
			t.Fatalf("a refused upload left %s behind", e.Name())
		}
	}
}

func TestSweep_DropsSilentMembersAndForgetsEmptySessionsOnTime(t *testing.T) {
	g := newRig(t)
	r := g.relay()
	saved := started(t, r, "saved.docx", person(7, true))
	unsaved := started(t, r, "unsaved.docx", person(7, true))
	lease(t, r, unsaved.SID, unsaved.Me.Client, 0)
	if _, err := r.Append(ctx, unsaved.SID, unsaved.Me.Client, AppendRequest{Kind: KindChanges, Base: head(t, r, unsaved.SID), Ctr: 1, CT: []byte("c")}); err != nil {
		t.Fatal(err)
	}
	g.clock.add(30 * time.Second)
	if err := r.Touch(ctx, saved.SID, saved.Me.Client); err != nil {
		t.Fatal(err)
	}
	g.clock.add(20 * time.Second)
	r.Sweep(ctx)
	if _, err := r.Member(ctx, unsaved.SID, unsaved.Me.Client); !errors.Is(err, ErrNotMember) {
		t.Fatalf("unseen for 50 s: %v, want dropped", err)
	}
	if _, err := r.Member(ctx, saved.SID, saved.Me.Client); err != nil {
		t.Fatalf("seen 20 s ago: %v, want kept", err)
	}
	g.mu.Lock()
	if len(g.left) != 1 || g.left[0].Reason != "timeout" || g.left[0].Path != "unsaved.docx" {
		t.Fatalf("departures: %+v", g.left)
	}
	g.mu.Unlock()
	if err := r.Leave(ctx, saved.SID, saved.Me.Client, "left"); err != nil {
		t.Fatal(err)
	}

	g.clock.add(59 * time.Minute)
	if gone := r.Sweep(ctx); len(gone) != 0 {
		t.Fatalf("forgot %v before the hour", gone)
	}
	g.clock.add(2 * time.Minute)
	if gone := r.Sweep(ctx); len(gone) != 1 || gone[0] != saved.SID {
		t.Fatalf("after an hour the saved session goes: %v", gone)
	}
	if _, err := os.Stat(filepath.Join(g.dir, saved.SID)); !os.IsNotExist(err) {
		t.Fatalf("its blobs went too: %v", err)
	}
	g.clock.add(29 * 24 * time.Hour)
	if gone := r.Sweep(ctx); len(gone) != 0 {
		t.Fatalf("unsaved work kept 30 days, forgot %v", gone)
	}
	g.clock.add(24 * time.Hour)
	if gone := r.Sweep(ctx); len(gone) != 1 || gone[0] != unsaved.SID {
		t.Fatalf("after 30 days: %v", gone)
	}
}

func TestOpen_ACreatorThatLeftBeforeTheBaseLetsSomebodyElseStart(t *testing.T) {
	g := newRig(t)
	r := g.relay()
	a := open(t, r, "d.docx", person(7, true))
	if err := r.Leave(ctx, a.SID, a.Me.Client, "timeout"); err != nil {
		t.Fatal(err)
	}
	b := open(t, r, "d.docx", person(8, true))
	if !b.Created || b.SID == a.SID {
		t.Fatalf("a session nobody will ever finish makes way: %+v", b)
	}
}

func TestOpen_AnOrdinaryFolderNeedsASecretKeyAndAnEncryptedOneKeepsItsOwn(t *testing.T) {
	g := newRig(t)
	r := g.relay(func(o *Options) { o.Keys = nil })
	if _, err := r.Open(ctx, docReq("d.docx"), person(7, true)); !errors.Is(err, ErrNoServerKey) {
		t.Fatalf("no secret key: %v", err)
	}
	req := docReq("e.docx")
	req.KeyMode = KeyFolder
	a, err := r.Open(ctx, req, person(7, true))
	if err != nil || !a.Created || a.Key != nil || a.SealedKey != "" {
		t.Fatalf("folder mode: %+v %v", a, err)
	}
	if _, err := r.PutBlob(ctx, a.SID, a.Me.Client, BaseBlob, bytes.NewReader([]byte("b"))); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Open(ctx, req, person(8, true)); !errors.Is(err, ErrNotReady) {
		t.Fatalf("no key yet: %v", err)
	}
	if err := r.SetKey(ctx, a.SID, a.Me.Client, "sealed-under-the-folder-key"); err != nil {
		t.Fatal(err)
	}
	if err := r.SetKey(ctx, a.SID, a.Me.Client, "another"); !errors.Is(err, ErrKeySet) {
		t.Fatalf("a second key: %v", err)
	}
	b, err := r.Open(ctx, req, person(8, true))
	if err != nil || b.SealedKey != "sealed-under-the-folder-key" || b.Key != nil {
		t.Fatalf("the joiner gets the sealed key, never a raw one: %+v %v", b, err)
	}
}

func TestOpen_ASessionHasAsManyMembersAsItMay(t *testing.T) {
	g := newRig(t)
	r := g.relay(func(o *Options) { o.MaxMembers = 2 })
	started(t, r, "d.docx", person(7, true))
	open(t, r, "d.docx", person(8, true))
	if _, err := r.Open(ctx, docReq("d.docx"), person(9, true)); !errors.Is(err, ErrFull) {
		t.Fatalf("a third: %v", err)
	}
}

func TestOpen_WhoJoinsIsWhoTheServerSays(t *testing.T) {
	g := newRig(t)
	r := g.relay()
	if _, err := r.Open(ctx, docReq("d.docx"), Identity{User: 0, Name: "anybody", CanEdit: true}); !errors.Is(err, ErrNoIdentity) {
		t.Fatalf("no account: %v", err)
	}
	a := started(t, r, "d.docx", Identity{User: 7, Person: "7/work-1", Name: "Ayşe (work)", CanEdit: true})
	b := open(t, r, "d.docx", Identity{User: 7, Person: "7/work-2", Name: "Dana (work)", CanEdit: true})
	if a.Me.User == b.Me.User {
		t.Fatal("two people behind one proxy token are two people")
	}
	if b.Me.Name != "Dana (work)" {
		t.Fatalf("name: %q", b.Me.Name)
	}
}

func TestRelay_TwoProcessesWritingAtOnceNeverShareAPlace(t *testing.T) {
	g := newRig(t)
	p1, p2 := g.relay(), g.relay()
	a := started(t, p1, "d.docx", person(7, true))
	b := open(t, p2, "d.docx", person(8, true))
	var wg sync.WaitGroup
	write := func(r *Relay, client string) {
		defer wg.Done()
		for ctr := uint64(1); ctr <= 20; ctr++ {
			for {
				_, err := r.Append(ctx, a.SID, client, AppendRequest{Kind: KindLock, Base: head(t, r, a.SID), Ctr: ctr, CT: []byte("x")})
				var conflict *ConflictError
				if errors.As(err, &conflict) {
					continue
				}
				if err != nil {
					t.Errorf("append: %v", err)
					return
				}
				break
			}
		}
	}
	wg.Add(2)
	go write(p1, a.Me.Client)
	go write(p2, b.Me.Client)
	wg.Wait()
	es, err := p1.Entries(ctx, a.SID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	locks := 0
	for i, e := range es {
		if e.Seq != uint64(i+1) {
			t.Fatalf("hole or repeat at %d: %d", i, e.Seq)
		}
		if e.Kind == KindLock {
			locks++
		}
	}
	if locks != 40 {
		t.Fatalf("%d lock entries, want 40", locks)
	}
}

func TestAuthorise_ARequestNamingAMemberMustBeItsAccountAndCarryItsToken(t *testing.T) {
	g := newRig(t)
	r := g.relay()
	tok, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	who := person(7, true)
	who.TokenHash = TokenHash(tok)
	a, err := r.Open(ctx, docReq("d.docx"), who)
	if err != nil {
		t.Fatal(err)
	}
	if m, err := r.Authorise(ctx, a.SID, a.Me.Client, tok, 7); err != nil || m.Client != a.Me.Client {
		t.Fatalf("its own request: %v %v", m, err)
	}
	for name, try := range map[string]func() error{
		"another account":  func() error { _, err := r.Authorise(ctx, a.SID, a.Me.Client, tok, 8); return err },
		"another token":    func() error { _, err := r.Authorise(ctx, a.SID, a.Me.Client, tok+"0", 7); return err },
		"no token":         func() error { _, err := r.Authorise(ctx, a.SID, a.Me.Client, "", 7); return err },
		"another client":   func() error { _, err := r.Authorise(ctx, a.SID, "c2", tok, 7); return err },
		"not a session id": func() error { _, err := r.Authorise(ctx, "../"+a.SID, a.Me.Client, tok, 7); return err },
		"another session": func() error {
			_, err := r.Authorise(ctx, "ffffffffffffffffffffffffffffffff", a.Me.Client, tok, 7)
			return err
		},
	} {
		if err := try(); !errors.Is(err, ErrNotMember) {
			t.Errorf("%s: %v, want ErrNotMember", name, err)
		}
	}
}

// A cursor reaches the members connected to ANOTHER process on the same
// database (filex 0.56): the member's latest one is written to its row, at
// most every CursorEvery and the last one always, and goes with the member.
// Red before: cursors were in-process only.
func TestCursors_ReachTheMembersOfAnotherProcess(t *testing.T) {
	g := newRig(t)
	p1, p2 := g.relay(), g.relay()
	a := started(t, p1, "d.docx", person(7, true))
	b := open(t, p2, "d.docx", person(8, true))
	if err := p1.SendEph(a.SID, Eph{Client: a.Me.Client, Ctr: 1, CT: []byte("first")}); err != nil {
		t.Fatal(err)
	}
	cs, err := p2.Cursors(ctx, a.SID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].Client != a.Me.Client || cs[0].Ctr != 1 || string(cs[0].CT) != "first" {
		t.Fatalf("the other process reads a's cursor: %+v", cs)
	}
	// A second cursor right after the first is written when CursorEvery is up.
	if err := p1.SendEph(a.SID, Eph{Client: a.Me.Client, Ctr: 2, CT: []byte("second")}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		cs, err = p2.Cursors(ctx, a.SID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(cs) == 1 && cs[0].Ctr == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the latest cursor was never written: %+v", cs)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := p1.Leave(ctx, a.SID, a.Me.Client, "left"); err != nil {
		t.Fatal(err)
	}
	if cs, _ = p2.Cursors(ctx, a.SID, 0); len(cs) != 0 {
		t.Fatalf("a member's cursor goes with it: %+v", cs)
	}
	_ = b
}
