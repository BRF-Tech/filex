package handlers

// The ONE reading of "can the person this link acts as still stand behind it?"
//
// PublicAPI.linkCreator is consulted from three places that each used to have
// no opinion at all: the state the public shell renders (describe), the
// surface a visitor presses buttons on (loadApp) and the job those presses
// queue (enqueueAsCreator). Two of those three are gated by the first, so
// their copies of the question cannot be driven apart through HTTP -- which
// is exactly why the question is measured HERE, on its own, where every
// branch is reachable and each one can be made red by itself.
//
// ⚠ An internal test (package handlers), not an HTTP one, and it may not use
// internal/testutil: testutil imports internal/api, which imports this
// package, so an in-package test that reached for it would not compile. The
// stub below is the whole dependency instead -- and it is deliberately a db
// store with NOTHING wired but GetUser, so that a future edit which makes
// linkCreator read the node, the storage or the grant table fails here loudly
// rather than growing a quiet second query on every public page load.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// oneUserStore answers GetUser and nothing else. The embedded nil db.Store
// means any other call panics -- see the note above: that is the assertion,
// not an accident.
type oneUserStore struct {
	db.Store
	u   *model.User
	err error
}

func (s oneUserStore) GetUser(context.Context, int64) (*model.User, error) { return s.u, s.err }

func TestLinkCreator_AnswersForEveryWayALinkCanLoseThePersonBehindIt(t *testing.T) {
	id := int64(7)
	enabled := &model.User{ID: id, Role: model.RoleUser, Enabled: true}
	disabled := &model.User{ID: id, Role: model.RoleUser, Enabled: false}

	cases := []struct {
		name  string
		store db.Store
		share *model.Share
		live  bool
		// nobody marks the one live answer that hands back no person: the
		// link is not stopped, but there is nobody to run a job as.
		nobody bool
		why    string
	}{
		{
			name: "an account in good standing", store: oneUserStore{u: enabled},
			share: &model.Share{CreatedBy: &id}, live: true,
			why: "the ordinary case: the link works because the person behind it still does",
		},
		{
			name: "an account switched off", store: oneUserStore{u: disabled},
			share: &model.Share{CreatedBy: &id}, live: false,
			why: "the owner's decision: disabling an account closes the doors it left open, " +
				"even though Enabled is not a soft delete and the grants are untouched",
		},
		{
			name: "an account that is gone", store: oneUserStore{u: nil},
			share: &model.Share{CreatedBy: &id}, live: false,
			why: "the rights the job would spend went with the row",
		},
		{
			name: "a store that cannot answer", store: oneUserStore{err: errors.New("db down")},
			share: &model.Share{CreatedBy: &id}, live: false,
			why: "fails CLOSED: an outage must not read as permission on an anonymous door",
		},
		{
			name: "a link with nobody behind it", store: oneUserStore{u: enabled},
			share: &model.Share{}, live: true, nobody: true,
			why: "created_by is null -- an app's scheduled tick runs with no actor, so a link " +
				"it minted to deliver a document never had an account behind it. Nobody has " +
				"left, so nothing is closed; the JOB door refuses it separately, because a job " +
				"needs an ACL to run under and this link has none",
		},
		{
			name: "no share at all", store: oneUserStore{u: enabled},
			share: nil, live: false,
			why: "nil in, closed out -- callers must not have to guard this themselves",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/public/s/tok", nil)
			got, live := linkCreator(r.Context(), tc.store, tc.share)
			if live != tc.live {
				t.Fatalf("linkCreator live = %v, want %v -- %s", live, tc.live, tc.why)
			}
			if live && got == nil && !tc.nobody {
				t.Fatal("a live answer must hand back the person, which is who the job then runs as")
			}
			if tc.nobody && got != nil {
				t.Fatal("a link that names nobody must hand back nobody, or the job door has somebody to act as")
			}
			if !live && got != nil {
				t.Fatal("a refused answer must hand back nobody, so a caller that ignores the bool cannot act as them")
			}
		})
	}
}

// ⭐ A store that is never reached at all. h.Store nil is not a hypothetical:
// PublicAPI is assembled field by field (NewPublicAPI + the Attach* calls),
// and a public door that panicked on a half-wired handler would be a crash
// reachable without any credential.
func TestLinkCreator_WithNoStoreWiredRefusesRatherThanPanics(t *testing.T) {
	id := int64(7)
	if u, live := linkCreator(context.Background(), nil, &model.Share{CreatedBy: &id}); live || u != nil {
		t.Fatal("an unwired handler must refuse, not claim the link is good")
	}
}
