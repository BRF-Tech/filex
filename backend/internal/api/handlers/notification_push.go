// Package handlers — notification_push.go
//
// Web Push's HTTP half (task #191, internal/notify push.go):
//
//	GET    /api/notifications/push                     — {available, reason, public_key, devices}
//	POST   /api/notifications/push/subscriptions       — {endpoint, keys: {p256dh, auth}, label}: this browser
//	DELETE /api/notifications/push/subscriptions/{id}  — one of the caller's devices
//	POST   /api/notifications/push/forget              — {endpoint}: this browser, turning push off or signing out
//	POST   /api/notifications/push/test                — a test push to every device of the caller, now
//	GET    /api/admin/notifications/push               — the instance's key and how many devices there are
//	POST   /api/admin/notifications/push/rotate        — a new key; every device is forgotten
//
// ⚠ A person's own, from a browser signed in as them: an API key is refused
// (sessionOnly). A device receives the person's WHOLE bell, so a key confined
// to one folder that could register one would read past its folder.
//
// ⚠ The endpoint is an address the person sends; notify refuses anything but
// a browser's push service before it is stored (webpush.Policy), and the
// endpoint and the keys never come back in an answer - a device is named by
// the hex SHA-256 of its endpoint, which the client computes the same way.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
)

// pushBodyLimit bounds a subscription body: an endpoint (at most 2048), two
// keys and a label.
const pushBodyLimit = 8 << 10

// pushes is the service's push half, nil when it has none (a test double).
func (h *Notifications) pushes() notify.Pushes {
	if p, ok := h.Service.(notify.Pushes); ok {
		return p
	}
	return nil
}

// pushCaller is the person a push request is about, and the push half - or
// false, with the answer written: nobody signed in (401), an API key (403
// session_required), notifications offline (503).
//
// ⚠ Who is asking is answered before whether the service is up: an API key
// is refused the same way on every server, and a 503 never tells a caller
// that has no business here that it might retry (token_verbs_test.go).
func (h *Notifications) pushCaller(w http.ResponseWriter, r *http.Request) (*model.User, notify.Pushes, bool) {
	user := auth.UserFrom(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return nil, nil, false
	}
	if !sessionOnly(w, r, "Push notifications belong to a browser signed in as you; an API key cannot manage them.", nil) {
		return nil, nil, false
	}
	if h.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notifications offline"})
		return nil, nil, false
	}
	p := h.pushes()
	if p == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "push_unavailable", "reason": notify.PushOffDisabled})
		return nil, nil, false
	}
	return user, p, true
}

// pushOff writes a 409 for a server where push does not work, and reports
// whether err was that.
func pushOff(w http.ResponseWriter, err error) bool {
	var off *notify.PushOff
	if !errors.As(err, &off) {
		return false
	}
	writeJSON(w, http.StatusConflict, map[string]string{"error": "push_unavailable", "reason": off.Reason})
	return true
}

// pushState is GET /api/notifications/push's answer.
func pushState(ctx context.Context, p notify.Pushes, userID int64) (map[string]any, error) {
	devices, err := p.PushDevices(ctx, userID)
	if err != nil {
		return nil, err
	}
	info := p.PushInfo(ctx)
	return map[string]any{
		"available":  info.Available,
		"reason":     info.Reason,
		"public_key": info.PublicKey,
		"devices":    devices,
	}, nil
}

// PushStatus answers whether push works here, the key a browser subscribes
// with, and the caller's devices.
//
//	GET /api/notifications/push
func (h *Notifications) PushStatus(w http.ResponseWriter, r *http.Request) {
	user, p, ok := h.pushCaller(w, r)
	if !ok {
		return
	}
	out, err := pushState(r.Context(), p, user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// PushSubscribe records this browser as one of the caller's devices.
//
//	POST /api/notifications/push/subscriptions
//	body: {endpoint, keys: {p256dh, auth}, label} - PushSubscription.toJSON() and a name
func (h *Notifications) PushSubscribe(w http.ResponseWriter, r *http.Request) {
	user, p, ok := h.pushCaller(w, r)
	if !ok {
		return
	}
	var body struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, pushBodyLimit)).Decode(&body); err != nil || body.Endpoint == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "message": "endpoint and keys are required"})
		return
	}
	dev, err := p.PushSubscribe(r.Context(), user.ID, notify.PushSubscriptionInput{
		Endpoint: body.Endpoint, P256dh: body.Keys.P256dh, Auth: body.Keys.Auth, Label: body.Label,
	})
	if err != nil {
		if pushOff(w, err) {
			return
		}
		var ref *notify.PushRefusal
		if errors.As(err, &ref) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": ref.Code, "message": ref.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, dev)
}

// PushRemove forgets one of the caller's devices.
//
//	DELETE /api/notifications/push/subscriptions/{id}
func (h *Notifications) PushRemove(w http.ResponseWriter, r *http.Request) {
	user, p, ok := h.pushCaller(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	removed, err := p.PushForget(r.Context(), user.ID, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !removed {
		// Somebody else's device is answered as one that does not exist.
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PushForget forgets this browser for the caller (by its endpoint): push
// turned off on it, or the person signing out of it.
//
//	POST /api/notifications/push/forget
//	body: {endpoint}
func (h *Notifications) PushForget(w http.ResponseWriter, r *http.Request) {
	user, p, ok := h.pushCaller(w, r)
	if !ok {
		return
	}
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, pushBodyLimit)).Decode(&body); err != nil || body.Endpoint == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "message": "endpoint is required"})
		return
	}
	removed, err := p.PushForgetEndpoint(r.Context(), user.ID, body.Endpoint)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"removed": removed})
}

// PushTest pushes a test to every device of the caller, now.
//
//	POST /api/notifications/push/test
func (h *Notifications) PushTest(w http.ResponseWriter, r *http.Request) {
	user, p, ok := h.pushCaller(w, r)
	if !ok {
		return
	}
	res, err := p.PushTest(r.Context(), user.ID)
	if err != nil {
		if pushOff(w, err) {
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// adminPushes is the push half for the instance's operator: the key is the
// instance's, so a tenant's administrator is refused (requireSupertenant).
func (h *Notifications) adminPushes(w http.ResponseWriter, r *http.Request) (notify.Pushes, bool) {
	if !requireSupertenant(w, r, "the push key is the instance's; only the platform's administrators manage it") {
		return nil, false
	}
	p := h.pushes()
	if p == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "push_unavailable", "reason": notify.PushOffDisabled})
		return nil, false
	}
	return p, true
}

// AdminPush answers the instance's push key and how many devices there are.
//
//	GET /api/admin/notifications/push
func (h *Notifications) AdminPush(w http.ResponseWriter, r *http.Request) {
	p, ok := h.adminPushes(w, r)
	if !ok {
		return
	}
	n, err := p.PushCount(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"push": p.PushInfo(r.Context()), "devices": n})
}

// AdminPushRotate replaces the instance's push key. Every device is
// forgotten (a subscription is bound to the key it was made with); each one
// subscribes again the next time filex opens on it.
//
//	POST /api/admin/notifications/push/rotate
func (h *Notifications) AdminPushRotate(w http.ResponseWriter, r *http.Request) {
	p, ok := h.adminPushes(w, r)
	if !ok {
		return
	}
	if !sessionOnly(w, r, "Rotating the push key needs an administrator signed in to the admin panel; an API key cannot do it.", nil) {
		return
	}
	info, dropped, err := p.RotatePushKey(r.Context())
	if err != nil {
		if pushOff(w, err) {
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"push": info, "devices_forgotten": dropped})
}
