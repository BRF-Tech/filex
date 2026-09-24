package handlers_test

// Every row of GET /api/admin/shares says WHETHER a PIN is set, and never
// what it is.
//
// 2026-09-20, the product owner on the admin Shares page: "paylaşımlar
// sayfasında sadece token'ı kopyala diye bir şeye gerek yok, pin varsa pini
// kopyala çıkmalı." The row now says in words that a link is PIN-protected —
// and it offers NO "copy PIN", because there is nothing to copy: the PIN goes
// in through bcrypt (internal/share/service.go Create) and model.Share tags
// PinHash `json:"-"`. A future hand that "helpfully" serialises the hash to
// make a copy-PIN button possible would hand every admin listing a password
// hash to crack offline, so both halves are pinned here: `has_pin` per row is
// the contract the page draws from, and neither the hash nor the PIN ever
// appears in the body.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestAdminShares_RowSaysAPinIsSet_ButNeverSendsIt(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	ctx := context.Background()
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "paylasim", Driver: "local", MountPath: "/paylasim",
		ConfigJSON: []byte(`{"path":"` + jsonPath(t.TempDir()) + `"}`),
		SyncMode:   model.SyncModeOnDemand, Enabled: true,
	})
	require.NoError(t, err)
	n, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, Name: "teklif.pdf", Path: "teklif.pdf", Type: model.NodeTypeFile,
		PathHash: pathkey.Hash(st.ID, "teklif.pdf"), SeenAt: time.Now(),
	})
	require.NoError(t, err)

	const plainPIN = "834595"
	hash, err := bcrypt.GenerateFromPassword([]byte(plainPIN), bcrypt.MinCost)
	require.NoError(t, err)

	_, err = store.CreateShare(ctx, &model.Share{
		NodeID: n.ID, Token: "tok-pin-yes", Kind: model.ShareKindDownload,
		PinHash: string(hash),
	})
	require.NoError(t, err)
	_, err = store.CreateShare(ctx, &model.Share{
		NodeID: n.ID, Token: "tok-pin-no", Kind: model.ShareKindDownload,
	})
	require.NoError(t, err)

	resp, err := client.Get(srv.URL + "/api/admin/shares?limit=10")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var body struct {
		Items []struct {
			Share struct {
				Token  string `json:"token"`
				HasPin bool   `json:"has_pin"`
			} `json:"share"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))

	got := map[string]bool{}
	for _, it := range body.Items {
		got[it.Share.Token] = it.Share.HasPin
	}
	require.Len(t, got, 2)
	require.True(t, got["tok-pin-yes"], "the row of a PIN-protected link must say so; the page has no other way to know")
	require.False(t, got["tok-pin-no"], "a link with no PIN must not be drawn as PIN-protected")

	text := string(raw)
	require.NotContains(t, text, "pin_hash", "the listing must never carry the PIN column, under any name")
	require.NotContains(t, text, string(hash), "a bcrypt hash in an admin listing is a hash handed out to crack offline")
	require.NotContains(t, text, plainPIN, "the PIN itself is unrecoverable by design — nothing here may reintroduce it")
	require.False(t, strings.Contains(strings.ToLower(text), `"pin":`), "no field named pin, however spelled")
}
