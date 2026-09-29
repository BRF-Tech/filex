package cliclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The CLI leaves a plugin request with its key and reads the answer; a
// refusal carries the server's sentence, not only its code.
func TestRequestPlugin(t *testing.T) {
	var got map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/admin/plugin-requests":
			_ = json.NewDecoder(r.Body).Decode(&got)
			if got["reason"] == "" || got["reason"] == nil {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"reason_required","message":"say why the plugin is needed"}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"request":{"id":7,"kind":"app","op":"install","name":"sign","version":"0.1.1",` +
				`"status":"pending","permissions":["files:read"],"sha256":"ab"},"created":true,"message":"waiting for an administrator"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/admin/plugin-requests":
			if r.URL.Query().Get("status") != "all" {
				t.Errorf("status = %q", r.URL.Query().Get("status"))
			}
			_, _ = w.Write([]byte(`{"requests":[{"id":7,"status":"pending","name":"sign"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(Conn{URL: srv.URL, Token: "filex_key"})

	res, err := c.RequestPlugin(context.Background(), PluginRequestInput{Kind: "app", GitHubRepo: "BRF-Tech/filex-sign", Ref: "v0.1.1", Reason: "sözleşmeler"})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer filex_key" {
		t.Errorf("the key travels as a bearer: %q", auth)
	}
	if got["github_repo"] != "BRF-Tech/filex-sign" || got["kind"] != "app" || got["reason"] != "sözleşmeler" {
		t.Errorf("body = %v", got)
	}
	if _, ok := got["sha256"]; ok {
		t.Errorf("empty fields are left out: %v", got)
	}
	if res.Request.ID != 7 || !res.Created || res.Request.Status != "pending" || res.Message == "" {
		t.Errorf("answer = %+v", res)
	}

	_, err = c.RequestPlugin(context.Background(), PluginRequestInput{Kind: "app", GitHubRepo: "x/y"})
	if err == nil || !strings.Contains(err.Error(), "say why the plugin is needed") {
		t.Errorf("a refusal says the server's sentence: %v", err)
	}

	list, _, err := c.PluginRequests(context.Background(), "all")
	if err != nil || len(list) != 1 || list[0].Name != "sign" {
		t.Errorf("list = %+v, %v", list, err)
	}
}
