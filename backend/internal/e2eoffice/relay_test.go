package e2eoffice

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
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

func newRelay(t *testing.T) (*Relay, *clock) {
	t.Helper()
	c := &clock{t: time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)}
	return New(Options{Now: c.now, Who: signedIn}), c
}

// signedInKey stands for the account a request is signed in as: the server's
// own knowledge of the caller, which the relay asks through Options.Who.
type signedInKey struct{}

func as(user int64, canEdit bool) context.Context {
	return context.WithValue(context.Background(), signedInKey{}, Identity{User: user, Name: "user", CanEdit: canEdit})
}

func signedIn(ctx context.Context, _ string) (Identity, error) {
	id, ok := ctx.Value(signedInKey{}).(Identity)
	if !ok {
		return Identity{}, ErrNoIdentity
	}
	return id, nil
}

// readySession opens a session for doc and gives it a key and a base, the
// way its creator does before anybody joins.
func readySession(t *testing.T, r *Relay, doc string) string {
	t.Helper()
	res, err := r.Open(OpenRequest{Doc: doc, Build: "9.4.0-129", FileSig: "100:1"})
	if err != nil || !res.Created {
		t.Fatalf("open: %+v %v", res, err)
	}
	if err := r.SetKey(res.SID, []byte("sealed-session-key")); err != nil {
		t.Fatal(err)
	}
	if err := r.PutBlob(res.SID, BaseBlob, []byte("sealed-base")); err != nil {
		t.Fatal(err)
	}
	return res.SID
}

func join(t *testing.T, r *Relay, sid string, user int64, canEdit bool) Hello {
	t.Helper()
	h, err := r.Join(as(user, canEdit), sid)
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	return h
}

func head(t *testing.T, r *Relay, sid string) uint64 {
	t.Helper()
	st, err := r.State(sid)
	if err != nil {
		t.Fatal(err)
	}
	return st.Head
}

func TestAppend_LandsRightAfterTheHeadItWasWrittenAgainstOrNotAtAll(t *testing.T) {
	r, _ := newRelay(t)
	sid := readySession(t, r, "d")
	a := join(t, r, sid, 1, true)
	b := join(t, r, sid, 2, true)

	base := head(t, r, sid)
	seqA, err := r.Append(sid, a.Client, AppendRequest{Kind: KindLock, Base: base, Ctr: 1, CT: []byte("a")})
	if err != nil || seqA != base+1 {
		t.Fatalf("first append: seq=%d err=%v", seqA, err)
	}
	// b wrote against the same head: it does not land behind a's entry.
	_, err = r.Append(sid, b.Client, AppendRequest{Kind: KindLock, Base: base, Ctr: 1, CT: []byte("b")})
	var conflict *ConflictError
	if !errors.As(err, &conflict) || conflict.Head != seqA {
		t.Fatalf("second append against the old head: %v", err)
	}
	// Caught up, b lands right after.
	seqB, err := r.Append(sid, b.Client, AppendRequest{Kind: KindLock, Base: seqA, Ctr: 1, CT: []byte("b")})
	if err != nil || seqB != seqA+1 {
		t.Fatalf("retry: seq=%d err=%v", seqB, err)
	}
}

func TestAppend_TheCounterOnlyGrowsAndARefusedAttemptDoesNotSpendIt(t *testing.T) {
	r, _ := newRelay(t)
	sid := readySession(t, r, "d")
	a := join(t, r, sid, 1, true)
	h := head(t, r, sid)
	if _, err := r.Append(sid, a.Client, AppendRequest{Kind: KindLock, Base: h - 1, Ctr: 5, CT: []byte("x")}); err == nil {
		t.Fatal("a stale base was accepted")
	}
	// The conflict did not use counter 5.
	if _, err := r.Append(sid, a.Client, AppendRequest{Kind: KindLock, Base: h, Ctr: 5, CT: []byte("x")}); err != nil {
		t.Fatalf("retry with the same counter: %v", err)
	}
	if _, err := r.Append(sid, a.Client, AppendRequest{Kind: KindLock, Base: h + 1, Ctr: 5, CT: []byte("y")}); !errors.Is(err, ErrCounter) {
		t.Fatalf("a repeated counter: %v", err)
	}
}

func TestAppend_MembersWriteOnlyTheirKindsAndOnlyWithSomethingSealed(t *testing.T) {
	r, _ := newRelay(t)
	sid := readySession(t, r, "d")
	a := join(t, r, sid, 1, true)
	h := head(t, r, sid)
	for _, k := range []Kind{KindJoin, KindLeave, KindSaved, Kind("other")} {
		if _, err := r.Append(sid, a.Client, AppendRequest{Kind: k, Base: h, Ctr: 1, CT: []byte("x")}); !errors.Is(err, ErrKind) {
			t.Fatalf("kind %q: %v", k, err)
		}
	}
	if _, err := r.Append(sid, a.Client, AppendRequest{Kind: KindLock, Base: h, Ctr: 1}); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty payload: %v", err)
	}
	if _, err := r.Append(sid, "c99", AppendRequest{Kind: KindLock, Base: h, Ctr: 1, CT: []byte("x")}); !errors.Is(err, ErrNotMember) {
		t.Fatalf("stranger: %v", err)
	}
}

func TestReadOnlyMember_ReadsAlongButWritesNothing(t *testing.T) {
	r, _ := newRelay(t)
	sid := readySession(t, r, "d")
	v := join(t, r, sid, 3, false)
	h := head(t, r, sid)
	if _, err := r.Append(sid, v.Client, AppendRequest{Kind: KindLock, Base: h, Ctr: 1, CT: []byte("x")}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("append: %v", err)
	}
	if _, err := r.Lease(sid, v.Client, LeaseAcquire, 0); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("lease: %v", err)
	}
	if _, err := r.Saved(sid, v.Client, h, "1:1"); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("saved: %v", err)
	}
	if err := r.SendEph(sid, v.Client, []byte("cursor")); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("eph: %v", err)
	}
	backlog, _, err := r.Subscribe(sid, v.Client, 0)
	if err != nil || len(backlog) == 0 {
		t.Fatalf("subscribe: %d entries, %v", len(backlog), err)
	}
}

func TestChanges_NeedTheLease(t *testing.T) {
	r, _ := newRelay(t)
	sid := readySession(t, r, "d")
	a := join(t, r, sid, 1, true)
	h := head(t, r, sid)
	if _, err := r.Append(sid, a.Client, AppendRequest{Kind: KindChanges, Base: h, Ctr: 1, CT: []byte("x")}); !errors.Is(err, ErrNoLease) {
		t.Fatalf("changes without the lease: %v", err)
	}
	if ok, err := r.Lease(sid, a.Client, LeaseAcquire, 0); !ok || err != nil {
		t.Fatalf("lease: %v %v", ok, err)
	}
	seq, err := r.Append(sid, a.Client, AppendRequest{Kind: KindChanges, Base: h, Ctr: 1, CT: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := r.State(sid)
	if st.ChangesHead != seq || !st.Dirty {
		t.Fatalf("state after changes: %+v", st)
	}
}

func TestLease_GoesOnlyToAWriterThatHasSeenEveryChange(t *testing.T) {
	r, _ := newRelay(t)
	sid := readySession(t, r, "d")
	a := join(t, r, sid, 1, true)
	b := join(t, r, sid, 2, true)

	if ok, _ := r.Lease(sid, a.Client, LeaseAcquire, 0); !ok {
		t.Fatal("a: lease refused on an empty log")
	}
	seq, err := r.Append(sid, a.Client, AppendRequest{Kind: KindChanges, Base: head(t, r, sid), Ctr: 1, CT: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Lease(sid, a.Client, LeaseRelease, seq); err != nil {
		t.Fatal(err)
	}
	// b has not seen a's changes: the lease would let it write changes built
	// on an older document.
	if ok, _ := r.Lease(sid, b.Client, LeaseAcquire, 0); ok {
		t.Fatal("b got the lease without having seen a's changes")
	}
	if ok, _ := r.Lease(sid, b.Client, LeaseAcquire, seq); !ok {
		t.Fatal("b caught up and was still refused")
	}
}

func TestLease_OneHolderAtATimeUntilItIsReleasedOrExpires(t *testing.T) {
	r, c := newRelay(t)
	sid := readySession(t, r, "d")
	a := join(t, r, sid, 1, true)
	b := join(t, r, sid, 2, true)

	if ok, _ := r.Lease(sid, a.Client, LeaseAcquire, 0); !ok {
		t.Fatal("a refused")
	}
	if ok, _ := r.Lease(sid, b.Client, LeaseAcquire, 0); ok {
		t.Fatal("b got a lease a holds")
	}
	// a renews: still a's.
	if ok, _ := r.Lease(sid, a.Client, LeaseAcquire, 0); !ok {
		t.Fatal("a could not renew")
	}
	c.add(defaultLeaseTTL + time.Second)
	// a's lease ran out: its changes are refused and b may take it.
	if _, err := r.Append(sid, a.Client, AppendRequest{Kind: KindChanges, Base: head(t, r, sid), Ctr: 1, CT: []byte("x")}); !errors.Is(err, ErrNoLease) {
		t.Fatalf("changes under an expired lease: %v", err)
	}
	if ok, _ := r.Lease(sid, b.Client, LeaseAcquire, 0); !ok {
		t.Fatal("b refused an expired lease")
	}
	if _, err := r.Lease(sid, b.Client, LeaseRelease, 0); err != nil {
		t.Fatal(err)
	}
	if ok, _ := r.Lease(sid, a.Client, LeaseAcquire, 0); !ok {
		t.Fatal("a refused a released lease")
	}
}

func TestLease_EveryChangeRenewsIt(t *testing.T) {
	r, c := newRelay(t)
	sid := readySession(t, r, "d")
	a := join(t, r, sid, 1, true)
	if ok, _ := r.Lease(sid, a.Client, LeaseAcquire, 0); !ok {
		t.Fatal("refused")
	}
	for i := uint64(1); i <= 3; i++ {
		c.add(defaultLeaseTTL - time.Second)
		if _, err := r.Append(sid, a.Client, AppendRequest{Kind: KindChanges, Base: head(t, r, sid), Ctr: i, CT: []byte("x")}); err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
	}
}

func TestLeave_ReleasesTheLease(t *testing.T) {
	r, _ := newRelay(t)
	sid := readySession(t, r, "d")
	a := join(t, r, sid, 1, true)
	b := join(t, r, sid, 2, true)
	if ok, _ := r.Lease(sid, a.Client, LeaseAcquire, 0); !ok {
		t.Fatal("refused")
	}
	if err := r.Leave(sid, a.Client); err != nil {
		t.Fatal(err)
	}
	if ok, _ := r.Lease(sid, b.Client, LeaseAcquire, 0); !ok {
		t.Fatal("the lease of a member that left is still held")
	}
	if _, err := r.Lease(sid, a.Client, LeaseAcquire, 0); !errors.Is(err, ErrNotMember) {
		t.Fatalf("a member that left: %v", err)
	}
}

func TestJoin_IndexUserIsNeverGivenTwice(t *testing.T) {
	r, _ := newRelay(t)
	sid := readySession(t, r, "d")
	seen := map[int]bool{}
	clients := map[string]bool{}
	for i := 0; i < 5; i++ {
		h := join(t, r, sid, 7, true)
		if seen[h.IndexUser] || clients[h.Client] {
			t.Fatalf("join %d reused index %d or client %s", i, h.IndexUser, h.Client)
		}
		seen[h.IndexUser] = true
		clients[h.Client] = true
		// The same person leaving and coming back gets a new index: the
		// editor's object ids are built from it.
		if err := r.Leave(sid, h.Client); err != nil {
			t.Fatal(err)
		}
	}
}

func TestJoin_WaitsForTheKeyAndTheBase(t *testing.T) {
	r, _ := newRelay(t)
	res, err := r.Open(OpenRequest{Doc: "d", Build: "b", FileSig: "1:1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Join(as(1, true), res.SID); !errors.Is(err, ErrNotReady) {
		t.Fatalf("join before the key: %v", err)
	}
	if err := r.SetKey(res.SID, []byte("k")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Join(as(1, true), res.SID); !errors.Is(err, ErrNotReady) {
		t.Fatalf("join before the base: %v", err)
	}
	if err := r.PutBlob(res.SID, BaseBlob, []byte("b")); err != nil {
		t.Fatal(err)
	}
	h, err := r.Join(as(1, true), res.SID)
	if err != nil || string(h.Key) != "k" {
		t.Fatalf("join: %+v %v", h, err)
	}
}

func TestSetKeyAndBlobs_AreWrittenOnce(t *testing.T) {
	r, _ := newRelay(t)
	sid := readySession(t, r, "d")
	if err := r.SetKey(sid, []byte("other")); !errors.Is(err, ErrKeySet) {
		t.Fatalf("second key: %v", err)
	}
	if err := r.PutBlob(sid, BaseBlob, []byte("other")); !errors.Is(err, ErrBlobSet) {
		t.Fatalf("second base: %v", err)
	}
	for _, bad := range []string{"", "../x", "a/b", "media image.png"} {
		if err := r.PutBlob(sid, bad, []byte("x")); !errors.Is(err, ErrBlobName) {
			t.Fatalf("blob name %q: %v", bad, err)
		}
	}
	if err := r.PutBlob(sid, "image1.png", []byte("img")); err != nil {
		t.Fatal(err)
	}
	got, err := r.Blob(sid, "image1.png")
	if err != nil || string(got) != "img" {
		t.Fatalf("blob: %q %v", got, err)
	}
	if _, err := r.Blob(sid, "nope.png"); !errors.Is(err, ErrNoBlob) {
		t.Fatalf("missing blob: %v", err)
	}
}

func TestOpen_OneFileOneSession(t *testing.T) {
	r, _ := newRelay(t)
	sid := readySession(t, r, "d")
	res, err := r.Open(OpenRequest{Doc: "d", Build: "9.4.0-129", FileSig: "100:1"})
	if err != nil || res.Created || res.SID != sid || string(res.Key) != "sealed-session-key" {
		t.Fatalf("second opener: %+v %v", res, err)
	}
	other, err := r.Open(OpenRequest{Doc: "e", Build: "9.4.0-129", FileSig: "100:1"})
	if err != nil || !other.Created || other.SID == sid {
		t.Fatalf("another file: %+v %v", other, err)
	}
}

func TestOpen_AnotherBuildOrAChangedFileIsRefusedWhileWorkIsUnsaved(t *testing.T) {
	r, _ := newRelay(t)
	sid := readySession(t, r, "d")
	a := join(t, r, sid, 1, true)
	if ok, _ := r.Lease(sid, a.Client, LeaseAcquire, 0); !ok {
		t.Fatal("refused")
	}
	if _, err := r.Append(sid, a.Client, AppendRequest{Kind: KindChanges, Base: head(t, r, sid), Ctr: 1, CT: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if err := r.Leave(sid, a.Client); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Open(OpenRequest{Doc: "d", Build: "9.5.0-1", FileSig: "100:1"}); !errors.Is(err, ErrBuild) {
		t.Fatalf("another build: %v", err)
	}
	if _, err := r.Open(OpenRequest{Doc: "d", Build: "9.4.0-129", FileSig: "200:2"}); !errors.Is(err, ErrFileChanged) {
		t.Fatalf("a file changed outside: %v", err)
	}
}

func TestOpen_ASavedEmptySessionMakesWayForANewFileOrBuild(t *testing.T) {
	r, _ := newRelay(t)
	sid := readySession(t, r, "d")
	a := join(t, r, sid, 1, true)
	if ok, _ := r.Lease(sid, a.Client, LeaseAcquire, 0); !ok {
		t.Fatal("refused")
	}
	seq, err := r.Append(sid, a.Client, AppendRequest{Kind: KindChanges, Base: head(t, r, sid), Ctr: 1, CT: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Saved(sid, a.Client, seq, "300:3"); err != nil {
		t.Fatal(err)
	}
	// The save is the file now: opening it as it is continues the session.
	same, err := r.Open(OpenRequest{Doc: "d", Build: "9.4.0-129", FileSig: "300:3"})
	if err != nil || same.SID != sid {
		t.Fatalf("after the save: %+v %v", same, err)
	}
	if err := r.Leave(sid, a.Client); err != nil {
		t.Fatal(err)
	}
	fresh, err := r.Open(OpenRequest{Doc: "d", Build: "9.4.0-129", FileSig: "400:4"})
	if err != nil || !fresh.Created || fresh.SID == sid {
		t.Fatalf("a changed file after a clean session: %+v %v", fresh, err)
	}
	if _, err := r.State(sid); !errors.Is(err, ErrNoSession) {
		t.Fatalf("the replaced session: %v", err)
	}
}

func TestSaved_MovesForwardAndCleansTheSession(t *testing.T) {
	r, _ := newRelay(t)
	sid := readySession(t, r, "d")
	a := join(t, r, sid, 1, true)
	if ok, _ := r.Lease(sid, a.Client, LeaseAcquire, 0); !ok {
		t.Fatal("refused")
	}
	seq, err := r.Append(sid, a.Client, AppendRequest{Kind: KindChanges, Base: head(t, r, sid), Ctr: 1, CT: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Saved(sid, a.Client, seq+5, "1:2"); !errors.Is(err, ErrSaveAhead) {
		t.Fatalf("a save past the head: %v", err)
	}
	if _, err := r.Saved(sid, a.Client, seq, "1:2"); err != nil {
		t.Fatal(err)
	}
	st, _ := r.State(sid)
	if st.Dirty || st.SavedThrough != seq {
		t.Fatalf("after the save: %+v", st)
	}
	if _, err := r.Saved(sid, a.Client, seq-1, "1:3"); !errors.Is(err, ErrSaveBehind) {
		t.Fatalf("an older save: %v", err)
	}
	// Locks are not document changes: they do not make the session dirty.
	if _, err := r.Append(sid, a.Client, AppendRequest{Kind: KindLock, Base: head(t, r, sid), Ctr: 2, CT: []byte("l")}); err != nil {
		t.Fatal(err)
	}
	if st, _ := r.State(sid); st.Dirty {
		t.Fatal("a lock made the session dirty")
	}
}

// Every member reads one order: the join, the entries, the leave, the save,
// in the same sequence for everyone, with nothing missing.
func TestSubscribers_AllSeeOneOrder(t *testing.T) {
	r, _ := newRelay(t)
	sid := readySession(t, r, "d")
	a := join(t, r, sid, 1, true)
	b := join(t, r, sid, 2, true)
	_, chA, err := r.Subscribe(sid, a.Client, 0)
	if err != nil {
		t.Fatal(err)
	}
	backB, chB, err := r.Subscribe(sid, b.Client, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(backB) != 2 || backB[0].Kind != KindJoin || backB[1].Member.Client != b.Client {
		t.Fatalf("b's backlog: %+v", backB)
	}
	ctr := map[string]uint64{}
	for i := 0; i < 6; i++ {
		who := a.Client
		if i%2 == 1 {
			who = b.Client
		}
		ctr[who]++
		if _, err := r.Append(sid, who, AppendRequest{Kind: KindLock, Base: head(t, r, sid), Ctr: ctr[who], CT: []byte{byte(i)}}); err != nil {
			t.Fatal(err)
		}
	}
	c := join(t, r, sid, 3, true)
	var gotA, gotB []uint64
	for len(gotB) < 7 {
		f := <-chB
		gotB = append(gotB, f.Entry.Seq)
	}
	for len(gotA) < 7 {
		f := <-chA
		gotA = append(gotA, f.Entry.Seq)
	}
	for i := range gotA {
		if gotA[i] != gotB[i] || gotA[i] != uint64(i+3) {
			t.Fatalf("a saw %v, b saw %v", gotA, gotB)
		}
	}
	backC, _, err := r.Subscribe(sid, c.Client, 0)
	if err != nil || len(backC) != 9 {
		t.Fatalf("a late joiner's backlog: %d %v", len(backC), err)
	}
	for i, e := range backC {
		if e.Seq != uint64(i+1) {
			t.Fatalf("backlog out of order at %d: %d", i, e.Seq)
		}
	}
}

// A member that does not keep up is cut off, never skipped: its channel
// closes, and subscribing again from what it has gives it the rest.
func TestSubscriber_ThatFallsBehindIsCutOffNotSkipped(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)}
	r := New(Options{Now: c.now, SubscriberBuffer: 2, Who: signedIn})
	sid := readySession(t, r, "d")
	a := join(t, r, sid, 1, true)
	slow := join(t, r, sid, 2, true)
	back, ch, err := r.Subscribe(sid, slow.Client, 0)
	if err != nil {
		t.Fatal(err)
	}
	last := back[len(back)-1].Seq
	for i := uint64(1); i <= 5; i++ {
		if _, err := r.Append(sid, a.Client, AppendRequest{Kind: KindLock, Base: head(t, r, sid), Ctr: i, CT: []byte("x")}); err != nil {
			t.Fatal(err)
		}
	}
	for f := range ch {
		if f.Entry.Seq != last+1 {
			t.Fatalf("got %d after %d", f.Entry.Seq, last)
		}
		last = f.Entry.Seq
	}
	if last == head(t, r, sid) {
		t.Fatal("the buffer never filled; the test proves nothing")
	}
	rest, _, err := r.Subscribe(sid, slow.Client, last)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) == 0 || rest[0].Seq != last+1 || rest[len(rest)-1].Seq != head(t, r, sid) {
		t.Fatalf("resubscribing from %d gave %+v", last, rest)
	}
}

func TestEph_GoesToTheOthersAndIsNotKept(t *testing.T) {
	r, _ := newRelay(t)
	sid := readySession(t, r, "d")
	a := join(t, r, sid, 1, true)
	b := join(t, r, sid, 2, true)
	_, chA, _ := r.Subscribe(sid, a.Client, head(t, r, sid))
	_, chB, _ := r.Subscribe(sid, b.Client, head(t, r, sid))
	before := head(t, r, sid)
	if err := r.SendEph(sid, a.Client, []byte("cursor")); err != nil {
		t.Fatal(err)
	}
	select {
	case f := <-chB:
		if f.Eph == nil || f.Eph.Client != a.Client || string(f.Eph.CT) != "cursor" {
			t.Fatalf("b got %+v", f)
		}
	default:
		t.Fatal("b got nothing")
	}
	select {
	case f := <-chA:
		t.Fatalf("the sender got its own cursor: %+v", f)
	default:
	}
	if head(t, r, sid) != before {
		t.Fatal("a cursor went into the log")
	}
}

func TestLog_HasACeiling(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)}
	r := New(Options{Now: c.now, MaxLogBytes: 10, MaxEntryBytes: 8, Who: signedIn})
	sid := readySession(t, r, "d")
	a := join(t, r, sid, 1, true)
	if _, err := r.Append(sid, a.Client, AppendRequest{Kind: KindLock, Base: head(t, r, sid), Ctr: 1, CT: make([]byte, 9)}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("an entry over the cap: %v", err)
	}
	if _, err := r.Append(sid, a.Client, AppendRequest{Kind: KindLock, Base: head(t, r, sid), Ctr: 1, CT: make([]byte, 8)}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Append(sid, a.Client, AppendRequest{Kind: KindLock, Base: head(t, r, sid), Ctr: 2, CT: make([]byte, 8)}); !errors.Is(err, ErrLogFull) {
		t.Fatalf("past the log's cap: %v", err)
	}
}

func TestSweep_KeepsUnsavedWorkThirtyDaysAndASavedSessionAnHour(t *testing.T) {
	r, c := newRelay(t)
	unsaved := readySession(t, r, "unsaved")
	a := join(t, r, unsaved, 1, true)
	if ok, _ := r.Lease(unsaved, a.Client, LeaseAcquire, 0); !ok {
		t.Fatal("refused")
	}
	if _, err := r.Append(unsaved, a.Client, AppendRequest{Kind: KindChanges, Base: head(t, r, unsaved), Ctr: 1, CT: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if err := r.Leave(unsaved, a.Client); err != nil {
		t.Fatal(err)
	}
	saved := readySession(t, r, "saved")
	b := join(t, r, saved, 2, true)
	if err := r.Leave(saved, b.Client); err != nil {
		t.Fatal(err)
	}
	occupied := readySession(t, r, "occupied")
	join(t, r, occupied, 3, true)

	c.add(2 * time.Hour)
	if got := r.Sweep(); len(got) != 1 || got[0] != saved {
		t.Fatalf("after two hours: %v", got)
	}
	c.add(29 * 24 * time.Hour)
	if got := r.Sweep(); len(got) != 0 {
		t.Fatalf("after 29 days: %v", got)
	}
	if st, ok := r.StateOf("unsaved"); !ok || !st.Dirty {
		t.Fatalf("the unsaved session is gone or clean: %+v %v", st, ok)
	}
	c.add(2 * 24 * time.Hour)
	if got := r.Sweep(); len(got) != 1 || got[0] != unsaved {
		t.Fatalf("after 31 days: %v", got)
	}
	if _, ok := r.StateOf("occupied"); !ok {
		t.Fatal("a session somebody is in was swept")
	}
}

func TestOpen_ACreatorThatNeverFinishedLetsSomebodyElseStart(t *testing.T) {
	r, c := newRelay(t)
	res, err := r.Open(OpenRequest{Doc: "d", Build: "b", FileSig: "1:1"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := r.Open(OpenRequest{Doc: "d", Build: "b", FileSig: "1:1"})
	if err != nil || again.Created || again.SID != res.SID {
		t.Fatalf("while the creator is still at it: %+v %v", again, err)
	}
	c.add(defaultNotReadyTTL + time.Second)
	fresh, err := r.Open(OpenRequest{Doc: "d", Build: "b", FileSig: "1:1"})
	if err != nil || !fresh.Created || fresh.SID == res.SID {
		t.Fatalf("after the creator gave up: %+v %v", fresh, err)
	}
}

func TestRelay_IsSafeForConcurrentWriters(t *testing.T) {
	r, _ := newRelay(t)
	sid := readySession(t, r, "d")
	const writers, each = 4, 25
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		h := join(t, r, sid, int64(w+1), true)
		wg.Add(1)
		go func(client string) {
			defer wg.Done()
			var ctr uint64
			for n := 0; n < each; {
				st, err := r.State(sid)
				if err != nil {
					t.Error(err)
					return
				}
				_, err = r.Append(sid, client, AppendRequest{Kind: KindLock, Base: st.Head, Ctr: ctr + 1, CT: []byte("x")})
				var conflict *ConflictError
				if errors.As(err, &conflict) {
					continue
				}
				if err != nil {
					t.Error(err)
					return
				}
				ctr++
				n++
			}
		}(h.Client)
	}
	wg.Wait()
	if got := head(t, r, sid); got != writers+writers*each {
		t.Fatalf("head %d, want %d", got, writers+writers*each)
	}
}

// Who a member is and whether it may write are the server's answer for the
// file (Options.Who: the signed-in account, filex's rules), never the
// joining caller's: a relay with no way to ask lets nobody in, and a member
// the rules make read-only cannot write whatever it would have claimed.
func TestJoin_TheMemberIsWhoTheServerSaysNotWhoTheCallerSays(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)}
	asked := map[string]int{}
	rules := func(ctx context.Context, doc string) (Identity, error) {
		asked[doc]++
		id, err := signedIn(ctx, doc)
		if err != nil {
			return id, err
		}
		// The file's rules: on "ro" nobody may write.
		if doc == "ro" {
			id.CanEdit = false
		}
		return id, nil
	}
	r := New(Options{Now: c.now, Who: rules})

	sid := readySession(t, r, "ro")
	h, err := r.Join(as(7, true), sid)
	if err != nil {
		t.Fatal(err)
	}
	if asked["ro"] != 1 {
		t.Fatalf("the relay asked the server about the session's own file: %v", asked)
	}
	if _, err := r.Append(sid, h.Client, AppendRequest{Kind: KindLock, Base: head(t, r, sid), Ctr: 1, CT: []byte("x")}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("a member the file's rules make read-only wrote: %v", err)
	}

	// Nobody signed in: no member.
	if _, err := r.Join(context.Background(), sid); !errors.Is(err, ErrNoIdentity) {
		t.Fatalf("an unknown caller joined: %v", err)
	}

	// A relay that cannot ask lets nobody in.
	bare := New(Options{Now: c.now})
	sid2 := readySession(t, bare, "d")
	if _, err := bare.Join(as(1, true), sid2); !errors.Is(err, ErrNoIdentity) {
		t.Fatalf("a relay without Who let a member in: %v", err)
	}
}
