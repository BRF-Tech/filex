package cliclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The vault API client against a stub of docs/E2E-VAULT-FORMAT.md → "API".

func vaultTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(Conn{URL: srv.URL, Token: "tok"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func TestVaultState_ReadsTheLockAndBothTimeForms(t *testing.T) {
	c := vaultTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/files/e2e/vault/state", r.URL.Path)
		require.Equal(t, "docs://Kasa", r.URL.Query().Get("path"))
		require.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, `{"vault_id":"wpU155hSR3hD1u8bSnGv7w","pack_log2":22,"generation":7,
			"lock":{"holder":{"name":"Ayşe","client":"web","label":"Firefox, ofis"},"since":"2026-10-06T09:00:00Z","expires_at":1791277260000,"mine":false}}`)
	})
	st, err := c.VaultState(context.Background(), "docs://Kasa/")
	require.NoError(t, err)
	require.Equal(t, uint64(7), st.Generation)
	require.Equal(t, 22, st.PackLog2)
	require.NotNil(t, st.Lock)
	require.Equal(t, "Ayşe (web, Firefox, ofis)", st.Lock.Holder.String())
	require.True(t, st.Lock.Since.Equal(time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)))
	require.Equal(t, int64(1791277260000), st.Lock.ExpiresAt.UnixMilli())

	c = vaultTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"vault_id":"x","pack_log2":22,"generation":0,"lock":null}`)
	})
	st, err = c.VaultState(context.Background(), "docs://Kasa")
	require.NoError(t, err)
	require.Nil(t, st.Lock)

	_, err = c.VaultState(context.Background(), "docs://")
	require.Error(t, err, "a storage root is not a vault")
}

func TestVaultLock_SomeoneElseHoldsIt(t *testing.T) {
	c := vaultTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/files/e2e/vault/lock", r.URL.Path)
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, map[string]string{"path": "docs://Kasa", "client": "mount", "label": "laptop"}, body)
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "VAULT_LOCKED", "message": "Ayşe is writing in this vault.",
			"holder": map[string]string{"name": "Ayşe", "client": "web"}, "since": "2026-10-06T09:00:00Z", "retry_after": 42,
		})
	})
	_, err := c.VaultLock(context.Background(), "docs://Kasa", VaultClientMount, "laptop")
	require.True(t, IsVaultCode(err, VaultCodeLocked))
	var ve *VaultError
	require.ErrorAs(t, err, &ve)
	require.Equal(t, http.StatusConflict, ve.Status)
	require.Equal(t, "Ayşe", ve.Holder.Name)
	require.Equal(t, 42, ve.RetryAfter)
	require.Contains(t, ve.Error(), "Ayşe is writing")
}

func TestVaultLock_TakesItAndRenewsWithTheToken(t *testing.T) {
	var renewed []map[string]any
	c := vaultTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/files/e2e/vault/lock":
			require.Empty(t, r.Header.Get(VaultLockHeader))
			writeJSON(w, http.StatusOK, map[string]any{"token": "T0K", "generation": 3, "lease_seconds": 60, "idle_seconds": 180, "expires_at": "2026-10-06T09:01:00Z"})
		case "/api/files/e2e/vault/lock/renew":
			require.Equal(t, "T0K", r.Header.Get(VaultLockHeader))
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			renewed = append(renewed, body)
			if len(renewed) == 2 {
				writeJSON(w, http.StatusConflict, map[string]any{"error": "VAULT_LOCK_LOST", "message": "idle", "reason": "idle"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"expires_at": "2026-10-06T09:02:00Z", "idle_until": "2026-10-06T09:04:00Z"})
		case "/api/files/e2e/vault/lock/release":
			require.Equal(t, "T0K", r.Header.Get(VaultLockHeader))
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected %s", r.URL.Path)
		}
	})
	ctx := context.Background()
	l, err := c.VaultLock(ctx, "docs://Kasa", VaultClientCLI, "")
	require.NoError(t, err)
	require.Equal(t, "T0K", l.Token)
	require.Equal(t, uint64(3), l.Generation)
	require.Equal(t, 180, l.IdleSeconds)

	_, err = c.VaultRenew(ctx, "docs://Kasa", l.Token, true)
	require.NoError(t, err)
	_, err = c.VaultRenew(ctx, "docs://Kasa", l.Token, false)
	require.True(t, IsVaultCode(err, VaultCodeLockLost))
	var ve *VaultError
	require.ErrorAs(t, err, &ve)
	require.Equal(t, "idle", ve.Reason)
	require.Equal(t, true, renewed[0]["active"])
	require.Equal(t, false, renewed[1]["active"])
	require.NoError(t, c.VaultRelease(ctx, "docs://Kasa", l.Token))
}

func TestVaultPutPackAndIndex(t *testing.T) {
	pack := bytes.Repeat([]byte{0x5a}, 1<<16)
	var gotPack []byte
	c := vaultTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPut, r.Method)
		require.Equal(t, "L", r.Header.Get(VaultLockHeader))
		require.Equal(t, "docs://Kasa", r.URL.Query().Get("path"))
		switch r.URL.Path {
		case "/api/files/e2e/vault/pack":
			require.Equal(t, "b067d7bcd62c9f817216a5ef1b1b653b", r.URL.Query().Get("id"))
			gotPack, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
		case "/api/files/e2e/vault/index":
			if r.URL.Query().Get("generation") == "9" {
				writeJSON(w, http.StatusConflict, map[string]any{"error": "VAULT_GENERATION", "message": "not latest + 1", "latest": 4})
				return
			}
			require.Equal(t, "5", r.URL.Query().Get("generation"))
			writeJSON(w, http.StatusCreated, map[string]any{"generation": 5})
		}
	})
	ctx := context.Background()
	require.NoError(t, c.VaultPutPack(ctx, "docs://Kasa", "L", "b067d7bcd62c9f817216a5ef1b1b653b", pack))
	require.Equal(t, pack, gotPack)
	g, err := c.VaultPutIndex(ctx, "docs://Kasa", "L", 5, make([]byte, 65536))
	require.NoError(t, err)
	require.Equal(t, uint64(5), g)
	_, err = c.VaultPutIndex(ctx, "docs://Kasa", "L", 9, make([]byte, 65536))
	var ve *VaultError
	require.ErrorAs(t, err, &ve)
	require.Equal(t, VaultCodeGeneration, ve.Code)
	require.Equal(t, uint64(4), ve.Latest)
}

func TestVaultListAll_FollowsTheCursor(t *testing.T) {
	var afters []string
	c := vaultTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "pack", r.URL.Query().Get("kind"))
		after := r.URL.Query().Get("after")
		afters = append(afters, after)
		if after == "" {
			_, _ = io.WriteString(w, `{"items":[{"id":"aa","size":65536,"mtime":1791277200000}],"next":"aa"}`)
			return
		}
		_, _ = io.WriteString(w, `{"items":[{"id":"bb","size":65536,"mtime":"2026-10-06T09:00:00Z"}],"next":null}`)
	})
	items, err := c.VaultListAll(context.Background(), "docs://Kasa", "pack")
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, []string{"", "aa"}, afters)
	require.Equal(t, "bb", items[1].ID)
	require.Equal(t, int64(1791277200000), items[0].MTime.UnixMilli())
}

func TestVaultDelete_SendsPacksAndGenerations(t *testing.T) {
	c := vaultTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/files/e2e/vault/delete", r.URL.Path)
		require.Equal(t, "L", r.Header.Get(VaultLockHeader))
		var body struct {
			Path    string   `json:"path"`
			Packs   []string `json:"packs"`
			Indexes []uint64 `json:"indexes"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, []string{"aa", "bb"}, body.Packs)
		require.Equal(t, []uint64{1}, body.Indexes)
		writeJSON(w, http.StatusOK, map[string]any{"deleted": map[string]any{"packs": 2, "indexes": []uint64{1}}})
	})
	np, ni, err := c.VaultDelete(context.Background(), "docs://Kasa", "L", []string{"aa", "bb"}, []uint64{1})
	require.NoError(t, err)
	require.Equal(t, 2, np)
	require.Equal(t, 1, ni)
}

func TestReadWhole_RefusesMoreThanItsLimit(t *testing.T) {
	d := &downloadServer{body: bytes.Repeat([]byte("x"), 2048)}
	srv := httptest.NewServer(d.handler(t))
	t.Cleanup(srv.Close)
	c := New(Conn{URL: srv.URL, Token: "tok"})
	_, err := c.ReadWhole(context.Background(), "docs://Kasa/v/idx/0000000000000001.fxi", 1024)
	require.ErrorIs(t, err, ErrObjectTooLarge)
	b, err := c.ReadWhole(context.Background(), "docs://Kasa/v/idx/0000000000000001.fxi", 4096)
	require.NoError(t, err)
	require.Len(t, b, 2048)
}
