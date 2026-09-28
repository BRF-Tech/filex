package handlers_test

// wiring:e2 password — the web UI announces a folder password change once the
// new key file is written; the server records it in the audit log and tells
// the folder's OWNER (e2e.password_changed), who may not be the person who
// changed it. Not a gate — the server never sees a password — but not free
// either: the path must be an encrypted folder.

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

func TestE2ePasswordChanged_AuditsAndTellsTheOwner(t *testing.T) {
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

	// A path INSIDE the folder names the folder it sits in.
	rec := postPasswordChanged(t, h, other, map[string]any{"path": "alpha://kasa/alt", "via": "recovery_key", "rekey": true})
	require.Equal(t, 200, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"ok":true,"notified":true}`, rec.Body.String())

	select {
	case ev := <-sink.got:
		assert.Equal(t, notify.EventE2EPasswordChanged, ev.Event)
		assert.Equal(t, notify.SeverityWarning, ev.Severity, "a reset with the recovery key is a warning")
		require.NotNil(t, ev.UserID)
		assert.Equal(t, owner.ID, *ev.UserID, "the OWNER is told, not the person who changed it")
		assert.Equal(t, "kasa", ev.Meta["folder"])
		assert.Equal(t, "recovery_key", ev.Meta["via"])
		assert.Equal(t, true, ev.Meta["rekey"])
		assert.Equal(t, "other@example.com", ev.Meta["actor_email"])
	case <-time.After(5 * time.Second):
		t.Fatal("no notification was sent")
	}

	rows, err := fx.store.ListAuditRecent(ctx, 10)
	require.NoError(t, err)
	var found *model.AuditEntry
	for _, r := range rows {
		if r.Action == "e2e.password_change" {
			found = r
		}
	}
	require.NotNil(t, found, "the change is in the audit log")
	require.NotNil(t, found.UserID)
	assert.Equal(t, other.ID, *found.UserID)
	assert.Equal(t, "recovery_key", found.Metadata["via"])
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
