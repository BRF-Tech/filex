package handlers_test

// Web Push over real HTTP, in a real two-tenant instance (task #191,
// internal/notify push.go, handlers/notification_push.go).
//
// A device receives what its person's bell tells them, so the things pinned
// here are the doors around it: a person manages only their own devices, the
// endpoint and the keys never come back, an API key (which may be confined to
// one folder) cannot register a device that would read the whole bell, another
// tenant's alert never reaches a device, a device removed is pushed nothing,
// and the instance's key is the platform operator's alone to rotate.
//
// ⚠ Red on the code before it: notify.PushConfig and the
// /api/notifications/push routes do not exist.

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/secretbox"
	"github.com/brf-tech/filex/backend/internal/webpush"
)

// phoneFarm is the browsers and their push service: each push is decrypted
// with its device's key and counted by device name.
type phoneFarm struct {
	*httptest.Server
	mu    sync.Mutex
	keys  map[string][2]any // name -> {*ecdh.PrivateKey, []byte auth}
	inbox map[string][]map[string]any
}

func newPhoneFarm(t *testing.T) *phoneFarm {
	t.Helper()
	pf := &phoneFarm{keys: map[string][2]any{}, inbox: map[string][]map[string]any{}}
	pf.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/p/")
		body, _ := io.ReadAll(r.Body)
		pf.mu.Lock()
		defer pf.mu.Unlock()
		k, ok := pf.keys[name]
		if !ok {
			w.WriteHeader(http.StatusGone)
			return
		}
		plain, err := webpush.Decrypt(body, k[0].(*ecdh.PrivateKey), k[1].([]byte))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var msg map[string]any
		_ = json.Unmarshal(plain, &msg)
		pf.inbox[name] = append(pf.inbox[name], msg)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(pf.Close)
	return pf
}

// subscription is what the browser called name posts (PushSubscription.toJSON
// and a label).
func (pf *phoneFarm) subscription(t *testing.T, name string) map[string]any {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	auth := make([]byte, 16)
	_, err = rand.Read(auth)
	require.NoError(t, err)
	pf.mu.Lock()
	pf.keys[name] = [2]any{priv, auth}
	pf.mu.Unlock()
	return map[string]any{
		"endpoint": pf.URL + "/p/" + name,
		"keys": map[string]string{
			"p256dh": base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()),
			"auth":   base64.RawURLEncoding.EncodeToString(auth),
		},
		"label": "Safari on iPhone",
	}
}

func (pf *phoneFarm) got(name string) []map[string]any {
	pf.mu.Lock()
	defer pf.mu.Unlock()
	return append([]map[string]any(nil), pf.inbox[name]...)
}

func newPushFix(t *testing.T) (*mtFix, *phoneFarm, notify.Pushes) {
	t.Helper()
	pf := newPhoneFarm(t)
	box, err := secretbox.New("handlers-push-secret")
	require.NoError(t, err)
	f := newMTFix(t, true, func(c *notify.Config) {
		c.Push = &notify.PushConfig{
			Box: box, Subject: "mailto:ops@example.com",
			Endpoints: webpush.Policy{Insecure: true}, Client: pf.Client(),
		}
	})
	p, ok := f.Notif.(notify.Pushes)
	require.True(t, ok)
	return f, pf, p
}

func TestPush_APersonManagesOnlyTheirOwnDevices(t *testing.T) {
	f, pf, p := newPushFix(t)
	ctx := context.Background()

	status, body := doJSON(t, f.A, http.MethodGet, f.URL+"/api/notifications/push", nil)
	require.Equal(t, http.StatusOK, status, "%v", body)
	require.Equal(t, true, body["available"], "%v", body)
	key, _ := body["public_key"].(string)
	raw, err := base64.RawURLEncoding.DecodeString(key)
	require.NoError(t, err)
	require.Len(t, raw, 65, "the key a browser subscribes with")
	require.Empty(t, body["devices"])

	sub := pf.subscription(t, "a-phone")
	status, dev := doJSON(t, f.A, http.MethodPost, f.URL+"/api/notifications/push/subscriptions", sub)
	require.Equal(t, http.StatusCreated, status, "%v", dev)
	require.Equal(t, "Safari on iPhone", dev["label"])
	require.Equal(t, webpush.EndpointHash(sub["endpoint"].(string)), dev["endpoint_hash"])
	for _, secret := range []string{"endpoint", "p256dh", "auth", "auth_secret", "Endpoint", "P256dh"} {
		require.NotContains(t, dev, secret, "a device's address or key came back")
	}
	devID := int64(dev["id"].(float64))

	_, mine := doJSON(t, f.A, http.MethodGet, f.URL+"/api/notifications/push", nil)
	require.Len(t, mine["devices"], 1)
	_, theirs := doJSON(t, f.B, http.MethodGet, f.URL+"/api/notifications/push", nil)
	require.Empty(t, theirs["devices"], "another person's device is in my list")

	// Somebody else's device is answered as one that does not exist.
	status, _ = doJSON(t, f.B, http.MethodDelete, f.URL+"/api/notifications/push/subscriptions/"+itoa64(devID), nil)
	require.Equal(t, http.StatusNotFound, status)
	status, _ = doJSON(t, f.B, http.MethodPost, f.URL+"/api/notifications/push/forget", map[string]string{"endpoint": sub["endpoint"].(string)})
	require.Equal(t, http.StatusOK, status)
	_, mine = doJSON(t, f.A, http.MethodGet, f.URL+"/api/notifications/push", nil)
	require.Len(t, mine["devices"], 1, "another person removed my device")

	// What A's bell tells them reaches the phone.
	uid := f.UserA
	_, err = f.Notif.Send(ctx, notify.Event{Event: notify.EventCommentAdded, Severity: notify.SeverityInfo,
		Title: "New comment", Node: &notify.NodeRef{StorageID: f.StA.ID, Path: "blog/yazi.md", Name: "yazi.md"}, UserID: &uid})
	require.NoError(t, err)
	n, err := p.FlushPush(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Len(t, pf.got("a-phone"), 1)

	// The test push, now.
	status, res := doJSON(t, f.A, http.MethodPost, f.URL+"/api/notifications/push/test", nil)
	require.Equal(t, http.StatusOK, status, "%v", res)
	require.EqualValues(t, 1, res["sent"])
	require.Len(t, pf.got("a-phone"), 2)

	// Removed: pushed nothing more.
	status, _ = doJSON(t, f.A, http.MethodDelete, f.URL+"/api/notifications/push/subscriptions/"+itoa64(devID), nil)
	require.Equal(t, http.StatusNoContent, status)
	_, err = f.Notif.Send(ctx, notify.Event{Event: notify.EventCommentAdded, Severity: notify.SeverityInfo,
		Title: "New comment", Node: &notify.NodeRef{StorageID: f.StA.ID, Path: "blog/yazi.md", Name: "yazi.md"}, UserID: &uid})
	require.NoError(t, err)
	_, err = p.FlushPush(ctx)
	require.NoError(t, err)
	require.Len(t, pf.got("a-phone"), 2, "a removed device was pushed")
}

func TestPush_AnAPIKeyCannotRegisterADevice(t *testing.T) {
	pf := newPhoneFarm(t)
	box, err := secretbox.New("handlers-push-secret")
	require.NoError(t, err)
	f := newMTFix(t, false, func(c *notify.Config) {
		c.Push = &notify.PushConfig{Box: box, Subject: "mailto:ops@example.com",
			Endpoints: webpush.Policy{Insecure: true}, Client: pf.Client()}
	})
	tok := issueToken(t, f.Store, f.UserA, fullScopes, nil)
	raw, err := json.Marshal(pf.subscription(t, "token-phone"))
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, f.URL+"/api/notifications/push/subscriptions", bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Filex-Token", tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Equal(t, "session_required", out["error"])
	n, err := f.Notif.(notify.Pushes).PushCount(context.Background())
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestPush_AnEndpointThatIsNotAPushServiceIsRefused(t *testing.T) {
	pf := newPhoneFarm(t)
	box, err := secretbox.New("handlers-push-secret")
	require.NoError(t, err)
	f := newMTFix(t, true, func(c *notify.Config) {
		c.Push = &notify.PushConfig{Box: box, Subject: "mailto:ops@example.com", Client: pf.Client()}
	})
	sub := pf.subscription(t, "x")
	for _, endpoint := range []string{pf.URL + "/p/x", "https://169.254.169.254/latest/meta-data", "https://intranet.example/x"} {
		sub["endpoint"] = endpoint
		status, body := doJSON(t, f.A, http.MethodPost, f.URL+"/api/notifications/push/subscriptions", sub)
		require.Equal(t, http.StatusBadRequest, status, "%s: %v", endpoint, body)
		require.Equal(t, "push_endpoint_refused", body["error"])
	}
}

// TestPush_AnotherTenantsAlertNeverReachesADevice: an antivirus alert in
// tenant bravo's storage reaches the platform operator's device and nobody's
// in tenant alpha.
func TestPush_AnotherTenantsAlertNeverReachesADevice(t *testing.T) {
	f, pf, p := newPushFix(t)
	ctx := context.Background()
	for name, c := range map[string]*http.Client{"alpha-member": f.A, "alpha-admin": f.AdminA, "operator": f.Super} {
		status, body := doJSON(t, c, http.MethodPost, f.URL+"/api/notifications/push/subscriptions", pf.subscription(t, name))
		require.Equal(t, http.StatusCreated, status, "%s: %v", name, body)
	}

	f.emitWorkerAV(t, f.StB.ID, "/gizli/bravo.exe")
	_, err := p.FlushPush(ctx)
	require.NoError(t, err)
	require.Empty(t, pf.got("alpha-member"), "another tenant's alert reached a member's phone")
	require.Empty(t, pf.got("alpha-admin"), "another tenant's alert reached its administrator's phone")
	require.Len(t, pf.got("operator"), 1, "the platform operator's bell takes every tenant's alert")
}

func TestPush_TheKeyIsThePlatformOperatorsToRotate(t *testing.T) {
	f, pf, _ := newPushFix(t)
	status, body := doJSON(t, f.A, http.MethodPost, f.URL+"/api/notifications/push/subscriptions", pf.subscription(t, "a"))
	require.Equal(t, http.StatusCreated, status, "%v", body)
	_, before := doJSON(t, f.A, http.MethodGet, f.URL+"/api/notifications/push", nil)

	status, _ = doJSON(t, f.AdminA, http.MethodPost, f.URL+"/api/admin/notifications/push/rotate", nil)
	require.Equal(t, http.StatusForbidden, status, "a tenant's administrator rotated the instance's key")
	status, _ = doJSON(t, f.A, http.MethodPost, f.URL+"/api/admin/notifications/push/rotate", nil)
	require.Equal(t, http.StatusForbidden, status)

	status, info := doJSON(t, f.Super, http.MethodGet, f.URL+"/api/admin/notifications/push", nil)
	require.Equal(t, http.StatusOK, status, "%v", info)
	require.EqualValues(t, 1, info["devices"])
	status, rotated := doJSON(t, f.Super, http.MethodPost, f.URL+"/api/admin/notifications/push/rotate", nil)
	require.Equal(t, http.StatusOK, status, "%v", rotated)
	require.EqualValues(t, 1, rotated["devices_forgotten"])

	_, after := doJSON(t, f.A, http.MethodGet, f.URL+"/api/notifications/push", nil)
	require.NotEqual(t, before["public_key"], after["public_key"])
	require.Empty(t, after["devices"], "a device subscribed with the old key outlived the rotation")
	require.NotContains(t, rotated, "private_key")
}
