package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

// Through the real routes: the echo app declares two permissions of its own
// (request: default user, audit: default admin) and prints what it is told
// the person holds — on a screen (`perms=` in the wizard's text) and in a job
// (the facts action's message). The signing app draws its "…or Request
// signatures…" hint from exactly this (filex-sign docs/SIGN.md §20).
func TestAppPlugins_TheAppIsToldWhichOfItsPermissionsThePersonHolds(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEchoWith(t, func(m map[string]any) {
		m["user_permissions"] = []any{
			map[string]any{"id": "request", "label": map[string]any{"en": "Request", "tr": "İste"}, "default": "user"},
			map[string]any{"id": "audit", "label": map[string]any{"en": "Audit", "tr": "Denetim"}, "default": "admin"},
		}
	})
	f.writeFile(t, "docs/a.txt", "x")
	client, _, _ := f.loginRegular(t, "ada", model.GrantViewer)

	wizard := func(c *http.Client) string {
		t.Helper()
		status, raw := doReq(t, c, http.MethodGet, f.srv.URL+"/api/files/plugins/views/echo/wizard", nil)
		require.Equal(t, http.StatusOK, status, string(raw))
		var ans struct {
			Surface struct {
				Nodes []struct {
					Props struct {
						Text map[string]string `json:"text"`
					} `json:"props"`
				} `json:"nodes"`
			} `json:"surface"`
		}
		require.NoError(t, json.Unmarshal(raw, &ans))
		require.NotEmpty(t, ans.Surface.Nodes, string(raw))
		txt := ans.Surface.Nodes[0].Props.Text["en"]
		_, perms, ok := strings.Cut(txt, " perms=")
		require.True(t, ok, txt)
		return perms
	}
	facts := func(c *http.Client) string {
		t.Helper()
		status, raw := doReq(t, c, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/facts/run",
			map[string]any{"paths": []string{"main://docs/a.txt"}})
		require.Equal(t, http.StatusAccepted, status, string(raw))
		var ans struct {
			Op struct {
				ID int64 `json:"id"`
			} `json:"op"`
			JobID string `json:"job_id"`
		}
		require.NoError(t, json.Unmarshal(raw, &ans))
		op := f.drain(t, ans.Op.ID)
		require.Equal(t, "ok", op["status"], op)
		job, err := f.store.GetAppPluginJob(context.Background(), ans.JobID)
		require.NoError(t, err)
		return job.Message
	}

	assert.Equal(t, "request,audit", wizard(f.admin), "an administrator holds every one")
	assert.Equal(t, "request", wizard(client), "a user holds the user-default one")
	assert.Equal(t, "ro=false perms=request", facts(client), "a job is told the same")

	// The built-in User role takes it away: the next screen and the next job
	// say so.
	require.NoError(t, perm.SaveAppDecisions(context.Background(), f.store, model.RoleUser, map[string]string{"app.echo.request": model.PermDeny}))
	t.Cleanup(perm.Invalidate)
	assert.Equal(t, "", wizard(client))
	assert.Equal(t, "ro=false", facts(client))
	assert.Equal(t, "request,audit", wizard(f.admin))
}
