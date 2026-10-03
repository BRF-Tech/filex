package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
)

// OnlyOffice configured by FILEX_ONLYOFFICE_URL / _JWT and switched off on
// External services: off for the editor right away, and the answer and the
// audit row say how long (until the next start writes the environment back)
// and how to switch it off for good (remove the variable).
//
// ⚠ Red before 0.50's fix: the editor kept answering 200 - the OnlyOffice
// service fell back to its boot-time values when the row said "off" - while
// capabilities and the card said it was off.
func TestExternalAdmin_SwitchingOffAnEnvPinnedOnlyOfficeIsOffAndSaysUntilWhen(t *testing.T) {
	h, nodeID := liveExternalServer(t, func(c *config.Config) {
		c.ExternalServices.OnlyOffice.URL = "https://docs.env"
		c.ExternalServices.OnlyOffice.JWTSecret = "env-secret"
	})
	ctx := context.Background()
	// The row as the boot writes it for a pinned service (server.seedExternalDefaults).
	require.NoError(t, h.Store.UpsertExternalService(ctx, "onlyoffice", true,
		"https://docs.env", "env-secret", "{}", time.Time{}, "unknown"))

	resp := h.Get(t, "/api/files/onlyoffice/config?id="+itoa(nodeID)+"&mode=edit")
	require.Equal(t, http.StatusOK, resp.StatusCode, "configured by the environment")
	resp.Body.Close()

	resp = h.Patch(t, "/api/admin/external/onlyoffice", map[string]any{"enabled": false})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out struct {
		EnvManaged bool   `json:"env_managed"`
		EnvVar     string `json:"env_var"`
		Note       string `json:"note"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	resp.Body.Close()
	require.True(t, out.EnvManaged)
	require.Equal(t, "FILEX_ONLYOFFICE_URL", out.EnvVar)
	require.Contains(t, out.Note, "until filex restarts")
	require.Contains(t, out.Note, "remove FILEX_ONLYOFFICE_URL")

	resp = h.Get(t, "/api/files/onlyoffice/config?id="+itoa(nodeID)+"&mode=edit")
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode,
		"switched off is off: the boot-time values do not answer instead")
	resp.Body.Close()

	// The audit row of the change carries the same words.
	require.Eventually(t, func() bool {
		rows, err := h.Store.ListAuditRecent(ctx, 50)
		if err != nil {
			return false
		}
		for _, r := range rows {
			if r.Action == "external.update" && r.Metadata["env_managed"] == true && r.Metadata["enabled"] == false {
				note, _ := r.Metadata["note"].(string)
				return note == out.Note
			}
		}
		return false
	}, 5*time.Second, 50*time.Millisecond, "an external.update audit row with env_managed, enabled=false and the note")
}
