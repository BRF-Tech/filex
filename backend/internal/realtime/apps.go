package realtime

import "encoding/json"

// ── Instance-wide notices: an app's approved version changed ────────────
//
// Rooms and watches carry what happens in folders. An app's version is not in
// any folder: when an administrator approves a newer version (or goes back to
// the one before), every open explorer hears `app.updated` so an interface
// that is open on the old version can offer to reload (AppFrame). The frame
// names the app and its version only — apps are installed for the whole
// instance and every signed-in person's action list already names them.

// wireAppUpdated is the frame an open explorer receives.
type wireAppUpdated struct {
	Type    string `json:"type"` // always "app.updated"
	App     string `json:"app"`
	Version string `json:"version"`
}

// Connect registers c for instance-wide notices for as long as its socket
// is open. Unsubscribe (on disconnect) forgets it.
func (h *Hub) Connect(c *Client) {
	h.mu.Lock()
	if h.connected == nil {
		h.connected = make(map[*Client]struct{})
	}
	h.connected[c] = struct{}{}
	h.mu.Unlock()
}

// AppUpdated tells every connected client that app now runs at version.
// Non-blocking per client, like every other frame: a stalled reader misses
// it and loads the new version the next time it opens the app.
func (h *Hub) AppUpdated(app, version string) {
	frame, err := json.Marshal(wireAppUpdated{Type: "app.updated", App: app, Version: version})
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.connected {
		select {
		case c.Send <- frame:
		default:
		}
	}
}
