package handlers_test

// A single encrypted file (`.fxe`, docs/E2E-ENCRYPTION.md → "Single encrypted
// files") carries its own escrow slot, and opening it with the escrow key is
// the same event as opening a folder with it: the web UI proves it holds the
// key (challenge → nonce) BEFORE it opens anything, and the file's OWNER is
// told. The password-change announcement is the same announcement for a file
// as for a folder.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
)

// seedFxeFile adds catalogue rows for a `.fxe` at the storage root, a plain
// file, and a DIRECTORY whose name merely ends in .fxe — beside the encrypted
// folder /kasa of seedE2eTree.
func seedFxeFile(t *testing.T) (*twoStorageFixture, int64) {
	t.Helper()
	fx, storageID := seedE2eTree(t)
	ctx := context.Background()
	mk := func(name string, typ model.NodeType) *model.Node {
		n, err := fx.store.CreateNode(ctx, &model.Node{
			StorageID: storageID,
			Name:      name,
			Path:      "/" + name,
			PathHash:  e2eListPathHash(storageID, "/"+name),
			Type:      typ,
			Size:      64,
			Mime:      "application/octet-stream",
			Etag:      "e-" + name,
		})
		require.NoError(t, err)
		return n
	}
	mk("Rapor 2027.pdf.fxe", model.NodeTypeFile)
	mk("duz.txt", model.NodeTypeFile)
	mk("klasor.fxe", model.NodeTypeDirectory)
	return fx, storageID
}

// setFxeOwner records owner as the owner of the seeded .fxe.
func setFxeOwner(t *testing.T, fx *twoStorageFixture, owner *model.User) *model.Node {
	t.Helper()
	ctx := context.Background()
	n, err := fx.store.GetNodeByPath(ctx, fx.stA.ID, e2eListPathHash(fx.stA.ID, "/Rapor 2027.pdf.fxe"))
	require.NoError(t, err)
	require.NoError(t, fx.store.SetNodeOwner(ctx, n.ID, &owner.ID))
	return n
}

func postE2E(t *testing.T, fn func(w *httptest.ResponseRecorder, body []byte, u *model.User), u *model.User, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	buf, err := json.Marshal(body)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	fn(rec, buf, u)
	return rec
}

func escrowCall(h *handlers.E2E, used bool) func(w *httptest.ResponseRecorder, body []byte, u *model.User) {
	return func(w *httptest.ResponseRecorder, body []byte, u *model.User) {
		target := "/api/files/e2e/escrow/challenge"
		if used {
			target = "/api/files/e2e/escrow/used"
		}
		req := httptest.NewRequest("POST", target, bytes.NewReader(body))
		req = req.WithContext(auth.WithUser(req.Context(), u))
		if used {
			h.EscrowUsed(w, req)
		} else {
			h.EscrowChallenge(w, req)
		}
	}
}

// openChallenge answers a challenge the way the browser does: RSA-OAEP with
// the escrow private key.
func openChallenge(t *testing.T, pkcs8B64, challengeB64 string) string {
	t.Helper()
	der, err := base64.StdEncoding.DecodeString(pkcs8B64)
	require.NoError(t, err)
	anyPriv, err := x509.ParsePKCS8PrivateKey(der)
	require.NoError(t, err)
	sealed, err := base64.StdEncoding.DecodeString(challengeB64)
	require.NoError(t, err)
	nonce, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, anyPriv.(*rsa.PrivateKey), sealed, nil)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(nonce)
}

// escrowKeyPair is a throwaway installation escrow key: the public half for
// the handler, the private half for the test playing the operator.
func escrowKeyPair(t *testing.T) (pkcs8 string, key *e2e.EscrowKey) {
	t.Helper()
	spki, pkcs8, err := e2e.GenerateEscrowKeyPair(2048)
	require.NoError(t, err)
	key, err = e2e.ParseEscrowPublicKey(spki)
	require.NoError(t, err)
	return pkcs8, key
}

func TestE2eEscrow_ASingleEncryptedFileIsAnnouncedToItsOwner(t *testing.T) {
	ctx := context.Background()
	fx, _ := seedFxeFile(t)
	owner, err := fx.store.CreateUser(ctx, "owner@example.com", "x", "user", "en", "UTC")
	require.NoError(t, err)
	operator, err := fx.store.CreateUser(ctx, "operator@example.com", "x", "admin", "en", "UTC")
	require.NoError(t, err)
	setFxeOwner(t, fx, owner)

	pkcs8, key := escrowKeyPair(t)
	sink := &capturingSink{got: make(chan notify.Event, 4)}
	handlers.SetNotifySink(sink)
	t.Cleanup(func() { handlers.SetNotifySink(nil) })
	h := handlers.NewE2E(fx.store, key)

	const file = "alpha://Rapor 2027.pdf.fxe"
	rec := postE2E(t, escrowCall(h, false), operator, map[string]any{"path": file})
	require.Equal(t, 200, rec.Code, "a .fxe is something the escrow key opens: %s", rec.Body.String())
	var ch struct{ ID, Challenge, Kid string }
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &ch))
	assert.Equal(t, key.KID, ch.Kid)

	rec = postE2E(t, escrowCall(h, true), operator, map[string]any{"path": file, "id": ch.ID, "nonce": openChallenge(t, pkcs8, ch.Challenge)})
	require.Equal(t, 200, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"ok":true,"notified":true}`, rec.Body.String())

	select {
	case ev := <-sink.got:
		assert.Equal(t, notify.EventE2EEscrowUsed, ev.Event)
		assert.Equal(t, notify.SeverityWarning, ev.Severity)
		// The file's wording, said by the server from the facts (notify say.go).
		assert.Equal(t, "Encrypted file opened with the escrow key", notify.SayEvent("en", ev).Title)
		require.NotNil(t, ev.UserID)
		assert.Equal(t, owner.ID, *ev.UserID, "the file's OWNER is told, not the operator")
		assert.Equal(t, "Rapor 2027.pdf.fxe", ev.Meta["file"])
		assert.Equal(t, "file", ev.Meta["kind"])
		assert.Equal(t, "operator@example.com", ev.Meta["actor_email"])
		assert.Equal(t, notify.FileTarget("Rapor 2027.pdf.fxe"), ev.Target, "the notification points at the file")
	case <-time.After(5 * time.Second):
		t.Fatal("no notification was sent")
	}
}

func TestE2eEscrow_WhatIsNotAnEncryptedFileIsRefused(t *testing.T) {
	ctx := context.Background()
	fx, _ := seedFxeFile(t)
	operator, err := fx.store.CreateUser(ctx, "operator@example.com", "x", "admin", "en", "UTC")
	require.NoError(t, err)
	pkcs8, key := escrowKeyPair(t)
	sink := &capturingSink{got: make(chan notify.Event, 4)}
	handlers.SetNotifySink(sink)
	t.Cleanup(func() { handlers.SetNotifySink(nil) })
	h := handlers.NewE2E(fx.store, key)

	for _, p := range []string{"alpha://duz.txt", "alpha://klasor.fxe", "alpha://yok.fxe"} {
		rec := postE2E(t, escrowCall(h, false), operator, map[string]any{"path": p})
		assert.Equal(t, 400, rec.Code, "%s: %s", p, rec.Body.String())
	}

	// A challenge minted for the file cannot be spent announcing the folder,
	// nor one minted for the folder on the file.
	rec := postE2E(t, escrowCall(h, false), operator, map[string]any{"path": "alpha://Rapor 2027.pdf.fxe"})
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var ch struct{ ID, Challenge string }
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &ch))
	rec = postE2E(t, escrowCall(h, true), operator, map[string]any{"path": "alpha://kasa", "id": ch.ID, "nonce": openChallenge(t, pkcs8, ch.Challenge)})
	assert.Equal(t, 400, rec.Code, rec.Body.String())

	rec = postE2E(t, escrowCall(h, false), operator, map[string]any{"path": "alpha://kasa"})
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &ch))
	rec = postE2E(t, escrowCall(h, true), operator, map[string]any{"path": "alpha://Rapor 2027.pdf.fxe", "id": ch.ID, "nonce": openChallenge(t, pkcs8, ch.Challenge)})
	assert.Equal(t, 400, rec.Code, rec.Body.String())

	select {
	case ev := <-sink.got:
		t.Fatalf("a refused report must notify nobody, got %v", ev.Event)
	case <-time.After(300 * time.Millisecond):
	}
}
