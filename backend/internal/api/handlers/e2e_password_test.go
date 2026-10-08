package handlers_test

// wiring:e2 password — up to 0.53 the web UI announced a folder password
// change and this door turned the announcement into an audit row and the
// owner's notification, on the client's word alone (`via`, `rekey`, and no
// change needed at all). Since 0.54 the server records a change itself, from
// the key file it sees rewritten (e2e/slotchange); the door is kept for older
// clients, answers, and records nothing.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
)

// capturingSink records what the handler sends. Only Send is ever called.
type capturingSink struct {
	notify.Service
	got chan notify.Event
}

func (c *capturingSink) Send(_ context.Context, e notify.Event) (int64, error) {
	c.got <- e
	return 1, nil
}

func postPasswordChanged(t *testing.T, h *handlers.E2E, u *model.User, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	buf, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest("POST", "/api/files/e2e/password-changed", bytes.NewReader(buf))
	req = req.WithContext(auth.WithUser(req.Context(), u))
	rec := httptest.NewRecorder()
	h.PasswordChanged(rec, req)
	return rec
}

// A client's word is not a record: an announcement - even of a reset with the
// recovery key, which nothing happened to back - tells nobody and writes no
// audit row. Before 0.54 it told the owner (a warning) and audited it.
func TestE2ePasswordChanged_AnAnnouncementTellsNobodyAndRecordsNothing(t *testing.T) {
	fx, storageID := seedE2eTree(t)
	ctx := context.Background()

	owner, err := fx.store.CreateUser(ctx, "owner@example.com", "x", "user", "en", "UTC")
	require.NoError(t, err)
	other, err := fx.store.CreateUser(ctx, "other@example.com", "x", "user", "en", "UTC")
	require.NoError(t, err)
	kasa, err := fx.store.GetNodeByPath(ctx, storageID, e2eListPathHash(storageID, "/kasa"))
	require.NoError(t, err)
	require.NoError(t, fx.store.SetNodeOwner(ctx, kasa.ID, &owner.ID))

	sink := &capturingSink{got: make(chan notify.Event, 4)}
	handlers.SetNotifySink(sink)
	t.Cleanup(func() { handlers.SetNotifySink(nil) })
	h := handlers.NewE2E(fx.store, nil)

	// An older client still calls it after a password change: answered, so it
	// shows no error.
	rec := postPasswordChanged(t, h, other, map[string]any{"path": "alpha://kasa/alt", "via": "recovery_key", "rekey": true})
	require.Equal(t, 200, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"ok":true}`, rec.Body.String())

	select {
	case ev := <-sink.got:
		t.Fatalf("an announcement must tell nobody, got %v", ev.Event)
	case <-time.After(300 * time.Millisecond):
	}
	rows, err := fx.store.ListAuditRecent(ctx, 10)
	require.NoError(t, err)
	for _, r := range rows {
		assert.NotEqual(t, "e2e.password_change", r.Action, "an announcement writes no audit row")
	}
}

func TestE2ePasswordChanged_RefusesWhatIsNotAnEncryptedFolder(t *testing.T) {
	fx, _ := seedE2eTree(t)
	sink := &capturingSink{got: make(chan notify.Event, 4)}
	handlers.SetNotifySink(sink)
	t.Cleanup(func() { handlers.SetNotifySink(nil) })
	h := handlers.NewE2E(fx.store, nil)
	u := &model.User{ID: 1, Email: "a@example.com"}

	rec := postPasswordChanged(t, h, u, map[string]any{"path": "alpha://acik", "via": "password"})
	assert.Equal(t, 400, rec.Code, rec.Body.String())
	rec = postPasswordChanged(t, h, u, map[string]any{"path": "alpha://kasa", "via": "telepathy"})
	assert.Equal(t, 400, rec.Code, rec.Body.String())
	select {
	case ev := <-sink.got:
		t.Fatalf("a refused announcement must notify nobody, got %v", ev.Event)
	case <-time.After(300 * time.Millisecond):
	}
}
